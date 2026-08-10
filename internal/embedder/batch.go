package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Batch embedding support for the benchmark ingestion path
// (docs/v0.4/01-bench-infra.md). The vatbrain-bench entrypoint embeds an
// entire /v1/add batch concurrently instead of one text per HTTP request:
// a bounded worker pool feeds 64-text chunks to the provider's batch API
// (the Zhipu embedding-3 hard per-request limit, docs/v0.3/
// 05-agent-memory-handoff.md 【并发调研】), retrying rate-limit responses
// (429, 1302, 1305) with exponential backoff.

// zhipuEmbeddingBatchLimit is the Zhipu embedding-3 hard per-request text
// limit and the default batch size.
const zhipuEmbeddingBatchLimit = 64

// defaultBatchWorkers is the default concurrent embedding worker count,
// inside the researched 32–64 range.
const defaultBatchWorkers = 32

// maxBatchWorkers caps the worker pool (any account tier is at least V1 with
// 100 in-flight requests, so 64 stays comfortably under the limit).
const maxBatchWorkers = 64

// defaultEmbedMaxRetries bounds retries per chunk on rate-limit responses.
const defaultEmbedMaxRetries = 5

// defaultEmbedRetryBackoff is the initial backoff before the first retry; it
// doubles per attempt up to embedRetryBackoffCap.
const (
	defaultEmbedRetryBackoff = 250 * time.Millisecond
	embedRetryBackoffCap     = 8 * time.Second
)

// ErrBatchNotSupported reports that the wrapped embedder cannot embed many
// texts in one call. Callers (the bench server) fall back to per-message
// embedding when they see it.
var ErrBatchNotSupported = errors.New("embedder: batch embedding not supported")

// BatchCapable is implemented by embedders that can embed many texts in a
// single call: OpenAIProvider, KeywordEmbedder, DualChannelEmbedder, and
// BatchEmbedder itself. The benchmark ingestion path type-asserts it to
// enable the concurrent /v1/add phase; embedders without it (e.g. the Claude
// embedder) keep the sequential pipeline.
type BatchCapable interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// ── OpenAIProvider ────────────────────────────────────────────────────────────

// openAIEmbedBatchRequest is the batch form of the /embeddings request.
type openAIEmbedBatchRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// EmbedBatch embeds many texts in one or more HTTP requests. Texts are split
// into chunks of at most zhipuEmbeddingBatchLimit; each chunk is retried with
// exponential backoff on rate-limit responses (HTTP 429, or a JSON error body
// carrying Zhipu codes 1302/1305). Vectors are returned in input order. An
// empty input performs no request.
func (o *OpenAIProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	maxRetries := o.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultEmbedMaxRetries
	}
	backoff := o.RetryBackoff
	if backoff <= 0 {
		backoff = defaultEmbedRetryBackoff
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += zhipuEmbeddingBatchLimit {
		end := min(start+zhipuEmbeddingBatchLimit, len(texts))
		vecs, err := o.embedChunkWithRetry(ctx, texts[start:end], maxRetries, backoff)
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// embedChunkWithRetry embeds one chunk, retrying rate-limit responses up to
// maxRetries times with doubling backoff.
func (o *OpenAIProvider) embedChunkWithRetry(ctx context.Context, chunk []string, maxRetries int, backoff time.Duration) ([][]float32, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := backoff << (attempt - 1)
			if wait > embedRetryBackoffCap {
				wait = embedRetryBackoffCap
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, fmt.Errorf("openai embed batch: %w", ctx.Err())
			}
		}
		vecs, retryable, err := o.embedChunkOnce(ctx, chunk)
		if err == nil {
			return vecs, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, fmt.Errorf("openai embed batch: retries exhausted: %w", lastErr)
}

// embedChunkOnce performs one batch POST and reports whether a failed call is
// retryable (a rate-limit response: HTTP 429, or Zhipu codes 1302/1305 in the
// JSON error body).
func (o *OpenAIProvider) embedChunkOnce(ctx context.Context, chunk []string) ([][]float32, bool, error) {
	body, err := json.Marshal(openAIEmbedBatchRequest{Model: o.Model, Input: chunk})
	if err != nil {
		return nil, false, fmt.Errorf("openai embed batch: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("openai embed batch: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := o.HTTPClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("openai embed batch: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// best-effort: include the error body in the message when readable.
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		msg := fmt.Errorf("openai embed batch: status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(data)))
		return nil, isRateLimitResponse(resp.StatusCode, data), msg
	}

	var out openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false, fmt.Errorf("openai embed batch: decode: %w", err)
	}
	if len(out.Data) != len(chunk) {
		return nil, false, fmt.Errorf("openai embed batch: got %d vectors for %d texts",
			len(out.Data), len(chunk))
	}
	return reorderBatchVectors(out.Data, len(chunk)), false, nil
}

// reorderBatchVectors maps the provider's embedding data back to the input
// texts. OpenAI-compatible providers return data aligned with the input order,
// optionally carrying an index field; when every item carries a distinct valid
// index, the vectors are ordered by it (robust against out-of-order
// responses). Without usable indices, response order is trusted.
func reorderBatchVectors(data []openAIEmbedData, n int) [][]float32 {
	byIndex := make([]int, n)
	usable := len(data) == n
	seen := make([]bool, n)
	for i, d := range data {
		idx := d.Index
		if idx < 0 || idx >= n || seen[idx] {
			usable = false
			break
		}
		seen[idx] = true
		byIndex[idx] = i
	}

	vecs := make([][]float32, n)
	for out, src := range byIndex {
		if !usable {
			src = out
		}
		e := data[src].Embedding
		vecs[out] = make([]float32, len(e))
		for j, v := range e {
			vecs[out][j] = float32(v)
		}
	}
	return vecs
}

// isRateLimitResponse reports whether an error response is a retryable rate
// limit: HTTP 429, or a JSON error body carrying the Zhipu rate-limit codes
// 1302 (concurrency ceiling) or 1305 (overload).
func isRateLimitResponse(status int, body []byte) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	var out struct {
		Error struct {
			Code json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false
	}
	// error.code may be a JSON string ("1302") or a number (1302).
	var code string
	var s string
	if err := json.Unmarshal(out.Error.Code, &s); err == nil {
		code = s
	} else {
		var n float64
		if err := json.Unmarshal(out.Error.Code, &n); err == nil {
			code = strconv.Itoa(int(n))
		}
	}
	switch code {
	case "1302", "1305":
		return true
	}
	return false
}

// ── KeywordEmbedder ───────────────────────────────────────────────────────────

// EmbedBatch embeds every text with the same keyword channel Embed uses,
// preserving input order. The benchmark's concurrent ingestion path works
// without an external service, and tests exercise the pool with it.
func (k *KeywordEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := k.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		out[i] = vec
	}
	return out, nil
}

// ── DualChannelEmbedder ───────────────────────────────────────────────────────

// batchSemanticProvider is the optional batched form of SemanticProvider.
type batchSemanticProvider interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// EmbedBatch embeds many texts with the same dual-channel semantics as Embed:
// the semantic channel when it yields a vector with signal, the keyword
// channel per text otherwise (error or zero vector). When the semantic channel
// supports batching it is used in one call per chunk; otherwise each text
// takes the single-text path.
func (d *DualChannelEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))

	if sp, ok := d.Semantic.(batchSemanticProvider); ok {
		if vecs, err := sp.EmbedBatch(ctx, texts); err == nil && len(vecs) == len(texts) {
			for i, v := range vecs {
				if hasMagnitude(v) {
					out[i] = v
				}
			}
			for i, v := range out {
				if v == nil {
					// best-effort: the keyword channel cannot fail for
					// non-empty input (deterministic hashing).
					kv, _ := d.Keyword.Embed(ctx, texts[i])
					out[i] = kv
				}
			}
			return out, nil
		}
	}

	// Semantic batch unavailable or failed: per-text semantics identical to
	// Embed (semantic first, keyword fallback).
	for i, text := range texts {
		// best-effort: Embed falls back to the keyword channel, which cannot
		// fail for non-empty input.
		vec, _ := d.Embed(ctx, text)
		out[i] = vec
	}
	return out, nil
}

// ── BatchEmbedder (worker pool) ───────────────────────────────────────────────

// BatchOptions configures the concurrent batch embedder.
type BatchOptions struct {
	// Workers is the number of concurrent embedding workers. 0 uses the
	// default (32); values are clamped to [1, 64].
	Workers int
	// BatchSize is the maximum texts handed to the wrapped embedder per call.
	// 0 uses the default (64, the provider hard limit).
	BatchSize int
}

// BatchEmbedder wraps an Embedder and adds concurrent batch embedding: a
// bounded worker pool pulls batch-size chunks from a job queue and embeds
// them in parallel, preserving input order in the result. It is the
// concurrency machinery of the benchmark ingestion path; rate-limit retry
// stays with the provider that owns its error codes.
type BatchEmbedder struct {
	inner     Embedder
	workers   int
	batchSize int
}

// embedChunk is one batch job: texts[chunk.start:chunk.end].
type embedChunk struct {
	start int
	end   int
}

// NewBatchEmbedder wraps inner with a worker pool. Workers and batch size
// come from opts with the documented defaults and clamps.
func NewBatchEmbedder(inner Embedder, opts BatchOptions) *BatchEmbedder {
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultBatchWorkers
	}
	if workers > maxBatchWorkers {
		workers = maxBatchWorkers
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = zhipuEmbeddingBatchLimit
	}
	if batchSize > zhipuEmbeddingBatchLimit {
		batchSize = zhipuEmbeddingBatchLimit
	}
	return &BatchEmbedder{inner: inner, workers: workers, batchSize: batchSize}
}

// Embed delegates to the wrapped embedder (single-text path, unchanged).
func (b *BatchEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return b.inner.Embed(ctx, text)
}

// EmbedBatch embeds texts concurrently: chunks of batchSize are dispatched to
// up to workers goroutines and the vectors are reassembled in input order. It
// fails fast: the first chunk error cancels the remaining jobs and is
// returned, discarding partial results. Returns ErrBatchNotSupported when the
// wrapped embedder has no batch capability.
func (b *BatchEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	inner, ok := b.inner.(BatchCapable)
	if !ok {
		return nil, ErrBatchNotSupported
	}
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	out := make([][]float32, len(texts))
	chunks := (len(texts) + b.batchSize - 1) / b.batchSize
	workers := min(b.workers, chunks)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan embedChunk, chunks)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				vecs, err := inner.EmbedBatch(ctx, texts[j.start:j.end])
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
					return
				}
				if len(vecs) != j.end-j.start {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("embedder: batch returned %d vectors for %d texts",
							len(vecs), j.end-j.start)
					}
					mu.Unlock()
					cancel()
					return
				}
				copy(out[j.start:j.end], vecs)
			}
		}()
	}

	go func() {
		defer close(jobs)
		for start := 0; start < len(texts); start += b.batchSize {
			select {
			case jobs <- embedChunk{start: start, end: min(start+b.batchSize, len(texts))}:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

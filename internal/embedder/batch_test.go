package embedder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── OpenAIProvider.EmbedBatch ─────────────────────────────────────────────────

// embedIndexHandler builds a handler that echoes each input text as the vector
// [index, 0, 0, ...] where index is the trailing integer of the text
// ("mem-000042" → 42), in input order, so tests can assert batching, order, and
// retry behavior across requests. Vectors have dimension 3. fn, when non-nil,
// runs after the request is decoded with the decoded input length.
func embedIndexHandler(t *testing.T, fn func(n int)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if fn != nil {
			fn(len(req.Input))
		}
		data := make([]map[string]any, 0, len(req.Input))
		for _, text := range req.Input {
			n, _ := strconv.Atoi(text[strings.LastIndex(text, "-")+1:])
			data = append(data, map[string]any{
				"embedding": []float64{float64(n), 0, 0},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func TestOpenAIProvider_EmbedBatch_OrderAndShape(t *testing.T) {
	ts := embedIndexHandler(t, nil)
	defer ts.Close()

	p := &OpenAIProvider{
		BaseURL:    ts.URL,
		Model:      "embedding-3",
		HTTPClient: ts.Client(),
	}

	texts := []string{"mem-000003", "mem-000001", "mem-000002"}
	vecs, err := p.EmbedBatch(context.Background(), texts)
	require.NoError(t, err)
	require.Len(t, vecs, 3)

	// Input order must be preserved in the response vectors.
	assert.Equal(t, []float32{3, 0, 0}, vecs[0])
	assert.Equal(t, []float32{1, 0, 0}, vecs[1])
	assert.Equal(t, []float32{2, 0, 0}, vecs[2])
}

func TestOpenAIProvider_EmbedBatch_ChunksAtHardLimit(t *testing.T) {
	var requestSizes []int
	var mu sync.Mutex
	ts := embedIndexHandler(t, func(n int) {
		mu.Lock()
		requestSizes = append(requestSizes, n)
		mu.Unlock()
	})
	defer ts.Close()

	p := &OpenAIProvider{
		BaseURL:    ts.URL,
		Model:      "embedding-3",
		HTTPClient: ts.Client(),
	}

	texts := make([]string, 130)
	for i := range texts {
		texts[i] = fmt.Sprintf("mem-%06d", i)
	}
	vecs, err := p.EmbedBatch(context.Background(), texts)
	require.NoError(t, err)
	require.Len(t, vecs, 130)

	// 130 texts split into requests of at most 64: 64 + 64 + 2.
	mu.Lock()
	assert.Equal(t, []int{64, 64, 2}, requestSizes)
	mu.Unlock()

	// Order preserved across chunks: vec[i][0] == i.
	for i, v := range vecs {
		assert.Equal(t, float32(i), v[0], "vector %d out of order", i)
	}
}

func TestOpenAIProvider_EmbedBatch_RetriesOn429(t *testing.T) {
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"embedding": []float64{7, 0, 0}},
		}})
	}))
	defer ts.Close()

	p := &OpenAIProvider{
		BaseURL:      ts.URL,
		Model:        "embedding-3",
		HTTPClient:   ts.Client(),
		MaxRetries:   4,
		RetryBackoff: time.Millisecond,
	}
	vecs, err := p.EmbedBatch(context.Background(), []string{"mem-000007"})
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{7, 0, 0}}, vecs)
	assert.Equal(t, int32(3), n.Load(), "two 429s must be retried, third attempt succeeds")
}

func TestOpenAIProvider_EmbedBatch_RetriesOnRateLimitCodes(t *testing.T) {
	cases := []struct {
		name string
		body string // response body for the first 429, "" = plain 429
	}{
		{name: "plain-429", body: ""},
		{name: "zhipu-1302-string", body: `{"error":{"code":"1302","message":"concurrency limit"}}`},
		{name: "zhipu-1305-string", body: `{"error":{"code":"1305","message":"overloaded"}}`},
		{name: "zhipu-1302-numeric", body: `{"error":{"code":1302,"message":"concurrency limit"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if n.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusTooManyRequests)
					if tc.body != "" {
						w.Write([]byte(tc.body))
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
					{"embedding": []float64{1, 2, 3}},
				}})
			}))
			defer ts.Close()

			p := &OpenAIProvider{
				BaseURL:      ts.URL,
				Model:        "embedding-3",
				HTTPClient:   ts.Client(),
				MaxRetries:   3,
				RetryBackoff: time.Millisecond,
			}
			vecs, err := p.EmbedBatch(context.Background(), []string{"mem-000001"})
			require.NoError(t, err)
			assert.Equal(t, [][]float32{{1, 2, 3}}, vecs)
			assert.Equal(t, int32(2), n.Load(), "rate-limit response must be retried exactly once")
		})
	}
}

func TestOpenAIProvider_EmbedBatch_RetryExhausted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	p := &OpenAIProvider{
		BaseURL:      ts.URL,
		Model:        "embedding-3",
		HTTPClient:   ts.Client(),
		MaxRetries:   2,
		RetryBackoff: time.Millisecond,
	}
	_, err := p.EmbedBatch(context.Background(), []string{"mem-000001"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retries exhausted")
}

func TestOpenAIProvider_EmbedBatch_NoRetryOnServerError(t *testing.T) {
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	p := &OpenAIProvider{
		BaseURL:      ts.URL,
		Model:        "embedding-3",
		HTTPClient:   ts.Client(),
		MaxRetries:   3,
		RetryBackoff: time.Millisecond,
	}
	_, err := p.EmbedBatch(context.Background(), []string{"mem-000001"})
	require.Error(t, err)
	assert.Equal(t, int32(1), n.Load(), "a 500 must not be retried")
}

func TestOpenAIProvider_EmbedBatch_EmptyInput(t *testing.T) {
	var n atomic.Int32
	ts := embedIndexHandler(t, func(i int) { n.Add(1) })
	defer ts.Close()

	p := &OpenAIProvider{BaseURL: ts.URL, Model: "embedding-3", HTTPClient: ts.Client()}
	vecs, err := p.EmbedBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, vecs)
	assert.Equal(t, int32(0), n.Load(), "no request must be sent for empty input")
}

func TestOpenAIProvider_EmbedBatch_ContextCancelled(t *testing.T) {
	ts := embedIndexHandler(t, nil)
	defer ts.Close()

	p := &OpenAIProvider{BaseURL: ts.URL, Model: "embedding-3", HTTPClient: ts.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.EmbedBatch(ctx, []string{"mem-000001"})
	require.Error(t, err)
}

func TestOpenAIProvider_EmbedBatch_DataLengthMismatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"embedding": []float64{1, 2, 3}},
		}})
	}))
	defer ts.Close()

	p := &OpenAIProvider{BaseURL: ts.URL, Model: "embedding-3", HTTPClient: ts.Client()}
	_, err := p.EmbedBatch(context.Background(), []string{"mem-000001", "mem-000002"})
	require.Error(t, err, "2 texts but 1 vector must be an error")
}

// ── KeywordEmbedder.EmbedBatch ────────────────────────────────────────────────

func TestKeywordEmbedder_EmbedBatch_MatchesEmbed(t *testing.T) {
	k := NewKeywordEmbedder(16)
	texts := []string{"Alice loves hiking", "中文记忆", "", "sourdough bread"}

	got, err := k.EmbedBatch(context.Background(), texts)
	require.NoError(t, err)
	require.Len(t, got, len(texts))

	for i, text := range texts {
		want, err := k.Embed(context.Background(), text)
		require.NoError(t, err)
		assert.Equal(t, want, got[i], "EmbedBatch text %d must equal Embed", i)
	}
}

// ── DualChannelEmbedder.EmbedBatch ────────────────────────────────────────────

type failingSemantic struct{}

func (f *failingSemantic) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("semantic down")
}

func (f *failingSemantic) EmbedBatch(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("semantic down")
}

type zeroSemantic struct{}

func (z *zeroSemantic) Embed(context.Context, string) ([]float32, error) {
	return []float32{0, 0, 0}, nil
}

func (z *zeroSemantic) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{0, 0, 0}
	}
	return out, nil
}

func TestDualChannelEmbedder_EmbedBatch_FallsBackToKeywordPerText(t *testing.T) {
	// Semantic channel down: every text must get the keyword vector, exactly
	// like the single-text Embed path.
	d := NewDualChannelEmbedder(&failingSemantic{})
	texts := []string{"Alice loves hiking", "中文记忆"}

	got, err := d.EmbedBatch(context.Background(), texts)
	require.NoError(t, err)
	for i, text := range texts {
		want, err := d.Keyword.Embed(context.Background(), text)
		require.NoError(t, err)
		assert.Equal(t, want, got[i])
	}
}

func TestDualChannelEmbedder_EmbedBatch_ZeroVectorFallsBack(t *testing.T) {
	// Semantic returns zero vectors: the dual channel must replace them with
	// keyword vectors so the pipeline never degrades to zero-vector silence.
	d := NewDualChannelEmbedder(&zeroSemantic{})
	got, err := d.EmbedBatch(context.Background(), []string{"hello world"})
	require.NoError(t, err)
	want, err := d.Keyword.Embed(context.Background(), "hello world")
	require.NoError(t, err)
	assert.Equal(t, want, got[0])
}

func TestDualChannelEmbedder_EmbedBatch_SemanticBatchUsedWhenAvailable(t *testing.T) {
	ts := embedIndexHandler(t, nil)
	defer ts.Close()

	d := NewDualChannelEmbedder(&OpenAIProvider{
		BaseURL:    ts.URL,
		Model:      "embedding-3",
		HTTPClient: ts.Client(),
	})
	got, err := d.EmbedBatch(context.Background(), []string{"mem-000007", "mem-000009"})
	require.NoError(t, err)
	assert.Equal(t, []float32{7, 0, 0}, got[0])
	assert.Equal(t, []float32{9, 0, 0}, got[1])
}

// ── BatchEmbedder (worker pool) ───────────────────────────────────────────────

// fakeBatchEmbedder is a batch-capable embedder whose vectors are derived from
// the text length (distinct for distinct-length inputs) and which reports the
// number of calls and the maximum number of concurrent EmbedBatch calls.
type fakeBatchEmbedder struct {
	delay         time.Duration
	fail          bool
	calls         atomic.Int32
	current       atomic.Int32
	maxConcurrent atomic.Int32
}

func (f *fakeBatchEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := f.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (f *fakeBatchEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	f.calls.Add(1)
	n := f.current.Add(1)
	for {
		prev := f.maxConcurrent.Load()
		if n <= prev || f.maxConcurrent.CompareAndSwap(prev, n) {
			break
		}
	}
	defer f.current.Add(-1)

	if f.fail {
		return nil, errors.New("fake batch embedder failure")
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = []float32{float32(len(t))}
	}
	return out, nil
}

func TestBatchEmbedder_ChunksByBatchSize_OrderPreserved(t *testing.T) {
	fake := &fakeBatchEmbedder{}
	b := NewBatchEmbedder(fake, BatchOptions{Workers: 2, BatchSize: 2})

	// Lengths 1..7 are distinct, so reassembly order is verifiable.
	texts := []string{"a", "bb", "ccc", "dddd", "eeeee", "ffffff", "ggggggg"}
	vecs, err := b.EmbedBatch(context.Background(), texts)
	require.NoError(t, err)
	require.Len(t, vecs, len(texts))
	for i, text := range texts {
		assert.Equal(t, []float32{float32(len(text))}, vecs[i],
			"vector %d (%q) must keep its input position", i, text)
	}
	// 7 texts at batchSize 2 → 4 inner calls.
	assert.Equal(t, int32(4), fake.calls.Load())
}

func TestBatchEmbedder_WorkersBoundConcurrency(t *testing.T) {
	fake := &fakeBatchEmbedder{delay: 10 * time.Millisecond}
	b := NewBatchEmbedder(fake, BatchOptions{Workers: 3, BatchSize: 1})

	texts := make([]string, 12)
	for i := range texts {
		texts[i] = strings.Repeat("x", i+1)
	}
	_, err := b.EmbedBatch(context.Background(), texts)
	require.NoError(t, err)
	assert.LessOrEqual(t, fake.maxConcurrent.Load(), int32(3),
		"worker pool must not exceed the configured worker count")
	assert.GreaterOrEqual(t, fake.maxConcurrent.Load(), int32(2),
		"with 12 chunks and 3 workers at least 2 calls must run concurrently")

	// Single worker: strictly sequential.
	fake2 := &fakeBatchEmbedder{delay: 5 * time.Millisecond}
	b1 := NewBatchEmbedder(fake2, BatchOptions{Workers: 1, BatchSize: 1})
	_, err = b1.EmbedBatch(context.Background(), texts)
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake2.maxConcurrent.Load())
}

func TestBatchEmbedder_WorkerError_Propagates(t *testing.T) {
	fake := &fakeBatchEmbedder{fail: true}
	b := NewBatchEmbedder(fake, BatchOptions{Workers: 4, BatchSize: 2})

	_, err := b.EmbedBatch(context.Background(), []string{"a", "bb", "ccc", "dddd"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fake batch embedder failure")
}

func TestBatchEmbedder_NotBatchCapable_ReturnsSentinel(t *testing.T) {
	stub := NewStubEmbedder() // Embed only, no EmbedBatch
	b := NewBatchEmbedder(stub, BatchOptions{})

	_, err := b.EmbedBatch(context.Background(), []string{"a", "b"})
	assert.ErrorIs(t, err, ErrBatchNotSupported)
}

func TestBatchEmbedder_Embed_Delegates(t *testing.T) {
	k := NewKeywordEmbedder(16)
	b := NewBatchEmbedder(k, BatchOptions{Workers: 2})

	got, err := b.Embed(context.Background(), "hello world")
	require.NoError(t, err)
	want, err := k.Embed(context.Background(), "hello world")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestBatchEmbedder_EmptyInput(t *testing.T) {
	fake := &fakeBatchEmbedder{}
	b := NewBatchEmbedder(fake, BatchOptions{Workers: 2})

	vecs, err := b.EmbedBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, vecs)
	assert.Equal(t, int32(0), fake.calls.Load(), "no worker must run for empty input")
}

func TestBatchEmbedder_DefaultOptions(t *testing.T) {
	b := NewBatchEmbedder(&fakeBatchEmbedder{}, BatchOptions{})
	assert.Equal(t, 32, b.workers, "default worker pool size")
	assert.Equal(t, 64, b.batchSize, "default batch size")

	// Workers are clamped to the 32–64 range.
	b2 := NewBatchEmbedder(&fakeBatchEmbedder{}, BatchOptions{Workers: 1000})
	assert.Equal(t, 64, b2.workers)
	b3 := NewBatchEmbedder(&fakeBatchEmbedder{}, BatchOptions{Workers: -5})
	assert.Equal(t, 32, b3.workers)
}

func TestOpenAIProvider_EmbedBatch_OrdersByIndexField(t *testing.T) {
	// A provider may return vectors out of order with an index field per
	// item; the vectors must be mapped back to their input positions.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, 0, len(req.Input))
		// Emit in reverse order. Each item's vector encodes its text and its
		// index field tags the true input position, so the client must
		// reorder by index to restore input order.
		for i := len(req.Input) - 1; i >= 0; i-- {
			n, _ := strconv.Atoi(req.Input[i][strings.LastIndex(req.Input[i], "-")+1:])
			data = append(data, map[string]any{
				"index":     i,
				"embedding": []float64{float64(n), 0, 0},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer ts.Close()

	p := &OpenAIProvider{BaseURL: ts.URL, Model: "embedding-3", HTTPClient: ts.Client()}
	vecs, err := p.EmbedBatch(context.Background(), []string{"mem-000003", "mem-000001", "mem-000002"})
	require.NoError(t, err)
	require.Len(t, vecs, 3)
	assert.Equal(t, []float32{3, 0, 0}, vecs[0], "input 0 must get the vector tagged index 0")
	assert.Equal(t, []float32{1, 0, 0}, vecs[1])
	assert.Equal(t, []float32{2, 0, 0}, vecs[2])
}

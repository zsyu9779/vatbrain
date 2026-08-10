package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SemanticProvider is the semantic channel of the dual-channel architecture:
// an external or local embedding service producing dense vectors.
type SemanticProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// OpenAIProvider talks to any OpenAI-compatible /embeddings endpoint
// (OpenAI, Voyage, Zhipu, local vLLM/llama.cpp servers).
type OpenAIProvider struct {
	APIKey     string
	BaseURL    string // e.g. https://api.openai.com/v1 or https://api.voyageai.com/v1
	Model      string
	HTTPClient *http.Client
	// MaxRetries bounds retries per chunk on rate-limit responses (HTTP 429
	// or Zhipu error codes 1302/1305). 0 uses the default (5). Applies to
	// EmbedBatch only; the single-text Embed keeps its original one-shot
	// behavior.
	MaxRetries int
	// RetryBackoff is the initial backoff before the first retry; it doubles
	// per attempt up to 8s. 0 uses the default (250ms). Applies to EmbedBatch
	// only.
	RetryBackoff time.Duration
}

// NewOpenAIProvider creates an OpenAI-compatible semantic provider.
func NewOpenAIProvider(apiKey, baseURL, model string) *OpenAIProvider {
	return &OpenAIProvider{
		APIKey:     apiKey,
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Model:      model,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type openAIEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []openAIEmbedData `json:"data"`
}

// openAIEmbedData is one item of the /embeddings data array. Index, when
// present, is the input position the vector belongs to; providers that return
// items out of order rely on it.
type openAIEmbedData struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

// Embed calls the /embeddings endpoint and returns the first vector.
func (o *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(openAIEmbedRequest{Model: o.Model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("openai embed: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai embed: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := o.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embed: do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("openai embed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var out openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai embed: decode: %w", err)
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("openai embed: empty embedding")
	}
	f32 := make([]float32, len(out.Data[0].Embedding))
	for i, v := range out.Data[0].Embedding {
		f32[i] = float32(v)
	}
	return f32, nil
}

// LocalProvider is a network-free semantic provider backed by the keyword
// embedder — the fallback semantic channel when no external service is
// configured.
type LocalProvider struct {
	Keyword *KeywordEmbedder
}

// NewLocalProvider creates a local semantic provider.
func NewLocalProvider() *LocalProvider {
	return &LocalProvider{Keyword: NewKeywordEmbedder(0)}
}

// Embed delegates to the keyword channel.
func (l *LocalProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return l.Keyword.Embed(ctx, text)
}

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

// ClaudeEmbedder generates embeddings via the Claude Embeddings API.
type ClaudeEmbedder struct {
	APIKey     string
	BaseURL    string
	Model      string // default "claude-text-embedding-3-small"
	HTTPClient *http.Client
}

// NewClaudeEmbedder returns a ClaudeEmbedder with sensible defaults.
func NewClaudeEmbedder(apiKey, baseURL, model string) *ClaudeEmbedder {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	if model == "" {
		model = "claude-text-embedding-3-small"
	}
	return &ClaudeEmbedder{
		APIKey:     apiKey,
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Model:      model,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type embeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embeddingResponse struct {
	Data  []embeddingData `json:"data"`
	Error *apiError       `json:"error,omitempty"`
}

type embeddingData struct {
	Embedding []float32 `json:"embedding"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Embed generates a dense vector for the given text using the Claude Embeddings API.
func (c *ClaudeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	url := c.BaseURL + "/v1/embeddings"

	reqBody := embeddingRequest{
		Model: c.Model,
		Input: text,
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("claude embedder: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("claude embedder: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude embedder: http do: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("claude embedder: read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("claude embedder: api %d: %s", resp.StatusCode, string(respBody))
	}

	var er embeddingResponse
	if err := json.Unmarshal(respBody, &er); err != nil {
		return nil, fmt.Errorf("claude embedder: unmarshal: %w", err)
	}
	if er.Error != nil {
		return nil, fmt.Errorf("claude embedder: api error: %s", er.Error.Message)
	}
	if len(er.Data) == 0 {
		return nil, fmt.Errorf("claude embedder: no embedding returned")
	}

	return er.Data[0].Embedding, nil
}

package embedder

import (
	"context"
	"fmt"
)

// ClaudeEmbedder is a skeleton for calling the Claude API to generate
// embeddings. The Embed method returns an error for v0.1 — wire in a
// real implementation when an API key and endpoint are available.
type ClaudeEmbedder struct {
	APIKey  string
	BaseURL string
}

// NewClaudeEmbedder returns a ClaudeEmbedder skeleton.
func NewClaudeEmbedder(apiKey, baseURL string) *ClaudeEmbedder {
	return &ClaudeEmbedder{APIKey: apiKey, BaseURL: baseURL}
}

// Embed is not yet implemented for v0.1.
func (c *ClaudeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, fmt.Errorf("claude embedder: not yet implemented")
}

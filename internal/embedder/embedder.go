// Package embedder provides an abstraction for generating vector embeddings
// from text. It is an external-integration concern, not a memory algorithm.
package embedder

import "context"

// Embedder generates a dense vector representation for a given text.
// Implementations may call external services (Claude API, OpenAI, local models).
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

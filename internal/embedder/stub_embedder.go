package embedder

import (
	"context"

	"github.com/vatbrain/vatbrain/internal/models"
)

// StubEmbedder returns a zero vector of DefaultEmbeddingDim length.
// It exists so the full write/search pipeline can be exercised without a
// real embedding service.
type StubEmbedder struct {
	Dim int
}

// NewStubEmbedder creates a StubEmbedder with the default embedding dimension.
func NewStubEmbedder() *StubEmbedder {
	return &StubEmbedder{Dim: models.DefaultEmbeddingDim}
}

// Embed returns a zero-valued embedding of length Dim.
func (s *StubEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, s.Dim), nil
}

package embedder

import (
	"context"
)

// DualChannelEmbedder combines the keyword channel (local, always available,
// CJK-safe) with a semantic channel (external/local provider). Embed returns
// the semantic vector when it carries a signal; otherwise the keyword vector,
// so the pipeline never degrades to zero-vector silence.
type DualChannelEmbedder struct {
	Keyword  *KeywordEmbedder
	Semantic SemanticProvider
}

// NewDualChannelEmbedder wires a keyword channel with a semantic provider.
func NewDualChannelEmbedder(semantic SemanticProvider) *DualChannelEmbedder {
	if semantic == nil {
		semantic = NewLocalProvider()
	}
	return &DualChannelEmbedder{
		Keyword:  NewKeywordEmbedder(0),
		Semantic: semantic,
	}
}

// Embed prefers the semantic channel; falls back to the keyword vector when
// the semantic call fails or yields a zero vector.
func (d *DualChannelEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if d.Semantic != nil {
		if vec, err := d.Semantic.Embed(ctx, text); err == nil && hasMagnitude(vec) {
			return vec, nil
		}
	}
	return d.Keyword.Embed(ctx, text)
}

// hasMagnitude reports whether v has any non-zero component.
func hasMagnitude(v []float32) bool {
	for _, c := range v {
		if c != 0 {
			return true
		}
	}
	return false
}

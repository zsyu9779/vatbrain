package core

import (
	"context"

	"github.com/vatbrain/vatbrain/internal/embedder"
)

// runeEmbedder is a deterministic, character-overlap embedder used by the CJK
// regression tests: texts sharing many runes yield high cosine similarity.
// It stands in for a real embedding service so the tests are hermetic and
// independent of any external API.
type runeEmbedder struct{}

// runeEmbedderDim is the vector width of runeEmbedder (256 buckets).
const runeEmbedderDim = 256

// Embed encodes the character multiset of text into a fixed-width vector:
// vec[int(r)%dim] holds the count of rune r. Cosine similarity between two
// such vectors approximates character overlap.
func (runeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, runeEmbedderDim)
	for _, r := range text {
		vec[int(r)%runeEmbedderDim]++
	}
	return vec, nil
}

var _ embedder.Embedder = runeEmbedder{}

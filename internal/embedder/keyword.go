package embedder

import (
	"context"
	"math"
)

// KeywordEmbedder is the keyword channel of the dual-channel architecture
// (docs/v0.1/tech-specs/01-embedder-architecture.md): a deterministic,
// CJK-safe text-heuristic pseudo-vector embedder. Same text → same vector;
// similar text → similar cosine. It needs no external service, so retrieval
// and clustering keep working without an API key — unlike the zero-vector
// StubEmbedder, whose vectors carry no semantic signal.
type KeywordEmbedder struct {
	Dim int
}

// NewKeywordEmbedder creates a keyword embedder with the given dimension.
func NewKeywordEmbedder(dim int) *KeywordEmbedder {
	if dim <= 0 {
		dim = defaultDim()
	}
	return &KeywordEmbedder{Dim: dim}
}

// Embed maps text to a deterministic unit vector via char-bigram feature
// hashing with sign preservation. CJK-safe: bigrams capture Chinese and
// Latin alike.
func (k *KeywordEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, k.Dim)
	for _, bg := range charBigrams(text) {
		idx := hashString(bg) % uint64(k.Dim)
		// Sign-preserving: same bigram always contributes the same direction.
		sign := float32(1)
		if hashString("s"+bg)&1 == 1 {
			sign = -1
		}
		vec[idx] += sign
	}
	// Normalize to unit length (zero vector for empty input).
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return vec, nil
	}
	n := float32(math.Sqrt(norm))
	for i := range vec {
		vec[i] /= n
	}
	return vec, nil
}

// charBigrams builds the character-bigram list of s — the write-path
// hashing form of the keyword channel. It is deliberately case-sensitive and
// order-preserving: KeywordEmbedder.Embed vectors are persisted, so this
// form must stay byte-stable across versions. Query-time scoring uses the
// exported CharBigrams (set form, ASCII case-folded) instead.
func charBigrams(s string) []string {
	r := []rune(s)
	if len(r) == 0 {
		return nil
	}
	if len(r) == 1 {
		return []string{string(r[0])}
	}
	out := make([]string, 0, len(r)-1)
	for i := 0; i+1 < len(r); i++ {
		out = append(out, string(r[i:i+2]))
	}
	return out
}

// hashString is FNV-1a over the string, returning a stable uint64.
func hashString(s string) uint64 {
	h := uint64(14695981039346656037)
	for _, b := range []byte(s) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return h
}

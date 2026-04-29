// Package vector provides zero-dependency vector math utilities for in-process
// embedding operations: cosine similarity, dot product, top-K selection, and
// binary encoding/decoding of float64 slices.
package vector

import (
	"encoding/binary"
	"math"
	"sort"
)

// CosineSimilarity returns the cosine similarity between two float64 slices.
// Returns 0 when either slice is empty or the slices have different lengths,
// or when either vector has zero magnitude.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// DotProduct returns the dot product of two float64 slices.
// Returns 0 when slices are empty or have different lengths.
func DotProduct(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}

// TopK returns the indices of the top-K highest scores in descending order.
// If k > len(scores), returns all indices. Ties are broken by index order.
func TopK(scores []float64, k int) []int {
	if k <= 0 || len(scores) == 0 {
		return nil
	}
	type indexed struct {
		idx   int
		score float64
	}
	items := make([]indexed, len(scores))
	for i, s := range scores {
		items[i] = indexed{idx: i, score: s}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		return items[i].idx < items[j].idx
	})
	if k > len(items) {
		k = len(items)
	}
	result := make([]int, k)
	for i := range k {
		result[i] = items[i].idx
	}
	return result
}

// Encode serializes a []float64 to a binary blob using little-endian encoding.
// Each float64 occupies 8 bytes.
func Encode(v []float64) []byte {
	if len(v) == 0 {
		return nil
	}
	b := make([]byte, len(v)*8)
	for i, f := range v {
		binary.LittleEndian.PutUint64(b[i*8:], math.Float64bits(f))
	}
	return b
}

// Decode deserializes a binary blob back to a []float64.
// Returns nil if len(b) is not a multiple of 8.
func Decode(b []byte) []float64 {
	if len(b)%8 != 0 {
		return nil
	}
	n := len(b) / 8
	if n == 0 {
		return nil
	}
	v := make([]float64, n)
	for i := range n {
		v[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[i*8:]))
	}
	return v
}

// Float32To64 converts a []float32 to []float64.
func Float32To64(v []float32) []float64 {
	if len(v) == 0 {
		return nil
	}
	out := make([]float64, len(v))
	for i, f := range v {
		out[i] = float64(f)
	}
	return out
}

// Float64To32 converts a []float64 to []float32.
func Float64To32(v []float64) []float32 {
	if len(v) == 0 {
		return nil
	}
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = float32(f)
	}
	return out
}

// L2Norm returns the Euclidean norm (L2 norm) of the vector.
func L2Norm(v []float64) float64 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	return math.Sqrt(sum)
}

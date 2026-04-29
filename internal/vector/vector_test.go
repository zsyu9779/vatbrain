package vector

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCosineSimilarity_Identical(t *testing.T) {
	v := []float64{1, 2, 3}
	assert.InDelta(t, 1.0, CosineSimilarity(v, v), 1e-9)
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{0, 1, 0}
	assert.InDelta(t, 0.0, CosineSimilarity(a, b), 1e-9)
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{-1, -2, -3}
	assert.InDelta(t, -1.0, CosineSimilarity(a, b), 1e-9)
}

func TestCosineSimilarity_Partial(t *testing.T) {
	a := []float64{1, 0}
	b := []float64{1, 1}
	// cos = 1 / (1 * sqrt(2)) = 0.7071...
	assert.InDelta(t, 0.7071, CosineSimilarity(a, b), 0.001)
}

func TestCosineSimilarity_Empty(t *testing.T) {
	assert.Equal(t, 0.0, CosineSimilarity(nil, []float64{1}))
	assert.Equal(t, 0.0, CosineSimilarity([]float64{}, []float64{1}))
	assert.Equal(t, 0.0, CosineSimilarity([]float64{1}, nil))
}

func TestCosineSimilarity_MismatchedLengths(t *testing.T) {
	assert.Equal(t, 0.0, CosineSimilarity([]float64{1, 2}, []float64{1}))
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	assert.Equal(t, 0.0, CosineSimilarity([]float64{0, 0}, []float64{1, 2}))
	assert.Equal(t, 0.0, CosineSimilarity([]float64{1, 2}, []float64{0, 0}))
}

func TestDotProduct(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{4, 5, 6}
	assert.InDelta(t, 32.0, DotProduct(a, b), 1e-9)
}

func TestDotProduct_Empty(t *testing.T) {
	assert.Equal(t, 0.0, DotProduct(nil, []float64{1}))
	assert.Equal(t, 0.0, DotProduct([]float64{1}, []float64{2, 3}))
}

func TestTopK(t *testing.T) {
	scores := []float64{0.1, 0.9, 0.5, 0.3, 0.8}
	got := TopK(scores, 3)
	assert.Equal(t, []int{1, 4, 2}, got)
}

func TestTopK_KGreaterThanLen(t *testing.T) {
	scores := []float64{0.5, 0.2}
	got := TopK(scores, 10)
	assert.Len(t, got, 2)
}

func TestTopK_Empty(t *testing.T) {
	assert.Nil(t, TopK(nil, 3))
	assert.Nil(t, TopK([]float64{}, 3))
}

func TestTopK_KZero(t *testing.T) {
	assert.Nil(t, TopK([]float64{0.5}, 0))
}

func TestTopK_TiesBrokenByIndex(t *testing.T) {
	scores := []float64{0.5, 0.5, 0.5}
	got := TopK(scores, 3)
	assert.Equal(t, []int{0, 1, 2}, got)
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	orig := []float64{1.5, -2.5, 0.0, math.Pi}
	enc := Encode(orig)
	dec := Decode(enc)
	assert.Len(t, dec, len(orig))
	for i := range orig {
		assert.InDelta(t, orig[i], dec[i], 1e-9)
	}
}

func TestEncode_Empty(t *testing.T) {
	assert.Nil(t, Encode(nil))
	assert.Nil(t, Encode([]float64{}))
}

func TestDecode_BadLength(t *testing.T) {
	assert.Nil(t, Decode([]byte{1, 2, 3}))
	assert.Nil(t, Decode(nil))
}

func TestFloat32To64_RoundTrip(t *testing.T) {
	orig := []float32{1.5, -2.5, 0.0}
	f64 := Float32To64(orig)
	f32 := Float64To32(f64)
	assert.Equal(t, orig, f32)
}

func TestFloat32To64_Empty(t *testing.T) {
	assert.Nil(t, Float32To64(nil))
	assert.Nil(t, Float32To64([]float32{}))
}

func TestFloat64To32_Empty(t *testing.T) {
	assert.Nil(t, Float64To32(nil))
	assert.Nil(t, Float64To32([]float64{}))
}

func TestL2Norm(t *testing.T) {
	assert.InDelta(t, 5.0, L2Norm([]float64{3, 4}), 1e-9)
	assert.InDelta(t, 0.0, L2Norm([]float64{0, 0, 0}), 1e-9)
	assert.InDelta(t, 0.0, L2Norm(nil), 1e-9)
}

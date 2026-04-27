package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCosineSimilarity_IdenticalVectors(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	sim := cosineSimilarity(a, b)
	assert.InDelta(t, 1.0, sim, 0.001)
}

func TestCosineSimilarity_OrthogonalVectors(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	sim := cosineSimilarity(a, b)
	assert.InDelta(t, 0.0, sim, 0.001)
}

func TestCosineSimilarity_OppositeVectors(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{-1, 0}
	sim := cosineSimilarity(a, b)
	assert.InDelta(t, -1.0, sim, 0.001)
}

func TestCosineSimilarity_EmptyVectors(t *testing.T) {
	assert.Equal(t, 0.0, cosineSimilarity(nil, nil))
	assert.Equal(t, 0.0, cosineSimilarity([]float32{}, []float32{1, 0}))
}

func TestCosineSimilarity_MismatchedLengths(t *testing.T) {
	assert.Equal(t, 0.0, cosineSimilarity([]float32{1, 0}, []float32{1, 0, 0}))
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	assert.Equal(t, 0.0, cosineSimilarity([]float32{0, 0}, []float32{1, 0}))
}

func TestPatternSeparation_MergeSameEntity(t *testing.T) {
	ps := DefaultPatternSeparation()
	emb := []float32{1, 0, 0}
	ctx := SeparationContext{
		ProjectID: "my-project",
		Language:  "go",
		EntityID:  "func:NewRedisPool",
	}

	result := ps.Check(emb, emb, ctx, ctx)
	assert.True(t, result.ShouldMerge)
	assert.Equal(t, "same_entity_same_context", result.Reason)
}

func TestPatternSeparation_BelowThreshold(t *testing.T) {
	ps := DefaultPatternSeparation()
	// Embeddings with cosine similarity ~0.0
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	ctx := SeparationContext{
		ProjectID: "my-project",
		Language:  "go",
		EntityID:  "func:NewRedisPool",
	}

	result := ps.Check(a, b, ctx, ctx)
	assert.False(t, result.ShouldMerge)
	assert.Equal(t, "below_similarity_threshold", result.Reason)
}

func TestPatternSeparation_DifferentProject(t *testing.T) {
	ps := DefaultPatternSeparation()
	emb := []float32{1, 0, 0} // identical vectors, similarity = 1.0
	ctxA := SeparationContext{
		ProjectID: "project-a",
		Language:  "go",
		EntityID:  "func:NewRedisPool",
	}
	ctxB := SeparationContext{
		ProjectID: "project-b",
		Language:  "go",
		EntityID:  "func:NewRedisPool",
	}

	result := ps.Check(emb, emb, ctxA, ctxB)
	assert.False(t, result.ShouldMerge)
	assert.Equal(t, "different_project", result.Reason)
}

func TestPatternSeparation_DifferentLanguage(t *testing.T) {
	ps := DefaultPatternSeparation()
	emb := []float32{1, 0, 0}
	ctxA := SeparationContext{
		ProjectID: "my-project",
		Language:  "go",
		EntityID:  "func:NewRedisPool",
	}
	ctxB := SeparationContext{
		ProjectID: "my-project",
		Language:  "python",
		EntityID:  "func:NewRedisPool",
	}

	result := ps.Check(emb, emb, ctxA, ctxB)
	assert.False(t, result.ShouldMerge)
	assert.Equal(t, "different_language", result.Reason)
}

func TestPatternSeparation_DifferentEntity(t *testing.T) {
	ps := DefaultPatternSeparation()
	emb := []float32{1, 0, 0}
	ctxA := SeparationContext{
		ProjectID: "my-project",
		Language:  "go",
		EntityID:  "func:NewRedisPool",
	}
	ctxB := SeparationContext{
		ProjectID: "my-project",
		Language:  "go",
		EntityID:  "func:NewDBPool",
	}

	result := ps.Check(emb, emb, ctxA, ctxB)
	assert.False(t, result.ShouldMerge)
	assert.Equal(t, "different_entity", result.Reason)
}

func TestPatternSeparation_ScaledIdentityVectors(t *testing.T) {
	ps := DefaultPatternSeparation()
	// Same direction, different magnitude — cos sim = 1.0
	a := []float32{2, 0, 0}
	b := []float32{5, 0, 0}
	ctx := SeparationContext{ProjectID: "p", Language: "go", EntityID: "func:F"}
	result := ps.Check(a, b, ctx, ctx)
	assert.True(t, result.ShouldMerge)
}

func TestPatternSeparation_CustomThreshold(t *testing.T) {
	ps := &PatternSeparation{SimilarityThreshold: 0.95}
	// Cosine sim = 0.5 for these vectors
	a := []float32{1, 1}
	b := []float32{1, 0}
	ctx := SeparationContext{ProjectID: "p", Language: "go", EntityID: "func:F"}
	result := ps.Check(a, b, ctx, ctx)
	assert.False(t, result.ShouldMerge) // 0.5 < 0.95
}

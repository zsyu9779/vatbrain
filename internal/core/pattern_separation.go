package core

import "math"

// PatternSeparation determines whether a new memory should be merged into an
// existing one or stored as a separate node.
//
// It implements Design Principle 7 (Pattern Separation before Pattern Completion):
// similarity alone is not enough to justify merging. Two different projects
// experiencing the same Redis timeout must stay as independent memories.
//
// The check is three-tiered:
//  1. Semantic similarity must exceed a high threshold (default 0.85)
//  2. Hard constraint dimensions must match (project_id, language)
//  3. Entity/theme identity must match (same function, not just same error)
type PatternSeparation struct {
	// SimilarityThreshold is the minimum cosine similarity required to even
	// consider merging. Must be in [0, 1]. Default 0.85.
	SimilarityThreshold float64
}

// DefaultPatternSeparation returns a PatternSeparation with the tuned default threshold.
func DefaultPatternSeparation() *PatternSeparation {
	return &PatternSeparation{
		SimilarityThreshold: 0.85,
	}
}

// SeparationResult records the outcome of a pattern separation check.
type SeparationResult struct {
	ShouldMerge bool
	Reason      string // human-readable explanation
}

// Check evaluates whether a new memory should merge with an existing candidate.
//
//	newEmbedding and candidateEmbedding are the vector representations.
//	newCtx and candidateCtx carry the hard-constraint dimensions and entity identity.
func (ps *PatternSeparation) Check(
	newEmbedding, candidateEmbedding []float32,
	newCtx, candidateCtx SeparationContext,
) SeparationResult {
	// Step 1: Semantic similarity must be very high.
	sim := cosineSimilarity(newEmbedding, candidateEmbedding)
	if sim < ps.SimilarityThreshold {
		return SeparationResult{ShouldMerge: false, Reason: "below_similarity_threshold"}
	}

	// Step 2: Hard constraint dimensions must match.
	if newCtx.ProjectID != candidateCtx.ProjectID {
		return SeparationResult{ShouldMerge: false, Reason: "different_project"}
	}
	if newCtx.Language != candidateCtx.Language {
		return SeparationResult{ShouldMerge: false, Reason: "different_language"}
	}

	// Step 3: Entity/theme identity must match.
	// Same function called twice → merge. Different functions, same error → don't merge.
	if newCtx.EntityID != candidateCtx.EntityID {
		return SeparationResult{ShouldMerge: false, Reason: "different_entity"}
	}

	return SeparationResult{ShouldMerge: true, Reason: "same_entity_same_context"}
}

// SeparationContext carries the hard-constraint dimensions for pattern separation.
type SeparationContext struct {
	ProjectID string
	Language  string
	EntityID  string
}

// cosineSimilarity computes the cosine similarity between two float32 vectors.
// Returns 0 if either vector is empty or zero-length.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (sqrtF64(normA) * sqrtF64(normB))
}


// sqrtF64 is a thin wrapper around math.Sqrt for readability.
func sqrtF64(x float64) float64 {
	return math.Sqrt(x)
}

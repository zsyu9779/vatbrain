package core

import (
	"context"
	"time"

	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// embeddingSimilarityThreshold is the cosine similarity above which two
// summaries are treated as carrying the same information. It is shared by the
// significance gate's cross-cycle condition and link_on_write's RELATES_TO
// edge creation, and is aligned with ConsolidationEngine.AccuracyThreshold.
const embeddingSimilarityThreshold = 0.7

// SignificanceGate decides whether an event passes the threshold for persistence
// into long-term memory. It implements the principle "forgetting is default,
// remembering is the exception."
//
// Four conditions gate the write (any one is sufficient, per DESIGN_PRINCIPLES.md Section 4.2):
//  1. User explicitly confirmed ("remember this", "this is important")
//  2. Cross-cycle persistence (the same information appears in >= 2 working-memory cycles)
//  3. Prediction error (the event is a correction or caused a behavior change)
//  4. Subsequent reference (the event was actively referenced >= 2 times later)
type SignificanceGate struct {
	// MinCrossCycleCount is the minimum number of working-memory cycles that must
	// contain similar information for condition 2 to pass. Default 2.
	MinCrossCycleCount int
	// MinSubsequentRefs is the minimum number of subsequent references for
	// condition 4 to pass. Default 2.
	MinSubsequentRefs int
	// Embedder, when set, enables embedding-based cross-cycle similarity.
	// Embeddings are CJK-safe; the keyword TokenOverlap fallback only sees
	// space-delimited Latin text (a Chinese summary tokenizes to an empty
	// set). When nil, the gate degrades to keyword overlap.
	Embedder embedder.Embedder
	// EmbedSimilarityThreshold is the cosine similarity above which two
	// summaries count as the same information. Default 0.7.
	EmbedSimilarityThreshold float64
}

// DefaultSignificanceGate returns a SignificanceGate with sensible defaults.
func DefaultSignificanceGate() *SignificanceGate {
	return &SignificanceGate{
		MinCrossCycleCount:       2,
		MinSubsequentRefs:        2,
		EmbedSimilarityThreshold: embeddingSimilarityThreshold,
	}
}

// GateResult is the outcome of evaluating whether an event should be persisted.
type GateResult struct {
	ShouldPersist bool
	Reason        string // one of the GateReason constants, or "below_threshold"
}

// Evaluate runs the event through all four gating conditions.
// It returns as soon as the first condition passes (short-circuit OR).
func (g *SignificanceGate) Evaluate(ctx context.Context, event WriteEvent, workingMemory []WorkingMemoryCycle) GateResult {
	// Condition 1: User explicitly confirmed.
	if event.UserConfirmed {
		return GateResult{ShouldPersist: true, Reason: "user_confirmed"}
	}

	// Condition 2: Cross-cycle persistence — same info in >= N recent cycles.
	if g.countRecentCycles(ctx, event, workingMemory) >= g.MinCrossCycleCount {
		return GateResult{ShouldPersist: true, Reason: "cross_cycle_persistence"}
	}

	// Condition 3: Prediction error / correction signal.
	if event.IsCorrection || event.CausedBehaviorChange {
		return GateResult{ShouldPersist: true, Reason: "prediction_error"}
	}

	// Condition 4: Subsequently referenced >= N times by later interactions.
	if event.SubsequentReferenceCount >= g.MinSubsequentRefs {
		return GateResult{ShouldPersist: true, Reason: "subsequent_reference"}
	}

	return GateResult{ShouldPersist: false, Reason: "below_threshold"}
}

// countRecentCycles counts how many recent working-memory cycles contain
// information similar to the given event. When the gate has an embedder, it
// uses embedding cosine similarity (CJK-safe); otherwise it falls back to
// keyword overlap (v0.1 proxy, Latin-only).
func (g *SignificanceGate) countRecentCycles(ctx context.Context, event WriteEvent, cycles []WorkingMemoryCycle) int {
	if g.Embedder != nil {
		threshold := g.EmbedSimilarityThreshold
		if threshold <= 0 {
			threshold = embeddingSimilarityThreshold
		}
		count := 0
		for _, c := range cycles {
			sim, ok := embeddingSimilarity(ctx, g.Embedder, event.Summary, c.Summary)
			if ok {
				if sim >= threshold {
					count++
				}
				continue
			}
			// Embedding unavailable (no signal): best-effort keyword fallback.
			if TokenOverlap(event.Summary, c.Summary) {
				count++
			}
		}
		return count
	}
	count := 0
	for _, c := range cycles {
		if TokenOverlap(event.Summary, c.Summary) {
			count++
		}
	}
	return count
}

// embeddingSimilarity returns the cosine similarity between two texts embedded
// via emb. ok is false when embedding fails or yields a zero-magnitude vector
// (no semantic signal) — callers then fall back to the lexical token proxy.
func embeddingSimilarity(ctx context.Context, emb embedder.Embedder, a, b string) (float64, bool) {
	ea, err := emb.Embed(ctx, a)
	if err != nil {
		return 0, false
	}
	eb, err := emb.Embed(ctx, b)
	if err != nil {
		return 0, false
	}
	if len(ea) == 0 || len(eb) == 0 || len(ea) != len(eb) {
		return 0, false
	}
	if !vectorHasMagnitude(ea) || !vectorHasMagnitude(eb) {
		return 0, false
	}
	return vector.CosineSimilarity(vector.Float32To64(ea), vector.Float32To64(eb)), true
}

// vectorHasMagnitude reports whether v has any non-zero component. A zero
// vector (e.g. the stub embedder) carries no semantic signal and must not be
// treated as a meaningful embedding.
func vectorHasMagnitude(v []float32) bool {
	for _, x := range v {
		if x != 0 {
			return true
		}
	}
	return false
}

// topicOverlap is a v0.1 approximation for semantic similarity between two
// summaries. It checks whether the summaries share any significant words.
// Phase 2+ can replace this with embedding similarity.
// TokenOverlap checks if two strings share meaningful tokens (longer than 3 chars).
func TokenOverlap(a, b string) bool {
	// Simple token overlap: split on whitespace and check for shared tokens
	// longer than 3 characters (to skip noise words).
	aTokens := Tokenize(a)
	bTokens := Tokenize(b)

	aLen, bLen := len(aTokens), len(bTokens)
	if aLen == 0 || bLen == 0 {
		return false
	}

	set := make(map[string]struct{}, aLen)
	for _, t := range aTokens {
		set[t] = struct{}{}
	}

	matches := 0
	for _, t := range bTokens {
		if _, ok := set[t]; ok {
			matches++
		}
	}

	// At least 3 shared tokens, or > 30% overlap of the smaller set.
	if matches >= 3 {
		return true
	}
	smaller := aLen
	if bLen < smaller {
		smaller = bLen
	}
	return float64(matches)/float64(smaller) > 0.3
}

// Tokenize splits text into lowercase tokens longer than 3 chars.
func Tokenize(s string) []string {
	var tokens []string
	start := -1
	for i, r := range s {
		if IsAlphaNum(r) {
			if start < 0 {
				start = i
			}
		} else {
			if start >= 0 && i-start > 3 {
				tokens = append(tokens, toLower(s[start:i]))
			}
			start = -1
		}
	}
	if start >= 0 && len(s)-start > 3 {
		tokens = append(tokens, toLower(s[start:]))
	}
	return tokens
}

// IsAlphaNum reports whether the rune is alphanumeric.
func IsAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}

// WriteEvent is the input to the significance gate, representing a candidate
// memory event before persistence.
type WriteEvent struct {
	Summary                  string
	UserConfirmed            bool
	IsCorrection             bool
	CausedBehaviorChange     bool
	SubsequentReferenceCount int
	// OccurredAt is when the event happened (from the source message's
	// chat_time). Zero means no explicit time was provided; the pipeline falls
	// back to the write time (CreatedAt).
	OccurredAt time.Time
}

// WorkingMemoryCycle represents one cycle's compressed summary.
// In v0.1 a "cycle" is roughly one task/interaction boundary.
type WorkingMemoryCycle struct {
	Summary string
}

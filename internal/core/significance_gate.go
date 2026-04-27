package core

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
}

// DefaultSignificanceGate returns a SignificanceGate with sensible defaults.
func DefaultSignificanceGate() *SignificanceGate {
	return &SignificanceGate{
		MinCrossCycleCount: 2,
		MinSubsequentRefs:  2,
	}
}

// GateResult is the outcome of evaluating whether an event should be persisted.
type GateResult struct {
	ShouldPersist bool
	Reason        string // one of the GateReason constants, or "below_threshold"
}

// Evaluate runs the event through all four gating conditions.
// It returns as soon as the first condition passes (short-circuit OR).
func (g *SignificanceGate) Evaluate(event WriteEvent, workingMemory []WorkingMemoryCycle) GateResult {
	// Condition 1: User explicitly confirmed.
	if event.UserConfirmed {
		return GateResult{ShouldPersist: true, Reason: "user_confirmed"}
	}

	// Condition 2: Cross-cycle persistence — same info in >= N recent cycles.
	if g.countRecentCycles(event, workingMemory) >= g.MinCrossCycleCount {
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
// information similar to the given event. v0.1 uses simple keyword overlap
// as a proxy for topic similarity.
func (g *SignificanceGate) countRecentCycles(event WriteEvent, cycles []WorkingMemoryCycle) int {
	count := 0
	for _, c := range cycles {
		if topicOverlap(event.Summary, c.Summary) {
			count++
		}
	}
	return count
}

// topicOverlap is a v0.1 approximation for semantic similarity between two
// summaries. It checks whether the summaries share any significant words.
// Phase 2+ can replace this with embedding similarity.
func topicOverlap(a, b string) bool {
	// Simple token overlap: split on whitespace and check for shared tokens
	// longer than 3 characters (to skip noise words).
	aTokens := tokenize(a)
	bTokens := tokenize(b)

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

// tokenize splits text into lowercase tokens longer than 3 chars.
func tokenize(s string) []string {
	var tokens []string
	start := -1
	for i, r := range s {
		if isAlphaNum(r) {
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

func isAlphaNum(r rune) bool {
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
}

// WorkingMemoryCycle represents one cycle's compressed summary.
// In v0.1 a "cycle" is roughly one task/interaction boundary.
type WorkingMemoryCycle struct {
	Summary string
}

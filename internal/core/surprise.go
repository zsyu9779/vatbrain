package core

// SurpriseScorer computes the prediction-error signal described in
// DESIGN_PRINCIPLES.md §12. The insight: dopamine neurons encode reward
// prediction error — the gap between expected and actual. A memory that broke
// an expectation (a user correction, a caused behavior change) carries far more
// information than one that was merely used, so it earns an independent
// "surprise" dimension that extends its decay half-life.
//
// The score is deliberately independent of the regular weight and computed from
// event signals at write time, then persisted alongside the memory so the
// surprise-aware decay can be applied (or a search can opt into a surprise
// ranking boost) without re-deriving it.
type SurpriseScorer struct {
	// CorrectionSurprise is the surprise contributed by a user correction of
	// prior information. Default 0.7.
	CorrectionSurprise float64
	// BehaviorChangeSurprise is the surprise contributed by an event that
	// caused an agent behavior change. Default 0.5.
	BehaviorChangeSurprise float64
}

// DefaultSurpriseScorer returns a SurpriseScorer with tuned defaults. A full
// correction (0.7) plus a behavior change (0.5) clamps to the maximum 1.0; a
// bare correction lands at 0.7 — clearly a prediction-error signal but not the
// absolute ceiling, so rare double-signal events still rank above it.
func DefaultSurpriseScorer() *SurpriseScorer {
	return &SurpriseScorer{
		CorrectionSurprise:    0.7,
		BehaviorChangeSurprise: 0.5,
	}
}

// Score computes the surprise score in [0, 1] for a write event.
//
// A deliberate user instruction ("记住：...") is the opposite of a broken
// expectation — the agent was told exactly what to do and did it — so it scores
// zero. Corrections and behavior changes score their configured weights and the
// result clamps at 1.
func (s *SurpriseScorer) Score(event WriteEvent) float64 {
	if s == nil {
		s = DefaultSurpriseScorer()
	}
	if event.UserConfirmed {
		return 0
	}

	var surprise float64
	if event.IsCorrection {
		surprise += s.CorrectionSurprise
	}
	if event.CausedBehaviorChange {
		surprise += s.BehaviorChangeSurprise
	}
	if surprise > 1 {
		return 1
	}
	return surprise
}

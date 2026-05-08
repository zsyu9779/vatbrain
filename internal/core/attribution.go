package core

import (
	"math"

	"github.com/vatbrain/vatbrain/internal/models"
)

// ApplyFeedback computes weight and effective-frequency deltas from post-retrieval
// behaviour. It is a pure function — no side effects, no I/O.
//
// Behaviour → delta mapping (design doc §6.2):
//   - Used:      +0.10  (retrieved AND adopted by the caller)
//   - Corrected: +0.50  (retrieved but led to a correction — strongest signal)
//   - Corrected + user: additional +0.30 (user explicitly corrected)
//   - Confirmed: +0.20  (retrieved AND confirmed correct by user)
//   - Ignored:   –0.05  (retrieved but unused — micro-decay)
//
// WeightDecayEngine handles the time→decay dimension; ApplyFeedback handles the
// behaviour→increment dimension. The two are independent.
func ApplyFeedback(
	currentWeight float64,
	currentEffFreq float64,
	action models.SearchAction,
	isUserCorrected bool,
) (newWeight float64, newEffFreq float64) {

	const (
		boostUsed      = 0.10
		boostCorrected = 0.50
		boostConfirmed = 0.20
		penaltyIgnored = -0.05

		// Extra bonus when correction comes from user (not LLM inference).
		trustBoostCorrected = 0.30
	)

	delta := 0.0
	switch action {
	case models.SearchActionUsed:
		delta = boostUsed
	case models.SearchActionCorrected:
		delta = boostCorrected
		if isUserCorrected {
			delta += trustBoostCorrected
		}
	case models.SearchActionConfirmed:
		delta = boostConfirmed
	case models.SearchActionIgnored:
		delta = penaltyIgnored
	}

	newWeight = currentWeight + delta
	if newWeight < 0 {
		newWeight = 0
	}
	if newWeight > 1 {
		newWeight = 1
	}
	newEffFreq = currentEffFreq + math.Abs(delta)
	return
}

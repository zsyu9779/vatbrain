package api

import "github.com/vatbrain/vatbrain/internal/models"

// clampWeight ensures the weight stays in [0, 1].
func clampWeight(w float64) float64 {
	if w < 0 {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}

// feedbackDelta returns the weight change for a given user action.
func feedbackDelta(action models.SearchAction) float64 {
	switch action {
	case models.SearchActionUsed:
		return 0.15
	case models.SearchActionConfirmed:
		return 0.20
	case models.SearchActionIgnored:
		return -0.05
	case models.SearchActionCorrected:
		return 0.30
	default:
		return 0
	}
}

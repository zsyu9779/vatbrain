package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vatbrain/vatbrain/internal/models"
)

func TestApplyFeedback_Used(t *testing.T) {
	w, f := ApplyFeedback(1.0, 1.0, models.SearchActionUsed, false)
	// 1.0 + 0.10 = 1.10 → clamped to 1.0.
	assert.InDelta(t, 1.0, w, 0.001)
	assert.InDelta(t, 1.10, f, 0.001)
}

func TestApplyFeedback_Corrected(t *testing.T) {
	w, f := ApplyFeedback(1.0, 1.0, models.SearchActionCorrected, false)
	// 1.0 + 0.50 = 1.50 → clamped to 1.0.
	assert.InDelta(t, 1.0, w, 0.001)
	assert.InDelta(t, 1.50, f, 0.001) // effFreq uses full delta
}

func TestApplyFeedback_CorrectedByUser(t *testing.T) {
	w, f := ApplyFeedback(1.0, 1.0, models.SearchActionCorrected, true)
	// 0.50 (corrected) + 0.30 (user bonus) = 0.80 → clamped to 1.0.
	assert.InDelta(t, 1.0, w, 0.001)
	assert.InDelta(t, 1.80, f, 0.001) // effFreq uses full delta
}

func TestApplyFeedback_Confirmed(t *testing.T) {
	w, f := ApplyFeedback(1.0, 1.0, models.SearchActionConfirmed, false)
	// 1.0 + 0.20 = 1.20 → clamped to 1.0.
	assert.InDelta(t, 1.0, w, 0.001)
	assert.InDelta(t, 1.20, f, 0.001)
}

func TestApplyFeedback_Ignored(t *testing.T) {
	w, f := ApplyFeedback(1.0, 1.0, models.SearchActionIgnored, false)
	assert.InDelta(t, 0.95, w, 0.001)
	assert.InDelta(t, 1.05, f, 0.001)
}

func TestApplyFeedback_ClampToZero(t *testing.T) {
	// Starting at 0.01, ignored action (-0.05) → would go negative → clamped to 0.
	w, f := ApplyFeedback(0.01, 1.0, models.SearchActionIgnored, false)
	assert.InDelta(t, 0.0, w, 0.001) // clamped
	assert.InDelta(t, 1.05, f, 0.001) // effFreq still increases
}

func TestApplyFeedback_UnknownAction(t *testing.T) {
	w, f := ApplyFeedback(1.0, 1.0, models.SearchAction("unknown"), false)
	assert.InDelta(t, 1.0, w, 0.001) // unchanged
	assert.InDelta(t, 1.0, f, 0.001)
}

func TestApplyFeedback_ConsecutiveCorrections(t *testing.T) {
	// Two consecutive user corrections — weight caps at 1.0.
	w, f := ApplyFeedback(1.0, 1.0, models.SearchActionCorrected, true)
	assert.InDelta(t, 1.0, w, 0.001)
	assert.InDelta(t, 1.80, f, 0.001)

	w2, f2 := ApplyFeedback(w, f, models.SearchActionCorrected, true)
	assert.InDelta(t, 1.0, w2, 0.001)
	assert.InDelta(t, 2.60, f2, 0.001)
}

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
)

// handleFeedback implements POST /api/v0/memories/{memory_id}/feedback.
//
// Records user behavior after a retrieval. Updates weight via simple delta
// based on the action: used (+0.15), confirmed (+0.20), ignored (-0.05),
// corrected (+0.30).
func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	memoryID, err := uuid.Parse(chi.URLParam(r, "memory_id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid memory_id format")
		return
	}

	var req models.FeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !req.Action.IsValid() {
		respondError(w, http.StatusBadRequest, "invalid action")
		return
	}

	ctx := r.Context()

	// Fetch current memory from Store.
	mem, err := s.Store.GetEpisodic(ctx, memoryID)
	if err != nil {
		slog.Error("store get episodic for feedback", "err", err)
		respondError(w, http.StatusInternalServerError, "read memory failed")
		return
	}
	if mem == nil {
		respondError(w, http.StatusNotFound, "memory not found")
		return
	}

	// Compute weight delta.
	delta := feedbackDelta(req.Action)
	newWeight := clampWeight(mem.Weight + delta)
	now := time.Now()

	// Update via Store.
	if err := s.Store.UpdateEpisodicWeight(ctx, memoryID, newWeight, mem.EffectiveFrequency); err != nil {
		slog.Error("store feedback update", "err", err)
		respondError(w, http.StatusInternalServerError, "weight update failed")
		return
	}
	// Also touch to update last_accessed_at.
	_ = s.Store.TouchEpisodic(ctx, memoryID, now)

	respondJSON(w, http.StatusOK, map[string]any{
		"memory_id":  memoryID,
		"new_weight": newWeight,
	})
}

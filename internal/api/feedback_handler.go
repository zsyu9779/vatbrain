package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
)

// handleFeedback implements POST /api/v0/memories/{memory_id}/feedback.
//
// Records user behavior after a retrieval. Updates weight via behavior-attribution
// delta and, for correction actions, triggers reconsolidation back-propagation
// through DERIVED_FROM traceability chains (v0.2).
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

	// Determine memory type and extract current weight/effFreq in one pass.
	// Falls through: episodic → semantic → pitfall.
	memType, currentWeight, currentEffFreq, lookupErr := s.lookupMemory(ctx, memoryID)
	if lookupErr != nil {
		slog.Error("store get memory for feedback", "id", memoryID, "err", lookupErr)
		respondError(w, http.StatusInternalServerError, "read memory failed")
		return
	}

	isUserCorrected := req.Action == models.SearchActionCorrected
	newWeight, newEffFreq := core.ApplyFeedback(currentWeight, currentEffFreq, req.Action, isUserCorrected)

	// Persist weight update.
	switch memType {
	case "episodic":
		if upErr := s.Store.UpdateEpisodicWeight(ctx, memoryID, newWeight, newEffFreq); upErr != nil {
			slog.Error("store feedback update", "err", upErr)
			respondError(w, http.StatusInternalServerError, "weight update failed")
			return
		}
	case "semantic":
		if upErr := s.Store.UpdateSemanticWeight(ctx, memoryID, newWeight, newEffFreq); upErr != nil {
			slog.Error("store feedback update semantic", "err", upErr)
			respondError(w, http.StatusInternalServerError, "weight update failed")
			return
		}
	case "pitfall":
		if upErr := s.Store.UpdatePitfallWeight(ctx, memoryID, newWeight); upErr != nil {
			slog.Error("store feedback update pitfall", "err", upErr)
			respondError(w, http.StatusInternalServerError, "weight update failed")
			return
		}
	}

	// v0.2: Reconsolidation — back-propagate correction signals to source memories.
	var recResult *core.ReconsolidationResult
	if req.Action == models.SearchActionCorrected && req.CorrectionDetail != nil &&
		s.Reconsolidation != nil {
		var recErr error
		recResult, recErr = s.Reconsolidation.Process(
			ctx, s.Store, memoryID, memType,
			*req.CorrectionDetail, isUserCorrected,
		)
		if recErr != nil {
			slog.Warn("reconsolidation failed", "corrected_id", memoryID, "err", recErr)
		}
	}

	resp := map[string]any{
		"memory_id":  memoryID,
		"new_weight": newWeight,
		"type":       memType,
	}
	if recResult != nil {
		resp["reconsolidation"] = recResult
	}
	respondJSON(w, http.StatusOK, resp)
}

// lookupMemory tries each memory type in turn and returns the type, current
// weight, effective frequency, and the last lookup error (if all fail).
func (s *Server) lookupMemory(ctx context.Context, id uuid.UUID) (
	memType string, weight, effFreq float64, err error,
) {
	// Episodic.
	ep, epErr := s.Store.GetEpisodic(ctx, id)
	if epErr == nil && ep != nil {
		return "episodic", ep.Weight, ep.EffectiveFrequency, nil
	}

	// Semantic.
	sem, semErr := s.Store.GetSemantic(ctx, id)
	if semErr == nil && sem != nil {
		return "semantic", sem.Weight, sem.EffectiveFrequency, nil
	}

	// Pitfall.
	pf, pfErr := s.Store.GetPitfall(ctx, id)
	if pfErr == nil && pf != nil {
		return "pitfall", pf.Weight, 1.0, nil // PitfallMemory has no EffectiveFrequency
	}

	// All three failed. Return the most specific error.
	if pfErr != nil {
		return "", 0, 0, pfErr
	}
	if semErr != nil {
		return "", 0, 0, semErr
	}
	return "", 0, 0, epErr
}

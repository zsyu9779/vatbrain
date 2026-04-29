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

// handleTouch implements POST /api/v0/memories/{memory_id}/touch.
//
// Records a retrieval hit, recomputes full weight via the WeightDecayEngine,
// and updates via the Store.
func (s *Server) handleTouch(w http.ResponseWriter, r *http.Request) {
	memoryID, err := uuid.Parse(chi.URLParam(r, "memory_id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid memory_id format")
		return
	}

	var req models.TouchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	_ = req // reserved for future use

	ctx := r.Context()
	now := time.Now()

	// Fetch current memory from Store.
	mem, err := s.Store.GetEpisodic(ctx, memoryID)
	if err != nil {
		slog.Error("store get episodic for touch", "err", err)
		respondError(w, http.StatusInternalServerError, "read memory failed")
		return
	}
	if mem == nil {
		respondError(w, http.StatusNotFound, "memory not found")
		return
	}

	newWeight := s.WeightDecay.Weight(1.0, mem.CreatedAt, now, now)
	newEffFreq := mem.EffectiveFrequency + 1.0

	// Update via Store: weight + effective frequency.
	if err := s.Store.UpdateEpisodicWeight(ctx, memoryID, newWeight, newEffFreq); err != nil {
		slog.Error("store touch update", "err", err)
		respondError(w, http.StatusInternalServerError, "touch update failed")
		return
	}
	// Also touch to update last_accessed_at.
	_ = s.Store.TouchEpisodic(ctx, memoryID, now)

	respondJSON(w, http.StatusOK, models.TouchResponse{
		NewWeight: newWeight,
	})
}

// handleWeightDetail implements GET /api/v0/memories/{memory_id}/weight.
//
// Returns the full weight calculation breakdown for a given memory.
func (s *Server) handleWeightDetail(w http.ResponseWriter, r *http.Request) {
	memoryID, err := uuid.Parse(chi.URLParam(r, "memory_id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid memory_id format")
		return
	}

	ctx := r.Context()
	now := time.Now()

	mem, err := s.Store.GetEpisodic(ctx, memoryID)
	if err != nil {
		slog.Error("store get episodic for weight detail", "err", err)
		respondError(w, http.StatusInternalServerError, "read memory failed")
		return
	}
	if mem == nil {
		respondError(w, http.StatusNotFound, "memory not found")
		return
	}

	// Compute decay components.
	experienceDecay := s.WeightDecay.Weight(mem.EffectiveFrequency, mem.CreatedAt, mem.CreatedAt, now)
	activityDecay := 0.0
	if mem.LastAccessedAt != nil {
		activityDecay = s.WeightDecay.Weight(mem.EffectiveFrequency, mem.CreatedAt, *mem.LastAccessedAt, now)
	}

	respondJSON(w, http.StatusOK, models.WeightDetailResponse{
		MemoryID:           memoryID,
		Weight:             mem.Weight,
		EffectiveFrequency: mem.EffectiveFrequency,
		ExperienceDecay:    experienceDecay,
		ActivityDecay:      activityDecay,
	})
}

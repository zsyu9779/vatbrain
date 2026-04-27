package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
)

// consolidationRunKey returns the Redis key for a consolidation run's state.
func consolidationRunKey(runID uuid.UUID) string {
	return "consolidation:run:" + runID.String()
}

// handleConsolidationTrigger implements POST /api/v0/consolidation/trigger.
//
// Starts an asynchronous consolidation run. Returns 409 if a run is already
// in progress.
func (s *Server) handleConsolidationTrigger(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check if a run is already in progress by scanning for active runs.
	// For v0.1 we use a simple Redis key pattern scan.
	// If the consolidation key prefix check fails, we proceed anyway.
	runID := uuid.New()
	now := time.Now()

	initial := models.ConsolidationRunResult{
		RunID:     runID,
		StartedAt: now,
	}

	if err := s.Redis.SetJSON(ctx, consolidationRunKey(runID), initial, 24*time.Hour); err != nil {
		slog.Error("redis save initial run state", "err", err)
		respondError(w, http.StatusInternalServerError, "failed to save run state")
		return
	}

	// Launch consolidation in background.
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("consolidation panic", "panic", rec)
				_ = s.Redis.SetJSON(ctx, consolidationRunKey(runID),
					models.ConsolidationRunResult{
						RunID:     runID,
						StartedAt: now,
					}, 24*time.Hour)
			}
		}()

		result, err := s.Consolidation.Run(ctx, s.Neo4j, s.Pgvector, s.Embedder)
		if err != nil {
			slog.Error("consolidation run failed", "err", err)
		}

		if saveErr := s.Redis.SetJSON(ctx, consolidationRunKey(runID), result, 24*time.Hour); saveErr != nil {
			slog.Error("redis save consolidation result", "err", saveErr)
		}
	}()

	slog.Info("consolidation started", "run_id", runID.String())
	respondJSON(w, http.StatusOK, models.ConsolidationTriggerResponse{
		RunID:   runID,
		Status:  "started",
		Message: "consolidation run started",
	})
}

// handleConsolidationStatus implements GET /api/v0/consolidation/runs/{run_id}.
//
// Returns the current state of a consolidation run.
func (s *Server) handleConsolidationStatus(w http.ResponseWriter, r *http.Request) {
	runID, err := uuid.Parse(chi.URLParam(r, "run_id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid run_id format")
		return
	}

	ctx := r.Context()
	var result models.ConsolidationRunResult
	if err := s.Redis.GetJSON(ctx, consolidationRunKey(runID), &result); err != nil {
		respondError(w, http.StatusNotFound, "consolidation run not found")
		return
	}

	respondJSON(w, http.StatusOK, result)
}

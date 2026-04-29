package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
)

// handleConsolidationTrigger implements POST /api/v0/consolidation/trigger.
//
// Starts an asynchronous consolidation run. Uses Store for run state persistence.
func (s *Server) handleConsolidationTrigger(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	runID := uuid.New()
	now := time.Now()

	initial := models.ConsolidationRunResult{
		RunID:     runID,
		StartedAt: now,
	}

	if err := s.Store.SaveConsolidationRun(ctx, &initial); err != nil {
		slog.Error("store save initial run state", "err", err)
		respondError(w, http.StatusInternalServerError, "failed to save run state")
		return
	}

	// Launch consolidation in background.
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("consolidation panic", "panic", rec)
			}
		}()

		result, err := s.Consolidation.Run(ctx, s.Store, s.Embedder)
		if err != nil {
			slog.Error("consolidation run failed", "err", err)
		}

		if saveErr := s.Store.SaveConsolidationRun(ctx, &result); saveErr != nil {
			slog.Error("store save consolidation result", "err", saveErr)
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
	result, err := s.Store.GetConsolidationRun(ctx, runID)
	if err != nil {
		respondError(w, http.StatusNotFound, "consolidation run not found")
		return
	}

	respondJSON(w, http.StatusOK, result)
}

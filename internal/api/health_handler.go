package api

import (
	"log/slog"
	"net/http"

	"github.com/vatbrain/vatbrain/internal/models"
)

// handleHealth implements GET /api/v0/health.
//
// Checks the Store backend and returns a unified health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := s.Store.HealthCheck(ctx); err != nil {
		slog.Warn("health check degraded", "err", err)
		respondJSON(w, http.StatusServiceUnavailable, models.HealthResponse{
			Status:  "degraded",
			Message: "unhealthy: " + err.Error(),
		})
		return
	}

	respondJSON(w, http.StatusOK, models.HealthResponse{
		Status: "healthy",
	})
}

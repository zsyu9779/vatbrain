package api

import (
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/sync/errgroup"
	"github.com/vatbrain/vatbrain/internal/models"
)

// handleHealth implements GET /api/v0/health.
//
// Checks all 4 databases concurrently and returns a unified health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	g, ctx := errgroup.WithContext(ctx)

	var neo4jOK, pgOK, redisOK, minioOK bool
	g.Go(func() error {
		err := s.Neo4j.HealthCheck(ctx)
		neo4jOK = (err == nil)
		if err != nil {
			slog.Warn("health neo4j", "err", err)
		}
		return nil // don't cancel other checks on one failure
	})
	g.Go(func() error {
		err := s.Pgvector.HealthCheck(ctx)
		pgOK = (err == nil)
		if err != nil {
			slog.Warn("health pgvector", "err", err)
		}
		return nil
	})
	g.Go(func() error {
		err := s.Redis.HealthCheck(ctx)
		redisOK = (err == nil)
		if err != nil {
			slog.Warn("health redis", "err", err)
		}
		return nil
	})
	g.Go(func() error {
		err := s.Minio.HealthCheck(ctx)
		minioOK = (err == nil)
		if err != nil {
			slog.Warn("health minio", "err", err)
		}
		return nil
	})

	_ = g.Wait() // errors logged individually above

	if neo4jOK && pgOK && redisOK && minioOK {
		respondJSON(w, http.StatusOK, models.HealthResponse{
			Status: "healthy",
		})
		return
	}

	// Build degradation message.
	var down []string
	if !neo4jOK {
		down = append(down, "neo4j")
	}
	if !pgOK {
		down = append(down, "pgvector")
	}
	if !redisOK {
		down = append(down, "redis")
	}
	if !minioOK {
		down = append(down, "minio")
	}

	respondJSON(w, http.StatusServiceUnavailable, models.HealthResponse{
		Status:  "degraded",
		Message: "unhealthy: " + strings.Join(down, ", "),
	})
}

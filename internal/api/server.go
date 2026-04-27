// Package api implements the HTTP API layer for VatBrain.
// Handlers are methods on Server, which holds all dependencies via constructor
// injection — no global state.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/db/minio"
	"github.com/vatbrain/vatbrain/internal/db/neo4j"
	"github.com/vatbrain/vatbrain/internal/db/pgvector"
	"github.com/vatbrain/vatbrain/internal/db/redis"
	"github.com/vatbrain/vatbrain/internal/embedder"
)

// Server holds all dependencies for the HTTP API.
type Server struct {
	Neo4j    *neo4j.Client
	Pgvector *pgvector.Client
	Redis    *redis.Client
	Minio    *minio.Client

	WeightDecay       *core.WeightDecayEngine
	SignificanceGate  *core.SignificanceGate
	PatternSeparation *core.PatternSeparation
	RetrievalEngine   *core.RetrievalEngine
	Consolidation     *core.ConsolidationEngine

	Embedder embedder.Embedder
	Config   config.Config
}

// NewServer creates a Server with all required dependencies.
func NewServer(
	cfg config.Config,
	neo4jClient *neo4j.Client,
	pgvectorClient *pgvector.Client,
	redisClient *redis.Client,
	minioClient *minio.Client,
	weightDecay *core.WeightDecayEngine,
	significanceGate *core.SignificanceGate,
	patternSeparation *core.PatternSeparation,
	retrievalEngine *core.RetrievalEngine,
	consolidation *core.ConsolidationEngine,
	emb embedder.Embedder,
) *Server {
	return &Server{
		Neo4j:              neo4jClient,
		Pgvector:           pgvectorClient,
		Redis:              redisClient,
		Minio:              minioClient,
		WeightDecay:        weightDecay,
		SignificanceGate:   significanceGate,
		PatternSeparation:  patternSeparation,
		RetrievalEngine:    retrievalEngine,
		Consolidation:      consolidation,
		Embedder:           emb,
		Config:             cfg,
	}
}

// Routes builds the chi router with all API endpoints and middleware.
func (s *Server) Routes() chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(middleware.RealIP)

	r.Route("/api/v0", func(r chi.Router) {
		r.Post("/memories/episodic", s.handleWrite)
		r.Post("/memories/search", s.handleSearch)
		r.Post("/memories/{memory_id}/feedback", s.handleFeedback)
		r.Post("/memories/{memory_id}/touch", s.handleTouch)
		r.Get("/memories/{memory_id}/weight", s.handleWeightDetail)
		r.Post("/consolidation/trigger", s.handleConsolidationTrigger)
		r.Get("/consolidation/runs/{run_id}", s.handleConsolidationStatus)
		r.Get("/health", s.handleHealth)
	})

	return r
}

// ListenAndServe starts the HTTP server and blocks until SIGINT or SIGTERM.
func (s *Server) ListenAndServe() {
	addr := ":" + strconv.Itoa(s.Config.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("vatbrain starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-quit
	slog.Info("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("forced shutdown", "err", err)
	}

	// Close DB connections.
	if err := s.Neo4j.Close(ctx); err != nil {
		slog.Error("neo4j close", "err", err)
	}
	s.Pgvector.Close()
	if err := s.Redis.Close(); err != nil {
		slog.Error("redis close", "err", err)
	}

	slog.Info("server stopped")
}

// respondJSON writes a JSON response with the given status code.
func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "err", err)
	}
}

// respondError writes a JSON error response.
func respondError(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}

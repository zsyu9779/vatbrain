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
	"sync"
	"syscall"
	"time"

	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/robfig/cron/v3"
	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/db/minio"
	"github.com/vatbrain/vatbrain/internal/db/neo4j"
	"github.com/vatbrain/vatbrain/internal/db/pgvector"
	"github.com/vatbrain/vatbrain/internal/db/redis"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/store"
)

// Server holds all dependencies for the HTTP API.
type Server struct {
	Store         store.MemoryStore
	WorkingMemory *store.WorkingMemoryBuffer

	// Legacy DB clients (Phase 4 backward compat).
	Neo4j    *neo4j.Client
	Pgvector *pgvector.Client
	Redis    *redis.Client
	Minio    *minio.Client

	WeightDecay       *core.WeightDecayEngine
	Reconsolidation   *core.ReconsolidationEngine
	SignificanceGate  *core.SignificanceGate
	PatternSeparation *core.PatternSeparation
	RetrievalEngine   *core.RetrievalEngine
	Consolidation     *core.ConsolidationEngine

	Embedder embedder.Embedder
	Config   config.Config

	cron             *cron.Cron
	consolidationMu  sync.Mutex // in-process guard for scheduled consolidation
}

// NewServer creates a Server with all required dependencies.
func NewServer(
	cfg config.Config,
	s store.MemoryStore,
	wm *store.WorkingMemoryBuffer,
	neo4jClient *neo4j.Client,
	pgvectorClient *pgvector.Client,
	redisClient *redis.Client,
	minioClient *minio.Client,
	weightDecay *core.WeightDecayEngine,
		reconsolidation *core.ReconsolidationEngine,
	significanceGate *core.SignificanceGate,
	patternSeparation *core.PatternSeparation,
	retrievalEngine *core.RetrievalEngine,
	consolidation *core.ConsolidationEngine,
	emb embedder.Embedder,
) *Server {
	srv := &Server{
		Store:              s,
		WorkingMemory:      wm,
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

	if cfg.Scheduler.Enabled {
		srv.cron = cron.New(cron.WithLogger(&cronLogger{}))
	}

	return srv
}

// cronLogger adapts slog to cron.Logger.
type cronLogger struct{}

func (l *cronLogger) Info(msg string, keysAndValues ...any) {
	slog.Info("cron: "+msg, keysAndValues...)
}

func (l *cronLogger) Error(err error, msg string, keysAndValues ...any) {
	args := append([]any{"err", err}, keysAndValues...)
	slog.Error("cron: "+msg, args...)
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
			r.Post("/pitfalls/search", s.handlePitfallSearch)
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

	// Start background scheduler.
	if s.cron != nil {
		_, err := s.cron.AddFunc(s.Config.Scheduler.ConsolidationCron, s.runScheduledConsolidation)
		if err != nil {
			slog.Error("failed to register consolidation cron", "err", err)
		} else {
			s.cron.Start()
			slog.Info("scheduler started", "consolidation_cron", s.Config.Scheduler.ConsolidationCron)
		}
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

	// Stop the scheduler first so no new jobs start.
	if s.cron != nil {
		<-s.cron.Stop().Done()
		slog.Info("scheduler stopped")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("forced shutdown", "err", err)
	}

	// Close store.
	if s.Store != nil {
		if err := s.Store.Close(); err != nil {
			slog.Error("store close", "err", err)
		}
	}

	// Close legacy DB connections.
	if s.Neo4j != nil {
		if err := s.Neo4j.Close(ctx); err != nil {
			slog.Error("neo4j close", "err", err)
		}
	}
	if s.Pgvector != nil {
		s.Pgvector.Close()
	}
	if s.Redis != nil {
		if err := s.Redis.Close(); err != nil {
			slog.Error("redis close", "err", err)
		}
	}

	slog.Info("server stopped")
}

// runScheduledConsolidation is the cron callback for daily consolidation.
func (s *Server) runScheduledConsolidation() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if s.Store == nil {
		slog.Warn("scheduler: skip consolidation — store not available")
		return
	}

	// In-process guard against overlapping runs.
	if !s.consolidationMu.TryLock() {
		slog.Info("scheduler: skip consolidation — previous run still in progress")
		return
	}
	defer s.consolidationMu.Unlock()

	slog.Info("scheduler: starting daily consolidation")
	result, err := s.Consolidation.Run(ctx, s.Store, s.Embedder)
	if err != nil {
		slog.Error("scheduler: consolidation failed", "err", err)
		return
	}

	slog.Info("scheduler: consolidation complete",
		"episodics_scanned", result.EpisodicsScanned,
		"candidate_rules", result.CandidateRulesFound,
		"rules_persisted", result.RulesPersisted,
		"avg_accuracy", result.AverageAccuracy,
	)
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

// Package app provides the shared application bootstrap logic used by both the
// HTTP API server and the MCP server. It wires up configuration, databases,
// engines, and the embedder into a single struct.
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/db/minio"
	"github.com/vatbrain/vatbrain/internal/db/neo4j"
	"github.com/vatbrain/vatbrain/internal/db/pgvector"
	"github.com/vatbrain/vatbrain/internal/db/redis"
	"github.com/vatbrain/vatbrain/internal/embedder"
)

// App holds all initialised application components.
type App struct {
	Config             config.Config
	Neo4j              *neo4j.Client
	Pgvector           *pgvector.Client
	Redis              *redis.Client
	Minio              *minio.Client
	WeightDecay        *core.WeightDecayEngine
	SignificanceGate   *core.SignificanceGate
	PatternSeparation  *core.PatternSeparation
	RetrievalEngine    *core.RetrievalEngine
	Consolidation      *core.ConsolidationEngine
	Embedder           embedder.Embedder
}

// New bootstraps the full application: config, databases, engines, and embedder.
// Missing databases are logged as warnings but do not prevent startup.
func New(ctx context.Context) (*App, error) {
	cfg := config.LoadFromEnv()

	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	neo4jClient, err := neo4j.NewClient(initCtx, cfg.Neo4j)
	if err != nil {
		slog.Warn("neo4j not available — continuing", "err", err)
	}

	pgvectorClient, err := pgvector.NewClient(initCtx, cfg.Pgvector)
	if err != nil {
		slog.Warn("pgvector not available — continuing", "err", err)
	}

	redisClient, err := redis.NewClient(initCtx, cfg.Redis)
	if err != nil {
		slog.Warn("redis not available — continuing", "err", err)
	}

	minioClient, err := minio.NewClient(initCtx, cfg.Minio)
	if err != nil {
		slog.Warn("minio not available — continuing", "err", err)
	}

	weightDecay := core.DefaultWeightDecayEngine()
	applyWeightDecayConfig(weightDecay, &cfg)

	significanceGate := core.DefaultSignificanceGate()
	applySignificanceConfig(significanceGate, &cfg)

	patternSeparation := core.DefaultPatternSeparation()
	if cfg.PatternSeparation.SimilarityThreshold != 0 {
		patternSeparation.SimilarityThreshold = cfg.PatternSeparation.SimilarityThreshold
	}

	retrievalEngine := core.DefaultRetrievalEngine()
	if cfg.Retrieval.MaxCandidates != 0 {
		retrievalEngine.MaxCandidates = cfg.Retrieval.MaxCandidates
	}

	consolidation := core.DefaultConsolidationEngine()
	if cfg.Consolidation.HoursToScan != 0 {
		consolidation.HoursToScan = cfg.Consolidation.HoursToScan
	}
	if cfg.Consolidation.MinClusterSize != 0 {
		consolidation.MinClusterSize = cfg.Consolidation.MinClusterSize
	}
	if cfg.Consolidation.AccuracyThreshold != 0 {
		consolidation.AccuracyThreshold = cfg.Consolidation.AccuracyThreshold
	}

	emb := embedder.NewStubEmbedder()

	return &App{
		Config:             cfg,
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
	}, nil
}

// Close shuts down all database connections.
func (a *App) Close() {
	if a.Neo4j != nil {
		if err := a.Neo4j.Close(context.Background()); err != nil {
			slog.Warn("error closing neo4j", "err", err)
		}
	}
	if a.Pgvector != nil {
		a.Pgvector.Close()
	}
	if a.Redis != nil {
		if err := a.Redis.Close(); err != nil {
			slog.Warn("error closing redis", "err", err)
		}
	}
}

func applyWeightDecayConfig(w *core.WeightDecayEngine, cfg *config.Config) {
	if cfg.WeightDecay.LambdaDecay != 0 {
		w.LambdaDecay = cfg.WeightDecay.LambdaDecay
	}
	if cfg.WeightDecay.AlphaExperience != 0 {
		w.AlphaExperience = cfg.WeightDecay.AlphaExperience
	}
	if cfg.WeightDecay.BetaActivity != 0 {
		w.BetaActivity = cfg.WeightDecay.BetaActivity
	}
	if cfg.WeightDecay.CoolingThreshold != 0 {
		w.CoolingThreshold = cfg.WeightDecay.CoolingThreshold
	}
}

func applySignificanceConfig(s *core.SignificanceGate, cfg *config.Config) {
	if cfg.SignificanceGate.MinCrossCycleCount != 0 {
		s.MinCrossCycleCount = cfg.SignificanceGate.MinCrossCycleCount
	}
	if cfg.SignificanceGate.MinSubsequentRefs != 0 {
		s.MinSubsequentRefs = cfg.SignificanceGate.MinSubsequentRefs
	}
}

// Package config centralises server and infrastructure configuration loaded
// from environment variables with sensible defaults matching docker-compose.yml.
package config

import (
	"os"
	"strconv"

	"github.com/vatbrain/vatbrain/internal/db/minio"
	"github.com/vatbrain/vatbrain/internal/db/neo4j"
	"github.com/vatbrain/vatbrain/internal/db/pgvector"
	"github.com/vatbrain/vatbrain/internal/db/redis"
)

// StoreConfig selects the storage backend and holds backend-specific config.
type StoreConfig struct {
	Backend string // "sqlite" (default), "neo4j+pgvector", "memory"
	SQLite  SQLiteConfig
}

// SQLiteConfig holds configuration for the SQLite backend.
type SQLiteConfig struct {
	Path string // e.g. "./vatbrain.db"
	WAL  bool   // enable WAL mode
}

// Config holds all configuration for the VatBrain server.
type Config struct {
	Port  int
	Store StoreConfig

	Neo4j    neo4j.Config
	Pgvector pgvector.Config
	Redis    redis.Config
	Minio    minio.Config

	WeightDecay       WeightDecayConfig
	SignificanceGate  SignificanceGateConfig
	PatternSeparation PatternSeparationConfig
	Retrieval         RetrievalConfig
	Consolidation     ConsolidationConfig
	PitfallDecay      PitfallDecayConfig
	Scheduler         SchedulerConfig
	LLM               LLMConfig
}

// WeightDecayConfig holds tunable parameters for the weight decay engine.
type WeightDecayConfig struct {
	LambdaDecay      float64
	AlphaExperience  float64
	BetaActivity     float64
	CoolingThreshold float64
}

// PitfallDecayConfig holds tunable parameters specific to PitfallMemory decay.
type PitfallDecayConfig struct {
	LambdaDecay      float64
	AlphaExperience  float64
	BetaActivity     float64
	CoolingThreshold float64
}

// SignificanceGateConfig holds tunable parameters for the significance gate.
type SignificanceGateConfig struct {
	MinCrossCycleCount int
	MinSubsequentRefs  int
}

// PatternSeparationConfig holds tunable parameters for pattern separation.
type PatternSeparationConfig struct {
	SimilarityThreshold float64
}

// RetrievalConfig holds tunable parameters for the retrieval engine.
type RetrievalConfig struct {
	MaxCandidates int
}

// ConsolidationConfig holds tunable parameters for the consolidation engine.
type ConsolidationConfig struct {
	HoursToScan       float64
	MinClusterSize    int
	AccuracyThreshold float64
}

// SchedulerConfig holds parameters for the background cron scheduler.
type SchedulerConfig struct {
	Enabled bool
	// ConsolidationCron is the cron expression for the daily consolidation job.
	// Default "0 3 * * *" (3 AM daily).
	ConsolidationCron string
}

// LLMConfig holds configuration for LLM API access (Claude).
type LLMConfig struct {
	APIKey  string
	BaseURL string
	Model   string // Messages API model, default "claude-sonnet-4-6-20250501"
}

// LoadFromEnv reads configuration from environment variables with defaults.
func LoadFromEnv() Config {
	return Config{
		Port: envInt("PORT", 8080),

		Store: StoreConfig{
			Backend: envStr("VATBRAIN_STORE_BACKEND", "sqlite"),
			SQLite: SQLiteConfig{
				Path: envStr("VATBRAIN_SQLITE_PATH", "./vatbrain.db"),
				WAL:  envBool("VATBRAIN_SQLITE_WAL", true),
			},
		},

		Neo4j: neo4j.Config{
			URI:                  envStr("NEO4J_URI", "bolt://localhost:7687"),
			Username:             envStr("NEO4J_USERNAME", "neo4j"),
			Password:             envStr("NEO4J_PASSWORD", "vatbrain"),
			Database:             envStr("NEO4J_DATABASE", "neo4j"),
			MaxConnectionPoolSize: envInt("NEO4J_MAX_CONN_POOL", 100),
		},

		Pgvector: pgvector.Config{
			Host:     envStr("PG_HOST", "localhost"),
			Port:     envInt("PG_PORT", 5432),
			User:     envStr("PG_USER", "vatbrain"),
			Password: envStr("PG_PASSWORD", "vatbrain"),
			Database: envStr("PG_DATABASE", "vatbrain"),
			MaxConns: int32(envInt("PG_MAX_CONNS", 20)),
		},

		Redis: redis.Config{
			Addr:     envStr("REDIS_ADDR", "localhost:6379"),
			Password: envStr("REDIS_PASSWORD", ""),
			DB:       envInt("REDIS_DB", 0),
		},

		Minio: minio.Config{
			Endpoint:  envStr("MINIO_ENDPOINT", "localhost:9000"),
			AccessKey: envStr("MINIO_ACCESS_KEY", "minioadmin"),
			SecretKey: envStr("MINIO_SECRET_KEY", "minioadmin"),
			Bucket:    envStr("MINIO_BUCKET", "vatbrain"),
			UseSSL:    envBool("MINIO_USE_SSL", false),
		},

		WeightDecay: WeightDecayConfig{
			LambdaDecay:      envFloat("WEIGHT_LAMBDA_DECAY", 0.1),
			AlphaExperience:  envFloat("WEIGHT_ALPHA_EXPERIENCE", 0.005),
			BetaActivity:     envFloat("WEIGHT_BETA_ACTIVITY", 0.05),
			CoolingThreshold: envFloat("WEIGHT_COOLING_THRESHOLD", 0.01),
		},

		SignificanceGate: SignificanceGateConfig{
			MinCrossCycleCount: envInt("GATE_MIN_CROSS_CYCLE", 2),
			MinSubsequentRefs:  envInt("GATE_MIN_SUBSEQUENT_REFS", 2),
		},

		PatternSeparation: PatternSeparationConfig{
			SimilarityThreshold: envFloat("PATTERN_SIMILARITY_THRESHOLD", 0.85),
		},

		Retrieval: RetrievalConfig{
			MaxCandidates: envInt("RETRIEVAL_MAX_CANDIDATES", 500),
		},

		Consolidation: ConsolidationConfig{
			HoursToScan:       envFloat("CONSOLIDATION_HOURS_TO_SCAN", 24),
			MinClusterSize:    envInt("CONSOLIDATION_MIN_CLUSTER_SIZE", 3),
			AccuracyThreshold: envFloat("CONSOLIDATION_ACCURACY_THRESHOLD", 0.7),
		},

			PitfallDecay: PitfallDecayConfig{
				LambdaDecay:      envFloat("PITFALL_LAMBDA_DECAY", 0.15),
				AlphaExperience:  envFloat("PITFALL_ALPHA_EXPERIENCE", 0.008),
				BetaActivity:     envFloat("PITFALL_BETA_ACTIVITY", 0.03),
				CoolingThreshold: envFloat("PITFALL_COOLING_THRESHOLD", 0.005),
			},

		Scheduler: SchedulerConfig{
			Enabled:           envBool("SCHEDULER_ENABLED", true),
			ConsolidationCron: envStr("SCHEDULER_CONSOLIDATION_CRON", "0 3 * * *"),
		},

		LLM: LLMConfig{
			APIKey:  envStr("ANTHROPIC_API_KEY", ""),
			BaseURL: envStr("ANTHROPIC_BASE_URL", ""),
			Model:   envStr("ANTHROPIC_MODEL", "claude-sonnet-4-6-20250501"),
		},
	}
}

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

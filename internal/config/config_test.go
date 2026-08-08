package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEnvStr_Missing_ReturnsDefault(t *testing.T) {
	assert.Equal(t, "default", envStr("VATBRAIN_TEST_NONEXISTENT_KEY_XYZ", "default"))
}

func TestEnvStr_Present_ReturnsValue(t *testing.T) {
	t.Setenv("VATBRAIN_TEST_KEY", "myval")
	assert.Equal(t, "myval", envStr("VATBRAIN_TEST_KEY", "default"))
}

func TestEnvStr_EmptyValue(t *testing.T) {
	t.Setenv("VATBRAIN_TEST_EMPTY", "")
	// Empty string is a valid value — LookupEnv returns ok=true.
	assert.Equal(t, "", envStr("VATBRAIN_TEST_EMPTY", "default"))
}

func TestEnvInt_Missing_ReturnsDefault(t *testing.T) {
	assert.Equal(t, 8080, envInt("VATBRAIN_TEST_PORT_XYZ", 8080))
}

func TestEnvInt_Present_ReturnsParsed(t *testing.T) {
	t.Setenv("VATBRAIN_TEST_PORT", "9999")
	assert.Equal(t, 9999, envInt("VATBRAIN_TEST_PORT", 8080))
}

func TestEnvInt_Invalid_ReturnsDefault(t *testing.T) {
	t.Setenv("VATBRAIN_TEST_BAD_INT", "not-a-number")
	assert.Equal(t, 100, envInt("VATBRAIN_TEST_BAD_INT", 100))
}

func TestEnvFloat_Missing_ReturnsDefault(t *testing.T) {
	assert.InDelta(t, 0.1, envFloat("VATBRAIN_TEST_FLOAT_XYZ", 0.1), 1e-9)
}

func TestEnvFloat_Present_ReturnsParsed(t *testing.T) {
	t.Setenv("VATBRAIN_TEST_FLOAT", "0.55")
	assert.InDelta(t, 0.55, envFloat("VATBRAIN_TEST_FLOAT", 0.1), 1e-9)
}

func TestEnvFloat_Invalid_ReturnsDefault(t *testing.T) {
	t.Setenv("VATBRAIN_TEST_BAD_FLOAT", "abc")
	assert.InDelta(t, 3.14, envFloat("VATBRAIN_TEST_BAD_FLOAT", 3.14), 1e-9)
}

func TestEnvBool_Missing_ReturnsDefault(t *testing.T) {
	assert.True(t, envBool("VATBRAIN_TEST_BOOL_XYZ", true))
	assert.False(t, envBool("VATBRAIN_TEST_BOOL_XYZ2", false))
}

func TestEnvBool_Present_TrueValues(t *testing.T) {
	for _, v := range []string{"true", "True", "TRUE", "1", "t", "T"} {
		t.Setenv("VATBRAIN_TEST_BOOL", v)
		assert.True(t, envBool("VATBRAIN_TEST_BOOL", false), "value: %s", v)
	}
}

func TestEnvBool_Present_FalseValues(t *testing.T) {
	for _, v := range []string{"false", "False", "FALSE", "0", "f", "F"} {
		t.Setenv("VATBRAIN_TEST_BOOL", v)
		assert.False(t, envBool("VATBRAIN_TEST_BOOL", true), "value: %s", v)
	}
}

func TestEnvBool_Invalid_ReturnsDefault(t *testing.T) {
	t.Setenv("VATBRAIN_TEST_BAD_BOOL", "yes")
	assert.True(t, envBool("VATBRAIN_TEST_BAD_BOOL", true))
	assert.False(t, envBool("VATBRAIN_TEST_BAD_BOOL", false))
}

func TestLoadFromEnv_Defaults(t *testing.T) {
	cfg := LoadFromEnv()

	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "sqlite", cfg.Store.Backend)
	assert.Equal(t, "./vatbrain.db", cfg.Store.SQLite.Path)
	assert.True(t, cfg.Store.SQLite.WAL)

	assert.Equal(t, "bolt://localhost:7687", cfg.Neo4j.URI)
	assert.Equal(t, "neo4j", cfg.Neo4j.Username)
	assert.Equal(t, 100, cfg.Neo4j.MaxConnectionPoolSize)

	assert.Equal(t, "localhost", cfg.Pgvector.Host)
	assert.Equal(t, 5432, cfg.Pgvector.Port)
	assert.Equal(t, int32(20), cfg.Pgvector.MaxConns)

	assert.Equal(t, "localhost:6379", cfg.Redis.Addr)

	assert.Equal(t, "localhost:9000", cfg.Minio.Endpoint)
	assert.Equal(t, "minioadmin", cfg.Minio.AccessKey)
	assert.Equal(t, "vatbrain", cfg.Minio.Bucket)

	assert.InDelta(t, 0.1, cfg.WeightDecay.LambdaDecay, 1e-9)
	assert.InDelta(t, 0.01, cfg.WeightDecay.CoolingThreshold, 1e-9)

	assert.Equal(t, 2, cfg.SignificanceGate.MinCrossCycleCount)

	assert.InDelta(t, 0.85, cfg.PatternSeparation.SimilarityThreshold, 1e-9)
	assert.Equal(t, 500, cfg.Retrieval.MaxCandidates)

	assert.InDelta(t, 24, cfg.Consolidation.HoursToScan, 1e-9)
	assert.Equal(t, 3, cfg.Consolidation.MinClusterSize)

	assert.InDelta(t, 0.15, cfg.PitfallDecay.LambdaDecay, 1e-9)
	assert.InDelta(t, 0.005, cfg.PitfallDecay.CoolingThreshold, 1e-9)

	assert.True(t, cfg.Scheduler.Enabled)
	assert.Equal(t, "0 3 * * *", cfg.Scheduler.ConsolidationCron)

	// LLM defaults may be overwritten by env vars; just verify they exist.
	assert.NotEmpty(t, cfg.LLM.Model)

	// Watcher: hermes home defaults to empty → adapter falls back to ~/.hermes
	assert.Equal(t, "", cfg.Watcher.HermesHomeDir)
	assert.Equal(t, 300*time.Second, cfg.Watcher.PollInterval)
}

func TestLoadFromEnv_CustomValues(t *testing.T) {
	t.Setenv("PORT", "3000")
	t.Setenv("VATBRAIN_STORE_BACKEND", "memory")
	t.Setenv("WEIGHT_LAMBDA_DECAY", "0.25")
	t.Setenv("GATE_MIN_CROSS_CYCLE", "5")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("VATBRAIN_WATCHER_HERMES_HOME", "/tmp/hermes-profile")

	cfg := LoadFromEnv()
	assert.Equal(t, 3000, cfg.Port)
	assert.Equal(t, "memory", cfg.Store.Backend)
	assert.InDelta(t, 0.25, cfg.WeightDecay.LambdaDecay, 1e-9)
	assert.Equal(t, 5, cfg.SignificanceGate.MinCrossCycleCount)
	assert.Equal(t, "sk-test", cfg.LLM.APIKey)
	assert.Equal(t, "/tmp/hermes-profile", cfg.Watcher.HermesHomeDir)
}

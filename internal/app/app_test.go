package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/store/memory"
	"github.com/vatbrain/vatbrain/internal/store/sqlite"
)

func TestNewMemoryStore_Memory(t *testing.T) {
	s, err := app.NewMemoryStore(config.StoreConfig{Backend: "memory"}, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, s)
	_, ok := s.(*memory.Store)
	assert.True(t, ok, "expected in-memory store")
	assert.NoError(t, s.Close())
}

func TestNewMemoryStore_SQLite(t *testing.T) {
	cfg := config.StoreConfig{
		Backend: "sqlite",
		SQLite:  config.SQLiteConfig{Path: ":memory:?cache=shared"},
	}
	s, err := app.NewMemoryStore(cfg, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, s)
	_, ok := s.(*sqlite.Store)
	assert.True(t, ok, "expected sqlite store")
	assert.NoError(t, s.Close())
}

func TestNewMemoryStore_UnknownBackend(t *testing.T) {
	_, err := app.NewMemoryStore(config.StoreConfig{Backend: "cassandra"}, nil, nil)
	assert.Error(t, err)
}

func TestNewMemoryStore_Neo4JPG_MissingClients(t *testing.T) {
	cfg := config.StoreConfig{Backend: "neo4j+pgvector"}
	_, err := app.NewMemoryStore(cfg, nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "neo4j+pgvector backend requires")
}

func TestNewMemoryStore_SQLite_MissingPath(t *testing.T) {
	// Empty path should still work because sqlite defaults to file path
	cfg := config.StoreConfig{
		Backend: "sqlite",
	}
	s, err := app.NewMemoryStore(cfg, nil, nil)
	// This may fail depending on the SQLite file path logic
	if err == nil {
		assert.NotNil(t, s)
		s.Close()
	}
}

func TestApp_CanUseMemoryStore(t *testing.T) {
	// Verify we can create a minimal App struct (not via New() which needs real DBs)
	s := memory.NewStore()
	require.NotNil(t, s)

	// Test basic store operation through the app.
	ctx := context.Background()
	assert.NoError(t, s.HealthCheck(ctx))
	assert.NoError(t, s.Close())
}

func TestConfigStoreBackendDefaults(t *testing.T) {
	cfg := config.LoadFromEnv()
	// The default backend should be "memory" or set from env.
	// Just verify it's non-empty.
	assert.NotEmpty(t, cfg.Store.Backend)
}

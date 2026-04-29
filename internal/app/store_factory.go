package app

import (
	"fmt"

	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
	"github.com/vatbrain/vatbrain/internal/store/sqlite"
)

// NewMemoryStore creates the configured storage backend.
func NewMemoryStore(cfg config.StoreConfig) (store.MemoryStore, error) {
	switch cfg.Backend {
	case "sqlite":
		return sqlite.NewStore(cfg.SQLite)
	case "neo4j+pgvector":
		return nil, fmt.Errorf("neo4j+pgvector backend not yet implemented")
	case "memory":
		return memory.NewStore(), nil
	default:
		return nil, fmt.Errorf("unknown store backend: %s", cfg.Backend)
	}
}

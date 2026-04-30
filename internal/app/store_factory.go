package app

import (
	"context"
	"fmt"

	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/db/neo4j"
	"github.com/vatbrain/vatbrain/internal/db/pgvector"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
	"github.com/vatbrain/vatbrain/internal/store/neo4jpg"
	"github.com/vatbrain/vatbrain/internal/store/sqlite"
)

// NewMemoryStore creates the configured storage backend.
func NewMemoryStore(cfg config.StoreConfig, nc *neo4j.Client, pc *pgvector.Client) (store.MemoryStore, error) {
	switch cfg.Backend {
	case "sqlite":
		return sqlite.NewStore(cfg.SQLite)
	case "neo4j+pgvector":
		if nc == nil || pc == nil {
			return nil, fmt.Errorf("neo4j+pgvector backend requires neo4j and pgvector clients")
		}
		return neo4jpg.NewStore(context.Background(), nc, pc)
	case "memory":
		return memory.NewStore(), nil
	default:
		return nil, fmt.Errorf("unknown store backend: %s", cfg.Backend)
	}
}

package sqlite

import (
	"database/sql"
	"fmt"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS episodic_memories (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    language TEXT NOT NULL,
    task_type TEXT NOT NULL,
    summary TEXT NOT NULL,
    source_type TEXT NOT NULL,
    trust_level INTEGER NOT NULL DEFAULT 3,
    weight REAL NOT NULL DEFAULT 1.0,
    effective_frequency REAL NOT NULL DEFAULT 1.0,
    entity_group TEXT DEFAULT '',
    context_vector BLOB DEFAULT NULL,
    full_snapshot_uri TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    last_accessed_at TEXT,
    obsoleted_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_episodic_project ON episodic_memories(project_id, language);
CREATE INDEX IF NOT EXISTS idx_episodic_task ON episodic_memories(task_type);
CREATE INDEX IF NOT EXISTS idx_episodic_weight ON episodic_memories(weight DESC);

CREATE TABLE IF NOT EXISTS semantic_memories (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    content TEXT NOT NULL,
    source_type TEXT NOT NULL,
    trust_level INTEGER NOT NULL DEFAULT 3,
    weight REAL NOT NULL DEFAULT 1.0,
    effective_frequency REAL NOT NULL DEFAULT 1.0,
    entity_group TEXT DEFAULT '',
    consolidation_run_id TEXT DEFAULT '',
    backtest_accuracy REAL DEFAULT 0.0,
    source_episodic_ids TEXT DEFAULT '',
    context_vector BLOB DEFAULT NULL,
    created_at TEXT NOT NULL,
    last_accessed_at TEXT,
    obsoleted_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_semantic_type ON semantic_memories(type);
CREATE INDEX IF NOT EXISTS idx_semantic_project ON semantic_memories(entity_group);

CREATE TABLE IF NOT EXISTS memory_edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    edge_type TEXT NOT NULL,
    properties TEXT DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_edges_from ON memory_edges(from_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_to ON memory_edges(to_id, edge_type);

CREATE TABLE IF NOT EXISTS consolidation_runs (
    id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    episodics_scanned INTEGER DEFAULT 0,
    candidate_rules_found INTEGER DEFAULT 0,
    rules_persisted INTEGER DEFAULT 0,
    average_accuracy REAL DEFAULT 0.0
);
`

func migrate(db *sql.DB) error {
	_, err := db.Exec(schemaSQL)
	if err != nil {
		return fmt.Errorf("sqlite schema: %w", err)
	}
	return nil
}

func enableWAL(db *sql.DB) error {
	_, err := db.Exec("PRAGMA journal_mode=WAL")
	return err
}

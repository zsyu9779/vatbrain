package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
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
    is_correction INTEGER DEFAULT 0,
    surprise_score REAL NOT NULL DEFAULT 0,
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
    surprise_score REAL NOT NULL DEFAULT 0,
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
    average_accuracy REAL DEFAULT 0.0,
    pitfalls_extracted INTEGER DEFAULT 0,
    pitfalls_merged INTEGER DEFAULT 0,
    pitfalls_persisted INTEGER DEFAULT 0,
    rules_error TEXT DEFAULT '',
    pitfall_error TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS pitfall_memories (
    id TEXT PRIMARY KEY,
    entity_id TEXT NOT NULL,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('FUNCTION','MODULE','API','CONFIG','QUERY')),
    project_id TEXT NOT NULL,
    language TEXT NOT NULL,
    signature TEXT NOT NULL,
    signature_embedding BLOB,
    root_cause_category TEXT NOT NULL CHECK (root_cause_category IN ('CONCURRENCY','RESOURCE_EXHAUSTION','CONFIG','CONTRACT_VIOLATION','LOGIC_ERROR','UNKNOWN')),
    fix_strategy TEXT NOT NULL DEFAULT '',
    was_user_corrected INTEGER NOT NULL DEFAULT 0,
    occurrence_count INTEGER NOT NULL DEFAULT 1,
    last_occurred_at TEXT,
    source_type TEXT NOT NULL,
    trust_level INTEGER NOT NULL DEFAULT 3 CHECK (trust_level BETWEEN 1 AND 5),
    weight REAL NOT NULL DEFAULT 1.0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    obsoleted_at TEXT,
    source_episodic_ids TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'proposed',
    times_shown INTEGER NOT NULL DEFAULT 0,
    times_suppressed INTEGER NOT NULL DEFAULT 0,
    times_adopted INTEGER NOT NULL DEFAULT 0,
    protection_level INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_pitfall_entity ON pitfall_memories(entity_id, project_id);
CREATE INDEX IF NOT EXISTS idx_pitfall_project ON pitfall_memories(project_id, language);
CREATE INDEX IF NOT EXISTS idx_pitfall_weight ON pitfall_memories(weight DESC);

CREATE TABLE IF NOT EXISTS pitfall_edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    edge_type TEXT NOT NULL,
    properties TEXT DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pitfall_edges_from ON pitfall_edges(from_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_pitfall_edges_to ON pitfall_edges(to_id, edge_type);

CREATE TABLE IF NOT EXISTS rule_conflicts (
    id TEXT PRIMARY KEY,
    rule_a_id TEXT NOT NULL,
    rule_b_id TEXT NOT NULL,
    entity_group TEXT DEFAULT '',
    basis TEXT NOT NULL DEFAULT 'polarity',
    status TEXT NOT NULL DEFAULT 'pending',
    resolution TEXT DEFAULT '',
    reason TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    resolved_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_rule_conflicts_status ON rule_conflicts(status);
CREATE INDEX IF NOT EXISTS idx_rule_conflicts_entity ON rule_conflicts(entity_group);
`

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("sqlite schema: %w", err)
	}

	// Backfill for databases created before is_correction existed. SQLite
	// errors on a duplicate ALTER, so tolerate it as "already migrated".
	if _, err := db.Exec(`ALTER TABLE episodic_memories
		ADD COLUMN is_correction INTEGER DEFAULT 0`); err != nil && !isDuplicateColumnErr(err) {
		return fmt.Errorf("sqlite migrate is_correction: %w", err)
	}

	// v0.2.2 Pitfall Workbench: status + interference counters.
	for _, col := range []string{
		"status TEXT NOT NULL DEFAULT 'proposed'",
		"times_shown INTEGER NOT NULL DEFAULT 0",
		"times_suppressed INTEGER NOT NULL DEFAULT 0",
	} {
		if _, err := db.Exec("ALTER TABLE pitfall_memories ADD COLUMN " + col); err != nil &&
			!isDuplicateColumnErr(err) {
			return fmt.Errorf("sqlite migrate pitfall %s: %w", col, err)
		}
	}

	// v0.3 feedback loop: adopted counter + protection level.
	for _, col := range []string{
		"times_adopted INTEGER NOT NULL DEFAULT 0",
		"protection_level INTEGER NOT NULL DEFAULT 0",
	} {
		if _, err := db.Exec("ALTER TABLE pitfall_memories ADD COLUMN " + col); err != nil &&
			!isDuplicateColumnErr(err) {
			return fmt.Errorf("sqlite migrate pitfall %s: %w", col, err)
		}
	}

	// v0.3 Surprise Score (prediction-error signal): independent dimension that
	// extends the half-life of memories that broke an expectation.
	for table, col := range map[string]string{
		"episodic_memories": "surprise_score REAL NOT NULL DEFAULT 0",
		"semantic_memories": "surprise_score REAL NOT NULL DEFAULT 0",
	} {
		if _, err := db.Exec("ALTER TABLE " + table + " ADD COLUMN " + col); err != nil &&
			!isDuplicateColumnErr(err) {
			return fmt.Errorf("sqlite migrate %s surprise_score: %w", table, err)
		}
	}

	// v0.4 temporal attribute (ticket 02): when the event happened. The column
	// and index live outside schemaSQL so existing databases — whose
	// episodic_memories predates the column — migrate in-place (an index on a
	// missing column inside schemaSQL would fail before the ALTER runs).
	if _, err := db.Exec(`ALTER TABLE episodic_memories
		ADD COLUMN occurred_at TEXT`); err != nil && !isDuplicateColumnErr(err) {
		return fmt.Errorf("sqlite migrate occurred_at: %w", err)
	}
	// Backfill: events written before occurred_at existed occurred at write
	// time. Idempotent — only fills rows still missing the value.
	if _, err := db.Exec(`UPDATE episodic_memories
		SET occurred_at = created_at WHERE occurred_at IS NULL`); err != nil {
		return fmt.Errorf("sqlite migrate occurred_at backfill: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_episodic_occurred
		ON episodic_memories(occurred_at DESC)`); err != nil {
		return fmt.Errorf("sqlite migrate occurred_at index: %w", err)
	}

	return nil
}

// isDuplicateColumnErr reports whether err is SQLite's "duplicate column name"
// error, which is expected when the column already exists.
func isDuplicateColumnErr(err error) bool {
	if err == nil {
		return false
	}
	// modernc.org/sqlite returns errors containing the SQLite message.
	return strings.Contains(err.Error(), "duplicate column name")
}

func enableWAL(db *sql.DB) error {
	_, err := db.Exec("PRAGMA journal_mode=WAL")
	return err
}

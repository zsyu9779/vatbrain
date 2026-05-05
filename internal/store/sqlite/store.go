// Package sqlite implements store.MemoryStore using a local SQLite database.
// It supports zero-external-process startup while preserving full vector-search
// capability via in-process cosine similarity.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/lru"
	"github.com/vatbrain/vatbrain/internal/vector"

	_ "modernc.org/sqlite"
)

// Store implements store.MemoryStore backed by a local SQLite database.
type Store struct {
	db       *sql.DB
	mu       sync.Mutex
	hotCache *lru.HotCache[string, []models.EpisodicMemory]
}

// NewStore creates a new SQLite-backed MemoryStore.
func NewStore(cfg config.SQLiteConfig) (*Store, error) {
	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite single-writer constraint

	if cfg.WAL {
		if err := enableWAL(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("sqlite: wal: %w", err)
		}
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	s := &Store{
		db:       db,
		hotCache: lru.NewHotCache[string, []models.EpisodicMemory](128, 5*time.Minute),
	}

	return s, nil
}

// ── Episodic Memory ─────────────────────────────────────────────────────────

// WriteEpisodic stores an episodic memory.
func (s *Store) WriteEpisodic(_ context.Context, mem *models.EpisodicMemory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	la := now
	if mem.LastAccessedAt != nil {
		la = *mem.LastAccessedAt
	}

	var cvBlob any
	if len(mem.ContextVector) > 0 {
		cvBlob = vector.Encode(vector.Float32To64(mem.ContextVector))
	}

	var obsoleted any
	if mem.ObsoletedAt != nil {
		obsoleted = mem.ObsoletedAt.UTC().Format(time.RFC3339)
	}

	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO episodic_memories
			(id, project_id, language, task_type, summary, source_type,
			 trust_level, weight, effective_frequency, entity_group,
			 context_vector, full_snapshot_uri,
			 created_at, last_accessed_at, obsoleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		mem.ID.String(),
		mem.ProjectID,
		mem.Language,
		string(mem.TaskType),
		mem.Summary,
		string(mem.SourceType),
		int(mem.TrustLevel),
		mem.Weight,
		mem.EffectiveFrequency,
		mem.EntityGroup,
		cvBlob,
		mem.FullSnapshotURI,
		mem.CreatedAt.UTC().Format(time.RFC3339),
		la.UTC().Format(time.RFC3339),
		obsoleted,
	)
	return err
}

// GetEpisodic retrieves a single episodic memory by ID.
func (s *Store) GetEpisodic(_ context.Context, id uuid.UUID) (*models.EpisodicMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`
		SELECT id, project_id, language, task_type, summary, source_type,
		       trust_level, weight, effective_frequency, entity_group,
		       context_vector, full_snapshot_uri,
		       created_at, last_accessed_at, obsoleted_at
		FROM episodic_memories WHERE id = ?
	`, id.String())

	return scanEpisodic(row)
}

// TouchEpisodic updates the last-accessed timestamp.
func (s *Store) TouchEpisodic(_ context.Context, id uuid.UUID, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE episodic_memories SET last_accessed_at = ? WHERE id = ?`,
		now.UTC().Format(time.RFC3339), id.String())
	return err
}

// UpdateEpisodicWeight updates the weight and effective frequency.
func (s *Store) UpdateEpisodicWeight(_ context.Context, id uuid.UUID, weight, effFreq float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE episodic_memories SET weight = ?, effective_frequency = ? WHERE id = ?`,
		weight, effFreq, id.String())
	return err
}

// MarkObsolete marks a memory as obsolete.
func (s *Store) MarkObsolete(_ context.Context, id uuid.UUID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE episodic_memories SET obsoleted_at = ? WHERE id = ?`,
		at.UTC().Format(time.RFC3339), id.String())
	return err
}

// ── Semantic Memory ─────────────────────────────────────────────────────────

// WriteSemantic stores a semantic memory.
func (s *Store) WriteSemantic(_ context.Context, mem *models.SemanticMemory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	srcIDs, _ := json.Marshal(mem.SourceEpisodicIDs)
	var obsoleted any
	if mem.ObsoletedAt != nil {
		obsoleted = mem.ObsoletedAt.UTC().Format(time.RFC3339)
	}
	la := time.Now().UTC()
	if mem.LastAccessedAt != nil {
		la = *mem.LastAccessedAt
	}

	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO semantic_memories
			(id, type, content, source_type, trust_level, weight,
			 effective_frequency, entity_group, consolidation_run_id,
			 backtest_accuracy, source_episodic_ids,
			 created_at, last_accessed_at, obsoleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		mem.ID.String(),
		string(mem.Type),
		mem.Content,
		string(mem.SourceType),
		int(mem.TrustLevel),
		mem.Weight,
		mem.EffectiveFrequency,
		mem.EntityGroup,
		mem.ConsolidationRunID,
		mem.BacktestAccuracy,
		string(srcIDs),
		mem.CreatedAt.UTC().Format(time.RFC3339),
		la.UTC().Format(time.RFC3339),
		obsoleted,
	)
	return err
}

// GetSemantic retrieves a single semantic memory by ID.
func (s *Store) GetSemantic(_ context.Context, id uuid.UUID) (*models.SemanticMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`
		SELECT id, type, content, source_type, trust_level, weight,
		       effective_frequency, entity_group, consolidation_run_id,
		       backtest_accuracy, source_episodic_ids,
		       created_at, last_accessed_at, obsoleted_at
		FROM semantic_memories WHERE id = ?
	`, id.String())

	return scanSemantic(row)
}

// ── Edges ───────────────────────────────────────────────────────────────────

// CreateEdge creates a directed edge between two memory nodes.
func (s *Store) CreateEdge(_ context.Context, from, to uuid.UUID, edgeType string, props map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	propsJSON := "{}"
	if len(props) > 0 {
		b, _ := json.Marshal(props)
		propsJSON = string(b)
	}

	_, err := s.db.Exec(`
		INSERT INTO memory_edges (from_id, to_id, edge_type, properties, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, from.String(), to.String(), edgeType, propsJSON, time.Now().UTC().Format(time.RFC3339))
	return err
}

// GetEdges retrieves edges for a node. direction can be "out", "in", or "" (both).
func (s *Store) GetEdges(_ context.Context, nodeID uuid.UUID, edgeType, direction string) ([]store.Edge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `SELECT from_id, to_id, edge_type, properties, created_at FROM memory_edges WHERE `
	args := []any{}
	switch direction {
	case "out":
		query += `from_id = ?`
		args = append(args, nodeID.String())
	case "in":
		query += `to_id = ?`
		args = append(args, nodeID.String())
	default:
		query += `(from_id = ? OR to_id = ?)`
		args = append(args, nodeID.String(), nodeID.String())
	}
	if edgeType != "" {
		query += ` AND edge_type = ?`
		args = append(args, edgeType)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []store.Edge
	for rows.Next() {
		var e store.Edge
		var fromStr, toStr, createdAtStr, propsStr string
		if err := rows.Scan(&fromStr, &toStr, &e.EdgeType, &propsStr, &createdAtStr); err != nil {
			return nil, err
		}
		e.FromID, _ = uuid.Parse(fromStr)
		e.ToID, _ = uuid.Parse(toStr)
		e.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		if propsStr != "" && propsStr != "{}" {
			json.Unmarshal([]byte(propsStr), &e.Properties)
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// ── Consolidation ───────────────────────────────────────────────────────────

// ScanRecent returns episodic memories created since a given time.
func (s *Store) ScanRecent(_ context.Context, since time.Time, limit int) ([]store.EpisodicScanItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT id, summary, task_type, project_id, language, entity_group, entity_group AS entity_id,
		       weight, last_accessed_at
		FROM episodic_memories
		WHERE created_at >= ? AND obsoleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT ?
	`, since.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []store.EpisodicScanItem
	for rows.Next() {
		var item store.EpisodicScanItem
		var idStr, taskTypeStr, laStr string
		var entityID string
		if err := rows.Scan(&idStr, &item.Summary, &taskTypeStr,
			&item.ProjectID, &item.Language, &item.EntityGroup,
			&entityID, &item.Weight, &laStr); err != nil {
			return nil, err
		}
		item.EntityID = entityID
		item.ID, _ = uuid.Parse(idStr)
		item.TaskType = models.TaskType(taskTypeStr)
		item.LastAccessed, _ = time.Parse(time.RFC3339, laStr)
		items = append(items, item)
	}
	return items, rows.Err()
}

// SaveConsolidationRun stores a consolidation run result.
func (s *Store) SaveConsolidationRun(_ context.Context, run *models.ConsolidationRunResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var completedAt any
	if run.CompletedAt != nil {
		completedAt = run.CompletedAt.UTC().Format(time.RFC3339)
	}

	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO consolidation_runs
				(id, started_at, completed_at, episodics_scanned,
				 candidate_rules_found, rules_persisted, average_accuracy,
				 pitfalls_extracted, pitfalls_merged, pitfalls_persisted,
				 rules_error, pitfall_error)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.RunID.String(),
		run.StartedAt.UTC().Format(time.RFC3339),
		completedAt,
		run.EpisodicsScanned,
		run.CandidateRulesFound,
		run.RulesPersisted,
		run.AverageAccuracy,
			run.PitfallsExtracted,
			run.PitfallsMerged,
			run.PitfallsPersisted,
			run.RulesError,
			run.PitfallError,
	)
	return err
}

// GetConsolidationRun retrieves a consolidation run result.
func (s *Store) GetConsolidationRun(_ context.Context, runID uuid.UUID) (*models.ConsolidationRunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var run models.ConsolidationRunResult
	var idStr, startedStr string
	var completedStr *string

	err := s.db.QueryRow(`
		SELECT id, started_at, completed_at, episodics_scanned,
			       candidate_rules_found, rules_persisted, average_accuracy,
			       pitfalls_extracted, pitfalls_merged, pitfalls_persisted,
			       rules_error, pitfall_error
		FROM consolidation_runs WHERE id = ?
	`, runID.String()).Scan(
		&idStr, &startedStr, &completedStr,
		&run.EpisodicsScanned, &run.CandidateRulesFound,
			&run.RulesPersisted, &run.AverageAccuracy,
			&run.PitfallsExtracted, &run.PitfallsMerged,
			&run.PitfallsPersisted, &run.RulesError,
			&run.PitfallError,
	)
	if err != nil {
		return nil, err
	}

	run.RunID, _ = uuid.Parse(idStr)
	run.StartedAt, _ = time.Parse(time.RFC3339, startedStr)
	if completedStr != nil {
		t, _ := time.Parse(time.RFC3339, *completedStr)
		run.CompletedAt = &t
	}
	return &run, nil
}

// ── Lifecycle ───────────────────────────────────────────────────────────────

// HealthCheck verifies the SQLite database is reachable.
func (s *Store) HealthCheck(_ context.Context) error {
	return s.db.Ping()
}

// Close closes the SQLite database.
func (s *Store) Close() error {
	return s.db.Close()
}

// ── Internal Helpers ────────────────────────────────────────────────────────

func scanEpisodic(row *sql.Row) (*models.EpisodicMemory, error) {
	var m models.EpisodicMemory
	var idStr, taskTypeStr, sourceTypeStr string
	var createdAtStr, laStr string
	var obsoletedStr *string
	var cvBlob []byte

	err := row.Scan(
		&idStr, &m.ProjectID, &m.Language, &taskTypeStr, &m.Summary, &sourceTypeStr,
		&m.TrustLevel, &m.Weight, &m.EffectiveFrequency, &m.EntityGroup,
		&cvBlob, &m.FullSnapshotURI,
		&createdAtStr, &laStr, &obsoletedStr,
	)
	if err != nil {
		return nil, err
	}

	m.ID, _ = uuid.Parse(idStr)
	m.TaskType = models.TaskType(taskTypeStr)
	m.SourceType = models.SourceType(sourceTypeStr)
	m.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	if t, err := time.Parse(time.RFC3339, laStr); err == nil {
		m.LastAccessedAt = &t
	}
	if obsoletedStr != nil {
		t, _ := time.Parse(time.RFC3339, *obsoletedStr)
		m.ObsoletedAt = &t
	}
	if len(cvBlob) > 0 {
		m.ContextVector = vector.Float64To32(vector.Decode(cvBlob))
	}
	return &m, nil
}

func scanSemantic(row *sql.Row) (*models.SemanticMemory, error) {
	var m models.SemanticMemory
	var idStr, typeStr, sourceTypeStr string
	var createdAtStr, laStr string
	var obsoletedStr *string
	var srcIDsStr string

	err := row.Scan(
		&idStr, &typeStr, &m.Content, &sourceTypeStr, &m.TrustLevel, &m.Weight,
		&m.EffectiveFrequency, &m.EntityGroup, &m.ConsolidationRunID,
		&m.BacktestAccuracy, &srcIDsStr,
		&createdAtStr, &laStr, &obsoletedStr,
	)
	if err != nil {
		return nil, err
	}

	m.ID, _ = uuid.Parse(idStr)
	m.Type = models.MemoryType(typeStr)
	m.SourceType = models.SourceType(sourceTypeStr)
	m.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	if t, err := time.Parse(time.RFC3339, laStr); err == nil {
		m.LastAccessedAt = &t
	}
	if obsoletedStr != nil {
		t, _ := time.Parse(time.RFC3339, *obsoletedStr)
		m.ObsoletedAt = &t
	}
	if srcIDsStr != "" {
		json.Unmarshal([]byte(srcIDsStr), &m.SourceEpisodicIDs)
	}
	return &m, nil
}

func scanEpisodicRows(rows *sql.Rows) ([]models.EpisodicMemory, error) {
	var results []models.EpisodicMemory
	for rows.Next() {
		var m models.EpisodicMemory
		var idStr, taskTypeStr, sourceTypeStr string
		var createdAtStr, laStr string
		var obsoletedStr *string
		var cvBlob []byte

		err := rows.Scan(
			&idStr, &m.ProjectID, &m.Language, &taskTypeStr, &m.Summary, &sourceTypeStr,
			&m.TrustLevel, &m.Weight, &m.EffectiveFrequency, &m.EntityGroup,
			&cvBlob, &m.FullSnapshotURI,
			&createdAtStr, &laStr, &obsoletedStr,
		)
		if err != nil {
			return nil, err
		}
		m.ID, _ = uuid.Parse(idStr)
		m.TaskType = models.TaskType(taskTypeStr)
		m.SourceType = models.SourceType(sourceTypeStr)
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		if t, err := time.Parse(time.RFC3339, laStr); err == nil {
			m.LastAccessedAt = &t
		}
		if obsoletedStr != nil {
			t, _ := time.Parse(time.RFC3339, *obsoletedStr)
			m.ObsoletedAt = &t
		}
		if len(cvBlob) > 0 {
			m.ContextVector = vector.Float64To32(vector.Decode(cvBlob))
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

func scanSemanticRows(rows *sql.Rows) ([]models.SemanticMemory, error) {
	var results []models.SemanticMemory
	for rows.Next() {
		var m models.SemanticMemory
		var idStr, typeStr, sourceTypeStr string
		var createdAtStr, laStr string
		var obsoletedStr *string
		var srcIDsStr string

		err := rows.Scan(
			&idStr, &typeStr, &m.Content, &sourceTypeStr, &m.TrustLevel, &m.Weight,
			&m.EffectiveFrequency, &m.EntityGroup, &m.ConsolidationRunID,
			&m.BacktestAccuracy, &srcIDsStr,
			&createdAtStr, &laStr, &obsoletedStr,
		)
		if err != nil {
			return nil, err
		}
		m.ID, _ = uuid.Parse(idStr)
		m.Type = models.MemoryType(typeStr)
		m.SourceType = models.SourceType(sourceTypeStr)
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		if t, err := time.Parse(time.RFC3339, laStr); err == nil {
			m.LastAccessedAt = &t
		}
		if obsoletedStr != nil {
			t, _ := time.Parse(time.RFC3339, *obsoletedStr)
			m.ObsoletedAt = &t
		}
		if srcIDsStr != "" {
			json.Unmarshal([]byte(srcIDsStr), &m.SourceEpisodicIDs)
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

// ── Import Aliases (to avoid dot imports in search.go) ──────────────────────

// these are aliased within the package
var _ = vector.CosineSimilarity
var _ = vector.Float32To64
var _ = vector.Float64To32
var _ = vector.Encode
var _ = vector.Decode

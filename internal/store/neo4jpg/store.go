// Package neo4jpg implements store.MemoryStore backed by Neo4j (graph) and
// pgvector (vector embeddings). It maps the MemoryStore interface to Cypher
// queries on Neo4j nodes/edges and vector operations on pgvector.
package neo4jpg

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	neodriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/vatbrain/vatbrain/internal/db/neo4j"
	"github.com/vatbrain/vatbrain/internal/db/pgvector"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// Store implements store.MemoryStore on Neo4j + pgvector.
type Store struct {
	neo4j *neo4j.Client
	pg    *pgvector.Client
}

// NewStore creates a new Neo4j+pgvector store and runs idempotent schema setup.
func NewStore(ctx context.Context, nc *neo4j.Client, pc *pgvector.Client) (*Store, error) {
	s := &Store{neo4j: nc, pg: pc}
	if err := s.setupSchema(ctx); err != nil {
		return nil, fmt.Errorf("neo4jpg: schema setup: %w", err)
	}
	return s, nil
}

// setupSchema creates uniqueness constraints idempotently.
func (s *Store) setupSchema(ctx context.Context) error {
	constraints := []string{
		"CREATE CONSTRAINT IF NOT EXISTS FOR (e:EpisodicMemory) REQUIRE e.id IS UNIQUE",
		"CREATE CONSTRAINT IF NOT EXISTS FOR (m:SemanticMemory) REQUIRE m.id IS UNIQUE",
			"CREATE CONSTRAINT IF NOT EXISTS FOR (p:PitfallMemory) REQUIRE p.id IS UNIQUE",
		"CREATE CONSTRAINT IF NOT EXISTS FOR (c:ConsolidationRun) REQUIRE c.run_id IS UNIQUE",
	}
	for _, cypher := range constraints {
		if _, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
			_, err := tx.Run(ctx, cypher, nil)
			return nil, err
		}); err != nil {
			return fmt.Errorf("constraint: %w", err)
		}
	}
	return nil
}

// ── Episodic Memory ─────────────────────────────────────────────────────────

// WriteEpisodic creates an (:EpisodicMemory) node and inserts the embedding
// into pgvector (best-effort).
func (s *Store) WriteEpisodic(ctx context.Context, mem *models.EpisodicMemory) error {
	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		params := map[string]any{
			"id":              mem.ID.String(),
			"projectID":       mem.ProjectID,
			"language":        mem.Language,
			"taskType":        string(mem.TaskType),
			"summary":         mem.Summary,
			"sourceType":      string(mem.SourceType),
			"trustLevel":      int64(mem.TrustLevel),
			"weight":          mem.Weight,
			"effFreq":         mem.EffectiveFrequency,
			"createdAt":       mem.CreatedAt,
			"entityGroup":     mem.EntityGroup,
			"embeddingID":     mem.EmbeddingID,
			"fullSnapshotURI": mem.FullSnapshotURI,
		}
		if mem.LastAccessedAt != nil {
			params["lastAccessedAt"] = *mem.LastAccessedAt
		} else {
			params["lastAccessedAt"] = nil
		}
		if mem.ObsoletedAt != nil {
			params["obsoletedAt"] = *mem.ObsoletedAt
		} else {
			params["obsoletedAt"] = nil
		}

		_, err := tx.Run(ctx, `
			CREATE (e:EpisodicMemory {
				id: $id,
				project_id: $projectID,
				language: $language,
				task_type: $taskType,
				summary: $summary,
				source_type: $sourceType,
				trust_level: $trustLevel,
				weight: $weight,
				effective_frequency: $effFreq,
				created_at: $createdAt,
				last_accessed_at: $lastAccessedAt,
				obsoleted_at: $obsoletedAt,
				entity_group: $entityGroup,
				embedding_id: $embeddingID,
				full_snapshot_uri: $fullSnapshotURI
			})
		`, params)
		return nil, err
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: write episodic: %w", err)
	}

	// Best-effort pgvector insert.
	if len(mem.ContextVector) > 0 {
		if pgErr := s.pg.InsertEmbedding(ctx, mem.ID.String(), mem.ContextVector,
			mem.Summary, mem.ProjectID, mem.Language, string(mem.TaskType),
			map[string]any{"entity_id": mem.EntityGroup}); pgErr != nil {
			slog.Warn("neo4jpg: pgvector insert failed, continuing",
				"memory_id", mem.ID.String(), "err", pgErr)
		}
	}
	return nil
}

// GetEpisodic retrieves a single episodic memory by ID.
func (s *Store) GetEpisodic(ctx context.Context, id uuid.UUID) (*models.EpisodicMemory, error) {
	raw, err := s.neo4j.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx,
			`MATCH (e:EpisodicMemory {id: $id}) RETURN e`, map[string]any{"id": id.String()})
		if runErr != nil {
			return nil, runErr
		}
		if !records.Next(ctx) {
			return nil, records.Err()
		}
		return scanEpisodic(records.Record(), "e")
	})
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: get episodic: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("neo4jpg: episodic memory %s not found", id)
	}
	mem := raw.(*models.EpisodicMemory)

	// Best-effort: attach embedding from pgvector.
	if vec, err := s.pg.GetEmbedding(ctx, id.String()); err == nil {
		mem.ContextVector = vec
	}
	return mem, nil
}

// SearchEpisodic finds episodic memories matching the request criteria.
// If an embedding is provided, uses pgvector similarity search first, then
// fetches matching nodes from Neo4j. Otherwise falls back to structured query.
func (s *Store) SearchEpisodic(ctx context.Context, req store.EpisodicSearchRequest) ([]models.EpisodicMemory, error) {
	if len(req.Embedding) > 0 {
		return s.searchEpisodicWithEmbedding(ctx, req)
	}
	return s.searchEpisodicStructured(ctx, req)
}

// searchEpisodicWithEmbedding uses pgvector → Neo4j by ID.
func (s *Store) searchEpisodicWithEmbedding(ctx context.Context, req store.EpisodicSearchRequest) ([]models.EpisodicMemory, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	f32Emb := vector.Float64To32(req.Embedding)
	pgResults, err := s.pg.SimilaritySearch(ctx, f32Emb, limit*3, nil)
	if err != nil {
		slog.Warn("neo4jpg: pgvector search failed, falling back to structured",
			"err", err)
		return s.searchEpisodicStructured(ctx, req)
	}
	if len(pgResults) == 0 {
		return nil, nil
	}

	ids := make([]string, len(pgResults))
	scoreByID := make(map[string]float64, len(pgResults))
	for i, r := range pgResults {
		ids[i] = r.MemoryID
		scoreByID[r.MemoryID] = r.Similarity
	}

	raw, err := s.neo4j.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx,
			`MATCH (e:EpisodicMemory)
			 WHERE e.id IN $ids
			 RETURN e`, map[string]any{"ids": ids})
		if runErr != nil {
			return nil, runErr
		}
		var results []*models.EpisodicMemory
		for records.Next(ctx) {
			mem, scanErr := scanEpisodic(records.Record(), "e")
			if scanErr != nil {
				return nil, scanErr
			}
			results = append(results, mem)
		}
		return results, records.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: search episodic by ids: %w", err)
	}

	memories := raw.([]*models.EpisodicMemory)

	// Apply optional filters not covered by pgvector.
	var filtered []*models.EpisodicMemory
	for _, m := range memories {
		if req.ProjectID != "" && m.ProjectID != req.ProjectID {
			continue
		}
		if req.Language != "" && m.Language != req.Language {
			continue
		}
		if req.TaskType != "" && m.TaskType != req.TaskType {
			continue
		}
		if m.Weight < req.MinWeight {
			continue
		}
		if !req.IncludeObsolete && m.ObsoletedAt != nil {
			continue
		}
		filtered = append(filtered, m)
	}

	// Sort by pgvector similarity score descending.
	sort.Slice(filtered, func(i, j int) bool {
		return scoreByID[filtered[i].ID.String()] > scoreByID[filtered[j].ID.String()]
	})

	if limit < len(filtered) {
		filtered = filtered[:limit]
	}

	result := make([]models.EpisodicMemory, len(filtered))
	for i, m := range filtered {
		result[i] = *m
	}
	return result, nil
}

// searchEpisodicStructured runs a pure Cypher query.
func (s *Store) searchEpisodicStructured(ctx context.Context, req store.EpisodicSearchRequest) ([]models.EpisodicMemory, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	var clauses []string
	params := map[string]any{"limit": limit}

	if req.ProjectID != "" {
		clauses = append(clauses, "e.project_id = $projectID")
		params["projectID"] = req.ProjectID
	}
	if req.Language != "" {
		clauses = append(clauses, "e.language = $language")
		params["language"] = req.Language
	}
	if req.TaskType != "" {
		clauses = append(clauses, "e.task_type = $taskType")
		params["taskType"] = string(req.TaskType)
	}
	if req.MinWeight > 0 {
		clauses = append(clauses, "e.weight >= $minWeight")
		params["minWeight"] = req.MinWeight
	}
	if !req.IncludeObsolete {
		clauses = append(clauses, "e.obsoleted_at IS NULL")
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}

	query := fmt.Sprintf(`
		MATCH (e:EpisodicMemory)
		%s
		RETURN e
		ORDER BY e.weight DESC
		LIMIT $limit
	`, where)

	raw, err := s.neo4j.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx, query, params)
		if runErr != nil {
			return nil, runErr
		}
		var results []*models.EpisodicMemory
		for records.Next(ctx) {
			mem, scanErr := scanEpisodic(records.Record(), "e")
			if scanErr != nil {
				return nil, scanErr
			}
			results = append(results, mem)
		}
		return results, records.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: search episodic: %w", err)
	}

	memories := raw.([]*models.EpisodicMemory)
	result := make([]models.EpisodicMemory, len(memories))
	for i, m := range memories {
		result[i] = *m
	}
	return result, nil
}

// TouchEpisodic updates the last-accessed-at timestamp.
func (s *Store) TouchEpisodic(ctx context.Context, id uuid.UUID, now time.Time) error {
	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx,
			`MATCH (e:EpisodicMemory {id: $id})
			 SET e.last_accessed_at = $now`,
			map[string]any{"id": id.String(), "now": now})
		return nil, runErr
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: touch episodic: %w", err)
	}
	return nil
}

// UpdateEpisodicWeight updates the weight and effective frequency.
func (s *Store) UpdateEpisodicWeight(ctx context.Context, id uuid.UUID, weight, effFreq float64) error {
	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx,
			`MATCH (e:EpisodicMemory {id: $id})
			 SET e.weight = $weight, e.effective_frequency = $effFreq`,
			map[string]any{"id": id.String(), "weight": weight, "effFreq": effFreq})
		return nil, runErr
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: update episodic weight: %w", err)
	}
	return nil
}

// MarkObsolete sets obsoleted_at on an episodic memory.
func (s *Store) MarkObsolete(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx,
			`MATCH (e:EpisodicMemory {id: $id})
			 SET e.obsoleted_at = $at`,
			map[string]any{"id": id.String(), "at": at})
		return nil, runErr
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: mark obsolete: %w", err)
	}
	return nil
}

// ── Semantic Memory ─────────────────────────────────────────────────────────

// WriteSemantic creates a (:SemanticMemory) node.
func (s *Store) WriteSemantic(ctx context.Context, mem *models.SemanticMemory) error {
	srcIDs := make([]string, len(mem.SourceEpisodicIDs))
	for i, id := range mem.SourceEpisodicIDs {
		srcIDs[i] = id.String()
	}

	params := map[string]any{
		"id":                  mem.ID.String(),
		"type":                string(mem.Type),
		"content":             mem.Content,
		"sourceType":          string(mem.SourceType),
		"trustLevel":          int64(mem.TrustLevel),
		"weight":              mem.Weight,
		"effFreq":             mem.EffectiveFrequency,
		"createdAt":           mem.CreatedAt,
		"entityGroup":         mem.EntityGroup,
		"consolidationRunID":  mem.ConsolidationRunID,
		"backtestAccuracy":    mem.BacktestAccuracy,
		"sourceEpisodicIDs":   srcIDs,
	}
	if mem.LastAccessedAt != nil {
		params["lastAccessedAt"] = *mem.LastAccessedAt
	} else {
		params["lastAccessedAt"] = nil
	}
	if mem.ObsoletedAt != nil {
		params["obsoletedAt"] = *mem.ObsoletedAt
	} else {
		params["obsoletedAt"] = nil
	}

	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx, `
			CREATE (m:SemanticMemory {
				id: $id,
				type: $type,
				content: $content,
				source_type: $sourceType,
				trust_level: $trustLevel,
				weight: $weight,
				effective_frequency: $effFreq,
				created_at: $createdAt,
				last_accessed_at: $lastAccessedAt,
				obsoleted_at: $obsoletedAt,
				entity_group: $entityGroup,
				consolidation_run_id: $consolidationRunID,
				backtest_accuracy: $backtestAccuracy,
				source_episodic_ids: $sourceEpisodicIDs
			})
		`, params)
		return nil, runErr
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: write semantic: %w", err)
	}
	return nil
}

// GetSemantic retrieves a single semantic memory by ID.
func (s *Store) GetSemantic(ctx context.Context, id uuid.UUID) (*models.SemanticMemory, error) {
	raw, err := s.neo4j.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx,
			`MATCH (m:SemanticMemory {id: $id}) RETURN m`, map[string]any{"id": id.String()})
		if runErr != nil {
			return nil, runErr
		}
		if !records.Next(ctx) {
			return nil, records.Err()
		}
		return scanSemantic(records.Record(), "m")
	})
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: get semantic: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("neo4jpg: semantic memory %s not found", id)
	}
	return raw.(*models.SemanticMemory), nil
}

// SearchSemantic finds semantic memories matching the request criteria.
func (s *Store) SearchSemantic(ctx context.Context, req store.SemanticSearchRequest) ([]models.SemanticMemory, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	var clauses []string
	params := map[string]any{"limit": limit}

	if req.MemoryType != "" {
		clauses = append(clauses, "m.type = $type")
		params["type"] = string(req.MemoryType)
	}
	if req.ProjectID != "" {
		clauses = append(clauses, "m.entity_group = $projectID")
		params["projectID"] = req.ProjectID
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}

	query := fmt.Sprintf(`
		MATCH (m:SemanticMemory)
		%s
		RETURN m
		ORDER BY m.weight DESC
		LIMIT $limit
	`, where)

	raw, err := s.neo4j.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx, query, params)
		if runErr != nil {
			return nil, runErr
		}
		var results []*models.SemanticMemory
		for records.Next(ctx) {
			mem, scanErr := scanSemantic(records.Record(), "m")
			if scanErr != nil {
				return nil, scanErr
			}
			results = append(results, mem)
		}
		return results, records.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: search semantic: %w", err)
	}

	memories := raw.([]*models.SemanticMemory)
	result := make([]models.SemanticMemory, len(memories))
	for i, m := range memories {
		result[i] = *m
	}
	return result, nil
}

// ── Edges ───────────────────────────────────────────────────────────────────

// CreateEdge creates a directed relationship between two memory nodes.
// The edgeType is inserted via fmt.Sprintf — safe because all callers use
// internal constants (RELATES_TO, DERIVED_FROM, etc.).
func (s *Store) CreateEdge(ctx context.Context, from, to uuid.UUID, edgeType string, props map[string]any) error {
	query := fmt.Sprintf(`
		MATCH (a {id: $fromID})
		MATCH (b {id: $toID})
		CREATE (a)-[r:%s]->(b)
		SET r = $props
	`, edgeType)

	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx, query, map[string]any{
			"fromID": from.String(),
			"toID":   to.String(),
			"props":  props,
		})
		return nil, runErr
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: create edge: %w", err)
	}
	return nil
}

// GetEdges retrieves relationships for a node, optionally filtered by type and
// direction ("out", "in", or "" for both).
func (s *Store) GetEdges(ctx context.Context, nodeID uuid.UUID, edgeType string, direction string) ([]store.Edge, error) {
	var allEdges []store.Edge

	if direction == "" || direction == "out" {
		edges, err := s.getEdgesOneDir(ctx, nodeID, edgeType, "out")
		if err != nil {
			return nil, err
		}
		allEdges = append(allEdges, edges...)
	}

	if direction == "" || direction == "in" {
		edges, err := s.getEdgesOneDir(ctx, nodeID, edgeType, "in")
		if err != nil {
			return nil, err
		}
		allEdges = append(allEdges, edges...)
	}

	return allEdges, nil
}

func (s *Store) getEdgesOneDir(ctx context.Context, nodeID uuid.UUID, edgeType, direction string) ([]store.Edge, error) {
	var query string
	params := map[string]any{"nodeID": nodeID.String()}

	if direction == "out" {
		query = `MATCH (a {id: $nodeID})-[r]->(b)`
	} else {
		query = `MATCH (a)-[r]->(b {id: $nodeID})`
	}

	if edgeType != "" {
		query += ` WHERE type(r) = $edgeType`
		params["edgeType"] = edgeType
	}

	query += ` RETURN a.id AS fromID, b.id AS toID, type(r) AS edgeType, properties(r) AS props`

	raw, err := s.neo4j.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx, query, params)
		if runErr != nil {
			return nil, runErr
		}
		var edges []store.Edge
		for records.Next(ctx) {
			r := records.Record()
			fromID, _, _ := neodriver.GetRecordValue[string](r, "fromID")
			toID, _, _ := neodriver.GetRecordValue[string](r, "toID")
			etype, _, _ := neodriver.GetRecordValue[string](r, "edgeType")
			eprops, _, _ := neodriver.GetRecordValue[map[string]any](r, "props")

			fromUUID, err := uuid.Parse(fromID)
			if err != nil {
				continue
			}
			toUUID, err := uuid.Parse(toID)
			if err != nil {
				continue
			}
			edges = append(edges, store.Edge{
				FromID:     fromUUID,
				ToID:       toUUID,
				EdgeType:   etype,
				Properties: eprops,
			})
		}
		return edges, records.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: get edges: %w", err)
	}
	return raw.([]store.Edge), nil
}

// ── Consolidation ───────────────────────────────────────────────────────────

// ScanRecent returns episodic memories created since the given time, ordered by
// last-accessed-at descending.
func (s *Store) ScanRecent(ctx context.Context, since time.Time, limit int) ([]store.EpisodicScanItem, error) {
	if limit <= 0 {
		limit = 100
	}

	raw, err := s.neo4j.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx, `
			MATCH (e:EpisodicMemory)
			WHERE e.created_at >= $since AND e.obsoleted_at IS NULL
			RETURN e.id AS id, e.summary AS summary, e.task_type AS taskType,
			       e.project_id AS projectID, e.language AS language,
			       e.entity_group AS entityGroup, e.entity_group AS entityID, e.weight AS weight,
			       e.last_accessed_at AS lastAccessed, e.created_at AS createdAt
			ORDER BY e.last_accessed_at DESC
			LIMIT $limit
		`, map[string]any{"since": since, "limit": limit})
		if runErr != nil {
			return nil, runErr
		}
		var items []store.EpisodicScanItem
		for records.Next(ctx) {
			r := records.Record()
			idStr, _, _ := neodriver.GetRecordValue[string](r, "id")
			summary, _, _ := neodriver.GetRecordValue[string](r, "summary")
			taskType, _, _ := neodriver.GetRecordValue[string](r, "taskType")
			projectID, _, _ := neodriver.GetRecordValue[string](r, "projectID")
			language, _, _ := neodriver.GetRecordValue[string](r, "language")
			entityGroup, _, _ := neodriver.GetRecordValue[string](r, "entityGroup")
				entityID, _, _ := neodriver.GetRecordValue[string](r, "entityID")
			weight, _, _ := neodriver.GetRecordValue[float64](r, "weight")

			pid, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}

			// Use last_accessed_at if present, else created_at as fallback.
			la, laIsNil, _ := neodriver.GetRecordValue[time.Time](r, "lastAccessed")
			if laIsNil {
				la, _, _ = neodriver.GetRecordValue[time.Time](r, "createdAt")
			}

			items = append(items, store.EpisodicScanItem{
				ID:           pid,
				Summary:      summary,
				TaskType:     models.TaskType(taskType),
				ProjectID:    projectID,
				Language:     language,
				EntityGroup:  entityGroup,
				EntityID:     entityID,
				Weight:       weight,
				LastAccessed: la,
			})
		}
		return items, records.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: scan recent: %w", err)
	}
	return raw.([]store.EpisodicScanItem), nil
}

// SaveConsolidationRun persists a consolidation run result as a node.
func (s *Store) SaveConsolidationRun(ctx context.Context, run *models.ConsolidationRunResult) error {
	params := map[string]any{
		"runID":              run.RunID.String(),
		"startedAt":          run.StartedAt,
		"episodicsScanned":   int64(run.EpisodicsScanned),
		"candidateRulesFound": int64(run.CandidateRulesFound),
		"rulesPersisted":     int64(run.RulesPersisted),
		"averageAccuracy":    run.AverageAccuracy,
		"pitfallsExtracted": int64(run.PitfallsExtracted),
		"pitfallsMerged":    int64(run.PitfallsMerged),
		"pitfallsPersisted": int64(run.PitfallsPersisted),
		"rulesError":        run.RulesError,
		"pitfallError":      run.PitfallError,
	}
	if run.CompletedAt != nil {
		params["completedAt"] = *run.CompletedAt
	} else {
		params["completedAt"] = nil
	}

	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx, `
			CREATE (c:ConsolidationRun {
				run_id: $runID,
				started_at: $startedAt,
				completed_at: $completedAt,
				episodics_scanned: $episodicsScanned,
				candidate_rules_found: $candidateRulesFound,
				rules_persisted: $rulesPersisted,
				average_accuracy: $averageAccuracy,
				pitfalls_extracted: $pitfallsExtracted,
				pitfalls_merged: $pitfallsMerged,
				pitfalls_persisted: $pitfallsPersisted,
				rules_error: $rulesError,
				pitfall_error: $pitfallError
			})
		`, params)
		return nil, runErr
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: save consolidation run: %w", err)
	}
	return nil
}

// GetConsolidationRun retrieves a consolidation run by ID.
func (s *Store) GetConsolidationRun(ctx context.Context, runID uuid.UUID) (*models.ConsolidationRunResult, error) {
	raw, err := s.neo4j.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx,
			`MATCH (c:ConsolidationRun {run_id: $runID}) RETURN c`,
			map[string]any{"runID": runID.String()})
		if runErr != nil {
			return nil, runErr
		}
		if !records.Next(ctx) {
			return nil, records.Err()
		}
		r := records.Record()

		ridStr, _, _ := neodriver.GetRecordValue[string](r, "c.run_id")
		startedAt, _, _ := neodriver.GetRecordValue[time.Time](r, "c.started_at")
		completedAt, caIsNil, _ := neodriver.GetRecordValue[time.Time](r, "c.completed_at")
		epScanned, _, _ := neodriver.GetRecordValue[int64](r, "c.episodics_scanned")
		candFound, _, _ := neodriver.GetRecordValue[int64](r, "c.candidate_rules_found")
		rulesPersisted, _, _ := neodriver.GetRecordValue[int64](r, "c.rules_persisted")
		avgAcc, _, _ := neodriver.GetRecordValue[float64](r, "c.average_accuracy")
			pitExtracted, _, _ := neodriver.GetRecordValue[int64](r, "c.pitfalls_extracted")
			pitMerged, _, _ := neodriver.GetRecordValue[int64](r, "c.pitfalls_merged")
			pitPersisted, _, _ := neodriver.GetRecordValue[int64](r, "c.pitfalls_persisted")
			rulesError, _, _ := neodriver.GetRecordValue[string](r, "c.rules_error")
			pitfallError, _, _ := neodriver.GetRecordValue[string](r, "c.pitfall_error")

		rid, err := uuid.Parse(ridStr)
		if err != nil {
			return nil, err
		}

		result := &models.ConsolidationRunResult{
			RunID:              rid,
			StartedAt:          startedAt,
			EpisodicsScanned:   int(epScanned),
			CandidateRulesFound: int(candFound),
			RulesPersisted:     int(rulesPersisted),
			AverageAccuracy:    avgAcc,
				PitfallsExtracted:  int(pitExtracted),
				PitfallsMerged:     int(pitMerged),
				PitfallsPersisted:  int(pitPersisted),
				RulesError:         rulesError,
				PitfallError:       pitfallError,
		}
		if !caIsNil {
			result.CompletedAt = &completedAt
		}
		return result, nil
	})
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: get consolidation run: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("neo4jpg: consolidation run %s not found", runID)
	}
	return raw.(*models.ConsolidationRunResult), nil
}

// ── Lifecycle ───────────────────────────────────────────────────────────────

// HealthCheck verifies both Neo4j and pgvector are reachable.
func (s *Store) HealthCheck(ctx context.Context) error {
	if err := s.neo4j.HealthCheck(ctx); err != nil {
		return fmt.Errorf("neo4jpg: neo4j health: %w", err)
	}
	if err := s.pg.HealthCheck(ctx); err != nil {
		return fmt.Errorf("neo4jpg: pgvector health: %w", err)
	}
	return nil
}

// Close shuts down both clients.
func (s *Store) Close() error {
	var errs []string
	if err := s.neo4j.Close(context.Background()); err != nil {
		errs = append(errs, "neo4j: "+err.Error())
	}
	s.pg.Close()
	if len(errs) > 0 {
		return fmt.Errorf("neo4jpg: close errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// ── Scan Helpers ────────────────────────────────────────────────────────────

// scanEpisodic maps a Neo4j record to an EpisodicMemory.
func scanEpisodic(r *neodriver.Record, prefix string) (*models.EpisodicMemory, error) {
	key := func(field string) string { return prefix + "." + field }

	idStr, _, _ := neodriver.GetRecordValue[string](r, key("id"))
	projectID, _, _ := neodriver.GetRecordValue[string](r, key("project_id"))
	language, _, _ := neodriver.GetRecordValue[string](r, key("language"))
	taskType, _, _ := neodriver.GetRecordValue[string](r, key("task_type"))
	summary, _, _ := neodriver.GetRecordValue[string](r, key("summary"))
	sourceType, _, _ := neodriver.GetRecordValue[string](r, key("source_type"))
	trustLevel, _, _ := neodriver.GetRecordValue[int64](r, key("trust_level"))
	weight, _, _ := neodriver.GetRecordValue[float64](r, key("weight"))
	effFreq, _, _ := neodriver.GetRecordValue[float64](r, key("effective_frequency"))
	createdAt, _, _ := neodriver.GetRecordValue[time.Time](r, key("created_at"))
	lastAccessedAt, laIsNil, _ := neodriver.GetRecordValue[time.Time](r, key("last_accessed_at"))
	obsoletedAt, obIsNil, _ := neodriver.GetRecordValue[time.Time](r, key("obsoleted_at"))
	entityGroup, _, _ := neodriver.GetRecordValue[string](r, key("entity_group"))
	embeddingID, _, _ := neodriver.GetRecordValue[string](r, key("embedding_id"))
	fullSnapshotURI, _, _ := neodriver.GetRecordValue[string](r, key("full_snapshot_uri"))

	pid, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: parse id %q: %w", idStr, err)
	}

	mem := &models.EpisodicMemory{
		ID:                 pid,
		ProjectID:          projectID,
		Language:           language,
		TaskType:           models.TaskType(taskType),
		Summary:            summary,
		SourceType:         models.SourceType(sourceType),
		TrustLevel:         models.TrustLevel(trustLevel),
		Weight:             weight,
		EffectiveFrequency: effFreq,
		CreatedAt:          createdAt,
		EntityGroup:        entityGroup,
		EmbeddingID:        embeddingID,
		FullSnapshotURI:    fullSnapshotURI,
	}
	if !laIsNil {
		mem.LastAccessedAt = &lastAccessedAt
	}
	if !obIsNil {
		mem.ObsoletedAt = &obsoletedAt
	}
	return mem, nil
}

// scanSemantic maps a Neo4j record to a SemanticMemory.
func scanSemantic(r *neodriver.Record, prefix string) (*models.SemanticMemory, error) {
	key := func(field string) string { return prefix + "." + field }

	idStr, _, _ := neodriver.GetRecordValue[string](r, key("id"))
	memType, _, _ := neodriver.GetRecordValue[string](r, key("type"))
	content, _, _ := neodriver.GetRecordValue[string](r, key("content"))
	sourceType, _, _ := neodriver.GetRecordValue[string](r, key("source_type"))
	trustLevel, _, _ := neodriver.GetRecordValue[int64](r, key("trust_level"))
	weight, _, _ := neodriver.GetRecordValue[float64](r, key("weight"))
	effFreq, _, _ := neodriver.GetRecordValue[float64](r, key("effective_frequency"))
	createdAt, _, _ := neodriver.GetRecordValue[time.Time](r, key("created_at"))
	lastAccessedAt, laIsNil, _ := neodriver.GetRecordValue[time.Time](r, key("last_accessed_at"))
	obsoletedAt, obIsNil, _ := neodriver.GetRecordValue[time.Time](r, key("obsoleted_at"))
	entityGroup, _, _ := neodriver.GetRecordValue[string](r, key("entity_group"))
	consRunID, _, _ := neodriver.GetRecordValue[string](r, key("consolidation_run_id"))
	backtestAcc, _, _ := neodriver.GetRecordValue[float64](r, key("backtest_accuracy"))

	pid, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: parse id %q: %w", idStr, err)
	}

	// SourceEpisodicIDs is a list of strings in Neo4j.
	var srcIDs []uuid.UUID
	if srcList, _, _ := neodriver.GetRecordValue[[]any](r, key("source_episodic_ids")); srcList != nil {
		for _, v := range srcList {
			if s, ok := v.(string); ok {
				if uid, err := uuid.Parse(s); err == nil {
					srcIDs = append(srcIDs, uid)
				}
			}
		}
	}

	mem := &models.SemanticMemory{
		ID:                 pid,
		Type:               models.MemoryType(memType),
		Content:            content,
		SourceType:         models.SourceType(sourceType),
		TrustLevel:         models.TrustLevel(trustLevel),
		Weight:             weight,
		EffectiveFrequency: effFreq,
		CreatedAt:          createdAt,
		EntityGroup:        entityGroup,
		ConsolidationRunID: consRunID,
		BacktestAccuracy:   backtestAcc,
		SourceEpisodicIDs:  srcIDs,
	}
	if !laIsNil {
		mem.LastAccessedAt = &lastAccessedAt
	}
	if !obIsNil {
		mem.ObsoletedAt = &obsoletedAt
	}
	return mem, nil
}

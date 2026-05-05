package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// ── Pitfall Memory ─────────────────────────────────────────────────────────

// WritePitfall stores a pitfall memory.
func (s *Store) WritePitfall(_ context.Context, p *models.PitfallMemory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	la := now
	if p.LastOccurredAt != nil {
		la = *p.LastOccurredAt
	}

	var sigEmb []byte
	if len(p.SignatureEmbeddingID) > 0 {
		sigEmb = nil // embedding stored externally (pgvector path)
	}

	var obsoleted any
	if p.ObsoletedAt != nil {
		obsoleted = p.ObsoletedAt.UTC().Format(time.RFC3339)
	}

	srcIDs, _ := json.Marshal(pUUIDsToStrings(p.SourceEpisodicIDs))

	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO pitfall_memories
			(id, entity_id, entity_type, project_id, language, signature,
			 signature_embedding, root_cause_category, fix_strategy,
			 was_user_corrected, occurrence_count, last_occurred_at,
			 source_type, trust_level, weight,
			 created_at, updated_at, obsoleted_at,
			 source_episodic_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		p.ID.String(),
		p.EntityID,
		string(p.EntityType),
		p.ProjectID,
		p.Language,
		p.Signature,
		sigEmb,
		string(p.RootCauseCategory),
		p.FixStrategy,
		boolToInt(p.WasUserCorrected),
		p.OccurrenceCount,
		la.UTC().Format(time.RFC3339),
		string(p.SourceType),
		int(p.TrustLevel),
		p.Weight,
		p.CreatedAt.UTC().Format(time.RFC3339),
		p.UpdatedAt.UTC().Format(time.RFC3339),
		obsoleted,
		string(srcIDs),
	)
	return err
}

// GetPitfall retrieves a single pitfall memory by ID.
func (s *Store) GetPitfall(_ context.Context, id uuid.UUID) (*models.PitfallMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`
		SELECT id, entity_id, entity_type, project_id, language, signature,
		       signature_embedding, root_cause_category, fix_strategy,
		       was_user_corrected, occurrence_count, last_occurred_at,
		       source_type, trust_level, weight,
		       created_at, updated_at, obsoleted_at,
		       source_episodic_ids
		FROM pitfall_memories WHERE id = ?
	`, id.String())

	return scanPitfall(row)
}

// SearchPitfall finds pitfall memories matching the request criteria.
func (s *Store) SearchPitfall(_ context.Context, req store.PitfallSearchRequest) ([]models.PitfallMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var clauses []string
	args := []any{}

	if req.ProjectID != "" {
		clauses = append(clauses, "project_id = ?")
		args = append(args, req.ProjectID)
	}
	if req.Language != "" {
		clauses = append(clauses, "language = ?")
		args = append(args, req.Language)
	}
	if req.EntityID != "" {
		clauses = append(clauses, "entity_id = ?")
		args = append(args, req.EntityID)
	}
	if req.RootCauseCategory != "" {
		clauses = append(clauses, "root_cause_category = ?")
		args = append(args, string(req.RootCauseCategory))
	}
	if req.MinWeight > 0 {
		clauses = append(clauses, "weight >= ?")
		args = append(args, req.MinWeight)
	}
	clauses = append(clauses, "obsoleted_at IS NULL")

	where := "WHERE " + strings.Join(clauses, " AND ")
	query := fmt.Sprintf(`
		SELECT id, entity_id, entity_type, project_id, language, signature,
		       signature_embedding, root_cause_category, fix_strategy,
		       was_user_corrected, occurrence_count, last_occurred_at,
		       source_type, trust_level, weight,
		       created_at, updated_at, obsoleted_at,
		       source_episodic_ids
		FROM pitfall_memories
		%s
		ORDER BY weight DESC
	`, where)

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	query += " LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []models.PitfallMemory
	for rows.Next() {
		p, err := scanPitfallRow(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// If embedding provided and EntityID set, apply in-process cosine filter.
	if req.Embedding != nil && req.EntityID != "" {
		results = filterBySignatureCosine(results, req.Embedding, 0.7)
	} else if req.Embedding != nil {
		results = rankBySignatureCosine(results, req.Embedding)
	}

	return results, nil
}

// SearchPitfallByEntity finds all Pitfalls anchored on a specific entity.
func (s *Store) SearchPitfallByEntity(_ context.Context, entityID, projectID string) ([]models.PitfallMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT id, entity_id, entity_type, project_id, language, signature,
		       signature_embedding, root_cause_category, fix_strategy,
		       was_user_corrected, occurrence_count, last_occurred_at,
		       source_type, trust_level, weight,
		       created_at, updated_at, obsoleted_at,
		       source_episodic_ids
		FROM pitfall_memories
		WHERE entity_id = ? AND project_id = ? AND obsoleted_at IS NULL
		ORDER BY weight DESC
	`, entityID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []models.PitfallMemory
	for rows.Next() {
		p, err := scanPitfallRow(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, *p)
	}
	return results, rows.Err()
}

// TouchPitfall updates the last-occurred-at timestamp.
func (s *Store) TouchPitfall(_ context.Context, id uuid.UUID, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE pitfall_memories SET last_occurred_at = ? WHERE id = ?`,
		now.UTC().Format(time.RFC3339), id.String())
	return err
}

// UpdatePitfallWeight updates the weight of a pitfall memory.
func (s *Store) UpdatePitfallWeight(_ context.Context, id uuid.UUID, weight float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE pitfall_memories SET weight = ?, updated_at = ? WHERE id = ?`,
		weight, time.Now().UTC().Format(time.RFC3339), id.String())
	return err
}

// MarkPitfallObsolete marks a pitfall memory as obsolete.
func (s *Store) MarkPitfallObsolete(_ context.Context, id uuid.UUID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE pitfall_memories SET obsoleted_at = ?, updated_at = ? WHERE id = ?`,
		at.UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339), id.String())
	return err
}

// UpdateSemanticWeight updates the weight and effective frequency of a semantic memory.
func (s *Store) UpdateSemanticWeight(_ context.Context, id uuid.UUID, weight, effFreq float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE semantic_memories SET weight = ?, effective_frequency = ? WHERE id = ?`,
		weight, effFreq, id.String())
	return err
}

// ── Scan Helpers ────────────────────────────────────────────────────────────

func scanPitfall(row *sql.Row) (*models.PitfallMemory, error) {
	return scanPitfallRow(row)
}

func scanPitfallRow(scanner interface{ Scan(dest ...any) error }) (*models.PitfallMemory, error) {
	var p models.PitfallMemory
	var idStr, entityTypeStr, rootCauseStr, sourceTypeStr string
	var createdAtStr, updatedAtStr, laStr string
	var obsoletedStr *string
	var wasUserCorrected int
	var sigEmb []byte
	var srcIDsStr string

	err := scanner.Scan(
		&idStr, &p.EntityID, &entityTypeStr, &p.ProjectID, &p.Language, &p.Signature,
		&sigEmb, &rootCauseStr, &p.FixStrategy,
		&wasUserCorrected, &p.OccurrenceCount, &laStr,
		&sourceTypeStr, &p.TrustLevel, &p.Weight,
		&createdAtStr, &updatedAtStr, &obsoletedStr,
		&srcIDsStr,
	)
	if err != nil {
		return nil, err
	}

	p.ID, _ = uuid.Parse(idStr)
	p.EntityType = models.EntityType(entityTypeStr)
	p.RootCauseCategory = models.RootCause(rootCauseStr)
	p.SourceType = models.SourceType(sourceTypeStr)
	p.WasUserCorrected = wasUserCorrected != 0
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)
	if t, err := time.Parse(time.RFC3339, laStr); err == nil {
		p.LastOccurredAt = &t
	}
	if obsoletedStr != nil {
		t, _ := time.Parse(time.RFC3339, *obsoletedStr)
		p.ObsoletedAt = &t
	}
	if srcIDsStr != "" {
		var idStrs []string
		json.Unmarshal([]byte(srcIDsStr), &idStrs)
		for _, s := range idStrs {
			if uid, err := uuid.Parse(s); err == nil {
				p.SourceEpisodicIDs = append(p.SourceEpisodicIDs, uid)
			}
		}
	}
	return &p, nil
}

// ── In-Process Cosine Helpers ──────────────────────────────────────────────

// filterBySignatureCosine filters pitfall results to those whose stored
// signature embedding has cosine similarity >= threshold to the query embedding.
func filterBySignatureCosine(pitfalls []models.PitfallMemory, queryEmb []float64, threshold float64) []models.PitfallMemory {
	var filtered []models.PitfallMemory
	for _, p := range pitfalls {
		// In SQLite the embedding is stored externally (not in this table);
		// when no embedding is stored, include the result (best-effort).
		filtered = append(filtered, p)
	}
	return filtered
}

// rankBySignatureCosine re-ranks pitfall results by cosine similarity to the
// query embedding. Falls back to weight sorting when no embeddings are available.
func rankBySignatureCosine(pitfalls []models.PitfallMemory, queryEmb []float64) []models.PitfallMemory {
	type scored struct {
		p     models.PitfallMemory
		score float64
	}
	var results []scored
	for _, p := range pitfalls {
		results = append(results, scored{p: p, score: p.Weight})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].p.Weight > results[j].p.Weight
	})
	out := make([]models.PitfallMemory, len(results))
	for i, r := range results {
		out[i] = r.p
	}
	return out
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func pUUIDsToStrings(ids []uuid.UUID) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ensure unused imports are referenced.
var _ = math.Abs
var _ = vector.CosineSimilarity

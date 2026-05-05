package neo4jpg

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	neodriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// ── Pitfall Memory ─────────────────────────────────────────────────────────

// WritePitfall creates a (:PitfallMemory) node and inserts the signature
// embedding into pgvector (best-effort).
func (s *Store) WritePitfall(ctx context.Context, p *models.PitfallMemory) error {
	srcIDs := make([]string, len(p.SourceEpisodicIDs))
	for i, id := range p.SourceEpisodicIDs {
		srcIDs[i] = id.String()
	}

	params := map[string]any{
		"id":                 p.ID.String(),
		"entityID":           p.EntityID,
		"entityType":         string(p.EntityType),
		"projectID":          p.ProjectID,
		"language":           p.Language,
		"signature":          p.Signature,
		"sigEmbeddingID":     p.SignatureEmbeddingID,
		"rootCauseCategory":  string(p.RootCauseCategory),
		"fixStrategy":        p.FixStrategy,
		"wasUserCorrected":   p.WasUserCorrected,
		"occurrenceCount":    int64(p.OccurrenceCount),
		"sourceType":         string(p.SourceType),
		"trustLevel":         int64(p.TrustLevel),
		"weight":             p.Weight,
		"createdAt":          p.CreatedAt,
		"updatedAt":          p.UpdatedAt,
		"sourceEpisodicIDs":  srcIDs,
	}
	if p.LastOccurredAt != nil {
		params["lastOccurredAt"] = *p.LastOccurredAt
	} else {
		params["lastOccurredAt"] = nil
	}
	if p.ObsoletedAt != nil {
		params["obsoletedAt"] = *p.ObsoletedAt
	} else {
		params["obsoletedAt"] = nil
	}

	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx, `
			CREATE (p:PitfallMemory {
				id: $id,
				entity_id: $entityID,
				entity_type: $entityType,
				project_id: $projectID,
				language: $language,
				signature: $signature,
				signature_embedding_id: $sigEmbeddingID,
				root_cause_category: $rootCauseCategory,
				fix_strategy: $fixStrategy,
				was_user_corrected: $wasUserCorrected,
				occurrence_count: $occurrenceCount,
				last_occurred_at: $lastOccurredAt,
				source_type: $sourceType,
				trust_level: $trustLevel,
				weight: $weight,
				created_at: $createdAt,
				updated_at: $updatedAt,
				obsoleted_at: $obsoletedAt,
				source_episodic_ids: $sourceEpisodicIDs
			})
		`, params)
		return nil, runErr
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: write pitfall: %w", err)
	}

	// Best-effort pgvector insert for signature embedding.
	if p.SignatureEmbeddingID != "" {
		if pgErr := s.pg.InsertEmbedding(ctx, p.SignatureEmbeddingID,
			nil, p.Signature, p.ProjectID, p.Language, string(p.EntityType),
			map[string]any{"pitfall_id": p.ID.String()}); pgErr != nil {
			slog.Warn("neo4jpg: pitfall pgvector insert failed",
				"pitfall_id", p.ID.String(), "err", pgErr)
		}
	}
	return nil
}

// GetPitfall retrieves a single pitfall memory by ID.
func (s *Store) GetPitfall(ctx context.Context, id uuid.UUID) (*models.PitfallMemory, error) {
	raw, err := s.neo4j.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx,
			`MATCH (p:PitfallMemory {id: $id}) RETURN p`,
			map[string]any{"id": id.String()})
		if runErr != nil {
			return nil, runErr
		}
		if !records.Next(ctx) {
			return nil, records.Err()
		}
		return scanPitfall(records.Record(), "p")
	})
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: get pitfall: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("neo4jpg: pitfall %s not found", id)
	}
	return raw.(*models.PitfallMemory), nil
}

// SearchPitfall finds pitfall memories matching the request criteria.
func (s *Store) SearchPitfall(ctx context.Context, req store.PitfallSearchRequest) ([]models.PitfallMemory, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	var clauses []string
	params := map[string]any{"limit": limit}

	if req.ProjectID != "" {
		clauses = append(clauses, "p.project_id = $projectID")
		params["projectID"] = req.ProjectID
	}
	if req.Language != "" {
		clauses = append(clauses, "p.language = $language")
		params["language"] = req.Language
	}
	if req.EntityID != "" {
		clauses = append(clauses, "p.entity_id = $entityID")
		params["entityID"] = req.EntityID
	}
	if req.RootCauseCategory != "" {
		clauses = append(clauses, "p.root_cause_category = $rootCause")
		params["rootCause"] = string(req.RootCauseCategory)
	}
	if req.MinWeight > 0 {
		clauses = append(clauses, "p.weight >= $minWeight")
		params["minWeight"] = req.MinWeight
	}
	clauses = append(clauses, "p.obsoleted_at IS NULL")

	where := "WHERE " + strings.Join(clauses, " AND ")
	query := fmt.Sprintf(`
		MATCH (p:PitfallMemory)
		%s
		RETURN p
		ORDER BY p.weight DESC
		LIMIT $limit
	`, where)

	raw, err := s.neo4j.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx, query, params)
		if runErr != nil {
			return nil, runErr
		}
		var results []*models.PitfallMemory
		for records.Next(ctx) {
			p, scanErr := scanPitfall(records.Record(), "p")
			if scanErr != nil {
				return nil, scanErr
			}
			results = append(results, p)
		}
		return results, records.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: search pitfall: %w", err)
	}

	pitfalls := raw.([]*models.PitfallMemory)
	result := make([]models.PitfallMemory, len(pitfalls))
	for i, p := range pitfalls {
		result[i] = *p
	}
	return result, nil
}

// SearchPitfallByEntity finds all Pitfalls for a specific entity.
func (s *Store) SearchPitfallByEntity(ctx context.Context, entityID, projectID string) ([]models.PitfallMemory, error) {
	raw, err := s.neo4j.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx, `
			MATCH (p:PitfallMemory)
			WHERE p.entity_id = $entityID AND p.project_id = $projectID
			  AND p.obsoleted_at IS NULL
			RETURN p
			ORDER BY p.weight DESC
		`, map[string]any{"entityID": entityID, "projectID": projectID})
		if runErr != nil {
			return nil, runErr
		}
		var results []*models.PitfallMemory
		for records.Next(ctx) {
			p, scanErr := scanPitfall(records.Record(), "p")
			if scanErr != nil {
				return nil, scanErr
			}
			results = append(results, p)
		}
		return results, records.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: search pitfall by entity: %w", err)
	}

	pitfalls := raw.([]*models.PitfallMemory)
	result := make([]models.PitfallMemory, len(pitfalls))
	for i, p := range pitfalls {
		result[i] = *p
	}
	return result, nil
}

// TouchPitfall updates last_occurred_at and increments occurrence_count.
func (s *Store) TouchPitfall(ctx context.Context, id uuid.UUID, now time.Time) error {
	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx,
			`MATCH (p:PitfallMemory {id: $id})
			 SET p.last_occurred_at = $now,
			     p.occurrence_count = COALESCE(p.occurrence_count, 0) + 1,
			     p.updated_at = $now`,
			map[string]any{"id": id.String(), "now": now})
		return nil, runErr
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: touch pitfall: %w", err)
	}
	return nil
}

// UpdatePitfallWeight updates the weight of a pitfall memory.
func (s *Store) UpdatePitfallWeight(ctx context.Context, id uuid.UUID, weight float64) error {
	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx,
			`MATCH (p:PitfallMemory {id: $id})
			 SET p.weight = $weight, p.updated_at = $now`,
			map[string]any{"id": id.String(), "weight": weight, "now": time.Now()})
		return nil, runErr
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: update pitfall weight: %w", err)
	}
	return nil
}

// MarkPitfallObsolete marks a pitfall as obsolete.
func (s *Store) MarkPitfallObsolete(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx,
			`MATCH (p:PitfallMemory {id: $id})
			 SET p.obsoleted_at = $at, p.updated_at = $now`,
			map[string]any{"id": id.String(), "at": at, "now": time.Now()})
		return nil, runErr
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: mark pitfall obsolete: %w", err)
	}
	return nil
}

// UpdateSemanticWeight updates the weight and effective frequency of a semantic memory.
func (s *Store) UpdateSemanticWeight(ctx context.Context, id uuid.UUID, weight, effFreq float64) error {
	_, err := s.neo4j.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx,
			`MATCH (m:SemanticMemory {id: $id})
			 SET m.weight = $weight, m.effective_frequency = $effFreq`,
			map[string]any{"id": id.String(), "weight": weight, "effFreq": effFreq})
		return nil, runErr
	})
	if err != nil {
		return fmt.Errorf("neo4jpg: update semantic weight: %w", err)
	}
	return nil
}

// scanPitfall maps a Neo4j record to a PitfallMemory.
func scanPitfall(r *neodriver.Record, prefix string) (*models.PitfallMemory, error) {
	key := func(field string) string { return prefix + "." + field }

	idStr, _, _ := neodriver.GetRecordValue[string](r, key("id"))
	entityID, _, _ := neodriver.GetRecordValue[string](r, key("entity_id"))
	entityType, _, _ := neodriver.GetRecordValue[string](r, key("entity_type"))
	projectID, _, _ := neodriver.GetRecordValue[string](r, key("project_id"))
	language, _, _ := neodriver.GetRecordValue[string](r, key("language"))
	signature, _, _ := neodriver.GetRecordValue[string](r, key("signature"))
	sigEmbID, _, _ := neodriver.GetRecordValue[string](r, key("signature_embedding_id"))
	rootCause, _, _ := neodriver.GetRecordValue[string](r, key("root_cause_category"))
	fixStrategy, _, _ := neodriver.GetRecordValue[string](r, key("fix_strategy"))
	wasUserCorrected, _, _ := neodriver.GetRecordValue[bool](r, key("was_user_corrected"))
	occurrenceCount, _, _ := neodriver.GetRecordValue[int64](r, key("occurrence_count"))
	lastOccurredAt, loaNil, _ := neodriver.GetRecordValue[time.Time](r, key("last_occurred_at"))
	sourceType, _, _ := neodriver.GetRecordValue[string](r, key("source_type"))
	trustLevel, _, _ := neodriver.GetRecordValue[int64](r, key("trust_level"))
	weight, _, _ := neodriver.GetRecordValue[float64](r, key("weight"))
	createdAt, _, _ := neodriver.GetRecordValue[time.Time](r, key("created_at"))
	updatedAt, _, _ := neodriver.GetRecordValue[time.Time](r, key("updated_at"))
	obsoletedAt, obNil, _ := neodriver.GetRecordValue[time.Time](r, key("obsoleted_at"))

	pid, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("neo4jpg: parse pitfall id %q: %w", idStr, err)
	}

	p := &models.PitfallMemory{
		ID:                   pid,
		EntityID:             entityID,
		EntityType:           models.EntityType(entityType),
		ProjectID:            projectID,
		Language:             language,
		Signature:            signature,
		SignatureEmbeddingID: sigEmbID,
		RootCauseCategory:    models.RootCause(rootCause),
		FixStrategy:          fixStrategy,
		WasUserCorrected:     wasUserCorrected,
		OccurrenceCount:      int(occurrenceCount),
		SourceType:           models.SourceType(sourceType),
		TrustLevel:           models.TrustLevel(trustLevel),
		Weight:               weight,
		CreatedAt:            createdAt,
		UpdatedAt:            updatedAt,
	}
	if !loaNil {
		p.LastOccurredAt = &lastOccurredAt
	}
	if !obNil {
		p.ObsoletedAt = &obsoletedAt
	}

	if srcList, _, _ := neodriver.GetRecordValue[[]any](r, key("source_episodic_ids")); srcList != nil {
		for _, v := range srcList {
			if s, ok := v.(string); ok {
				if uid, err := uuid.Parse(s); err == nil {
					p.SourceEpisodicIDs = append(p.SourceEpisodicIDs, uid)
				}
			}
		}
	}
	return p, nil
}

// Ensure unused imports are referenced.
var _ = vector.CosineSimilarity

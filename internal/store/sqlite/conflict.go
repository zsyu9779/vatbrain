package sqlite

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
)

// ListRuleConflicts returns conflict records, optionally filtered by status,
// most recent first. This is the query behind the conflict Workbench.
func (s *Store) ListRuleConflicts(_ context.Context, status string, limit int) ([]models.RuleConflict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `SELECT id, rule_a_id, rule_b_id, entity_group, basis, status,
	          resolution, reason, created_at, resolved_at
	          FROM rule_conflicts`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`
	if limit <= 0 {
		limit = 20
	}
	query += ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.RuleConflict
	for rows.Next() {
		var c models.RuleConflict
		var idStr, aStr, bStr, entityGroup, basis, status, resolution, reason, createdAtStr string
		var resolvedAtStr *string
		if err := rows.Scan(&idStr, &aStr, &bStr, &entityGroup, &basis, &status,
			&resolution, &reason, &createdAtStr, &resolvedAtStr); err != nil {
			return nil, err
		}
		c.ID, _ = uuid.Parse(idStr)
		c.RuleAID, _ = uuid.Parse(aStr)
		c.RuleBID, _ = uuid.Parse(bStr)
		c.EntityGroup = entityGroup
		c.Basis = models.ConflictBasis(basis)
		c.Status = models.ConflictStatus(status)
		c.Resolution = resolution
		c.Reason = reason
		c.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		if resolvedAtStr != nil {
			t, _ := time.Parse(time.RFC3339, *resolvedAtStr)
			c.ResolvedAt = &t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveRuleConflict stores a newly detected conflict as pending.
func (s *Store) SaveRuleConflict(_ context.Context, c *models.RuleConflict) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO rule_conflicts
			(id, rule_a_id, rule_b_id, entity_group, basis, status, resolution, reason, created_at, resolved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		c.ID.String(), c.RuleAID.String(), c.RuleBID.String(),
		c.EntityGroup, string(c.Basis), string(c.Status),
		c.Resolution, c.Reason, c.CreatedAt.UTC().Format(time.RFC3339),
		nilIfZero(c.ResolvedAt),
	)
	return err
}

// ResolveRuleConflict records a resolution for a conflict record.
func (s *Store) ResolveRuleConflict(_ context.Context, id uuid.UUID, status models.ConflictStatus, resolution uuid.UUID, reason string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE rule_conflicts
		SET status = ?, resolution = ?, reason = ?, resolved_at = ?
		WHERE id = ?
	`, string(status), resolution.String(), reason, at.UTC().Format(time.RFC3339), id.String())
	return err
}

// MarkSemanticObsolete retires a semantic memory (obsoleted_at), used when a
// conflict resolution selects the other rule as the winner.
func (s *Store) MarkSemanticObsolete(_ context.Context, id uuid.UUID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE semantic_memories SET obsoleted_at = ? WHERE id = ?`,
		at.UTC().Format(time.RFC3339), id.String())
	return err
}

func nilIfZero(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

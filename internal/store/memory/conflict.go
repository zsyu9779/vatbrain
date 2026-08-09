package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
)

// ListRuleConflicts returns conflict records, optionally filtered by status,
// most recent first.
func (s *Store) ListRuleConflicts(_ context.Context, status string, limit int) ([]models.RuleConflict, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []models.RuleConflict
	for _, c := range s.ruleConflicts {
		if status != "" && string(c.Status) != status {
			continue
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

// SaveRuleConflict stores a newly detected conflict.
func (s *Store) SaveRuleConflict(_ context.Context, c *models.RuleConflict) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ruleConflicts == nil {
		s.ruleConflicts = make(map[uuid.UUID]*models.RuleConflict)
	}
	clone := *c
	s.ruleConflicts[c.ID] = &clone
	return nil
}

// ResolveRuleConflict records a resolution for a conflict record.
func (s *Store) ResolveRuleConflict(_ context.Context, id uuid.UUID, status models.ConflictStatus, resolution uuid.UUID, reason string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.ruleConflicts[id]
	if !ok {
		return fmt.Errorf("rule conflict %s not found", id)
	}
	c.Status = status
	c.Resolution = resolution.String()
	c.Reason = reason
	c.ResolvedAt = &at
	return nil
}

// MarkSemanticObsolete retires a semantic memory.
func (s *Store) MarkSemanticObsolete(_ context.Context, id uuid.UUID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.semantics[id]
	if !ok {
		return fmt.Errorf("semantic memory %s not found", id)
	}
	m.ObsoletedAt = &at
	return nil
}

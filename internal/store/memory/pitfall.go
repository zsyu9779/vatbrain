package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// ── Pitfall Memory ─────────────────────────────────────────────────────────

// WritePitfall stores a pitfall memory in the in-memory map.
func (s *Store) WritePitfall(_ context.Context, p *models.PitfallMemory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *p
	s.pitfalls[p.ID] = &clone
	return nil
}

// GetPitfall retrieves a single pitfall memory by ID.
func (s *Store) GetPitfall(_ context.Context, id uuid.UUID) (*models.PitfallMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.pitfalls[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", models.ErrPitfallNotFound, id)
	}
	clone := *p
	return &clone, nil
}

// SearchPitfall finds pitfall memories matching the request criteria.
func (s *Store) SearchPitfall(_ context.Context, req store.PitfallSearchRequest) ([]models.PitfallMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var candidates []models.PitfallMemory
	for _, p := range s.pitfalls {
		if req.ProjectID != "" && p.ProjectID != req.ProjectID {
			continue
		}
		if req.Language != "" && p.Language != req.Language {
			continue
		}
		if req.EntityID != "" && p.EntityID != req.EntityID {
			continue
		}
		if req.RootCauseCategory != "" && p.RootCauseCategory != req.RootCauseCategory {
			continue
		}
		if req.MinWeight > 0 && p.Weight < req.MinWeight {
			continue
		}
		if p.ObsoletedAt != nil {
			continue
		}
		candidates = append(candidates, *p)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Weight > candidates[j].Weight
	})

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > len(candidates) {
		limit = len(candidates)
	}
	return candidates[:limit], nil
}

// SearchPitfallByEntity finds all pitfall memories for a specific entity.
func (s *Store) SearchPitfallByEntity(_ context.Context, entityID, projectID string) ([]models.PitfallMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []models.PitfallMemory
	for _, p := range s.pitfalls {
		if p.EntityID == entityID && p.ProjectID == projectID && p.ObsoletedAt == nil {
			results = append(results, *p)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Weight > results[j].Weight
	})
	return results, nil
}

// TouchPitfall updates last_occurred_at and increments occurrence_count.
func (s *Store) TouchPitfall(_ context.Context, id uuid.UUID, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pitfalls[id]
	if !ok {
		return fmt.Errorf("%w: %s", models.ErrPitfallNotFound, id)
	}
	p.LastOccurredAt = &now
	p.OccurrenceCount++
	p.UpdatedAt = now
	return nil
}

// UpdatePitfallWeight updates the weight of a pitfall memory.
func (s *Store) UpdatePitfallWeight(_ context.Context, id uuid.UUID, weight float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pitfalls[id]
	if !ok {
		return fmt.Errorf("%w: %s", models.ErrPitfallNotFound, id)
	}
	p.Weight = weight
	p.UpdatedAt = time.Now().UTC()
	return nil
}

// MarkPitfallObsolete marks a pitfall memory as obsolete.
func (s *Store) MarkPitfallObsolete(_ context.Context, id uuid.UUID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pitfalls[id]
	if !ok {
		return fmt.Errorf("%w: %s", models.ErrPitfallNotFound, id)
	}
	p.ObsoletedAt = &at
	p.UpdatedAt = time.Now().UTC()
	return nil
}

// UpdateSemanticWeight updates weight and effective frequency of a semantic memory.
func (s *Store) UpdateSemanticWeight(_ context.Context, id uuid.UUID, weight, effFreq float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.semantics[id]
	if !ok {
		return fmt.Errorf("semantic memory %s not found", id)
	}
	m.Weight = weight
	m.EffectiveFrequency = effFreq
	return nil
}

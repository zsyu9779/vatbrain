// Package memory provides an in-memory implementation of store.MemoryStore for
// testing and development. All data is stored in Go maps and is lost on restart.
package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// Store implements store.MemoryStore entirely in-process.
type Store struct {
	mu           sync.RWMutex
	episodics    map[uuid.UUID]*models.EpisodicMemory
	semantics    map[uuid.UUID]*models.SemanticMemory
	pitfalls     map[uuid.UUID]*models.PitfallMemory
	edges        []store.Edge
	pitfallEdges []store.Edge
	consRuns     map[uuid.UUID]*models.ConsolidationRunResult
}

// NewStore creates a new in-memory store.
func NewStore() *Store {
	return &Store{
		episodics: make(map[uuid.UUID]*models.EpisodicMemory),
		semantics: make(map[uuid.UUID]*models.SemanticMemory),
		pitfalls:  make(map[uuid.UUID]*models.PitfallMemory),
		consRuns:  make(map[uuid.UUID]*models.ConsolidationRunResult),
	}
}

// WriteEpisodic stores an episodic memory.
func (s *Store) WriteEpisodic(_ context.Context, mem *models.EpisodicMemory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *mem
	s.episodics[mem.ID] = &clone
	return nil
}

// GetEpisodic retrieves a single episodic memory by ID.
func (s *Store) GetEpisodic(_ context.Context, id uuid.UUID) (*models.EpisodicMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.episodics[id]
	if !ok {
		return nil, fmt.Errorf("episodic memory %s not found", id)
	}
	clone := *m
	return &clone, nil
}

// SearchEpisodic filters episodic memories by the request criteria. If an
// embedding is provided, results are ranked by cosine similarity in-process.
func (s *Store) SearchEpisodic(_ context.Context, req store.EpisodicSearchRequest) ([]models.EpisodicMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var candidates []models.EpisodicMemory
	for _, m := range s.episodics {
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
		candidates = append(candidates, *m)
	}

	if req.Embedding != nil && len(candidates) > 0 {
		type scored struct {
			mem   models.EpisodicMemory
			score float64
		}
		var results []scored
		for _, m := range candidates {
			if len(m.ContextVector) == 0 {
				continue
			}
			emb := vector.Float32To64(m.ContextVector)
			if len(emb) != len(req.Embedding) {
				continue
			}
			results = append(results, scored{
				mem:   m,
				score: vector.CosineSimilarity(req.Embedding, emb),
			})
		}
		sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })

		limit := req.Limit
		if limit <= 0 {
			limit = 10
		}
		if limit > len(results) {
			limit = len(results)
		}
		result := make([]models.EpisodicMemory, limit)
		for i := range limit {
			result[i] = results[i].mem
		}
		return result, nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Weight != candidates[j].Weight {
			return candidates[i].Weight > candidates[j].Weight
		}
		if candidates[i].LastAccessedAt != nil && candidates[j].LastAccessedAt != nil {
			return candidates[i].LastAccessedAt.After(*candidates[j].LastAccessedAt)
		}
		return candidates[i].CreatedAt.After(candidates[j].CreatedAt)
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

// TouchEpisodic updates the last-accessed timestamp.
func (s *Store) TouchEpisodic(_ context.Context, id uuid.UUID, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.episodics[id]
	if !ok {
		return fmt.Errorf("episodic memory %s not found", id)
	}
	m.LastAccessedAt = &now
	return nil
}

// UpdateEpisodicWeight updates the weight and effective frequency of a memory.
func (s *Store) UpdateEpisodicWeight(_ context.Context, id uuid.UUID, weight, effFreq float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.episodics[id]
	if !ok {
		return fmt.Errorf("episodic memory %s not found", id)
	}
	m.Weight = weight
	m.EffectiveFrequency = effFreq
	return nil
}

// MarkObsolete marks a memory as obsolete.
func (s *Store) MarkObsolete(_ context.Context, id uuid.UUID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.episodics[id]
	if !ok {
		return fmt.Errorf("episodic memory %s not found", id)
	}
	m.ObsoletedAt = &at
	return nil
}

// WriteSemantic stores a semantic memory.
func (s *Store) WriteSemantic(_ context.Context, mem *models.SemanticMemory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *mem
	s.semantics[mem.ID] = &clone
	return nil
}

// GetSemantic retrieves a single semantic memory by ID.
func (s *Store) GetSemantic(_ context.Context, id uuid.UUID) (*models.SemanticMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.semantics[id]
	if !ok {
		return nil, fmt.Errorf("semantic memory %s not found", id)
	}
	clone := *m
	return &clone, nil
}

// SearchSemantic filters semantic memories by the request criteria.
func (s *Store) SearchSemantic(_ context.Context, req store.SemanticSearchRequest) ([]models.SemanticMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var candidates []models.SemanticMemory
	for _, m := range s.semantics {
		if req.MemoryType != "" && m.Type != req.MemoryType {
			continue
		}
		if req.ProjectID != "" && m.EntityGroup != req.ProjectID {
			continue
		}
		candidates = append(candidates, *m)
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

// CreateEdge stores a directed edge between two memory nodes.
func (s *Store) CreateEdge(_ context.Context, from, to uuid.UUID, edgeType string, props map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.edges = append(s.edges, store.Edge{
		FromID:     from,
		ToID:       to,
		EdgeType:   edgeType,
		Properties: props,
		CreatedAt:  time.Now().UTC(),
	})
	return nil
}

// GetEdges retrieves edges for a node.
func (s *Store) GetEdges(_ context.Context, nodeID uuid.UUID, edgeType string, direction string) ([]store.Edge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []store.Edge
	for _, e := range s.edges {
		if edgeType != "" && e.EdgeType != edgeType {
			continue
		}
		switch direction {
		case "out":
			if e.FromID != nodeID {
				continue
			}
		case "in":
			if e.ToID != nodeID {
				continue
			}
		default:
			if e.FromID != nodeID && e.ToID != nodeID {
				continue
			}
		}
		result = append(result, e)
	}
	return result, nil
}

// ScanRecent returns episodic memories modified since a given time.
func (s *Store) ScanRecent(_ context.Context, since time.Time, limit int) ([]store.EpisodicScanItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []store.EpisodicScanItem
	for _, m := range s.episodics {
		if m.CreatedAt.Before(since) {
			continue
		}
		if m.ObsoletedAt != nil {
			continue
		}
		la := m.CreatedAt
		if m.LastAccessedAt != nil {
			la = *m.LastAccessedAt
		}
		items = append(items, store.EpisodicScanItem{
			ID:           m.ID,
			Summary:      m.Summary,
			TaskType:     m.TaskType,
			ProjectID:    m.ProjectID,
			Language:     m.Language,
			EntityGroup:  m.EntityGroup,
			EntityID:     m.EntityGroup,
			Weight:       m.Weight,
			LastAccessed: la,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].LastAccessed.After(items[j].LastAccessed)
	})

	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items, nil
}

// SaveConsolidationRun stores a consolidation run result.
func (s *Store) SaveConsolidationRun(_ context.Context, run *models.ConsolidationRunResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *run
	s.consRuns[run.RunID] = &clone
	return nil
}

// GetConsolidationRun retrieves a consolidation run result by ID.
func (s *Store) GetConsolidationRun(_ context.Context, runID uuid.UUID) (*models.ConsolidationRunResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.consRuns[runID]
	if !ok {
		return nil, fmt.Errorf("consolidation run %s not found", runID)
	}
	clone := *run
	return &clone, nil
}

// HealthCheck always succeeds for the in-memory store.
func (s *Store) HealthCheck(_ context.Context) error { return nil }

// Close is a no-op for the in-memory store.
func (s *Store) Close() error { return nil }

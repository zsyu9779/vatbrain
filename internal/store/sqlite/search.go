package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// scoredEpisodic pairs a memory with its similarity score.
type scoredEpisodic struct {
	mem   models.EpisodicMemory
	score float64
}

// SearchEpisodic searches episodic memories. If an embedding is provided in the
// request, candidates are ranked by in-process cosine similarity. Otherwise,
// results are ranked by weight descending.
func (s *Store) SearchEpisodic(_ context.Context, req store.EpisodicSearchRequest) ([]models.EpisodicMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check hot cache for non-embedding queries
	if req.Embedding == nil {
		cacheKey := fmt.Sprintf("ep:%s:%s:%s:%v:%d", req.ProjectID, req.Language, req.TaskType, req.IncludeObsolete, req.Limit)
		if cached, ok := s.hotCache.Get(cacheKey); ok {
			return cached, nil
		}
	}

	where := []string{"1=1"}
	args := []any{}

	if req.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, req.ProjectID)
	}
	if req.Language != "" {
		where = append(where, "language = ?")
		args = append(args, req.Language)
	}
	if req.TaskType != "" {
		where = append(where, "task_type = ?")
		args = append(args, string(req.TaskType))
	}
	if req.MinWeight > 0 {
		where = append(where, "weight >= ?")
		args = append(args, req.MinWeight)
	}
	if !req.IncludeObsolete {
		where = append(where, "obsoleted_at IS NULL")
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	fetchLimit := limit
	if req.Embedding != nil {
		fetchLimit = limit * 5
		if fetchLimit > 500 {
			fetchLimit = 500
		}
	}

	query := fmt.Sprintf(`
		SELECT id, project_id, language, task_type, summary, source_type,
		       trust_level, weight, effective_frequency, entity_group,
		       context_vector, full_snapshot_uri,
		       created_at, last_accessed_at, obsoleted_at
		FROM episodic_memories
		WHERE %s
		ORDER BY weight DESC
		LIMIT ?
	`, strings.Join(where, " AND "))

	args = append(args, fetchLimit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates, err := scanEpisodicRows(rows)
	if err != nil {
		return nil, err
	}

	var results []models.EpisodicMemory

	if req.Embedding != nil && len(candidates) > 0 {
		var ranked []scoredEpisodic
		for _, m := range candidates {
			if len(m.ContextVector) == 0 {
				continue
			}
			emb := vector.Float32To64(m.ContextVector)
			if len(emb) != len(req.Embedding) {
				continue
			}
			ranked = append(ranked, scoredEpisodic{
				mem:   m,
				score: vector.CosineSimilarity(req.Embedding, emb),
			})
		}

		sortScoredEpisodics(ranked)

		if limit > len(ranked) {
			limit = len(ranked)
		}
		results = make([]models.EpisodicMemory, limit)
		for i := range limit {
			results[i] = ranked[i].mem
		}
	} else {
		if limit > len(candidates) {
			limit = len(candidates)
		}
		results = candidates[:limit]
	}

	// Populate hot cache for non-embedding queries
	if req.Embedding == nil {
		cacheKey := fmt.Sprintf("ep:%s:%s:%s:%v:%d", req.ProjectID, req.Language, req.TaskType, req.IncludeObsolete, req.Limit)
		s.hotCache.Set(cacheKey, results)
	}

	return results, nil
}

// SearchSemantic searches semantic memories.
func (s *Store) SearchSemantic(_ context.Context, req store.SemanticSearchRequest) ([]models.SemanticMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	where := []string{"1=1"}
	args := []any{}

	if req.MemoryType != "" {
		where = append(where, "type = ?")
		args = append(args, string(req.MemoryType))
	}
	if req.ProjectID != "" {
		where = append(where, "entity_group = ?")
		args = append(args, req.ProjectID)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	query := fmt.Sprintf(`
		SELECT id, type, content, source_type, trust_level, weight,
		       effective_frequency, entity_group, consolidation_run_id,
		       backtest_accuracy, source_episodic_ids,
		       created_at, last_accessed_at, obsoleted_at
		FROM semantic_memories
		WHERE %s
		ORDER BY weight DESC
		LIMIT ?
	`, strings.Join(where, " AND "))

	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSemanticRows(rows)
}

// sortScoredEpisodics sorts scored items by cosine similarity descending,
// with weight as tie-breaker.
func sortScoredEpisodics(items []scoredEpisodic) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[i].score < items[j].score ||
				(items[i].score == items[j].score && items[i].mem.Weight < items[j].mem.Weight) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

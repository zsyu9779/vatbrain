package sqlite

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// scoredEpisodic pairs a memory with its similarity score.
type scoredEpisodic struct {
	mem   models.EpisodicMemory
	score float64
}

// embeddingRankPool bounds the project candidates fetched for in-Go
// cosine/surprise ranking. Generous enough that a benchmark user's full
// episodic set (HaluMem Medium ~2500) is ranked holistically, while still
// capping memory: each candidate carries a 2048-dim context vector (~16KB),
// so 5000 candidates ≈ 80MB transient at the worst case.
const embeddingRankPool = 5000

// defaultRrfK is the RRF fusion constant used when EpisodicSearchRequest.RrfK
// is left at 0. 60 is the conventional value (Cornack et al. 2009).
const defaultRrfK = 60

// SearchEpisodic searches episodic memories. If an embedding is provided in the
// request, candidates are ranked by in-process cosine similarity. Otherwise,
// results are ranked by weight descending.
func (s *Store) SearchEpisodic(_ context.Context, req store.EpisodicSearchRequest) ([]models.EpisodicMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Time-filtered / time-sorted queries bypass the hot cache: their result
	// is inherently freshness-sensitive, and the window bounds change on every
	// call, so caching would serve stale windows.
	timeConstrained := !req.OccurredAfter.IsZero() ||
		!req.OccurredBefore.IsZero() || req.SortByOccurredAt

	// Check hot cache for non-embedding queries
	if req.Embedding == nil && !timeConstrained {
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
	// v0.4 temporal window: occurred_at is stored as UTC RFC3339 text (same
	// formatting as the write path), so lexicographic comparison is
	// chronological. Bounds are inclusive.
	if !req.OccurredAfter.IsZero() {
		where = append(where, "occurred_at >= ?")
		args = append(args, req.OccurredAfter.UTC().Format(time.RFC3339))
	}
	if !req.OccurredBefore.IsZero() {
		where = append(where, "occurred_at <= ?")
		args = append(args, req.OccurredBefore.UTC().Format(time.RFC3339))
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	// SortByOccurredAt orders results by event time (most recent first) —
	// "最近一次" semantics. On the structured path it becomes the SQL order;
	// on the embedding/surprise paths the SQL order is irrelevant (Go
	// re-ranks), so the time preference is applied after ranking: rank-then-
	// sort, top-K relevant ordered by occurred_at descending.
	orderBy := "weight DESC"
	if req.SortByOccurredAt {
		orderBy = "occurred_at DESC"
	}
	// goRanking is true when cosine/surprise ranking must happen in Go: the
	// SQL layer then fetches a generous candidate pool for local re-ranking.
	goRanking := req.Embedding != nil || req.SurpriseBoost > 0
	fetchLimit := limit
	if goRanking {
		// The old limit*5 (capped at 500) was too small: with near-uniform
		// weights (e.g. a freshly ingested benchmark user), `ORDER BY weight
		// DESC` selects an arbitrary subset and the true best-embedding match
		// can fall outside the pool — which pinned pinpoint-fact recall at
		// ~10% on HaluMem.
		fetchLimit = embeddingRankPool
	}

	query := fmt.Sprintf(`
		SELECT id, project_id, language, task_type, summary, source_type,
		       trust_level, weight, effective_frequency, entity_group,
		       context_vector, full_snapshot_uri, is_correction, surprise_score,
		       created_at, last_accessed_at, obsoleted_at, occurred_at
		FROM episodic_memories
		WHERE %s
		ORDER BY %s
		LIMIT ?
	`, strings.Join(where, " AND "), orderBy)

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

	if req.Embedding != nil && len(candidates) > 0 && goRanking {
		var ranked []scoredEpisodic
		if req.Query != "" {
			// RRF fusion: the lexical channel (query-vs-summary bigram
			// overlap) rescues exact-keyword facts the semantic channel ranks
			// low — and vice versa — while candidates ranked by a single
			// channel still compete through it.
			ranked = rrfRanked(candidates, req)
		} else {
			ranked = semanticScores(candidates, req.Embedding)
			// Surprise boost promotes high-surprise memories above otherwise
			// equal peers without disturbing pure-cosine ordering.
			if req.SurpriseBoost > 0 {
				for i := range ranked {
					ranked[i].score *= 1 + req.SurpriseBoost*ranked[i].mem.SurpriseScore
				}
			}
		}

		sortScoredEpisodics(ranked)
		results = topScoredEpisodics(ranked, limit, req.SortByOccurredAt)
	} else if goRanking {
		// Weighted surprise ranking: order by weight × surprise factor so
		// prediction-error memories surface above otherwise-equal peers.
		var ranked []scoredEpisodic
		for _, m := range candidates {
			ranked = append(ranked, scoredEpisodic{
				mem:   m,
				score: m.Weight * (1 + req.SurpriseBoost*m.SurpriseScore),
			})
		}
		sortScoredEpisodics(ranked)
		results = topScoredEpisodics(ranked, limit, req.SortByOccurredAt)
	} else {
		// Either no Go-side ranking (plain weight/occurred_at SQL order) or a
		// time-sorted query whose SQL order already decided the sequence.
		if limit > len(candidates) {
			limit = len(candidates)
		}
		results = candidates[:limit]
	}

	// Populate hot cache for non-embedding queries
	if req.Embedding == nil && !timeConstrained {
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
		       backtest_accuracy, source_episodic_ids, surprise_score,
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

// topScoredEpisodics truncates a relevance-ranked slice to the top limit and,
// when timeSorted ("最近一次" semantics), re-orders that slice by occurred_at
// descending so the most recent of the relevant memories surface first —
// rank-then-sort, matching the lexical fallback path in the provider.
func topScoredEpisodics(ranked []scoredEpisodic, limit int, timeSorted bool) []models.EpisodicMemory {
	if limit > len(ranked) {
		limit = len(ranked)
	}
	ranked = ranked[:limit]
	if timeSorted {
		sort.Slice(ranked, func(i, j int) bool {
			return ranked[i].mem.EffectiveOccurredAt().After(ranked[j].mem.EffectiveOccurredAt())
		})
	}
	results := make([]models.EpisodicMemory, limit)
	for i := range limit {
		results[i] = ranked[i].mem
	}
	return results
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

// rrfRanked ranks the hard-constraint candidate pool by reciprocal rank
// fusion of two channels: semantic (embedding cosine against the stored
// context vector, skipping vectorless candidates) and lexical (query-vs-
// summary character-bigram overlap, CJK-safe — the keyword channel's feature
// machinery). Each channel yields a 1-based rank list; a candidate's fused
// score is Σ 1/(K + rank) over the lists it appears in, so a memory that one
// channel ranks last but the other ranks first still competes. Candidates
// ranked by neither channel score zero and are excluded. SurpriseBoost is
// applied on the fused score, promoting high-surprise memories above
// otherwise-equal peers without disturbing fusion ordering.
func rrfRanked(candidates []models.EpisodicMemory,
	req store.EpisodicSearchRequest) []scoredEpisodic {
	k := req.RrfK
	if k <= 0 {
		k = defaultRrfK
	}

	sem := semanticScores(candidates, req.Embedding)
	sortScoredEpisodics(sem)

	lex := make([]scoredEpisodic, 0, len(candidates))
	for _, m := range candidates {
		if s := embedder.BigramOverlap(req.Query, m.Summary); s > 0 {
			lex = append(lex, scoredEpisodic{mem: m, score: s})
		}
	}
	sortScoredEpisodics(lex)

	semRank := rankMap(sem)
	lexRank := rankMap(lex)

	fused := make([]scoredEpisodic, 0, len(candidates))
	for _, m := range candidates {
		id := m.ID.String()
		score := 0.0
		if r, ok := semRank[id]; ok {
			score += 1.0 / float64(k+r)
		}
		if r, ok := lexRank[id]; ok {
			score += 1.0 / float64(k+r)
		}
		if score == 0 {
			continue
		}
		if req.SurpriseBoost > 0 {
			score *= 1 + req.SurpriseBoost*m.SurpriseScore
		}
		fused = append(fused, scoredEpisodic{mem: m, score: score})
	}
	return fused
}

// semanticScores scores candidates by embedding cosine against the stored
// context vector — the shared semantic channel of the pure-embedding ranking
// and the RRF fusion. Vectorless candidates and dimension mismatches are
// skipped. Results are unsorted; callers rank them.
func semanticScores(candidates []models.EpisodicMemory,
	query []float64) []scoredEpisodic {
	out := make([]scoredEpisodic, 0, len(candidates))
	for _, m := range candidates {
		if len(m.ContextVector) == 0 {
			continue
		}
		emb := vector.Float32To64(m.ContextVector)
		if len(emb) != len(query) {
			continue
		}
		out = append(out, scoredEpisodic{
			mem:   m,
			score: vector.CosineSimilarity(query, emb),
		})
	}
	return out
}

// rankMap maps each ranked item's memory ID to its 1-based position in the
// list — the input representation of reciprocal rank fusion.
func rankMap(items []scoredEpisodic) map[string]int {
	m := make(map[string]int, len(items))
	for i, item := range items {
		m[item.mem.ID.String()] = i + 1
	}
	return m
}

package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
)

// handleSearch implements POST /api/v0/memories/search.
//
// Pipeline: embed query → Neo4j candidates → Contextual Gating →
// pgvector similarity → merge with semantic results → respond.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req models.SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Query == "" {
		respondError(w, http.StatusBadRequest, "query is required")
		return
	}
	if req.TopK <= 0 {
		req.TopK = 10
	}

	ctx := r.Context()

	// Generate query embedding.
	queryEmb, err := s.Embedder.Embed(ctx, req.Query)
	if err != nil {
		slog.Error("embed query", "err", err)
		respondError(w, http.StatusInternalServerError, "embedding failed")
		return
	}

	// Fetch episodic candidates from Neo4j.
	episodics, err := fetchEpisodicCandidates(ctx, s.Neo4j, req.Context, req.IncludeDormant)
	if err != nil {
		slog.Error("neo4j fetch episodics", "err", err)
		respondError(w, http.StatusInternalServerError, "fetch candidates failed")
		return
	}

	// Stage 1: Contextual Gating.
	gating := &core.ContextualGating{}
	filtered, stats := gating.FilterAndRank(episodics, req.Context,
		s.WeightDecay.CoolingThreshold, s.RetrievalEngine.MaxCandidates)

	// Collect filtered IDs for pgvector similarity search.
	filteredIDs := make([]string, len(filtered))
	for i, f := range filtered {
		filteredIDs[i] = f.MemoryID
	}

	// Build id → episodic map for lookup.
	epByID := make(map[string]models.EpisodicMemory, len(episodics))
	for _, ep := range episodics {
		epByID[ep.ID.String()] = ep
	}

	// Stage 2: Semantic ranking via pgvector.
	rankStart := time.Now()
	var results []models.SearchResultItem

	if len(filteredIDs) > 0 {
		pgResults, pgErr := s.Pgvector.SimilaritySearch(ctx, queryEmb, req.TopK, filteredIDs)
		if pgErr != nil {
			slog.Warn("pgvector similarity search", "err", pgErr)
		}

		for _, pr := range pgResults {
			ep, ok := epByID[pr.MemoryID]
			if !ok {
				continue
			}
			results = append(results, models.SearchResultItem{
				MemoryID:       ep.ID,
				Type:           "episodic",
				Content:        ep.Summary,
				TrustLevel:     ep.TrustLevel,
				Weight:         ep.Weight,
				RelevanceScore: pr.Similarity,
			})
		}
	}

	semanticRankMs := time.Since(rankStart).Milliseconds()

	// Fetch and filter semantic candidates.
	semantics, semErr := fetchSemanticCandidates(ctx, s.Neo4j)
	if semErr != nil {
		slog.Warn("neo4j fetch semantics", "err", semErr)
	}

	for _, sem := range semantics {
		if tokenOverlap(req.Query, sem.Content) {
			results = append(results, models.SearchResultItem{
				MemoryID:       sem.ID,
				Type:           "semantic",
				Content:        sem.Content,
				TrustLevel:     sem.TrustLevel,
				Weight:         sem.Weight,
				RelevanceScore: 0.5,
			})
		}
	}

	// Sort by relevance descending, cap at TopK.
	sort.Slice(results, func(i, j int) bool {
		return results[i].RelevanceScore > results[j].RelevanceScore
	})
	if len(results) > req.TopK {
		results = results[:req.TopK]
	}

	respondJSON(w, http.StatusOK, models.SearchResponse{
		Results:            results,
		ContextFilterStats:  stats,
		SemanticRankTimeMs: semanticRankMs,
	})
}

// fetchEpisodicCandidates queries Neo4j for episodic memories matching the
// search context.
func fetchEpisodicCandidates(
	ctx context.Context,
	client interface {
		ExecuteRead(ctx context.Context, fn func(tx neo4j.ManagedTransaction) (any, error)) (any, error)
	},
	sc models.SearchContext,
	includeDormant bool,
) ([]models.EpisodicMemory, error) {
	raw, err := client.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		cyp := `
			MATCH (e:EpisodicMemory)
			WHERE e.project_id = $projectID AND e.language = $language
		`
		if !includeDormant {
			cyp += ` AND e.obsoleted_at IS NULL`
		}
		cyp += `
			RETURN e.id, e.project_id, e.language, e.task_type, e.summary,
			       e.source_type, e.trust_level, e.weight, e.effective_frequency,
			       e.created_at, e.last_accessed_at, e.obsoleted_at,
			       e.entity_group, e.embedding_id
			ORDER BY e.weight DESC
			LIMIT 500
		`

		records, err := tx.Run(ctx, cyp, map[string]any{
			"projectID": sc.ProjectID,
			"language":  sc.Language,
		})
		if err != nil {
			return nil, err
		}

		var results []models.EpisodicMemory
		for records.Next(ctx) {
			r := records.Record()
			m := scanEpisodic(r)
			if m != nil {
				results = append(results, *m)
			}
		}
		return results, records.Err()
	})
	if err != nil {
		return nil, err
	}

	episodics, ok := raw.([]models.EpisodicMemory)
	if !ok {
		return nil, nil
	}
	return episodics, nil
}

// fetchSemanticCandidates queries Neo4j for all non-obsoleted semantic memories.
func fetchSemanticCandidates(
	ctx context.Context,
	client interface {
		ExecuteRead(ctx context.Context, fn func(tx neo4j.ManagedTransaction) (any, error)) (any, error)
	},
) ([]models.SemanticMemory, error) {
	raw, err := client.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		cyp := `
			MATCH (s:SemanticMemory)
			WHERE s.obsoleted_at IS NULL
			RETURN s.id, s.type, s.content, s.source_type, s.trust_level, s.weight,
			       s.effective_frequency, s.created_at, s.last_accessed_at,
			       s.obsoleted_at, s.entity_group, s.consolidation_run_id,
			       s.backtest_accuracy, s.source_episodic_ids
			LIMIT 200
		`

		records, err := tx.Run(ctx, cyp, nil)
		if err != nil {
			return nil, err
		}

		var results []models.SemanticMemory
		for records.Next(ctx) {
			r := records.Record()
			m := scanSemantic(r)
			if m != nil {
				results = append(results, *m)
			}
		}
		return results, records.Err()
	})
	if err != nil {
		return nil, err
	}

	semantics, ok := raw.([]models.SemanticMemory)
	if !ok {
		return nil, nil
	}
	return semantics, nil
}

// scanEpisodic converts a neo4j Record into an EpisodicMemory.
func scanEpisodic(r *neo4j.Record) *models.EpisodicMemory {
	id, _, _ := neo4j.GetRecordValue[string](r, "e.id")
	if id == "" {
		return nil
	}
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return nil
	}

	projectID, _, _ := neo4j.GetRecordValue[string](r, "e.project_id")
	lang, _, _ := neo4j.GetRecordValue[string](r, "e.language")
	taskType, _, _ := neo4j.GetRecordValue[string](r, "e.task_type")
	summary, _, _ := neo4j.GetRecordValue[string](r, "e.summary")
	sourceType, _, _ := neo4j.GetRecordValue[string](r, "e.source_type")
	trustLevel, _, _ := neo4j.GetRecordValue[int64](r, "e.trust_level")
	weight, _, _ := neo4j.GetRecordValue[float64](r, "e.weight")
	effFreq, _, _ := neo4j.GetRecordValue[float64](r, "e.effective_frequency")
	createdAt, _, _ := neo4j.GetRecordValue[time.Time](r, "e.created_at")
	lastAccessedAt, isNil, _ := neo4j.GetRecordValue[time.Time](r, "e.last_accessed_at")
	entityGroup, _, _ := neo4j.GetRecordValue[string](r, "e.entity_group")
	embeddingID, _, _ := neo4j.GetRecordValue[string](r, "e.embedding_id")

	m := &models.EpisodicMemory{
		ID:                 parsedID,
		ProjectID:          projectID,
		Language:           lang,
		TaskType:           models.TaskType(taskType),
		Summary:            summary,
		SourceType:         models.SourceType(sourceType),
		TrustLevel:         models.TrustLevel(trustLevel),
		Weight:             weight,
		EffectiveFrequency: effFreq,
		CreatedAt:          createdAt,
		EntityGroup:        entityGroup,
		EmbeddingID:        embeddingID,
	}
	if !isNil {
		m.LastAccessedAt = &lastAccessedAt
	}
	return m
}

// scanSemantic converts a neo4j Record into a SemanticMemory.
func scanSemantic(r *neo4j.Record) *models.SemanticMemory {
	id, _, _ := neo4j.GetRecordValue[string](r, "s.id")
	if id == "" {
		return nil
	}
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return nil
	}

	memType, _, _ := neo4j.GetRecordValue[string](r, "s.type")
	content, _, _ := neo4j.GetRecordValue[string](r, "s.content")
	sourceType, _, _ := neo4j.GetRecordValue[string](r, "s.source_type")
	trustLevel, _, _ := neo4j.GetRecordValue[int64](r, "s.trust_level")
	weight, _, _ := neo4j.GetRecordValue[float64](r, "s.weight")
	effFreq, _, _ := neo4j.GetRecordValue[float64](r, "s.effective_frequency")
	createdAt, _, _ := neo4j.GetRecordValue[time.Time](r, "s.created_at")
	lastAccessedAt, isNil, _ := neo4j.GetRecordValue[time.Time](r, "s.last_accessed_at")
	entityGroup, _, _ := neo4j.GetRecordValue[string](r, "s.entity_group")
	runID, _, _ := neo4j.GetRecordValue[string](r, "s.consolidation_run_id")
	accuracy, _, _ := neo4j.GetRecordValue[float64](r, "s.backtest_accuracy")

	m := &models.SemanticMemory{
		ID:                 parsedID,
		Type:               models.MemoryType(memType),
		Content:            content,
		SourceType:         models.SourceType(sourceType),
		TrustLevel:         models.TrustLevel(trustLevel),
		Weight:             weight,
		EffectiveFrequency: effFreq,
		CreatedAt:          createdAt,
		EntityGroup:        entityGroup,
		ConsolidationRunID: runID,
		BacktestAccuracy:   accuracy,
	}
	if !isNil {
		m.LastAccessedAt = &lastAccessedAt
	}

	// Parse source_episodic_ids from the record.
	rawIDs, _, _ := neo4j.GetRecordValue[[]any](r, "s.source_episodic_ids")
	for _, rawID := range rawIDs {
		if s, ok := rawID.(string); ok {
			if uid, parseErr := uuid.Parse(s); parseErr == nil {
				m.SourceEpisodicIDs = append(m.SourceEpisodicIDs, uid)
			}
		}
	}

	return m
}

// tokenOverlap is a v0.1 approximation for semantic matching between a query
// and a semantic memory's content. Returns true if they share meaningful words.
func tokenOverlap(query, content string) bool {
	qTokens := tokenizeLower(query)
	cTokens := tokenizeLower(content)
	if len(qTokens) == 0 {
		return false
	}

	cSet := make(map[string]struct{}, len(cTokens))
	for _, t := range cTokens {
		cSet[t] = struct{}{}
	}

	matches := 0
	for _, t := range qTokens {
		if _, ok := cSet[t]; ok {
			matches++
		}
	}
	return matches >= 2 || (len(qTokens) > 0 && float64(matches)/float64(len(qTokens)) > 0.3)
}

// tokenizeLower splits text into lowercase tokens longer than 3 chars.
func tokenizeLower(s string) []string {
	var tokens []string
	start := -1
	for i, r := range s {
		if isAlphaNum(r) {
			if start < 0 {
				start = i
			}
		} else {
			if start >= 0 && i-start > 3 {
				tokens = append(tokens, strings.ToLower(s[start:i]))
			}
			start = -1
		}
	}
	if start >= 0 && len(s)-start > 3 {
		tokens = append(tokens, strings.ToLower(s[start:]))
	}
	return tokens
}

func isAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
)

func searchMemoriesTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("search_memories",
		mcp.WithDescription("Search memories using two-stage retrieval: contextual gating + semantic ranking."),
		mcp.WithString("query", mcp.Required(),
			mcp.Description("Search query describing what you're looking for")),
		mcp.WithString("project_id",
			mcp.Description("Filter by project identifier")),
		mcp.WithString("language",
			mcp.Description("Filter by programming language")),
		mcp.WithString("task_type",
			mcp.Description("Filter by task type"),
			mcp.Enum("debug", "feature", "refactor", "review")),
		mcp.WithNumber("top_k",
			mcp.Description("Maximum number of results to return (default 10)")),
		mcp.WithBoolean("include_dormant",
			mcp.Description("Include dormant/obsoleted memories (default false)")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query, err := req.RequireString("query")
			if err != nil {
				return mcp.NewToolResultError("query is required"), nil
			}
			topK := int(req.GetFloat("top_k", 10))
			if topK <= 0 {
				topK = 10
			}

			sc := models.SearchContext{
				ProjectID: req.GetString("project_id", ""),
				Language:  req.GetString("language", ""),
				TaskType:  models.TaskType(req.GetString("task_type", "")),
			}
			includeDormant := req.GetBool("include_dormant", false)

			queryEmb, embErr := a.Embedder.Embed(ctx, query)
			if embErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("embedding failed: %v", embErr)), nil
			}

			episodics, fErr := fetchEpisodicCandidates(ctx, a, sc, includeDormant)
			if fErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("fetch candidates failed: %v", fErr)), nil
			}

			gating := &core.ContextualGating{}
			filtered, _ := gating.FilterAndRank(episodics, sc,
				a.WeightDecay.CoolingThreshold, a.RetrievalEngine.MaxCandidates)

			filteredIDs := make([]string, len(filtered))
			for i, f := range filtered {
				filteredIDs[i] = f.MemoryID
			}

			epByID := make(map[string]models.EpisodicMemory, len(episodics))
			for _, ep := range episodics {
				epByID[ep.ID.String()] = ep
			}

			var results []searchMemoryOutput
			if len(filteredIDs) > 0 {
				pgResults, pgErr := a.Pgvector.SimilaritySearch(ctx, queryEmb, topK, filteredIDs)
				if pgErr != nil {
					slog.Warn("pgvector similarity search", "err", pgErr)
				}
				for _, pr := range pgResults {
					ep, ok := epByID[pr.MemoryID]
					if !ok {
						continue
					}
					results = append(results, searchMemoryOutput{
						MemoryID:       ep.ID,
						Type:           "episodic",
						Content:        ep.Summary,
						TrustLevel:     int(ep.TrustLevel),
						Weight:         ep.Weight,
						RelevanceScore: pr.Similarity,
					})
				}
			}

			semantics, semErr := fetchSemanticCandidates(ctx, a)
			if semErr != nil {
				slog.Warn("neo4j fetch semantics", "err", semErr)
			}
			for _, sem := range semantics {
				if tokenOverlap(query, sem.Content) {
					results = append(results, searchMemoryOutput{
						MemoryID:       sem.ID,
						Type:           "semantic",
						Content:        sem.Content,
						TrustLevel:     int(sem.TrustLevel),
						Weight:         sem.Weight,
						RelevanceScore: 0.5,
						SourceIDs:      sem.SourceEpisodicIDs,
					})
				}
			}

			sort.Slice(results, func(i, j int) bool {
				return results[i].RelevanceScore > results[j].RelevanceScore
			})
			if len(results) > topK {
				results = results[:topK]
			}

			resp, jErr := mcp.NewToolResultJSON(results)
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

type searchMemoryOutput struct {
	MemoryID       uuid.UUID   `json:"memory_id"`
	Type           string      `json:"type"`
	Content        string      `json:"content"`
	TrustLevel     int         `json:"trust_level"`
	Weight         float64     `json:"weight"`
	RelevanceScore float64     `json:"relevance_score"`
	SourceIDs      []uuid.UUID `json:"source_ids,omitempty"`
}

func fetchEpisodicCandidates(
	ctx context.Context,
	a *app.App,
	sc models.SearchContext,
	includeDormant bool,
) ([]models.EpisodicMemory, error) {
	raw, err := a.Neo4j.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
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
	episodics, _ := raw.([]models.EpisodicMemory)
	return episodics, nil
}

func fetchSemanticCandidates(ctx context.Context, a *app.App) ([]models.SemanticMemory, error) {
	raw, err := a.Neo4j.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		records, err := tx.Run(ctx, `
			MATCH (s:SemanticMemory)
			WHERE s.obsoleted_at IS NULL
			RETURN s.id, s.type, s.content, s.source_type, s.trust_level, s.weight,
			       s.effective_frequency, s.created_at, s.last_accessed_at,
			       s.obsoleted_at, s.entity_group, s.consolidation_run_id,
			       s.backtest_accuracy, s.source_episodic_ids
			LIMIT 200
		`, nil)
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
	semantics, _ := raw.([]models.SemanticMemory)
	return semantics, nil
}

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
	rawIDs, _, _ := neo4j.GetRecordValue[[]any](r, "s.source_episodic_ids")
	for _, rawID := range rawIDs {
		if s, ok := rawID.(string); ok {
			if uid, pErr := uuid.Parse(s); pErr == nil {
				m.SourceEpisodicIDs = append(m.SourceEpisodicIDs, uid)
			}
		}
	}
	return m
}

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

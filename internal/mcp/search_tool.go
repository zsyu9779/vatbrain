package mcp

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
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
		mcp.WithString("entity_id",
			mcp.Description("Entity anchor for pitfall injection (e.g. func:NewRedisPool)")),
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
				EntityID:  req.GetString("entity_id", ""),
			}
			includeDormant := req.GetBool("include_dormant", false)

			queryEmb, embErr := a.Embedder.Embed(ctx, query)
			if embErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("embedding failed: %v", embErr)), nil
			}

			// Search episodic memories via Store.
			episodics, fErr := a.Store.SearchEpisodic(ctx, store.EpisodicSearchRequest{
				ProjectID:       sc.ProjectID,
				Language:        sc.Language,
				Embedding:       vector.Float32To64(queryEmb),
				Limit:           a.RetrievalEngine.MaxCandidates,
				IncludeObsolete: includeDormant,
			})
			if fErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("fetch candidates failed: %v", fErr)), nil
			}

			gating := &core.ContextualGating{}
			filtered, _ := gating.FilterAndRank(episodics, sc,
				a.WeightDecay.CoolingThreshold, a.RetrievalEngine.MaxCandidates)

			epByID := make(map[string]models.EpisodicMemory, len(episodics))
			for _, ep := range episodics {
				epByID[ep.ID.String()] = ep
			}

			var results []searchMemoryOutput
			for _, f := range filtered {
				ep, ok := epByID[f.MemoryID]
				if !ok {
					continue
				}
				results = append(results, searchMemoryOutput{
					MemoryID:       ep.ID,
					Type:           "episodic",
					Content:        ep.Summary,
					TrustLevel:     int(ep.TrustLevel),
					Weight:         ep.Weight,
					RelevanceScore: ep.Weight,
				})
			}

			// Fetch and filter semantic candidates.
			semantics, semErr := a.Store.SearchSemantic(ctx, store.SemanticSearchRequest{
				Limit: 200,
			})
			if semErr != nil {
				_ = semErr // best-effort
			}
			for _, sem := range semantics {
				if core.TokenOverlap(query, sem.Content) {
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

			// v0.2: Pitfall injection for entity-anchored searches.
			if sc.EntityID != "" && sc.ProjectID != "" {
				pitfalls, pfErr := a.Store.SearchPitfallByEntity(ctx, sc.EntityID, sc.ProjectID)
				if pfErr == nil {
					sort.Slice(pitfalls, func(i, j int) bool {
						if pitfalls[i].WasUserCorrected != pitfalls[j].WasUserCorrected {
							return pitfalls[i].WasUserCorrected
						}
						if pitfalls[i].OccurrenceCount != pitfalls[j].OccurrenceCount {
							return pitfalls[i].OccurrenceCount > pitfalls[j].OccurrenceCount
						}
						return pitfalls[i].Weight > pitfalls[j].Weight
					})
					pitfallLimit := 3
					if len(pitfalls) < pitfallLimit {
						pitfallLimit = len(pitfalls)
					}
					for _, p := range pitfalls[:pitfallLimit] {
						results = append(results, searchMemoryOutput{
							MemoryID:       p.ID,
							Type:           "pitfall",
							Content:        p.Signature,
							TrustLevel:     int(p.TrustLevel),
							Weight:         p.Weight,
							RelevanceScore: 0.6,
						})
					}
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

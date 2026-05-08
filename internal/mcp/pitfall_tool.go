package mcp

import (
	"context"
	"fmt"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

func searchPitfallsTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("search_pitfalls",
		mcp.WithDescription("Search Pitfall memories — error patterns learned from past debug sessions. Supports dual-key matching: entity_id (exact) + query embedding (semantic similarity)."),
		mcp.WithString("entity_id",
			mcp.Description("Code entity anchor (e.g. func:NewRedisPool)")),
		mcp.WithString("project_id",
			mcp.Description("Filter by project identifier")),
		mcp.WithString("query",
			mcp.Description("Search query describing the error pattern to find")),
		mcp.WithString("root_cause_category",
			mcp.Description("Filter by root cause"),
			mcp.Enum("CONCURRENCY", "RESOURCE_EXHAUSTION", "CONFIG", "CONTRACT_VIOLATION", "LOGIC_ERROR", "UNKNOWN")),
		mcp.WithNumber("top_k",
			mcp.Description("Maximum number of results (default 10)")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			entityID := req.GetString("entity_id", "")
			projectID := req.GetString("project_id", "")
			query := req.GetString("query", "")
			rootCause := req.GetString("root_cause_category", "")
			topK := int(req.GetFloat("top_k", 10))
			if topK <= 0 {
				topK = 10
			}

			var pitfalls []models.PitfallMemory
			var err error

			if entityID != "" {
				pitfalls, err = a.Store.SearchPitfallByEntity(ctx, entityID, projectID)
			} else {
				sr := store.PitfallSearchRequest{
					ProjectID:        projectID,
					RootCauseCategory: models.RootCause(rootCause),
					Limit:            topK,
				}
				if query != "" {
					emb, embErr := a.Embedder.Embed(ctx, query)
					if embErr == nil {
						sr.Embedding = vector.Float32To64(emb)
					}
				}
				pitfalls, err = a.Store.SearchPitfall(ctx, sr)
			}
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("pitfall search failed: %v", err)), nil
			}

			// Sort: user-corrected first, then occurrence_count desc.
			sort.Slice(pitfalls, func(i, j int) bool {
				if pitfalls[i].WasUserCorrected != pitfalls[j].WasUserCorrected {
					return pitfalls[i].WasUserCorrected
				}
				if pitfalls[i].OccurrenceCount != pitfalls[j].OccurrenceCount {
					return pitfalls[i].OccurrenceCount > pitfalls[j].OccurrenceCount
				}
				return pitfalls[i].Weight > pitfalls[j].Weight
			})

			type pitfallOutput struct {
				PitfallID         string `json:"pitfall_id"`
				EntityID          string `json:"entity_id"`
				Signature         string `json:"signature"`
				RootCauseCategory string `json:"root_cause_category"`
				FixStrategy       string `json:"fix_strategy"`
				WasUserCorrected  bool   `json:"was_user_corrected"`
				OccurrenceCount   int    `json:"occurrence_count"`
				Weight            float64 `json:"weight"`
			}

			limit := topK
			if len(pitfalls) < limit {
				limit = len(pitfalls)
			}
			output := make([]pitfallOutput, 0, limit)
			for _, p := range pitfalls[:limit] {
				output = append(output, pitfallOutput{
					PitfallID:         p.ID.String(),
					EntityID:          p.EntityID,
					Signature:         p.Signature,
					RootCauseCategory: string(p.RootCauseCategory),
					FixStrategy:       p.FixStrategy,
					WasUserCorrected:  p.WasUserCorrected,
					OccurrenceCount:   p.OccurrenceCount,
					Weight:            p.Weight,
				})
			}

			resp, jErr := mcp.NewToolResultJSON(output)
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

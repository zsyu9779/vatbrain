package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
)

func getMemoryWeightTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("get_memory_weight",
		mcp.WithDescription("Get the full weight breakdown for a memory, including "+
			"effective frequency, experience decay, and activity decay components."),
		mcp.WithString("memory_id", mcp.Required(),
			mcp.Description("UUID of the memory to inspect")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			memoryIDStr, err := req.RequireString("memory_id")
			if err != nil {
				return mcp.NewToolResultError("memory_id is required"), nil
			}
			memoryID, pErr := uuid.Parse(memoryIDStr)
			if pErr != nil {
				return mcp.NewToolResultError("invalid memory_id format"), nil
			}

			now := time.Now()

			mem, rErr := a.Store.GetEpisodic(ctx, memoryID)
			if rErr != nil || mem == nil {
				return mcp.NewToolResultError("memory not found"), nil
			}

			experienceDecay := a.WeightDecay.Weight(mem.EffectiveFrequency, mem.CreatedAt, mem.CreatedAt, now)
			activityDecay := 0.0
			if mem.LastAccessedAt != nil {
				activityDecay = a.WeightDecay.Weight(mem.EffectiveFrequency, mem.CreatedAt, *mem.LastAccessedAt, now)
			}

			resp, jErr := mcp.NewToolResultJSON(weightOutput{
				MemoryID:           memoryID,
				Weight:             mem.Weight,
				EffectiveFrequency: mem.EffectiveFrequency,
				ExperienceDecay:    experienceDecay,
				ActivityDecay:      activityDecay,
			})
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

func touchMemoryTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("touch_memory",
		mcp.WithDescription("Record a retrieval hit on a memory, updating its last_accessed_at "+
			"and recomputing the weight."),
		mcp.WithString("memory_id", mcp.Required(),
			mcp.Description("UUID of the memory that was retrieved")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			memoryIDStr, err := req.RequireString("memory_id")
			if err != nil {
				return mcp.NewToolResultError("memory_id is required"), nil
			}
			memoryID, pErr := uuid.Parse(memoryIDStr)
			if pErr != nil {
				return mcp.NewToolResultError("invalid memory_id format"), nil
			}

			now := time.Now()

			mem, rErr := a.Store.GetEpisodic(ctx, memoryID)
			if rErr != nil || mem == nil {
				return mcp.NewToolResultError("memory not found"), nil
			}

			newWeight := a.WeightDecay.Weight(1.0, mem.CreatedAt, now, now)
			newEffFreq := mem.EffectiveFrequency + 1.0

			if uErr := a.Store.UpdateEpisodicWeight(ctx, memoryID, newWeight, newEffFreq); uErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("touch update failed: %v", uErr)), nil
			}
			_ = a.Store.TouchEpisodic(ctx, memoryID, now)

			resp, jErr := mcp.NewToolResultJSON(touchOutput{
				MemoryID:  memoryID,
				NewWeight: newWeight,
			})
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

type weightOutput struct {
	MemoryID           uuid.UUID `json:"memory_id"`
	Weight             float64   `json:"weight"`
	EffectiveFrequency float64   `json:"effective_frequency"`
	ExperienceDecay    float64   `json:"experience_decay"`
	ActivityDecay      float64   `json:"activity_decay"`
}

type touchOutput struct {
	MemoryID  uuid.UUID `json:"memory_id"`
	NewWeight float64   `json:"new_weight"`
}

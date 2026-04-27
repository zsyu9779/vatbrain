package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
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
			var createdAt time.Time
			var lastAccessedAt time.Time
			var hasLastAccess bool
			var effFreq, weight float64

			raw, rErr := a.Neo4j.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
				records, err := tx.Run(ctx, `
				MATCH (e:EpisodicMemory {id: $id})
				RETURN e.created_at, e.last_accessed_at, e.effective_frequency, e.weight
			`, map[string]any{"id": memoryID.String()})
				if err != nil {
					return false, err
				}
				if !records.Next(ctx) {
					return false, records.Err()
				}
				r := records.Record()
				createdAt, _, _ = neo4j.GetRecordValue[time.Time](r, "e.created_at")
				lastAccessedAt, hasLastAccess, _ = neo4j.GetRecordValue[time.Time](r, "e.last_accessed_at")
				effFreq, _, _ = neo4j.GetRecordValue[float64](r, "e.effective_frequency")
				weight, _, _ = neo4j.GetRecordValue[float64](r, "e.weight")
				return true, records.Err()
			})
			if rErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("read memory failed: %v", rErr)), nil
			}
			if raw != true {
				return mcp.NewToolResultError("memory not found"), nil
			}

			experienceDecay := a.WeightDecay.Weight(effFreq, createdAt, createdAt, now)
			activityDecay := 0.0
			if hasLastAccess {
				activityDecay = a.WeightDecay.Weight(effFreq, createdAt, lastAccessedAt, now)
			}

			resp, jErr := mcp.NewToolResultJSON(weightOutput{
				MemoryID:           memoryID,
				Weight:             weight,
				EffectiveFrequency: effFreq,
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
			var createdAt time.Time
			var lastAccessedAt *time.Time

			raw, rErr := a.Neo4j.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
				records, err := tx.Run(ctx, `
				MATCH (e:EpisodicMemory {id: $id})
				RETURN e.created_at, e.last_accessed_at
			`, map[string]any{"id": memoryID.String()})
				if err != nil {
					return false, err
				}
				if !records.Next(ctx) {
					return false, records.Err()
				}
				r := records.Record()
				createdAt, _, _ = neo4j.GetRecordValue[time.Time](r, "e.created_at")
				la, laIsNil, _ := neo4j.GetRecordValue[time.Time](r, "e.last_accessed_at")
				if !laIsNil {
					lastAccessedAt = &la
				}
				return true, records.Err()
			})
			if rErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("read memory failed: %v", rErr)), nil
			}
			if raw != true {
				return mcp.NewToolResultError("memory not found"), nil
			}

			newWeight := a.WeightDecay.Weight(1.0, createdAt, now, now)
			_ = lastAccessedAt

			_, uErr := a.Neo4j.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
				_, err := tx.Run(ctx, `
				MATCH (e:EpisodicMemory {id: $id})
				SET e.last_accessed_at = $now,
				    e.weight = $newWeight,
				    e.effective_frequency = e.effective_frequency + 1
			`, map[string]any{
					"id":        memoryID.String(),
					"now":       now,
					"newWeight": newWeight,
				})
				return nil, err
			})
			if uErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("touch update failed: %v", uErr)), nil
			}

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

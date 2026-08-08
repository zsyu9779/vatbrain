package mcp

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
)

func writeMemoryTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("write_memory",
		mcp.WithDescription("Write an episodic memory through the significance gate, "+
			"embedding, pattern separation, and persistence pipeline."),
		mcp.WithString("project_id", mcp.Required(),
			mcp.Description("Project identifier — hard constraint for retrieval")),
		mcp.WithString("summary", mcp.Required(),
			mcp.Description("Summary of the memory to store")),
		mcp.WithString("language",
			mcp.Description("Programming language or framework context")),
		mcp.WithString("task_type",
			mcp.Description("Task type: debug, feature, refactor, or review"),
			mcp.Enum("debug", "feature", "refactor", "review")),
		mcp.WithString("entity_id",
			mcp.Description("Entity identifier for pattern separation (e.g. func:NewRedisPool)")),
		mcp.WithBoolean("user_confirmed",
			mcp.Description("Whether the user explicitly confirmed this memory")),
		mcp.WithBoolean("is_correction",
			mcp.Description("Whether this is a correction of previous information")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			projectID, err := req.RequireString("project_id")
			if err != nil {
				return mcp.NewToolResultError("project_id is required"), nil
			}
			summary, err := req.RequireString("summary")
			if err != nil {
				return mcp.NewToolResultError("summary is required"), nil
			}
			language := req.GetString("language", "")
			taskType := req.GetString("task_type", "")
			entityID := req.GetString("entity_id", "")
			userConfirmed := req.GetBool("user_confirmed", false)
			isCorrection := req.GetBool("is_correction", false)

			// Shared write pipeline: significance gate → embedding →
			// pattern-separation merge → persistence → link-on-write. The
			// hermes provider daemon routes through the same code.
			deps := core.WriteDeps{
				Store:       a.Store,
				Gate:        a.SignificanceGate,
				PatternSep:  a.PatternSeparation,
				WeightDecay: a.WeightDecay,
				Embedder:    a.Embedder,
				WorkingMem:  a.WorkingMemory,
			}
			event := core.WriteEvent{
				Summary:       summary,
				UserConfirmed: userConfirmed,
				IsCorrection:  isCorrection,
			}
			res, err := core.WriteMemory(ctx, deps, event,
				projectID, language, entityID, models.TaskType(taskType))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("write memory failed: %v", err)), nil
			}

			out := writeMemoryOutput{
				MemoryID:    res.MemoryID,
				Persisted:   res.Persisted,
				GateReason:  res.GateReason,
				MergeAction: string(res.MergeAction),
				Weight:      res.Weight,
			}
			resp, jErr := mcp.NewToolResultJSON(out)
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

type writeMemoryOutput struct {
	MemoryID    uuid.UUID `json:"memory_id"`
	Persisted   bool      `json:"persisted"`
	GateReason  string    `json:"gate_reason"`
	MergeAction string    `json:"merge_action"`
	Weight      float64   `json:"weight"`
}

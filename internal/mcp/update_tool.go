package mcp

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/core"
)

// signalUpdateTool is the explicit update signal (v0.4 Update Tracking):
// tell VatBrain that a memory carries newer information covering older
// memories of the same subject. The covered memories are marked obsolete,
// the newer memory's weight is boosted, and SUPERSEDED edges record the
// supersession — the same actions the write pipeline applies automatically
// when a temporally newer same-subject event arrives. Idempotent: a second
// call on the same memory finds nothing because the covered memories are
// already retired.
func signalUpdateTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("signal_update",
		mcp.WithDescription("Signal that a memory carries newer information about the same subject as older memories: the covered older memories are marked obsolete, the newer memory's weight is boosted (x1.5 by default), and SUPERSEDED edges record the supersession."),
		mcp.WithString("memory_id", mcp.Required(), mcp.Description("ID of the memory that carries the newer information")),
		mcp.WithNumber("boost", mcp.Description("Weight boost multiplier for the newer memory (default 1.5)")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			memStr, err := req.RequireString("memory_id")
			if err != nil {
				return mcp.NewToolResultError("memory_id is required"), nil
			}
			mid, err := uuid.Parse(memStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid memory_id: %v", err)), nil
			}
			boost := req.GetFloat("boost", 0) // 0 → tracker default

			res, err := core.RunUpdateTracking(ctx, a.Store, mid, boost)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("update tracking failed: %v", err)), nil
			}
			resp, jErr := mcp.NewToolResultJSON(res)
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

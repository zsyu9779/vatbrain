package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
)

func healthCheckTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("health_check",
		mcp.WithDescription("Check the health status of the storage backend. Returns 'healthy' if the store is reachable."),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if err := a.Store.HealthCheck(ctx); err != nil {
				resp, e := mcp.NewToolResultJSON(map[string]string{
					"status":  "degraded",
					"message": "unhealthy: " + err.Error(),
				})
				if e != nil {
					return mcp.NewToolResultError(e.Error()), nil
				}
				return resp, nil
			}

			resp, e := mcp.NewToolResultJSON(map[string]string{"status": "healthy"})
			if e != nil {
				return mcp.NewToolResultError(e.Error()), nil
			}
			return resp, nil
		},
	}
}

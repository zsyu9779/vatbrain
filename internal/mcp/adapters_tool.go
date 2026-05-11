package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
)

type adapterInfo struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Healthy     bool              `json:"healthy"`
	LastScanAt  string            `json:"last_scan_at"`
	LastError   string            `json:"last_error,omitempty"`
	TotalSeen   int               `json:"total_seen"`
	Watching    bool              `json:"watching"`
	WatchPath   string            `json:"watch_path"`
	Config      map[string]string `json:"config"`
}

func listAdaptersTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("list_adapters",
		mcp.WithDescription("List all configured Agent Memory Watcher adapters and their status."),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if a.MemoryWatcher == nil {
				return mcp.NewToolResultError("MemoryWatcher is not enabled. Set VATBRAIN_WATCHER_ENABLED=true"), nil
			}

			statuses := a.MemoryWatcher.Registry().Statuses()
			result := make([]adapterInfo, len(statuses))
			for i, s := range statuses {
				lastScan := ""
				if !s.LastScanAt.IsZero() {
					lastScan = s.LastScanAt.Format("2006-01-02T15:04:05Z")
				}
				result[i] = adapterInfo{
					Name:        s.Name,
					Description: s.Description,
					Healthy:     s.Healthy,
					LastScanAt:  lastScan,
					LastError:   s.LastError,
					TotalSeen:   s.TotalSeen,
					Watching:    s.Watching,
					WatchPath:   s.WatchPath,
					Config:      s.Config,
				}
			}

			resp, jErr := mcp.NewToolResultJSON(result)
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

// Ensure fmt is referenced.
var _ = fmt.Sprintf

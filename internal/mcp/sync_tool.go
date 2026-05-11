package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
)

type syncResultOutput struct {
	AdaptersScanned int    `json:"adapters_scanned"`
	TotalFound      int    `json:"total_found"`
	TotalWritten    int    `json:"total_written"`
	TotalSkipped    int    `json:"total_skipped"`
	TotalFailed     int    `json:"total_failed"`
	Message         string `json:"message"`
}

func syncMemoriesTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("sync_memories",
		mcp.WithDescription("Manually trigger a scan of all enabled agent memory adapters. "+
			"Returns counts of found, written, skipped, and failed memories."),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if a.MemoryWatcher == nil {
				return mcp.NewToolResultError("MemoryWatcher is not enabled. Set VATBRAIN_WATCHER_ENABLED=true"), nil
			}

			result := a.MemoryWatcher.ScanAll(ctx)

			msg := "sync complete"
			if result.TotalFailed > 0 {
				msg = "sync completed with failures"
			}

			out := syncResultOutput{
				AdaptersScanned: result.AdaptersScanned,
				TotalFound:      result.TotalFound,
				TotalWritten:    result.TotalWritten,
				TotalSkipped:    result.TotalSkipped,
				TotalFailed:     result.TotalFailed,
				Message:         msg,
			}

			resp, jErr := mcp.NewToolResultJSON(out)
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

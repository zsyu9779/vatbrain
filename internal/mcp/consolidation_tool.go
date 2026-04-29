package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/models"
)

func triggerConsolidationTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("trigger_consolidation",
		mcp.WithDescription("Trigger a sleep consolidation run. Scans recent episodic memories, "+
			"clusters them, extracts semantic rules, backtests, and persists passed rules."),
		mcp.WithNumber("hours_to_scan",
			mcp.Description("Look back this many hours for episodic memories (default 24)")),
		mcp.WithNumber("min_cluster_size",
			mcp.Description("Minimum cluster size to trigger rule extraction (default 3)")),
		mcp.WithNumber("accuracy_threshold",
			mcp.Description("Minimum backtest accuracy for rule persistence (default 0.7)")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if hrs := req.GetFloat("hours_to_scan", 0); hrs > 0 {
				a.Consolidation.HoursToScan = hrs
			}
			if minSz := req.GetFloat("min_cluster_size", 0); minSz > 0 {
				a.Consolidation.MinClusterSize = int(minSz)
			}
			if accThresh := req.GetFloat("accuracy_threshold", 0); accThresh > 0 {
				a.Consolidation.AccuracyThreshold = accThresh
			}

			runID := uuid.New()
			now := time.Now()

			initial := models.ConsolidationRunResult{
				RunID:     runID,
				StartedAt: now,
			}

			if err := a.Store.SaveConsolidationRun(ctx, &initial); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to save run state: %v", err)), nil
			}

			go func() {
				defer func() {
					if rec := recover(); rec != nil {
						_ = rec
					}
				}()

				result, rErr := a.Consolidation.Run(ctx, a.Store, a.Embedder)
				if rErr != nil {
					_ = rErr
				}

				if saveErr := a.Store.SaveConsolidationRun(ctx, &result); saveErr != nil {
					_ = saveErr
				}
			}()

			resp, jErr := mcp.NewToolResultJSON(consolidationOutput{
				RunID:   runID.String(),
				Status:  "started",
				Message: "consolidation run started",
			})
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

type consolidationOutput struct {
	RunID   string `json:"run_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

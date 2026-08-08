package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/provider"
)

// prepareEditContextTool is the v0.3 proactive risk injection entry point:
// given the files about to be edited, it returns relevant memories + top
// injectable pitfalls + a risk score + reason codes. The agent folds the
// output into its pre-edit context (e.g. the hermes prepare_edit_context
// tool or an injected block).
func prepareEditContextTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("prepare_edit_context",
		mcp.WithDescription("Prepare edit context: relevant memories + top Pitfall risks + risk score for files about to be modified."),
		mcp.WithString("files", mcp.Required(),
			mcp.Description("Comma-separated file paths being edited")),
		mcp.WithString("task_type",
			mcp.Description("Task type: debug, feature, refactor, or review"),
			mcp.Enum("debug", "feature", "refactor", "review")),
		mcp.WithString("language",
			mcp.Description("Programming language or framework context")),
		mcp.WithString("user_goal",
			mcp.Description("Optional user goal to anchor recall")),
		mcp.WithString("project_id",
			mcp.Description("Optional project filter (default: all projects)")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			filesStr, err := req.RequireString("files")
			if err != nil {
				return mcp.NewToolResultError("files is required"), nil
			}
			files := splitComma(filesStr)
			taskType := models.TaskType(req.GetString("task_type", "feature"))
			language := req.GetString("language", "")
			userGoal := req.GetString("user_goal", "")
			projectID := req.GetString("project_id", "")

			// Query = user goal + file names + entity refs from file paths.
			query := strings.TrimSpace(userGoal + " " + filesStr)

			deps := core.WriteDeps{
				Store:       a.Store,
				Gate:        a.SignificanceGate,
				PatternSep:  a.PatternSeparation,
				WeightDecay: a.WeightDecay,
				Embedder:    a.Embedder,
				WorkingMem:  a.WorkingMemory,
			}

			episodes, err := provider.RetrieveEpisodic(ctx, deps, projectID, query, 5)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("memory retrieval failed: %v", err)), nil
			}
			pitfalls, err := provider.RetrievePitfalls(ctx, deps, projectID, query, 3)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("pitfall retrieval failed: %v", err)), nil
			}

			risk := core.ComputeRisk(core.RiskRequest{
				Files:     files,
				ProjectID: projectID,
				Language:  language,
				TaskType:  taskType,
				UserGoal:  userGoal,
			}, pitfalls, episodes, time.Now().UTC())

			// Record the surfaced pitfalls as "shown" for the interference
			// rate metric (best-effort).
			for _, p := range risk.Pitfalls {
				_ = a.Store.AddPitfallCounters(ctx, p.ID, 1, 0)
			}

			type memoryOutput struct {
				MemoryID  string `json:"memory_id"`
				Summary   string `json:"summary"`
				ProjectID string `json:"project_id"`
				Language  string `json:"language"`
			}
			type riskOutput struct {
				PitfallID         string  `json:"pitfall_id"`
				EntityID          string  `json:"entity_id"`
				Signature         string  `json:"signature"`
				RootCauseCategory string  `json:"root_cause_category"`
				FixStrategy       string  `json:"fix_strategy"`
				Status            string  `json:"status"`
				Confidence        float64 `json:"confidence"`
				OccurrenceCount   int     `json:"occurrence_count"`
			}

			type output struct {
				RiskScore   float64        `json:"risk_score"`
				ReasonCodes []string       `json:"reason_codes"`
				Pitfalls    []riskOutput   `json:"pitfalls"`
				Memories    []memoryOutput `json:"memories"`
			}

			out := output{
				RiskScore:   risk.RiskScore,
				ReasonCodes: risk.ReasonCodes,
				Pitfalls:    make([]riskOutput, 0, len(risk.Pitfalls)),
				Memories:    make([]memoryOutput, 0, len(risk.Episodes)),
			}
			for _, p := range risk.Pitfalls {
				out.Pitfalls = append(out.Pitfalls, riskOutput{
					PitfallID:         p.ID.String(),
					EntityID:          p.EntityID,
					Signature:         p.Signature,
					RootCauseCategory: string(p.RootCauseCategory),
					FixStrategy:       p.FixStrategy,
					Status:            string(p.Status.Normalize()),
					Confidence:        float64(p.TrustLevel) / float64(models.TrustLevelMax),
					OccurrenceCount:   p.OccurrenceCount,
				})
			}
			for _, m := range risk.Episodes {
				out.Memories = append(out.Memories, memoryOutput{
					MemoryID:  m.ID.String(),
					Summary:   m.Summary,
					ProjectID: m.ProjectID,
					Language:  m.Language,
				})
			}

			resp, jErr := mcp.NewToolResultJSON(out)
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

// splitComma splits a comma-separated string, trimming whitespace and
// dropping empties.
func splitComma(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// conflictSummary is the compact projection used by list_rule_conflicts.
type conflictSummary struct {
	ConflictID  string `json:"conflict_id"`
	RuleAID     string `json:"rule_a_id"`
	RuleBID     string `json:"rule_b_id"`
	EntityGroup string `json:"entity_group"`
	Basis       string `json:"basis"`
	Status      string `json:"status"`
	Resolution  string `json:"resolution"`
	Reason      string `json:"reason"`
	CreatedAt   string `json:"created_at"`
	ResolvedAt  string `json:"resolved_at,omitempty"`
}

func toConflictSummary(c models.RuleConflict) conflictSummary {
	s := conflictSummary{
		ConflictID:  c.ID.String(),
		RuleAID:     c.RuleAID.String(),
		RuleBID:     c.RuleBID.String(),
		EntityGroup: c.EntityGroup,
		Basis:       string(c.Basis),
		Status:      string(c.Status),
		Resolution:  c.Resolution,
		Reason:      c.Reason,
		CreatedAt:   c.CreatedAt.Format("2006-01-02 15:04"),
	}
	if c.ResolvedAt != nil {
		s.ResolvedAt = c.ResolvedAt.Format("2006-01-02 15:04")
	}
	return s
}

// detectRuleConflictsTool runs conflict detection + trust-based auto-resolution
// for a project (ROADMAP v1.0 冲突协调引擎).
func detectRuleConflictsTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("detect_rule_conflicts",
		mcp.WithDescription("Scan a project's semantic rules for contradictions (opposite directives on the same subject). Unequal trust is auto-resolved (higher trust wins); equal trust is left pending for human adjudication."),
		mcp.WithString("project_id", mcp.Description("Project identifier to scan")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			projectID := req.GetString("project_id", "")
			res, err := core.RunRuleConflictDetection(ctx, a.Store, projectID)
			if err != nil {
				if errors.Is(err, core.ErrConflictStoreUnsupported) {
					return mcp.NewToolResultError("conflict governance not supported by the configured store backend"), nil
				}
				return mcp.NewToolResultError(fmt.Sprintf("conflict detection failed: %v", err)), nil
			}
			resp, jErr := mcp.NewToolResultJSON(map[string]any{
				"detected":      res.Detected,
				"auto_resolved": res.AutoResolved,
				"pending":       res.Pending,
			})
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

// listRuleConflictsTool lists conflict records with a status filter.
func listRuleConflictsTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("list_rule_conflicts",
		mcp.WithDescription("List rule-conflict records (v1.0 冲突协调). Filter by status to see what needs human adjudication."),
		mcp.WithString("status", mcp.Description("Filter by status"),
			mcp.Enum("pending", "auto_resolved", "manual_resolved", "dismissed")),
		mcp.WithNumber("top_k", mcp.Description("Maximum number of results (default 20)")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			status := req.GetString("status", "")
			topK := int(req.GetFloat("top_k", 20))
			if topK <= 0 {
				topK = 20
			}

			cs, ok := a.Store.(store.RuleConflictStore)
			if !ok {
				return mcp.NewToolResultError("conflict governance not supported by the configured store backend"), nil
			}
			conflicts, err := cs.ListRuleConflicts(ctx, status, topK)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("list conflicts failed: %v", err)), nil
			}

			out := make([]conflictSummary, 0, len(conflicts))
			for _, c := range conflicts {
				out = append(out, toConflictSummary(c))
			}
			resp, jErr := mcp.NewToolResultJSON(out)
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

// resolveRuleConflictTool is the human adjudication path: pick the winning
// rule of a pending conflict; the loser is retired.
func resolveRuleConflictTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("resolve_rule_conflict",
		mcp.WithDescription("Adjudicate a pending rule conflict: pick the winning rule; the other is retired (obsoleted)."),
		mcp.WithString("conflict_id", mcp.Required(), mcp.Description("Conflict record ID (uuid)")),
		mcp.WithString("winner_rule_id", mcp.Required(), mcp.Description("Rule ID that should win (one of the two conflicted rules)")),
		mcp.WithString("note", mcp.Description("Optional free-form rationale")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			conflictID, err := req.RequireString("conflict_id")
			if err != nil {
				return mcp.NewToolResultError("conflict_id is required"), nil
			}
			winnerStr, err := req.RequireString("winner_rule_id")
			if err != nil {
				return mcp.NewToolResultError("winner_rule_id is required"), nil
			}
			note := req.GetString("note", "")

			cid, err := uuid.Parse(conflictID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid conflict_id: %v", err)), nil
			}
			wid, err := uuid.Parse(winnerStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid winner_rule_id: %v", err)), nil
			}

			cs, ok := a.Store.(store.RuleConflictStore)
			if !ok {
				return mcp.NewToolResultError("conflict governance not supported by the configured store backend"), nil
			}
			conflicts, err := cs.ListRuleConflicts(ctx, "", 2000)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("fetch conflicts failed: %v", err)), nil
			}
			var conflict *models.RuleConflict
			for i := range conflicts {
				if conflicts[i].ID == cid {
					conflict = &conflicts[i]
					break
				}
			}
			if conflict == nil {
				return mcp.NewToolResultError(fmt.Sprintf("conflict %s not found", conflictID)), nil
			}

			if err := core.ResolveManually(ctx, a.Store, conflict, wid, note); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("resolve failed: %v", err)), nil
			}
			return mcp.NewToolResultStructured(map[string]string{
				"conflict_id": conflictID,
				"winner":      winnerStr,
				"status":      string(models.ConflictManualResolved),
			}, fmt.Sprintf("conflict %s resolved — winner %s, loser retired", conflictID, winnerStr)), nil
		},
	}
}

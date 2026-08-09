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
	"github.com/vatbrain/vatbrain/internal/store"
)

// pitfallSummary is the compact projection used by list_pitfalls and
// explain_pitfall.
type pitfallSummary struct {
	PitfallID        string  `json:"pitfall_id"`
	EntityID         string  `json:"entity_id"`
	EntityType       string  `json:"entity_type"`
	ProjectID        string  `json:"project_id"`
	Signature        string  `json:"signature"`
	RootCauseCategory string `json:"root_cause_category"`
	FixStrategy      string  `json:"fix_strategy"`
	Status           string  `json:"status"`
	WasUserCorrected bool    `json:"was_user_corrected"`
	OccurrenceCount  int     `json:"occurrence_count"`
	LastOccurredAt   string  `json:"last_occurred_at,omitempty"`
	Weight           float64 `json:"weight"`
	TrustLevel       int     `json:"trust_level"`
	Injectable       bool    `json:"injectable"`
	InterferenceRate float64 `json:"interference_rate"`
	TimesShown       int     `json:"times_shown"`
	TimesSuppressed  int     `json:"times_suppressed"`
}

func toPitfallSummary(p models.PitfallMemory) pitfallSummary {
	s := pitfallSummary{
		PitfallID:         p.ID.String(),
		EntityID:          p.EntityID,
		EntityType:        string(p.EntityType),
		ProjectID:         p.ProjectID,
		Signature:         p.Signature,
		RootCauseCategory: string(p.RootCauseCategory),
		FixStrategy:       p.FixStrategy,
		Status:            string(p.Status.Normalize()),
		WasUserCorrected:  p.WasUserCorrected,
		OccurrenceCount:   p.OccurrenceCount,
		Weight:            p.Weight,
		TrustLevel:        int(p.TrustLevel),
		Injectable:        p.Injectable(),
		InterferenceRate:  p.InterferenceRate(),
		TimesShown:        p.TimesShown,
		TimesSuppressed:   p.TimesSuppressed,
	}
	if p.LastOccurredAt != nil {
		s.LastOccurredAt = p.LastOccurredAt.Format("2006-01-02")
	}
	return s
}

// listPitfallsTool lists pitfalls with Workbench filters and interference
// metrics (v0.2.2).
func listPitfallsTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("list_pitfalls",
		mcp.WithDescription("List Pitfall memories (v0.2.2 Workbench) with status and interference-rate metrics."),
		mcp.WithString("project_id", mcp.Description("Filter by project identifier")),
		mcp.WithString("status", mcp.Description("Filter by state"),
			mcp.Enum("proposed", "confirmed", "suppressed", "obsolete")),
		mcp.WithString("root_cause_category",
			mcp.Description("Filter by root cause"),
			mcp.Enum("CONCURRENCY", "RESOURCE_EXHAUSTION", "CONFIG", "CONTRACT_VIOLATION", "LOGIC_ERROR", "UNKNOWN")),
		mcp.WithNumber("top_k", mcp.Description("Maximum number of results (default 20)")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			projectID := req.GetString("project_id", "")
			status := req.GetString("status", "")
			rootCause := req.GetString("root_cause_category", "")
			topK := int(req.GetFloat("top_k", 20))
			if topK <= 0 {
				topK = 20
			}

			pitfalls, err := a.Store.SearchPitfall(ctx, store.PitfallSearchRequest{
				ProjectID:         projectID,
				RootCauseCategory: models.RootCause(rootCause),
				Limit:             topK,
			})
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("list pitfalls failed: %v", err)), nil
			}

			out := make([]pitfallSummary, 0, len(pitfalls))
			for _, p := range pitfalls {
				if status != "" && string(p.Status.Normalize()) != status {
					continue
				}
				out = append(out, toPitfallSummary(p))
			}

			resp, jErr := mcp.NewToolResultJSON(out)
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

// explainPitfallTool explains one pitfall including source episodic IDs
// (traceable source — v0.2.2 准出).
func explainPitfallTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("explain_pitfall",
		mcp.WithDescription("Explain a single Pitfall: signature, root cause, fix strategy, status, source episodes."),
		mcp.WithString("pitfall_id", mcp.Required(), mcp.Description("Pitfall ID (uuid)")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			idStr, err := req.RequireString("pitfall_id")
			if err != nil {
				return mcp.NewToolResultError("pitfall_id is required"), nil
			}
			id, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid pitfall_id: %v", err)), nil
			}
			p, err := a.Store.GetPitfall(ctx, id)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("pitfall not found: %v", err)), nil
			}

			type explainOutput struct {
				pitfallSummary
				SourceEpisodicIDs []string `json:"source_episodic_ids"`
				CreatedAt         string   `json:"created_at"`
				UpdatedAt         string   `json:"updated_at"`
			}
			src := make([]string, len(p.SourceEpisodicIDs))
			for i, sid := range p.SourceEpisodicIDs {
				src[i] = sid.String()
			}
			out := explainOutput{
				pitfallSummary:    toPitfallSummary(*p),
				SourceEpisodicIDs: src,
				CreatedAt:         p.CreatedAt.Format(time.RFC3339),
				UpdatedAt:         p.UpdatedAt.Format(time.RFC3339),
			}
			resp, jErr := mcp.NewToolResultJSON(out)
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

// confirmPitfallTool promotes a pitfall to confirmed (approved for active
// injection).
func confirmPitfallTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("confirm_pitfall",
		mcp.WithDescription("Confirm a proposed Pitfall — promote to 'confirmed' so active risk injection may surface it."),
		mcp.WithString("pitfall_id", mcp.Required(), mcp.Description("Pitfall ID (uuid)")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			idStr, err := req.RequireString("pitfall_id")
			if err != nil {
				return mcp.NewToolResultError("pitfall_id is required"), nil
			}
			id, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid pitfall_id: %v", err)), nil
			}
			if err := a.Store.UpdatePitfallStatus(ctx, id, models.PitfallConfirmed); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("confirm failed: %v", err)), nil
			}
			return mcp.NewToolResultStructured(map[string]string{"status": "confirmed"},
				fmt.Sprintf("pitfall %s confirmed", idStr)), nil
		},
	}
}

// suppressPitfallTool suppresses a pitfall (the injection escape valve) and
// records the suppression for the interference-rate metric.
func suppressPitfallTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("suppress_pitfall",
		mcp.WithDescription("Suppress a Pitfall — it will no longer be actively injected. Records a suppression for the interference-rate metric."),
		mcp.WithString("pitfall_id", mcp.Required(), mcp.Description("Pitfall ID (uuid)")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			idStr, err := req.RequireString("pitfall_id")
			if err != nil {
				return mcp.NewToolResultError("pitfall_id is required"), nil
			}
			id, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid pitfall_id: %v", err)), nil
			}
			if err := a.Store.UpdatePitfallStatus(ctx, id, models.PitfallSuppressed); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("suppress failed: %v", err)), nil
			}
			// Best-effort interference counter bump + weight drop (逃生阀：
			// 抑制后不仅状态失效，注入权重也下降)。
			if cErr := a.Store.AddPitfallCounters(ctx, id, 0, 1); cErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("suppress failed to record counter: %v", cErr)), nil
			}
			_ = a.Store.ApplyPitfallFeedback(ctx, id, models.PitfallFeedbackIgnored, time.Now().UTC())
			return mcp.NewToolResultStructured(map[string]string{"status": "suppressed"},
				fmt.Sprintf("pitfall %s suppressed", idStr)), nil
		},
	}
}

// feedbackPitfallTool is the v0.3 feedback-loop entry point (Capture
// Feedback): the agent/user reports whether an injection was adopted,
// ignored, or the error recurred — driving weight up/down and the
// protection-level escalation.
func feedbackPitfallTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("feedback_pitfall",
		mcp.WithDescription("Feed back the outcome of a Pitfall injection (v0.3): adopted → weight up; ignored → weight down; recurred → protection level up."),
		mcp.WithString("pitfall_id", mcp.Required(), mcp.Description("Pitfall ID (uuid)")),
		mcp.WithString("action", mcp.Required(),
			mcp.Description("Feedback outcome"),
			mcp.Enum("adopted", "ignored", "recurred")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			idStr, err := req.RequireString("pitfall_id")
			if err != nil {
				return mcp.NewToolResultError("pitfall_id is required"), nil
			}
			actionStr, err := req.RequireString("action")
			if err != nil {
				return mcp.NewToolResultError("action is required"), nil
			}
			action := models.PitfallFeedbackAction(actionStr)
			if !action.IsValid() {
				return mcp.NewToolResultError(fmt.Sprintf("invalid action %q", actionStr)), nil
			}
			id, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid pitfall_id: %v", err)), nil
			}
			if err := a.Store.ApplyPitfallFeedback(ctx, id, action, time.Now().UTC()); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("feedback failed: %v", err)), nil
			}
			return mcp.NewToolResultStructured(map[string]string{
				"pitfall_id": idStr,
				"action":     actionStr,
			}, fmt.Sprintf("pitfall %s feedback: %s", idStr, actionStr)), nil
		},
	}
}

// linkPitfallEntityTool re-anchors a pitfall to a code entity.
func linkPitfallEntityTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("link_pitfall_entity",
		mcp.WithDescription("Re-anchor a Pitfall to a code entity (update entity_id / entity_type)."),
		mcp.WithString("pitfall_id", mcp.Required(), mcp.Description("Pitfall ID (uuid)")),
		mcp.WithString("entity_id", mcp.Required(), mcp.Description("Code entity anchor (e.g. clawfeed-push-v3.py or func:NewRedisPool)")),
		mcp.WithString("entity_type", mcp.Description("Entity type"),
			mcp.Enum("FUNCTION", "MODULE", "API", "CONFIG", "QUERY")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			idStr, err := req.RequireString("pitfall_id")
			if err != nil {
				return mcp.NewToolResultError("pitfall_id is required"), nil
			}
			entityID, err := req.RequireString("entity_id")
			if err != nil {
				return mcp.NewToolResultError("entity_id is required"), nil
			}
			entityType := models.EntityType(req.GetString("entity_type", ""))

			id, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid pitfall_id: %v", err)), nil
			}
			p, err := a.Store.GetPitfall(ctx, id)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("pitfall not found: %v", err)), nil
			}
			p.EntityID = entityID
			if entityType != "" {
				p.EntityType = entityType
			}
			p.UpdatedAt = time.Now().UTC()
			if err := a.Store.WritePitfall(ctx, p); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("link failed: %v", err)), nil
			}
			return mcp.NewToolResultStructured(map[string]string{"entity_id": entityID},
				fmt.Sprintf("pitfall %s now anchored to %s", idStr, entityID)), nil
		},
	}
}

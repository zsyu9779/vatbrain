package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
)

// prepareEditContextParams mirrors the MCP prepare_edit_context tool inputs —
// the hermes plugin routes its handle_tool_call here.
type prepareEditContextParams struct {
	SessionID string   `json:"session_id"`
	Files     []string `json:"files"`
	TaskType  string   `json:"task_type"`
	Language  string   `json:"language"`
	UserGoal  string   `json:"user_goal"`
	ProjectID string   `json:"project_id"`
}

// prepareEditContextResult is the risk-injection output (v0.3).
type prepareEditContextResult struct {
	RiskScore   float64        `json:"risk_score"`
	ReasonCodes []string       `json:"reason_codes"`
	Pitfalls    []pitfallOut   `json:"pitfalls"`
	Memories    []memoryOut    `json:"memories"`
}

type pitfallOut struct {
	PitfallID         string  `json:"pitfall_id"`
	EntityID          string  `json:"entity_id"`
	Signature         string  `json:"signature"`
	RootCauseCategory string  `json:"root_cause_category"`
	FixStrategy       string  `json:"fix_strategy"`
	Status            string  `json:"status"`
	Confidence        float64 `json:"confidence"`
	OccurrenceCount   int     `json:"occurrence_count"`
}

type memoryOut struct {
	MemoryID  string `json:"memory_id"`
	Summary   string `json:"summary"`
	ProjectID string `json:"project_id"`
	Language  string `json:"language"`
}

// handlePrepareEditContext computes relevant memories + injectable pitfalls
// + risk score for the files about to be edited (v0.3 proactive injection).
func (s *Server) handlePrepareEditContext(ctx context.Context, req rpcRequest) rpcResponse {
	var p prepareEditContextParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newErrorResponse(req.ID, -32602, fmt.Sprintf("invalid prepare_edit_context params: %v", err))
	}
	session, ok := s.sessionFor(p.SessionID)
	if !ok {
		return newErrorResponse(req.ID, -32002, "unknown session: call initialize first")
	}
	projectID := p.ProjectID
	if projectID == "" {
		projectID = session.projectID
	}

	query := strings.TrimSpace(p.UserGoal + " " + strings.Join(p.Files, " "))
	episodes, err := RetrieveEpisodic(ctx, s.deps, projectID, query, 5)
	if err != nil {
		return newErrorResponse(req.ID, -32000, fmt.Sprintf("memory retrieval failed: %v", err))
	}
	pitfalls, err := RetrievePitfalls(ctx, s.deps, projectID, query, 3)
	if err != nil {
		return newErrorResponse(req.ID, -32000, fmt.Sprintf("pitfall retrieval failed: %v", err))
	}

	risk := core.ComputeRisk(core.RiskRequest{
		Files:     p.Files,
		ProjectID: projectID,
		Language:  p.Language,
		TaskType:  models.TaskType(p.TaskType),
		UserGoal:  p.UserGoal,
	}, pitfalls, episodes, time.Now().UTC())

	for _, pf := range risk.Pitfalls {
		if cErr := s.deps.Store.AddPitfallCounters(ctx, pf.ID, 1, 0); cErr != nil {
			slog.Warn("provider: record pitfall shown counter", "err", cErr)
		}
	}

	out := prepareEditContextResult{
		RiskScore:   risk.RiskScore,
		ReasonCodes: risk.ReasonCodes,
	}
	for _, pf := range risk.Pitfalls {
		out.Pitfalls = append(out.Pitfalls, pitfallOut{
			PitfallID:         pf.ID.String(),
			EntityID:          pf.EntityID,
			Signature:         pf.Signature,
			RootCauseCategory: string(pf.RootCauseCategory),
			FixStrategy:       pf.FixStrategy,
			Status:            string(pf.Status.Normalize()),
			Confidence:        float64(pf.TrustLevel) / float64(models.TrustLevelMax),
			OccurrenceCount:   pf.OccurrenceCount,
		})
	}
	for _, m := range risk.Episodes {
		out.Memories = append(out.Memories, memoryOut{
			MemoryID:  m.ID.String(),
			Summary:   m.Summary,
			ProjectID: m.ProjectID,
			Language:  m.Language,
		})
	}

	return newResponse(req.ID, out)
}

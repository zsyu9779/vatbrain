package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vatbrain/vatbrain/internal/models"
)

// Additional hermes hook method names.
const (
	MethodMaintenance = "maintenance"
	MethodPreCompress = "pre_compress"
	MethodOnDelegation = "on_delegation"
)

// maintenanceParams carries the session for a periodic maintenance tick.
type maintenanceParams struct {
	SessionID string `json:"session_id"`
}

type maintenanceResult struct {
	OK bool `json:"ok"`
}

// handleMaintenance is the periodic lightweight maintenance tick hermes fires
// every N turns (on_turn_start). Phase 4 keeps it a bounded ack; weight
// recompute / cold-store migration can hook here later.
func (s *Server) handleMaintenance(req rpcRequest) rpcResponse {
	var p maintenanceParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newErrorResponse(req.ID, -32602, fmt.Sprintf("invalid maintenance params: %v", err))
	}
	if _, ok := s.sessionFor(p.SessionID); !ok {
		return newErrorResponse(req.ID, -32002, "unknown session: call initialize first")
	}
	slog.Debug("provider: maintenance tick", "session", p.SessionID)
	return newResponse(req.ID, maintenanceResult{OK: true})
}

// preCompressParams mirrors hermes on_pre_compress(messages) with a condensed
// view: the plugin sends recent message texts so the daemon can derive an
// insight that survives compression.
type preCompressParams struct {
	SessionID string   `json:"session_id"`
	Messages  []string `json:"messages"`
}

type preCompressResult struct {
	Insight string `json:"insight"`
}

// handlePreCompress derives a compression-worthy insight from the messages
// about to be discarded. Heuristic: the last non-trivial user message, marked
// so the compressor preserves it. A future LLM line can replace the heuristic.
func (s *Server) handlePreCompress(req rpcRequest) rpcResponse {
	var p preCompressParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newErrorResponse(req.ID, -32602, fmt.Sprintf("invalid pre_compress params: %v", err))
	}
	if _, ok := s.sessionFor(p.SessionID); !ok {
		return newErrorResponse(req.ID, -32002, "unknown session: call initialize first")
	}

	insight := ""
	for i := len(p.Messages) - 1; i >= 0; i-- {
		text := strings.TrimSpace(p.Messages[i])
		if text == "" {
			continue
		}
		// 挑最后一条非平凡用户消息作为压缩保真锚点。
		event := DeriveWriteEvent(text, "")
		if event.UserConfirmed || event.IsCorrection || len([]rune(text)) > 20 {
			insight = "[vatbrain] " + truncateRunesProvider(text, 300)
			break
		}
	}

	return newResponse(req.ID, preCompressResult{Insight: insight})
}

// onDelegationParams mirrors hermes on_delegation(task, result, child_session_id).
type onDelegationParams struct {
	SessionID       string `json:"session_id"`
	Task            string `json:"task"`
	Result          string `json:"result"`
	ChildSessionID  string `json:"child_session_id"`
}

type onDelegationResult struct {
	Persisted bool   `json:"persisted"`
	MemoryID  string `json:"memory_id,omitempty"`
}

// handleOnDelegation ingests a subagent delegation as an episodic observation
// (SourceType=DELEGATION) — the parent's memory provider sees what was
// delegated and what came back (HERMES_INTEGRATION §3 hook table).
func (s *Server) handleOnDelegation(ctx context.Context, req rpcRequest) rpcResponse {
	var p onDelegationParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newErrorResponse(req.ID, -32602, fmt.Sprintf("invalid on_delegation params: %v", err))
	}
	session, ok := s.sessionFor(p.SessionID)
	if !ok {
		return newErrorResponse(req.ID, -32002, "unknown session: call initialize first")
	}
	if strings.TrimSpace(p.Task) == "" {
		return newResponse(req.ID, onDelegationResult{Persisted: false})
	}

	id := uuid.New()
	now := time.Now().UTC()
	summary := truncateRunesProvider("委派："+p.Task+" → "+p.Result, 500)
	mem := &models.EpisodicMemory{
		ID:                 id,
		ProjectID:          session.projectID,
		TaskType:           models.TaskTypeFeature,
		Summary:            summary,
		SourceType:         models.SourceTypeDelegation,
		TrustLevel:         models.DefaultTrustLevel,
		Weight:             1.0,
		EffectiveFrequency: 1.0,
		CreatedAt:          now,
		FullSnapshotURI:    fmt.Sprintf("hermes://delegation/%s", p.ChildSessionID),
	}
	if emb, err := s.deps.Embedder.Embed(ctx, summary); err == nil {
		mem.ContextVector = emb
	}
	if err := s.deps.Store.WriteEpisodic(ctx, mem); err != nil {
		return newErrorResponse(req.ID, -32000, fmt.Sprintf("delegation write failed: %v", err))
	}
	return newResponse(req.ID, onDelegationResult{Persisted: true, MemoryID: id.String()})
}

// truncateRunesProvider bounds a string to max runes.
func truncateRunesProvider(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

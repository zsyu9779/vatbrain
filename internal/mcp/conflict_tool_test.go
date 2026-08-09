package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/app"
	vatmcp "github.com/vatbrain/vatbrain/internal/mcp"
	"github.com/vatbrain/vatbrain/internal/models"
)

func seedRule(t *testing.T, a *app.App, content string, trust models.TrustLevel) models.SemanticMemory {
	t.Helper()
	r := models.SemanticMemory{
		ID:          uuid.New(),
		Type:        models.MemoryTypeRule,
		Content:     content,
		SourceType:  models.SourceTypeINFERRED,
		TrustLevel:  trust,
		Weight:      1.0,
		EntityGroup: "proj",
		CreatedAt:   time.Now().UTC(),
	}
	require.NoError(t, a.Store.WriteSemantic(context.Background(), &r))
	return r
}

func TestDetectRuleConflicts_EndToEnd(t *testing.T) {
	a := minimalApp()

	high := seedRule(t, a, "Redis MaxOpenConns 应该设为 100", models.TrustLevelMax)
	low := seedRule(t, a, "Redis MaxOpenConns 不要设为 100", models.TrustLevelMin)
	equalA := seedRule(t, a, "日志级别 应该用 INFO", 3)
	seedRule(t, a, "日志级别 不要用 INFO", 3)

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	raw := callTool(t, srv, "detect_rule_conflicts", map[string]any{"project_id": "proj"})
	var res struct {
		Detected     int `json:"detected"`
		AutoResolved int `json:"auto_resolved"`
		Pending      int `json:"pending"`
	}
	require.NoError(t, json.Unmarshal(raw, &res))
	assert.Equal(t, 2, res.Detected)
	assert.Equal(t, 1, res.AutoResolved)
	assert.Equal(t, 1, res.Pending)

	// The low-trust rule is retired by the tool.
	got, err := a.Store.GetSemantic(context.Background(), low.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ObsoletedAt)
	// The high-trust winner survives.
	winner, err := a.Store.GetSemantic(context.Background(), high.ID)
	require.NoError(t, err)
	assert.Nil(t, winner.ObsoletedAt)

	// list_rule_conflicts shows one pending (equal trust) + one auto_resolved.
	rawList := callTool(t, srv, "list_rule_conflicts", map[string]any{"status": "pending"})
	var pending []map[string]any
	require.NoError(t, json.Unmarshal(rawList, &pending))
	require.Len(t, pending, 1)
	assert.Equal(t, equalA.ID.String(), pending[0]["rule_a_id"])
}

func TestResolveRuleConflict_Manual(t *testing.T) {
	a := minimalApp()
	ruleA := seedRule(t, a, "日志级别 应该用 INFO", 3)
	ruleB := seedRule(t, a, "日志级别 不要用 INFO", 3)

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	// Detect leaves the equal-trust pair pending.
	raw := callTool(t, srv, "detect_rule_conflicts", map[string]any{"project_id": "proj"})
	var det struct {
		Pending int `json:"pending"`
	}
	require.NoError(t, json.Unmarshal(raw, &det))
	assert.Equal(t, 1, det.Pending)

	// Find the pending conflict and adjudicate: ruleA wins.
	rawList := callTool(t, srv, "list_rule_conflicts", map[string]any{"status": "pending"})
	var conflicts []struct {
		ConflictID string `json:"conflict_id"`
	}
	require.NoError(t, json.Unmarshal(rawList, &conflicts))
	require.Len(t, conflicts, 1)

	callTool(t, srv, "resolve_rule_conflict", map[string]any{
		"conflict_id":    conflicts[0].ConflictID,
		"winner_rule_id": ruleA.ID.String(),
		"note":           "用户明确要求 INFO",
	})

	loser, err := a.Store.GetSemantic(context.Background(), ruleB.ID)
	require.NoError(t, err)
	require.NotNil(t, loser.ObsoletedAt, "losing rule must be retired by manual resolution")
	winner, err := a.Store.GetSemantic(context.Background(), ruleA.ID)
	require.NoError(t, err)
	assert.Nil(t, winner.ObsoletedAt)
}

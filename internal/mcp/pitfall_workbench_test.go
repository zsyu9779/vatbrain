package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/app"
	vatmcp "github.com/vatbrain/vatbrain/internal/mcp"
	"github.com/vatbrain/vatbrain/internal/models"
)

func seedPitfall(t *testing.T, a *app.App, status models.PitfallStatus) models.PitfallMemory {
	t.Helper()
	now := time.Now().UTC()
	p := models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "clawfeed-push-v3.py",
		EntityType:        models.EntityTypeModule,
		ProjectID:         "coder",
		Signature:         "ClawFeed 推送必须用 v3.py，旧脚本会以错误身份发送",
		RootCauseCategory: models.RootCauseConfig,
		FixStrategy:       "使用 clawfeed-push-v3.py --as bot",
		OccurrenceCount:   3,
		LastOccurredAt:    &now,
		TrustLevel:        models.DefaultTrustLevel,
		Weight:            1.0,
		CreatedAt:         now,
		UpdatedAt:         now,
		Status:            status,
	}
	require.NoError(t, a.Store.WritePitfall(context.Background(), &p))
	return p
}

func callTool(t *testing.T, srv *mcptest.Server, name string, args map[string]any) json.RawMessage {
	t.Helper()
	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	result, err := srv.Client().CallTool(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError, "tool %s returned an error", name)
	return json.RawMessage(result.Content[0].(mcp.TextContent).Text)
}

func TestListPitfalls_StatusAndInterference(t *testing.T) {
	a := minimalApp()
	seedPitfall(t, a, models.PitfallConfirmed)
	seedPitfall(t, a, models.PitfallProposed)

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	raw := callTool(t, srv, "list_pitfalls", map[string]any{"project_id": "coder"})
	var out []struct {
		Status           string  `json:"status"`
		Injectable       bool    `json:"injectable"`
		InterferenceRate float64 `json:"interference_rate"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Len(t, out, 2)
	for _, p := range out {
		assert.Contains(t, []string{"confirmed", "proposed"}, p.Status)
		assert.True(t, p.Injectable, "confirmed/proposed with count>=2 should be injectable")
		assert.Equal(t, 0.0, p.InterferenceRate)
	}
}

func TestConfirmPitfall_TransitionsStatus(t *testing.T) {
	a := minimalApp()
	p := seedPitfall(t, a, models.PitfallProposed)

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	callTool(t, srv, "confirm_pitfall", map[string]any{"pitfall_id": p.ID.String()})

	got, err := a.Store.GetPitfall(context.Background(), p.ID)
	require.NoError(t, err)
	assert.Equal(t, models.PitfallConfirmed, got.Status)
}

func TestSuppressPitfall_RecordsInterference(t *testing.T) {
	a := minimalApp()
	p := seedPitfall(t, a, models.PitfallConfirmed)

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	callTool(t, srv, "suppress_pitfall", map[string]any{"pitfall_id": p.ID.String()})

	got, err := a.Store.GetPitfall(context.Background(), p.ID)
	require.NoError(t, err)
	assert.Equal(t, models.PitfallSuppressed, got.Status)
	assert.Equal(t, 1, got.TimesSuppressed, "suppress 应计入干扰率计数")
	assert.False(t, got.Injectable(), "suppressed pitfall 不应再注入")
}

func TestLinkPitfallEntity_Reanchors(t *testing.T) {
	a := minimalApp()
	p := seedPitfall(t, a, models.PitfallProposed)

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	callTool(t, srv, "link_pitfall_entity", map[string]any{
		"pitfall_id": p.ID.String(),
		"entity_id":  "func:NewRedisPool",
		"entity_type": "FUNCTION",
	})

	got, err := a.Store.GetPitfall(context.Background(), p.ID)
	require.NoError(t, err)
	assert.Equal(t, "func:NewRedisPool", got.EntityID)
	assert.Equal(t, models.EntityTypeFunction, got.EntityType)
}

func TestExplainPitfall_TraceableSource(t *testing.T) {
	a := minimalApp()
	p := seedPitfall(t, a, models.PitfallConfirmed)

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	raw := callTool(t, srv, "explain_pitfall", map[string]any{"pitfall_id": p.ID.String()})
	var out struct {
		EntityID  string   `json:"entity_id"`
		Signature string   `json:"signature"`
		Fix       string   `json:"fix_strategy"`
		SourceEp  []string `json:"source_episodic_ids"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, "clawfeed-push-v3.py", out.EntityID)
	assert.Contains(t, out.Signature, "v3.py")
}

func TestPrepareEditContext_ReturnsRiskAndPitfalls(t *testing.T) {
	a := minimalApp()
	ctx := context.Background()

	// 注入一条已确认的中文 pitfall
	now := time.Now().UTC()
	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "clawfeed-push-v3.py",
		EntityType:        models.EntityTypeModule,
		ProjectID:         "coder",
		Signature:         "ClawFeed 推送必须用 v3.py，旧脚本以错误身份发送",
		RootCauseCategory: models.RootCauseConfig,
		FixStrategy:       "使用 clawfeed-push-v3.py --as bot",
		OccurrenceCount:   5,
		LastOccurredAt:    &now,
		TrustLevel:        models.TrustLevelMax,
		Weight:            1.0,
		CreatedAt:         now,
		UpdatedAt:         now,
		Status:            models.PitfallConfirmed,
	}
	require.NoError(t, a.Store.WritePitfall(ctx, p))

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	raw := callTool(t, srv, "prepare_edit_context", map[string]any{
		"files":      "scripts/clawfeed-push-v3.py, config/feedpush.conf",
		"task_type":  "refactor",
		"language":   "python",
		"user_goal":  "修复 ClawFeed 播报推送身份问题",
		"project_id": "coder",
	})
	var out struct {
		RiskScore   float64  `json:"risk_score"`
		ReasonCodes []string `json:"reason_codes"`
		Pitfalls    []struct {
			EntityID  string `json:"entity_id"`
			Signature string `json:"signature"`
			Status    string `json:"status"`
		} `json:"pitfalls"`
		Memories []struct {
			Summary string `json:"summary"`
		} `json:"memories"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.True(t, out.RiskScore > 0, "存在已确认 pitfall 时 risk_score 应 > 0")
	assert.Contains(t, out.ReasonCodes, "recent_error")
	require.NotEmpty(t, out.Pitfalls)
	assert.Equal(t, "clawfeed-push-v3.py", out.Pitfalls[0].EntityID)
	assert.Equal(t, "confirmed", out.Pitfalls[0].Status)

	// prepare_edit_context 应记录 TimesShown（干扰率分子）
	got, err := a.Store.GetPitfall(context.Background(), p.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, got.TimesShown)
}

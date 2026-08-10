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

// seedEpisodic stores a fact-style episodic memory directly (bypassing the
// write pipeline so the test controls occurred_at and weight exactly).
func seedEpisodic(t *testing.T, a *app.App, id uuid.UUID, projectID, summary string, occurred time.Time, weight float64) {
	t.Helper()
	require.NoError(t, a.Store.WriteEpisodic(context.Background(), &models.EpisodicMemory{
		ID:          id,
		ProjectID:   projectID,
		Summary:     summary,
		Weight:      weight,
		OccurredAt:  occurred,
		CreatedAt:   occurred,
	}))
}

func TestSignalUpdate_RetiresCoveredOld(t *testing.T) {
	a := minimalApp()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)
	oldID := uuid.New()
	newID := uuid.New()
	seedEpisodic(t, a, oldID, "proj", "The user prefers PostgreSQL", t1, 0.3)
	seedEpisodic(t, a, newID, "proj", "The user now prefers SQLite", t2, 0.5)

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	raw := callTool(t, srv, "signal_update", map[string]any{"memory_id": newID.String()})
	var res struct {
		Detected      int `json:"detected"`
		Applied       int `json:"applied"`
		CarrierWeight float64 `json:"carrier_weight"`
	}
	require.NoError(t, json.Unmarshal(raw, &res))
	assert.Equal(t, 1, res.Detected)
	assert.Equal(t, 1, res.Applied)
	assert.InDelta(t, 0.75, res.CarrierWeight, 0.0001, "0.5 x default boost 1.5")

	// The covered memory is retired; the supersession is traceable.
	old, err := a.Store.GetEpisodic(context.Background(), oldID)
	require.NoError(t, err)
	require.NotNil(t, old.ObsoletedAt)
	edges, err := a.Store.GetEdges(context.Background(), newID, "SUPERSEDED", "outgoing")
	require.NoError(t, err)
	require.Len(t, edges, 1)
	assert.Equal(t, oldID, edges[0].ToID)

	// Idempotent: re-signalling the same memory finds nothing to do.
	raw2 := callTool(t, srv, "signal_update", map[string]any{"memory_id": newID.String()})
	var res2 struct {
		Detected int `json:"detected"`
		Applied  int `json:"applied"`
	}
	require.NoError(t, json.Unmarshal(raw2, &res2))
	assert.Equal(t, 0, res2.Detected)
	assert.Equal(t, 0, res2.Applied)
}

func TestSignalUpdate_ChineseDirectiveReversal(t *testing.T) {
	// 中文用例（CONTRIBUTING.md 约定）:指令反转即使 bigram 高度重合也构成更新。
	a := minimalApp()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)
	oldID := uuid.New()
	newID := uuid.New()
	seedEpisodic(t, a, oldID, "proj", "Redis MaxOpenConns 不要设为 100", t1, 0.5)
	seedEpisodic(t, a, newID, "proj", "Redis MaxOpenConns 应该设为 100", t2, 0.4)

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	raw := callTool(t, srv, "signal_update", map[string]any{"memory_id": newID.String()})
	var res struct {
		Detected int `json:"detected"`
		Applied  int `json:"applied"`
		Pairs    []struct {
			Reason string `json:"reason"`
		} `json:"pairs"`
	}
	require.NoError(t, json.Unmarshal(raw, &res))
	assert.Equal(t, 1, res.Detected)
	assert.Equal(t, 1, res.Applied)
	require.Len(t, res.Pairs, 1)
	assert.Contains(t, res.Pairs[0].Reason, "polarity flip",
		"反转判定的理由必须说明极性翻转,动作才可解释")

	old, err := a.Store.GetEpisodic(context.Background(), oldID)
	require.NoError(t, err)
	require.NotNil(t, old.ObsoletedAt, "被反转的旧指令必须废弃")
}

func TestSignalUpdate_NoCoverage(t *testing.T) {
	a := minimalApp()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)
	otherID := uuid.New()
	newID := uuid.New()
	seedEpisodic(t, a, otherID, "proj", "Alice got a shell necklace in Hawaii", t1, 0.8)
	seedEpisodic(t, a, newID, "proj", "Carol plans to adopt a beagle puppy", t2, 0.8)

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	raw := callTool(t, srv, "signal_update", map[string]any{"memory_id": newID.String()})
	var res struct {
		Detected int `json:"detected"`
		Applied  int `json:"applied"`
	}
	require.NoError(t, json.Unmarshal(raw, &res))
	assert.Equal(t, 0, res.Detected)
	assert.Equal(t, 0, res.Applied)

	other, err := a.Store.GetEpisodic(context.Background(), otherID)
	require.NoError(t, err)
	assert.Nil(t, other.ObsoletedAt)
}

func TestSignalUpdate_MemoryNotFound(t *testing.T) {
	a := minimalApp()
	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	var req mcp.CallToolRequest
	req.Params.Name = "signal_update"
	req.Params.Arguments = map[string]any{"memory_id": uuid.New().String()}
	result, err := srv.Client().CallTool(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "a missing memory must surface as a tool error")
}

package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/models"
)

func TestServe_OnDelegation_PersistsEpisodic(t *testing.T) {
	deps, st := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-del", "agent_context": "primary", "agent_identity": "coder",
	}))

	resp := w.send(t, requestLine(t, 2, MethodOnDelegation, map[string]any{
		"session_id": "sess-del",
		"task":       "排查 ClawFeed 播报发送失败",
		"result":     "根因是用了旧脚本，已改用 v3.py",
		"child_session_id": "child-1",
	}))
	require.Equal(t, "", resp.errorMessage())
	var out onDelegationResult
	json.Unmarshal(resp.Result, &out)
	assert.True(t, out.Persisted)
	require.NotEmpty(t, out.MemoryID)

	ep, err := st.GetEpisodic(context.Background(), mustUUID(t, out.MemoryID))
	require.NoError(t, err)
	assert.Equal(t, models.SourceTypeDelegation, ep.SourceType)
	assert.Contains(t, ep.Summary, "委派")
	assert.Contains(t, ep.Summary, "ClawFeed")
	assert.Contains(t, ep.FullSnapshotURI, "child-1")

	w.send(t, requestLine(t, 3, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_PreCompress_ReturnsInsight(t *testing.T) {
	deps, _ := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-pc", "agent_context": "primary",
	}))

	resp := w.send(t, requestLine(t, 2, MethodPreCompress, map[string]any{
		"session_id": "sess-pc",
		"messages":   []string{"继续", "不对，字段名应该是 total_score 而非 overall_score", "好的"},
	}))
	require.Equal(t, "", resp.errorMessage())
	var out preCompressResult
	json.Unmarshal(resp.Result, &out)
	// 最后一条非平凡消息（纠错）作为压缩保真锚点
	assert.Contains(t, out.Insight, "total_score")
	assert.Contains(t, out.Insight, "[vatbrain]")

	// 无信号消息 → 空洞察
	resp = w.send(t, requestLine(t, 3, MethodPreCompress, map[string]any{
		"session_id": "sess-pc", "messages": []string{"继续", "好的", "嗯"},
	}))
	json.Unmarshal(resp.Result, &out)
	assert.Equal(t, "", out.Insight)

	w.send(t, requestLine(t, 4, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_Maintenance_Acks(t *testing.T) {
	deps, _ := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-mnt", "agent_context": "primary",
	}))
	resp := w.send(t, requestLine(t, 2, MethodMaintenance, map[string]any{
		"session_id": "sess-mnt",
	}))
	require.Equal(t, "", resp.errorMessage())

	w.send(t, requestLine(t, 3, MethodShutdown, map[string]any{}))
	<-done
}

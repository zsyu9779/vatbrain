package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
)

func TestServe_OnMemoryWrite_Add_UserExplicit(t *testing.T) {
	deps, st := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-mw", "agent_context": "primary", "agent_identity": "coder",
	}))

	resp := w.send(t, requestLine(t, 2, MethodOnMemoryWrite, map[string]any{
		"session_id": "sess-mw",
		"action":     "add",
		"target":     "memory",
		"content":    "软路由 OpenClash 覆写脚本用 Ruby YAML 解析，不要用文本 gsub",
		"metadata":   map[string]string{"write_origin": "assistant_tool"},
	}))
	require.Equal(t, "", resp.errorMessage())
	var out onMemoryWriteResult
	json.Unmarshal(resp.Result, &out)
	assert.True(t, out.Persisted)
	require.NotEmpty(t, out.MemoryID)

	// 图内 episodic：SourceType=USER（最高可信级），source=user_explicit 溯源
	ep, err := st.GetEpisodic(context.Background(), mustUUID(t, out.MemoryID))
	require.NoError(t, err)
	assert.Equal(t, models.SourceTypeUSER, ep.SourceType)
	assert.Equal(t, models.TrustLevelMax, ep.TrustLevel)
	assert.Contains(t, ep.FullSnapshotURI, "source=user_explicit")
	assert.Contains(t, ep.FullSnapshotURI, "origin=assistant_tool")
	assert.Contains(t, ep.Summary, "OpenClash")

	w.send(t, requestLine(t, 3, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_OnMemoryWrite_Replace_DerivesFromPrior(t *testing.T) {
	deps, st := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-rep", "agent_context": "primary", "agent_identity": "coder",
	}))

	// add 旧条目
	resp := w.send(t, requestLine(t, 2, MethodOnMemoryWrite, map[string]any{
		"session_id": "sess-rep", "action": "add", "target": "memory",
		"content": "旧规则：ClawFeed 用旧脚本推送",
	}))
	var first onMemoryWriteResult
	json.Unmarshal(resp.Result, &first)
	require.NotEmpty(t, first.MemoryID)

	// replace 成新条目 → DERIVED_FROM 边 + 旧条目 obsoleted
	resp = w.send(t, requestLine(t, 3, MethodOnMemoryWrite, map[string]any{
		"session_id": "sess-rep", "action": "replace", "target": "memory",
		"content": "新规则：ClawFeed 必须用 clawfeed-push-v3.py 推送",
	}))
	require.Equal(t, "", resp.errorMessage())
	var out onMemoryWriteResult
	json.Unmarshal(resp.Result, &out)
	assert.True(t, out.Persisted)
	assert.True(t, out.Obsoleted, "replace 应把旧条目标记 obsolete")
	assert.True(t, out.EdgeCreated, "replace 应创建 DERIVED_FROM 边")

	oldEp, err := st.GetEpisodic(context.Background(), mustUUID(t, first.MemoryID))
	require.NoError(t, err)
	assert.NotNil(t, oldEp.ObsoletedAt)

	edges, err := st.GetEdges(context.Background(), mustUUID(t, first.MemoryID), "DERIVED_FROM", "out")
	require.NoError(t, err)
	require.Len(t, edges, 1)
	assert.Equal(t, out.MemoryID, edges[0].ToID.String())

	w.send(t, requestLine(t, 4, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_OnMemoryWrite_Remove_ObsoletesPrior(t *testing.T) {
	deps, st := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-rm", "agent_context": "primary", "agent_identity": "coder",
	}))
	resp := w.send(t, requestLine(t, 2, MethodOnMemoryWrite, map[string]any{
		"session_id": "sess-rm", "action": "add", "target": "user",
		"content": "用户身份：中文语境开发者",
	}))
	var added onMemoryWriteResult
	json.Unmarshal(resp.Result, &added)

	resp = w.send(t, requestLine(t, 3, MethodOnMemoryWrite, map[string]any{
		"session_id": "sess-rm", "action": "remove", "target": "user",
		"content": "用户身份：中文语境开发者",
	}))
	var out onMemoryWriteResult
	json.Unmarshal(resp.Result, &out)
	assert.True(t, out.Obsoleted)

	ep, err := st.GetEpisodic(context.Background(), mustUUID(t, added.MemoryID))
	require.NoError(t, err)
	assert.NotNil(t, ep.ObsoletedAt)

	w.send(t, requestLine(t, 4, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_OnSessionSwitch_Reset_ClearsWorkingMemory(t *testing.T) {
	deps, _ := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-sw", "agent_context": "primary", "agent_identity": "coder",
	}))
	// 写一条用户确认记忆 → working memory 缓冲累计
	w.send(t, requestLine(t, 2, MethodSyncTurn, map[string]any{
		"session_id": "sess-sw", "user_content": "记住：A 项目的排障结论",
	}))
	require.NotEmpty(t, deps.WorkingMem.GetAll("coder"))

	// /new → reset=true 重绑新 session 并清空缓冲
	resp := w.send(t, requestLine(t, 3, MethodOnSessionSwitch, map[string]any{
		"session_id": "sess-sw", "new_session_id": "sess-sw-2", "reset": true,
	}))
	require.Equal(t, "", resp.errorMessage())
	assert.Empty(t, deps.WorkingMem.GetAll("coder"))

	// 新 session 可继续写入
	resp = w.send(t, requestLine(t, 4, MethodSyncTurn, map[string]any{
		"session_id": "sess-sw-2", "user_content": "记住：B 项目的新结论",
	}))
	require.Equal(t, "", resp.errorMessage())
	require.NotEmpty(t, deps.WorkingMem.GetAll("coder"))

	w.send(t, requestLine(t, 5, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_OnSessionEnd_StartsIntegration(t *testing.T) {
	deps, _ := testDeps(t)
	s := NewServer(deps)
	s.Consolidation = core.DefaultConsolidationEngine()

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-end", "agent_context": "primary", "agent_identity": "coder",
	}))
	resp := w.send(t, requestLine(t, 2, MethodOnSessionEnd, map[string]any{
		"session_id": "sess-end",
	}))
	require.Equal(t, "", resp.errorMessage())
	var out onSessionEndResult
	json.Unmarshal(resp.Result, &out)
	assert.True(t, out.Started)

	w.send(t, requestLine(t, 3, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_OnSessionSwitch_Rewound_InvalidatesPrefetch(t *testing.T) {
	deps, _ := testDeps(t)
	s := NewServer(deps)

	// 直接塞一条缓存，验证 rewound 后失效
	s.prefetchMu.Lock()
	s.prefetchCache["sess-rew"] = "cached context"
	s.prefetchMu.Unlock()

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-rew", "agent_context": "primary",
	}))
	resp := w.send(t, requestLine(t, 2, MethodOnSessionSwitch, map[string]any{
		"session_id": "sess-rew", "new_session_id": "sess-rew-2", "rewound": true,
	}))
	require.Equal(t, "", resp.errorMessage())

	s.prefetchMu.Lock()
	_, ok := s.prefetchCache["sess-rew"]
	s.prefetchMu.Unlock()
	assert.False(t, ok, "rewound 后 prefetch 缓存应失效")

	w.send(t, requestLine(t, 3, MethodShutdown, map[string]any{}))
	<-done
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	u, err := uuid.Parse(s)
	require.NoError(t, err)
	return u
}

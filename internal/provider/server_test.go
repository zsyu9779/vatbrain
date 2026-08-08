package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

// testDeps builds the shared write-pipeline dependencies over an in-memory
// store, mirroring what cmd/vatbrain-provider wires from app.New.
func testDeps(t *testing.T) (core.WriteDeps, *memory.Store) {
	t.Helper()
	st := memory.NewStore()
	gate := core.DefaultSignificanceGate()
	gate.Embedder = embedder.NewStubEmbedder()
	return core.WriteDeps{
		Store:       st,
		Gate:        gate,
		PatternSep:  core.DefaultPatternSeparation(),
		WeightDecay: core.DefaultWeightDecayEngine(),
		Embedder:    embedder.NewStubEmbedder(),
		WorkingMem:  store.NewWorkingMemoryBuffer(20),
	}, st
}

// TestServe_CorrectionTurn_PersistsIsCorrection is the Phase 2 acceptance:
// a user correction ingested via the stdio JSON-RPC protocol lands in the
// graph as an IsCorrection=true episodic.
func TestServe_CorrectionTurn_PersistsIsCorrection(t *testing.T) {
	deps, st := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	// initialize (bind session → project "coder")
	init := requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id":     "sess-001",
		"hermes_home":    "/home/user/.hermes",
		"platform":       "cli",
		"agent_context":  "primary",
		"agent_identity": "coder",
	})
	resp := w.send(t, init)
	require.Equal(t, "", resp.errorMessage())
	var initOut initializeResult
	json.Unmarshal(resp.Result, &initOut)
	assert.Equal(t, "vatbrain", initOut.Provider)
	assert.Equal(t, "coder", initOut.ProjectID)
	assert.False(t, initOut.ReadOnlyMode)

	// sync_turn with a Chinese correction → persisted as correction
	sync := requestLine(t, 2, MethodSyncTurn, map[string]any{
		"session_id":        "sess-001",
		"user_content":      "不对，evaluator 输出字段是 total_score 不是 overall_score",
		"assistant_content": "好的，我记错了字段名",
	})
	syncResp := w.send(t, sync)
	require.Equal(t, "", syncResp.errorMessage())
	var out syncTurnResult
	json.Unmarshal(syncResp.Result, &out)
	assert.True(t, out.Persisted, "correction must pass the gate (prediction_error)")
	assert.True(t, out.IsCorrection)
	assert.Equal(t, "prediction_error", out.GateReason)

	// Verify the episodic in the store carries IsCorrection=true.
	items, err := st.ScanRecent(context.Background(), time.Time{}, 100)
	require.NoError(t, err)
	require.Len(t, items, 1)
	ep, err := st.GetEpisodic(context.Background(), items[0].ID)
	require.NoError(t, err)
	assert.True(t, ep.IsCorrection)
	assert.Contains(t, ep.Summary, "total_score")
	assert.Equal(t, "coder", ep.ProjectID)

	// shutdown
	w.send(t, requestLine(t, 3, MethodShutdown, map[string]any{}))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after shutdown")
	}
}

func TestServe_BelowThresholdTurn_NotPersisted(t *testing.T) {
	deps, st := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-002", "agent_context": "primary",
	}))
	resp := w.send(t, requestLine(t, 2, MethodSyncTurn, map[string]any{
		"session_id":   "sess-002",
		"user_content": "继续排查一下刚才的问题",
	}))
	require.Equal(t, "", resp.errorMessage())
	var out syncTurnResult
	json.Unmarshal(resp.Result, &out)
	assert.False(t, out.Persisted)

	items, err := st.ScanRecent(context.Background(), time.Time{}, 100)
	require.NoError(t, err)
	assert.Empty(t, items)

	w.send(t, requestLine(t, 3, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_UnknownSession_Error(t *testing.T) {
	deps, _ := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	resp := w.send(t, requestLine(t, 1, MethodSyncTurn, map[string]any{
		"session_id": "no-such-session", "user_content": "记住：不要用旧脚本",
	}))
	require.NotEmpty(t, resp.errorMessage())

	w.send(t, requestLine(t, 2, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_NonPrimaryContext_ReadOnly(t *testing.T) {
	deps, st := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	// subagent context must not write (hermes contract)
	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-sub", "agent_context": "subagent",
	}))
	resp := w.send(t, requestLine(t, 2, MethodSyncTurn, map[string]any{
		"session_id": "sess-sub", "user_content": "记住：子代理上下文不写入",
	}))
	var out syncTurnResult
	json.Unmarshal(resp.Result, &out)
	assert.False(t, out.Persisted)
	assert.Equal(t, "read_only_context", out.GateReason)

	items, err := st.ScanRecent(context.Background(), time.Time{}, 100)
	require.NoError(t, err)
	assert.Empty(t, items)

	w.send(t, requestLine(t, 3, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_MethodNotFound(t *testing.T) {
	deps, _ := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	resp := w.send(t, requestLine(t, 1, "frobnicate", map[string]any{}))
	require.NotEmpty(t, resp.errorMessage())

	w.send(t, requestLine(t, 2, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_Prefetch_ReturnsRelevantContext(t *testing.T) {
	deps, _ := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-pref", "agent_context": "primary", "agent_identity": "coder",
	}))
	// 持久化一条中文记忆（显式指令 → user_confirmed）
	w.send(t, requestLine(t, 2, MethodSyncTurn, map[string]any{
		"session_id": "sess-pref",
		"user_content": "记住：软路由 OpenClash 覆写脚本用 Ruby YAML 解析，不要用文本 gsub",
	}))

	// 冷 prefetch：查询与记忆相关 → 返回记忆上下文
	resp := w.send(t, requestLine(t, 3, MethodPrefetch, map[string]any{
		"session_id": "sess-pref", "query": "OpenClash 覆写脚本 Ruby YAML 怎么解析",
	}))
	require.Equal(t, "", resp.errorMessage())
	var out prefetchResult
	json.Unmarshal(resp.Result, &out)
	assert.Contains(t, out.Context, "[vatbrain memory context]")
	assert.Contains(t, out.Context, "OpenClash")

	// 无关查询 → 空上下文
	resp = w.send(t, requestLine(t, 4, MethodPrefetch, map[string]any{
		"session_id": "sess-pref", "query": "量子计算 引力波 黑洞 弦论",
	}))
	json.Unmarshal(resp.Result, &out)
	assert.Equal(t, "", out.Context)

	w.send(t, requestLine(t, 5, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_QueuePrefetch_WarmsCache(t *testing.T) {
	deps, _ := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-q", "agent_context": "primary", "agent_identity": "coder",
	}))
	w.send(t, requestLine(t, 2, MethodSyncTurn, map[string]any{
		"session_id": "sess-q",
		"user_content": "记住：MiniMax max_tokens 必须设到 8000 才能同时输出 thinking 和 text",
	}))

	// queue_prefetch 预热（后台），随后 prefetch 命中缓存
	resp := w.send(t, requestLine(t, 3, MethodQueuePrefetch, map[string]any{
		"session_id": "sess-q", "query": "MiniMax max_tokens thinking text",
	}))
	require.Equal(t, "", resp.errorMessage())

	// 后台预热有竞态 → 轮询 prefetch 直到命中或超时
	var out prefetchResult
	found := false
	for i := 0; i < 20; i++ {
		resp := w.send(t, requestLine(t, 4, MethodPrefetch, map[string]any{
			"session_id": "sess-q", "query": "MiniMax max_tokens thinking text",
		}))
		json.Unmarshal(resp.Result, &out)
		if strings.Contains(out.Context, "MiniMax") {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.True(t, found, "queue_prefetch 预热后 prefetch 应能读到缓存")

	w.send(t, requestLine(t, 5, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_Prefetch_UnknownSession(t *testing.T) {
	deps, _ := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	resp := w.send(t, requestLine(t, 1, MethodPrefetch, map[string]any{
		"session_id": "nope", "query": "anything",
	}))
	require.NotEmpty(t, resp.errorMessage())

	w.send(t, requestLine(t, 2, MethodShutdown, map[string]any{}))
	<-done
}

func TestServe_Ping(t *testing.T) {
	deps, _ := testDeps(t)
	s := NewServer(deps)

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	resp := w.send(t, requestLine(t, 1, MethodPing, map[string]any{}))
	require.Equal(t, "", resp.errorMessage())
	var pong map[string]bool
	json.Unmarshal(resp.Result, &pong)
	assert.True(t, pong["pong"])

	w.send(t, requestLine(t, 2, MethodShutdown, map[string]any{}))
	<-done
}

// ---------------------------------------------------------------------------
// Protocol plumbing helpers
// ---------------------------------------------------------------------------

// pipeLines pairs the io.Reader fed to Serve with a wire the test uses to
// send newline-delimited JSON-RPC requests and read responses. Two io.Pipes
// give a fully synchronous request→response flow (the request write blocks
// until Serve reads it, and the response read blocks until Serve writes it).
func pipeLines(t *testing.T) (io.Reader, *rpcTestWire) {
	t.Helper()
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	wire := &rpcTestWire{
		reqW:  reqW,
		respW: respW,
		sc:    bufio.NewScanner(respR),
		mu:    &sync.Mutex{},
	}
	return reqR, wire
}

func requestLine(t *testing.T, id int, method string, params map[string]any) string {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	b, err := json.Marshal(req)
	require.NoError(t, err)
	return string(b) + "\n"
}

// rpcTestWire sends one request line and reads the corresponding response.
// It also implements io.Writer so Serve writes responses through it.
type rpcTestWire struct {
	reqW  *io.PipeWriter
	respW *io.PipeWriter
	sc    *bufio.Scanner
	mu    *sync.Mutex
}

// Write implements io.Writer, forwarding to the response pipe consumed by
// the scanner.
func (w *rpcTestWire) Write(p []byte) (int, error) {
	return w.respW.Write(p)
}

func (w *rpcTestWire) send(t *testing.T, line string) rpcResp {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.reqW.Write([]byte(line)); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if !w.sc.Scan() {
		t.Fatalf("read response: %v", w.sc.Err())
	}
	var raw struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(w.sc.Bytes(), &raw); err != nil {
		t.Fatalf("parse response %q: %v", w.sc.Bytes(), err)
	}
	return rpcResp{ID: raw.ID, Result: raw.Result, Error: raw.Error}
}

// rpcResp is a parsed JSON-RPC response for assertions.
type rpcResp struct {
	ID     int
	Result json.RawMessage
	Error  *rpcError
}

func (r rpcResp) errorMessage() string {
	if r.Error == nil {
		return ""
	}
	return r.Error.Message
}

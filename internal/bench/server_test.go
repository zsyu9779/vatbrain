package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
	"github.com/vatbrain/vatbrain/internal/store/sqlite"
)

// newTestServer builds a bench server over the in-memory store + keyword
// embedder (deterministic, non-zero vectors, no external API) and an httptest
// client.
func newTestServer(t *testing.T, gate GateMode, opts ...Options) *httptest.Server {
	t.Helper()
	st := memory.NewStore()
	deps := core.WriteDeps{
		Store:       st,
		Gate:        core.DefaultSignificanceGate(),
		PatternSep:  core.DefaultPatternSeparation(),
		WeightDecay: core.DefaultWeightDecayEngine(),
		Embedder:    embedder.NewKeywordEmbedder(models.DefaultEmbeddingDim),
		WorkingMem:  store.NewWorkingMemoryBuffer(20),
	}
	o := Options{GateMode: gate}
	if len(opts) > 0 {
		o = opts[0]
	}
	srv, err := NewServer(deps, o)
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts
}

func postJSONAuth(t *testing.T, url, body, token string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("POST", url, bytes.NewBufferString(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

func postJSON(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

func TestBench_AddSearchRoundTrip(t *testing.T) {
	ts := newTestServer(t, GateModeOff)

	// Seed 6 messages so top_k=5 < candidate count — the ranking assertion
	// below would fail if the embedder ranked the Hawaii memory below a
	// distractor. Distractors share no character bigrams with the query.
	code, out := postJSON(t, ts.URL+"/v1/add", `{
		"user_id": "u1",
		"messages": [
			{"role": "user", "content": "Alice loves hiking in the mountains on weekends"},
			{"role": "user", "content": "Bob is learning to bake sourdough bread at home"},
			{"role": "user", "content": "Carol plans to adopt a beagle puppy next spring"},
			{"role": "user", "content": "Dan finished a ten kilometer run in the rain"},
			{"role": "user", "content": "Erin reads science fiction novels before sleeping"},
			{"role": "user", "content": "Alice got a shell necklace on a trip to Hawaii"}
		]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(6), out["persisted"])
	assert.Equal(t, float64(0), out["skipped"])

	// Semantic search must rank the Hawaii memory first for a Hawaii query.
	code, out = postJSON(t, ts.URL+"/v1/search", `{"user_id":"u1","query":"Hawaii necklace trip","top_k":5}`)
	require.Equal(t, http.StatusOK, code)

	results, ok := out["results"].([]any)
	require.True(t, ok, "expected results array, got %#v", out["results"])
	require.NotEmpty(t, results, "expected at least one recalled memory")

	contents := make([]string, 0, len(results))
	for _, r := range results {
		rm, ok := r.(map[string]any)
		require.True(t, ok)
		contents = append(contents, rm["content"].(string))
	}
	assert.Contains(t, contents[0], "Hawaii", "top result should be the Hawaii memory, got %q", contents[0])
	// A clearly unrelated distractor must not be in the top-k.
	for _, c := range contents {
		assert.NotContains(t, c, "sourdough", "distractor leaked into top-k")
	}
}

func TestBench_TemporalPrefixInMemory(t *testing.T) {
	ts := newTestServer(t, GateModeOff)

	// A message stamped with chat_time gets a "[YYYY-MM-DD] " date prefix on
	// its stored summary, so retrieved context carries the event date.
	code, out := postJSON(t, ts.URL+"/v1/add", `{
		"user_id": "u1",
		"messages": [
			{"role": "user", "content": "Alice got a shell necklace on a trip to Hawaii", "chat_time": "2029-05-04T10:00:00Z"}
		]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(1), out["persisted"])

	code, out = postJSON(t, ts.URL+"/v1/search", `{"user_id":"u1","query":"Hawaii necklace trip","top_k":5}`)
	require.Equal(t, http.StatusOK, code)
	results, ok := out["results"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, results)
	withDate := results[0].(map[string]any)["content"].(string)
	assert.Contains(t, withDate, "[2029-05-04]", "retrieved memory should carry the message date")
	assert.Contains(t, withDate, "Alice got a shell necklace on a trip to Hawaii")

	// A message without chat_time must keep its summary unchanged: no "["
	// prefix, content exactly as stored.
	code, out = postJSON(t, ts.URL+"/v1/add", `{
		"user_id": "u2",
		"messages": [
			{"role": "user", "content": "Bob is learning to bake sourdough bread at home"}
		]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(1), out["persisted"])

	code, out = postJSON(t, ts.URL+"/v1/search", `{"user_id":"u2","query":"sourdough bread","top_k":5}`)
	require.Equal(t, http.StatusOK, code)
	results, ok = out["results"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, results)
	noDate := results[0].(map[string]any)["content"].(string)
	assert.Equal(t, "Bob is learning to bake sourdough bread at home", noDate)
	assert.NotContains(t, noDate, "[", "message without chat_time must not get a date prefix")

	// The date prefix must apply in gate "on" mode too: a correction that
	// persists still carries the event date on its summary.
	tsOn := newTestServer(t, GateModeOn)
	code, out = postJSON(t, tsOn.URL+"/v1/add", `{
		"user_id": "u1",
		"messages": [
			{"role": "user", "content": "actually it should be blue not red", "chat_time": "2029-05-04T10:00:00Z"}
		]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(1), out["persisted"])

	code, out = postJSON(t, tsOn.URL+"/v1/search", `{"user_id":"u1","query":"blue red","top_k":5}`)
	require.Equal(t, http.StatusOK, code)
	results, ok = out["results"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, results)
	assert.Contains(t, results[0].(map[string]any)["content"].(string), "[2029-05-04]")
}

func TestBench_Add_EmptyOrMissingContentSkipped(t *testing.T) {
	ts := newTestServer(t, GateModeOff)

	code, out := postJSON(t, ts.URL+"/v1/add", `{
		"user_id": "u1",
		"messages": [
			{"role": "user", "content": "  "},
			{"role": "user", "content": "a real memory"}
		]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(1), out["persisted"])
	assert.Equal(t, float64(1), out["skipped"])

	reasons, ok := out["gate_reason_counts"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(1), reasons["empty_message"])
}

func TestBench_GateOn_FiltersChitChatButKeepsCorrections(t *testing.T) {
	ts := newTestServer(t, GateModeOn)

	// Plain chit-chat is gated out in production-faithful mode.
	code, out := postJSON(t, ts.URL+"/v1/add", `{
		"user_id": "u1",
		"messages": [{"role": "user", "content": "Alice loves hiking in the mountains"}]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(0), out["persisted"])
	assert.Equal(t, float64(1), out["skipped"])

	// A correction ("actually it should be ...") is a prediction-error signal
	// and persists even with the gate on.
	code, out = postJSON(t, ts.URL+"/v1/add", `{
		"user_id": "u1",
		"messages": [{"role": "user", "content": "actually it should be blue not red"}]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(1), out["persisted"])
}

func TestBench_Delete_IsolatesUser(t *testing.T) {
	ts := newTestServer(t, GateModeOff)

	code, out := postJSON(t, ts.URL+"/v1/add", `{
		"user_id": "u1",
		"messages": [{"role": "user", "content": "one"}, {"role": "user", "content": "two"}]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(2), out["persisted"])

	code, out = postJSON(t, ts.URL+"/v1/delete", `{"user_id":"u1"}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(2), out["deleted"])

	// Search after delete returns nothing.
	code, out = postJSON(t, ts.URL+"/v1/search", `{"user_id":"u1","query":"one","top_k":5}`)
	require.Equal(t, http.StatusOK, code)
	results, ok := out["results"].([]any)
	require.True(t, ok)
	assert.Empty(t, results)
}

func TestBench_ValidationErrors(t *testing.T) {
	ts := newTestServer(t, GateModeOff)

	code, _ := postJSON(t, ts.URL+"/v1/add", `{"messages": []}`)
	assert.Equal(t, http.StatusBadRequest, code)

	code, _ = postJSON(t, ts.URL+"/v1/search", `{"user_id":"u1","query":""}`)
	assert.Equal(t, http.StatusBadRequest, code)

	code, _ = postJSON(t, ts.URL+"/v1/delete", `{"user_id":""}`)
	assert.Equal(t, http.StatusBadRequest, code)

	code, _ = postJSON(t, ts.URL+"/v1/add", `not-json{`)
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestBench_Health(t *testing.T) {
	ts := newTestServer(t, GateModeOff)

	resp, err := http.Get(ts.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, true, out["ok"])
}

func TestBench_AuthTokenEnforced(t *testing.T) {
	ts := newTestServer(t, GateModeOff, Options{Token: "s3cret"})

	// No token -> 401.
	code, _ := postJSON(t, ts.URL+"/v1/search", `{"user_id":"u1","query":"q","top_k":5}`)
	assert.Equal(t, http.StatusUnauthorized, code)

	// Wrong token -> 401.
	code, _ = postJSONAuth(t, ts.URL+"/v1/search", `{"user_id":"u1","query":"q","top_k":5}`, "wrong")
	assert.Equal(t, http.StatusUnauthorized, code)

	// Correct token -> 200.
	code, _ = postJSONAuth(t, ts.URL+"/v1/search", `{"user_id":"u1","query":"q","top_k":5}`, "s3cret")
	assert.Equal(t, http.StatusOK, code)

	// /health stays open for liveness probes.
	resp, err := http.Get(ts.URL + "/health")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestBench_InvalidGateModeRejected(t *testing.T) {
	st := memory.NewStore()
	deps := core.WriteDeps{
		Store:       st,
		Gate:        core.DefaultSignificanceGate(),
		PatternSep:  core.DefaultPatternSeparation(),
		WeightDecay: core.DefaultWeightDecayEngine(),
		Embedder:    embedder.NewKeywordEmbedder(models.DefaultEmbeddingDim),
	}
	_, err := NewServer(deps, Options{GateMode: "OFF"})
	require.Error(t, err)
	_, err = NewServer(deps, Options{GateMode: GateModeOn})
	require.NoError(t, err)
}

func TestBench_DecodeRejectsTrailingData(t *testing.T) {
	ts := newTestServer(t, GateModeOff)

	// Two JSON docs / trailing garbage must be rejected, not silently ignored.
	code, _ := postJSON(t, ts.URL+"/v1/add", `{"user_id":"u1","messages":[{"content":"a"}]} {"user_id":"u2","messages":[{"content":"b"}]}`)
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestBench_AddRejectsOversizedBatch(t *testing.T) {
	ts := newTestServer(t, GateModeOff, Options{MaxMessagesPerAdd: 3})

	code, _ := postJSON(t, ts.URL+"/v1/add", `{
		"user_id":"u1",
		"messages":[{"content":"a"},{"content":"b"},{"content":"c"},{"content":"d"}]
	}`)
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestBench_SQLiteBackend_EndToEnd(t *testing.T) {
	// Drive add -> search -> delete through the HTTP handlers over the real
	// sqlite backend (the production store the cmd entrypoint uses).
	st, err := sqlite.NewStore(config.SQLiteConfig{Path: filepath.Join(t.TempDir(), "bench.db"), WAL: true})
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	deps := core.WriteDeps{
		Store:       st,
		Gate:        core.DefaultSignificanceGate(),
		PatternSep:  core.DefaultPatternSeparation(),
		WeightDecay: core.DefaultWeightDecayEngine(),
		Embedder:    embedder.NewKeywordEmbedder(models.DefaultEmbeddingDim),
		WorkingMem:  store.NewWorkingMemoryBuffer(20),
	}
	srv, err := NewServer(deps, Options{GateMode: GateModeOff})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	code, out := postJSON(t, ts.URL+"/v1/add",
		`{"user_id":"u1","messages":[{"content":"Alice got a shell necklace in Hawaii"}]}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(1), out["persisted"])

	code, out = postJSON(t, ts.URL+"/v1/search",
		`{"user_id":"u1","query":"Hawaii necklace","top_k":5}`)
	require.Equal(t, http.StatusOK, code)
	results, ok := out["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].(map[string]any)["content"].(string), "Hawaii")

	code, out = postJSON(t, ts.URL+"/v1/delete", `{"user_id":"u1"}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(1), out["deleted"])
}

// ── Concurrent ingestion (parallel embedding, sequential writes) ─────────────

// failingBatchEmbedder wraps a keyword embedder and always fails batched
// embedding, simulating a provider outage during the parallel phase.
type failingBatchEmbedder struct {
	*embedder.KeywordEmbedder
}

func (f *failingBatchEmbedder) EmbedBatch(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("embedding service unavailable")
}

func TestBench_Add_ConcurrentIngest_PersistsAllMessages(t *testing.T) {
	ts := newTestServer(t, GateModeOff)

	// The keyword embedder is batch-capable, so /v1/add must embed all 200
	// messages concurrently, then persist them sequentially.
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"role":"user","content":"memory number %d about topic alpha","chat_time":"2029-01-01T10:00:00Z"}`, i)
	}
	code, out := postJSON(t, ts.URL+"/v1/add",
		fmt.Sprintf(`{"user_id":"u1","messages":[%s]}`, sb.String()))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(200), out["persisted"])
	assert.Equal(t, float64(0), out["skipped"])

	code, out = postJSON(t, ts.URL+"/v1/search", `{"user_id":"u1","query":"topic alpha memory","top_k":10}`)
	require.Equal(t, http.StatusOK, code)
	results, ok := out["results"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, results, "concurrently ingested memories must be retrievable")
}

func TestBench_Add_ConcurrentIngest_EmptyMessagesSkipped(t *testing.T) {
	ts := newTestServer(t, GateModeOff)

	code, out := postJSON(t, ts.URL+"/v1/add", `{
		"user_id": "u1",
		"messages": [
			{"role": "user", "content": "  "},
			{"role": "user", "content": "a real memory about penguins"},
			{"role": "user", "content": ""}
		]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(1), out["persisted"])
	assert.Equal(t, float64(2), out["skipped"])
	reasons, ok := out["gate_reason_counts"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(2), reasons["empty_message"])
}

func TestBench_Add_ConcurrentIngest_PreservesWriteOrder(t *testing.T) {
	// Writes must persist in request order even though embedding is parallel:
	// two similar messages merge into one memory, and the merged summary
	// concatenates their summaries in request order ("first\nsecond"). If the
	// sequential phase reordered writes, the concatenation would flip.
	ts := newTestServer(t, GateModeOff)

	code, out := postJSON(t, ts.URL+"/v1/add", `{
		"user_id": "u1",
		"messages": [
			{"role": "user", "content": "alpha beta gamma"},
			{"role": "user", "content": "alpha beta gamma delta"}
		]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(2), out["persisted"])

	code, out = postJSON(t, ts.URL+"/v1/search", `{"user_id":"u1","query":"gamma","top_k":3}`)
	require.Equal(t, http.StatusOK, code)
	results, ok := out["results"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, results)
	top := results[0].(map[string]any)["content"].(string)
	assert.True(t, strings.HasPrefix(top, "alpha beta gamma\n"),
		"merged memory must concatenate summaries in request order, got %q", top)
}

func TestBench_Add_EmbeddingFailure_Returns500(t *testing.T) {
	st := memory.NewStore()
	deps := core.WriteDeps{
		Store:       st,
		Gate:        core.DefaultSignificanceGate(),
		PatternSep:  core.DefaultPatternSeparation(),
		WeightDecay: core.DefaultWeightDecayEngine(),
		Embedder:    &failingBatchEmbedder{KeywordEmbedder: embedder.NewKeywordEmbedder(models.DefaultEmbeddingDim)},
		WorkingMem:  store.NewWorkingMemoryBuffer(20),
	}
	srv, err := NewServer(deps, Options{GateMode: GateModeOff})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	code, _ := postJSON(t, ts.URL+"/v1/add",
		`{"user_id":"u1","messages":[{"content":"a memory"}]}`)
	assert.Equal(t, http.StatusInternalServerError, code)
}

func TestBench_Add_NonBatchEmbedder_FallsBackSequential(t *testing.T) {
	// An embedder without batch support (stub) must degrade to the sequential
	// per-message pipeline, not fail the request.
	st := memory.NewStore()
	deps := core.WriteDeps{
		Store:       st,
		Gate:        core.DefaultSignificanceGate(),
		PatternSep:  core.DefaultPatternSeparation(),
		WeightDecay: core.DefaultWeightDecayEngine(),
		Embedder:    embedder.NewStubEmbedder(),
		WorkingMem:  store.NewWorkingMemoryBuffer(20),
	}
	srv, err := NewServer(deps, Options{GateMode: GateModeOff})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	code, out := postJSON(t, ts.URL+"/v1/add", `{
		"user_id": "u1",
		"messages": [{"content": "one"}, {"content": "two"}, {"content": "three"}]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(3), out["persisted"])
}

func TestBench_Add_ConcurrentIngest_OptionsPlumbed(t *testing.T) {
	// IngestWorkers / EmbedBatchSize must reach the batch embedder: a tiny
	// batch size of 1 with 2 workers still persists everything.
	st := memory.NewStore()
	deps := core.WriteDeps{
		Store:       st,
		Gate:        core.DefaultSignificanceGate(),
		PatternSep:  core.DefaultPatternSeparation(),
		WeightDecay: core.DefaultWeightDecayEngine(),
		Embedder:    embedder.NewKeywordEmbedder(models.DefaultEmbeddingDim),
		WorkingMem:  store.NewWorkingMemoryBuffer(20),
	}
	srv, err := NewServer(deps, Options{
		GateMode:       GateModeOff,
		IngestWorkers:  2,
		EmbedBatchSize: 1,
	})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	code, out := postJSON(t, ts.URL+"/v1/add", `{
		"user_id": "u1",
		"messages": [
			{"content": "a"}, {"content": "b"}, {"content": "c"},
			{"content": "d"}, {"content": "e"}
		]
	}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(5), out["persisted"])
}

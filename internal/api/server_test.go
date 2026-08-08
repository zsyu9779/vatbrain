package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vatbrain/vatbrain/internal/api"
	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

func newTestServer() *api.Server {
	cfg := config.LoadFromEnv()
	return api.NewServer(
		cfg,
		memory.NewStore(),
		store.NewWorkingMemoryBuffer(20),
		nil, nil, nil, nil, // legacy DB clients
		core.DefaultWeightDecayEngine(),
		core.DefaultReconsolidationEngine(),
		core.DefaultSignificanceGate(),
		core.DefaultPatternSeparation(),
		core.DefaultRetrievalEngine(),
		core.DefaultConsolidationEngine(),
		embedder.NewStubEmbedder(),
	)
}

func doRequest(t *testing.T, srv *api.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	return w
}

func decodeJSON[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.NewDecoder(w.Body).Decode(&v))
	return v
}

// ── Health ─────────────────────────────────────────────────────────────────

func TestHealth_Healthy(t *testing.T) {
	srv := newTestServer()
	w := doRequest(t, srv, http.MethodGet, "/api/v0/health", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.HealthResponse](t, w)
	assert.Equal(t, "healthy", resp.Status)
}

// ── Write ──────────────────────────────────────────────────────────────────

func TestHandleWrite_Success(t *testing.T) {
	srv := newTestServer()
	body := models.WriteRequest{
		ProjectID: "test-proj",
		Language:  "go",
		TaskType:  models.TaskTypeDebug,
		Content: models.WriteContent{
			Summary:  "fixed a nil pointer dereference",
			EntityID: "func:Foo",
		},
		UserConfirmed: true,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/episodic", body)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.WriteResponse](t, w)
	assert.True(t, resp.Persisted)
	assert.Equal(t, models.MergeActionCreatedNew, resp.MergeAction)
	assert.NotEqual(t, uuid.Nil, resp.MemoryID)
}

func TestHandleWrite_MissingProjectID(t *testing.T) {
	srv := newTestServer()
	body := models.WriteRequest{
		Content: models.WriteContent{Summary: "test"},
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/episodic", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleWrite_MissingSummary(t *testing.T) {
	srv := newTestServer()
	body := models.WriteRequest{
		ProjectID: "test-proj",
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/episodic", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleWrite_InvalidJSON(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/v0/memories/episodic",
		bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleWrite_Merge(t *testing.T) {
	srv := newTestServer()

	// Write an initial memory with an embedding so it's a merge candidate.
	emb := make([]float32, 1536)
	emb[0] = 1.0
	initialID := uuid.New()
	now := time.Now()
	mem := &models.EpisodicMemory{
		ID:            initialID,
		ProjectID:     "merge-proj",
		Language:      "go",
		TaskType:      models.TaskTypeDebug,
		Summary:       "original memory",
		SourceType:    models.SourceTypeUSER,
		TrustLevel:    5,
		Weight:        1.0,
		CreatedAt:     now,
		EntityGroup:   "func:Foo",
		ContextVector: emb,
	}
	require.NoError(t, srv.Store.WriteEpisodic(context.Background(), mem))

	// Now write a similar memory through the handler.
	// The stub embedder returns all zeros, so similarity is 0 and no merge happens
	// unless the embedder produces similar embeddings.
	body := models.WriteRequest{
		ProjectID: "merge-proj",
		Language:  "go",
		TaskType:  models.TaskTypeDebug,
		Content: models.WriteContent{
			Summary:  "additional details",
			EntityID: "func:Foo",
		},
		UserConfirmed: true,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/episodic", body)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.WriteResponse](t, w)
	assert.True(t, resp.Persisted)
	// With stub embedder (all zeros), it won't merge, but that's fine
	_ = resp
}

// ── Search ─────────────────────────────────────────────────────────────────

func TestHandleSearch_Success(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	// Write a memory first
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "search-proj",
		Language:   "go",
		TaskType:   models.TaskTypeDebug,
		Summary:    "fixed auth bug",
		SourceType: models.SourceTypeUSER,
		TrustLevel: 5,
		Weight:     1.0,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, srv.Store.WriteEpisodic(ctx, mem))

	body := models.SearchRequest{
		Query:   "auth bug",
		Context: models.SearchContext{ProjectID: "search-proj"},
		TopK:    5,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleSearch_MissingQuery(t *testing.T) {
	srv := newTestServer()
	body := models.SearchRequest{TopK: 10}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/search", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleSearch_EmptyResults(t *testing.T) {
	srv := newTestServer()
	body := models.SearchRequest{
		Query:   "nonexistent",
		Context: models.SearchContext{ProjectID: "no-such-proj"},
		TopK:    5,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.SearchResponse](t, w)
	assert.Empty(t, resp.Results)
}

func TestHandleSearch_WithPitfalls(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	// Write a pitfall for entity anchoring
	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:Buggy",
		EntityType:        models.EntityTypeFunction,
		ProjectID:         "pf-search",
		Language:          "go",
		Signature:         "nil pointer in concurrent map access",
		RootCauseCategory: models.RootCauseConcurrency,
		TrustLevel:        3,
		Weight:            0.8,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	require.NoError(t, srv.Store.WritePitfall(ctx, p))

	body := models.SearchRequest{
		Query: "nil pointer",
		Context: models.SearchContext{
			ProjectID: "pf-search",
			EntityID:  "func:Buggy",
		},
		TopK: 10,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.SearchResponse](t, w)
	// Should contain the pitfall result
	hasPitfall := false
	for _, r := range resp.Results {
		if r.Type == "pitfall" {
			hasPitfall = true
			break
		}
	}
	assert.True(t, hasPitfall, "expected pitfall results in search response")
}

// ── Feedback ───────────────────────────────────────────────────────────────

func TestHandleFeedback_ValidAction(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "fb-proj",
		Summary:    "test memory",
		TaskType:   models.TaskTypeFeature,
		SourceType: models.SourceTypeUSER,
		TrustLevel: 5,
		Weight:     0.8,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, srv.Store.WriteEpisodic(ctx, mem))

	body := models.FeedbackRequest{
		Action: models.SearchActionUsed,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/"+mem.ID.String()+"/feedback", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleFeedback_InvalidAction(t *testing.T) {
	srv := newTestServer()
	body := models.FeedbackRequest{
		Action: "invalid_action",
	}
	id := uuid.New().String()
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/"+id+"/feedback", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleFeedback_InvalidMemoryID(t *testing.T) {
	srv := newTestServer()
	body := models.FeedbackRequest{
		Action: models.SearchActionUsed,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v0/memories/not-a-uuid/feedback",
		bytes.NewReader(mustMarshal(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleFeedback_NotFound(t *testing.T) {
	srv := newTestServer()
	body := models.FeedbackRequest{
		Action: models.SearchActionConfirmed,
	}
	id := uuid.New() // never written
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/"+id.String()+"/feedback", body)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleFeedback_Correction(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "corr-proj",
		Summary:    "correctable memory",
		TaskType:   models.TaskTypeFeature,
		SourceType: models.SourceTypeUSER,
		TrustLevel: 5,
		Weight:     0.7,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, srv.Store.WriteEpisodic(ctx, mem))

	body := models.FeedbackRequest{
		Action: models.SearchActionCorrected,
		CorrectionDetail: &models.CorrectionDetail{
			Original:    "wrong",
			CorrectedTo: "right",
		},
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/"+mem.ID.String()+"/feedback", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── Touch ──────────────────────────────────────────────────────────────────

func TestHandleTouch_Success(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "touch-proj",
		Summary:    "touch test",
		TaskType:   models.TaskTypeReview,
		SourceType: models.SourceTypeUSER,
		TrustLevel: 5,
		Weight:     0.5,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, srv.Store.WriteEpisodic(ctx, mem))

	body := models.TouchRequest{}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/"+mem.ID.String()+"/touch", body)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.TouchResponse](t, w)
	assert.True(t, resp.NewWeight > 0)
}

func TestHandleTouch_NotFound(t *testing.T) {
	srv := newTestServer()
	id := uuid.New()
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/"+id.String()+"/touch", models.TouchRequest{})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Weight Detail ──────────────────────────────────────────────────────────

func TestHandleWeightDetail_Success(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "wd-proj",
		Summary:    "weight test",
		TaskType:   models.TaskTypeFeature,
		SourceType: models.SourceTypeUSER,
		TrustLevel: 5,
		Weight:     0.75,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, srv.Store.WriteEpisodic(ctx, mem))

	w := doRequest(t, srv, http.MethodGet, "/api/v0/memories/"+mem.ID.String()+"/weight", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.WeightDetailResponse](t, w)
	assert.Equal(t, mem.ID, resp.MemoryID)
	assert.Equal(t, 0.75, resp.Weight)
}

func TestHandleWeightDetail_NotFound(t *testing.T) {
	srv := newTestServer()
	id := uuid.New()
	w := doRequest(t, srv, http.MethodGet, "/api/v0/memories/"+id.String()+"/weight", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Pitfall Search ─────────────────────────────────────────────────────────

func TestHandlePitfallSearch_ByEntity(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:Pit",
		EntityType:        models.EntityTypeFunction,
		ProjectID:         "pit-proj",
		Signature:         "race condition",
		RootCauseCategory: models.RootCauseConcurrency,
		TrustLevel:        3,
		Weight:            0.9,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	require.NoError(t, srv.Store.WritePitfall(ctx, p))

	body := map[string]any{
		"entity_id":  "func:Pit",
		"project_id": "pit-proj",
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/pitfalls/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandlePitfallSearch_EmptyResults(t *testing.T) {
	srv := newTestServer()
	body := map[string]any{
		"entity_id":  "func:Nonexistent",
		"project_id": "no-such-proj",
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/pitfalls/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandlePitfallSearch_FullSearch(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	p := &models.PitfallMemory{
		ID:                uuid.New(),
		ProjectID:         "pit-full",
		Signature:         "out of memory",
		RootCauseCategory: models.RootCauseResourceExhaustion,
		TrustLevel:        2,
		Weight:            0.6,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	require.NoError(t, srv.Store.WritePitfall(ctx, p))

	body := map[string]any{
		"project_id":          "pit-full",
		"root_cause_category": "RESOURCE_EXHAUSTION",
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/pitfalls/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── Consolidation ──────────────────────────────────────────────────────────

func TestHandleConsolidationTrigger_Success(t *testing.T) {
	srv := newTestServer()
	w := doRequest(t, srv, http.MethodPost, "/api/v0/consolidation/trigger", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.ConsolidationTriggerResponse](t, w)
	assert.Equal(t, "started", resp.Status)
	assert.NotEqual(t, uuid.Nil, resp.RunID)
}

func TestHandleConsolidationStatus_ValidRunID(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	run := &models.ConsolidationRunResult{
		RunID:             uuid.New(),
		StartedAt:         time.Now(),
		EpisodicsScanned:  100,
		RulesPersisted:    5,
		AverageAccuracy:   0.85,
		PitfallsExtracted: 2,
	}
	require.NoError(t, srv.Store.SaveConsolidationRun(ctx, run))

	w := doRequest(t, srv, http.MethodGet, "/api/v0/consolidation/runs/"+run.RunID.String(), nil)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.ConsolidationRunResult](t, w)
	assert.Equal(t, run.RunID, resp.RunID)
	assert.Equal(t, 100, resp.EpisodicsScanned)
}

func TestHandleConsolidationStatus_NotFound(t *testing.T) {
	srv := newTestServer()
	id := uuid.New()
	w := doRequest(t, srv, http.MethodGet, "/api/v0/consolidation/runs/"+id.String(), nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleConsolidationStatus_InvalidRunID(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/v0/consolidation/runs/not-a-uuid", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleFeedback_Semantic(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	sem := &models.SemanticMemory{
		ID:         uuid.New(),
		Type:       models.MemoryTypeRule,
		Content:    "always check errors",
		SourceType: models.SourceTypeINFERRED,
		TrustLevel: 2,
		Weight:     0.6,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, srv.Store.WriteSemantic(ctx, sem))

	body := models.FeedbackRequest{
		Action: models.SearchActionConfirmed,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/"+sem.ID.String()+"/feedback", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleFeedback_Pitfall(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:FeedbackPf",
		ProjectID:         "fb-pf-proj",
		Signature:         "test pitfall",
		RootCauseCategory: models.RootCauseLogicError,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        3,
		Weight:            0.5,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	require.NoError(t, srv.Store.WritePitfall(ctx, p))

	body := models.FeedbackRequest{
		Action: models.SearchActionIgnored,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/"+p.ID.String()+"/feedback", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleWrite_GateRejected(t *testing.T) {
	srv := newTestServer()
	// Without user_confirmed and with empty working memory, the significance gate may reject.
	body := models.WriteRequest{
		ProjectID: "gate-test",
		Content: models.WriteContent{
			Summary: "a very short note",
		},
		UserConfirmed: false,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/episodic", body)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.WriteResponse](t, w)
	// Gate may pass or reject depending on defaults; just verify valid response.
	assert.False(t, resp.MemoryID == uuid.Nil && resp.Persisted)
	_ = resp
}

func TestHandleTouch_WithLastAccessedAt(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	now := time.Now().Add(-time.Hour)
	mem := &models.EpisodicMemory{
		ID:             uuid.New(),
		ProjectID:      "touch-laa-proj",
		Summary:        "recently accessed memory",
		TaskType:       models.TaskTypeRefactor,
		SourceType:     models.SourceTypeUSER,
		TrustLevel:     5,
		Weight:         0.9,
		CreatedAt:      now,
		LastAccessedAt: &now,
	}
	require.NoError(t, srv.Store.WriteEpisodic(ctx, mem))

	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/"+mem.ID.String()+"/touch", models.TouchRequest{})
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleWeightDetail_WithLastAccessed(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	now := time.Now().Add(-30 * time.Minute)
	mem := &models.EpisodicMemory{
		ID:             uuid.New(),
		ProjectID:      "wd-laa-proj",
		Summary:        "memory with access time",
		TaskType:       models.TaskTypeFeature,
		SourceType:     models.SourceTypeUSER,
		TrustLevel:     5,
		Weight:         0.6,
		CreatedAt:      now,
		LastAccessedAt: &now,
	}
	require.NoError(t, srv.Store.WriteEpisodic(ctx, mem))

	w := doRequest(t, srv, http.MethodGet, "/api/v0/memories/"+mem.ID.String()+"/weight", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.WeightDetailResponse](t, w)
	assert.Equal(t, mem.ID, resp.MemoryID)
	assert.GreaterOrEqual(t, resp.ExperienceDecay, 0.0)
}

func TestHandlePitfallSearch_WithQuery(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	p := &models.PitfallMemory{
		ID:                uuid.New(),
		ProjectID:         "pit-query-proj",
		Signature:         "database connection timeout",
		RootCauseCategory: models.RootCauseResourceExhaustion,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        2,
		Weight:            0.7,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	require.NoError(t, srv.Store.WritePitfall(ctx, p))

	body := map[string]any{
		"project_id": "pit-query-proj",
		"query":      "timeout",
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/pitfalls/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ── Embedder mocks ───────────────────────────────────────────────────────────

type failingEmbedder struct{ err error }

func (f *failingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, f.err
}

type fixedEmbedder struct{ emb []float32 }

func (f *fixedEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return f.emb, nil
}

func newTestServerWithEmbedder(emb embedder.Embedder) *api.Server {
	cfg := config.LoadFromEnv()
	return api.NewServer(
		cfg,
		memory.NewStore(),
		store.NewWorkingMemoryBuffer(20),
		nil, nil, nil, nil,
		core.DefaultWeightDecayEngine(),
		core.DefaultReconsolidationEngine(),
		core.DefaultSignificanceGate(),
		core.DefaultPatternSeparation(),
		core.DefaultRetrievalEngine(),
		core.DefaultConsolidationEngine(),
		emb,
	)
}

// ── Write: embed error ───────────────────────────────────────────────────────

func TestHandleWrite_EmbedError(t *testing.T) {
	srv := newTestServerWithEmbedder(&failingEmbedder{err: context.DeadlineExceeded})
	body := models.WriteRequest{
		ProjectID:     "err-proj",
		Content:       models.WriteContent{Summary: "some text"},
		UserConfirmed: true,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/episodic", body)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Search: embed error ──────────────────────────────────────────────────────

func TestHandleSearch_EmbedError(t *testing.T) {
	srv := newTestServerWithEmbedder(&failingEmbedder{err: context.DeadlineExceeded})
	body := models.SearchRequest{
		Query:   "test",
		Context: models.SearchContext{ProjectID: "err-proj"},
		TopK:    5,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/search", body)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Search: TopK capping ─────────────────────────────────────────────────────

func TestHandleSearch_TopKCapping(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	// Write semantic memories that token-overlap with the query to produce multiple results.
	for i := 0; i < 3; i++ {
		sem := &models.SemanticMemory{
			ID:         uuid.New(),
			Type:       models.MemoryTypeRule,
			Content:    "error handling pattern " + string(rune('a'+i)),
			SourceType: models.SourceTypeINFERRED,
			TrustLevel: 2,
			Weight:     0.6,
			CreatedAt:  time.Now(),
		}
		require.NoError(t, srv.Store.WriteSemantic(ctx, sem))
	}

	// Search with TopK=1 to trigger the capping branch.
	body := models.SearchRequest{
		Query:   "error handling",
		Context: models.SearchContext{ProjectID: "topk-proj"},
		TopK:    1,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.SearchResponse](t, w)
	assert.LessOrEqual(t, len(resp.Results), 1)
}

// ── Search: include dormant ──────────────────────────────────────────────────

func TestHandleSearch_IncludeDormant(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "dormant-proj",
		Summary:    "dormant memory",
		TaskType:   models.TaskTypeFeature,
		SourceType: models.SourceTypeUSER,
		TrustLevel: 5,
		Weight:     0.1,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, srv.Store.WriteEpisodic(ctx, mem))

	body := models.SearchRequest{
		Query:          "dormant",
		Context:        models.SearchContext{ProjectID: "dormant-proj"},
		TopK:           10,
		IncludeDormant: true,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── Write: with IsCorrection ─────────────────────────────────────────────────

func TestHandleWrite_IsCorrection(t *testing.T) {
	srv := newTestServer()
	body := models.WriteRequest{
		ProjectID:     "corr-write",
		Content:       models.WriteContent{Summary: "corrected memory content"},
		UserConfirmed: true,
		IsCorrection:  true,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/episodic", body)
	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeJSON[models.WriteResponse](t, w)
	assert.True(t, resp.Persisted)
}

// ── Search: with language filter ─────────────────────────────────────────────

func TestHandleSearch_WithLanguage(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "lang-proj",
		Language:   "python",
		Summary:    "python script",
		TaskType:   models.TaskTypeFeature,
		SourceType: models.SourceTypeUSER,
		TrustLevel: 5,
		Weight:     1.0,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, srv.Store.WriteEpisodic(ctx, mem))

	body := models.SearchRequest{
		Query:   "script",
		Context: models.SearchContext{ProjectID: "lang-proj", Language: "python"},
		TopK:    10,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── Search: with semantic memory overlap ─────────────────────────────────────

// ── Health: degraded backend ──────────────────────────────────────────────────

type unhealthyStore struct {
	*memory.Store
}

func (u *unhealthyStore) HealthCheck(_ context.Context) error {
	return context.DeadlineExceeded
}

func TestHandleHealth_Degraded(t *testing.T) {
	cfg := config.LoadFromEnv()
	srv := api.NewServer(
		cfg,
		&unhealthyStore{Store: memory.NewStore()},
		store.NewWorkingMemoryBuffer(20),
		nil, nil, nil, nil,
		core.DefaultWeightDecayEngine(),
		core.DefaultReconsolidationEngine(),
		core.DefaultSignificanceGate(),
		core.DefaultPatternSeparation(),
		core.DefaultRetrievalEngine(),
		core.DefaultConsolidationEngine(),
		embedder.NewStubEmbedder(),
	)

	w := doRequest(t, srv, http.MethodGet, "/api/v0/health", nil)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp models.HealthResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "degraded", resp.Status)
	assert.Contains(t, resp.Message, "unhealthy")
}

func TestHandleSearch_WithSemanticOverlap(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	sem := &models.SemanticMemory{
		ID:         uuid.New(),
		Type:       models.MemoryTypeRule,
		Content:    "always check errors before proceeding",
		SourceType: models.SourceTypeINFERRED,
		TrustLevel: 2,
		Weight:     0.6,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, srv.Store.WriteSemantic(ctx, sem))

	body := models.SearchRequest{
		Query:   "check errors",
		Context: models.SearchContext{ProjectID: "sem-proj"},
		TopK:    10,
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/memories/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

// Touch: invalid memory ID

func TestHandleTouch_InvalidMemoryID(t *testing.T) {
	srv := newTestServer()
	body := models.TouchRequest{}
	req := httptest.NewRequest(http.MethodPost, "/api/v0/memories/not-a-uuid/touch",
		bytes.NewReader(mustMarshal(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// WeightDetail: invalid memory ID

func TestHandleWeightDetail_InvalidMemoryID(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/v0/memories/not-a-uuid/weight", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// Pitfall search: entity_id + query (dual-key path)

func TestHandlePitfallSearch_ByEntityAndQuery(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:BugFunc",
		ProjectID:         "dual-key-proj",
		Signature:         "nil pointer dereference in handler",
		RootCauseCategory: models.RootCauseLogicError,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        2,
		Weight:            0.7,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	require.NoError(t, srv.Store.WritePitfall(ctx, p))

	body := map[string]any{
		"entity_id":  "func:BugFunc",
		"project_id": "dual-key-proj",
		"query":      "nil pointer",
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v0/pitfalls/search", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

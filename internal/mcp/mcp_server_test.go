package mcp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/embedder"
	vatmcp "github.com/vatbrain/vatbrain/internal/mcp"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

// minimalApp creates an App with only engines and a stub embedder for testing.
func minimalApp() *app.App {
	cfg := config.LoadFromEnv()
	return &app.App{
		Config:             cfg,
		Store:              memory.NewStore(),
		WorkingMemory:      store.NewWorkingMemoryBuffer(20),
		WeightDecay:        core.DefaultWeightDecayEngine(),
		Reconsolidation:    core.DefaultReconsolidationEngine(),
		SignificanceGate:   core.DefaultSignificanceGate(),
		PatternSeparation:  core.DefaultPatternSeparation(),
		RetrievalEngine:    core.DefaultRetrievalEngine(),
		Consolidation:      core.DefaultConsolidationEngine(),
		Embedder:           embedder.NewStubEmbedder(),
	}
}

func TestMCPServer_ToolRegistration(t *testing.T) {
	a := minimalApp()

	// Create tools manually for testing.
	tools := vatmcp.RegisteredTools(a)
	require.NotEmpty(t, tools)

	names := make(map[string]bool, len(tools))
	for _, st := range tools {
		names[st.Tool.Name] = true
	}

	expected := []string{
		"write_memory",
		"search_memories",
		"search_pitfalls",
		"trigger_consolidation",
		"get_memory_weight",
		"touch_memory",
		"health_check",
		// v0.2.2 Pitfall Workbench
		"list_pitfalls",
		"explain_pitfall",
		"confirm_pitfall",
		"suppress_pitfall",
		"link_pitfall_entity",
		"feedback_pitfall",
		// v0.3 Proactive Risk Injection
		"prepare_edit_context",
	}
	for _, name := range expected {
		assert.True(t, names[name], "expected tool %q to be registered", name)
	}
	assert.Len(t, tools, len(expected))
}

func TestToolSchemas(t *testing.T) {
	a := minimalApp()
	tools := vatmcp.RegisteredTools(a)

	for _, st := range tools {
		t.Run(st.Tool.Name, func(t *testing.T) {
			assert.NotEmpty(t, st.Tool.Name)
			assert.NotEmpty(t, st.Tool.Description,
				"tool %q should have a description", st.Tool.Name)
			assert.NotNil(t, st.Tool.InputSchema,
				"tool %q should have an input schema", st.Tool.Name)
			assert.NotNil(t, st.Handler,
				"tool %q should have a handler", st.Tool.Name)
		})
	}
}

func TestSearchMemories_MissingRequiredArgs(t *testing.T) {
	a := minimalApp()

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	ctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "search_memories"
	req.Params.Arguments = map[string]any{}

	result, err := srv.Client().CallTool(ctx, req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should return error for missing query")
}

func TestWriteMemory_MissingRequiredArgs(t *testing.T) {
	a := minimalApp()

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	ctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "write_memory"
	req.Params.Arguments = map[string]any{"summary": "test"}

	// Missing project_id — should error.
	result, err := srv.Client().CallTool(ctx, req)
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestGetMemoryWeight_MissingRequiredArgs(t *testing.T) {
	a := minimalApp()

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	ctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "get_memory_weight"
	req.Params.Arguments = map[string]any{}

	result, err := srv.Client().CallTool(ctx, req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should error for missing memory_id")
}

func TestTouchMemory_MissingRequiredArgs(t *testing.T) {
	a := minimalApp()

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	ctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "touch_memory"
	req.Params.Arguments = map[string]any{}

	result, err := srv.Client().CallTool(ctx, req)
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestHealthCheck_NoArgs(t *testing.T) {
	a := minimalApp()

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	ctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "health_check"
	req.Params.Arguments = map[string]any{}

	result, err := srv.Client().CallTool(ctx, req)
	require.NoError(t, err)
	// Will likely fail due to nil DBs, but shouldn't panic.
	_ = result
}

func TestTriggerConsolidation_WithArgs(t *testing.T) {
	a := minimalApp()

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	ctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "trigger_consolidation"
	req.Params.Arguments = map[string]any{
		"hours_to_scan":    12.0,
		"min_cluster_size": 5.0,
	}

	result, err := srv.Client().CallTool(ctx, req)
	require.NoError(t, err)
	_ = result
}

func TestWriteMemory_Success(t *testing.T) {
	a := minimalApp()

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	ctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "write_memory"
	req.Params.Arguments = map[string]any{
		"project_id": "test-proj",
		"summary":    "fixed a nil pointer in handler",
		"language":   "go",
		"task_type":  "debug",
	}

	result, err := srv.Client().CallTool(ctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected success but got error: %v", result)
}

func TestSearchMemories_Success(t *testing.T) {
	a := minimalApp()

	// Write a memory first so there's something to search.
	ctx := context.Background()
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "search-test",
		Language:   "go",
		Summary:    "fixed auth bug",
		TaskType:   models.TaskTypeDebug,
		SourceType: models.SourceTypeUSER,
		TrustLevel: 5,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, a.Store.WriteEpisodic(ctx, mem))

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "search_memories"
	req.Params.Arguments = map[string]any{
		"query":      "auth bug",
		"project_id": "search-test",
		"limit":      5.0,
	}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected success: %v", result)
}

func TestGetMemoryWeight_Success(t *testing.T) {
	a := minimalApp()

	ctx := context.Background()
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "weight-test",
		Language:   "go",
		Summary:    "test memory",
		TaskType:   models.TaskTypeFeature,
		SourceType: models.SourceTypeUSER,
		TrustLevel: 5,
		Weight:     0.9,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, a.Store.WriteEpisodic(ctx, mem))

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "get_memory_weight"
	req.Params.Arguments = map[string]any{
		"memory_id": mem.ID.String(),
	}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected success: %v", result)
}

func TestTouchMemory_Success(t *testing.T) {
	a := minimalApp()

	ctx := context.Background()
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "touch-test",
		Language:   "go",
		Summary:    "touchable memory",
		TaskType:   models.TaskTypeReview,
		SourceType: models.SourceTypeUSER,
		TrustLevel: 5,
		Weight:     0.8,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, a.Store.WriteEpisodic(ctx, mem))

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "touch_memory"
	req.Params.Arguments = map[string]any{
		"memory_id": mem.ID.String(),
	}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected success: %v", result)
}

func TestSearchPitfalls_Success(t *testing.T) {
	a := minimalApp()

	ctx := context.Background()
	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:Test",
		EntityType:        models.EntityTypeFunction,
		ProjectID:         "pitfall-test",
		Language:          "go",
		Signature:         "test pitfall",
		RootCauseCategory: models.RootCauseLogicError,
		TrustLevel:        3,
		Weight:            0.7,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	require.NoError(t, a.Store.WritePitfall(ctx, p))

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "search_pitfalls"
	req.Params.Arguments = map[string]any{
		"entity_id":  "func:Test",
		"project_id": "pitfall-test",
	}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected success: %v", result)
}

func TestTriggerConsolidation_WithCustomParams(t *testing.T) {
	a := minimalApp()

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	ctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "trigger_consolidation"
	req.Params.Arguments = map[string]any{
		"hours_to_scan":      48.0,
		"min_cluster_size":   5.0,
		"accuracy_threshold": 0.8,
	}

	result, err := srv.Client().CallTool(ctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected success: %v", result)
}

func TestSearchMemories_WithEntityAndPitfall(t *testing.T) {
	a := minimalApp()

	ctx := context.Background()
	// Write an episodic memory
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "entity-search",
		Language:   "go",
		Summary:    "concurrent map write in handler",
		TaskType:   models.TaskTypeDebug,
		SourceType: models.SourceTypeUSER,
		TrustLevel: 5,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, a.Store.WriteEpisodic(ctx, mem))

	// Write a pitfall for the entity
	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:ConcurrentHandler",
		EntityType:        models.EntityTypeFunction,
		ProjectID:         "entity-search",
		Signature:         "concurrent map write",
		RootCauseCategory: models.RootCauseConcurrency,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        3,
		Weight:            0.8,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	require.NoError(t, a.Store.WritePitfall(ctx, p))

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "search_memories"
	req.Params.Arguments = map[string]any{
		"query":      "concurrent",
		"project_id": "entity-search",
		"entity_id":  "func:ConcurrentHandler",
		"limit":      5.0,
	}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected success: %v", result)
}

func TestWriteMemory_WithCorrection(t *testing.T) {
	a := minimalApp()

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	ctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "write_memory"
	req.Params.Arguments = map[string]any{
		"project_id":    "correction-proj",
		"summary":       "the correct approach is X not Y",
		"language":      "go",
		"task_type":     "debug",
		"is_correction": true,
	}

	result, err := srv.Client().CallTool(ctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected success: %v", result)
}

func TestGetMemoryWeight_InvalidID(t *testing.T) {
	a := minimalApp()
	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "get_memory_weight"
	req.Params.Arguments = map[string]any{"memory_id": "not-a-uuid"}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should error for invalid UUID")
}

func TestGetMemoryWeight_NotFound(t *testing.T) {
	a := minimalApp()
	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "get_memory_weight"
	req.Params.Arguments = map[string]any{"memory_id": uuid.New().String()}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should error for non-existent memory")
}

func TestTouchMemory_InvalidID(t *testing.T) {
	a := minimalApp()
	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "touch_memory"
	req.Params.Arguments = map[string]any{"memory_id": "invalid-uuid"}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should error for invalid UUID")
}

func TestTouchMemory_NotFound(t *testing.T) {
	a := minimalApp()
	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "touch_memory"
	req.Params.Arguments = map[string]any{"memory_id": uuid.New().String()}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should error for non-existent memory")
}

func TestSearchPitfalls_ByRootCause(t *testing.T) {
	a := minimalApp()
	ctx := context.Background()
	p := &models.PitfallMemory{
		ID:                uuid.New(),
		ProjectID:         "pf-rc-proj",
		Signature:         "out of memory",
		RootCauseCategory: models.RootCauseResourceExhaustion,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        2,
		Weight:            0.6,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	require.NoError(t, a.Store.WritePitfall(ctx, p))

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "search_pitfalls"
	req.Params.Arguments = map[string]any{
		"project_id":          "pf-rc-proj",
		"root_cause_category": "RESOURCE_EXHAUSTION",
	}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected success: %v", result)
}

func TestSearchPitfalls_WithQuery(t *testing.T) {
	a := minimalApp()
	ctx := context.Background()
	p := &models.PitfallMemory{
		ID:                uuid.New(),
		ProjectID:         "pf-q-proj",
		Signature:         "database connection timeout",
		RootCauseCategory: models.RootCauseResourceExhaustion,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        2,
		Weight:            0.7,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	require.NoError(t, a.Store.WritePitfall(ctx, p))

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "search_pitfalls"
	req.Params.Arguments = map[string]any{
		"project_id": "pf-q-proj",
		"query":      "timeout",
		"top_k":      5.0,
	}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected success: %v", result)
}

// minimalAppWithEmbedder creates an App with a custom embedder.
func minimalAppWithEmbedder(emb embedder.Embedder) *app.App {
	cfg := config.LoadFromEnv()
	return &app.App{
		Config:             cfg,
		Store:              memory.NewStore(),
		WorkingMemory:      store.NewWorkingMemoryBuffer(20),
		WeightDecay:        core.DefaultWeightDecayEngine(),
		Reconsolidation:    core.DefaultReconsolidationEngine(),
		SignificanceGate:   core.DefaultSignificanceGate(),
		PatternSeparation:  core.DefaultPatternSeparation(),
		RetrievalEngine:    core.DefaultRetrievalEngine(),
		Consolidation:      core.DefaultConsolidationEngine(),
		Embedder:           emb,
	}
}

func TestNewMCPServer_HasTools(t *testing.T) {
	a := minimalApp()
	s := vatmcp.NewMCPServer(a)
	assert.NotNil(t, s)
}

type failingEmbedder struct{ err error }

func (f *failingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, f.err
}

func TestSearchMemories_EmbedError(t *testing.T) {
	a := minimalAppWithEmbedder(&failingEmbedder{err: errors.New("api error")})

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "search_memories"
	req.Params.Arguments = map[string]any{
		"query":      "test",
		"project_id": "proj",
	}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "expected embed error")
}

type unhealthyStore struct {
	*memory.Store
}

func (u *unhealthyStore) HealthCheck(_ context.Context) error {
	return errors.New("backend unreachable")
}

func TestHealthCheck_DegradedResponse(t *testing.T) {
	cfg := config.LoadFromEnv()
	a := &app.App{
		Config:             cfg,
		Store:              &unhealthyStore{Store: memory.NewStore()},
		WorkingMemory:      store.NewWorkingMemoryBuffer(20),
		WeightDecay:        core.DefaultWeightDecayEngine(),
		Reconsolidation:    core.DefaultReconsolidationEngine(),
		SignificanceGate:   core.DefaultSignificanceGate(),
		PatternSeparation:  core.DefaultPatternSeparation(),
		RetrievalEngine:    core.DefaultRetrievalEngine(),
		Consolidation:      core.DefaultConsolidationEngine(),
		Embedder:           embedder.NewStubEmbedder(),
	}

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "health_check"
	req.Params.Arguments = map[string]any{}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "health_check should return degraded status, not an error")
}

func TestHealthCheck_HealthyResponse(t *testing.T) {
	a := minimalApp()

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "health_check"
	req.Params.Arguments = map[string]any{}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected healthy response")
}

func TestWriteMemory_EmbedError(t *testing.T) {
	a := minimalAppWithEmbedder(&failingEmbedder{err: errors.New("api down")})

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "write_memory"
	req.Params.Arguments = map[string]any{
		"project_id":    "err-proj",
		"summary":       "test",
		"user_confirmed": true,
	}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "expected embed error")
}

func TestSearchPitfalls_NoArgs(t *testing.T) {
	a := minimalApp()

	srv, err := mcptest.NewServer(t, vatmcp.RegisteredTools(a)...)
	require.NoError(t, err)
	defer srv.Close()

	tctx := t.Context()
	var req mcp.CallToolRequest
	req.Params.Name = "search_pitfalls"
	req.Params.Arguments = map[string]any{}

	result, err := srv.Client().CallTool(tctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "full search without args should succeed")
}

func TestClampWeight(t *testing.T) {
	assert.Equal(t, 0.0, vatmcp.ClampWeight(-1.0))
	assert.Equal(t, 0.0, vatmcp.ClampWeight(-0.1))
	assert.Equal(t, 0.0, vatmcp.ClampWeight(0.0))
	assert.Equal(t, 0.5, vatmcp.ClampWeight(0.5))
	assert.Equal(t, 1.0, vatmcp.ClampWeight(1.0))
	assert.Equal(t, 1.0, vatmcp.ClampWeight(1.5))
	assert.Equal(t, 1.0, vatmcp.ClampWeight(100.0))
}

package mcp_test

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/embedder"
	vatmcp "github.com/vatbrain/vatbrain/internal/mcp"
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
		"trigger_consolidation",
		"get_memory_weight",
		"touch_memory",
		"health_check",
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
	// Will fail because no Redis, but shouldn't panic.
	_ = result
}

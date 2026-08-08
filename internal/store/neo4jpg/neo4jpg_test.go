package neo4jpg_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vatbrain/vatbrain/internal/db/neo4j"
	"github.com/vatbrain/vatbrain/internal/db/pgvector"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/neo4jpg"
	"github.com/vatbrain/vatbrain/internal/vector"
)

func mustConnect(t *testing.T) *neo4jpg.Store {
	t.Helper()
	ctx := context.Background()

	nc, err := neo4j.NewClient(ctx, neo4j.Config{
		URI:      "bolt://localhost:7687",
		Username: "neo4j",
		Password: "vatbrain",
		Database: "neo4j",
	})
	if err != nil {
		t.Skipf("skipping integration test: neo4j not available: %v", err)
	}

	pc, err := pgvector.NewClient(ctx, pgvector.Config{
		Host:     "localhost",
		Port:     5432,
		User:     "vatbrain",
		Password: "vatbrain",
		Database: "vatbrain",
	})
	if err != nil {
		nc.Close(ctx)
		t.Skipf("skipping integration test: pgvector not available: %v", err)
	}

	s, err := neo4jpg.NewStore(ctx, nc, pc)
	if err != nil {
		nc.Close(ctx)
		pc.Close()
		t.Skipf("skipping integration test: neo4jpg store init failed: %v", err)
	}

	t.Cleanup(func() { s.Close() })
	return s
}

// ── Episodic Memory ────────────────────────────────────────────────────────

func TestNeo4jpg_WriteGetEpisodic(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	mem := &models.EpisodicMemory{
		ID:            uuid.New(),
		ProjectID:     "test-proj",
		Language:      "go",
		TaskType:      models.TaskTypeDebug,
		Summary:       "fixed nil pointer",
		SourceType:    models.SourceTypeLLM,
		TrustLevel:    3,
		Weight:        1.0,
		CreatedAt:   time.Now().UTC(),
		EntityGroup: "func:Foo",
	}
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, mem.ID, got.ID)
	assert.Equal(t, mem.ProjectID, got.ProjectID)
	assert.Equal(t, "fixed nil pointer", got.Summary)
	assert.Equal(t, models.TaskTypeDebug, got.TaskType)
}

func TestNeo4jpg_GetEpisodic_NotFound(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()
	_, err := s.GetEpisodic(ctx, uuid.New())
	assert.Error(t, err)
}

func TestNeo4jpg_SearchEpisodic_Structured(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "search-proj",
		Language:   "python",
		TaskType:   models.TaskTypeFeature,
		Summary:    "implemented data pipeline",
		SourceType: models.SourceTypeLLM,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "search-proj",
		Language:  "python",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestNeo4jpg_SearchEpisodic_WithEmbedding(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	emb := make([]float32, 1536)
	emb[0] = 1.0
	mem := &models.EpisodicMemory{
		ID:            uuid.New(),
		ProjectID:     "emb-search",
		Language:      "go",
		TaskType:      models.TaskTypeDebug,
		Summary:       "embedded search test",
		SourceType:    models.SourceTypeLLM,
		TrustLevel:    3,
		Weight:        1.0,
		CreatedAt:     time.Now().UTC(),
		ContextVector: emb,
	}
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "emb-search",
		Embedding: vector.Float32To64(emb),
		Limit:     10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestNeo4jpg_SearchEpisodic_Structured_NoResults(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "nonexistent-proj-12345",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestNeo4jpg_GetEpisodic_WithEmbedding(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	emb := make([]float32, 1536)
	emb[0] = 0.5
	mem := &models.EpisodicMemory{
		ID:            uuid.New(),
		ProjectID:     "get-emb",
		Language:      "go",
		TaskType:      models.TaskTypeDebug,
		Summary:       "get with embedding",
		SourceType:    models.SourceTypeLLM,
		TrustLevel:    3,
		Weight:        1.0,
		CreatedAt:     time.Now().UTC(),
		ContextVector: emb,
	}
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	assert.NotNil(t, got)
	// ContextVector should be retrieved from pgvector (best-effort).
	assert.NotEmpty(t, got.ContextVector)
}

func TestNeo4jpg_TouchEpisodic(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "touch-proj",
		Summary:    "touchable",
		SourceType: models.SourceTypeLLM,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	now := time.Now().UTC()
	require.NoError(t, s.TouchEpisodic(ctx, mem.ID, now))

	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.LastAccessedAt)
}

func TestNeo4jpg_UpdateEpisodicWeight(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "weight-proj",
		Summary:    "weighted",
		SourceType: models.SourceTypeLLM,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	require.NoError(t, s.UpdateEpisodicWeight(ctx, mem.ID, 0.5, 2.0))
	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, 0.5, got.Weight)
	assert.Equal(t, 2.0, got.EffectiveFrequency)
}

func TestNeo4jpg_MarkObsolete(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "obs-proj",
		Summary:    "to be obsoleted",
		SourceType: models.SourceTypeLLM,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	now := time.Now().UTC()
	require.NoError(t, s.MarkObsolete(ctx, mem.ID, now))

	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.ObsoletedAt)
}

// ── Semantic Memory ────────────────────────────────────────────────────────

func TestNeo4jpg_WriteGetSemantic(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	mem := &models.SemanticMemory{
		ID:         uuid.New(),
		Type:       models.MemoryTypeRule,
		Content:    "always check err after json.Decode",
		SourceType: models.SourceTypeINFERRED,
		TrustLevel: 2,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
		EntityGroup: "err-handling",
	}
	require.NoError(t, s.WriteSemantic(ctx, mem))

	got, err := s.GetSemantic(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, mem.ID, got.ID)
	assert.Equal(t, models.MemoryTypeRule, got.Type)
	assert.Equal(t, mem.Content, got.Content)
}

func TestNeo4jpg_GetSemantic_NotFound(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()
	_, err := s.GetSemantic(ctx, uuid.New())
	assert.Error(t, err)
}

func TestNeo4jpg_SearchSemantic(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	mem := &models.SemanticMemory{
		ID:         uuid.New(),
		Type:       models.MemoryTypeFact,
		Content:    "the API server listens on port 8080",
		SourceType: models.SourceTypeSummarized,
		TrustLevel: 2,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
		EntityGroup: "search-sem-proj",
	}
	require.NoError(t, s.WriteSemantic(ctx, mem))

	results, err := s.SearchSemantic(ctx, store.SemanticSearchRequest{
		ProjectID:  "search-sem-proj",
		MemoryType: models.MemoryTypeFact,
		Limit:      10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestNeo4jpg_UpdateSemanticWeight(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	mem := &models.SemanticMemory{
		ID:         uuid.New(),
		Type:       models.MemoryTypeRule,
		Content:    "rule for weight update",
		SourceType: models.SourceTypeINFERRED,
		TrustLevel: 2,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, s.WriteSemantic(ctx, mem))

	require.NoError(t, s.UpdateSemanticWeight(ctx, mem.ID, 0.3, 5.0))
	got, err := s.GetSemantic(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, 0.3, got.Weight)
	assert.Equal(t, 5.0, got.EffectiveFrequency)
}

// ── Edges ──────────────────────────────────────────────────────────────────

func TestNeo4jpg_CreateGetEdges(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	from := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "edge-proj",
		Summary:    "from node",
		SourceType: models.SourceTypeLLM,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	to := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "edge-proj",
		Summary:    "to node",
		SourceType: models.SourceTypeLLM,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, s.WriteEpisodic(ctx, from))
	require.NoError(t, s.WriteEpisodic(ctx, to))

	err := s.CreateEdge(ctx, from.ID, to.ID, "RELATES_TO", map[string]any{
		"strength": 0.9,
	})
	require.NoError(t, err)

	// Get outgoing edges
	edges, err := s.GetEdges(ctx, from.ID, "", "out")
	require.NoError(t, err)
	assert.NotEmpty(t, edges)
	assert.Equal(t, "RELATES_TO", edges[0].EdgeType)

	// Get edges with type filter
	edges, err = s.GetEdges(ctx, from.ID, "RELATES_TO", "")
	require.NoError(t, err)
	assert.NotEmpty(t, edges)
	assert.Equal(t, "RELATES_TO", edges[0].EdgeType)
}

func TestNeo4jpg_GetEdges_Incoming(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	from := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "edge-in-proj",
		Summary:    "from node",
		SourceType: models.SourceTypeLLM,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	to := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "edge-in-proj",
		Summary:    "to node",
		SourceType: models.SourceTypeLLM,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, s.WriteEpisodic(ctx, from))
	require.NoError(t, s.WriteEpisodic(ctx, to))
	require.NoError(t, s.CreateEdge(ctx, from.ID, to.ID, "RELATES_TO", nil))

	edges, err := s.GetEdges(ctx, to.ID, "", "in")
	require.NoError(t, err)
	assert.NotEmpty(t, edges)
	assert.Equal(t, from.ID, edges[0].FromID)
}

func TestNeo4jpg_GetEdges_EmptyResult(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	edges, err := s.GetEdges(ctx, uuid.New(), "", "")
	require.NoError(t, err)
	assert.Empty(t, edges)
}

// ── Pitfall Memory ─────────────────────────────────────────────────────────

func TestNeo4jpg_WriteGetPitfall(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:TestFunc",
		EntityType:        models.EntityTypeFunction,
		ProjectID:         "pf-proj",
		Signature:         "nil pointer dereference in handler",
		RootCauseCategory: models.RootCauseLogicError,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        3,
		Weight:            1.0,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	require.NoError(t, s.WritePitfall(ctx, p))

	got, err := s.GetPitfall(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)
	assert.Equal(t, p.EntityID, got.EntityID)
	assert.Equal(t, p.Signature, got.Signature)
	assert.Equal(t, models.RootCauseLogicError, got.RootCauseCategory)
}

func TestNeo4jpg_GetPitfall_NotFound(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()
	_, err := s.GetPitfall(ctx, uuid.New())
	assert.Error(t, err)
}

func TestNeo4jpg_SearchPitfall_ByEntityID(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:SearchMe",
		EntityType:        models.EntityTypeFunction,
		ProjectID:         "pf-search-proj",
		Signature:         "concurrent map write",
		RootCauseCategory: models.RootCauseConcurrency,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        3,
		Weight:            1.0,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	require.NoError(t, s.WritePitfall(ctx, p))

	results, err := s.SearchPitfall(ctx, store.PitfallSearchRequest{
		EntityID:  "func:SearchMe",
		ProjectID: "pf-search-proj",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, results)
	assert.Equal(t, "func:SearchMe", results[0].EntityID)
}

func TestNeo4jpg_SearchPitfall_ByRootCause(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:RCTest",
		ProjectID:         "rc-proj",
		Signature:         "OOM in loop",
		RootCauseCategory: models.RootCauseResourceExhaustion,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        3,
		Weight:            1.0,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	require.NoError(t, s.WritePitfall(ctx, p))

	results, err := s.SearchPitfall(ctx, store.PitfallSearchRequest{
		ProjectID:        "rc-proj",
		RootCauseCategory: models.RootCauseResourceExhaustion,
		Limit:            10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestNeo4jpg_SearchPitfallByEntity(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:ByEntity",
		EntityType:        models.EntityTypeFunction,
		ProjectID:         "be-proj",
		Signature:         "test pitfall by entity",
		RootCauseCategory: models.RootCauseUnknown,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        3,
		Weight:            1.0,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	require.NoError(t, s.WritePitfall(ctx, p))

	results, err := s.SearchPitfallByEntity(ctx, "func:ByEntity", "be-proj")
	require.NoError(t, err)
	assert.NotEmpty(t, results)
	assert.Equal(t, "func:ByEntity", results[0].EntityID)
}

func TestNeo4jpg_SearchPitfallByEntity_NotFound(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	results, err := s.SearchPitfallByEntity(ctx, "func:NotFound", "no-proj")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestNeo4jpg_TouchPitfall(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:TouchPf",
		ProjectID:         "tp-proj",
		Signature:         "touchable pitfall",
		RootCauseCategory: models.RootCauseLogicError,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        3,
		Weight:            1.0,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	require.NoError(t, s.WritePitfall(ctx, p))

	now := time.Now().UTC()
	require.NoError(t, s.TouchPitfall(ctx, p.ID, now))

	got, err := s.GetPitfall(ctx, p.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.LastOccurredAt)
	assert.Equal(t, 1, got.OccurrenceCount)
}

func TestNeo4jpg_UpdatePitfallWeight(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:WeightPf",
		ProjectID:         "wp-proj",
		Signature:         "weighted pitfall",
		RootCauseCategory: models.RootCauseLogicError,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        3,
		Weight:            1.0,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	require.NoError(t, s.WritePitfall(ctx, p))

	require.NoError(t, s.UpdatePitfallWeight(ctx, p.ID, 0.2))
	got, err := s.GetPitfall(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, 0.2, got.Weight)
}

func TestNeo4jpg_MarkPitfallObsolete(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	p := &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:ObsPf",
		ProjectID:         "op-proj",
		Signature:         "obsolete pitfall",
		RootCauseCategory: models.RootCauseLogicError,
		SourceType:        models.SourceTypeLLM,
		TrustLevel:        3,
		Weight:            1.0,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	require.NoError(t, s.WritePitfall(ctx, p))

	now := time.Now().UTC()
	require.NoError(t, s.MarkPitfallObsolete(ctx, p.ID, now))

	got, err := s.GetPitfall(ctx, p.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.ObsoletedAt)
}

// ── Consolidation ──────────────────────────────────────────────────────────

func TestNeo4jpg_ScanRecent(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	// Use a time clearly in the past to avoid clock-resolution issues with Neo4j.
	past := time.Now().UTC().Add(-5 * time.Minute)
	mem := &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "scan-proj",
		Summary:    "recent memory",
		TaskType:   models.TaskTypeDebug,
		SourceType: models.SourceTypeLLM,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  past,
	}
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	// Scan with a since time earlier than the memory's CreatedAt.
	items, err := s.ScanRecent(ctx, past.Add(-time.Minute), 50)
	require.NoError(t, err)
	found := false
	for _, item := range items {
		if item.ID == mem.ID {
			found = true
			break
		}
	}
	assert.True(t, found, "expected to find recently created memory in scan")
}

func TestNeo4jpg_ScanRecent_OldOnly(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	items, err := s.ScanRecent(ctx, time.Now().UTC().Add(time.Minute), 10)
	require.NoError(t, err)
	assert.Empty(t, items) // nothing created in the future
}

func TestNeo4jpg_SaveGetConsolidationRun(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	run := &models.ConsolidationRunResult{
		RunID:              uuid.New(),
		StartedAt:          time.Now().UTC(),
		EpisodicsScanned:   42,
		CandidateRulesFound: 7,
		RulesPersisted:     3,
		AverageAccuracy:    0.9,
		PitfallsExtracted:  1,
		PitfallsMerged:     0,
		PitfallsPersisted:  1,
	}
	require.NoError(t, s.SaveConsolidationRun(ctx, run))

	got, err := s.GetConsolidationRun(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, run.RunID, got.RunID)
	assert.Equal(t, 42, got.EpisodicsScanned)
	assert.Equal(t, 7, got.CandidateRulesFound)
	assert.Equal(t, 3, got.RulesPersisted)
	assert.InDelta(t, 0.9, got.AverageAccuracy, 0.01)
	assert.Equal(t, 1, got.PitfallsExtracted)
	assert.Equal(t, 1, got.PitfallsPersisted)
}

func TestNeo4jpg_SaveConsolidationRun_Completed(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()

	completed := time.Now().UTC()
	run := &models.ConsolidationRunResult{
		RunID:              uuid.New(),
		StartedAt:          time.Now().UTC(),
		CompletedAt:        &completed,
		EpisodicsScanned:   10,
		CandidateRulesFound: 2,
		RulesPersisted:     1,
		AverageAccuracy:    0.75,
	}
	require.NoError(t, s.SaveConsolidationRun(ctx, run))

	got, err := s.GetConsolidationRun(ctx, run.RunID)
	require.NoError(t, err)
	assert.NotNil(t, got.CompletedAt)
	assert.True(t, got.CompletedAt.Equal(completed))
}

func TestNeo4jpg_GetConsolidationRun_NotFound(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()
	_, err := s.GetConsolidationRun(ctx, uuid.New())
	assert.Error(t, err)
}

// ── Lifecycle ──────────────────────────────────────────────────────────────

func TestNeo4jpg_HealthCheck(t *testing.T) {
	s := mustConnect(t)
	ctx := context.Background()
	assert.NoError(t, s.HealthCheck(ctx))
}

func TestNeo4jpg_Close(t *testing.T) {
	// Create a fresh store and close it — covers the Close path.
	s := mustConnect(t)
	assert.NoError(t, s.Close())
}



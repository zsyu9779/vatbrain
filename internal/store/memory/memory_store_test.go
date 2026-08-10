package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

var ctx = context.Background()

func makeEpisodic() *models.EpisodicMemory {
	now := time.Now().UTC()
	return &models.EpisodicMemory{
		ID:                uuid.New(),
		Summary:           "fixed a bug in login",
		ProjectID:         "test-project",
		Language:          "go",
		TaskType:          models.TaskTypeDebug,
		SourceType:        models.SourceTypeUSER,
		TrustLevel:        5,
		Weight:            models.DefaultWeight,
		EffectiveFrequency: 1,
		CreatedAt:         now,
		ContextVector:     []float32{0.1, 0.2, 0.3},
	}
}

func makeSemantic() *models.SemanticMemory {
	now := time.Now().UTC()
	return &models.SemanticMemory{
		ID:                uuid.New(),
		EntityGroup:       "test-project",
		Type:              models.MemoryTypeRule,
		Content:           "Login always checks password hash",
		Weight:            1.0,
		EffectiveFrequency: 1,
		CreatedAt:         now,
	}
}

func makePitfall() *models.PitfallMemory {
	now := time.Now().UTC()
	return &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "func:Login",
		EntityType:        models.EntityTypeFunction,
		ProjectID:         "test-project",
		Language:          "go",
		Signature:         "nil pointer in Login",
		RootCauseCategory: models.RootCauseLogicError,
		TrustLevel:        3,
		Weight:            0.8,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func makeConsolidationRun() *models.ConsolidationRunResult {
	runID := uuid.New()
	now := time.Now().UTC()
	return &models.ConsolidationRunResult{
		RunID:             runID,
		StartedAt:         now.Add(-time.Hour),
		CompletedAt:       &now,
		RulesPersisted:    2,
		PitfallsPersisted: 1,
	}
}

// ── Episodic Memory Tests ──────────────────────────────────────────────

func TestMemoryStore_WriteGetEpisodic(t *testing.T) {
	s := memory.NewStore()
	mem := makeEpisodic()
	err := s.WriteEpisodic(ctx, mem)
	require.NoError(t, err)

	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, mem.ID, got.ID)
	assert.Equal(t, mem.Summary, got.Summary)
	assert.Equal(t, mem.ProjectID, got.ProjectID)
}

func TestMemoryStore_DeleteEpisodicByProject(t *testing.T) {
	s := memory.NewStore()

	mem := makeEpisodic() // ProjectID "test-project"
	other := makeEpisodic()
	other.ProjectID = "other-project"
	require.NoError(t, s.WriteEpisodic(ctx, mem))
	require.NoError(t, s.WriteEpisodic(ctx, other))

	n, err := s.DeleteEpisodicByProject(ctx, "test-project")
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	_, err = s.GetEpisodic(ctx, mem.ID)
	assert.Error(t, err)

	got, err := s.GetEpisodic(ctx, other.ID)
	require.NoError(t, err)
	assert.Equal(t, "other-project", got.ProjectID)

	// Deleting a project with no memories returns 0, not an error.
	n, err = s.DeleteEpisodicByProject(ctx, "test-project")
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestMemoryStore_GetEpisodic_NotFound(t *testing.T) {
	s := memory.NewStore()
	_, err := s.GetEpisodic(ctx, uuid.New())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestMemoryStore_SearchEpisodic_ByProject(t *testing.T) {
	s := memory.NewStore()
	m1 := makeEpisodic()
	m1.ProjectID = "proj-a"
	m2 := makeEpisodic()
	m2.ProjectID = "proj-b"
	require.NoError(t, s.WriteEpisodic(ctx, m1))
	require.NoError(t, s.WriteEpisodic(ctx, m2))

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "proj-a",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, m1.ID, results[0].ID)
}

func TestMemoryStore_SearchEpisodic_ByLanguage(t *testing.T) {
	s := memory.NewStore()
	m1 := makeEpisodic()
	m1.Language = "go"
	m2 := makeEpisodic()
	m2.Language = "python"
	require.NoError(t, s.WriteEpisodic(ctx, m1))
	require.NoError(t, s.WriteEpisodic(ctx, m2))

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		Language: "python",
		Limit:    10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, m2.ID, results[0].ID)
}

func TestMemoryStore_SearchEpisodic_MinWeight(t *testing.T) {
	s := memory.NewStore()
	m1 := makeEpisodic()
	m1.Weight = 0.1
	m2 := makeEpisodic()
	m2.Weight = 0.9
	require.NoError(t, s.WriteEpisodic(ctx, m1))
	require.NoError(t, s.WriteEpisodic(ctx, m2))

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		MinWeight: 0.5,
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, m2.ID, results[0].ID)
}

func TestMemoryStore_SearchEpisodic_ExcludeObsolete(t *testing.T) {
	s := memory.NewStore()
	m1 := makeEpisodic()
	m2 := makeEpisodic()
	require.NoError(t, s.WriteEpisodic(ctx, m1))
	require.NoError(t, s.WriteEpisodic(ctx, m2))

	now := time.Now().UTC()
	require.NoError(t, s.MarkObsolete(ctx, m2.ID, now))

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, m1.ID, results[0].ID)
}

func TestMemoryStore_SearchEpisodic_IncludeObsolete(t *testing.T) {
	s := memory.NewStore()
	m1 := makeEpisodic()
	m2 := makeEpisodic()
	require.NoError(t, s.WriteEpisodic(ctx, m1))
	require.NoError(t, s.WriteEpisodic(ctx, m2))
	require.NoError(t, s.MarkObsolete(ctx, m2.ID, time.Now().UTC()))

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		IncludeObsolete: true,
		Limit:           10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestMemoryStore_SearchEpisodic_WithEmbedding(t *testing.T) {
	s := memory.NewStore()
	m1 := makeEpisodic()
	m1.ContextVector = []float32{0.2, 0.4, 0.6}
	m2 := makeEpisodic()
	m2.ContextVector = []float32{0.1, 0.2, 0.3}
	require.NoError(t, s.WriteEpisodic(ctx, m1))
	require.NoError(t, s.WriteEpisodic(ctx, m2))

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		Embedding: []float64{0.15, 0.3, 0.45},
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	// Both have proportional vectors → cosine similarity ≈ 1.0 for both.
	ids := make(map[uuid.UUID]bool)
	for _, r := range results {
		ids[r.ID] = true
	}
	assert.True(t, ids[m1.ID], "expected m1 in results")
	assert.True(t, ids[m2.ID], "expected m2 in results")
}

func TestMemoryStore_TouchEpisodic(t *testing.T) {
	s := memory.NewStore()
	mem := makeEpisodic()
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	now := time.Now().UTC()
	require.NoError(t, s.TouchEpisodic(ctx, mem.ID, now))

	got, _ := s.GetEpisodic(ctx, mem.ID)
	assert.True(t, got.LastAccessedAt.Equal(now))
}

func TestMemoryStore_TouchEpisodic_NotFound(t *testing.T) {
	s := memory.NewStore()
	err := s.TouchEpisodic(ctx, uuid.New(), time.Now().UTC())
	assert.Error(t, err)
}

func TestMemoryStore_UpdateEpisodicWeight(t *testing.T) {
	s := memory.NewStore()
	mem := makeEpisodic()
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	err := s.UpdateEpisodicWeight(ctx, mem.ID, 0.5, 3.0)
	require.NoError(t, err)

	got, _ := s.GetEpisodic(ctx, mem.ID)
	assert.InDelta(t, 0.5, got.Weight, 1e-9)
	assert.InDelta(t, 3.0, got.EffectiveFrequency, 1e-9)
}

func TestMemoryStore_MarkObsolete(t *testing.T) {
	s := memory.NewStore()
	mem := makeEpisodic()
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	now := time.Now().UTC()
	require.NoError(t, s.MarkObsolete(ctx, mem.ID, now))

	got, _ := s.GetEpisodic(ctx, mem.ID)
	assert.True(t, got.ObsoletedAt.Equal(now))
}

// ── Semantic Memory Tests ──────────────────────────────────────────────

func TestMemoryStore_WriteGetSemantic(t *testing.T) {
	s := memory.NewStore()
	mem := makeSemantic()
	err := s.WriteSemantic(ctx, mem)
	require.NoError(t, err)

	got, err := s.GetSemantic(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, mem.ID, got.ID)
	assert.Equal(t, mem.Content, got.Content)
}

func TestMemoryStore_GetSemantic_NotFound(t *testing.T) {
	s := memory.NewStore()
	_, err := s.GetSemantic(ctx, uuid.New())
	assert.Error(t, err)
}

func TestMemoryStore_SearchSemantic_ByType(t *testing.T) {
	s := memory.NewStore()
	m1 := makeSemantic()
	m1.Type = models.MemoryTypeRule
	m2 := makeSemantic()
	m2.Type = models.MemoryTypeFact
	require.NoError(t, s.WriteSemantic(ctx, m1))
	require.NoError(t, s.WriteSemantic(ctx, m2))

	results, err := s.SearchSemantic(ctx, store.SemanticSearchRequest{
		MemoryType: models.MemoryTypeRule,
		Limit:      10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, m1.ID, results[0].ID)
}

func TestMemoryStore_SearchSemantic_ByProject(t *testing.T) {
	s := memory.NewStore()
	m1 := makeSemantic()
	m1.EntityGroup = "p1"
	m2 := makeSemantic()
	m2.EntityGroup = "p2"
	require.NoError(t, s.WriteSemantic(ctx, m1))
	require.NoError(t, s.WriteSemantic(ctx, m2))

	results, err := s.SearchSemantic(ctx, store.SemanticSearchRequest{
		ProjectID: "p1",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, m1.ID, results[0].ID)
}

// ── Edge Tests ─────────────────────────────────────────────────────────

func TestMemoryStore_CreateGetEdges(t *testing.T) {
	s := memory.NewStore()
	id1 := uuid.New()
	id2 := uuid.New()
	err := s.CreateEdge(ctx, id1, id2, "RELATES_TO", map[string]any{"weight": 0.8})
	require.NoError(t, err)

	edges, err := s.GetEdges(ctx, id1, "", "")
	require.NoError(t, err)
	assert.Len(t, edges, 1)
	assert.Equal(t, "RELATES_TO", edges[0].EdgeType)
	assert.Equal(t, 0.8, edges[0].Properties["weight"])
}

func TestMemoryStore_GetEdges_DirectionOut(t *testing.T) {
	s := memory.NewStore()
	id1 := uuid.New()
	id2 := uuid.New()
	require.NoError(t, s.CreateEdge(ctx, id1, id2, "RELATES_TO", nil))

	edges, err := s.GetEdges(ctx, id1, "RELATES_TO", "out")
	require.NoError(t, err)
	assert.Len(t, edges, 1)

	edges, err = s.GetEdges(ctx, id2, "RELATES_TO", "out")
	require.NoError(t, err)
	assert.Empty(t, edges)
}

func TestMemoryStore_GetEdges_DirectionIn(t *testing.T) {
	s := memory.NewStore()
	id1 := uuid.New()
	id2 := uuid.New()
	require.NoError(t, s.CreateEdge(ctx, id1, id2, "DEPENDS_ON", nil))

	edges, err := s.GetEdges(ctx, id2, "DEPENDS_ON", "in")
	require.NoError(t, err)
	assert.Len(t, edges, 1)

	edges, err = s.GetEdges(ctx, id1, "DEPENDS_ON", "in")
	require.NoError(t, err)
	assert.Empty(t, edges)
}

func TestMemoryStore_GetEdges_TypeFilter(t *testing.T) {
	s := memory.NewStore()
	id1 := uuid.New()
	id2 := uuid.New()
	id3 := uuid.New()
	require.NoError(t, s.CreateEdge(ctx, id1, id2, "TYPE_A", nil))
	require.NoError(t, s.CreateEdge(ctx, id1, id3, "TYPE_B", nil))

	edges, err := s.GetEdges(ctx, id1, "TYPE_A", "")
	require.NoError(t, err)
	assert.Len(t, edges, 1)
	assert.Equal(t, "TYPE_A", edges[0].EdgeType)
}

// ── Pitfall Memory Tests ──────────────────────────────────────────────

func TestMemoryStore_WriteGetPitfall(t *testing.T) {
	s := memory.NewStore()
	p := makePitfall()
	err := s.WritePitfall(ctx, p)
	require.NoError(t, err)

	got, err := s.GetPitfall(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)
	assert.Equal(t, p.Signature, got.Signature)
}

func TestMemoryStore_GetPitfall_NotFound(t *testing.T) {
	s := memory.NewStore()
	_, err := s.GetPitfall(ctx, uuid.New())
	assert.Error(t, err)
	assert.ErrorIs(t, err, models.ErrPitfallNotFound)
}

func TestMemoryStore_SearchPitfall_ByEntityID(t *testing.T) {
	s := memory.NewStore()
	p1 := makePitfall()
	p1.EntityID = "func:A"
	p2 := makePitfall()
	p2.EntityID = "func:B"
	require.NoError(t, s.WritePitfall(ctx, p1))
	require.NoError(t, s.WritePitfall(ctx, p2))

	results, err := s.SearchPitfall(ctx, store.PitfallSearchRequest{
		EntityID: "func:A",
		Limit:    10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, p1.ID, results[0].ID)
}

func TestMemoryStore_SearchPitfall_ByProject(t *testing.T) {
	s := memory.NewStore()
	p1 := makePitfall()
	p1.ProjectID = "proj-x"
	p2 := makePitfall()
	p2.ProjectID = "proj-y"
	require.NoError(t, s.WritePitfall(ctx, p1))
	require.NoError(t, s.WritePitfall(ctx, p2))

	results, err := s.SearchPitfall(ctx, store.PitfallSearchRequest{
		ProjectID: "proj-x",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, p1.ID, results[0].ID)
}

func TestMemoryStore_SearchPitfall_ByRootCause(t *testing.T) {
	s := memory.NewStore()
	p1 := makePitfall()
	p1.RootCauseCategory = models.RootCauseConcurrency
	p2 := makePitfall()
	p2.RootCauseCategory = models.RootCauseLogicError
	require.NoError(t, s.WritePitfall(ctx, p1))
	require.NoError(t, s.WritePitfall(ctx, p2))

	results, err := s.SearchPitfall(ctx, store.PitfallSearchRequest{
		RootCauseCategory: models.RootCauseConcurrency,
		Limit:             10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, p1.ID, results[0].ID)
}

func TestMemoryStore_SearchPitfall_MinWeight(t *testing.T) {
	s := memory.NewStore()
	p1 := makePitfall()
	p1.Weight = 0.3
	p2 := makePitfall()
	p2.Weight = 0.9
	require.NoError(t, s.WritePitfall(ctx, p1))
	require.NoError(t, s.WritePitfall(ctx, p2))

	results, err := s.SearchPitfall(ctx, store.PitfallSearchRequest{
		MinWeight: 0.5,
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, p2.ID, results[0].ID)
}

func TestMemoryStore_SearchPitfallByEntity(t *testing.T) {
	s := memory.NewStore()
	p1 := makePitfall()
	p1.EntityID = "func:DoThing"
	p1.ProjectID = "proj-a"
	p2 := makePitfall()
	p2.EntityID = "func:DoThing"
	p2.ProjectID = "proj-b"
	require.NoError(t, s.WritePitfall(ctx, p1))
	require.NoError(t, s.WritePitfall(ctx, p2))

	results, err := s.SearchPitfallByEntity(ctx, "func:DoThing", "proj-a")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, p1.ID, results[0].ID)
}

func TestMemoryStore_TouchPitfall(t *testing.T) {
	s := memory.NewStore()
	p := makePitfall()
	p.OccurrenceCount = 0
	require.NoError(t, s.WritePitfall(ctx, p))

	now := time.Now().UTC()
	require.NoError(t, s.TouchPitfall(ctx, p.ID, now))

	got, _ := s.GetPitfall(ctx, p.ID)
	assert.Equal(t, 1, got.OccurrenceCount)
	assert.True(t, got.LastOccurredAt.Equal(now))
}

func TestMemoryStore_UpdatePitfallWeight(t *testing.T) {
	s := memory.NewStore()
	p := makePitfall()
	require.NoError(t, s.WritePitfall(ctx, p))

	err := s.UpdatePitfallWeight(ctx, p.ID, 0.25)
	require.NoError(t, err)

	got, _ := s.GetPitfall(ctx, p.ID)
	assert.InDelta(t, 0.25, got.Weight, 1e-9)
}

func TestMemoryStore_MarkPitfallObsolete(t *testing.T) {
	s := memory.NewStore()
	p := makePitfall()
	require.NoError(t, s.WritePitfall(ctx, p))

	now := time.Now().UTC()
	require.NoError(t, s.MarkPitfallObsolete(ctx, p.ID, now))

	got, _ := s.GetPitfall(ctx, p.ID)
	assert.True(t, got.ObsoletedAt.Equal(now))
}

// ── Consolidation Tests ───────────────────────────────────────────────

func TestMemoryStore_ScanRecent(t *testing.T) {
	s := memory.NewStore()
	old := makeEpisodic()
	old.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	new := makeEpisodic()
	new.CreatedAt = time.Now().UTC()
	require.NoError(t, s.WriteEpisodic(ctx, old))
	require.NoError(t, s.WriteEpisodic(ctx, new))

	since := time.Now().UTC().Add(-1 * time.Hour)
	items, err := s.ScanRecent(ctx, since, 100)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, new.ID, items[0].ID)
}

func TestMemoryStore_ScanRecent_SkipObsolete(t *testing.T) {
	s := memory.NewStore()
	mem := makeEpisodic()
	mem.CreatedAt = time.Now().UTC()
	require.NoError(t, s.WriteEpisodic(ctx, mem))
	require.NoError(t, s.MarkObsolete(ctx, mem.ID, time.Now().UTC()))

	since := time.Now().UTC().Add(-1 * time.Hour)
	items, err := s.ScanRecent(ctx, since, 100)
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestMemoryStore_ScanRecent_Limit(t *testing.T) {
	s := memory.NewStore()
	for i := 0; i < 5; i++ {
		m := makeEpisodic()
		m.CreatedAt = time.Now().UTC()
		require.NoError(t, s.WriteEpisodic(ctx, m))
	}

	since := time.Now().UTC().Add(-1 * time.Hour)
	items, err := s.ScanRecent(ctx, since, 3)
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestMemoryStore_SaveGetConsolidationRun(t *testing.T) {
	s := memory.NewStore()
	run := makeConsolidationRun()
	err := s.SaveConsolidationRun(ctx, run)
	require.NoError(t, err)

	got, err := s.GetConsolidationRun(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, run.RunID, got.RunID)
	assert.Equal(t, 2, got.RulesPersisted)
}

func TestMemoryStore_GetConsolidationRun_NotFound(t *testing.T) {
	s := memory.NewStore()
	_, err := s.GetConsolidationRun(ctx, uuid.New())
	assert.Error(t, err)
}

// ── Semantic Weight & Lifecycle Tests ──────────────────────────────────

func TestMemoryStore_UpdateSemanticWeight(t *testing.T) {
	s := memory.NewStore()
	mem := makeSemantic()
	require.NoError(t, s.WriteSemantic(ctx, mem))

	err := s.UpdateSemanticWeight(ctx, mem.ID, 0.75, 2.0)
	require.NoError(t, err)

	got, _ := s.GetSemantic(ctx, mem.ID)
	assert.InDelta(t, 0.75, got.Weight, 1e-9)
	assert.InDelta(t, 2.0, got.EffectiveFrequency, 1e-9)
}

func TestMemoryStore_UpdateSemanticWeight_NotFound(t *testing.T) {
	s := memory.NewStore()
	err := s.UpdateSemanticWeight(ctx, uuid.New(), 0.5, 1.0)
	assert.Error(t, err)
}

func TestMemoryStore_HealthCheck(t *testing.T) {
	s := memory.NewStore()
	assert.NoError(t, s.HealthCheck(ctx))
}

func TestMemoryStore_Close(t *testing.T) {
	s := memory.NewStore()
	assert.NoError(t, s.Close())
}

// ── Concurrent Write Test ──────────────────────────────────────────────

func TestMemoryStore_ConcurrentWrites(t *testing.T) {
	s := memory.NewStore()
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			mem := makeEpisodic()
			_ = s.WriteEpisodic(ctx, mem)
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{Limit: 100})
	require.NoError(t, err)
	assert.Len(t, results, 50)
}

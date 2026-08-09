package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := NewStore(config.SQLiteConfig{Path: path, WAL: true})
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func makeEpisodic(projectID, lang, taskType, summary string) *models.EpisodicMemory {
	now := time.Now().UTC()
	return &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  projectID,
		Language:   lang,
		TaskType:   models.TaskType(taskType),
		Summary:    summary,
		SourceType: models.SourceTypeUSER,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  now,
	}
}

func TestSQLite_Schema_AutoCreate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	assert.NoError(t, s.HealthCheck(ctx))

	// Verify tables exist
	var count int
	err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='episodic_memories'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='semantic_memories'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='memory_edges'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='consolidation_runs'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestSQLite_WriteEpisodic_Read(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := makeEpisodic("proj-a", "go", "debug", "nil pointer in handler")
	mem.ContextVector = []float32{0.1, 0.2, 0.3}
	err := s.WriteEpisodic(ctx, mem)
	require.NoError(t, err)

	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)

	assert.Equal(t, mem.ID, got.ID)
	assert.Equal(t, "proj-a", got.ProjectID)
	assert.Equal(t, "go", got.Language)
	assert.Equal(t, models.TaskType("debug"), got.TaskType)
	assert.Equal(t, "nil pointer in handler", got.Summary)
	assert.InDelta(t, 1.0, got.Weight, 1e-9)
	assert.Len(t, got.ContextVector, 3)
	assert.InDelta(t, 0.1, got.ContextVector[0], 1e-5)
}

func TestSQLite_WriteEpisodic_NoVector(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := makeEpisodic("proj-b", "ts", "feature", "add login")
	err := s.WriteEpisodic(ctx, mem)
	require.NoError(t, err)

	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ContextVector)
}

func TestSQLite_SearchEpisodic_ByProjectLanguage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.WriteEpisodic(ctx, makeEpisodic("proj-a", "go", "debug", "fix nil pointer"))
	require.NoError(t, err)
	err = s.WriteEpisodic(ctx, makeEpisodic("proj-a", "go", "feature", "add cache"))
	require.NoError(t, err)
	err = s.WriteEpisodic(ctx, makeEpisodic("proj-b", "py", "debug", "fix timeout"))
	require.NoError(t, err)

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "proj-a",
		Language:  "go",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 2)
	for _, r := range results {
		assert.Equal(t, "proj-a", r.ProjectID)
		assert.Equal(t, "go", r.Language)
	}
}

func TestSQLite_SearchEpisodic_WithEmbedding(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Write 3 memories with known embeddings
	m1 := makeEpisodic("p", "go", "debug", "redis pool exhausted")
	m1.ContextVector = []float32{1, 0, 0}
	require.NoError(t, s.WriteEpisodic(ctx, m1))

	m2 := makeEpisodic("p", "go", "debug", "http timeout error")
	m2.ContextVector = []float32{0, 1, 0}
	require.NoError(t, s.WriteEpisodic(ctx, m2))

	m3 := makeEpisodic("p", "go", "debug", "memory leak in loop")
	m3.ContextVector = []float32{1, 0.1, 0}
	require.NoError(t, s.WriteEpisodic(ctx, m3))

	// Query embedding is closer to m1 and m3
	queryEmb := []float64{1, 0, 0}
	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Language:  "go",
		Embedding: queryEmb,
		Limit:     3,
	})
	require.NoError(t, err)
	assert.Len(t, results, 3)
	// m1 should be top (cos=1.0), m3 next (cos=0.995), m2 last (cos=0.0)
	assert.Equal(t, m1.ID, results[0].ID)
	assert.Equal(t, m3.ID, results[1].ID)
	assert.Equal(t, m2.ID, results[2].ID)
}

func TestSQLite_SearchEpisodic_EmbeddingPoolIncludesAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 550 near-uniform memories + 1 distinctive fact, all with the same weight
	// — the old SQL weight pre-limit (pool 500) could skip the distinctive row
	// entirely, pinning pinpoint-fact recall. The embedding pool must rank the
	// full project candidate set.
	const distractors = 550
	for i := 0; i < distractors; i++ {
		m := makeEpisodic("p", "go", "debug", fmt.Sprintf("distractor %d", i))
		m.ContextVector = []float32{0, 1, 0}
		require.NoError(t, s.WriteEpisodic(ctx, m))
	}
	fact := makeEpisodic("p", "go", "debug", "the golden banana pendant")
	fact.ContextVector = []float32{1, 0, 0}
	require.NoError(t, s.WriteEpisodic(ctx, fact))

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Embedding: []float64{1, 0, 0},
		Limit:     10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, fact.ID, results[0].ID,
		"distinctive memory must rank top even beyond the old 500-pool")
}

func TestSQLite_SearchEpisodic_NoEmbedding(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m1 := makeEpisodic("p", "go", "debug", "low weight")
	m1.Weight = 0.3
	require.NoError(t, s.WriteEpisodic(ctx, m1))

	m2 := makeEpisodic("p", "go", "debug", "high weight")
	m2.Weight = 0.9
	require.NoError(t, s.WriteEpisodic(ctx, m2))

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, m2.ID, results[0].ID) // higher weight first
}

func TestSQLite_TouchEpisodic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := makeEpisodic("p", "go", "debug", "test")
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	time.Sleep(time.Millisecond)
	now := time.Now().UTC()
	require.NoError(t, s.TouchEpisodic(ctx, mem.ID, now))

	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	assert.True(t, !got.LastAccessedAt.Before(now.Add(-time.Second)))
}

func TestSQLite_UpdateEpisodicWeight(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := makeEpisodic("p", "go", "debug", "test")
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	require.NoError(t, s.UpdateEpisodicWeight(ctx, mem.ID, 0.5, 2.0))

	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	assert.InDelta(t, 0.5, got.Weight, 1e-9)
	assert.InDelta(t, 2.0, got.EffectiveFrequency, 1e-9)
}

func TestSQLite_MarkObsolete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := makeEpisodic("p", "go", "debug", "test")
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	now := time.Now().UTC()
	require.NoError(t, s.MarkObsolete(ctx, mem.ID, now))

	// Verify the memory is actually marked obsolete in DB
	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ObsoletedAt)

	// Search should not include it by default
	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 0)

	// Unless we explicitly include obsolete
	results, err = s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:       "p",
		Limit:           10,
		IncludeObsolete: true,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestSQLite_DeleteEpisodicByProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Two memories in project "p", one in a different project.
	require.NoError(t, s.WriteEpisodic(ctx, makeEpisodic("p", "go", "debug", "one")))
	require.NoError(t, s.WriteEpisodic(ctx, makeEpisodic("p", "go", "debug", "two")))
	require.NoError(t, s.WriteEpisodic(ctx, makeEpisodic("other", "go", "debug", "keep")))

	n, err := s.DeleteEpisodicByProject(ctx, "p")
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// Remaining project is untouched; "p" is empty.
	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 0)

	results, err = s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "other",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)

	// Deleting a project with no memories returns 0, not an error.
	n, err = s.DeleteEpisodicByProject(ctx, "p")
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestSQLite_DeleteEpisodicByProject_InvalidatesHotCache(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.WriteEpisodic(ctx, makeEpisodic("p", "go", "debug", "one")))

	// A non-embedding search populates the hot cache for project "p".
	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{ProjectID: "p", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, results, 1)

	// Delete must purge the cache so the same search cannot serve the deleted row.
	n, err := s.DeleteEpisodicByProject(ctx, "p")
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	results, err = s.SearchEpisodic(ctx, store.EpisodicSearchRequest{ProjectID: "p", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestSQLite_WriteSemantic_Search(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := &models.SemanticMemory{
		ID:         uuid.New(),
		Type:       models.MemoryTypeRule,
		Content:    "always close response bodies",
		SourceType: models.SourceTypeINFERRED,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, s.WriteSemantic(ctx, mem))

	results, err := s.SearchSemantic(ctx, store.SemanticSearchRequest{
		MemoryType: models.MemoryTypeRule,
		Limit:      10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "always close response bodies", results[0].Content)
}

func TestSQLite_CreateEdge_GetEdges(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id1 := uuid.New()
	id2 := uuid.New()

	err := s.CreateEdge(ctx, id1, id2, "RELATES_TO", map[string]any{
		"strength":  0.8,
		"dimension": "SEMANTIC",
	})
	require.NoError(t, err)

	edges, err := s.GetEdges(ctx, id1, "RELATES_TO", "out")
	require.NoError(t, err)
	assert.Len(t, edges, 1)
	assert.Equal(t, id1, edges[0].FromID)
	assert.Equal(t, id2, edges[0].ToID)
	assert.Equal(t, "RELATES_TO", edges[0].EdgeType)
	assert.InDelta(t, 0.8, edges[0].Properties["strength"].(float64), 1e-9)
}

func TestSQLite_GetEdges_BothDirections(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id1 := uuid.New()
	id2 := uuid.New()

	require.NoError(t, s.CreateEdge(ctx, id1, id2, "RELATES_TO", nil))
	require.NoError(t, s.CreateEdge(ctx, id2, id1, "DERIVED_FROM", nil))

	// Both directions from id1
	edges, err := s.GetEdges(ctx, id1, "", "")
	require.NoError(t, err)
	assert.Len(t, edges, 2)
}

func TestSQLite_ScanRecent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	past := time.Now().UTC().Add(-2 * time.Hour)

	old := makeEpisodic("p", "go", "debug", "old memory")
	old.CreatedAt = past.Add(-1 * time.Hour)
	require.NoError(t, s.WriteEpisodic(ctx, old))

	recent := makeEpisodic("p", "go", "debug", "recent memory")
	recent.CreatedAt = past.Add(30 * time.Minute)
	require.NoError(t, s.WriteEpisodic(ctx, recent))

	items, err := s.ScanRecent(ctx, past, 10)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, recent.ID, items[0].ID)
	assert.Equal(t, "recent memory", items[0].Summary)
}

func TestSQLite_SaveGetConsolidationRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	run := &models.ConsolidationRunResult{
		RunID:              uuid.New(),
		StartedAt:          now,
		EpisodicsScanned:   50,
		CandidateRulesFound: 5,
		RulesPersisted:     3,
		AverageAccuracy:    0.85,
	}
	require.NoError(t, s.SaveConsolidationRun(ctx, run))

	got, err := s.GetConsolidationRun(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, run.RunID, got.RunID)
	assert.Equal(t, 50, got.EpisodicsScanned)
	assert.Equal(t, 5, got.CandidateRulesFound)
	assert.Equal(t, 3, got.RulesPersisted)
	assert.InDelta(t, 0.85, got.AverageAccuracy, 1e-9)
}

func TestSQLite_EndToEnd_WriteSearchDecay(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Write 5 episodics with known embeddings
	embeddings := [][]float32{
		{1, 0, 0},
		{0.9, 0.1, 0},
		{0, 1, 0},
		{0, 0, 1},
		{0.5, 0.5, 0},
	}
	var ids []uuid.UUID
	for i, emb := range embeddings {
		mem := makeEpisodic("e2e", "go", "debug", "memory "+string(rune('A'+i)))
		mem.ContextVector = emb
		require.NoError(t, s.WriteEpisodic(ctx, mem))
		ids = append(ids, mem.ID)
	}

	// Search with embedding close to first two
	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "e2e",
		Embedding: vector.Float32To64([]float32{1, 0.05, 0}),
		Limit:     3,
	})
	require.NoError(t, err)
	assert.Len(t, results, 3)
	// First should be ids[0] (perfect match)
	assert.Equal(t, ids[0], results[0].ID)

	// Decay the first one
	require.NoError(t, s.UpdateEpisodicWeight(ctx, ids[0], 0.001, 0))
	require.NoError(t, s.UpdateEpisodicWeight(ctx, ids[1], 1.0, 3.0))

	// Search without embedding - ids[1] should rank first by weight
	results, err = s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "e2e",
		Limit:     5,
	})
	require.NoError(t, err)
	assert.Equal(t, ids[1], results[0].ID)

	// Touch ids[0] to revive
	require.NoError(t, s.TouchEpisodic(ctx, ids[0], time.Now().UTC()))

	got, err := s.GetEpisodic(ctx, ids[0])
	require.NoError(t, err)
	assert.NotNil(t, got.LastAccessedAt)
}

func TestSQLite_ConcurrentWrites(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	done := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func(n int) {
			mem := makeEpisodic("concurrent", "go", "debug", "goroutine")
			_ = s.WriteEpisodic(ctx, mem)
			done <- true
		}(i)
	}
	for i := 0; i < 5; i++ {
		<-done
	}

	results, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "concurrent",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 5)
}

func TestSQLite_HotCache(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		mem := makeEpisodic("cache", "go", "debug", "memory")
		mem.Weight = float64(i) * 0.1
		require.NoError(t, s.WriteEpisodic(ctx, mem))
	}

	// First call should populate cache
	results1, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "cache",
		Limit:     10,
	})
	require.NoError(t, err)

	// Second call should hit cache (same non-embedding query)
	results2, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "cache",
		Limit:     10,
	})
	require.NoError(t, err)

	assert.Len(t, results1, 3)
	assert.Len(t, results2, 3)
}

// ── Pitfall Tests ─────────────────────────────────────────────────────

func makePitfall(entityID, projectID, sig string) *models.PitfallMemory {
	now := time.Now().UTC()
	return &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          entityID,
		EntityType:        models.EntityTypeFunction,
		ProjectID:         projectID,
		Language:          "go",
		Signature:         sig,
		RootCauseCategory: models.RootCauseLogicError,
		TrustLevel:        3,
		Weight:            0.8,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func TestSQLite_WritePitfall_Get(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := makePitfall("func:A", "proj-p", "nil deref in A")
	err := s.WritePitfall(ctx, p)
	require.NoError(t, err)

	got, err := s.GetPitfall(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)
	assert.Equal(t, p.EntityID, got.EntityID)
	assert.Equal(t, p.Signature, got.Signature)
	assert.Equal(t, p.Weight, got.Weight)
}

func TestSQLite_GetPitfall_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.GetPitfall(ctx, uuid.New())
	assert.Error(t, err)
}

func TestSQLite_SearchPitfall_ByEntityID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p1 := makePitfall("func:A", "proj", "sig1")
	p2 := makePitfall("func:B", "proj", "sig2")
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

func TestSQLite_SearchPitfall_ByProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p1 := makePitfall("func:A", "proj-x", "sig")
	p2 := makePitfall("func:B", "proj-y", "sig")
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

func TestSQLite_SearchPitfall_ByRootCause(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p1 := makePitfall("func:A", "proj", "sig1")
	p1.RootCauseCategory = models.RootCauseConcurrency
	p2 := makePitfall("func:B", "proj", "sig2")
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

func TestSQLite_SearchPitfall_MinWeight(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p1 := makePitfall("func:A", "proj", "s1")
	p1.Weight = 0.2
	p2 := makePitfall("func:B", "proj", "s2")
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

func TestSQLite_SearchPitfallByEntity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p1 := makePitfall("func:DoThing", "proj-a", "sig1")
	p2 := makePitfall("func:DoThing", "proj-b", "sig2")
	require.NoError(t, s.WritePitfall(ctx, p1))
	require.NoError(t, s.WritePitfall(ctx, p2))

	results, err := s.SearchPitfallByEntity(ctx, "func:DoThing", "proj-a")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, p1.ID, results[0].ID)
}

func TestSQLite_SearchPitfallByEntity_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	results, err := s.SearchPitfallByEntity(ctx, "func:Nonexistent", "proj")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSQLite_TouchPitfall(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := makePitfall("func:A", "proj", "sig")
	require.NoError(t, s.WritePitfall(ctx, p))

	now := time.Now().UTC()
	require.NoError(t, s.TouchPitfall(ctx, p.ID, now))

	got, err := s.GetPitfall(ctx, p.ID)
	require.NoError(t, err)
	assert.True(t, got.LastOccurredAt.Truncate(time.Second).Equal(now.Truncate(time.Second)))
}

func TestSQLite_UpdatePitfallWeight(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := makePitfall("func:A", "proj", "sig")
	require.NoError(t, s.WritePitfall(ctx, p))

	err := s.UpdatePitfallWeight(ctx, p.ID, 0.25)
	require.NoError(t, err)

	got, err := s.GetPitfall(ctx, p.ID)
	require.NoError(t, err)
	assert.InDelta(t, 0.25, got.Weight, 1e-9)
}

func TestSQLite_MarkPitfallObsolete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := makePitfall("func:A", "proj", "sig")
	require.NoError(t, s.WritePitfall(ctx, p))

	now := time.Now().UTC()
	require.NoError(t, s.MarkPitfallObsolete(ctx, p.ID, now))

	got, err := s.GetPitfall(ctx, p.ID)
	require.NoError(t, err)
	assert.True(t, got.ObsoletedAt.Truncate(time.Second).Equal(now.Truncate(time.Second)))
}

func TestSQLite_UpdateSemanticWeight(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := &models.SemanticMemory{
		ID:                 uuid.New(),
		Type:               models.MemoryTypeRule,
		Content:            "test rule",
		Weight:             1.0,
		EffectiveFrequency: 1.0,
		CreatedAt:          time.Now().UTC(),
	}
	require.NoError(t, s.WriteSemantic(ctx, mem))

	err := s.UpdateSemanticWeight(ctx, mem.ID, 0.75, 2.0)
	require.NoError(t, err)

	got, err := s.GetSemantic(ctx, mem.ID)
	require.NoError(t, err)
	assert.InDelta(t, 0.75, got.Weight, 1e-9)
	assert.InDelta(t, 2.0, got.EffectiveFrequency, 1e-9)
}

func TestSQLite_WritePitfall_WithSourceEpisodicIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	srcID1 := uuid.New()
	srcID2 := uuid.New()
	p := makePitfall("func:A", "proj", "sig")
	p.SourceEpisodicIDs = []uuid.UUID{srcID1, srcID2}
	require.NoError(t, s.WritePitfall(ctx, p))

	got, err := s.GetPitfall(ctx, p.ID)
	require.NoError(t, err)
	assert.Len(t, got.SourceEpisodicIDs, 2)
	assert.Contains(t, got.SourceEpisodicIDs, srcID1)
	assert.Contains(t, got.SourceEpisodicIDs, srcID2)
}

func TestSQLite_GetEdges_EmptyResult(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	edges, err := s.GetEdges(ctx, uuid.New(), "", "out")
	require.NoError(t, err)
	assert.Empty(t, edges)
}

func TestSQLite_GetConsolidationRun_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, err := s.GetConsolidationRun(ctx, uuid.New())
	assert.Error(t, err)
}

func TestSQLite_SearchPitfall_WithEmbedding(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := makePitfall("func:A", "proj", "sig")
	require.NoError(t, s.WritePitfall(ctx, p))

	results, err := s.SearchPitfall(ctx, store.PitfallSearchRequest{
		EntityID:  "func:A",
		Embedding: []float64{0.1, 0.2, 0.3},
		Limit:     10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestSQLite_NewStore_NoWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-wal.db")
	s, err := NewStore(config.SQLiteConfig{Path: path, WAL: false})
	require.NoError(t, err)
	defer s.Close()
	assert.NoError(t, s.HealthCheck(context.Background()))
}

func TestSQLite_NewStore_BadPath(t *testing.T) {
	_, err := NewStore(config.SQLiteConfig{Path: "/nonexistent/dir/db.sqlite"})
	assert.Error(t, err)
}

func TestSQLite_GetConsolidationRun_Completed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	run := &models.ConsolidationRunResult{
		RunID:             uuid.New(),
		StartedAt:         now,
		CompletedAt:       &now,
		EpisodicsScanned:  50,
		RulesPersisted:    3,
		AverageAccuracy:   0.9,
		PitfallsExtracted: 1,
	}
	require.NoError(t, s.SaveConsolidationRun(ctx, run))

	got, err := s.GetConsolidationRun(ctx, run.RunID)
	require.NoError(t, err)
	assert.NotNil(t, got.CompletedAt)
	assert.Equal(t, 50, got.EpisodicsScanned)
}

func TestSQLite_SearchPitfall_WithEmbeddingNoEntity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := makePitfall("func:B", "proj", "sigB")
	require.NoError(t, s.WritePitfall(ctx, p))

	results, err := s.SearchPitfall(ctx, store.PitfallSearchRequest{
		ProjectID: "proj",
		Embedding: []float64{0.1, 0.2},
		Limit:     10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestMain(m *testing.M) {
	// Ensure no stray test.db files
	os.Remove("test.db")
	code := m.Run()
	os.Remove("test.db")
	os.Exit(code)
}

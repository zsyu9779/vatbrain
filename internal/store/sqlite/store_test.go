package sqlite

import (
	"context"
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

func TestMain(m *testing.M) {
	// Ensure no stray test.db files
	os.Remove("test.db")
	code := m.Run()
	os.Remove("test.db")
	os.Exit(code)
}

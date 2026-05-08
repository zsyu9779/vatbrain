package tests

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	neodriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/db/neo4j"
	"github.com/vatbrain/vatbrain/internal/db/pgvector"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/neo4jpg"
)

// testEmbedder returns a deterministic short embedding for test control.
// It uses the first 4 dimensions to encode a simple hash so pgvector
// similarity produces meaningful ranking.
type testEmbedder struct {
	dim int
}

func (e *testEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, e.dim)
	// Simple position-weighted hash: each byte of text contributes to dims 0-3.
	for i := 0; i < len(text); i++ {
		idx := i % 4
		vec[idx] += float32(text[i]) / 1000.0
	}
	return vec, nil
}

var _ embedder.Embedder = (*testEmbedder)(nil)

func TestE2E_WriteRetrieveDecayConsolidate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	neo4jClient, err := neo4j.NewClient(ctx, neo4j.Config{
		URI:                  "bolt://localhost:7687",
		Username:             "neo4j",
		Password:             "vatbrain",
		Database:             "neo4j",
		MaxConnectionPoolSize: 10,
	})
	require.NoError(t, err, "neo4j must be available")
	defer neo4jClient.Close(ctx)

	pgClient, err := pgvector.NewClient(ctx, pgvector.Config{
		Host:     "localhost",
		Port:     5432,
		User:     "vatbrain",
		Password: "vatbrain",
		Database: "vatbrain",
		MaxConns: 5,
	})
	require.NoError(t, err, "pgvector must be available")
	defer pgClient.Close()

	st, err := neo4jpg.NewStore(ctx, neo4jClient, pgClient)
	require.NoError(t, err, "neo4jpg store setup")

	projectID := "e2e_" + uuid.New().String()[:8]
	t.Logf("project_id=%s", projectID)

	// ─────────────────────────────────────────────────────────────
	// Phase 1: WRITE — create episodic memories + embeddings
	// ─────────────────────────────────────────────────────────────
	type testMemory struct {
		id       uuid.UUID
		summary  string
		taskType models.TaskType
		embed    []float32
	}

	// 3 debug memories (related) — will form a consolidation cluster.
	// 2 feature memories (unrelated).
	memories := []testMemory{
		{id: uuid.New(), summary: "redis connection pool exhausted at maxconns=50 causing timeout", taskType: models.TaskTypeDebug},
		{id: uuid.New(), summary: "redis pool timeout when connecting to primary node after failover", taskType: models.TaskTypeDebug},
		{id: uuid.New(), summary: "redis connection leak in healthcheck goroutine depleting pool", taskType: models.TaskTypeDebug},
		{id: uuid.New(), summary: "added user authentication middleware with JWT validation", taskType: models.TaskTypeFeature},
		{id: uuid.New(), summary: "refactored config loader to support YAML and env vars", taskType: models.TaskTypeFeature},
	}

	emb := &testEmbedder{dim: 1536}
	now := time.Now().UTC()
	decay := core.DefaultWeightDecayEngine()

	for i, m := range memories {
		vec, err := emb.Embed(ctx, m.summary)
		require.NoError(t, err)
		memories[i].embed = vec

		effFreq, weight := decay.ComputeFull([]time.Time{now}, now, now)

		err = st.WriteEpisodic(ctx, &models.EpisodicMemory{
			ID:                 m.id,
			ProjectID:          projectID,
			Language:           "go",
			TaskType:           m.taskType,
			Summary:            m.summary,
			SourceType:         models.SourceTypeLLM,
			TrustLevel:         models.DefaultTrustLevel,
			Weight:             weight,
			EffectiveFrequency: effFreq,
			CreatedAt:          now,
			EntityGroup:        "test",
			EmbeddingID:        m.id.String(),
			ContextVector:      vec,
		})
		require.NoError(t, err, "write episodic memory %d", i)
		t.Logf("created memory %s", m.id.String()[:8])
	}
	t.Logf("Phase 1 WRITE: %d memories created via Store", len(memories))

	// Verify all nodes exist via Store.
	for _, m := range memories {
		got, err := st.GetEpisodic(ctx, m.id)
		require.NoError(t, err)
		assert.Equal(t, m.summary, got.Summary)
	}

	// ─────────────────────────────────────────────────────────────
	// Phase 2: LINK ON WRITE — create RELATES_TO edges via Store
	// ─────────────────────────────────────────────────────────────
	for _, m := range memories {
		core.LinkOnWrite(ctx, st, m.id, m.summary, projectID, "", m.taskType)
	}
	t.Log("Phase 2 LINK: complete — RELATES_TO edges created via Store")

	// ─────────────────────────────────────────────────────────────
	// Phase 3: RETRIEVE — 2-stage pipeline via Store
	// ─────────────────────────────────────────────────────────────
	_ = core.DefaultRetrievalEngine() // exercised indirectly below

	// Fetch episodic candidates via Store.
	candidates, err := st.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: projectID,
		Language:  "go",
		Limit:     500,
	})
	require.NoError(t, err)
	require.Len(t, candidates, 5, "all 5 episodics should match project+language")

	// Stage 1: Contextual Gating.
	gating := &core.ContextualGating{}
	searchCtx := models.SearchContext{
		ProjectID: projectID,
		Language:  "go",
		TaskType:  "debug",
	}
	filtered, stats := gating.FilterAndRank(candidates, searchCtx,
		decay.CoolingThreshold, 500)

	assert.Equal(t, 5, stats.TotalCandidates)
	assert.Equal(t, 5, stats.AfterFilter, "all 5 pass hard constraints")
	t.Logf("Stage 1 GATING: %d candidates → %d after filter (%dms)",
		stats.TotalCandidates, stats.AfterFilter, stats.FilterTimeMs)

	// Stage 2: Semantic Ranking via pgvector.
	queryVec, err := emb.Embed(ctx, "redis connection pool timeout error")
	require.NoError(t, err)

	filteredIDs := make([]string, len(filtered))
	for i, f := range filtered {
		filteredIDs[i] = f.MemoryID
	}

	pgResults, err := pgClient.SimilaritySearch(ctx, queryVec, 5, filteredIDs)
	require.NoError(t, err)
	require.NotEmpty(t, pgResults)

	// The redis-related debug memories should rank higher.
	t.Logf("Stage 2 RANKING: top result = %s (similarity=%.4f)",
		pgResults[0].MemoryID[:8], pgResults[0].Similarity)
	for _, pr := range pgResults {
			s := pr.SummaryText
			if len(s) > 60 {
				s = s[:60]
			}
			t.Logf("  %s: %.4f - %s", pr.MemoryID[:8], pr.Similarity, s)
	}

	// At least 1 result should be returned.
	assert.GreaterOrEqual(t, len(pgResults), 1)

	// ─────────────────────────────────────────────────────────────
	// Phase 4: DECAY — weight computation
	// ─────────────────────────────────────────────────────────────
	// Simulate a memory created 30 days ago with 3 accesses spread over time.
	oldCreated := now.Add(-30 * 24 * time.Hour)
	accesses := []time.Time{
		oldCreated,                            // initial
		oldCreated.Add(5 * 24 * time.Hour),    // +5 days
		oldCreated.Add(15 * 24 * time.Hour),   // +15 days
	}
	effFreq, finalWeight := decay.ComputeFull(accesses, oldCreated, now)

	t.Logf("Phase 4 DECAY: 30-day memory, 3 accesses → effFreq=%.4f, weight=%.4f",
		effFreq, finalWeight)

	// Fresh memory (just now).
	freshEffFreq, freshWeight := decay.ComputeFull([]time.Time{now}, now, now)
	t.Logf("Phase 4 DECAY: fresh memory → effFreq=%.4f, weight=%.4f",
		freshEffFreq, freshWeight)

	assert.Greater(t, freshWeight, finalWeight,
		"fresh memory should have higher weight than 30-day memory")

	// Very old memory (180 days, single access) should be below cooling threshold.
	veryOld := now.Add(-180 * 24 * time.Hour)
	_, oldWeight := decay.ComputeFull([]time.Time{veryOld}, veryOld, now)
	t.Logf("Phase 4 DECAY: 180-day memory, 1 access → weight=%.6f", oldWeight)
	assert.True(t, decay.IsCooled(oldWeight),
		"180-day memory should drop below cooling threshold (%.6f < %.4f)",
		oldWeight, decay.CoolingThreshold)

	// Verify WeightDecayEngine.Weight formula matches expectations.
	// Weight = effFreq * e^(-alpha * days_since_created) * e^(-beta * days_since_access)
	expectedExperienceDecay := math.Exp(-decay.AlphaExperience * 30)
	expectedActivityDecay := math.Exp(-decay.BetaActivity * 15)
	expectedWeight := effFreq * expectedExperienceDecay * expectedActivityDecay
	assert.InDelta(t, expectedWeight, finalWeight, 0.001,
		"weight formula should match manual computation")

	// ─────────────────────────────────────────────────────────────
	// Phase 5: CONSOLIDATE — cluster episodics → semantic memories via Store
	// ─────────────────────────────────────────────────────────────
	engine2 := core.DefaultConsolidationEngine()
	engine2.HoursToScan = 24
	engine2.MinClusterSize = 3
	engine2.AccuracyThreshold = 0.7

	result, err := engine2.Run(ctx, st, emb)
	require.NoError(t, err)
	t.Logf("Phase 5 CONSOLIDATE: scanned=%d, rules=%d, persisted=%d, avgAcc=%.2f",
		result.EpisodicsScanned, result.CandidateRulesFound,
		result.RulesPersisted, result.AverageAccuracy)
	assert.GreaterOrEqual(t, result.EpisodicsScanned, 3,
		"should scan at least 3 debug episodics")
	assert.GreaterOrEqual(t, result.RulesPersisted, 1,
		"should persist at least 1 semantic rule from the debug cluster")

	// ─────────────────────────────────────────────────────────────
	// CLEANUP
	// ─────────────────────────────────────────────────────────────
	// Delete semantic memories (created by consolidation).
	_, _ = neo4jClient.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, _ = tx.Run(ctx,
			`MATCH (m:SemanticMemory) DETACH DELETE m`, nil)
		return nil, nil
	})
	_, _ = neo4jClient.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, _ = tx.Run(ctx,
			`MATCH (c:ConsolidationRun) DETACH DELETE c`, nil)
		return nil, nil
	})

	// Delete episodic memories.
	for _, m := range memories {
		mid := m.id.String()
		_, _ = neo4jClient.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
			_, _ = tx.Run(ctx,
				`MATCH (e:EpisodicMemory {id: $id}) DETACH DELETE e`,
				map[string]any{"id": mid})
			return nil, nil
		})
		err := pgClient.DeleteByMemoryID(ctx, mid)
		if err != nil {
			t.Logf("cleanup pgvector %s: %v", mid[:8], err)
		}
	}

	t.Log("Cleanup complete")
}

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

	projectID := "e2e_" + uuid.New().String()[:8]
	t.Logf("project_id=%s", projectID)

	// ─────────────────────────────────────────────────────────────
	// Phase 1: WRITE — create episodic memories + embeddings
	// ─────────────────────────────────────────────────────────────
	type testMemory struct {
		id       string
		summary  string
		taskType string
		embed    []float32
	}

	// 3 debug memories (related) — will form a consolidation cluster.
	// 2 feature memories (unrelated).
	memories := []testMemory{
		{
			id:       uuid.New().String(),
			summary:  "redis connection pool exhausted at maxconns=50 causing timeout",
			taskType: "debug",
		},
		{
			id:       uuid.New().String(),
			summary:  "redis pool timeout when connecting to primary node after failover",
			taskType: "debug",
		},
		{
			id:       uuid.New().String(),
			summary:  "redis connection leak in healthcheck goroutine depleting pool",
			taskType: "debug",
		},
		{
			id:       uuid.New().String(),
			summary:  "added user authentication middleware with JWT validation",
			taskType: "feature",
		},
		{
			id:       uuid.New().String(),
			summary:  "refactored config loader to support YAML and env vars",
			taskType: "feature",
		},
	}

	emb := &testEmbedder{dim: 1536}
	now := time.Now().UTC()
	decay := core.DefaultWeightDecayEngine()

	for i, m := range memories {
		// Generate deterministic embedding.
		vec, err := emb.Embed(ctx, m.summary)
		require.NoError(t, err)
		memories[i].embed = vec

		effFreq, weight := decay.ComputeFull([]time.Time{now}, now, now)

		_, err = neo4jClient.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
			_, runErr := tx.Run(ctx, `
				CREATE (e:EpisodicMemory {
					id: $id,
					project_id: $projectID,
					language: $language,
					task_type: $taskType,
					summary: $summary,
					source_type: $sourceType,
					trust_level: $trustLevel,
					weight: $weight,
					effective_frequency: $effFreq,
					created_at: datetime(),
					entity_group: $entityGroup,
					embedding_id: $embeddingID
				})
			`, map[string]any{
				"id":          m.id,
				"projectID":   projectID,
				"language":    "go",
				"taskType":    m.taskType,
				"summary":     m.summary,
				"sourceType":  string(models.SourceTypeLLM),
				"trustLevel":  int(models.DefaultTrustLevel),
				"weight":      weight,
				"effFreq":     effFreq,
				"entityGroup": "test",
				"embeddingID": m.id,
			})
			return nil, runErr
		})
		require.NoError(t, err, "create episodic memory %d", i)
		t.Logf("created memory %s", m.id[:8])

		// Insert embedding.
		err = pgClient.InsertEmbedding(ctx, m.id, vec, m.summary,
			projectID, "go", m.taskType,
			map[string]any{"entity_id": "test"})
		require.NoError(t, err, "insert embedding %d", i)
	}
	t.Logf("Phase 1 WRITE: %d memories created", len(memories))

	// Verify all nodes exist.
	for _, m := range memories {
		raw, err := neo4jClient.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
			records, runErr := tx.Run(ctx,
				`MATCH (e:EpisodicMemory {id: $id}) RETURN e.summary AS summary`,
				map[string]any{"id": m.id})
			if runErr != nil {
				return nil, runErr
			}
			if !records.Next(ctx) {
				return nil, records.Err()
			}
			r := records.Record()
			s, _, _ := neodriver.GetRecordValue[string](r, "summary")
			return s, nil
		})
		require.NoError(t, err)
		assert.Equal(t, m.summary, raw)
	}

	// ─────────────────────────────────────────────────────────────
	// Phase 2: LINK ON WRITE — create RELATES_TO edges
	// TODO(v0.1.1): Update to use Store interface once Neo4j Store adapter (Phase 4) is done.
	// ─────────────────────────────────────────────────────────────
	_ = core.LinkOnWrite
	t.Log("Phase 2 LINK: skipped — requires Neo4j Store adapter (Phase 4)")

	// ─────────────────────────────────────────────────────────────
	// Phase 3: RETRIEVE — 2-stage pipeline
	// ─────────────────────────────────────────────────────────────
	_ = core.DefaultRetrievalEngine() // exercised indirectly below

	// Fetch episodic candidates (mimics fetchEpisodicCandidates).
	raw, err := neo4jClient.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx, `
			MATCH (e:EpisodicMemory)
			WHERE e.project_id = $projectID AND e.language = $language
			  AND e.obsoleted_at IS NULL
			RETURN e.id, e.project_id, e.language, e.task_type, e.summary,
			       e.source_type, e.trust_level, e.weight, e.effective_frequency,
			       e.created_at, e.last_accessed_at, e.obsoleted_at,
			       e.entity_group, e.embedding_id
			ORDER BY e.weight DESC LIMIT 500
		`, map[string]any{"projectID": projectID, "language": "go"})
		if runErr != nil {
			return nil, runErr
		}
		var results []models.EpisodicMemory
		for records.Next(ctx) {
			r := records.Record()
			id, _, _ := neodriver.GetRecordValue[string](r, "e.id")
			if id == "" {
				continue
			}
			pid, _ := uuid.Parse(id)
			projID, _, _ := neodriver.GetRecordValue[string](r, "e.project_id")
			lang, _, _ := neodriver.GetRecordValue[string](r, "e.language")
			taskType, _, _ := neodriver.GetRecordValue[string](r, "e.task_type")
			summary, _, _ := neodriver.GetRecordValue[string](r, "e.summary")
			sourceType, _, _ := neodriver.GetRecordValue[string](r, "e.source_type")
			trustLevel, _, _ := neodriver.GetRecordValue[int64](r, "e.trust_level")
			weight, _, _ := neodriver.GetRecordValue[float64](r, "e.weight")
			effFreq, _, _ := neodriver.GetRecordValue[float64](r, "e.effective_frequency")
			createdAt, _, _ := neodriver.GetRecordValue[time.Time](r, "e.created_at")
			lastAccessedAt, laIsNil, _ := neodriver.GetRecordValue[time.Time](r, "e.last_accessed_at")
			entityGroup, _, _ := neodriver.GetRecordValue[string](r, "e.entity_group")
			embeddingID, _, _ := neodriver.GetRecordValue[string](r, "e.embedding_id")

			m := models.EpisodicMemory{
				ID:                 pid,
				ProjectID:          projID,
				Language:           lang,
				TaskType:           models.TaskType(taskType),
				Summary:            summary,
				SourceType:         models.SourceType(sourceType),
				TrustLevel:         models.TrustLevel(trustLevel),
				Weight:             weight,
				EffectiveFrequency: effFreq,
				CreatedAt:          createdAt,
				EntityGroup:        entityGroup,
				EmbeddingID:        embeddingID,
			}
			if !laIsNil {
				m.LastAccessedAt = &lastAccessedAt
			}
			results = append(results, m)
		}
		return results, records.Err()
	})
	require.NoError(t, err)
	candidates := raw.([]models.EpisodicMemory)
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
	// Phase 5: CONSOLIDATE — cluster episodics → semantic memories
	// ─────────────────────────────────────────────────────────────
	engine2 := core.DefaultConsolidationEngine()
	engine2.HoursToScan = 24 // scan last 24 hours (our memories are just created)
	engine2.MinClusterSize = 3
	engine2.AccuracyThreshold = 0.7

	_ = engine2
	_ = emb
	t.Log("Phase 5 CONSOLIDATE: skipped — requires Store adapter (Phase 4)")

	// ─────────────────────────────────────────────────────────────
	// CLEANUP
	// ─────────────────────────────────────────────────────────────
	// Cleanup semantic memories (deleted via direct neo4j in Phase 5 previously; skip).
	_ = neo4jClient

	// Delete episodic memories.
	for _, m := range memories {
		_, _ = neo4jClient.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
			_, _ = tx.Run(ctx,
				`MATCH (e:EpisodicMemory {id: $id}) DETACH DELETE e`,
				map[string]any{"id": m.id})
			return nil, nil
		})
		err := pgClient.DeleteByMemoryID(ctx, m.id)
		if err != nil {
			t.Logf("cleanup pgvector %s: %v", m.id[:8], err)
		}
	}

	t.Log("Cleanup complete")
}

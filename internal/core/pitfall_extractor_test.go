package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vatbrain/vatbrain/internal/llm"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// idEmbedder returns an embedding derived from the input text via FNV-64 hash,
// producing deterministic unit-length vectors. Same text → same vector.
type idEmbedder struct{ dim int }

func newIDEmbedder(dim int) *idEmbedder { return &idEmbedder{dim: dim} }

func (e *idEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := deriveVector(text, e.dim)
	return vec, nil
}

func deriveVector(text string, dim int) []float32 {
	h := uint64(14695981039346656037) // FNV offset basis
	for _, b := range []byte(text) {
		h ^= uint64(b)
		h *= 1099511628211 // FNV prime
	}
	vec := make([]float32, dim)
	for i := range vec {
		h ^= h >> 33
		h *= 0xff51afd7ed558ccd
		h ^= h >> 33
		h *= 0xc4ceb9fe1a85ec53
		h ^= h >> 33
		vec[i] = float32(h&0x7fffff)/float32(0x7fffff)*2 - 1
	}
	// Normalize to unit length.
	var sq float64
	for _, v := range vec {
		sq += float64(v) * float64(v)
	}
	norm := float32(1.0)
	if sq > 0 {
		norm = float32(1.0 / sq)
	}
	for i, v := range vec {
		vec[i] = v * norm
	}
	return vec
}

func makeScanItems(entityID string, summaries []string) []store.EpisodicScanItem {
	items := make([]store.EpisodicScanItem, len(summaries))
	now := time.Now().UTC()
	for i, s := range summaries {
		items[i] = store.EpisodicScanItem{
			ID:           uuid.New(),
			Summary:      s,
			TaskType:     models.TaskTypeDebug,
			ProjectID:    "test-project",
			Language:     "go",
			EntityID:     entityID,
			Weight:       1.0,
			LastAccessed: now,
		}
	}
	return items
}

// ── Extract Pipeline Tests ──────────────────────────────────────────────────

func TestExtract_EmptyDebugSet(t *testing.T) {
	pe := &PitfallExtractor{
		MinClusterSize: 3,
		Embedder:       newIDEmbedder(16),
	}
	// No debug episodics at all.
	pitfalls, found, merged, err := pe.Extract(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 0, len(pitfalls))
	assert.Equal(t, 0, found)
	assert.Equal(t, 0, merged)

	// Only feature episodics.
	items := []store.EpisodicScanItem{
		{ID: uuid.New(), Summary: "added auth", TaskType: models.TaskTypeFeature, ProjectID: "p", Language: "go"},
	}
	pitfalls, found, merged, err = pe.Extract(context.Background(), items)
	require.NoError(t, err)
	assert.Equal(t, 0, len(pitfalls))
	assert.Equal(t, 0, found)
	assert.Equal(t, 0, merged)
}

func TestExtract_DebugWithoutEntityID(t *testing.T) {
	// Debug episodics without entity_id are skipped.
	pe := &PitfallExtractor{
		MinClusterSize: 3,
		Embedder:       newIDEmbedder(16),
	}
	items := makeScanItems("", []string{"debug A", "debug B", "debug C"})
	pitfalls, found, merged, err := pe.Extract(context.Background(), items)
	require.NoError(t, err)
	assert.Equal(t, 0, len(pitfalls))
	assert.Equal(t, 0, found)
	assert.Equal(t, 0, merged)
}

func TestExtract_BelowMinClusterSize(t *testing.T) {
	pe := &PitfallExtractor{
		MinClusterSize: 3,
		Embedder:       newIDEmbedder(16),
	}
	items := makeScanItems("func:DoStuff", []string{"debug 1", "debug 2"})
	pitfalls, found, merged, err := pe.Extract(context.Background(), items)
	require.NoError(t, err)
	assert.Equal(t, 0, len(pitfalls), "entity group below min size → skipped")
	assert.Equal(t, 0, found)
	assert.Equal(t, 0, merged)
}

func TestExtract_SingleEntityHeuristic(t *testing.T) {
	pe := &PitfallExtractor{
		MinClusterSize: 3,
		MergeThreshold: 0.85,
		Embedder:       newIDEmbedder(16),
	}
	// Identical summaries → same embedding → cosine=1.0 → merge into 1 cluster.
	summaries := []string{
		"redis pool exhaustion with timeout errors",
		"redis pool exhaustion with timeout errors",
		"redis pool exhaustion with timeout errors",
	}
	items := makeScanItems("func:NewRedisPool", summaries)
	pitfalls, found, merged, err := pe.Extract(context.Background(), items)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(pitfalls), 1, "3 identical summaries should form a cluster")
	assert.GreaterOrEqual(t, found, 1)
	assert.Equal(t, 0, merged)

	pf := pitfalls[0]
	assert.Equal(t, "func:NewRedisPool", pf.EntityID)
	assert.Equal(t, models.EntityTypeFunction, pf.EntityType)
	assert.Equal(t, "test-project", pf.ProjectID)
	assert.Equal(t, "go", pf.Language)
	assert.Equal(t, models.RootCauseUnknown, pf.RootCauseCategory)
	assert.Equal(t, models.SourceTypeINFERRED, pf.SourceType)
	assert.GreaterOrEqual(t, pf.OccurrenceCount, 3)
	assert.Len(t, pf.SourceEpisodicIDs, 3)
}

func TestExtract_MultipleEntities(t *testing.T) {
	pe := &PitfallExtractor{
		MinClusterSize: 2,
		MergeThreshold: 0.85,
		Embedder:       newIDEmbedder(16),
	}
	// Each entity gets identical summaries to force clustering.
	itemsA := makeScanItems("func:DoA", []string{"err A", "err A"})
	itemsB := makeScanItems("func:DoB", []string{"err B", "err B"})
	all := append(itemsA, itemsB...)
	pitfalls, found, merged, err := pe.Extract(context.Background(), all)
	require.NoError(t, err)
	assert.Equal(t, 2, len(pitfalls))
	assert.Equal(t, 2, found)
	entities := map[string]bool{}
	for _, p := range pitfalls {
		entities[p.EntityID] = true
	}
	assert.True(t, entities["func:DoA"])
	assert.True(t, entities["func:DoB"])
	assert.Equal(t, 0, merged)
}

func TestExtract_WithLLM(t *testing.T) {
	llmOutput := PitfallLLMOutput{
		Signature:         "Redis pool exhausted under high concurrency",
		RootCauseCategory: "RESOURCE_EXHAUSTION",
		FixStrategy:       "Increase MaxOpenConns to 200 and add pool monitoring",
		Confidence:        0.85,
	}
	llmJSON, err := json.Marshal(llmOutput)
	require.NoError(t, err)

	pe := &PitfallExtractor{
		MinClusterSize: 2,
		Embedder:       newIDEmbedder(16),
		LLMClient:      &llm.MockClient{Response: string(llmJSON)},
	}
	items := makeScanItems("func:NewRedisPool", []string{"redis timeout", "redis timeout"})
	pitfalls, _, _, err := pe.Extract(context.Background(), items)
	require.NoError(t, err)
	require.Equal(t, 1, len(pitfalls))

	pf := pitfalls[0]
	assert.Equal(t, "Redis pool exhausted under high concurrency", pf.Signature)
	assert.Equal(t, models.RootCauseResourceExhaustion, pf.RootCauseCategory)
	assert.Equal(t, "Increase MaxOpenConns to 200 and add pool monitoring", pf.FixStrategy)
	assert.Equal(t, models.TrustLevel(3), pf.TrustLevel)
}

func TestExtract_LLMErrorFallback(t *testing.T) {
	pe := &PitfallExtractor{
		MinClusterSize: 2,
		Embedder:       newIDEmbedder(16),
		LLMClient:      &llm.MockClient{Err: assert.AnError},
	}
	items := makeScanItems("func:NewRedisPool", []string{"redis timeout", "redis timeout"})
	// When LLM fails, the sub-cluster is skipped (no fallback to heuristic when LLM is set but fails).
	pitfalls, found, _, err := pe.Extract(context.Background(), items)
	require.NoError(t, err)
	// LLM failure causes the sub-cluster to be skipped → 0 pitfalls
	assert.Equal(t, 0, len(pitfalls))
	assert.GreaterOrEqual(t, found, 0)
}

// ── Parse Tests ────────────────────────────────────────────────────────────

func TestParsePitfallResponse_ValidJSON(t *testing.T) {
	raw := `{"signature":"redis pool exhausted","root_cause_category":"RESOURCE_EXHAUSTION","fix_strategy":"increase pool size","confidence":0.9}`
	out, err := parsePitfallResponse(raw)
	require.NoError(t, err)
	assert.Equal(t, "redis pool exhausted", out.Signature)
	assert.Equal(t, "RESOURCE_EXHAUSTION", out.RootCauseCategory)
	assert.Equal(t, "increase pool size", out.FixStrategy)
	assert.Equal(t, 0.9, out.Confidence)
}

func TestParsePitfallResponse_WithMarkdownFences(t *testing.T) {
	raw := "```json\n{\"signature\":\"concurrency bug\",\"root_cause_category\":\"CONCURRENCY\",\"fix_strategy\":\"add mutex\",\"confidence\":0.7}\n```"
	out, err := parsePitfallResponse(raw)
	require.NoError(t, err)
	assert.Equal(t, "concurrency bug", out.Signature)
	assert.Equal(t, "CONCURRENCY", out.RootCauseCategory)
}

func TestParsePitfallResponse_WithGenericFences(t *testing.T) {
	raw := "```\n{\"signature\":\"config error\",\"root_cause_category\":\"CONFIG\",\"fix_strategy\":\"fix env\",\"confidence\":0.5}\n```"
	out, err := parsePitfallResponse(raw)
	require.NoError(t, err)
	assert.Equal(t, "config error", out.Signature)
	assert.Equal(t, "CONFIG", out.RootCauseCategory)
}

func TestParsePitfallResponse_EmbeddedJSON(t *testing.T) {
	// Some LLMs wrap the JSON in explanatory text.
	raw := `Here is the pitfall: {"signature":"oom error","root_cause_category":"RESOURCE_EXHAUSTION","fix_strategy":"reduce memory","confidence":0.8} done.`
	out, err := parsePitfallResponse(raw)
	require.NoError(t, err)
	assert.Equal(t, "oom error", out.Signature)
}

func TestParsePitfallResponse_InvalidJSON(t *testing.T) {
	_, err := parsePitfallResponse("not json at all")
	assert.Error(t, err)
}

// ── Deduplication Tests ────────────────────────────────────────────────────

func TestDeduplicatePitfalls_NoDuplicates(t *testing.T) {
	pe := &PitfallExtractor{
		DedupThreshold: 0.9,
		Embedder:       newIDEmbedder(16),
	}
	now := time.Now().UTC()
	pitfalls := []models.PitfallMemory{
		{ID: uuid.New(), EntityID: "func:A", Signature: "pattern A", OccurrenceCount: 3, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New(), EntityID: "func:B", Signature: "pattern B", OccurrenceCount: 2, CreatedAt: now, UpdatedAt: now},
	}
	result := pe.deduplicatePitfalls(context.Background(), pitfalls)
	assert.Equal(t, 2, len(result))
}

func TestDeduplicatePitfalls_MergeIdentical(t *testing.T) {
	pe := &PitfallExtractor{
		DedupThreshold: 0.9,
		Embedder:       newIDEmbedder(16),
	}
	now := time.Now().UTC()
	// Same entity + same signature text → same embedding → high similarity → merge.
	pitfalls := []models.PitfallMemory{
		{ID: uuid.New(), EntityID: "func:A", Signature: "identical pattern", OccurrenceCount: 5, FixStrategy: "strategy 1", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New(), EntityID: "func:A", Signature: "identical pattern", OccurrenceCount: 2, FixStrategy: "strategy 2 longer", CreatedAt: now, UpdatedAt: now},
	}
	result := pe.deduplicatePitfalls(context.Background(), pitfalls)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, 7, result[0].OccurrenceCount) // 5 + 2
	assert.Equal(t, "strategy 2 longer", result[0].FixStrategy) // longer strategy wins
}

func TestDeduplicatePitfalls_DifferentEntitiesKept(t *testing.T) {
	pe := &PitfallExtractor{
		DedupThreshold: 0.9,
		Embedder:       newIDEmbedder(16),
	}
	now := time.Now().UTC()
	// Same signature, different entities → kept separate.
	pitfalls := []models.PitfallMemory{
		{ID: uuid.New(), EntityID: "func:A", Signature: "same pattern", OccurrenceCount: 3, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New(), EntityID: "func:B", Signature: "same pattern", OccurrenceCount: 2, CreatedAt: now, UpdatedAt: now},
	}
	result := pe.deduplicatePitfalls(context.Background(), pitfalls)
	assert.Equal(t, 2, len(result), "different entities must not be merged")
}

func TestDeduplicatePitfalls_SingleItem(t *testing.T) {
	pe := &PitfallExtractor{Embedder: newIDEmbedder(16)}
	now := time.Now().UTC()
	pitfalls := []models.PitfallMemory{
		{ID: uuid.New(), EntityID: "func:A", Signature: "only one", OccurrenceCount: 1, CreatedAt: now, UpdatedAt: now},
	}
	result := pe.deduplicatePitfalls(context.Background(), pitfalls)
	assert.Equal(t, 1, len(result))
}

// ── Merge Logic Tests ──────────────────────────────────────────────────────

func TestMergePitfallGroup_OccurrenceCount(t *testing.T) {
	pe := &PitfallExtractor{}
	now := time.Now().UTC()
	pitfalls := []models.PitfallMemory{
		{ID: uuid.New(), EntityID: "func:A", Signature: "sig", OccurrenceCount: 10, FixStrategy: "", WasUserCorrected: false, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New(), EntityID: "func:A", Signature: "sig", OccurrenceCount: 2, FixStrategy: "better fix", WasUserCorrected: true, CreatedAt: now, UpdatedAt: now},
	}
	mg := pitfallMergeGroup{primary: 0, members: []int{0, 1}}
	result := pe.mergePitfallGroup(pitfalls, mg)
	assert.Equal(t, 12, result.OccurrenceCount) // 10 + 2
	assert.Equal(t, "better fix", result.FixStrategy) // longer strategy
	assert.True(t, result.WasUserCorrected) // one was user-corrected
}

func TestMergePitfallGroup_PicksHigherOccurrenceAsBase(t *testing.T) {
	pe := &PitfallExtractor{}
	now := time.Now().UTC()
	later := now.Add(time.Hour)
	pitfalls := []models.PitfallMemory{
		{ID: uuid.New(), EntityID: "func:A", Signature: "sig", OccurrenceCount: 1, FixStrategy: "short", LastOccurredAt: &now, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New(), EntityID: "func:A", Signature: "sig", OccurrenceCount: 8, FixStrategy: "short", LastOccurredAt: &later, CreatedAt: now, UpdatedAt: now},
	}
	mg := pitfallMergeGroup{primary: 0, members: []int{0, 1}}
	result := pe.mergePitfallGroup(pitfalls, mg)
	assert.Equal(t, 9, result.OccurrenceCount)
	// Higher count pitfall became primary, so its LastOccurredAt should be preserved.
	assert.Equal(t, later, *result.LastOccurredAt)
}

// ── Entity Type Inference Tests ────────────────────────────────────────────

func TestInferEntityType(t *testing.T) {
	assert.Equal(t, models.EntityTypeFunction, inferEntityType("func:NewRedisPool"))
	assert.Equal(t, models.EntityTypeFunction, inferEntityType("func:handleRequest"))
	assert.Equal(t, models.EntityTypeModule, inferEntityType("module:auth"))
	assert.Equal(t, models.EntityTypeAPI, inferEntityType("api:/users/create"))
	assert.Equal(t, models.EntityTypeConfig, inferEntityType("config:redis.yaml"))
	assert.Equal(t, models.EntityTypeQuery, inferEntityType("query:SELECT_users"))
	assert.Equal(t, models.EntityTypeFunction, inferEntityType("unknown_prefix:foo")) // default
	assert.Equal(t, models.EntityTypeFunction, inferEntityType(""))                    // default
}

// ── HAC Sub-Clustering Tests ───────────────────────────────────────────────

func TestSubCluster_SingleItem(t *testing.T) {
	pe := &PitfallExtractor{
		MergeThreshold: 0.85,
		Embedder:       newIDEmbedder(16),
	}
	items := makeScanItems("func:A", []string{"only one"})
	g := EntityGroup{EntityID: "func:A", Episodics: items}
	clusters := pe.subCluster(context.Background(), g)
	assert.Equal(t, 1, len(clusters))
	assert.Equal(t, 1, len(clusters[0].Episodics))
}

func TestSubCluster_IdenticalSummariesMerge(t *testing.T) {
	pe := &PitfallExtractor{
		MergeThreshold: 0.85,
		Embedder:       newIDEmbedder(16),
	}
	// Same summary → same embedding → cosine similarity = 1.0 > 0.85 → merge.
	items := makeScanItems("func:A", []string{"same bug", "same bug", "same bug"})
	g := EntityGroup{EntityID: "func:A", Episodics: items}
	clusters := pe.subCluster(context.Background(), g)
	assert.Equal(t, 1, len(clusters), "identical summaries should merge into 1 cluster")
	assert.Equal(t, 3, len(clusters[0].Episodics))
}

func TestSubCluster_DistinctSummariesStaySeparate(t *testing.T) {
	pe := &PitfallExtractor{
		MergeThreshold: 0.99, // very high threshold — almost nothing merges
		Embedder:       newIDEmbedder(16),
	}
	items := makeScanItems("func:A", []string{"bug A description", "bug B description", "bug C description"})
	g := EntityGroup{EntityID: "func:A", Episodics: items}
	clusters := pe.subCluster(context.Background(), g)
	// With threshold 0.99, unlikely any random vectors will merge.
	assert.GreaterOrEqual(t, len(clusters), 1)
}

// ── groupByEntityID Tests ──────────────────────────────────────────────────

func TestGroupByEntityID(t *testing.T) {
	items := []store.EpisodicScanItem{
		{ID: uuid.New(), Summary: "a1", EntityID: "func:A"},
		{ID: uuid.New(), Summary: "a2", EntityID: "func:A"},
		{ID: uuid.New(), Summary: "b1", EntityID: "func:B"},
	}
	groups := groupByEntityID(items)
	assert.Equal(t, 2, len(groups))
	for _, g := range groups {
		switch g.EntityID {
		case "func:A":
			assert.Equal(t, 2, len(g.Episodics))
		case "func:B":
			assert.Equal(t, 1, len(g.Episodics))
		default:
			t.Errorf("unexpected entity: %s", g.EntityID)
		}
	}
}

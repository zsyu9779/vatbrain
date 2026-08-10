package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

// testWriteDeps builds the same dependency set the bench server tests use:
// in-memory store + keyword embedder + default engines.
func testWriteDeps(t *testing.T) WriteDeps {
	t.Helper()
	return WriteDeps{
		Store:       memory.NewStore(),
		Gate:        DefaultSignificanceGate(),
		PatternSep:  DefaultPatternSeparation(),
		WeightDecay: DefaultWeightDecayEngine(),
		Embedder:    embedder.NewKeywordEmbedder(models.DefaultEmbeddingDim),
		WorkingMem:  store.NewWorkingMemoryBuffer(20),
	}
}

func TestWriteMemoryWithEmbedding_PersistsProvidedEmbedding(t *testing.T) {
	deps := testWriteDeps(t)
	precomputed := []float32{0.25, 0.5, 0.75, 1.0}

	res, err := WriteMemoryWithEmbedding(context.Background(), deps,
		WriteEvent{Summary: "Alice got a shell necklace in Hawaii", UserConfirmed: true},
		precomputed, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	assert.True(t, res.Persisted)
	assert.Equal(t, models.MergeActionCreatedNew, res.MergeAction)

	stored, err := deps.Store.GetEpisodic(context.Background(), res.MemoryID)
	require.NoError(t, err)
	assert.Equal(t, precomputed, stored.ContextVector,
		"the persisted memory must carry the caller-provided embedding")
}

func TestWriteMemoryWithEmbedding_EmptyEmbeddingRejected(t *testing.T) {
	deps := testWriteDeps(t)

	_, err := WriteMemoryWithEmbedding(context.Background(), deps,
		WriteEvent{Summary: "a memory", UserConfirmed: true},
		nil, "u1", "en", "", models.TaskTypeFeature)
	require.Error(t, err)
}

func TestWriteMemoryWithEmbedding_GatedOut_NoPersist(t *testing.T) {
	deps := testWriteDeps(t)

	// Chit-chat with no user confirmation and no prediction-error signal is
	// gated out even though an embedding was precomputed.
	res, err := WriteMemoryWithEmbedding(context.Background(), deps,
		WriteEvent{Summary: "hi how are you"},
		[]float32{1, 0, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	assert.False(t, res.Persisted)
	assert.Equal(t, "below_threshold", res.GateReason)
}

func TestWriteMemoryWithEmbedding_MergesExistingMemory(t *testing.T) {
	deps := testWriteDeps(t)

	// Seed a memory, then write a near-identical summary with a similar vector:
	// pattern separation must merge into the existing memory, not create a new
	// one.
	emb, err := deps.Embedder.Embed(context.Background(), "Alice got a shell necklace in Hawaii")
	require.NoError(t, err)
	first, err := WriteMemory(context.Background(), deps,
		WriteEvent{Summary: "Alice got a shell necklace in Hawaii", UserConfirmed: true},
		"u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.True(t, first.Persisted)

	// Same vector → similarity 1.0 > threshold → merge.
	res, err := WriteMemoryWithEmbedding(context.Background(), deps,
		WriteEvent{Summary: "Alice got a shell necklace in Hawaii", UserConfirmed: true},
		emb, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	assert.True(t, res.Persisted)
	assert.Equal(t, models.MergeActionUpdatedExisting, res.MergeAction)
	assert.Equal(t, first.MemoryID, res.MemoryID, "merge must update the existing memory")
}

func TestWriteMemory_StillComputesEmbeddingInternally(t *testing.T) {
	deps := testWriteDeps(t)
	summary := "Dan finished a ten kilometer run in the rain"

	res, err := WriteMemory(context.Background(), deps,
		WriteEvent{Summary: summary, UserConfirmed: true},
		"u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.True(t, res.Persisted)

	stored, err := deps.Store.GetEpisodic(context.Background(), res.MemoryID)
	require.NoError(t, err)
	assert.NotEmpty(t, stored.ContextVector)

	want, err := deps.Embedder.Embed(context.Background(), summary)
	require.NoError(t, err)
	assert.Equal(t, want, stored.ContextVector,
		"WriteMemory must persist the embedding it computed internally")
}

func TestWriteMemoryWithEmbedding_PushesWorkingMemory(t *testing.T) {
	deps := testWriteDeps(t)

	res, err := WriteMemoryWithEmbedding(context.Background(), deps,
		WriteEvent{Summary: "Carol plans to adopt a beagle puppy", UserConfirmed: true},
		[]float32{1, 0, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.True(t, res.Persisted)

	cycles := deps.WorkingMem.GetAll("u1")
	require.Len(t, cycles, 1)
	assert.Equal(t, "Carol plans to adopt a beagle puppy", cycles[0])
}

func TestWriteMemory_OccurredAt_Passthrough(t *testing.T) {
	// Both write-pipeline paths (WriteMemory and WriteMemoryWithEmbedding)
	// must carry event.OccurredAt into the persisted memory.
	occurred := time.Date(2029, 5, 4, 10, 0, 0, 0, time.UTC)
	deps := testWriteDeps(t)

	res, err := WriteMemory(context.Background(), deps,
		WriteEvent{Summary: "Alice got a shell necklace in Hawaii",
			UserConfirmed: true, OccurredAt: occurred},
		"u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.True(t, res.Persisted)

	stored, err := deps.Store.GetEpisodic(context.Background(), res.MemoryID)
	require.NoError(t, err)
	assert.True(t, stored.OccurredAt.Equal(occurred),
		"WriteMemory must persist event.OccurredAt, got %v", stored.OccurredAt)

	emb := []float32{0.25, 0.5, 0.75, 1.0}
	res2, err := WriteMemoryWithEmbedding(context.Background(), deps,
		WriteEvent{Summary: "Bob fixed the flaky test by seeding time",
			UserConfirmed: true, OccurredAt: occurred},
		emb, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.True(t, res2.Persisted)

	stored2, err := deps.Store.GetEpisodic(context.Background(), res2.MemoryID)
	require.NoError(t, err)
	assert.True(t, stored2.OccurredAt.Equal(occurred),
		"WriteMemoryWithEmbedding must persist event.OccurredAt, got %v", stored2.OccurredAt)
}

func TestWriteMemory_OccurredAt_FallsBackToCreatedAt(t *testing.T) {
	deps := testWriteDeps(t)

	res, err := WriteMemory(context.Background(), deps,
		WriteEvent{Summary: "no explicit event time here", UserConfirmed: true},
		"u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.True(t, res.Persisted)

	stored, err := deps.Store.GetEpisodic(context.Background(), res.MemoryID)
	require.NoError(t, err)
	assert.True(t, stored.OccurredAt.Equal(stored.CreatedAt),
		"zero event.OccurredAt must fall back to CreatedAt, got %v", stored.OccurredAt)
}

func TestWriteMemory_OccurredAt_MergeKeepsStoryAnchor(t *testing.T) {
	deps := testWriteDeps(t)
	ctx := context.Background()

	t0 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2029, 5, 4, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 8, 10, 0, 0, 0, time.UTC)

	first, err := WriteMemory(ctx, deps,
		WriteEvent{Summary: "Alice got a shell necklace in Hawaii", UserConfirmed: true, OccurredAt: t1},
		"u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.True(t, first.Persisted)

	// Same-event restatement merges; the memory keeps its original anchor.
	emb, err := deps.Embedder.Embed(ctx, "Alice got a shell necklace in Hawaii")
	require.NoError(t, err)
	_, err = WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "Alice got a shell necklace in Hawaii", UserConfirmed: true, OccurredAt: t2},
		emb, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	stored, err := deps.Store.GetEpisodic(ctx, first.MemoryID)
	require.NoError(t, err)
	assert.True(t, stored.OccurredAt.Equal(t1),
		"a later restatement must not move the anchor, got %v", stored.OccurredAt)

	// A genuinely earlier event merges in: the anchor moves earlier.
	_, err = WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "Alice got a shell necklace in Hawaii", UserConfirmed: true, OccurredAt: t0},
		emb, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	stored, err = deps.Store.GetEpisodic(ctx, first.MemoryID)
	require.NoError(t, err)
	assert.True(t, stored.OccurredAt.Equal(t0),
		"an earlier explicit event must move the anchor, got %v", stored.OccurredAt)
}

// ── Update tracking (ticket 03) ─────────────────────────────────────────────

func TestWriteMemory_UpdateTracking_RetiresCoveredOld(t *testing.T) {
	// A temporally newer event about the same subject covers the older memory:
	// the old one is retired (not merged into), the new one is written fresh
	// with a boosted weight, and a SUPERSEDED edge records the supersession.
	deps := testWriteDeps(t)
	ctx := context.Background()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	first, err := WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "The user prefers PostgreSQL", UserConfirmed: true, OccurredAt: t1},
		[]float32{1, 0, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.True(t, first.Persisted)

	// Simulate a decayed old memory: the update must let the fresh, boosted
	// new memory clearly outrank it at retrieval.
	require.NoError(t, deps.Store.UpdateEpisodicWeight(ctx, first.MemoryID, 0.3, 1.0))

	// Orthogonal vector → pattern separation will NOT merge; update tracking
	// must recognise the newer same-subject statement and retire the old one.
	second, err := WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "The user now prefers SQLite", UserConfirmed: true, OccurredAt: t2},
		[]float32{0, 1, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.True(t, second.Persisted)
	assert.Equal(t, models.MergeActionCreatedNew, second.MergeAction,
		"an update must create a fresh memory, not append into the old one")
	require.NotEqual(t, first.MemoryID, second.MemoryID)

	// Old memory retired.
	old, err := deps.Store.GetEpisodic(ctx, first.MemoryID)
	require.NoError(t, err)
	require.NotNil(t, old.ObsoletedAt, "covered old memory must be marked obsolete")

	// Supersession edge recorded for traceability.
	edges, err := deps.Store.GetEdges(ctx, second.MemoryID, "SUPERSEDED", "outgoing")
	require.NoError(t, err)
	require.Len(t, edges, 1)
	assert.Equal(t, first.MemoryID, edges[0].ToID)

	// New memory weight is boosted above its un-boosted value (1.0 × 1.5
	// clamps at 1.0 for a fresh memory).
	got, err := deps.Store.GetEpisodic(ctx, second.MemoryID)
	require.NoError(t, err)
	assert.InDelta(t, second.Weight, got.Weight, 0.0001,
		"WriteResult weight must reflect the boosted weight")
	assert.Greater(t, got.Weight, old.Weight,
		"the newer memory must outrank the retired one")
}

func TestWriteMemory_UpdateTracking_RestatementStillMerges(t *testing.T) {
	// Exact restatements stay on the pattern-separation append path: no
	// obsoletion, no new memory.
	deps := testWriteDeps(t)
	ctx := context.Background()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	first, err := WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "Alice got a shell necklace in Hawaii", UserConfirmed: true, OccurredAt: t1},
		[]float32{1, 0, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)

	second, err := WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "Alice got a shell necklace in Hawaii", UserConfirmed: true, OccurredAt: t2},
		[]float32{1, 0, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	assert.Equal(t, models.MergeActionUpdatedExisting, second.MergeAction)
	assert.Equal(t, first.MemoryID, second.MemoryID)

	stored, err := deps.Store.GetEpisodic(ctx, first.MemoryID)
	require.NoError(t, err)
	assert.Nil(t, stored.ObsoletedAt, "a restatement must not retire the original")
}

func TestWriteMemory_UpdateTracking_OlderEventRetiresNothing(t *testing.T) {
	// An event that is NOT temporally newer (earlier occurred_at) cannot
	// cover anything: both memories stay active.
	deps := testWriteDeps(t)
	ctx := context.Background()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t0 := time.Date(2029, 4, 20, 0, 0, 0, 0, time.UTC)

	first, err := WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "The user prefers PostgreSQL", UserConfirmed: true, OccurredAt: t1},
		[]float32{1, 0, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)

	second, err := WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "The user now prefers SQLite", UserConfirmed: true, OccurredAt: t0},
		[]float32{0, 1, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.NotEqual(t, first.MemoryID, second.MemoryID)

	old, err := deps.Store.GetEpisodic(ctx, first.MemoryID)
	require.NoError(t, err)
	assert.Nil(t, old.ObsoletedAt, "an earlier event must not retire the newer memory")
}

func TestWriteMemory_UpdateTracking_ChineseDirectiveReversal(t *testing.T) {
	// 中文用例（CONTRIBUTING.md 约定）:指令反转(不要 → 应该)即使 bigram 高度
	// 重合也构成更新——旧指令废弃,新指令独立成条,不被 append 合并。
	deps := testWriteDeps(t)
	ctx := context.Background()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	first, err := WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "Redis MaxOpenConns 不要设为 100", UserConfirmed: true, OccurredAt: t1},
		[]float32{1, 0, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)

	second, err := WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "Redis MaxOpenConns 应该设为 100", UserConfirmed: true, OccurredAt: t2},
		[]float32{0, 1, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.NotEqual(t, first.MemoryID, second.MemoryID)

	old, err := deps.Store.GetEpisodic(ctx, first.MemoryID)
	require.NoError(t, err)
	require.NotNil(t, old.ObsoletedAt, "被反转的旧指令必须废弃")
	assert.Equal(t, "Redis MaxOpenConns 不要设为 100", old.Summary,
		"旧记忆内容保持原样,不被反转内容混入")
	got, err := deps.Store.GetEpisodic(ctx, second.MemoryID)
	require.NoError(t, err)
	assert.Equal(t, "Redis MaxOpenConns 应该设为 100", got.Summary,
		"新记忆独立承载反转后的指令")
}

func TestWriteMemory_UpdateTracking_DifferentSubjectRetiresNothing(t *testing.T) {
	deps := testWriteDeps(t)
	ctx := context.Background()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	first, err := WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "Alice got a shell necklace in Hawaii", UserConfirmed: true, OccurredAt: t1},
		[]float32{1, 0, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)

	second, err := WriteMemoryWithEmbedding(ctx, deps,
		WriteEvent{Summary: "Carol plans to adopt a beagle puppy", UserConfirmed: true, OccurredAt: t2},
		[]float32{0, 1, 0, 0}, "u1", "en", "", models.TaskTypeFeature)
	require.NoError(t, err)
	require.NotEqual(t, first.MemoryID, second.MemoryID)

	old, err := deps.Store.GetEpisodic(ctx, first.MemoryID)
	require.NoError(t, err)
	assert.Nil(t, old.ObsoletedAt)
}

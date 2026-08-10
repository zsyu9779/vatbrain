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

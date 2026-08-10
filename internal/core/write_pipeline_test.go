package core

import (
	"context"
	"testing"

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

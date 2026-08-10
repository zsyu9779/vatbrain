package memory_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

// seedTemporalMemories writes three memories with distinct occurred_at values
// and returns their IDs in occurred_at order (oldest first).
func seedTemporalMemories(t *testing.T, st *memory.Store, projectID string) []string {
	t.Helper()
	base := time.Date(2029, 5, 4, 0, 0, 0, 0, time.UTC)
	var ids []string
	for i, summary := range []string{
		"oldest event: Alice in Hawaii",
		"middle event: Bob in Paris",
		"newest event: Carol in Tokyo",
	} {
		mem := makeEpisodic()
		mem.ProjectID = projectID
		mem.Summary = summary
		mem.OccurredAt = base.Add(time.Duration(i) * 24 * time.Hour)
		require.NoError(t, st.WriteEpisodic(ctx, mem))
		ids = append(ids, mem.ID.String())
	}
	return ids
}

func TestMemory_SearchEpisodic_OccurredAt_FilterAndSort(t *testing.T) {
	st := memory.NewStore()
	ids := seedTemporalMemories(t, st, "proj-time")
	base := time.Date(2029, 5, 4, 0, 0, 0, 0, time.UTC)

	// Window filter: events at base, base+24h, base+48h — only the middle one
	// (05-05 00:00) falls in [05-04 01:00, 05-05 23:00].
	res, err := st.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:      "proj-time",
		OccurredAfter:  base.Add(1 * time.Hour),
		OccurredBefore: base.Add(47 * time.Hour),
		Limit:          10,
	})
	require.NoError(t, err)
	require.Len(t, res, 1, "only the middle event is inside the window")
	assert.Equal(t, ids[1], res[0].ID.String())

	// Time sort: newest first.
	res, err = st.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:        "proj-time",
		SortByOccurredAt: true,
		Limit:            10,
	})
	require.NoError(t, err)
	require.Len(t, res, 3)
	assert.Equal(t, ids[2], res[0].ID.String(), "most recent first")
	assert.Equal(t, ids[1], res[1].ID.String())
	assert.Equal(t, ids[0], res[2].ID.String())

	// Rank-then-sort on the embedding path: top-3 relevant re-ordered by
	// occurred_at descending.
	res, err = st.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:        "proj-time",
		Embedding:        []float64{0.1, 0.2, 0.3},
		SortByOccurredAt: true,
		Limit:            10,
	})
	require.NoError(t, err)
	require.Len(t, res, 3)
	assert.Equal(t, ids[2], res[0].ID.String())
	assert.Equal(t, ids[0], res[2].ID.String())
}

package provider

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
)

func TestParseRelativeTime(t *testing.T) {
	now := time.Date(2029, 5, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		query      string
		wantAfter  time.Time
		wantBefore time.Time
		wantSort   bool
	}{
		{
			name:       "english last week",
			query:      "what did Alice do last week?",
			wantAfter:  now.Add(-7 * 24 * time.Hour),
			wantBefore: now,
		},
		{
			name:       "chinese shangzhou",
			query:      "Alice 上周做了什么",
			wantAfter:  now.Add(-7 * 24 * time.Hour),
			wantBefore: now,
		},
		{
			name:       "yesterday",
			query:      "what happened yesterday",
			wantAfter:  now.Add(-24 * time.Hour),
			wantBefore: now,
		},
		{
			name:     "most recent",
			query:    "most recent trip",
			wantSort: true,
		},
		{
			name:     "chinese zuijinyici",
			query:    "最近一次 Alice 去了哪里",
			wantSort: true,
		},
		{
			name:     "chinese zuixin",
			query:    "最新消息",
			wantSort: true,
		},
		{
			name:  "no temporal expression",
			query: "how to fix nil pointer in login",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			win := ParseRelativeTime(tc.query, now)
			assert.Equal(t, tc.wantSort, win.SortNewest, "sort-newest flag")
			if !tc.wantAfter.IsZero() {
				assert.True(t, win.After.Equal(tc.wantAfter),
					"After: got %v want %v", win.After, tc.wantAfter)
			} else {
				assert.True(t, win.After.IsZero(), "After must be zero, got %v", win.After)
			}
			if !tc.wantBefore.IsZero() {
				assert.True(t, win.Before.Equal(tc.wantBefore),
					"Before: got %v want %v", win.Before, tc.wantBefore)
			} else {
				assert.True(t, win.Before.IsZero(), "Before must be zero, got %v", win.Before)
			}
		})
	}
}

// seedTemporalEpisodic writes a memory with an explicit occurrence time and a
// context vector computed by deps.Embedder, so the embedding retrieval path
// can rank it.
func seedTemporalEpisodic(t *testing.T, deps core.WriteDeps, projectID, summary string,
	occurredAt time.Time) {
	t.Helper()
	emb, err := deps.Embedder.Embed(context.Background(), summary)
	require.NoError(t, err)
	mem := &models.EpisodicMemory{
		ID:                 uuid.New(),
		ProjectID:          projectID,
		Language:           "en",
		TaskType:           models.TaskTypeFeature,
		Summary:            summary,
		SourceType:         models.SourceTypeUSER,
		TrustLevel:         models.DefaultTrustLevel,
		Weight:             1.0,
		EffectiveFrequency: 1.0,
		CreatedAt:          time.Now(),
		OccurredAt:         occurredAt,
		ContextVector:      emb,
	}
	require.NoError(t, deps.Store.WriteEpisodic(context.Background(), mem))
}

func TestRetrieveEpisodic_RelativeTimeWindow_EmbeddingPath(t *testing.T) {
	// Keyword embedder yields a signal vector → the embedding path runs.
	deps, _ := testDeps(t)
	deps.Embedder = embedder.NewKeywordEmbedder(models.DefaultEmbeddingDim)
	now := time.Now()

	// The stale trip (10 days ago) must be excluded by "last week"; the fresh
	// one (2 days ago) must survive.
	seedTemporalEpisodic(t, deps, "u1", "Alice got a shell necklace on a trip to Hawaii",
		now.Add(-10*24*time.Hour))
	seedTemporalEpisodic(t, deps, "u1", "Bob bought a surfboard on a trip to Hawaii",
		now.Add(-2*24*time.Hour))

	res, err := RetrieveEpisodic(context.Background(), deps, "u1",
		"what happened on the Hawaii trip last week", 5)
	require.NoError(t, err)
	require.Len(t, res, 1, "only the fresh memory is inside the last-week window")
	assert.Contains(t, res[0].Summary, "Bob")

	// "most recent" orders by occurred_at descending: the fresh memory first.
	res, err = RetrieveEpisodic(context.Background(), deps, "u1",
		"most recent Hawaii trip", 5)
	require.NoError(t, err)
	require.Len(t, res, 2)
	assert.Contains(t, res[0].Summary, "Bob", "most recent first")
	assert.Contains(t, res[1].Summary, "Alice")
}

func TestRetrieveEpisodic_RelativeTimeWindow_LexicalPath(t *testing.T) {
	// Stub embedder returns a zero vector → the lexical fallback runs; the
	// temporal window must still narrow the pool.
	deps, _ := testDeps(t)
	now := time.Now()
	seedTemporalEpisodic(t, deps, "u1", "Carol baked a sourdough loaf yesterday",
		now.Add(-2*24*time.Hour))
	seedTemporalEpisodic(t, deps, "u1", "Dave baked a sourdough loaf last month",
		now.Add(-30*24*time.Hour))

	res, err := RetrieveEpisodic(context.Background(), deps, "u1",
		"sourdough bread last week", 5)
	require.NoError(t, err)
	require.Len(t, res, 1, "only the fresh memory is inside the last-week window")
	assert.Contains(t, res[0].Summary, "Carol")
	// The returned memory carries its occurrence time for temporal reasoning.
	assert.False(t, res[0].OccurredAt.IsZero())
}

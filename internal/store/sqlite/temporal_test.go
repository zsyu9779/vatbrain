package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/store"
)

// legacyEpisodicDDL mirrors the episodic_memories DDL as it existed before the
// v0.4 temporal attribute (occurred_at) — every column except occurred_at.
const legacyEpisodicDDL = `
CREATE TABLE episodic_memories (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    language TEXT NOT NULL,
    task_type TEXT NOT NULL,
    summary TEXT NOT NULL,
    source_type TEXT NOT NULL,
    trust_level INTEGER NOT NULL DEFAULT 3,
    weight REAL NOT NULL DEFAULT 1.0,
    effective_frequency REAL NOT NULL DEFAULT 1.0,
    entity_group TEXT DEFAULT '',
    context_vector BLOB DEFAULT NULL,
    full_snapshot_uri TEXT DEFAULT '',
    is_correction INTEGER DEFAULT 0,
    surprise_score REAL NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    last_accessed_at TEXT,
    obsoleted_at TEXT
);`

func TestSQLite_Migrate_OccurredAt_Backfill(t *testing.T) {
	// Build a pre-occurred_at database: create the table without the column,
	// insert a legacy row, then run migrate() — the ticket-02 migration must
	// add the column and backfill existing rows with created_at.
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(legacyEpisodicDDL)
	require.NoError(t, err)

	createdAt := "2026-01-15T08:30:00Z"
	_, err = db.Exec(`
		INSERT INTO episodic_memories
			(id, project_id, language, task_type, summary, source_type,
			 trust_level, weight, effective_frequency, entity_group,
			 context_vector, full_snapshot_uri, is_correction, surprise_score,
			 created_at, last_accessed_at, obsoleted_at)
		VALUES (?, 'p', 'go', 'debug', 'legacy memory', 'USER', 3, 1.0, 1.0,
		        '', NULL, '', 0, 0, ?, NULL, NULL)`,
		uuid.NewString(), createdAt)
	require.NoError(t, err)

	require.NoError(t, migrate(db))

	var occurred string
	err = db.QueryRow(`SELECT occurred_at FROM episodic_memories`).Scan(&occurred)
	require.NoError(t, err)
	assert.Equal(t, createdAt, occurred, "legacy rows must be backfilled with created_at")

	// Idempotency: a second migrate must not error (duplicate column tolerated).
	require.NoError(t, migrate(db))

	var n int
	err = db.QueryRow(`SELECT count(*) FROM sqlite_master
		WHERE type='index' AND name='idx_episodic_occurred'`).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "occurred_at index must exist after migration")
}

func TestSQLite_WriteEpisodic_OccurredAt_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	occurred := time.Date(2029, 5, 4, 10, 0, 0, 0, time.UTC)
	mem := makeEpisodic("proj-a", "go", "debug", "Alice got a shell necklace in Hawaii")
	mem.OccurredAt = occurred
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	assert.True(t, got.OccurredAt.Equal(occurred),
		"occurred_at must round-trip, got %v want %v", got.OccurredAt, occurred)

	// Fallback: a memory written without an explicit occurrence time reads
	// back with OccurredAt == CreatedAt (backward compatible).
	mem2 := makeEpisodic("proj-a", "go", "debug", "no explicit event time")
	require.NoError(t, s.WriteEpisodic(ctx, mem2))

	got2, err := s.GetEpisodic(ctx, mem2.ID)
	require.NoError(t, err)
	assert.True(t, got2.OccurredAt.Equal(got2.CreatedAt),
		"zero OccurredAt must fall back to CreatedAt, got %v", got2.OccurredAt)
}

// seedTemporalMemories writes three memories with distinct occurred_at values
// (and distinct 3-dim context vectors so the embedding path can rank them)
// and returns their IDs in occurred_at order (oldest first).
func seedTemporalMemories(t *testing.T, s *Store, projectID string) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2029, 5, 4, 0, 0, 0, 0, time.UTC)
	vecs := [][]float32{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	var ids []uuid.UUID
	for i, summary := range []string{
		"oldest event: Alice in Hawaii",
		"middle event: Bob in Paris",
		"newest event: Carol in Tokyo",
	} {
		mem := makeEpisodic(projectID, "en", "feature", summary)
		mem.OccurredAt = base.Add(time.Duration(i) * 24 * time.Hour)
		mem.ContextVector = vecs[i]
		require.NoError(t, s.WriteEpisodic(ctx, mem))
		ids = append(ids, mem.ID)
	}
	return ids
}

func TestSQLite_SearchEpisodic_OccurredAt_FilterAndSort(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ids := seedTemporalMemories(t, s, "proj-time")
	base := time.Date(2029, 5, 4, 0, 0, 0, 0, time.UTC)

	// Window filter: events at base, base+24h, base+48h — only the middle one
	// (05-05 00:00) falls in [05-04 01:00, 05-05 23:00].
	res, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:      "proj-time",
		OccurredAfter:  base.Add(1 * time.Hour),  // after 05-04 01:00
		OccurredBefore: base.Add(47 * time.Hour), // before 05-05 23:00
		Limit:          10,
	})
	require.NoError(t, err)
	require.Len(t, res, 1, "only the middle event is inside the window")
	assert.Equal(t, ids[1], res[0].ID)

	// Time sort: newest first regardless of insertion order.
	res, err = s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:        "proj-time",
		SortByOccurredAt: true,
		Limit:            10,
	})
	require.NoError(t, err)
	require.Len(t, res, 3)
	assert.Equal(t, ids[2], res[0].ID, "most recent first")
	assert.Equal(t, ids[1], res[1].ID)
	assert.Equal(t, ids[0], res[2].ID)
}

func TestSQLite_SearchEpisodic_OccurredAt_EmbeddingPath(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedTemporalMemories(t, s, "proj-time")
	base := time.Date(2029, 5, 4, 0, 0, 0, 0, time.UTC)

	// On the embedding path the time window narrows the candidate pool before
	// cosine ranking: events at base, base+24h, base+48h — only the newest
	// (05-06 00:00) is >= 05-05 01:00.
	res, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:     "proj-time",
		Embedding:     []float64{1, 0, 0},
		OccurredAfter: base.Add(25 * time.Hour), // after 05-05 01:00
		Limit:         10,
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Contains(t, res[0].Summary, "Carol")

	// Rank-then-sort on the embedding path: the top-3 relevant memories
	// (cosine: Alice 1.0, then Bob and Carol at 0) are re-ordered by
	// occurred_at descending, so the most recent surfaces first.
	res, err = s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:        "proj-time",
		Embedding:        []float64{1, 0, 0},
		SortByOccurredAt: true,
		Limit:            10,
	})
	require.NoError(t, err)
	require.Len(t, res, 3)
	assert.Contains(t, res[0].Summary, "Carol")
	assert.Contains(t, res[2].Summary, "Alice")
}

func TestSQLite_SearchEpisodic_TimeQuery_BypassesHotCache(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Populate the hot cache with an unfiltered query.
	first := makeEpisodic("proj-cache", "en", "feature", "old cached event")
	first.OccurredAt = time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.WriteEpisodic(ctx, first))
	_, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{ProjectID: "proj-cache", Limit: 10})
	require.NoError(t, err)

	// A fresh event lands after the cached query ran; a time-windowed query
	// for the new window must see it (caching the old result would hide it).
	fresh := makeEpisodic("proj-cache", "en", "feature", "brand new event")
	fresh.OccurredAt = time.Date(2029, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.WriteEpisodic(ctx, fresh))

	res, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:     "proj-cache",
		OccurredAfter: time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC),
		Limit:         10,
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, fresh.ID, res[0].ID, "time-constrained query must not serve the stale cache")
}

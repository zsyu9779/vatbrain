package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/store"
)

// TestSearchEpisodic_RRF_LexicalRescue is the ticket's core claim: a memory
// that the semantic channel ranks last (near-zero cosine) but whose summary
// carries the query's exact keywords surfaces in the RRF top-k, where pure
// semantic ranking would keep it out.
//
//   - A: sem rank 1, slight lexical overlap (rank 2)
//   - B: sem rank 3 (cos ~0), lexical rank 1 — the rescue
//   - X: sem rank 2, no lexical overlap
//   - Y: sem rank 4, no lexical overlap
//
// Pure semantic top-2 = [A, X]; RRF (K=60) top-2 = [A, B].
func TestSearchEpisodic_RRF_LexicalRescue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := makeEpisodic("p", "go", "debug", "banana theme unrelated")
	a.ContextVector = []float32{1, 0, 0}
	require.NoError(t, s.WriteEpisodic(ctx, a))

	b := makeEpisodic("p", "go", "debug", "golden banana pendant")
	b.ContextVector = []float32{0, 1, 0}
	require.NoError(t, s.WriteEpisodic(ctx, b))

	x := makeEpisodic("p", "go", "debug", "unrelated noise")
	x.ContextVector = []float32{0.9, 0.1, 0}
	require.NoError(t, s.WriteEpisodic(ctx, x))

	y := makeEpisodic("p", "go", "debug", "more unrelated noise")
	y.ContextVector = []float32{0, 0, 1}
	require.NoError(t, s.WriteEpisodic(ctx, y))

	// Control: pure semantic ranking (no Query) keeps B out of top-2.
	semantic, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Embedding: []float64{1, 0, 0},
		Limit:     2,
	})
	require.NoError(t, err)
	require.Len(t, semantic, 2)
	assert.Equal(t, a.ID, semantic[0].ID)
	assert.Equal(t, x.ID, semantic[1].ID, "pure semantic top-2 must not contain the lexical-only hit")

	// RRF: the lexical channel rescues B into top-2.
	fused, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Embedding: []float64{1, 0, 0},
		Query:     "golden banana pendant",
		Limit:     2,
	})
	require.NoError(t, err)
	require.Len(t, fused, 2)
	assert.Equal(t, a.ID, fused[0].ID)
	assert.Equal(t, b.ID, fused[1].ID, "lexical-rank-1 memory must surface via fusion")
}

// TestSearchEpisodic_RRF_VectorlessRescue verifies the RRF lexical channel
// also ranks memories without a context vector, which the semantic channel
// skips entirely — a pinpoint-fact recall win.
func TestSearchEpisodic_RRF_VectorlessRescue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	vec := makeEpisodic("p", "go", "debug", "unrelated topic")
	vec.ContextVector = []float32{1, 0, 0}
	require.NoError(t, s.WriteEpisodic(ctx, vec))

	// No ContextVector: the pure semantic path excludes it from ranking.
	fact := makeEpisodic("p", "go", "debug", "golden banana pendant")
	require.NoError(t, s.WriteEpisodic(ctx, fact))

	semantic, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Embedding: []float64{1, 0, 0},
		Limit:     2,
	})
	require.NoError(t, err)
	require.Len(t, semantic, 1)
	assert.Equal(t, vec.ID, semantic[0].ID)

	// RRF: the vectorless memory enters the ranking through the lexical
	// channel and is returned (it ties the semantic-only candidate at the
	// same fused score — the exact order is a tie-break, not the claim).
	fused, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Embedding: []float64{1, 0, 0},
		Query:     "golden banana",
		Limit:     2,
	})
	require.NoError(t, err)
	require.Len(t, fused, 2)
	assert.Contains(t, []any{fused[0].ID, fused[1].ID}, fact.ID,
		"vectorless exact-keyword memory must surface via fusion")
}

// TestSearchEpisodic_RRF_NotBelowSemantic guards the "quality not below the
// pure semantic baseline" criterion: when the semantic channel already has
// the right answer, RRF keeps it on top.
func TestSearchEpisodic_RRF_NotBelowSemantic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	best := makeEpisodic("p", "go", "debug", "golden banana pendant")
	best.ContextVector = []float32{1, 0, 0}
	require.NoError(t, s.WriteEpisodic(ctx, best))

	other := makeEpisodic("p", "go", "debug", "unrelated noise")
	other.ContextVector = []float32{0.9, 0.1, 0}
	require.NoError(t, s.WriteEpisodic(ctx, other))

	for _, rrfK := range []int{0, 1, 60} {
		res, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
			ProjectID: "p",
			Embedding: []float64{1, 0, 0},
			Query:     "golden banana pendant",
			RrfK:      rrfK,
			Limit:     2,
		})
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, best.ID, res[0].ID, "K=%d must keep the semantic winner on top", rrfK)
	}
}

// TestSearchEpisodic_RRF_KAdjustable proves the K parameter changes fusion
// ordering: M ranks semantic-1 only; N ranks semantic-2 AND lexical-30
// (29 filler summaries overlap the query more than N). With a small K the
// single strong rank wins (M first); with the default K the deep second list
// presence wins (N first).
func TestSearchEpisodic_RRF_KAdjustable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := makeEpisodic("p", "go", "debug", "unrelated noise")
	m.ContextVector = []float32{1, 0, 0}
	require.NoError(t, s.WriteEpisodic(ctx, m))

	n := makeEpisodic("p", "go", "debug", "target xyz")
	n.ContextVector = []float32{0.9, 0.1, 0}
	require.NoError(t, s.WriteEpisodic(ctx, n))

	// 29 vectorless fillers with maximal lexical overlap: they take lexical
	// ranks 1..29, pushing N to rank 30.
	for i := 0; i < 29; i++ {
		f := makeEpisodic("p", "go", "debug", "target target target")
		require.NoError(t, s.WriteEpisodic(ctx, f))
	}

	req := store.EpisodicSearchRequest{
		ProjectID: "p",
		Embedding: []float64{1, 0, 0},
		Query:     "target",
		Limit:     32,
	}

	run := func(rrfK int) (idxM, idxN int) {
		req.RrfK = rrfK
		res, err := s.SearchEpisodic(ctx, req)
		require.NoError(t, err)
		idxM, idxN = -1, -1
		for i, r := range res {
			if r.ID == m.ID {
				idxM = i
			}
			if r.ID == n.ID {
				idxN = i
			}
		}
		return idxM, idxN
	}

	idxM, idxN := run(1)
	require.GreaterOrEqual(t, idxM, 0)
	require.GreaterOrEqual(t, idxN, 0)
	assert.Less(t, idxM, idxN, "K=1: single strong semantic rank beats deep fusion")

	for _, rrfK := range []int{0, 60} {
		idxM, idxN = run(rrfK)
		assert.Less(t, idxN, idxM, "K=%d (default): deep second-list presence beats single rank", rrfK)
	}
}

// TestSearchEpisodic_RRF_SurpriseBoostCoexist verifies SurpriseBoost applies
// on top of the fused score: with A and B tied at the same fused score, a
// high-surprise A edges above B only when the boost is on.
func TestSearchEpisodic_RRF_SurpriseBoostCoexist(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := makeEpisodic("p", "go", "debug", "unrelated noise")
	a.ContextVector = []float32{1, 0, 0}
	a.SurpriseScore = 1.0
	require.NoError(t, s.WriteEpisodic(ctx, a))

	b := makeEpisodic("p", "go", "debug", "golden banana pendant")
	b.SurpriseScore = 0
	require.NoError(t, s.WriteEpisodic(ctx, b))

	// Without boost: equal fused scores — both present, order unspecified.
	plain, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Embedding: []float64{1, 0, 0},
		Query:     "golden banana",
		Limit:     2,
	})
	require.NoError(t, err)
	require.Len(t, plain, 2)

	boosted, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:     "p",
		Embedding:     []float64{1, 0, 0},
		Query:         "golden banana",
		SurpriseBoost: 0.5,
		Limit:         2,
	})
	require.NoError(t, err)
	require.Len(t, boosted, 2)
	assert.Equal(t, a.ID, boosted[0].ID, "high-surprise memory must rank first when boost is on")
}

// TestSearchEpisodic_RRF_HardConstraints verifies RRF never bypasses the hard
// constraints: project/language/task_type filtering happens in SQL before any
// fusion ranking, so cross-constraint exact-keyword matches stay out.
func TestSearchEpisodic_RRF_HardConstraints(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	home := makeEpisodic("p", "go", "debug", "golden banana pendant")
	require.NoError(t, s.WriteEpisodic(ctx, home))

	otherProj := makeEpisodic("other", "go", "debug", "golden banana pendant")
	otherProj.ContextVector = []float32{1, 0, 0}
	require.NoError(t, s.WriteEpisodic(ctx, otherProj))

	otherLang := makeEpisodic("p", "py", "debug", "golden banana pendant")
	otherLang.ContextVector = []float32{1, 0, 0}
	require.NoError(t, s.WriteEpisodic(ctx, otherLang))

	otherTask := makeEpisodic("p", "go", "feature", "golden banana pendant")
	otherTask.ContextVector = []float32{1, 0, 0}
	require.NoError(t, s.WriteEpisodic(ctx, otherTask))

	res, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Language:  "go",
		TaskType:  "debug",
		Embedding: []float64{1, 0, 0},
		Query:     "golden banana",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, home.ID, res[0].ID)
}

// TestSearchEpisodic_RRF_TimeSort verifies rank-then-sort with RRF: among the
// top relevant memories, SortByOccurredAt orders by occurred_at descending.
func TestSearchEpisodic_RRF_TimeSort(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	older := makeEpisodic("p", "go", "debug", "golden banana")
	older.ContextVector = []float32{1, 0, 0}
	older.OccurredAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.WriteEpisodic(ctx, older))

	newer := makeEpisodic("p", "go", "debug", "golden banana too")
	newer.ContextVector = []float32{0.9, 0.1, 0}
	newer.OccurredAt = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.WriteEpisodic(ctx, newer))

	byRelevance, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Embedding: []float64{1, 0, 0},
		Query:     "golden banana",
		Limit:     2,
	})
	require.NoError(t, err)
	require.Len(t, byRelevance, 2)
	assert.Equal(t, older.ID, byRelevance[0].ID, "relevance-first ordering")

	byTime, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:        "p",
		Embedding:        []float64{1, 0, 0},
		Query:            "golden banana",
		SortByOccurredAt: true,
		Limit:            2,
	})
	require.NoError(t, err)
	require.Len(t, byTime, 2)
	assert.Equal(t, newer.ID, byTime[0].ID, "rank-then-sort: most recent of the relevant first")
}

// TestSearchEpisodic_QueryWithoutEmbedding verifies Query alone does not
// activate RRF: plain structured queries keep weight ordering exactly.
func TestSearchEpisodic_QueryWithoutEmbedding(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	low := makeEpisodic("p", "go", "debug", "golden banana pendant")
	low.Weight = 0.3
	require.NoError(t, s.WriteEpisodic(ctx, low))

	high := makeEpisodic("p", "go", "debug", "unrelated noise")
	high.Weight = 0.9
	require.NoError(t, s.WriteEpisodic(ctx, high))

	res, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "p",
		Query:     "golden banana",
		Limit:     2,
	})
	require.NoError(t, err)
	require.Len(t, res, 2)
	assert.Equal(t, high.ID, res[0].ID, "weight ordering must be untouched without Embedding")
}

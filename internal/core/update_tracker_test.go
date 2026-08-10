package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

// ── Fixtures ─────────────────────────────────────────────────────────────────

// factMem builds a minimal episodic memory with an explicit event time.
func factMem(id uuid.UUID, summary, entityGroup string, occurred time.Time) models.EpisodicMemory {
	return models.EpisodicMemory{
		ID:          id,
		Summary:     summary,
		EntityGroup: entityGroup,
		OccurredAt:  occurred,
	}
}

func storeMem(t *testing.T, s *memory.Store, m models.EpisodicMemory) {
	t.Helper()
	require.NoError(t, s.WriteEpisodic(context.Background(), &m))
}

// ── DetectUpdate ─────────────────────────────────────────────────────────────

func TestDetectUpdate_SameSubjectNewerCovers(t *testing.T) {
	tracker := DefaultUpdateTracker()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	new := factMem(uuid.New(), "The user now prefers SQLite", "", t2)
	old := factMem(uuid.New(), "The user prefers PostgreSQL", "", t1)

	pairs := tracker.DetectUpdate(new, []models.EpisodicMemory{old})
	require.Len(t, pairs, 1)
	assert.Equal(t, old.ID, pairs[0].OldID)
	assert.Equal(t, old.EffectiveOccurredAt(), pairs[0].OldOccurredAt)
	assert.Equal(t, new.EffectiveOccurredAt(), pairs[0].NewOccurredAt)
	// Known-good Dice for the fixture pair (computed independently): 0.694.
	assert.InDelta(t, 0.694, pairs[0].BigramSimilarity, 0.01)
	assert.Contains(t, pairs[0].Reason, "covers")
}

func TestDetectUpdate_NotNewerIsNoUpdate(t *testing.T) {
	tracker := DefaultUpdateTracker()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	// New event is OLDER than the candidate: no update.
	newOlder := factMem(uuid.New(), "The user now prefers SQLite", "", t1)
	old := factMem(uuid.New(), "The user prefers PostgreSQL", "", t2)
	assert.Empty(t, tracker.DetectUpdate(newOlder, []models.EpisodicMemory{old}))

	// New event is exactly AS OLD as the candidate (same batch): no update.
	newSame := factMem(uuid.New(), "The user now prefers SQLite", "", t2)
	assert.Empty(t, tracker.DetectUpdate(newSame, []models.EpisodicMemory{old}))
}

func TestDetectUpdate_DifferentSubjectIsNoUpdate(t *testing.T) {
	tracker := DefaultUpdateTracker()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	new := factMem(uuid.New(), "Carol plans to adopt a beagle puppy", "", t2)
	old := factMem(uuid.New(), "Alice got a shell necklace in Hawaii", "", t1)
	assert.Empty(t, tracker.DetectUpdate(new, []models.EpisodicMemory{old}))
}

func TestDetectUpdate_ExactRestatementIsNoUpdate(t *testing.T) {
	// An exact restatement must stay on the pattern-separation append path
	// (merge), not retire the original memory.
	tracker := DefaultUpdateTracker()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	new := factMem(uuid.New(), "The user prefers PostgreSQL", "", t2)
	old := factMem(uuid.New(), "The user prefers PostgreSQL", "", t1)
	assert.Empty(t, tracker.DetectUpdate(new, []models.EpisodicMemory{old}))
}

func TestDetectUpdate_ContainedExtensionIsRestatement(t *testing.T) {
	// A more complete version that CONTAINS the older statement ("alpha beta
	// gamma" → "alpha beta gamma delta") carries all of the old information:
	// it stays on the pattern-separation append path instead of retiring the
	// original.
	tracker := DefaultUpdateTracker()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	new := factMem(uuid.New(), "alpha beta gamma delta", "", t2)
	old := factMem(uuid.New(), "alpha beta gamma", "", t1)
	assert.Empty(t, tracker.DetectUpdate(new, []models.EpisodicMemory{old}))
}

func TestDetectUpdate_PolarityFlipIsUpdateDespiteHighSimilarity(t *testing.T) {
	// A directive reversal (不要 → 应该) shares nearly all bigrams with the
	// old statement, but it is a genuine update: the restatement guard must
	// yield to the opposite-polarity signal.
	tracker := DefaultUpdateTracker()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	new := factMem(uuid.New(), "Redis MaxOpenConns 应该设为 100", "", t2)
	old := factMem(uuid.New(), "Redis MaxOpenConns 不要设为 100", "", t1)

	pairs := tracker.DetectUpdate(new, []models.EpisodicMemory{old})
	require.Len(t, pairs, 1)
	assert.Equal(t, PolarityProhibitive, pairs[0].PolarityOld)
	assert.Equal(t, PolarityAffirmative, pairs[0].PolarityNew)
	assert.Contains(t, pairs[0].Reason, "polarity flip")
}

func TestDetectUpdate_AlreadyObsoleteCandidateSkipped(t *testing.T) {
	// Idempotency: a retired memory is never judged again, so a second
	// detection pass finds nothing to do.
	tracker := DefaultUpdateTracker()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)
	now := time.Date(2029, 5, 4, 0, 0, 0, 0, time.UTC)

	new := factMem(uuid.New(), "The user now prefers SQLite", "", t2)
	old := factMem(uuid.New(), "The user prefers PostgreSQL", "", t1)
	old.ObsoletedAt = &now

	assert.Empty(t, tracker.DetectUpdate(new, []models.EpisodicMemory{old}))
}

func TestDetectUpdate_EntityGroupConstraint(t *testing.T) {
	tracker := DefaultUpdateTracker()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	// Both sides anchor on DIFFERENT entities: subject overlap is not enough.
	newA := factMem(uuid.New(), "The user now prefers SQLite", "alice", t2)
	oldB := factMem(uuid.New(), "The user prefers PostgreSQL", "bob", t1)
	assert.Empty(t, tracker.DetectUpdate(newA, []models.EpisodicMemory{oldB}))

	// Same entity group: detected.
	oldA := factMem(uuid.New(), "The user prefers PostgreSQL", "alice", t1)
	pairs := tracker.DetectUpdate(newA, []models.EpisodicMemory{oldA})
	require.Len(t, pairs, 1)

	// One side unanchored: subject similarity decides.
	oldFree := factMem(uuid.New(), "The user prefers PostgreSQL", "", t1)
	assert.Len(t, tracker.DetectUpdate(newA, []models.EpisodicMemory{oldFree}), 1)
}

// ── ApplyUpdate ──────────────────────────────────────────────────────────────

func TestApplyUpdate_RetiresCoveredRecordsSupersessionAndBoostsCarrier(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2029, 5, 5, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	s := memory.NewStore()
	oldID := uuid.New()
	carrierID := uuid.New()
	storeMem(t, s, models.EpisodicMemory{
		ID: oldID, ProjectID: "proj", Summary: "The user prefers PostgreSQL",
		OccurredAt: t1, Weight: 0.5,
	})
	carrier := models.EpisodicMemory{
		ID: carrierID, ProjectID: "proj", Summary: "The user now prefers SQLite",
		OccurredAt: t2, Weight: 0.4,
	}
	storeMem(t, s, carrier)

	tracker := DefaultUpdateTracker()
	pair := UpdatePair{OldID: oldID, Reason: "newer memory covers older memory"}
	applied, err := tracker.ApplyUpdate(ctx, s, carrier, []UpdatePair{pair}, now)
	require.NoError(t, err)
	require.Len(t, applied.AppliedPairs, 1)
	assert.InDelta(t, 0.4*1.5, applied.CarrierWeight, 0.0001, "default boost 1.5")

	// Covered memory is retired.
	old, err := s.GetEpisodic(ctx, oldID)
	require.NoError(t, err)
	require.NotNil(t, old.ObsoletedAt)
	assert.True(t, old.ObsoletedAt.Equal(now))

	// Supersession is recorded as a traceable edge with an explanation.
	edges, err := s.GetEdges(ctx, carrierID, "SUPERSEDED", "outgoing")
	require.NoError(t, err)
	require.Len(t, edges, 1)
	assert.Equal(t, oldID, edges[0].ToID)
	assert.Contains(t, edges[0].Properties["reason"], "covers")

	// Carrier weight is boosted in the store.
	got, err := s.GetEpisodic(ctx, carrierID)
	require.NoError(t, err)
	assert.InDelta(t, 0.6, got.Weight, 0.0001)

	// No pairs → no-op, weight untouched.
	noop, err := tracker.ApplyUpdate(ctx, s, carrier, nil, now)
	require.NoError(t, err)
	assert.Empty(t, noop.AppliedPairs)
	assert.Equal(t, 0.0, noop.CarrierWeight)
}

func TestApplyUpdate_CustomBoost(t *testing.T) {
	ctx := context.Background()
	s := memory.NewStore()
	now := time.Now().UTC()
	oldID := uuid.New()
	carrierID := uuid.New()
	storeMem(t, s, models.EpisodicMemory{ID: oldID, Summary: "old fact", Weight: 0.5})
	carrier := models.EpisodicMemory{ID: carrierID, Summary: "new fact", Weight: 0.4}
	storeMem(t, s, carrier)

	tracker := DefaultUpdateTracker()
	tracker.WeightBoost = 2.0
	applied, err := tracker.ApplyUpdate(ctx, s, carrier, []UpdatePair{{OldID: oldID}}, now)
	require.NoError(t, err)
	assert.InDelta(t, 0.8, applied.CarrierWeight, 0.0001)
}

// ── RunUpdateTracking (explicit signal) ──────────────────────────────────────

func TestRunUpdateTracking_EndToEnd(t *testing.T) {
	ctx := context.Background()
	s := memory.NewStore()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	oldID := uuid.New()
	newID := uuid.New()
	storeMem(t, s, models.EpisodicMemory{
		ID: oldID, ProjectID: "proj", Summary: "The user prefers PostgreSQL",
		OccurredAt: t1, Weight: 0.5, CreatedAt: t1,
	})
	storeMem(t, s, models.EpisodicMemory{
		ID: newID, ProjectID: "proj", Summary: "The user now prefers SQLite",
		OccurredAt: t2, Weight: 0.4, CreatedAt: t2,
	})

	res, err := RunUpdateTracking(ctx, s, newID, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Detected)
	assert.Equal(t, 1, res.Applied)
	require.Len(t, res.Pairs, 1)
	assert.Equal(t, oldID, res.Pairs[0].OldID)
	assert.InDelta(t, 0.6, res.CarrierWeight, 0.0001)

	old, err := s.GetEpisodic(ctx, oldID)
	require.NoError(t, err)
	require.NotNil(t, old.ObsoletedAt)

	edges, err := s.GetEdges(ctx, newID, "SUPERSEDED", "outgoing")
	require.NoError(t, err)
	require.Len(t, edges, 1)
	assert.Equal(t, oldID, edges[0].ToID)
}

func TestRunUpdateTracking_Idempotent(t *testing.T) {
	ctx := context.Background()
	s := memory.NewStore()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	oldID := uuid.New()
	newID := uuid.New()
	storeMem(t, s, models.EpisodicMemory{
		ID: oldID, ProjectID: "proj", Summary: "The user prefers PostgreSQL",
		OccurredAt: t1, CreatedAt: t1,
	})
	storeMem(t, s, models.EpisodicMemory{
		ID: newID, ProjectID: "proj", Summary: "The user now prefers SQLite",
		OccurredAt: t2, CreatedAt: t2,
	})

	first, err := RunUpdateTracking(ctx, s, newID, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, first.Applied)

	// A second pass must find nothing: the covered memory is already retired,
	// and no duplicate edge or double-boost is produced.
	second, err := RunUpdateTracking(ctx, s, newID, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, second.Detected)
	assert.Equal(t, 0, second.Applied)

	edges, err := s.GetEdges(ctx, newID, "SUPERSEDED", "outgoing")
	require.NoError(t, err)
	assert.Len(t, edges, 1, "re-running must not duplicate supersession edges")
}

func TestRunUpdateTracking_BoostObservableOnDecayedCarrier(t *testing.T) {
	// A fresh memory is already at weight 1.0, so the x1.5 boost saturates at
	// the clamp and is invisible; on a decayed carrier the promotion must be
	// observable through the full explicit-signal flow.
	ctx := context.Background()
	s := memory.NewStore()
	t1 := time.Date(2029, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2029, 5, 3, 0, 0, 0, 0, time.UTC)

	oldID := uuid.New()
	newID := uuid.New()
	storeMem(t, s, models.EpisodicMemory{
		ID: oldID, ProjectID: "proj", Summary: "The user prefers PostgreSQL",
		OccurredAt: t1, CreatedAt: t1, Weight: 0.2,
	})
	storeMem(t, s, models.EpisodicMemory{
		ID: newID, ProjectID: "proj", Summary: "The user now prefers SQLite",
		OccurredAt: t2, CreatedAt: t2, Weight: 0.2,
	})

	res, err := RunUpdateTracking(ctx, s, newID, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	assert.InDelta(t, 0.3, res.CarrierWeight, 0.0001,
		"0.2 x default boost 1.5 — the promotion must be visible in the result")

	got, err := s.GetEpisodic(ctx, newID)
	require.NoError(t, err)
	assert.InDelta(t, 0.3, got.Weight, 0.0001,
		"the boosted weight must be persisted, not just reported")

	old, err := s.GetEpisodic(ctx, oldID)
	require.NoError(t, err)
	assert.Greater(t, got.Weight, old.Weight,
		"the promoted carrier must outrank the retired memory")
}

func TestRunUpdateTracking_MemoryNotFound(t *testing.T) {
	_, err := RunUpdateTracking(context.Background(), memory.NewStore(), uuid.New(), 0)
	require.Error(t, err)
}

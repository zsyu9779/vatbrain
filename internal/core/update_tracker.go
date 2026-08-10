package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// UpdatePair is one detected "temporally newer memory covers an older memory"
// judgement (v0.4 Update Tracking). The semantic basis — same subject via
// character-bigram Dice, polarity when both sides carry directives — is
// reused from conflict detection; the temporal recency is the new signal:
// the newer statement about the same subject covers the old state.
type UpdatePair struct {
	// OldID is the memory being covered (superseded).
	OldID uuid.UUID `json:"old_id"`
	// OldOccurredAt is when the covered memory's event happened.
	OldOccurredAt time.Time `json:"old_occurred_at"`
	// NewOccurredAt is when the covering memory's event happened.
	NewOccurredAt time.Time `json:"new_occurred_at"`
	// BigramSimilarity is the same-subject Dice coefficient (reused from the
	// conflict detector).
	BigramSimilarity float64 `json:"bigram_similarity"`
	// PolarityOld/New carry the directive polarity of each side when
	// DetectPolarity recognizes one, so a directive reversal is explainable.
	PolarityOld Polarity `json:"polarity_old,omitempty"`
	PolarityNew Polarity `json:"polarity_new,omitempty"`
	// Reason is the human-readable explanation of the judgement.
	Reason string `json:"reason"`
}

// UpdateTracker judges when a temporally newer memory about the same subject
// covers an older memory, and makes the judgement effective. It reuses the
// conflict detector's same-subject basis (character-bigram Dice + polarity)
// and the store's existing retirement/weight/edge primitives — it does not
// build a parallel storage mechanism.
type UpdateTracker struct {
	// MinBigramSimilarity is the Dice coefficient above which two memories
	// count as being about the same subject — the same default (0.25) as
	// ConflictDetector. Below it, the newer statement is a different subject
	// and cannot cover the older one.
	MinBigramSimilarity float64
	// RestatementSimilarity is the Dice coefficient at or above which the new
	// statement is a restatement of the old one and is left to the
	// pattern-separation append path instead of retiring the old memory.
	// Default 0.9. A summary that CONTAINS the other as a substring is also a
	// restatement (the longer one carries the shorter's information, so
	// nothing is lost by appending). A directive polarity flip
	// (prohibitive ↔ affirmative) overrides the restatement guard: an
	// explicit reversal is an update even when it shares nearly all bigrams.
	RestatementSimilarity float64
	// WeightBoost is the multiplier applied to the carrier (newer) memory's
	// weight when an update is applied — consistent with
	// ReconsolidationEngine.CorrectionBoost. Default 1.5.
	WeightBoost float64
}

// DefaultUpdateTracker returns an UpdateTracker with tuned defaults.
func DefaultUpdateTracker() *UpdateTracker {
	return &UpdateTracker{
		MinBigramSimilarity:  0.25,
		RestatementSimilarity: 0.9,
		WeightBoost:          1.5,
	}
}

// DetectUpdate returns the candidate memories that newMem covers: same
// subject (bigram basis reused from conflict detection), same entity when
// both sides anchor on one, strictly newer event time, and content that is
// not an exact restatement (unless the directives flip). It is pure (no I/O)
// so the judgement is unit-testable in isolation. Already-retired candidates
// are skipped, which is what makes repeated passes idempotent.
func (t *UpdateTracker) DetectUpdate(newMem models.EpisodicMemory, candidates []models.EpisodicMemory) []UpdatePair {
	minSim := t.MinBigramSimilarity
	if minSim <= 0 {
		minSim = 0.25
	}
	restatementSim := t.RestatementSimilarity
	if restatementSim <= 0 {
		restatementSim = 0.9
	}

	newPolarity, newHasPolarity := DetectPolarity(newMem.Summary)
	newOccurred := newMem.EffectiveOccurredAt()

	var pairs []UpdatePair
	for _, old := range candidates {
		if old.ID == newMem.ID {
			continue
		}
		if old.ObsoletedAt != nil {
			// Idempotency: a retired memory is never covered again.
			continue
		}
		// Same entity: when both sides anchor on an entity group, they must
		// match; an unanchored side lets the subject similarity decide.
		if newMem.EntityGroup != "" && old.EntityGroup != "" &&
			newMem.EntityGroup != old.EntityGroup {
			continue
		}

		sim := bigramDice(newMem.Summary, old.Summary)
		if sim < minSim {
			continue // different subject — cannot cover
		}

		oldPolarity, oldHasPolarity := DetectPolarity(old.Summary)
		polarityFlip := newHasPolarity && oldHasPolarity && newPolarity != oldPolarity
		restatement := sim >= restatementSim ||
			strings.Contains(newMem.Summary, old.Summary) ||
			strings.Contains(old.Summary, newMem.Summary)
		if restatement && !polarityFlip {
			continue // restatement — stays on the pattern-separation append path
		}

		oldOccurred := old.EffectiveOccurredAt()
		if !newOccurred.After(oldOccurred) {
			continue // not temporally newer — no coverage
		}

		reason := fmt.Sprintf("newer memory (occurred %s) covers older memory (occurred %s): same-subject bigram similarity %.2f",
			newOccurred.UTC().Format(time.RFC3339), oldOccurred.UTC().Format(time.RFC3339), sim)
		if polarityFlip {
			reason += fmt.Sprintf("; directive polarity flip %s -> %s", oldPolarity, newPolarity)
		}

		pairs = append(pairs, UpdatePair{
			OldID:            old.ID,
			OldOccurredAt:    oldOccurred,
			NewOccurredAt:    newOccurred,
			BigramSimilarity: sim,
			PolarityOld:      oldPolarity,
			PolarityNew:      newPolarity,
			Reason:           reason,
		})
	}
	return pairs
}

// AppliedUpdate reports what ApplyUpdate did.
type AppliedUpdate struct {
	// AppliedPairs are the pairs whose actions were taken.
	AppliedPairs []UpdatePair
	// CarrierWeight is the carrier memory's weight after the boost; 0 when
	// nothing was applied.
	CarrierWeight float64
}

// ApplyUpdate makes a detection effective: every covered memory is marked
// obsolete, a SUPERSEDED edge records the supersession from the carrier (the
// memory carrying the newer information) to each covered memory — the
// explainable, traceable trail — and the carrier's weight is boosted by
// WeightBoost. Idempotent by construction: DetectUpdate skips already
// retired memories, so re-applying a stale pair set is a no-op for the
// obsoletion and the weight boost is only applied when pairs exist.
func (t *UpdateTracker) ApplyUpdate(ctx context.Context, s store.MemoryStore, carrier models.EpisodicMemory, pairs []UpdatePair, now time.Time) (AppliedUpdate, error) {
	boost := t.WeightBoost
	if boost <= 0 {
		boost = 1.5
	}

	applied := AppliedUpdate{}
	for _, p := range pairs {
		if p.OldID == carrier.ID {
			continue
		}
		if err := s.MarkObsolete(ctx, p.OldID, now); err != nil {
			return applied, fmt.Errorf("update tracking: retire covered memory %s: %w", p.OldID, err)
		}
		if err := s.CreateEdge(ctx, carrier.ID, p.OldID, "SUPERSEDED", map[string]any{
			"at":     now.UTC(),
			"reason": p.Reason,
		}); err != nil {
			return applied, fmt.Errorf("update tracking: record supersession edge: %w", err)
		}
		applied.AppliedPairs = append(applied.AppliedPairs, p)
	}
	if len(applied.AppliedPairs) == 0 {
		return applied, nil
	}

	boosted := ClampWeight(carrier.Weight * boost)
	if err := s.UpdateEpisodicWeight(ctx, carrier.ID, boosted, carrier.EffectiveFrequency); err != nil {
		return applied, fmt.Errorf("update tracking: boost carrier weight: %w", err)
	}
	applied.CarrierWeight = boosted
	return applied, nil
}

// UpdateTrackingResult summarises an explicit update-signal pass.
type UpdateTrackingResult struct {
	// Detected is how many covered pairs the judgement found.
	Detected int `json:"detected"`
	// Applied is how many pairs were acted on.
	Applied int `json:"applied"`
	// Pairs carries the explainable judgements (old memory, times, basis).
	Pairs []UpdatePair `json:"pairs"`
	// CarrierWeight is the newer memory's weight after the boost (0 when
	// nothing was applied).
	CarrierWeight float64 `json:"carrier_weight"`
}

// updatePeerPool bounds the candidate pool for the explicit signal: the most
// recent memories of the project, enough to find a recently stated fact while
// keeping the bigram scan cheap.
const updatePeerPool = 500

// RunUpdateTracking is the explicit update signal: given a memory that
// carries newer information, find the project's active peers it covers and
// apply the update actions (retire the covered, boost the carrier, record
// SUPERSEDED edges). The peer pool is the project's most recent memories
// (time-ordered, which also bypasses the hot cache so the signal sees fresh
// writes). Idempotent: a second run finds nothing because the covered peers
// are already retired.
func RunUpdateTracking(ctx context.Context, s store.MemoryStore, memoryID uuid.UUID, boost float64) (UpdateTrackingResult, error) {
	mem, err := s.GetEpisodic(ctx, memoryID)
	if err != nil {
		return UpdateTrackingResult{}, fmt.Errorf("update tracking: fetch memory %s: %w", memoryID, err)
	}

	peers, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:       mem.ProjectID,
		Limit:           updatePeerPool,
		SortByOccurredAt: true,
	})
	if err != nil {
		return UpdateTrackingResult{}, fmt.Errorf("update tracking: fetch project peers: %w", err)
	}

	tracker := DefaultUpdateTracker()
	if boost > 0 {
		tracker.WeightBoost = boost
	}

	res := UpdateTrackingResult{Pairs: tracker.DetectUpdate(*mem, peers)}
	res.Detected = len(res.Pairs)
	if res.Detected == 0 {
		return res, nil
	}

	applied, err := tracker.ApplyUpdate(ctx, s, *mem, res.Pairs, time.Now().UTC())
	if err != nil {
		return res, err
	}
	res.Applied = len(applied.AppliedPairs)
	res.CarrierWeight = applied.CarrierWeight
	return res, nil
}

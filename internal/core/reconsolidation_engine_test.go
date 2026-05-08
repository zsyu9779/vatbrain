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

func TestReconsolidation_EpisodicDirect(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	now := time.Now().UTC()

	epID := uuid.New()
	err := s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID:                 epID,
		ProjectID:          "proj",
		Language:           "go",
		Summary:            "original bug description",
		Weight:             1.0,
		EffectiveFrequency: 1.0,
		TrustLevel:         3,
		CreatedAt:          now,
	})
	require.NoError(t, err)

	re := DefaultReconsolidationEngine()
	result, err := re.Process(ctx, s, epID, "episodic",
		models.CorrectionDetail{Original: "original", CorrectedTo: "fixed"},
		false) // not user-corrected
	require.NoError(t, err)

	assert.Equal(t, epID, result.CorrectedID)
	assert.Contains(t, result.UpdatedIDs, epID)

	ep, err := s.GetEpisodic(ctx, epID)
	require.NoError(t, err)
	assert.InDelta(t, 1.5, ep.Weight, 0.01) // 1.0 * 1.5
}

func TestReconsolidation_SemanticToEpisodic(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	now := time.Now().UTC()

	// Create 3 source episodics.
	var sourceIDs []uuid.UUID
	for i := 0; i < 3; i++ {
		id := uuid.New()
		sourceIDs = append(sourceIDs, id)
		err := s.WriteEpisodic(ctx, &models.EpisodicMemory{
			ID:                 id,
			ProjectID:          "proj",
			Language:           "go",
			Summary:            "source debug",
			Weight:             1.0,
			EffectiveFrequency: 1.0,
			TrustLevel:         3,
			CreatedAt:          now,
		})
		require.NoError(t, err)
	}

	// Create 1 semantic memory derived from them.
	semID := uuid.New()
	err := s.WriteSemantic(ctx, &models.SemanticMemory{
		ID:                 semID,
		Type:               models.MemoryTypePattern,
		Content:            "semantic rule",
		Weight:             1.0,
		EffectiveFrequency: 1.0,
		CreatedAt:          now,
	})
	require.NoError(t, err)

	// Create DERIVED_FROM edges.
	for _, epID := range sourceIDs {
		err := s.CreateEdge(ctx, semID, epID, "DERIVED_FROM", map[string]any{"run_id": "test-run"})
		require.NoError(t, err)
	}

	re := DefaultReconsolidationEngine()
	result, err := re.Process(ctx, s, semID, "semantic",
		models.CorrectionDetail{Original: "bad rule", CorrectedTo: "fixed rule"},
		true) // user-corrected
	require.NoError(t, err)

	assert.Equal(t, semID, result.CorrectedID)
	assert.Len(t, result.UpdatedIDs, 3)
	for _, epID := range sourceIDs {
		assert.Contains(t, result.UpdatedIDs, epID)
		ep, getErr := s.GetEpisodic(ctx, epID)
		require.NoError(t, getErr)
		assert.InDelta(t, 1.5, ep.Weight, 0.01)
		assert.Equal(t, models.TrustLevel(4), ep.TrustLevel) // user-corrected → +1
	}
}

func TestReconsolidation_NoTracebackChain(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	now := time.Now().UTC()

	semID := uuid.New()
	err := s.WriteSemantic(ctx, &models.SemanticMemory{
		ID:     semID,
		Type:   models.MemoryTypePattern,
		Weight: 1.0,
		CreatedAt: now,
	})
	require.NoError(t, err)
	// No DERIVED_FROM edges.

	re := DefaultReconsolidationEngine()
	result, err := re.Process(ctx, s, semID, "semantic",
		models.CorrectionDetail{Original: "rule", CorrectedTo: "better rule"},
		false)
	require.NoError(t, err)
	assert.Empty(t, result.UpdatedIDs)
	assert.Empty(t, result.SkippedIDs)
}

func TestReconsolidation_PartialSourceFailure(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	now := time.Now().UTC()

	// Create 1 valid source.
	epID := uuid.New()
	err := s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID:        epID,
		ProjectID: "proj",
		Language:  "go",
		Summary:   "valid",
		Weight:    1.0,
		CreatedAt: now,
	})
	require.NoError(t, err)

	// Create semantic + edges (including one to a non-existent episodic).
	semID := uuid.New()
	err = s.WriteSemantic(ctx, &models.SemanticMemory{
		ID:     semID,
		Type:   models.MemoryTypePattern,
		Weight: 1.0,
		CreatedAt: now,
	})
	require.NoError(t, err)

	err = s.CreateEdge(ctx, semID, epID, "DERIVED_FROM", map[string]any{"run_id": "test"})
	require.NoError(t, err)
	ghostID := uuid.New()
	err = s.CreateEdge(ctx, semID, ghostID, "DERIVED_FROM", map[string]any{"run_id": "test"})
	require.NoError(t, err)

	re := DefaultReconsolidationEngine()
	result, err := re.Process(ctx, s, semID, "semantic",
		models.CorrectionDetail{Original: "rule", CorrectedTo: "fixed"},
		false)
	require.NoError(t, err)
	assert.Len(t, result.UpdatedIDs, 1)
	assert.Contains(t, result.UpdatedIDs, epID)
	assert.Contains(t, result.SkippedIDs, ghostID) // ghost → skipped
}

func TestReconsolidation_UserCorrectedTrustBoost(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	now := time.Now().UTC()

	epID := uuid.New()
	err := s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID:        epID,
		ProjectID: "proj",
		Language:  "go",
		Summary:   "test",
		Weight:    1.0,
		TrustLevel: 3,
		CreatedAt: now,
	})
	require.NoError(t, err)

	re := DefaultReconsolidationEngine()

	// Run twice: first with user-corrected, second without.
	result, err := re.Process(ctx, s, epID, "episodic",
		models.CorrectionDetail{Original: "orig", CorrectedTo: "fix"},
		true)
	require.NoError(t, err)
	assert.Len(t, result.UpdatedIDs, 1)

	ep, _ := s.GetEpisodic(ctx, epID)
	assert.Equal(t, models.TrustLevel(4), ep.TrustLevel)

	// Second correction without user flag → no trust boost.
	result2, err := re.Process(ctx, s, epID, "episodic",
		models.CorrectionDetail{Original: "orig", CorrectedTo: "fix2"},
		false)
	require.NoError(t, err)
	assert.Len(t, result2.UpdatedIDs, 1)

	ep2, _ := s.GetEpisodic(ctx, epID)
	assert.Equal(t, models.TrustLevel(4), ep2.TrustLevel) // unchanged
}

func TestReconsolidation_CycleDetection(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	now := time.Now().UTC()

	epID := uuid.New()
	err := s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID:        epID,
		ProjectID: "proj",
		Language:  "go",
		Summary:   "test",
		Weight:    1.0,
		CreatedAt: now,
	})
	require.NoError(t, err)

	semID := uuid.New()
	err = s.WriteSemantic(ctx, &models.SemanticMemory{
		ID:     semID,
		Type:   models.MemoryTypePattern,
		Weight: 1.0,
		CreatedAt: now,
	})
	require.NoError(t, err)

	// Create bidirectional edges (artificial cycle).
	err = s.CreateEdge(ctx, semID, epID, "DERIVED_FROM", nil)
	require.NoError(t, err)
	err = s.CreateEdge(ctx, epID, semID, "DERIVED_FROM", nil)
	require.NoError(t, err)

	re := DefaultReconsolidationEngine()
	result, err := re.Process(ctx, s, semID, "semantic",
		models.CorrectionDetail{Original: "rule", CorrectedTo: "fixed"},
		false)
	require.NoError(t, err)
	// Should not infinite-loop — epID should be updated exactly once.
	assert.Len(t, result.UpdatedIDs, 1)
	assert.Contains(t, result.UpdatedIDs, epID)
}

func TestReconsolidation_UnknownType(t *testing.T) {
	s := memory.NewStore()
	re := DefaultReconsolidationEngine()
	_, err := re.Process(context.Background(), s, uuid.New(), "unknown",
		models.CorrectionDetail{}, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown corrected type")
}

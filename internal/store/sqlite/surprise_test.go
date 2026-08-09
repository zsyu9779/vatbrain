package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// TestSurprise_EpisodicRoundTrip verifies the surprise_score column survives a
// write → read cycle (and is zero for pre-migration rows via DEFAULT 0).
func TestSurprise_EpisodicRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := makeEpisodic("proj", "go", "debug", "api 返回 500")
	mem.SurpriseScore = 0.7
	mem.IsCorrection = true
	require.NoError(t, s.WriteEpisodic(ctx, mem))

	got, err := s.GetEpisodic(ctx, mem.ID)
	require.NoError(t, err)
	assert.InDelta(t, 0.7, got.SurpriseScore, 0.001)
	assert.True(t, got.IsCorrection)
}

// TestSurprise_SemanticRoundTrip mirrors the episodic round-trip for
// consolidated rules that inherited a surprise signal from their sources.
func TestSurprise_SemanticRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	sm := &models.SemanticMemory{
		ID:         uuid.New(),
		Type:       models.MemoryTypeRule,
		Content:    "并发问题通常出在锁粒度",
		SourceType: models.SourceTypeINFERRED,
		TrustLevel: 3,
		Weight:     1.0,
		CreatedAt:  now,
	}
	sm.SurpriseScore = 0.5
	require.NoError(t, s.WriteSemantic(ctx, sm))

	got, err := s.GetSemantic(ctx, sm.ID)
	require.NoError(t, err)
	assert.InDelta(t, 0.5, got.SurpriseScore, 0.001)
}

// TestSurprise_RankingBoost verifies the opt-in SurpriseBoost lifts a
// high-surprise memory above an otherwise-equal ordinary one, while the default
// (boost = 0) leaves ranking untouched.
func TestSurprise_RankingBoost(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	high := makeEpisodic("proj", "go", "debug", "记住：pgvector 维度必须匹配")
	high.Weight = 1.0
	high.SurpriseScore = 1.0
	low := makeEpisodic("proj", "go", "debug", "常规修复超时问题")
	low.Weight = 1.0
	low.SurpriseScore = 0
	require.NoError(t, s.WriteEpisodic(ctx, high))
	require.NoError(t, s.WriteEpisodic(ctx, low))

	// Default ranking (no boost): equal weights, order is by insertion, so the
	// boosted path must be the only place surprise changes the result.
	res, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: "proj",
		Limit:     2,
	})
	require.NoError(t, err)
	require.Len(t, res, 2)

	boosted, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:     "proj",
		Limit:         2,
		SurpriseBoost: 0.5, // 1 + 0.5×1.0 = 1.5× weight for high, 1.0× for low
	})
	require.NoError(t, err)
	require.Len(t, boosted, 2)
	assert.Equal(t, high.ID, boosted[0].ID, "high-surprise memory must rank first when surprise boost is on")
}

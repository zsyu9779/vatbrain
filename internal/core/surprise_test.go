package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

func TestSurpriseScorer_UserConfirmedIsZero(t *testing.T) {
	s := DefaultSurpriseScorer()
	assert.Equal(t, 0.0, s.Score(WriteEvent{
		Summary:       "记住：不要用文本 gsub 覆写 OpenClash 脚本",
		UserConfirmed: true,
		IsCorrection:  true, // confirmation wins over correction signal
	}))
}

func TestSurpriseScorer_PlainEventIsZero(t *testing.T) {
	s := DefaultSurpriseScorer()
	assert.Equal(t, 0.0, s.Score(WriteEvent{Summary: "日常修复一个拼写错误"}))
}

func TestSurpriseScorer_CorrectionIsHigh(t *testing.T) {
	s := DefaultSurpriseScorer()
	got := s.Score(WriteEvent{Summary: "不对，应该是数组下标越界", IsCorrection: true})
	assert.InDelta(t, 0.7, got, 0.001)
}

func TestSurpriseScorer_BehaviorChangeAdds(t *testing.T) {
	s := DefaultSurpriseScorer()
	got := s.Score(WriteEvent{Summary: "换用了新的配置方式", CausedBehaviorChange: true})
	assert.InDelta(t, 0.5, got, 0.001)

	// Correction + behavior change clamps at the maximum.
	both := s.Score(WriteEvent{IsCorrection: true, CausedBehaviorChange: true})
	assert.Equal(t, 1.0, both)
}

func TestSurpriseDecayScale_Boundaries(t *testing.T) {
	e := DefaultWeightDecayEngine()
	assert.Equal(t, 1.0, e.SurpriseDecayScale(0))
	// With default boost 2.0: surprise=1 → 1/(1+2) = 1/3 → 3× half-life.
	assert.InDelta(t, 1.0/3.0, e.SurpriseDecayScale(1.0), 0.001)
	assert.InDelta(t, 1.0/2.0, e.SurpriseDecayScale(0.5), 0.001)
	// Out-of-range input clamps to [0, 1].
	assert.InDelta(t, 1.0/3.0, e.SurpriseDecayScale(5.0), 0.001)
}

func TestWeightWithSurprise_AtCreationEqual(t *testing.T) {
	e := DefaultWeightDecayEngine()
	now := time.Now()

	w0 := e.WeightWithSurprise(1.0, now, now, now, 0)
	w1 := e.WeightWithSurprise(1.0, now, now, now, 1.0)
	// At creation time decay hasn't accumulated, so surprise changes nothing.
	assert.InDelta(t, w0, w1, 0.001)
	assert.InDelta(t, 1.0, w0, 0.001)
}

// TestWeightWithSurprise_SevenDaySurvival is the ROADMAP v0.3 acceptance check:
// "高惊讶度事件 7 天后权重显著高于同期低惊讶度事件" — after a week of equal
// age and inactivity, a fully-surprising memory must still outweigh an ordinary
// one by a clearly significant margin.
func TestWeightWithSurprise_SevenDaySurvival(t *testing.T) {
	e := DefaultWeightDecayEngine()
	now := time.Now()
	created := now.Add(-7 * 24 * time.Hour)
	lastAccessed := created

	low := e.WeightWithSurprise(1.0, created, lastAccessed, now, 0)
	high := e.WeightWithSurprise(1.0, created, lastAccessed, now, 1.0)

	// e^(-0.005·7·1/3)·e^(-0.05·7·1/3) / e^(-0.005·7)·e^(-0.05·7) ≈ 1.29
	assert.Greater(t, high, low*1.2, "high-surprise memory should retain ≥20% more weight after 7 days")
	assert.Less(t, high, low*1.5, "3× half-life cap should bound the gap")
}

// TestWriteMemory_PersistsSurprise exercises the write pipeline end to end: a
// correction event passes the gate via prediction_error and its surprise score
// lands on the persisted memory; a plain event persists with zero surprise.
func TestWriteMemory_PersistsSurprise(t *testing.T) {
	ctx := context.Background()
	deps := WriteDeps{
		Store:       memory.NewStore(),
		Gate:        DefaultSignificanceGate(),
		PatternSep:  DefaultPatternSeparation(),
		WeightDecay: DefaultWeightDecayEngine(),
		Embedder:    embedder.NewStubEmbedder(),
		Surprise:    DefaultSurpriseScorer(),
	}

	// Correction event → gate passes (prediction_error), surprise = 0.7.
	res, err := WriteMemory(ctx, deps,
		WriteEvent{Summary: "不对，应该用子查询而不是 JOIN", IsCorrection: true},
		"proj", "sql", "analytics_query", models.TaskTypeDebug)
	require.NoError(t, err)
	require.True(t, res.Persisted)

	mem, err := deps.Store.GetEpisodic(ctx, res.MemoryID)
	require.NoError(t, err)
	assert.True(t, mem.IsCorrection)
	assert.InDelta(t, 0.7, mem.SurpriseScore, 0.001)

	// Plain event → gate passes via cross-cycle (single write still persists
	// below threshold only when confirmed) — use a confirmed write to force
	// persistence, which must carry zero surprise.
	res2, err := WriteMemory(ctx, deps,
		WriteEvent{Summary: "记住：数据库连接池上限 100", UserConfirmed: true},
		"proj", "sql", "db_config", models.TaskTypeFeature)
	require.NoError(t, err)
	require.True(t, res2.Persisted)

	mem2, err := deps.Store.GetEpisodic(ctx, res2.MemoryID)
	require.NoError(t, err)
	assert.Equal(t, 0.0, mem2.SurpriseScore)
}

// TestReconsolidation_BumpsSurpriseOnUserCorrection verifies the surprise
// signal is written when a memory is the target of a user correction.
func TestReconsolidation_BumpsSurpriseOnUserCorrection(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	now := time.Now().UTC()

	epID := uuid.New()
	require.NoError(t, s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID:                 epID,
		ProjectID:          "proj",
		Language:           "go",
		Summary:            "original bug description",
		Weight:             1.0,
		EffectiveFrequency: 1.0,
		TrustLevel:         3,
		CreatedAt:          now,
	}))

	re := DefaultReconsolidationEngine()
	_, err := re.Process(ctx, s, epID, "episodic",
		models.CorrectionDetail{Original: "original", CorrectedTo: "fixed"},
		true)
	require.NoError(t, err)

	ep, err := s.GetEpisodic(ctx, epID)
	require.NoError(t, err)
	assert.InDelta(t, 0.7, ep.SurpriseScore, 0.001)
	assert.Equal(t, models.TrustLevel(4), ep.TrustLevel) // 3 → 4
}

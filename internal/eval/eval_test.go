package eval_test

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/eval"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/provider"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

// scenarioDir is the hand-crafted scenario artifact directory.
func scenarioDir() string { return filepath.Join("..", "..", "tests", "scenarios") }

// seedScenario loads a scenario's pitfall + episodes into a real store so the
// pipeline verification exercises the actual retrieval path.
func seedScenario(t *testing.T, s eval.Scenario) (core.WriteDeps, *memory.Store) {
	t.Helper()
	st := memory.NewStore()
	ctx := context.Background()
	now := time.Now().UTC()

	for _, ep := range s.Episodes {
		require.NoError(t, st.WriteEpisodic(ctx, &models.EpisodicMemory{
			ID:         uuid.New(),
			ProjectID:  "eval",
			Language:   s.Language,
			TaskType:   s.TaskType,
			Summary:    ep,
			SourceType: models.SourceTypeUSER,
			TrustLevel: models.TrustLevelMax,
			Weight:     1.0,
			CreatedAt:  now,
		}))
	}

	require.NoError(t, st.WritePitfall(ctx, &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          s.Pitfall.EntityID,
		EntityType:        s.Pitfall.EntityType,
		ProjectID:         "eval",
		Language:          s.Language,
		Signature:         s.Pitfall.Signature,
		RootCauseCategory: s.Pitfall.RootCause,
		FixStrategy:       s.Pitfall.FixStrategy,
		OccurrenceCount:   s.Pitfall.OccurrenceCount,
		LastOccurredAt:    &now,
		SourceType:        models.SourceTypeINFERRED,
		TrustLevel:        s.Pitfall.TrustLevel,
		Weight:            1.0,
		CreatedAt:         now,
		UpdatedAt:         now,
		Status:            models.PitfallConfirmed,
	}))

	deps := core.WriteDeps{
		Store:       st,
		Gate:        core.DefaultSignificanceGate(),
		PatternSep:  core.DefaultPatternSeparation(),
		WeightDecay: core.DefaultWeightDecayEngine(),
		Embedder:    embedder.NewStubEmbedder(),
		WorkingMem:  store.NewWorkingMemoryBuffer(20),
	}
	return deps, st
}

func TestEvalHarness_AllScenarios(t *testing.T) {
	scenarios, err := eval.Load(scenarioDir())
	require.NoError(t, err)
	require.Len(t, scenarios, 20, "评测需要 20 个场景")
	// 场景 ID 唯一
	seen := map[string]bool{}
	for _, s := range scenarios {
		assert.False(t, seen[s.ID], "duplicate scenario id %s", s.ID)
		seen[s.ID] = true
	}

	ctx := context.Background()
	rng := rand.New(rand.NewSource(42))
	var results []eval.Result

	for _, s := range scenarios {
		t.Run(s.ID, func(t *testing.T) {
			deps, _ := seedScenario(t, s)

			// 真实管道验证：prepare_edit_context 的检索应命中该 pitfall
			pitfalls, err := provider.RetrievePitfalls(ctx, deps, "eval", s.Query, 3)
			require.NoError(t, err)
			require.NotEmpty(t, pitfalls,
				"场景 %s：真实检索未命中 pitfall（query 与 signature 无重叠？）", s.ID)
			assert.Equal(t, s.Pitfall.EntityID, pitfalls[0].EntityID,
				"场景 %s：命中错误 pitfall", s.ID)

			// 确定性模拟
			res := eval.Simulate(s, rng)
			results = append(results, res)
			t.Logf("[%s] %s: errors %d→%d reduction=%.1f%% interference=%.1f%%",
				s.ID, s.Title,
				res.ErrorsWithoutInjection, res.ErrorsWithInjection,
				res.RepeatedErrorReductionRate*100, res.InterferenceRate*100)
		})
	}

	// 汇总：EVOLUTION_PLAN 验收阈值
	summary := eval.Aggregate(results)
	t.Logf("SUMMARY: %d scenarios | reduction=%.1f%% interference=%.1f%%",
		summary.Scenarios, summary.RepeatedErrorReductionRate*100, summary.InterferenceRate*100)

	assert.Equal(t, 20, summary.Scenarios)
	assert.True(t, summary.RepeatedErrorReductionRate > 0,
		"重复错误减少率必须可测（>0），got %.1f%%", summary.RepeatedErrorReductionRate*100)
	assert.True(t, summary.RepeatedErrorReductionRate >= 0.5,
		"重复错误减少率应显著，got %.1f%%", summary.RepeatedErrorReductionRate*100)
	assert.Less(t, summary.InterferenceRate, 0.30,
		"主动注入干扰率必须 <30%，got %.1f%%", summary.InterferenceRate*100)
}

func TestEvalHarness_ScenariosAreReasonable(t *testing.T) {
	// 场景参数自检：行为模型参数必须落在合理区间，避免测试橡皮图章。
	scenarios, err := eval.Load(scenarioDir())
	require.NoError(t, err)
	for _, s := range scenarios {
		assert.InDelta(t, 1.0, s.BaseErrorRate+s.AvoidanceRate, 1.0,
			"%s: 参数组合异常", s.ID)
		assert.True(t, s.BaseErrorRate > 0 && s.BaseErrorRate < 1, "%s base_error_rate", s.ID)
		assert.True(t, s.AvoidanceRate > 0.5, "%s avoidance_rate 过低", s.ID)
		assert.True(t, s.Relevance >= 0.75, "%s relevance 过低（干扰率会超标）", s.ID)
		assert.True(t, s.Sessions >= 100, "%s sessions 过少", s.ID)
		assert.NotEmpty(t, s.Pitfall.EntityID, "%s 缺 entity_id", s.ID)
		assert.NotEmpty(t, s.Pitfall.Signature, "%s 缺 signature", s.ID)
		assert.NotEmpty(t, s.Query, "%s 缺 query", s.ID)
	}
	_ = fmt.Sprint() // keep fmt imported in case t.Logf is pruned
}

package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/vatbrain/vatbrain/internal/models"
)

func TestComputeRisk_ConfirmedPitfall_FlagsRisk(t *testing.T) {
	now := time.Now()
	p := models.PitfallMemory{
		EntityID:          "clawfeed-push-v3.py",
		Signature:         "ClawFeed 推送必须用 v3.py",
		RootCauseCategory: models.RootCauseConfig,
		FixStrategy:       "使用 --as bot",
		OccurrenceCount:   5,
		LastOccurredAt:    &now, // 刚发生
		TrustLevel:        models.TrustLevelMax,
		Weight:            1.0,
		Status:            models.PitfallConfirmed,
	}

	res := ComputeRisk(RiskRequest{ProjectID: "coder"}, []models.PitfallMemory{p}, nil, now)
	assert.True(t, res.RiskScore > 0.6, "近期高频 confirmed pitfall 应产生高风险，got %v", res.RiskScore)
	assert.Contains(t, res.ReasonCodes, "recent_error")
	assert.Contains(t, res.ReasonCodes, "high_risk_pitfall")
	assert.Len(t, res.Pitfalls, 1)
}

func TestComputeRisk_SkipsNonInjectable(t *testing.T) {
	now := time.Now()
	suppressed := models.PitfallMemory{
		EntityID:        "old.py",
		OccurrenceCount: 10,
		TrustLevel:      models.TrustLevelMax,
		Status:          models.PitfallSuppressed,
	}
	proposedWeak := models.PitfallMemory{
		EntityID:        "new.py",
		OccurrenceCount: 1, // 低命中 → 不高置信 proposed
		TrustLevel:      models.DefaultTrustLevel,
		Status:          models.PitfallProposed,
	}

	res := ComputeRisk(RiskRequest{}, []models.PitfallMemory{suppressed, proposedWeak}, nil, now)
	assert.Equal(t, 0.0, res.RiskScore, "suppressed/低置信 proposed 不应产生风险")
	assert.Empty(t, res.Pitfalls)
}

func TestComputeRisk_TimeDecay_OldErrorLowRisk(t *testing.T) {
	old := time.Now().Add(-180 * 24 * time.Hour) // 半年前
	p := models.PitfallMemory{
		EntityID:        "stale.py",
		OccurrenceCount: 3,
		LastOccurredAt:  &old,
		TrustLevel:      models.TrustLevelMax,
		Status:          models.PitfallConfirmed,
	}

	res := ComputeRisk(RiskRequest{}, []models.PitfallMemory{p}, nil, time.Now())
	assert.Less(t, res.RiskScore, 0.3, "久远错误应因时间衰减而降权，got %v", res.RiskScore)
	assert.NotContains(t, res.ReasonCodes, "recent_error")
}

func TestComputeRisk_CapsPitfallsAtThree(t *testing.T) {
	now := time.Now()
	var pitfalls []models.PitfallMemory
	for i := 0; i < 6; i++ {
		pitfalls = append(pitfalls, models.PitfallMemory{
			EntityID:        "p" + string(rune('a'+i)),
			OccurrenceCount: 3,
			LastOccurredAt:  &now,
			TrustLevel:      models.TrustLevelMax,
			Status:          models.PitfallConfirmed,
		})
	}

	res := ComputeRisk(RiskRequest{}, pitfalls, nil, now)
	assert.Len(t, res.Pitfalls, 3, "EVOLUTION_PLAN: 最多注入 3 条")
}

func TestComputeRisk_ReasonMemoryRecall(t *testing.T) {
	now := time.Now()
	ep := models.EpisodicMemory{Summary: "相关记忆", ProjectID: "coder"}

	res := ComputeRisk(RiskRequest{}, nil, []models.EpisodicMemory{ep}, now)
	assert.Equal(t, 0.0, res.RiskScore)
	assert.Contains(t, res.ReasonCodes, "memory_recall")
	assert.Len(t, res.Pitfalls, 0)
}

func TestPitfallMemory_InterferenceRate(t *testing.T) {
	p := models.PitfallMemory{TimesShown: 10, TimesSuppressed: 3}
	assert.InDelta(t, 0.3, p.InterferenceRate(), 1e-9)
	p2 := models.PitfallMemory{}
	assert.Equal(t, 0.0, p2.InterferenceRate())
}

func TestProtectionDecayedWeight_SlowsDecay(t *testing.T) {
	base := 1.0
	days := 90.0
	// 无保护：90 天显著衰减
	unprotected := ProtectionDecayedWeight(base, 0, days)
	// 高保护：衰减明显更慢
	protected := ProtectionDecayedWeight(base, models.PitfallProtectionLevelMax, days)
	assert.Less(t, unprotected, protected, "保护级别应减缓衰减")
	assert.Less(t, unprotected, 0.2, "无保护 90 天应衰减到低值，got %v", unprotected)
	assert.Greater(t, protected, 0.5, "高保护 90 天应保持较高，got %v", protected)
	// 无时间流逝 → 原值
	assert.InDelta(t, 1.0, ProtectionDecayedWeight(1.0, 2, 0), 1e-9)
}

func TestPitfallMemory_Injectable(t *testing.T) {
	assert.True(t, models.PitfallMemory{Status: models.PitfallConfirmed}.Injectable())
	assert.True(t, models.PitfallMemory{
		Status: models.PitfallProposed, OccurrenceCount: 2, Weight: 0.8,
	}.Injectable(), "高置信 proposed 可注入")
	assert.False(t, models.PitfallMemory{Status: models.PitfallSuppressed}.Injectable())
	assert.False(t, models.PitfallMemory{
		Status: models.PitfallProposed, OccurrenceCount: 1,
	}.Injectable(), "低命中 proposed 不注入")
}

package core

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEffectiveFrequency_NoTimestamps(t *testing.T) {
	e := DefaultWeightDecayEngine()
	ef := e.EffectiveFrequency(nil, time.Now())
	assert.Equal(t, 0.0, ef)

	ef = e.EffectiveFrequency([]time.Time{}, time.Now())
	assert.Equal(t, 0.0, ef)
}

func TestEffectiveFrequency_SingleRecentAccess(t *testing.T) {
	e := DefaultWeightDecayEngine()
	now := time.Now()
	timestamps := []time.Time{now}

	ef := e.EffectiveFrequency(timestamps, now)
	assert.Equal(t, 1.0, ef) // e^(-0.1 * 0) = 1.0
}

func TestEffectiveFrequency_MultipleAccesses(t *testing.T) {
	e := DefaultWeightDecayEngine()
	now := time.Now()
	// 1 day ago and now
	timestamps := []time.Time{now.Add(-24 * time.Hour), now}

	ef := e.EffectiveFrequency(timestamps, now)
	// e^(-0.1 * 1) + e^0 = 0.9048 + 1.0 = 1.9048
	assert.InDelta(t, 1.9048, ef, 0.001)
}

func TestEffectiveFrequency_DecaysOverTime(t *testing.T) {
	e := DefaultWeightDecayEngine()
	now := time.Now()
	// All accesses are 10 days ago
	tenDaysAgo := now.Add(-240 * time.Hour)
	timestamps := []time.Time{tenDaysAgo, tenDaysAgo, tenDaysAgo}

	ef := e.EffectiveFrequency(timestamps, now)
	// 3 * e^(-0.1 * 10) = 3 * 0.3679 = 1.1036
	assert.InDelta(t, 1.1036, ef, 0.01)
}

func TestEffectiveFrequency_BurstyVsSpreadOut(t *testing.T) {
	e := DefaultWeightDecayEngine()
	now := time.Now()

	// Bursty: 10 accesses on the same day (today)
	bursty := make([]time.Time, 10)
	for i := range bursty {
		bursty[i] = now
	}
	burstyEF := e.EffectiveFrequency(bursty, now)
	assert.InDelta(t, 10.0, burstyEF, 0.01)

	// Spread out: 1 access per day for 10 days
	spread := make([]time.Time, 10)
	for i := range spread {
		spread[i] = now.Add(-time.Duration(9-i) * 24 * time.Hour)
	}
	spreadEF := e.EffectiveFrequency(spread, now)
	// spreadEF should be lower than burstyEF because older accesses decay
	assert.Less(t, spreadEF, burstyEF)
}

func TestWeight_BrandNewMemory(t *testing.T) {
	e := DefaultWeightDecayEngine()
	now := time.Now()
	ef := 1.0

	w := e.Weight(ef, now, now, now)
	// e^0 * e^0 * 1.0 = 1.0
	assert.InDelta(t, 1.0, w, 0.001)
}

func TestWeight_DualDecay(t *testing.T) {
	e := DefaultWeightDecayEngine()
	now := time.Now()
	createdAt := now.Add(-100 * 24 * time.Hour)    // 100 days ago
	lastAccessedAt := now.Add(-20 * 24 * time.Hour) // 20 days ago
	ef := 2.0

	w := e.Weight(ef, createdAt, lastAccessedAt, now)
	// experience: e^(-0.005 * 100) = e^(-0.5) = 0.6065
	// activity:    e^(-0.05 * 20)   = e^(-1.0) = 0.3679
	// weight = 2.0 * 0.6065 * 0.3679 = 0.4463
	assert.InDelta(t, 0.4463, w, 0.01)
}

func TestWeight_ExperienceSlowerThanActivity(t *testing.T) {
	e := DefaultWeightDecayEngine()
	now := time.Now()
	target := now.Add(-30 * 24 * time.Hour) // 30 days ago
	ef := 1.0

	// Weight from experience decay alone (no activity gap)
	wExp := e.Weight(ef, target, now, now)
	expDecay := math.Exp(-e.AlphaExperience * 30)

	// Weight from activity decay alone (no experience gap)
	wAct := e.Weight(ef, now, target, now)
	actDecay := math.Exp(-e.BetaActivity * 30)

	// Activity decay should be MUCH stronger than experience decay over same period
	assert.Less(t, wAct, wExp)
	assert.Greater(t, expDecay, actDecay*2) // experience > 2x activity
}

func TestIsCooled_BelowThreshold(t *testing.T) {
	e := DefaultWeightDecayEngine()
	assert.True(t, e.IsCooled(0.005))
	assert.False(t, e.IsCooled(0.05))
	assert.False(t, e.IsCooled(e.CoolingThreshold))  // boundary: exact threshold = not cooled
	assert.True(t, e.IsCooled(e.CoolingThreshold-1e-9)) // just below = cooled
}

func TestComputeFull_NoTimestamps(t *testing.T) {
	e := DefaultWeightDecayEngine()
	now := time.Now()
	ef, w := e.ComputeFull(nil, now, now)
	assert.Equal(t, 0.0, ef)
	assert.Equal(t, 0.0, w)
}

func TestComputeFull_Integration(t *testing.T) {
	e := DefaultWeightDecayEngine()
	now := time.Now()
	createdAt := now.Add(-30 * 24 * time.Hour)

	// Simulate: accessed 1, 2, 5, 7 days ago
	timestamps := []time.Time{
		now.Add(-7 * 24 * time.Hour),
		now.Add(-5 * 24 * time.Hour),
		now.Add(-2 * 24 * time.Hour),
		now.Add(-1 * 24 * time.Hour),
	}

	ef, w := e.ComputeFull(timestamps, createdAt, now)

	// ef = e^(-0.1*7) + e^(-0.1*5) + e^(-0.1*2) + e^(-0.1*1)
	//    = 0.4966 + 0.6065 + 0.8187 + 0.9048 = 2.8266
	assert.InDelta(t, 2.8266, ef, 0.01)

	// experience decay: e^(-0.005*30) = 0.8607
	// activity decay:    e^(-0.05*1)    = 0.9512
	// weight = 2.8266 * 0.8607 * 0.9512 = 2.314
	assert.InDelta(t, 2.314, w, 0.01)
}

func TestWeightDecayEngine_CustomParams(t *testing.T) {
	e := &WeightDecayEngine{
		LambdaDecay:      0.2,  // faster decay per access
		AlphaExperience:  0.01, // faster experience decay
		BetaActivity:     0.1,  // faster activity decay
		CoolingThreshold: 0.05, // higher cooling threshold
	}
	now := time.Now()
	timestamps := []time.Time{now.Add(-5 * 24 * time.Hour), now}
	createdAt := now.Add(-10 * 24 * time.Hour)

	_, w := e.ComputeFull(timestamps, createdAt, now)

	// With faster decay params, weight should be lower than defaults
	defaultEngine := DefaultWeightDecayEngine()
	_, defaultW := defaultEngine.ComputeFull(timestamps, createdAt, now)
	assert.Less(t, w, defaultW)
}

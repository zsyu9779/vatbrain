// Package core implements VatBrain's memory engines: weight decay, significance
// gating, pattern separation, and two-stage retrieval.
package core

import (
	"math"
	"time"
)

// WeightDecayEngine computes memory weights using recency-weighted frequency with
// dual-reference time decay and a cooling threshold.
//
// Algorithm (from DESIGN_PRINCIPLES.md Section 6.2):
//
//	EffectiveFrequency = Σ e^(-λ · days_since_access_i)
//	Weight = EffectiveFrequency · e^(-α · days_since_created) · e^(-β · days_since_last_access)
//
// When Weight drops below CoolingThreshold, the memory is eligible for cold storage.
type WeightDecayEngine struct {
	LambdaDecay      float64 // decay rate for individual access timestamps (default 0.1)
	AlphaExperience  float64 // slow decay from creation time (default 0.005)
	BetaActivity     float64 // fast decay from last access (default 0.05)
	CoolingThreshold float64 // weight below which memory is "cold" (default 0.01)
}

// DefaultWeightDecayEngine returns a WeightDecayEngine with tuned defaults.
//
// Parameter rationale (from design doc):
//
//	lambda_decay = 0.1  → single access drops to ~0.37 after ~10 days
//	alpha = 0.005       → "experience richness" halves after ~140 days (slow, long-term)
//	beta  = 0.05        → "activity" halves after ~14 days without access (faster)
//	cooling_threshold = 0.01 → below this the memory moves to cold storage
func DefaultWeightDecayEngine() *WeightDecayEngine {
	return &WeightDecayEngine{
		LambdaDecay:      0.1,
		AlphaExperience:  0.005,
		BetaActivity:     0.05,
		CoolingThreshold: 0.01,
	}
}

// EffectiveFrequency computes the recency-weighted sum of historical accesses.
// Each access contributes e^(-lambda * days_ago), so recent accesses dominate.
// Returns 0 if there are no access timestamps.
func (e *WeightDecayEngine) EffectiveFrequency(accessTimestamps []time.Time, now time.Time) float64 {
	var total float64
	for _, ts := range accessTimestamps {
		days := daysBetween(ts, now)
		total += math.Exp(-e.LambdaDecay * days)
	}
	return total
}

// Weight computes the final weight from effective_frequency and the two time references.
// created_at drives slow experience decay; lastAccessedAt drives faster activity decay.
func (e *WeightDecayEngine) Weight(
	effectiveFrequency float64,
	createdAt time.Time,
	lastAccessedAt time.Time,
	now time.Time,
) float64 {
	experienceDecay := math.Exp(-e.AlphaExperience * daysBetween(createdAt, now))
	activityDecay := math.Exp(-e.BetaActivity * daysBetween(lastAccessedAt, now))
	return effectiveFrequency * experienceDecay * activityDecay
}

// IsCooled reports whether the weight has dropped below the cooling threshold.
func (e *WeightDecayEngine) IsCooled(weight float64) bool {
	return weight < e.CoolingThreshold
}

// ComputeFull is a convenience method that runs the full pipeline:
// effective_frequency → weight. Access timestamps must be sorted oldest-first
// for correct decay computation.
func (e *WeightDecayEngine) ComputeFull(
	accessTimestamps []time.Time,
	createdAt time.Time,
	now time.Time,
) (effectiveFrequency, weight float64) {
	if len(accessTimestamps) == 0 {
		return 0, 0
	}
	ef := e.EffectiveFrequency(accessTimestamps, now)
	lastAccessedAt := accessTimestamps[len(accessTimestamps)-1]
	w := e.Weight(ef, createdAt, lastAccessedAt, now)
	return ef, w
}

// daysBetween returns the absolute fractional days between two times.
func daysBetween(t1, t2 time.Time) float64 {
	if t2.After(t1) {
		return t2.Sub(t1).Hours() / 24.0
	}
	return t1.Sub(t2).Hours() / 24.0
}

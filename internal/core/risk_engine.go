package core

import (
	"math"
	"os"
	"sort"
	"time"

	"github.com/vatbrain/vatbrain/internal/models"
)

// RiskRequest bundles the inputs for a proactive risk score (EVOLUTION_PLAN
// v0.3): what the agent is about to edit and in what context.
type RiskRequest struct {
	Files     []string
	EntityIDs []string
	ProjectID string
	Language  string
	TaskType  models.TaskType
	UserGoal  string
	// Complexity is the average structural complexity of the target modules
	// (0..1, default 0.5 = neutral). ROADMAP v0.3 formula:
	// 风险 = Pitfall 密度 × 时间衰减 × 模块复杂度. High complexity amplifies risk.
	Complexity float64
}

// complexityFactor maps a module-complexity score (0..1) onto a risk
// multiplier: complexity 0 → ×0.5, 0.5 (neutral) → ×1.0, 1.0 → ×1.5.
func complexityFactor(c float64) float64 {
	if c <= 0 {
		return 0.5
	}
	if c > 1 {
		return 1.5
	}
	return 0.5 + c
}

// EstimateModuleComplexity returns a 0..1 complexity proxy for a set of files
// based on log-scaled size (1KB→~0.43, 100KB→~0.71, 10MB→~1.0). Files that
// cannot be stat'ed default to 0.5 (neutral) so callers without disk access
// stay deterministic.
func EstimateModuleComplexity(files []string) float64 {
	if len(files) == 0 {
		return 0.5
	}
	var total float64
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			total += 0.5
			continue
		}
		total += math.Min(1.0, math.Log10(float64(info.Size())+1)/7.0)
	}
	return total / float64(len(files))
}

// RiskResult is the vatbrain-side output of prepare_edit_context.
type RiskResult struct {
	RiskScore   float64                `json:"risk_score"`    // 0..1
	ReasonCodes []string               `json:"reason_codes"`  // human-readable risk drivers
	Pitfalls    []models.PitfallMemory `json:"pitfalls"`      // top injectable pitfalls (≤3)
	Episodes    []models.EpisodicMemory `json:"episodes"`     // relevant memory recall
}

const (
	// riskPitfallCap bounds the number of pitfalls surfaced (EVOLUTION_PLAN:
	// 默认最多 3 条).
	riskPitfallCap = 3
	// pitfallTimeDecayDays halves a pitfall's contribution every 30 days since
	// its last occurrence (recent errors are more actionable).
	pitfallTimeDecayDays = 30.0
	// recentErrorWindow: an occurrence inside this window triggers the
	// "recent_error" reason code.
	recentErrorWindow = 7 * 24 * time.Hour
)

// ComputeRisk scores the edit risk from injectable pitfalls and relevant
// episodes, and selects the top pitfalls to surface. Pitfalls must already
// be filtered to injectable (confirmed / high-confidence proposed) — the
// suppressed/obsolete escape valve is enforced by the caller via
// PitfallMemory.Injectable().
func ComputeRisk(req RiskRequest, injectable []models.PitfallMemory,
	episodes []models.EpisodicMemory, now time.Time) RiskResult {

	var (
		total   float64
		reasons = map[string]bool{}
	)
	scores := make([]struct {
		p     models.PitfallMemory
		score float64
	}, 0, len(injectable))

	for _, p := range injectable {
		if !p.Injectable() {
			continue
		}
		occurrence := math.Min(1.0, float64(p.OccurrenceCount)/3.0)
		trust := float64(p.TrustLevel) / float64(models.TrustLevelMax)
		decay := 1.0
		if p.LastOccurredAt != nil {
			days := now.Sub(*p.LastOccurredAt).Hours() / 24
			decay = math.Exp(-days / pitfallTimeDecayDays)
		}
		contrib := occurrence * trust * decay
		scores = append(scores, struct {
			p     models.PitfallMemory
			score float64
		}{p, contrib})
		total += contrib

		if p.WasUserCorrected {
			reasons["user_corrected"] = true
		}
		if p.LastOccurredAt != nil && now.Sub(*p.LastOccurredAt) < recentErrorWindow {
			reasons["recent_error"] = true
		}
		if contrib >= 0.6 {
			reasons["high_risk_pitfall"] = true
		}
	}

	// Normalise risk to [0,1]; a single strong pitfall is enough to flag.
	risk := math.Min(1.0, total)

	// ROADMAP v0.3: 风险 = Pitfall 密度 × 时间衰减 × 模块复杂度。
	// 复杂度放大风险（复杂度 1.0 → ×1.5）。
	risk = math.Min(1.0, risk*complexityFactor(req.Complexity))

	if len(episodes) > 0 {
		reasons["memory_recall"] = true
	}
	if req.Files != nil && len(req.Files) > 0 {
		reasons["editing_files"] = true
	}
	if req.Complexity >= 0.75 {
		reasons["high_complexity"] = true
	}

	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })

	top := make([]models.PitfallMemory, 0, riskPitfallCap)
	for _, s := range scores {
		top = append(top, s.p)
		if len(top) >= riskPitfallCap {
			break
		}
	}

	return RiskResult{
		RiskScore:   risk,
		ReasonCodes: reasonList(reasons),
		Pitfalls:    top,
		Episodes:    episodes,
	}
}

// reasonList flattens a reason set into a stable slice.
func reasonList(reasons map[string]bool) []string {
	out := make([]string, 0, len(reasons))
	for r := range reasons {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// protectionDecayRate returns the decay-rate multiplier for a protection
// level. Protection slows decay: each level halves the decay rate (level 0 =
// full decay, level 3 = 1/8×), so escalated pitfalls fade far slower
// (DESIGN_PRINCIPLES §6.3 / v0.3 反馈闭环「保护级别」).
func protectionDecayRate(level int) float64 {
	if level <= 0 {
		return 1.0
	}
	if level > models.PitfallProtectionLevelMax {
		level = models.PitfallProtectionLevelMax
	}
	return 1.0 / math.Pow(2, float64(level))
}

// ProtectionDecayedWeight applies time decay to a pitfall weight, slowed by
// its protection level. days is the time since the last occurrence.
func ProtectionDecayedWeight(base float64, protectionLevel int, days float64) float64 {
	if days <= 0 {
		return base
	}
	// Exponential decay scaled by the protection multiplier (halflife 30d).
	decay := math.Exp(-days / pitfallTimeDecayDays * protectionDecayRate(protectionLevel))
	w := base * decay
	if w < 0 {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}

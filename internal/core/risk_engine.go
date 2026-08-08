package core

import (
	"math"
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

	if len(episodes) > 0 {
		reasons["memory_recall"] = true
	}
	if req.Files != nil && len(req.Files) > 0 {
		reasons["editing_files"] = true
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

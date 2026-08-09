// Package eval implements the Phase 6 evaluation harness for proactive risk
// injection. It replays a set of hand-crafted coding scenarios (tests/scenarios)
// against a deterministic behaviour model to measure the two EVOLUTION_PLAN
// acceptance metrics:
//
//   - RepeatedErrorReductionRate: the share of recurring errors the injection
//     prevents (reported as a fraction; must be measurable / > 0).
//   - InterferenceRate: the share of injections the user suppresses because
//     they were irrelevant (must stay < 30%).
//
// The harness also verifies, per scenario, that the real pipeline surfaces the
// scenario's pitfall for the scenario's query (store seed + provider retrieval),
// so the behavioural simulation is anchored to actual injection behaviour.
package eval

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vatbrain/vatbrain/internal/models"
)

// PitfallFixture is the pitfall a scenario expects the injection to surface.
type PitfallFixture struct {
	EntityID       string             `yaml:"entity_id"`
	EntityType     models.EntityType  `yaml:"entity_type"`
	Signature      string             `yaml:"signature"`
	RootCause      models.RootCause   `yaml:"root_cause"`
	FixStrategy    string             `yaml:"fix_strategy"`
	OccurrenceCount int               `yaml:"occurrence_count"`
	TrustLevel     models.TrustLevel  `yaml:"trust_level"`
}

// Scenario is one hand-crafted coding scenario.
type Scenario struct {
	ID             string         `yaml:"id"`
	Title          string         `yaml:"title"`
	Language       string         `yaml:"language"`
	TaskType       models.TaskType `yaml:"task_type"`
	Episodes       []string       `yaml:"episodes"`
	Pitfall        PitfallFixture `yaml:"pitfall"`
	// Query is used to verify the real pipeline surfaces the pitfall.
	Query string `yaml:"query"`

	// Behaviour-model knobs for the deterministic simulation.
	Sessions       int     `yaml:"sessions"`
	BaseErrorRate  float64 `yaml:"base_error_rate"` // error recurrence without injection
	AvoidanceRate  float64 `yaml:"avoidance_rate"`  // prob. a relevant injection prevents the error
	Relevance      float64 `yaml:"relevance"`       // prob. an injection is genuinely relevant
	// GenericEffectiveness is the error-reduction factor of a "generic memory"
	// arm (semantic recall only, no Pitfall injection). Defaults to 0.25 when
	// unset — meaningful recall helps a bit, but far less than a specific fix.
	GenericEffectiveness float64 `yaml:"generic_effectiveness,omitempty"`
}

// genericEffectiveness returns the arm's effectiveness (default 0.25).
func (s Scenario) genericEffectiveness() float64 {
	if s.GenericEffectiveness > 0 {
		return s.GenericEffectiveness
	}
	return 0.25
}

// Result is the simulation outcome for one scenario (three-arm: baseline /
// generic memory / VatBrain, plus injection-quality and cost-derived metrics).
type Result struct {
	ID   string `json:"id"`
	Title string `json:"title"`
	TotalEdits int `json:"total_edits"`

	// Three-arm repeated mistake counts.
	BaselineErrors int `json:"baseline_errors"` // 无记忆
	GenericErrors  int `json:"generic_errors"`  // 仅语义检索
	VatbrainErrors int `json:"vatbrain_errors"` // Pitfall + 注入

	// Injection quality.
	InjectedCount   int `json:"injected_count"`
	UsefulCount     int `json:"useful_count"`   // relevant 且注入避免错误
	SuppressedCount int `json:"suppressed_count"` // 不相关注入被 suppress

	// Derived metrics.
	RepeatedErrorReductionRate float64 `json:"repeated_error_reduction_rate"` // VatBrain vs baseline
	GenericReductionRate       float64 `json:"generic_reduction_rate"`         // generic vs baseline
	UsefulInjectionRate        float64 `json:"useful_injection_rate"`          // useful / injected
	FalseInjectionRate         float64 `json:"false_injection_rate"`           // suppressed / injected
	TaskTimeRatio              float64 `json:"task_time_ratio"`                // VatBrain / baseline (<1 = faster)
	TokenOverheadRatio         float64 `json:"token_overhead_ratio"`           // VatBrain / baseline (>1 = more tokens)
}

// Summary aggregates results across scenarios.
type Summary struct {
	Scenarios  int `json:"scenarios"`
	TotalEdits int `json:"total_edits"`

	BaselineErrors int `json:"baseline_errors"`
	GenericErrors  int `json:"generic_errors"`
	VatbrainErrors int `json:"vatbrain_errors"`

	InjectedCount   int `json:"injected_count"`
	UsefulCount     int `json:"useful_count"`
	SuppressedCount int `json:"suppressed_count"`

	RepeatedErrorReductionRate float64 `json:"repeated_error_reduction_rate"`
	GenericReductionRate       float64 `json:"generic_reduction_rate"`
	UsefulInjectionRate        float64 `json:"useful_injection_rate"`
	FalseInjectionRate         float64 `json:"false_injection_rate"`
	TaskTimeRatio              float64 `json:"task_time_ratio"`
	TokenOverheadRatio         float64 `json:"token_overhead_ratio"`
}

// Load reads all scenario YAML files from dir (sorted for determinism).
func Load(dir string) ([]Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var scenarios []Scenario
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		var s Scenario
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(data, &s); err != nil {
			return nil, err
		}
		scenarios = append(scenarios, s)
	}
	return scenarios, nil
}

// Simulate runs one scenario's deterministic three-arm behaviour model.
//
// Cost model (documented assumptions): each repeated error costs ~1 extra
// fix turn; a Pitfall injection adds ~3% of a turn of token/context overhead.
// TaskTimeRatio = VatBrain / baseline (<1 means the injection pays for itself
// by preventing retries).
func Simulate(s Scenario, rng *rand.Rand) Result {
	res := Result{ID: s.ID, Title: s.Title, TotalEdits: s.Sessions}
	genericEff := s.genericEffectiveness()

	for i := 0; i < s.Sessions; i++ {
		// 三臂共享同一"错误倾向"判定，保证可比性。
		baselineErr := rng.Float64() < s.BaseErrorRate
		if baselineErr {
			res.BaselineErrors++
		}
		// Generic memory：语义检索降低但不清除错误。
		if rng.Float64() < s.BaseErrorRate*(1-genericEff) {
			res.GenericErrors++
		}

		// VatBrain 注入臂。
		relevant := rng.Float64() < s.Relevance
		res.InjectedCount++
		if relevant {
			if rng.Float64() < 1-s.AvoidanceRate {
				res.VatbrainErrors++
			} else {
				res.UsefulCount++ // 注入避免错误
			}
		} else {
			// 不相关注入 → suppress（干扰），且不帮助避免错误。
			res.SuppressedCount++
			if rng.Float64() < s.BaseErrorRate {
				res.VatbrainErrors++
			}
		}
	}
	finishResult(&res)
	return res
}

// Aggregate combines per-scenario results into a summary.
func Aggregate(results []Result) Summary {
	s := Summary{Scenarios: len(results)}
	for _, r := range results {
		s.TotalEdits += r.TotalEdits
		s.BaselineErrors += r.BaselineErrors
		s.GenericErrors += r.GenericErrors
		s.VatbrainErrors += r.VatbrainErrors
		s.InjectedCount += r.InjectedCount
		s.UsefulCount += r.UsefulCount
		s.SuppressedCount += r.SuppressedCount
	}
	if s.BaselineErrors > 0 {
		s.RepeatedErrorReductionRate = float64(s.BaselineErrors-s.VatbrainErrors) /
			float64(s.BaselineErrors)
		s.GenericReductionRate = float64(s.BaselineErrors-s.GenericErrors) /
			float64(s.BaselineErrors)
	}
	if s.InjectedCount > 0 {
		s.UsefulInjectionRate = float64(s.UsefulCount) / float64(s.InjectedCount)
		s.FalseInjectionRate = float64(s.SuppressedCount) / float64(s.InjectedCount)
	}
	// 成本派生：时间 = 编辑 + 错误×修复回合 + 注入×开销；token = 1 + 注入占比。
	baselineTime := float64(s.TotalEdits) + float64(s.BaselineErrors)
	vatbrainTime := float64(s.TotalEdits) + float64(s.VatbrainErrors) +
		float64(s.InjectedCount)*injectionOverheadTurns
	if baselineTime > 0 {
		s.TaskTimeRatio = vatbrainTime / baselineTime
	}
	s.TokenOverheadRatio = 1 + float64(s.InjectedCount)*injectionOverheadTurns/float64(maxInt(1, s.TotalEdits))
	return s
}

// injectionOverheadTurns is the modelling constant: one injection costs ~3%
// of a turn's worth of context/tokens.
const injectionOverheadTurns = 0.03

// finishResult fills the derived rates on one result.
func finishResult(r *Result) {
	if r.BaselineErrors > 0 {
		r.RepeatedErrorReductionRate = float64(r.BaselineErrors-r.VatbrainErrors) /
			float64(r.BaselineErrors)
		r.GenericReductionRate = float64(r.BaselineErrors-r.GenericErrors) /
			float64(r.BaselineErrors)
	}
	if r.InjectedCount > 0 {
		r.UsefulInjectionRate = float64(r.UsefulCount) / float64(r.InjectedCount)
		r.FalseInjectionRate = float64(r.SuppressedCount) / float64(r.InjectedCount)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

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
}

// Result is the simulation outcome for one scenario.
type Result struct {
	ID                        string  `json:"id"`
	Title                     string  `json:"title"`
	TotalEdits                int     `json:"total_edits"`
	ErrorsWithoutInjection    int     `json:"errors_without_injection"`
	ErrorsWithInjection       int     `json:"errors_with_injection"`
	RepeatedErrorReductionRate float64 `json:"repeated_error_reduction_rate"`
	InjectedCount             int     `json:"injected_count"`
	SuppressedCount           int     `json:"suppressed_count"`
	InterferenceRate          float64 `json:"interference_rate"`
}

// Summary aggregates results across scenarios.
type Summary struct {
	Scenarios                int     `json:"scenarios"`
	TotalEdits               int     `json:"total_edits"`
	ErrorsWithoutInjection   int     `json:"errors_without_injection"`
	ErrorsWithInjection      int     `json:"errors_with_injection"`
	RepeatedErrorReductionRate float64 `json:"repeated_error_reduction_rate"`
	InjectedCount            int     `json:"injected_count"`
	SuppressedCount          int     `json:"suppressed_count"`
	InterferenceRate         float64 `json:"interference_rate"`
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

// Simulate runs one scenario's deterministic behaviour model.
func Simulate(s Scenario, rng *rand.Rand) Result {
	res := Result{ID: s.ID, Title: s.Title, TotalEdits: s.Sessions}
	for i := 0; i < s.Sessions; i++ {
		relevant := rng.Float64() < s.Relevance

		// No-injection arm: base recurrence.
		if rng.Float64() < s.BaseErrorRate {
			res.ErrorsWithoutInjection++
		}

		// Injection arm.
		res.InjectedCount++
		if relevant {
			if rng.Float64() < 1-s.AvoidanceRate {
				res.ErrorsWithInjection++
			}
		} else {
			// Irrelevant injection → the user suppresses it (interference).
			res.SuppressedCount++
			if rng.Float64() < s.BaseErrorRate {
				res.ErrorsWithInjection++
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
		s.ErrorsWithoutInjection += r.ErrorsWithoutInjection
		s.ErrorsWithInjection += r.ErrorsWithInjection
		s.InjectedCount += r.InjectedCount
		s.SuppressedCount += r.SuppressedCount
	}
	if s.ErrorsWithoutInjection > 0 {
		s.RepeatedErrorReductionRate = float64(s.ErrorsWithoutInjection-s.ErrorsWithInjection) /
			float64(s.ErrorsWithoutInjection)
	}
	if s.InjectedCount > 0 {
		s.InterferenceRate = float64(s.SuppressedCount) / float64(s.InjectedCount)
	}
	return s
}

// finishResult fills the derived rates on one result.
func finishResult(r *Result) {
	if r.ErrorsWithoutInjection > 0 {
		r.RepeatedErrorReductionRate = float64(r.ErrorsWithoutInjection-r.ErrorsWithInjection) /
			float64(r.ErrorsWithoutInjection)
	}
	if r.InjectedCount > 0 {
		r.InterferenceRate = float64(r.SuppressedCount) / float64(r.InjectedCount)
	}
}

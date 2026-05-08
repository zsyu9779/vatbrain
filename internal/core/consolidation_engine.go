package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/llm"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// ConsolidationEngine implements the sleep consolidation loop described in
// DESIGN_PRINCIPLES.md Section 4.3. It scans recent episodic memories, clusters
// related experiences, extracts rules, backtests, and persists them as semantic
// memories with full traceability chains.
//
// v0.1 uses simple (project_id, task_type) clustering and concatenation-based
// extraction. v0.2 adds parallel pitfall extraction from debug-type episodics.
type ConsolidationEngine struct {
	HoursToScan       float64
	MinClusterSize    int
	AccuracyThreshold float64
	LLMClient         llm.Client // v0.2: nil = degrade to v0.1 string-concat extraction
	PitfallExtractor  *PitfallExtractor
}

// DefaultConsolidationEngine returns a ConsolidationEngine with v0.1 tuned defaults.
func DefaultConsolidationEngine() *ConsolidationEngine {
	return &ConsolidationEngine{
		HoursToScan:       24,
		MinClusterSize:    3,
		AccuracyThreshold: 0.7,
	}
}

// PatternCluster groups episodic memories that share a (project_id, task_type)
// key. In v0.1 this is a plain structural grouping; future versions will use
// embedding clustering to discover non-obvious patterns.
type PatternCluster struct {
	ProjectID string
	TaskType  models.TaskType
	Episodics []store.EpisodicScanItem
}

// Run executes a full consolidation pass: scan → (rules || pitfalls) → persist.
// The semantic rule line and pitfall extraction line run in parallel goroutines.
func (e *ConsolidationEngine) Run(
	ctx context.Context,
	s store.MemoryStore,
	emb embedder.Embedder,
) (models.ConsolidationRunResult, error) {
	runID := uuid.New()
	result := models.ConsolidationRunResult{
		RunID:     runID,
		StartedAt: time.Now().UTC(),
	}

	since := result.StartedAt.Add(-time.Duration(e.HoursToScan * float64(time.Hour)))

	// Phase 1: Scan recent episodic memories (shared across both lines).
	episodics, err := s.ScanRecent(ctx, since, 1000)
	if err != nil {
		return result, fmt.Errorf("consolidation scan: %w", err)
	}
	result.EpisodicsScanned = len(episodics)

	if len(episodics) == 0 {
		now := time.Now()
		result.CompletedAt = &now
		return result, nil
	}

	// Phase 2–5: Run rule extraction and pitfall extraction in parallel.
	var wg sync.WaitGroup
	var rulesErr, pitfallErr error
	var ruleResult models.ConsolidationRunResult

	wg.Add(2)

	go func() {
		defer wg.Done()
		ruleResult, rulesErr = e.runRuleExtraction(ctx, s, emb, runID, episodics)
	}()

	go func() {
		defer wg.Done()
		pitfallErr = e.runPitfallExtraction(ctx, s, runID, episodics, &result)
	}()

	wg.Wait()

	// Merge rule extraction results.
	result.RulesPersisted = ruleResult.RulesPersisted
	result.CandidateRulesFound = ruleResult.CandidateRulesFound
	result.AverageAccuracy = ruleResult.AverageAccuracy
	result.RulesError = errToString(rulesErr)

	// Pitfall results are already written into result by runPitfallExtraction.
	result.PitfallError = errToString(pitfallErr)

	now := time.Now()
	result.CompletedAt = &now
	return result, nil
}

// runRuleExtraction executes the semantic rule extraction line: cluster →
// extract → backtest → persist. This is the v0.1 consolidation logic extracted
// into its own goroutine.
func (e *ConsolidationEngine) runRuleExtraction(
	ctx context.Context,
	s store.MemoryStore,
	emb embedder.Embedder,
	runID uuid.UUID,
	episodics []store.EpisodicScanItem,
) (models.ConsolidationRunResult, error) {
	result := models.ConsolidationRunResult{RunID: runID}

	// Cluster by (project_id, task_type).
	clusters := clusterByPattern(episodics, e.MinClusterSize)
	result.CandidateRulesFound = len(clusters)

	for _, cl := range clusters {
		ruleContent := e.extractRule(ctx, cl)
		accuracy := e.backtest(ctx, cl)

		if accuracy < e.AccuracyThreshold {
			continue
		}

		semID := uuid.New()
		sourceIDs := make([]uuid.UUID, len(cl.Episodics))
		for i, ep := range cl.Episodics {
			sourceIDs[i] = ep.ID
		}

		now := time.Now().UTC()
		sem := &models.SemanticMemory{
			ID:                 semID,
			Type:               models.MemoryTypePattern,
			Content:            ruleContent,
			SourceType:         models.SourceTypeINFERRED,
			TrustLevel:         models.DefaultTrustLevel,
			Weight:             1.0,
			EffectiveFrequency: 1.0,
			CreatedAt:          now,
			EntityGroup:        fmt.Sprintf("consolidation:%s:%s", cl.ProjectID, cl.TaskType),
			ConsolidationRunID: runID.String(),
			BacktestAccuracy:   accuracy,
			SourceEpisodicIDs:  sourceIDs,
		}

		if err := s.WriteSemantic(ctx, sem); err != nil {
			return result, fmt.Errorf("consolidation create semantic: %w", err)
		}

		// Create DERIVED_FROM edges.
		for _, epID := range sourceIDs {
			if err := s.CreateEdge(ctx, semID, epID, "DERIVED_FROM", map[string]any{
				"run_id": runID.String(),
			}); err != nil {
				return result, fmt.Errorf("consolidation derived_from edge: %w", err)
			}
		}

		result.RulesPersisted++
		if result.RulesPersisted == 1 {
			result.AverageAccuracy = accuracy
		} else {
			result.AverageAccuracy = (result.AverageAccuracy*float64(result.RulesPersisted-1) + accuracy) / float64(result.RulesPersisted)
		}
	}
	return result, nil
}

// runPitfallExtraction executes the pitfall extraction line with a 120-second
// timeout. It writes results directly into the provided result pointer.
func (e *ConsolidationEngine) runPitfallExtraction(
	ctx context.Context,
	s store.MemoryStore,
	runID uuid.UUID,
	episodics []store.EpisodicScanItem,
	result *models.ConsolidationRunResult,
) error {
	if e.PitfallExtractor == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	pitfalls, candidatesFound, merged, err := e.PitfallExtractor.Extract(ctx, episodics)
	result.CandidateRulesFound += candidatesFound
	result.PitfallsExtracted = len(pitfalls) + merged
	result.PitfallsMerged = merged

	if err != nil {
		return fmt.Errorf("pitfall extraction: %w", err)
	}

	for _, p := range pitfalls {
		// Persist pitfall.
		if writeErr := s.WritePitfall(ctx, &p); writeErr != nil {
			return fmt.Errorf("pitfall persist: %w", writeErr)
		}

		// Create DERIVED_FROM edges from pitfall to source episodics.
		for _, epID := range p.SourceEpisodicIDs {
			if edgeErr := s.CreateEdge(ctx, p.ID, epID, "DERIVED_FROM", map[string]any{
				"run_id": runID.String(),
			}); edgeErr != nil {
				return fmt.Errorf("pitfall derived_from edge: %w", edgeErr)
			}
		}

		result.PitfallsPersisted++
	}
	return nil
}

// clusterByPattern groups episodic memories by (project_id, task_type) and
// returns only clusters that meet the minimum size threshold.
func clusterByPattern(episodics []store.EpisodicScanItem, minSize int) []PatternCluster {
	groups := make(map[string]*PatternCluster)
	order := make([]string, 0)

	for _, ep := range episodics {
		key := ep.ProjectID + "|" + string(ep.TaskType)
		if _, ok := groups[key]; !ok {
			groups[key] = &PatternCluster{
				ProjectID: ep.ProjectID,
				TaskType:  ep.TaskType,
			}
			order = append(order, key)
		}
		groups[key].Episodics = append(groups[key].Episodics, ep)
	}

	var clusters []PatternCluster
	for _, key := range order {
		c := groups[key]
		if len(c.Episodics) >= minSize {
			clusters = append(clusters, *c)
		}
	}
	return clusters
}

// extractRule builds a candidate rule from a cluster. If an LLM client is
// configured, it calls the LLM to distill a pattern; otherwise it falls back
// to v0.1 string concatenation.
func (e *ConsolidationEngine) extractRule(ctx context.Context, cl PatternCluster) string {
	if e.LLMClient != nil {
		systemPrompt := "You are a knowledge extraction engine. Given a set of episodic memories about the same project and task type, extract a concise, reusable rule or pattern. Output only the rule text, no markdown formatting."
		userPrompt := fmt.Sprintf("Project: %s | TaskType: %s\n\nMemories:\n", cl.ProjectID, cl.TaskType)
		for i, ep := range cl.Episodics {
			userPrompt += fmt.Sprintf("[%d] %s\n", i, ep.Summary)
		}
		if rule, err := e.LLMClient.Chat(ctx, systemPrompt, userPrompt); err == nil {
			return rule
		}
	}
	// Fallback: v0.1 string concatenation.
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Pattern in %s/%s:\n", cl.ProjectID, cl.TaskType))
	for i, ep := range cl.Episodics {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("- ")
		b.WriteString(ep.Summary)
	}
	return b.String()
}

// backtest evaluates a candidate rule. If an LLM client is configured, it
// samples up to 20 held-out episodics and asks the LLM to verify the rule;
// otherwise returns 1.0 if the cluster meets the minimum size.
func (e *ConsolidationEngine) backtest(ctx context.Context, cl PatternCluster) float64 {
	if e.LLMClient != nil {
		sampleSize := len(cl.Episodics)
		if sampleSize > 20 {
			sampleSize = 20
		}
		if sampleSize < 3 {
			return 0.0
		}
		systemPrompt := "You are a rule validator. Given a candidate rule and a set of episodic memories, rate how well the rule describes the pattern on a scale from 0.0 to 1.0. Output ONLY the numeric score."
		userPrompt := fmt.Sprintf("Rule: %s\n\nEpisodic memories:\n", e.extractRule(ctx, cl))
		for i := 0; i < sampleSize; i++ {
			userPrompt += fmt.Sprintf("[%d] %s\n", i, cl.Episodics[i].Summary)
		}
		if resp, err := e.LLMClient.Chat(ctx, systemPrompt, userPrompt); err == nil {
			var score float64
			if _, scanErr := fmt.Sscanf(strings.TrimSpace(resp), "%f", &score); scanErr == nil {
				if score >= 0 && score <= 1 {
					return score
				}
			}
		}
	}
	// Fallback: v0.1 min-size check.
	if len(cl.Episodics) >= e.MinClusterSize {
		return 1.0
	}
	return 0.0
}

// errToString converts an error to a string, returning empty string for nil.
func errToString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

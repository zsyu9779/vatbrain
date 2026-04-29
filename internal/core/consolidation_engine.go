package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// ConsolidationEngine implements the sleep consolidation loop described in
// DESIGN_PRINCIPLES.md Section 4.3. It scans recent episodic memories, clusters
// related experiences, extracts rules, backtests, and persists them as semantic
// memories with full traceability chains.
//
// v0.1 uses simple (project_id, task_type) clustering and concatenation-based
// extraction. Later versions will use embedding clustering + LLM distillation.
type ConsolidationEngine struct {
	HoursToScan       float64
	MinClusterSize    int
	AccuracyThreshold float64
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

// Run executes a full consolidation pass: scan → cluster → extract → backtest →
// persist.
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

	// Phase 1: Scan recent episodic memories.
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

	// Phase 2: Cluster.
	clusters := clusterByPattern(episodics, e.MinClusterSize)
	result.CandidateRulesFound = len(clusters)

	// Phase 3-5: Extract → Backtest → Persist.
	for _, cl := range clusters {
		ruleContent := extractRule(cl)
		accuracy := backtest(cl, e.MinClusterSize)

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
			ID:                  semID,
			Type:                models.MemoryTypePattern,
			Content:             ruleContent,
			SourceType:          models.SourceTypeINFERRED,
			TrustLevel:          models.DefaultTrustLevel,
			Weight:              1.0,
			EffectiveFrequency:  1.0,
			CreatedAt:           now,
			EntityGroup:         fmt.Sprintf("consolidation:%s:%s", cl.ProjectID, cl.TaskType),
			ConsolidationRunID:  runID.String(),
			BacktestAccuracy:    accuracy,
			SourceEpisodicIDs:   sourceIDs,
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

	now := time.Now()
	result.CompletedAt = &now
	return result, nil
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

// extractRule builds a candidate rule from a cluster of episodic memories.
// v0.1 concatenates summaries; future versions will call an LLM to distill
// a pattern.
func extractRule(cl PatternCluster) string {
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

// backtest evaluates a candidate rule against its source episodics.
// v0.1 returns 1.0 if the cluster meets the minimum size; a real implementation
// would test the rule against held-out recent episodics.
func backtest(cl PatternCluster, minSize int) float64 {
	if len(cl.Episodics) >= minSize {
		return 1.0
	}
	return 0.0
}

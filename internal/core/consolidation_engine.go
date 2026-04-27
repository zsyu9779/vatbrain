package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/vatbrain/vatbrain/internal/models"
)

// ConsolidationEngine implements the sleep consolidation loop described in
// DESIGN_PRINCIPLES.md Section 4.3. It scans recent episodic memories, clusters
// related experiences, extracts rules, backtests, and persists them as semantic
// memories with full traceability chains.
//
// v0.1 uses simple (project_id, task_type) clustering and concatenation-based
// extraction. Later versions will use embedding clustering + LLM distillation.
type ConsolidationEngine struct {
	// HoursToScan is how far back to look for episodic memories. Default 24.
	HoursToScan float64
	// MinClusterSize is the minimum number of episodics in a cluster to extract
	// a rule. Default 3.
	MinClusterSize int
	// AccuracyThreshold is the minimum backtest accuracy to persist a rule.
	// Default 0.7.
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

// EpisodicScanResult is a lightweight record of a single episodic memory scanned
// during consolidation.
type EpisodicScanResult struct {
	ID          uuid.UUID
	ProjectID   string
	Language    string
	TaskType    models.TaskType
	Summary     string
	EntityGroup string
	Weight      float64
}

// PatternCluster groups episodic memories that share a (project_id, task_type)
// key. In v0.1 this is a plain structural grouping; future versions will use
// embedding clustering to discover non-obvious patterns.
type PatternCluster struct {
	ProjectID string
	TaskType  models.TaskType
	Episodics []EpisodicScanResult
}

// Run executes a full consolidation pass: scan → cluster → extract → backtest →
// persist. The embedder is used to generate vectors for new semantic memories.
//
// neo4jClient and pgvectorClient provide direct DB access. The caller is
// responsible for their lifecycle.
func (e *ConsolidationEngine) Run(
	ctx context.Context,
	neo4jClient interface {
		ExecuteRead(ctx context.Context, fn func(tx neo4j.ManagedTransaction) (any, error)) (any, error)
		ExecuteWrite(ctx context.Context, fn func(tx neo4j.ManagedTransaction) (any, error)) (any, error)
	},
	pgvectorClient interface {
		InsertEmbedding(ctx context.Context, memoryID string, embedding []float32,
			summaryText, projectID, language, taskType string, metadata map[string]any) error
	},
	embedder interface {
		Embed(ctx context.Context, text string) ([]float32, error)
	},
) (models.ConsolidationRunResult, error) {
	runID := uuid.New()
	result := models.ConsolidationRunResult{
		RunID:     runID,
		StartedAt: time.Now(),
	}

	since := result.StartedAt.Add(-time.Duration(e.HoursToScan * float64(time.Hour)))

	// Phase 1: Scan recent episodic memories.
	episodics, err := e.scan(ctx, neo4jClient, since)
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

		embedding, err := embedder.Embed(ctx, ruleContent)
		if err != nil {
			return result, fmt.Errorf("consolidation embed: %w", err)
		}

		_, err = neo4jClient.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
			_, err := tx.Run(ctx, `
				CREATE (s:SemanticMemory {
					id: $id,
					type: $type,
					content: $content,
					source_type: $sourceType,
					trust_level: $trustLevel,
					weight: $weight,
					effective_frequency: $effFreq,
					created_at: $createdAt,
					entity_group: $entityGroup,
					consolidation_run_id: $runID,
					backtest_accuracy: $accuracy,
					source_episodic_ids: $sourceIDs
				})
				RETURN s.id
			`, map[string]any{
				"id":         semID.String(),
				"type":       string(models.MemoryTypePattern),
				"content":    ruleContent,
				"sourceType": string(models.SourceTypeINFERRED),
				"trustLevel": int(models.DefaultTrustLevel),
				"weight":     1.0,
				"effFreq":    1.0,
				"createdAt":  time.Now(),
				"entityGroup": fmt.Sprintf("consolidation:%s:%s", cl.ProjectID, cl.TaskType),
				"runID":      runID.String(),
				"accuracy":   accuracy,
				"sourceIDs":  idStrings(sourceIDs),
			})
			return nil, err
		})
		if err != nil {
			return result, fmt.Errorf("consolidation create semantic: %w", err)
		}

		// Create DERIVED_FROM edges.
		for _, epID := range sourceIDs {
			_, err := neo4jClient.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
				_, err := tx.Run(ctx, `
					MATCH (s:SemanticMemory {id: $semID})
					MATCH (e:EpisodicMemory {id: $epID})
					CREATE (s)-[:DERIVED_FROM {run_id: $runID}]->(e)
				`, map[string]any{
					"semID": semID.String(),
					"epID":  epID.String(),
					"runID": runID.String(),
				})
				return nil, err
			})
			if err != nil {
				return result, fmt.Errorf("consolidation derived_from edge: %w", err)
			}
		}

		// Insert embedding into pgvector.
		err = pgvectorClient.InsertEmbedding(ctx, semID.String(), embedding,
			ruleContent, cl.ProjectID, "", string(cl.TaskType),
			map[string]any{
				"memory_type":         "semantic",
				"consolidation_run_id": runID.String(),
			})
		if err != nil {
			return result, fmt.Errorf("consolidation pgvector insert: %w", err)
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

// scan fetches episodic memories created since the given time.
func (e *ConsolidationEngine) scan(
	ctx context.Context,
	neo4jClient interface {
		ExecuteRead(ctx context.Context, fn func(tx neo4j.ManagedTransaction) (any, error)) (any, error)
	},
	since time.Time,
) ([]EpisodicScanResult, error) {
	raw, err := neo4jClient.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		cyp := `
			MATCH (e:EpisodicMemory)
			WHERE e.created_at >= $since AND e.obsoleted_at IS NULL
			RETURN e.id, e.project_id, e.language, e.task_type, e.summary,
			       e.entity_group, e.weight
			ORDER BY e.created_at DESC
			LIMIT 1000
		`
		records, err := tx.Run(ctx, cyp, map[string]any{
			"since": since,
		})
		if err != nil {
			return nil, err
		}

		var results []EpisodicScanResult
		for records.Next(ctx) {
			r := records.Record()
			id, _, _ := neo4j.GetRecordValue[string](r, "e.id")
			projectID, _, _ := neo4j.GetRecordValue[string](r, "e.project_id")
			lang, _, _ := neo4j.GetRecordValue[string](r, "e.language")
			taskType, _, _ := neo4j.GetRecordValue[string](r, "e.task_type")
			summary, _, _ := neo4j.GetRecordValue[string](r, "e.summary")
			entityGroup, _, _ := neo4j.GetRecordValue[string](r, "e.entity_group")
			weight, _, _ := neo4j.GetRecordValue[float64](r, "e.weight")

			parsedID, parseErr := uuid.Parse(id)
			if parseErr != nil {
				continue
			}

			results = append(results, EpisodicScanResult{
				ID:          parsedID,
				ProjectID:   projectID,
				Language:    lang,
				TaskType:    models.TaskType(taskType),
				Summary:     summary,
				EntityGroup: entityGroup,
				Weight:      weight,
			})
		}
		return results, records.Err()
	})
	if err != nil {
		return nil, err
	}

	episodics, ok := raw.([]EpisodicScanResult)
	if !ok {
		return nil, fmt.Errorf("consolidation: unexpected scan result type %T", raw)
	}
	return episodics, nil
}

// clusterByPattern groups episodic memories by (project_id, task_type) and
// returns only clusters that meet the minimum size threshold.
func clusterByPattern(episodics []EpisodicScanResult, minSize int) []PatternCluster {
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

// idStrings converts a slice of UUIDs to a slice of their string representations.
func idStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

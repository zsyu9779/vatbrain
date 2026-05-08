package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/llm"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// PitfallExtractor extracts PitfallMemory nodes from debug-type episodic clusters.
// It runs as part of the consolidation sleep cycle, in parallel with rule extraction.
type PitfallExtractor struct {
	MinClusterSize        int
	MergeThreshold        float64 // HAC merge threshold (cosine similarity, default 0.85)
	DedupThreshold        float64 // post-extraction dedup threshold (default 0.9)
	Embedder              embedder.Embedder
	LLMClient             llm.Client
	MaxConcurrency        int // max concurrent LLM calls per entity group
}

// PitfallCandidate is a provisional pitfall before LLM structuring.
type PitfallCandidate struct {
	EntityID    string
	EpisodicIDs []uuid.UUID
	Summaries   []string
}

// SubCluster is a tight cluster of episodic memories within an entity group,
// produced by HAC sub-clustering. It represents a single bug pattern.
type SubCluster struct {
	Episodics []store.EpisodicScanItem
}

// EntityGroup groups all debug episodic memories for a single entity_id.
type EntityGroup struct {
	EntityID  string
	Episodics []store.EpisodicScanItem
}

// PitfallLLMOutput is the expected JSON structure from the LLM pitfall extraction.
type PitfallLLMOutput struct {
	Signature         string  `json:"signature"`
	RootCauseCategory string  `json:"root_cause_category"`
	FixStrategy       string  `json:"fix_strategy"`
	Confidence        float64 `json:"confidence"`
}

// Extract runs the full pitfall extraction pipeline:
//  1. Filter episodic scan to task_type=debug + entity_id non-empty
//  2. Group by entity_id
//  3. For each entity group, embed summaries and perform HAC sub-clustering
//  4. For each sub-cluster >= MinClusterSize, call LLM to extract structured pitfall
//  5. Deduplicate across all extracted pitfalls (merge threshold 0.9)
//
// Returns pitfalls ready for persistence, plus counts of candidates found and merged.
func (pe *PitfallExtractor) Extract(
	ctx context.Context,
	episodics []store.EpisodicScanItem,
) (pitfalls []models.PitfallMemory, candidatesFound int, merged int, err error) {
	// Stage 0: Filter to task_type=debug + entity_id non-empty.
	var debugEps []store.EpisodicScanItem
	for _, ep := range episodics {
		if ep.TaskType == models.TaskTypeDebug && ep.EntityID != "" {
			debugEps = append(debugEps, ep)
		}
	}
	if len(debugEps) == 0 {
		return nil, 0, 0, nil
	}

	// Stage 1: Group by entity_id.
	groups := groupByEntityID(debugEps)

	// Stage 2: Per entity group — embed, HAC sub-cluster, LLM extract.
	var extracted []models.PitfallMemory
	for _, g := range groups {
		if len(g.Episodics) < pe.MinClusterSize {
			continue
		}
		subClusters := pe.subCluster(ctx, g)
		candidatesFound += len(subClusters)
		for _, sc := range subClusters {
			if len(sc.Episodics) < pe.MinClusterSize {
				continue
			}
			pf, extractErr := pe.extractFromSubCluster(ctx, g.EntityID, sc)
			if extractErr != nil {
				slog.Warn("pitfall_extractor: LLM extraction failed for entity",
					"entity_id", g.EntityID, "err", extractErr)
				continue
			}
			extracted = append(extracted, pf)
		}
	}

	// Stage 3: Deduplicate across entities (merge threshold 0.9).
	deduped := pe.deduplicatePitfalls(ctx, extracted)
	merged = len(extracted) - len(deduped)

	return deduped, candidatesFound, merged, nil
}

// groupByEntityID groups episodic memories by EntityID.
func groupByEntityID(episodics []store.EpisodicScanItem) []EntityGroup {
	groups := make(map[string]*EntityGroup)
	for _, ep := range episodics {
		g, ok := groups[ep.EntityID]
		if !ok {
			g = &EntityGroup{EntityID: ep.EntityID}
			groups[ep.EntityID] = g
		}
		g.Episodics = append(g.Episodics, ep)
	}
	result := make([]EntityGroup, 0, len(groups))
	for _, g := range groups {
		result = append(result, *g)
	}
	return result
}

// subCluster performs HAC sub-clustering within an entity group. It generates
// embeddings for each summary, then merges the closest pair iteratively until
// no pair exceeds the merge threshold.
func (pe *PitfallExtractor) subCluster(ctx context.Context, g EntityGroup) []SubCluster {
	n := len(g.Episodics)
	if n <= 1 {
		return []SubCluster{{Episodics: g.Episodics}}
	}

	// Generate embeddings for each summary.
	embeddings := make([][]float64, n)
	for i, ep := range g.Episodics {
		emb, err := pe.Embedder.Embed(ctx, ep.Summary)
		if err != nil {
			slog.Warn("pitfall_extractor: embed failed, using token fallback",
				"entity_id", g.EntityID, "err", err)
			embeddings[i] = nil
			continue
		}
		embeddings[i] = vector.Float32To64(emb)
	}

	// Initialize each episodic as its own cluster.
	clusters := make([][]int, n)
	for i := 0; i < n; i++ {
		clusters[i] = []int{i}
	}

	// Repeatedly merge closest pair until no pair >= mergeThreshold.
	for {
		bestI, bestJ, bestSim := -1, -1, 0.0
		for i := 0; i < len(clusters); i++ {
			for j := i + 1; j < len(clusters); j++ {
				sim := clusterSimilarity(clusters[i], clusters[j], embeddings)
				if sim > bestSim {
					bestSim = sim
					bestI = i
					bestJ = j
				}
			}
		}
		if bestSim < pe.MergeThreshold || bestI < 0 {
			break
		}
		// Merge cluster j into i.
		clusters[bestI] = append(clusters[bestI], clusters[bestJ]...)
		clusters = append(clusters[:bestJ], clusters[bestJ+1:]...)
	}

	// Convert index clusters to SubCluster results.
	result := make([]SubCluster, len(clusters))
	for ci, idxSet := range clusters {
		for _, idx := range idxSet {
			result[ci].Episodics = append(result[ci].Episodics, g.Episodics[idx])
		}
	}
	return result
}

// clusterSimilarity computes the average pairwise cosine similarity between
// two clusters. Returns 0 if either cluster has no valid embeddings.
func clusterSimilarity(a, b []int, embeddings [][]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var total float64
	var count int
	for _, ai := range a {
		if embeddings[ai] == nil {
			continue
		}
		for _, bi := range b {
			if embeddings[bi] == nil {
				continue
			}
			sim := vector.CosineSimilarity(embeddings[ai], embeddings[bi])
			total += sim
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

// extractFromSubCluster calls the LLM to extract a structured pitfall from a
// sub-cluster of debug episodic memories. Falls back to heuristic extraction
// when no LLM client is configured.
func (pe *PitfallExtractor) extractFromSubCluster(
	ctx context.Context, entityID string, sc SubCluster,
) (models.PitfallMemory, error) {
	projectID := ""
	language := ""
	if len(sc.Episodics) > 0 {
		projectID = sc.Episodics[0].ProjectID
		language = sc.Episodics[0].Language
	}

	sourceIDs := make([]uuid.UUID, len(sc.Episodics))
	for i, ep := range sc.Episodics {
		sourceIDs[i] = ep.ID
	}

	if pe.LLMClient != nil {
		return pe.extractWithLLM(ctx, entityID, projectID, language, sc, sourceIDs)
	}
	return pe.extractHeuristic(entityID, projectID, language, sc, sourceIDs)
}

// extractWithLLM calls the LLM for structured pitfall extraction.
func (pe *PitfallExtractor) extractWithLLM(
	ctx context.Context, entityID, projectID, language string,
	sc SubCluster, sourceIDs []uuid.UUID,
) (models.PitfallMemory, error) {
	systemPrompt := `You are an error pattern analyst. Given a cluster of debug sessions about the same code entity, extract a structured pitfall memory.

Output ONLY valid JSON, no markdown:
{
  "signature": "one-line error pattern description",
  "root_cause_category": "CONCURRENCY|RESOURCE_EXHAUSTION|CONFIG|CONTRACT_VIOLATION|LOGIC_ERROR|UNKNOWN",
  "fix_strategy": "≤500 chars: how the issue was resolved",
  "confidence": 0.0-1.0
}

Rules:
- signature should be a reusable pattern, not a specific traceback
- If summaries are insufficient to determine root cause → category=UNKNOWN
- fix_strategy must be actionable ("increase timeout" not "fix the bug")`

	var userPrompt strings.Builder
	userPrompt.WriteString(fmt.Sprintf("Entity: %s\nProject: %s\nLanguage: %s\n\nDebug sessions:\n",
		entityID, projectID, language))
	for i, ep := range sc.Episodics {
		userPrompt.WriteString(fmt.Sprintf("[%d] %s\n", i+1, ep.Summary))
	}

	response, err := pe.LLMClient.Chat(ctx, systemPrompt, userPrompt.String())
	if err != nil {
		return models.PitfallMemory{}, fmt.Errorf("pitfall LLM call: %w", err)
	}

	output, err := parsePitfallResponse(response)
	if err != nil {
		return models.PitfallMemory{}, fmt.Errorf("pitfall parse: %w", err)
	}

	now := time.Now().UTC()
	pf := models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          entityID,
		EntityType:        inferEntityType(entityID),
		ProjectID:         projectID,
		Language:          language,
		Signature:         output.Signature,
		RootCauseCategory: models.RootCause(output.RootCauseCategory),
		FixStrategy:       output.FixStrategy,
		SourceType:        models.SourceTypeINFERRED,
		TrustLevel:        3,
		Weight:            1.0,
		OccurrenceCount:   len(sc.Episodics),
		CreatedAt:         now,
		UpdatedAt:         now,
		SourceEpisodicIDs: sourceIDs,
	}
	if !pf.RootCauseCategory.IsValid() {
		pf.RootCauseCategory = models.RootCauseUnknown
	}
	return pf, nil
}

// extractHeuristic falls back to basic pattern extraction without LLM.
func (pe *PitfallExtractor) extractHeuristic(
	entityID, projectID, language string,
	sc SubCluster, sourceIDs []uuid.UUID,
) (models.PitfallMemory, error) {
	now := time.Now().UTC()
	signature := fmt.Sprintf("Debug pattern for %s (%d sessions)", entityID, len(sc.Episodics))
	fixStrategy := fmt.Sprintf("Review %d debug sessions for entity %s", len(sc.Episodics), entityID)

	pf := models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          entityID,
		EntityType:        inferEntityType(entityID),
		ProjectID:         projectID,
		Language:          language,
		Signature:         signature,
		RootCauseCategory: models.RootCauseUnknown,
		FixStrategy:       fixStrategy,
		SourceType:        models.SourceTypeINFERRED,
		TrustLevel:        2,
		Weight:            1.0,
		OccurrenceCount:   len(sc.Episodics),
		CreatedAt:         now,
		UpdatedAt:         now,
		SourceEpisodicIDs: sourceIDs,
	}
	return pf, nil
}

// parsePitfallResponse extracts JSON from an LLM response, handling markdown
// code fences and other common wrapping.
func parsePitfallResponse(raw string) (PitfallLLMOutput, error) {
	text := strings.TrimSpace(raw)

	// Strip markdown code fences.
	if strings.HasPrefix(text, "```") {
		idx := strings.Index(text, "\n")
		if idx >= 0 {
			text = text[idx+1:]
		}
		if end := strings.LastIndex(text, "```"); end >= 0 {
			text = text[:end]
		}
		text = strings.TrimSpace(text)
	}

	var output PitfallLLMOutput
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		// Try to recover: find the first JSON object.
		start := strings.Index(text, "{")
		end := strings.LastIndex(text, "}")
		if start >= 0 && end > start {
			if err2 := json.Unmarshal([]byte(text[start:end+1]), &output); err2 != nil {
				return output, fmt.Errorf("parse pitfall JSON: %w (original: %w)", err2, err)
			}
		} else {
			return output, fmt.Errorf("parse pitfall JSON: %w", err)
		}
	}
	return output, nil
}

// pitfallMergeGroup tracks which pitfalls should be merged during deduplication.
type pitfallMergeGroup struct {
	primary int
	members []int
}

// deduplicatePitfalls merges pitfalls whose signatures have cosine similarity
// above the dedup threshold (default 0.9). Returns the deduplicated set.
func (pe *PitfallExtractor) deduplicatePitfalls(
	ctx context.Context, pitfalls []models.PitfallMemory,
) []models.PitfallMemory {
	if len(pitfalls) <= 1 {
		return pitfalls
	}

	// Generate embeddings for each pitfall signature.
	embs := make([][]float64, len(pitfalls))
	for i, p := range pitfalls {
		emb, err := pe.Embedder.Embed(ctx, p.Signature)
		if err != nil {
			embs[i] = nil
			continue
		}
		embs[i] = vector.Float32To64(emb)
	}

	// Greedy merge: for each pitfall, find if it should be merged into an existing one.
	var groups []pitfallMergeGroup
	assigned := make([]bool, len(pitfalls))

	for i := range pitfalls {
		if assigned[i] {
			continue
		}
		mg := pitfallMergeGroup{primary: i, members: []int{i}}
		assigned[i] = true

		for j := i + 1; j < len(pitfalls); j++ {
			if assigned[j] || pitfalls[i].EntityID != pitfalls[j].EntityID {
				continue
			}
			if embs[i] != nil && embs[j] != nil {
				sim := vector.CosineSimilarity(embs[i], embs[j])
				if sim >= pe.DedupThreshold {
					mg.members = append(mg.members, j)
					assigned[j] = true
				}
			} else if pitfalls[i].Signature == pitfalls[j].Signature {
				mg.members = append(mg.members, j)
				assigned[j] = true
			}
		}
		groups = append(groups, mg)
	}

	result := make([]models.PitfallMemory, len(groups))
	for gi, mg := range groups {
		if len(mg.members) == 1 {
			result[gi] = pitfalls[mg.primary]
		} else {
			result[gi] = pe.mergePitfallGroup(pitfalls, mg)
		}
	}
	return result
}

// mergePitfallGroup merges multiple pitfalls into a single primary (the one
// with highest occurrence_count). Follows the merge strategy in design doc §4.4.
func (pe *PitfallExtractor) mergePitfallGroup(pitfalls []models.PitfallMemory, mg pitfallMergeGroup) models.PitfallMemory {
	primary := pitfalls[mg.primary]
	for _, idx := range mg.members {
		if idx == mg.primary {
			continue
		}
		other := pitfalls[idx]
		// Use higher occurrence_count pitfall as base.
		if other.OccurrenceCount > primary.OccurrenceCount {
			otherOccurrence := other.OccurrenceCount
			other.ObsoletedAt = primary.ObsoletedAt // preserve obsoleted status
			primary, other = other, primary
			primary.OccurrenceCount = otherOccurrence
		}
		primary.OccurrenceCount += other.OccurrenceCount
		primary.SourceEpisodicIDs = append(primary.SourceEpisodicIDs, other.SourceEpisodicIDs...)
		if len(other.FixStrategy) > len(primary.FixStrategy) {
			primary.FixStrategy = other.FixStrategy
		}
		if other.LastOccurredAt != nil {
			if primary.LastOccurredAt == nil || other.LastOccurredAt.After(*primary.LastOccurredAt) {
				primary.LastOccurredAt = other.LastOccurredAt
			}
		}
		if other.WasUserCorrected {
			primary.WasUserCorrected = true
		}
	}
	return primary
}

// inferEntityType guesses the entity type from the entity_id prefix convention.
// Convention: "func:" / "module:" / "api:" / "config:" / "query:" / default FUNCTION.
func inferEntityType(entityID string) models.EntityType {
	switch {
	case strings.HasPrefix(entityID, "func:"):
		return models.EntityTypeFunction
	case strings.HasPrefix(entityID, "module:"):
		return models.EntityTypeModule
	case strings.HasPrefix(entityID, "api:"):
		return models.EntityTypeAPI
	case strings.HasPrefix(entityID, "config:"):
		return models.EntityTypeConfig
	case strings.HasPrefix(entityID, "query:"):
		return models.EntityTypeQuery
	default:
		return models.EntityTypeFunction
	}
}

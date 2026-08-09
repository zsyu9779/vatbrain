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

// lexicalClusterMergeThreshold is the HAC merge threshold used when all
// embeddings lack signal (stub embedder / no API key) and clustering falls
// back to char-bigram Dice, whose scale is lower than cosine similarity.
const lexicalClusterMergeThreshold = 0.3

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
		v := vector.Float32To64(emb)
		// 零向量（stub embedder / 无 API key）无语义信号 → 标记 nil 走词法回退。
		if !vectorHasMagnitude64(v) {
			embeddings[i] = nil
			continue
		}
		embeddings[i] = v
	}

	// Initialize each episodic as its own cluster.
	clusters := make([][]int, n)
	for i := 0; i < n; i++ {
		clusters[i] = []int{i}
	}

	// 词法模式：全部 embedding 无信号（stub / 无 API key）时，bigram Dice 的
	// 量纲低于 embedding 余弦——用更低的词法合并阈值（0.85 余弦 → 0.3 bigram）。
	lexicalMode := true
	for _, e := range embeddings {
		if e != nil {
			lexicalMode = false
			break
		}
	}
	mergeThreshold := pe.MergeThreshold
	if lexicalMode {
		mergeThreshold = lexicalClusterMergeThreshold
	}

	// Repeatedly merge closest pair until no pair >= mergeThreshold.
	for {
		bestI, bestJ, bestSim := -1, -1, 0.0
		for i := 0; i < len(clusters); i++ {
			for j := i + 1; j < len(clusters); j++ {
				sim := clusterSimilarity(clusters[i], clusters[j], embeddings, g.Episodics)
				if sim > bestSim {
					bestSim = sim
					bestI = i
					bestJ = j
				}
			}
		}
		if bestSim < mergeThreshold || bestI < 0 {
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
func clusterSimilarity(a, b []int, embeddings [][]float64, episodics []store.EpisodicScanItem) float64 {
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
		// F1 回退：零向量（stub embedder / 无 API key）时用 CJK 安全的字符
		// bigram 重叠作为聚类代理，避免中文 debug 记忆静默不聚类。
		return bigramOverlapCluster(a, b, episodics)
	}
	return total / float64(count)
}

// bigramOverlapCluster returns the average char-bigram Dice overlap between
// two clusters' episode summaries (CJK-safe lexical proxy).
func bigramOverlapCluster(a, b []int, episodics []store.EpisodicScanItem) float64 {
	var total float64
	var pairs int
	for _, ai := range a {
		if ai < 0 || ai >= len(episodics) {
			continue
		}
		for _, bi := range b {
			if bi < 0 || bi >= len(episodics) {
				continue
			}
			total += charBigramOverlap(episodics[ai].Summary, episodics[bi].Summary)
			pairs++
		}
	}
	if pairs == 0 {
		return 0
	}
	return total / float64(pairs)
}

// charBigramOverlap is the Dice coefficient of the char-bigram sets of a and b.
func charBigramOverlap(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	as := charBigrams(a)
	bs := charBigrams(b)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	inter := 0
	for g := range as {
		if _, ok := bs[g]; ok {
			inter++
		}
	}
	return 2.0 * float64(inter) / float64(len(as)+len(bs))
}

// charBigrams builds the character-bigram set of s (Chinese + Latin safe).
func charBigrams(s string) map[string]struct{} {
	r := []rune(s)
	if len(r) == 1 {
		return map[string]struct{}{string(r[0]): {}}
	}
	out := make(map[string]struct{}, len(r)-1)
	for i := 0; i+1 < len(r); i++ {
		out[string(r[i:i+2])] = struct{}{}
	}
	return out
}

// truncateRunes bounds a string to max runes (CJK-aware).
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// vectorHasMagnitude64 reports whether v has any non-zero component.
func vectorHasMagnitude64(v []float64) bool {
	for _, c := range v {
		if c != 0 {
			return true
		}
	}
	return false
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

// extractHeuristic falls back to pattern extraction without LLM. The
// signature is anchored on the most representative episode summary (the one
// with the highest average overlap to the rest) so it carries the real error
// content and remains text-retrievable (CJK-safe), instead of a generic
// "Debug pattern for <entity>" placeholder.
func (pe *PitfallExtractor) extractHeuristic(
	entityID, projectID, language string,
	sc SubCluster, sourceIDs []uuid.UUID,
) (models.PitfallMemory, error) {
	now := time.Now().UTC()

	representative := representativeSummary(sc.Episodics)
	signature := truncateRunes(fmt.Sprintf("%s: %s", entityID, representative), 200)
	fixStrategy := truncateRunes(representative, 200)

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

// representativeSummary picks the episode summary with the highest average
// char-bigram overlap to the rest of the cluster — the one that best captures
// the shared error pattern.
func representativeSummary(episodics []store.EpisodicScanItem) string {
	if len(episodics) == 0 {
		return ""
	}
	if len(episodics) == 1 {
		return episodics[0].Summary
	}
	bestIdx, bestScore := 0, -1.0
	for i := range episodics {
		var total float64
		for j := range episodics {
			if i == j {
				continue
			}
			total += charBigramOverlap(episodics[i].Summary, episodics[j].Summary)
		}
		avg := total / float64(len(episodics)-1)
		if avg > bestScore {
			bestScore = avg
			bestIdx = i
		}
	}
	return episodics[bestIdx].Summary
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

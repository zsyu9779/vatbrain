package core

import (
	"context"
	"sort"
	"time"

	"github.com/vatbrain/vatbrain/internal/models"
)

// RetrievalEngine implements the two-stage retrieval pipeline described in
// DESIGN_PRINCIPLES.md Section 4.1.
//
// Stage 1 (Contextual Gating): narrow the candidate pool using cheap constraints —
// project_id + language hard-filter, task_type soft-weight.
// Stage 2 (Semantic Ranking): rank the survivors by vector cosine similarity.
type RetrievalEngine struct {
	// ContextCacheTTL is how long to cache Stage 1 results. Default 1 hour.
	ContextCacheTTL time.Duration
	// MaxCandidates is the maximum number of candidates to pass from Stage 1 to
	// Stage 2. Controls the tradeoff between recall and latency. Default 500.
	MaxCandidates int
}

// DefaultRetrievalEngine returns a RetrievalEngine with v0.1 tuned defaults.
func DefaultRetrievalEngine() *RetrievalEngine {
	return &RetrievalEngine{
		ContextCacheTTL: 1 * time.Hour,
		MaxCandidates:   500,
	}
}

// ── Stage 1: Contextual Gating ─────────────────────────────────────────────

// ContextualGating narrows the candidate pool by project, language, and task
// context before expensive vector comparison.
type ContextualGating struct{}

// HardFilterResult captures what survived the hard-constraint pass.
type HardFilterResult struct {
	MemoryID string
	Weight   float64
}

// ApplyHardConstraints returns the list of memory IDs that match project_id and
// language, have not been obsoleted, and are above the cooling threshold.
func (cg *ContextualGating) ApplyHardConstraints(
	candidates []models.EpisodicMemory,
	ctx models.SearchContext,
	coolingThreshold float64,
) []HardFilterResult {
	var results []HardFilterResult
	for _, m := range candidates {
		if m.ProjectID != ctx.ProjectID {
			continue
		}
		if m.Language != ctx.Language {
			continue
		}
		if m.ObsoletedAt != nil {
			continue
		}
		if m.Weight < coolingThreshold {
			continue
		}
		results = append(results, HardFilterResult{
			MemoryID: m.ID.String(),
			Weight:   m.Weight,
		})
	}
	return results
}

// ApplySoftWeights adjusts candidate weights based on context signals.
// A task_type match boosts weight by +20%.
func (cg *ContextualGating) ApplySoftWeights(
	candidates []HardFilterResult,
	ctx models.SearchContext,
) []HardFilterResult {
	adjusted := make([]HardFilterResult, len(candidates))
	for i, c := range candidates {
		boost := 1.0
		if ctx.TaskType.IsValid() {
			boost += 0.2
		}
		adjusted[i] = HardFilterResult{
			MemoryID: c.MemoryID,
			Weight:   c.Weight * boost,
		}
	}
	return adjusted
}

// FilterAndRank runs the full Stage 1 pipeline: hard constraints → soft weights →
// sort by weight descending → cap at maxCandidates.
func (cg *ContextualGating) FilterAndRank(
	candidates []models.EpisodicMemory,
	ctx models.SearchContext,
	coolingThreshold float64,
	maxCandidates int,
) ([]HardFilterResult, models.ContextFilterStats) {
	start := time.Now()

	hard := cg.ApplyHardConstraints(candidates, ctx, coolingThreshold)
	soft := cg.ApplySoftWeights(hard, ctx)

	sort.Slice(soft, func(i, j int) bool {
		return soft[i].Weight > soft[j].Weight
	})

	if len(soft) > maxCandidates {
		soft = soft[:maxCandidates]
	}

	stats := models.ContextFilterStats{
		TotalCandidates: len(candidates),
		AfterFilter:     len(soft),
		FilterTimeMs:    time.Since(start).Milliseconds(),
	}
	return soft, stats
}

// ── Stage 2: Semantic Ranking ──────────────────────────────────────────────

// SemanticRanker performs vector similarity ranking within a filtered candidate set.
type SemanticRanker struct{}

// RankedResult is a single ranked memory from Stage 2.
type RankedResult struct {
	MemoryID       string
	RelevanceScore float64
}

// Rank computes cosine similarity between the query embedding and each candidate
// embedding, then returns results sorted by relevance descending.
func (sr *SemanticRanker) Rank(
	queryEmbedding []float32,
	candidateEmbeddings map[string][]float32,
) []RankedResult {
	var results []RankedResult
	for id, emb := range candidateEmbeddings {
		sim := cosineSimilarity(queryEmbedding, emb)
		results = append(results, RankedResult{
			MemoryID:       id,
			RelevanceScore: sim,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].RelevanceScore > results[j].RelevanceScore
	})

	return results
}

// ── Full Pipeline ──────────────────────────────────────────────────────────

// RetrieveRequest bundles all inputs for a two-stage retrieval.
type RetrieveRequest struct {
	Query               string
	QueryEmbedding      []float32
	Context             models.SearchContext
	EpisodicCandidates  []models.EpisodicMemory
	CandidateEmbeddings map[string][]float32
	TopK                int
	IncludeDormant      bool
	CoolingThreshold    float64
}

// RetrieveResult wraps the full output of a two-stage retrieval.
type RetrieveResult struct {
	Results        []RankedResult
	FilterStats    models.ContextFilterStats
	SemanticRankMs int64
}

// Retrieve runs the full two-stage pipeline: Contextual Gating → Semantic Ranking.
func (e *RetrievalEngine) Retrieve(ctx context.Context, req RetrieveRequest) RetrieveResult {
	gating := &ContextualGating{}
	ranker := &SemanticRanker{}

	threshold := req.CoolingThreshold
	if threshold <= 0 {
		threshold = models.CoolingThreshold
	}
	maxCand := e.MaxCandidates
	if maxCand <= 0 {
		maxCand = 500
	}

	filtered, stats := gating.FilterAndRank(
		req.EpisodicCandidates,
		req.Context,
		threshold,
		maxCand,
	)

	filteredSet := make(map[string]struct{}, len(filtered))
	for _, f := range filtered {
		filteredSet[f.MemoryID] = struct{}{}
	}
	restrictedEmbeddings := make(map[string][]float32, len(filtered))
	for id, emb := range req.CandidateEmbeddings {
		if _, ok := filteredSet[id]; ok {
			restrictedEmbeddings[id] = emb
		}
	}

	rankStart := time.Now()
	ranked := ranker.Rank(req.QueryEmbedding, restrictedEmbeddings)

	topK := req.TopK
	if topK <= 0 || topK > len(ranked) {
		topK = len(ranked)
	}
	ranked = ranked[:topK]

	return RetrieveResult{
		Results:        ranked,
		FilterStats:    stats,
		SemanticRankMs: time.Since(rankStart).Milliseconds(),
	}
}

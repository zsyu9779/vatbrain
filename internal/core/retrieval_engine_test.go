package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/vatbrain/vatbrain/internal/models"
)

// ── Stage 1: Contextual Gating ─────────────────────────────────────────────

func TestContextualGating_HardConstraints_FiltersByProject(t *testing.T) {
	cg := &ContextualGating{}
	candidates := []models.EpisodicMemory{
		{ID: uuid.New(), ProjectID: "proj-a", Language: "go", Weight: 1.0},
		{ID: uuid.New(), ProjectID: "proj-b", Language: "go", Weight: 1.0},
	}
	ctx := models.SearchContext{ProjectID: "proj-a", Language: "go"}
	results := cg.ApplyHardConstraints(candidates, ctx, models.CoolingThreshold)
	assert.Len(t, results, 1)
}

func TestContextualGating_HardConstraints_FiltersByLanguage(t *testing.T) {
	cg := &ContextualGating{}
	candidates := []models.EpisodicMemory{
		{ID: uuid.New(), ProjectID: "p", Language: "go", Weight: 1.0},
		{ID: uuid.New(), ProjectID: "p", Language: "python", Weight: 1.0},
	}
	ctx := models.SearchContext{ProjectID: "p", Language: "go"}
	results := cg.ApplyHardConstraints(candidates, ctx, models.CoolingThreshold)
	assert.Len(t, results, 1)
}

func TestContextualGating_ExcludesObsoleted(t *testing.T) {
	cg := &ContextualGating{}
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	candidates := []models.EpisodicMemory{
		{ID: uuid.New(), ProjectID: "p", Language: "go", Weight: 1.0},
		{ID: uuid.New(), ProjectID: "p", Language: "go", Weight: 1.0, ObsoletedAt: &past},
	}
	ctx := models.SearchContext{ProjectID: "p", Language: "go"}
	results := cg.ApplyHardConstraints(candidates, ctx, models.CoolingThreshold)
	assert.Len(t, results, 1)
}

func TestContextualGating_ExcludesBelowCooling(t *testing.T) {
	cg := &ContextualGating{}
	candidates := []models.EpisodicMemory{
		{ID: uuid.New(), ProjectID: "p", Language: "go", Weight: 1.0},
		{ID: uuid.New(), ProjectID: "p", Language: "go", Weight: 0.001},
	}
	ctx := models.SearchContext{ProjectID: "p", Language: "go"}
	results := cg.ApplyHardConstraints(candidates, ctx, models.CoolingThreshold)
	assert.Len(t, results, 1)
}

func TestContextualGating_FilterAndRank_SortsByWeight(t *testing.T) {
	cg := &ContextualGating{}
	candidates := []models.EpisodicMemory{
		{ID: uuid.MustParse("aaaaaaaa-1111-1111-1111-111111111111"), ProjectID: "p", Language: "go", Weight: 3.0},
		{ID: uuid.MustParse("bbbbbbbb-1111-1111-1111-111111111111"), ProjectID: "p", Language: "go", Weight: 1.0},
		{ID: uuid.MustParse("cccccccc-1111-1111-1111-111111111111"), ProjectID: "p", Language: "go", Weight: 5.0},
		{ID: uuid.New(), ProjectID: "other", Language: "go", Weight: 10.0},
	}
	ctx := models.SearchContext{ProjectID: "p", Language: "go", TaskType: models.TaskTypeDebug}

	results, stats := cg.FilterAndRank(candidates, ctx, models.CoolingThreshold, 500)

	// one excluded by project, 3 remain sorted by weight desc (times 1.2 task boost)
	assert.Equal(t, 4, stats.TotalCandidates)
	assert.Equal(t, 3, stats.AfterFilter)
	assert.True(t, results[0].Weight > results[1].Weight)
	assert.True(t, results[1].Weight > results[2].Weight)
}

func TestContextualGating_MaxCandidates_Caps(t *testing.T) {
	cg := &ContextualGating{}
	var candidates []models.EpisodicMemory
	for i := range 10 {
		candidates = append(candidates, models.EpisodicMemory{
			ID:        uuid.New(),
			ProjectID: "p",
			Language:  "go",
			Weight:    float64(10 - i),
		})
	}
	ctx := models.SearchContext{ProjectID: "p", Language: "go"}
	results, stats := cg.FilterAndRank(candidates, ctx, models.CoolingThreshold, 3)
	assert.Equal(t, 10, stats.TotalCandidates)
	assert.Len(t, results, 3)
}

// ── Stage 2: Semantic Ranking ─────────────────────────────────────────────

func TestSemanticRanker_Rank(t *testing.T) {
	sr := &SemanticRanker{}
	queryEmb := []float32{1, 0, 0}
	candidateEmbs := map[string][]float32{
		"a": {1, 0, 0},
		"b": {0, 1, 0},
		"c": {0.707, 0.707, 0},
	}

	results := sr.Rank(queryEmb, candidateEmbs)
	assert.Len(t, results, 3)
	assert.Equal(t, "a", results[0].MemoryID)
	assert.InDelta(t, 1.0, results[0].RelevanceScore, 0.01)
	assert.Equal(t, "c", results[1].MemoryID)
	assert.InDelta(t, 0.707, results[1].RelevanceScore, 0.01)
	assert.Equal(t, "b", results[2].MemoryID)
	assert.InDelta(t, 0.0, results[2].RelevanceScore, 0.01)
}

func TestSemanticRanker_EmptyCandidates(t *testing.T) {
	sr := &SemanticRanker{}
	results := sr.Rank([]float32{1, 0}, map[string][]float32{})
	assert.Empty(t, results)
}

// ── Full Pipeline ──────────────────────────────────────────────────────────

func TestRetrievalEngine_FullPipeline(t *testing.T) {
	e := DefaultRetrievalEngine()

	aID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	bID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	cID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")

	req := RetrieveRequest{
		Query:          "redis pool exhausted",
		QueryEmbedding: []float32{1, 0},
		Context: models.SearchContext{
			ProjectID: "my-project",
			Language:  "go",
			TaskType:  models.TaskTypeDebug,
		},
		EpisodicCandidates: []models.EpisodicMemory{
			{ID: aID, ProjectID: "my-project", Language: "go", Weight: 3.0},
			{ID: bID, ProjectID: "my-project", Language: "go", Weight: 1.0},
			{ID: cID, ProjectID: "other-project", Language: "go", Weight: 5.0},
		},
		CandidateEmbeddings: map[string][]float32{
			aID.String(): {0.9, 0.1},
			bID.String(): {0.1, 0.9},
			cID.String(): {1, 0},
		},
		TopK:             2,
		CoolingThreshold: models.CoolingThreshold,
	}

	result := e.Retrieve(context.Background(), req)

	assert.Equal(t, 3, result.FilterStats.TotalCandidates)
	assert.Equal(t, 2, result.FilterStats.AfterFilter)
	assert.Len(t, result.Results, 2)
	// a should rank highest (cos sim to [1,0] is higher for [0.9,0.1] than [0.1,0.9])
	assert.Greater(t, result.Results[0].RelevanceScore, result.Results[1].RelevanceScore)
}

func TestRetrievalEngine_EmptyCandidates(t *testing.T) {
	e := DefaultRetrievalEngine()
	req := RetrieveRequest{
		Query:               "anything",
		QueryEmbedding:      []float32{1, 0},
		Context:             models.SearchContext{ProjectID: "p", Language: "go"},
		EpisodicCandidates:  []models.EpisodicMemory{},
		CandidateEmbeddings: map[string][]float32{},
		TopK:                10,
		CoolingThreshold:    models.CoolingThreshold,
	}
	result := e.Retrieve(context.Background(), req)
	assert.Empty(t, result.Results)
}

func TestRetrievalEngine_TopK_Clamping(t *testing.T) {
	e := DefaultRetrievalEngine()
	aID := uuid.New()
	req := RetrieveRequest{
		Query:          "test",
		QueryEmbedding: []float32{1, 0},
		Context:        models.SearchContext{ProjectID: "p", Language: "go"},
		EpisodicCandidates: []models.EpisodicMemory{
			{ID: aID, ProjectID: "p", Language: "go", Weight: 1.0},
		},
		CandidateEmbeddings: map[string][]float32{aID.String(): {1, 0}},
		TopK:                100,
		CoolingThreshold:    models.CoolingThreshold,
	}
	result := e.Retrieve(context.Background(), req)
	assert.Len(t, result.Results, 1)
}

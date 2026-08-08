package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/llm"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

func makeScanResult(projectID, taskType, summary string) store.EpisodicScanItem {
	return store.EpisodicScanItem{
		ID:        uuid.New(),
		ProjectID: projectID,
		TaskType:  models.TaskType(taskType),
		Summary:   summary,
	}
}

func TestClusterByPattern_GroupsByProjectAndTaskType(t *testing.T) {
	eps := []store.EpisodicScanItem{
		makeScanResult("projA", "debug", "debug session 1"),
		makeScanResult("projA", "debug", "debug session 2"),
		makeScanResult("projA", "debug", "debug session 3"),
		makeScanResult("projA", "feature", "feature work 1"),
		makeScanResult("projB", "debug", "projB debug"),
	}

	clusters := clusterByPattern(eps, 2)

	assert.Len(t, clusters, 1)
	assert.Equal(t, "projA", clusters[0].ProjectID)
	assert.Equal(t, models.TaskType("debug"), clusters[0].TaskType)
	assert.Len(t, clusters[0].Episodics, 3)
}

func TestClusterByPattern_EmptyInput(t *testing.T) {
	clusters := clusterByPattern(nil, 3)
	assert.Empty(t, clusters)
}

func TestClusterByPattern_BelowMinSize(t *testing.T) {
	eps := []store.EpisodicScanItem{
		makeScanResult("projA", "debug", "s1"),
		makeScanResult("projA", "debug", "s2"),
	}

	clusters := clusterByPattern(eps, 3)
	assert.Empty(t, clusters)
}

func TestClusterByPattern_AtMinSize(t *testing.T) {
	eps := []store.EpisodicScanItem{
		makeScanResult("projA", "debug", "s1"),
		makeScanResult("projA", "debug", "s2"),
		makeScanResult("projA", "debug", "s3"),
	}

	clusters := clusterByPattern(eps, 3)
	assert.Len(t, clusters, 1)
	assert.Len(t, clusters[0].Episodics, 3)
}

func TestClusterByPattern_MultipleClusters(t *testing.T) {
	eps := []store.EpisodicScanItem{
		makeScanResult("projA", "debug", "a1"), makeScanResult("projA", "debug", "a2"),
		makeScanResult("projA", "debug", "a3"),
		makeScanResult("projB", "feature", "b1"), makeScanResult("projB", "feature", "b2"),
		makeScanResult("projB", "feature", "b3"),
	}

	clusters := clusterByPattern(eps, 3)
	assert.Len(t, clusters, 2)
}

func TestExtractRule_ProducesContent(t *testing.T) {
	e := &ConsolidationEngine{}
	cl := PatternCluster{
		ProjectID: "test-proj",
		TaskType:  models.TaskTypeDebug,
		Episodics: []store.EpisodicScanItem{
			makeScanResult("test-proj", "debug", "nil pointer in handler"),
			makeScanResult("test-proj", "debug", "nil pointer in handler again"),
		},
	}

	rule := e.extractRule(t.Context(), cl)
	assert.Contains(t, rule, "test-proj/debug")
	assert.Contains(t, rule, "nil pointer in handler")
	assert.True(t, strings.Count(rule, "\n") >= 1)
}

func TestExtractRule_SingleEpisodic(t *testing.T) {
	e := &ConsolidationEngine{}
	cl := PatternCluster{
		ProjectID: "solo",
		TaskType:  models.TaskTypeRefactor,
		Episodics: []store.EpisodicScanItem{
			makeScanResult("solo", "refactor", "one event"),
		},
	}

	rule := e.extractRule(t.Context(), cl)
	assert.Contains(t, rule, "one event")
}

// F3: without an LLM the backtest is no longer a size-based rubber stamp.
// With no embedder signal it returns a constant below AccuracyThreshold, so
// unverified rules never persist on cluster size alone.
func TestBacktest_NoLLM_NilEmbedder_ConstantBelowThreshold(t *testing.T) {
	e := &ConsolidationEngine{MinClusterSize: 3, AccuracyThreshold: 0.7}
	cl := PatternCluster{
		Episodics: make([]store.EpisodicScanItem, 5),
	}
	assert.Equal(t, 0.5, e.backtest(t.Context(), nil, cl),
		"no LLM, no embedder → conservative constant, never passes 0.7")
}

// F3: with an embedder, a coherent cluster (near-identical summaries) scores
// above the threshold — legitimate patterns still persist.
func TestBacktest_EmbeddingConsistency_High(t *testing.T) {
	e := &ConsolidationEngine{MinClusterSize: 3, AccuracyThreshold: 0.7}
	cl := PatternCluster{
		ProjectID: "p",
		TaskType:  models.TaskTypeDebug,
		Episodics: []store.EpisodicScanItem{
			{Summary: "并发问题通常出在锁粒度，要仔细检查锁的范围和持有时间"},
			{Summary: "并发问题出在锁粒度上，需要仔细检查锁的范围与持有时间"},
			{Summary: "并发问题一般出在锁粒度，应该仔细检查锁的范围和持有时间"},
		},
	}
	score := e.backtest(t.Context(), runeEmbedder{}, cl)
	assert.GreaterOrEqual(t, score, 0.7, "coherent cluster must pass the backtest")
}

// F3: an incoherent cluster (mixed topics) scores below the threshold and its
// rule must not be persisted.
func TestBacktest_EmbeddingConsistency_Low(t *testing.T) {
	e := &ConsolidationEngine{MinClusterSize: 3, AccuracyThreshold: 0.7}
	cl := PatternCluster{
		ProjectID: "p",
		TaskType:  models.TaskTypeDebug,
		Episodics: []store.EpisodicScanItem{
			{Summary: "数据库连接池耗尽导致请求超时"},
			{Summary: "前端组件渲染性能优化"},
			{Summary: "CI 流水线构建缓存失效"},
		},
	}
	score := e.backtest(t.Context(), runeEmbedder{}, cl)
	assert.Less(t, score, 0.7, "incoherent cluster must fail the backtest")
}

func TestDefaultConsolidationEngine(t *testing.T) {
	e := DefaultConsolidationEngine()
	assert.Equal(t, 24.0, e.HoursToScan)
	assert.Equal(t, 3, e.MinClusterSize)
	assert.Equal(t, 0.7, e.AccuracyThreshold)
}

func TestConsolidationEngine_Run(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	// F3: the rune embedder yields a real similarity signal, so the coherent
	// cluster below passes the embedding-consistency backtest.
	emb := runeEmbedder{}
	now := time.Now()

	// Write 4 episodic memories with same (project, task_type) to exceed MinClusterSize=3.
	for i := 0; i < 4; i++ {
		mem := &models.EpisodicMemory{
			ID:         uuid.New(),
			ProjectID:  "cons-proj",
			TaskType:   models.TaskTypeDebug,
			Summary:    "debug session " + string(rune('a'+i)),
			SourceType: models.SourceTypeUSER,
			TrustLevel: 5,
			Weight:     1.0,
			CreatedAt:  now,
		}
		require.NoError(t, s.WriteEpisodic(ctx, mem))
	}

	e := &ConsolidationEngine{
		HoursToScan:       24,
		MinClusterSize:    3,
		AccuracyThreshold: 0.7,
	}

	result, err := e.Run(ctx, s, emb)
	require.NoError(t, err)
	assert.Equal(t, 4, result.EpisodicsScanned)
	assert.Equal(t, 1, result.RulesPersisted)
	assert.NotNil(t, result.CompletedAt)

	// Verify semantic memory was written.
	sems, semErr := s.SearchSemantic(ctx, store.SemanticSearchRequest{Limit: 10})
	require.NoError(t, semErr)
	assert.NotEmpty(t, sems, "expected semantic memory to be created")
}

// F3 acceptance: without an LLM and without a real embedding signal (stub
// yields zero vectors), consolidation must NOT bulk-persist unverified rules.
func TestConsolidationEngine_Run_NoSignal_NoRulesPersisted(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	emb := embedder.NewStubEmbedder()
	now := time.Now()

	// Same shape as TestConsolidationEngine_Run — 4 same-pattern memories.
	for i := 0; i < 4; i++ {
		mem := &models.EpisodicMemory{
			ID:         uuid.New(),
			ProjectID:  "cons-proj",
			TaskType:   models.TaskTypeDebug,
			Summary:    "debug session " + string(rune('a'+i)),
			SourceType: models.SourceTypeUSER,
			TrustLevel: 5,
			Weight:     1.0,
			CreatedAt:  now,
		}
		require.NoError(t, s.WriteEpisodic(ctx, mem))
	}

	e := &ConsolidationEngine{
		HoursToScan:       24,
		MinClusterSize:    3,
		AccuracyThreshold: 0.7,
	}

	result, err := e.Run(ctx, s, emb)
	require.NoError(t, err)
	assert.Equal(t, 4, result.EpisodicsScanned)
	assert.Equal(t, 1, result.CandidateRulesFound, "cluster exists but fails backtest")
	assert.Equal(t, 0, result.RulesPersisted,
		"no API key + no embedding signal → no unverified rules persisted")
}

func TestConsolidationEngine_Run_NoEpisodics(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	emb := embedder.NewStubEmbedder()

	e := &ConsolidationEngine{
		HoursToScan:       24,
		MinClusterSize:    3,
		AccuracyThreshold: 0.7,
	}

	result, err := e.Run(ctx, s, emb)
	require.NoError(t, err)
	assert.Equal(t, 0, result.EpisodicsScanned)
}

func TestConsolidationEngine_Run_BelowMinCluster(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	emb := embedder.NewStubEmbedder()
	now := time.Now()

	// Write only 2 memories — below MinClusterSize=3.
	for i := 0; i < 2; i++ {
		mem := &models.EpisodicMemory{
			ID:         uuid.New(),
			ProjectID:  "small-cluster",
			TaskType:   models.TaskTypeDebug,
			Summary:    "debug " + string(rune('a'+i)),
			SourceType: models.SourceTypeUSER,
			TrustLevel: 5,
			Weight:     1.0,
			CreatedAt:  now,
		}
		require.NoError(t, s.WriteEpisodic(ctx, mem))
	}

	e := &ConsolidationEngine{
		HoursToScan:       24,
		MinClusterSize:    3,
		AccuracyThreshold: 0.7,
	}

	result, err := e.Run(ctx, s, emb)
	require.NoError(t, err)
	assert.Equal(t, 2, result.EpisodicsScanned)
	assert.Equal(t, 0, result.RulesPersisted)
}

func TestErrToString(t *testing.T) {
	assert.Equal(t, "", errToString(nil))
	assert.Equal(t, "context canceled", errToString(context.Canceled))
}

func TestExtractRule_LLM(t *testing.T) {
	e := &ConsolidationEngine{
		LLMClient: &llm.MockClient{Response: "extracted pattern: always check errors first"},
	}
	cl := PatternCluster{
		ProjectID: "proj",
		TaskType:  models.TaskTypeDebug,
		Episodics: []store.EpisodicScanItem{
			makeScanResult("proj", "debug", "nil pointer"),
			makeScanResult("proj", "debug", "nil pointer again"),
		},
	}
	rule := e.extractRule(t.Context(), cl)
	assert.Contains(t, rule, "extracted pattern")
}

func TestExtractRule_LLMError_Fallback(t *testing.T) {
	e := &ConsolidationEngine{
		LLMClient: &llm.MockClient{Err: context.Canceled},
	}
	cl := PatternCluster{
		ProjectID: "proj",
		TaskType:  models.TaskTypeDebug,
		Episodics: []store.EpisodicScanItem{
			makeScanResult("proj", "debug", "nil pointer in handler"),
		},
	}
	rule := e.extractRule(t.Context(), cl)
	// Should fall back to v0.1 string concatenation.
	assert.Contains(t, rule, "proj/debug")
	assert.Contains(t, rule, "nil pointer in handler")
}

func TestBacktest_LLM_ValidScore(t *testing.T) {
	e := &ConsolidationEngine{
		MinClusterSize: 3,
		LLMClient:      &llm.MockClient{Response: "0.85"},
	}
	cl := PatternCluster{
		Episodics: make([]store.EpisodicScanItem, 5),
	}
	for i := range cl.Episodics {
		cl.Episodics[i] = makeScanResult("p", "debug", "event")
	}
	score := e.backtest(t.Context(), nil, cl)
	assert.InDelta(t, 0.85, score, 0.01)
}

func TestBacktest_LLM_InvalidResponse(t *testing.T) {
	e := &ConsolidationEngine{
		MinClusterSize: 3,
		LLMClient:      &llm.MockClient{Response: "not a number"},
	}
	cl := PatternCluster{
		Episodics: make([]store.EpisodicScanItem, 5),
	}
	for i := range cl.Episodics {
		cl.Episodics[i] = makeScanResult("p", "debug", "event")
	}
	score := e.backtest(t.Context(), nil, cl)
	// Fallback (F3): unparseable LLM output → no signal → constant below
	// threshold, rule not persisted.
	assert.Equal(t, 0.5, score)
}

func TestBacktest_LLM_SmallSample(t *testing.T) {
	e := &ConsolidationEngine{
		MinClusterSize: 3,
		LLMClient:      &llm.MockClient{Response: "0.9"},
	}
	cl := PatternCluster{
		Episodics: make([]store.EpisodicScanItem, 2),
	}
	for i := range cl.Episodics {
		cl.Episodics[i] = makeScanResult("p", "debug", "event")
	}
	score := e.backtest(t.Context(), nil, cl)
	// Sample size < 3 returns 0.0 with LLM.
	assert.Equal(t, 0.0, score)
}

func TestConsolidationEngine_Run_WithLLM(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	emb := embedder.NewStubEmbedder()
	now := time.Now()

	for i := 0; i < 3; i++ {
		mem := &models.EpisodicMemory{
			ID:         uuid.New(),
			ProjectID:  "llm-cons",
			TaskType:   models.TaskTypeDebug,
			Summary:    "debug " + string(rune('a'+i)),
			SourceType: models.SourceTypeUSER,
			TrustLevel: 5,
			Weight:     1.0,
			CreatedAt:  now,
		}
		require.NoError(t, s.WriteEpisodic(ctx, mem))
	}

	e := &ConsolidationEngine{
		HoursToScan:       24,
		MinClusterSize:    3,
		AccuracyThreshold: 0.5,
		LLMClient:         &llm.MockClient{Response: "check for nil pointers"},
	}

	result, err := e.Run(ctx, s, emb)
	require.NoError(t, err)
	assert.Equal(t, 3, result.EpisodicsScanned)
	assert.Equal(t, 1, result.RulesPersisted)
}

func TestConsolidationEngine_Run_WithPitfallExtractor(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	emb := embedder.NewStubEmbedder()
	now := time.Now()

	// Write debug episodics with EntityGroup set (→ EntityID in scan item).
	for i := 0; i < 3; i++ {
		mem := &models.EpisodicMemory{
			ID:          uuid.New(),
			ProjectID:   "pf-cons-proj",
			TaskType:    models.TaskTypeDebug,
			Summary:     "nil pointer in func:BugFunc",
			SourceType:  models.SourceTypeUSER,
			TrustLevel:  5,
			Weight:      1.0,
			CreatedAt:   now,
			EntityGroup: "func:BugFunc",
		}
		require.NoError(t, s.WriteEpisodic(ctx, mem))
	}

	e := &ConsolidationEngine{
		HoursToScan:       24,
		MinClusterSize:    3,
		AccuracyThreshold: 0.7,
		LLMClient:         &llm.MockClient{Response: "check for nil pointers"},
		PitfallExtractor: &PitfallExtractor{
			MinClusterSize: 9999, // filter out all entity groups
			Embedder:       emb,
			LLMClient:      &llm.MockClient{Response: `{"signature":"nil pointer","root_cause_category":"logic_error","fix_strategy":"check nil","confidence":0.9}`},
		},
	}

	result, err := e.Run(ctx, s, emb)
	require.NoError(t, err)
	assert.Equal(t, 3, result.EpisodicsScanned)
	// Rule extraction still runs for the 3-cluster, but F3 backtest rejects
	// it: mock LLM output is not a numeric score and the stub embedder yields
	// no signal → nothing persists unverified.
	assert.Equal(t, 0, result.RulesPersisted)
	// PitfallExtractor was set, so runPitfallExtraction was called.
	// With MinClusterSize=9999, no pitfalls are extracted.
	assert.Equal(t, 0, result.PitfallsExtracted)
}

func TestConsolidationEngine_Run_PitfallExtractor_NoDebugEpisodics(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	emb := embedder.NewStubEmbedder()
	now := time.Now()

	// Write feature episodics (not debug) — pitfall extractor only processes debug.
	for i := 0; i < 4; i++ {
		mem := &models.EpisodicMemory{
			ID:          uuid.New(),
			ProjectID:   "feat-cons",
			TaskType:    models.TaskTypeFeature,
			Summary:     "feature work " + string(rune('a'+i)),
			SourceType:  models.SourceTypeUSER,
			TrustLevel:  5,
			Weight:      1.0,
			CreatedAt:   now,
			EntityGroup: "func:FeatFunc",
		}
		require.NoError(t, s.WriteEpisodic(ctx, mem))
	}

	e := &ConsolidationEngine{
		HoursToScan:       24,
		MinClusterSize:    3,
		AccuracyThreshold: 0.7,
		LLMClient:         &llm.MockClient{Response: "feature pattern"},
		PitfallExtractor: &PitfallExtractor{
			MinClusterSize: 2,
			Embedder:       emb,
			LLMClient:      &llm.MockClient{Response: `{"signature":"test","root_cause_category":"unknown","fix_strategy":"none","confidence":0.5}`},
		},
	}

	result, err := e.Run(ctx, s, emb)
	require.NoError(t, err)
	// PitfallExtractor filters to debug only → none found.
	assert.Equal(t, 0, result.PitfallsExtracted)
}

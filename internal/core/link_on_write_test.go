package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

func newMemoryStoreForTest() *memory.Store {
	return memory.NewStore()
}

func TestTokenSimilarity_Identical(t *testing.T) {
	s := tokenSimilarity("redis connection pool exhausted", "redis connection pool exhausted")
	assert.InDelta(t, 1.0, s, 0.01)
}

func TestTokenSimilarity_PartialOverlap(t *testing.T) {
	s := tokenSimilarity("redis connection pool exhausted at maxconns",
		"redis pool timeout when connecting")
	assert.True(t, s > 0, "should have some overlap via 'redis' and 'pool'")
	assert.True(t, s < 0.8, "should not be too similar")
}

func TestTokenSimilarity_NoOverlap(t *testing.T) {
	s := tokenSimilarity("redis connection pool", "postgres migration failure")
	assert.Equal(t, 0.0, s)
}

func TestTokenSimilarity_ShortTokensIgnored(t *testing.T) {
	// "the" and "at" are < 4 chars, should be ignored
	s := tokenSimilarity("the cat sat", "the cat sat on the mat")
	assert.Equal(t, 0.0, s, "short tokens < 4 chars should be ignored")
}

func TestTokenSimilarity_EmptyInput(t *testing.T) {
	assert.Equal(t, 0.0, tokenSimilarity("", "something"))
	assert.Equal(t, 0.0, tokenSimilarity("something", ""))
}

func TestTokenSimilarity_CaseInsensitive(t *testing.T) {
	s := tokenSimilarity("Redis Connection Pool", "redis connection pool")
	assert.InDelta(t, 1.0, s, 0.01)
}

func TestLinkOnWrite_RelatesToEdges(t *testing.T) {
	s := newMemoryStoreForTest()
	ctx := context.Background()

	// Write two episodic memories with overlapping summaries
	from := uuid.New()
	require.NoError(t, s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID: from, ProjectID: "low-proj", Summary: "redis connection pool exhausted",
		SourceType: models.SourceTypeUSER, TrustLevel: 5, Weight: 1.0, CreatedAt: time.Now(),
	}))
	to := uuid.New()
	require.NoError(t, s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID: to, ProjectID: "low-proj", Summary: "redis pool timeout when connecting",
		SourceType: models.SourceTypeUSER, TrustLevel: 5, Weight: 1.0, CreatedAt: time.Now(),
	}))

	// LinkOnWrite should create RELATES_TO edges (nil embedder → token path)
	LinkOnWrite(ctx, nil, s, from, "redis connection pool exhausted", "low-proj", "", models.TaskTypeFeature)

	edges, err := s.GetEdges(ctx, from, "", "out")
	require.NoError(t, err)
	// Should have at least one RELATES_TO edge
	hasRelatesTo := false
	for _, e := range edges {
		if e.EdgeType == "RELATES_TO" {
			hasRelatesTo = true
			break
		}
	}
	assert.True(t, hasRelatesTo, "should create RELATES_TO edge for similar summaries")
}

func TestLinkOnWrite_DebugWithPitfall(t *testing.T) {
	s := newMemoryStoreForTest()
	ctx := context.Background()

	// Write an episodic debug memory
	memID := uuid.New()
	require.NoError(t, s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID: memID, ProjectID: "low-debug", Summary: "nil pointer in handler",
		TaskType: models.TaskTypeDebug, SourceType: models.SourceTypeUSER, TrustLevel: 5, Weight: 1.0,
		CreatedAt: time.Now(),
	}))

	// Write a pitfall for the same entity
	pfID := uuid.New()
	require.NoError(t, s.WritePitfall(ctx, &models.PitfallMemory{
		ID: pfID, EntityID: "func:Handler", EntityType: models.EntityTypeFunction,
		ProjectID: "low-debug", Signature: "nil pointer", SourceType: models.SourceTypeLLM,
		TrustLevel: 3, Weight: 1.0, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	LinkOnWrite(ctx, nil, s, memID, "nil pointer in handler", "low-debug", "func:Handler", models.TaskTypeDebug)

	// Should create TRIGGERED_PITFALL edge
	edges, err := s.GetEdges(ctx, memID, "TRIGGERED_PITFALL", "out")
	require.NoError(t, err)
	assert.NotEmpty(t, edges, "should create TRIGGERED_PITFALL edge for debug with pitfall")
}

// F1 acceptance: two similar Chinese summaries must produce a RELATES_TO edge
// via the embedding path — the token path would see empty token sets.
func TestLinkOnWrite_RelatesToEdges_Chinese(t *testing.T) {
	s := newMemoryStoreForTest()
	ctx := context.Background()

	from := uuid.New()
	require.NoError(t, s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID: from, ProjectID: "low-proj", Summary: "并发问题通常出在锁粒度，要仔细检查锁的范围和持有时间",
		SourceType: models.SourceTypeUSER, TrustLevel: 5, Weight: 1.0, CreatedAt: time.Now(),
	}))
	to := uuid.New()
	require.NoError(t, s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID: to, ProjectID: "low-proj", Summary: "并发问题出在锁粒度，需要仔细检查锁的范围和持有时间",
		SourceType: models.SourceTypeUSER, TrustLevel: 5, Weight: 1.0, CreatedAt: time.Now(),
	}))

	LinkOnWrite(ctx, runeEmbedder{}, s, from,
		"并发问题通常出在锁粒度，要仔细检查锁的范围和持有时间",
		"low-proj", "", models.TaskTypeFeature)

	edges, err := s.GetEdges(ctx, from, "RELATES_TO", "out")
	require.NoError(t, err)
	assert.NotEmpty(t, edges, "should create RELATES_TO edge for similar Chinese summaries")
}

// F1 negative: unrelated Chinese summaries must not link.
func TestLinkOnWrite_NoEdge_ChineseDissimilar(t *testing.T) {
	s := newMemoryStoreForTest()
	ctx := context.Background()

	from := uuid.New()
	require.NoError(t, s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID: from, ProjectID: "low-proj", Summary: "数据库连接池耗尽导致请求超时",
		SourceType: models.SourceTypeUSER, TrustLevel: 5, Weight: 1.0, CreatedAt: time.Now(),
	}))
	to := uuid.New()
	require.NoError(t, s.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID: to, ProjectID: "low-proj", Summary: "前端组件渲染性能优化",
		SourceType: models.SourceTypeUSER, TrustLevel: 5, Weight: 1.0, CreatedAt: time.Now(),
	}))

	LinkOnWrite(ctx, runeEmbedder{}, s, from,
		"数据库连接池耗尽导致请求超时",
		"low-proj", "", models.TaskTypeFeature)

	edges, err := s.GetEdges(ctx, from, "RELATES_TO", "out")
	require.NoError(t, err)
	assert.Empty(t, edges, "should not create RELATES_TO edge for unrelated Chinese summaries")
}

package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSignificanceGate_UserConfirmed(t *testing.T) {
	g := DefaultSignificanceGate()
	event := WriteEvent{
		Summary:       "Redis connection pool exhausted",
		UserConfirmed: true,
	}
	result := g.Evaluate(context.Background(),event, nil)
	assert.True(t, result.ShouldPersist)
	assert.Equal(t, "user_confirmed", result.Reason)
}

func TestSignificanceGate_CrossCyclePersistence(t *testing.T) {
	g := DefaultSignificanceGate()
	event := WriteEvent{
		Summary: "Redis connection pool exhausted at MaxOpenConns=50",
	}
	cycles := []WorkingMemoryCycle{
		{Summary: "Redis connection timeout in production"},
		{Summary: "Redis pool size configuration issue"},
	}
	result := g.Evaluate(context.Background(),event, cycles)
	assert.True(t, result.ShouldPersist)
	assert.Equal(t, "cross_cycle_persistence", result.Reason)
}

func TestSignificanceGate_PredictionError(t *testing.T) {
	g := DefaultSignificanceGate()

	// Correction signal
	event := WriteEvent{
		Summary:      "Fixed: actual bottleneck was DB, not Redis",
		IsCorrection: true,
	}
	result := g.Evaluate(context.Background(),event, nil)
	assert.True(t, result.ShouldPersist)
	assert.Equal(t, "prediction_error", result.Reason)

	// Behavior change signal
	event2 := WriteEvent{
		Summary:              "Reduced pool size based on load test findings",
		CausedBehaviorChange: true,
	}
	result2 := g.Evaluate(context.Background(),event2, nil)
	assert.True(t, result2.ShouldPersist)
	assert.Equal(t, "prediction_error", result2.Reason)
}

func TestSignificanceGate_SubsequentReference(t *testing.T) {
	g := DefaultSignificanceGate()
	event := WriteEvent{
		Summary:                  "Redis MaxOpenConns=100",
		SubsequentReferenceCount: 3,
	}
	result := g.Evaluate(context.Background(),event, nil)
	assert.True(t, result.ShouldPersist)
	assert.Equal(t, "subsequent_reference", result.Reason)
}

func TestSignificanceGate_BelowThreshold(t *testing.T) {
	g := DefaultSignificanceGate()
	event := WriteEvent{
		Summary: "A single, unconfirmed, uncorrected, unreferenced event",
	}
	result := g.Evaluate(context.Background(),event, nil)
	assert.False(t, result.ShouldPersist)
	assert.Equal(t, "below_threshold", result.Reason)
}

func TestSignificanceGate_ShortCircuitsOnFirstMatch(t *testing.T) {
	g := DefaultSignificanceGate()
	// User_confirmed should short-circuit before checking other conditions
	event := WriteEvent{
		Summary:       "Anything",
		UserConfirmed: true,
	}
	result := g.Evaluate(context.Background(),event, nil)
	assert.True(t, result.ShouldPersist)
	assert.Equal(t, "user_confirmed", result.Reason)
}

func TestSignificanceGate_InsufficientCrossCycle(t *testing.T) {
	g := DefaultSignificanceGate()
	event := WriteEvent{
		Summary: "Redis error",
	}
	// Only 1 cycle with similar content — below the MinCrossCycleCount (2)
	cycles := []WorkingMemoryCycle{
		{Summary: "Redis connection pool exhausted"},
	}
	result := g.Evaluate(context.Background(),event, cycles)
	assert.False(t, result.ShouldPersist)
}

func TestSignificanceGate_CustomMinCounts(t *testing.T) {
	g := &SignificanceGate{
		MinCrossCycleCount: 3,
		MinSubsequentRefs:  4,
	}

	// With MinCrossCycleCount=3, 2 matches is not enough
	event := WriteEvent{
		Summary: "Redis pool exhaustion",
	}
	cycles := []WorkingMemoryCycle{
		{Summary: "Redis timeout in production"},
		{Summary: "Redis configuration issue"},
	}
	result := g.Evaluate(context.Background(),event, cycles)
	assert.False(t, result.ShouldPersist)

	// With MinSubsequentRefs=4, 3 refs is not enough
	event2 := WriteEvent{
		Summary:                  "Redis MaxOpenConns=100",
		SubsequentReferenceCount: 3,
	}
	result2 := g.Evaluate(context.Background(),event2, nil)
	assert.False(t, result2.ShouldPersist)
}

func TestTopicOverlap_SharedKeywords(t *testing.T) {
	assert.True(t, TokenOverlap(
		"Redis connection pool exhausted at MaxOpenConns=50",
		"Redis pool size configuration issue in production",
	))
}

func TestTopicOverlap_NoSharedKeywords(t *testing.T) {
	assert.False(t, TokenOverlap(
		"Redis connection pool exhausted",
		"Database migration rollback failure",
	))
}

func TestTopicOverlap_ShortWordsIgnored(t *testing.T) {
	// "the" "and" "is" are <= 3 chars and should be ignored
	assert.False(t, TokenOverlap(
		"the cat is big",
		"the dog is big",
	))
}

func TestTokenize(t *testing.T) {
	tokens := Tokenize("Redis connection pool exhausted")
	expected := []string{"redis", "connection", "pool", "exhausted"}
	assert.Equal(t, expected, tokens)
}

func TestTokenize_SkipsShortWords(t *testing.T) {
	tokens := Tokenize("the cat is big")
	assert.Empty(t, tokens) // all words <= 3 chars
}

// F1 regression: the keyword tokenizer cannot see CJK text — a Chinese summary
// tokenizes to an empty set, so TokenOverlap can never match it. The embedding
// path must carry cross-cycle persistence for CJK content.
func TestSignificanceGate_CrossCyclePersistence_Chinese(t *testing.T) {
	g := DefaultSignificanceGate()
	g.Embedder = runeEmbedder{}

	event := WriteEvent{
		Summary: "并发问题通常出在锁粒度，要仔细检查锁的范围和持有时间",
	}
	cycles := []WorkingMemoryCycle{
		{Summary: "并发问题经常出在锁粒度上，需要检查锁的范围和持有时间"},
		{Summary: "并发问题出在锁粒度，需要仔细检查锁的范围和持有时间"},
	}

	result := g.Evaluate(context.Background(), event, cycles)
	assert.True(t, result.ShouldPersist)
	assert.Equal(t, "cross_cycle_persistence", result.Reason)
}

// F1 regression: embedding similarity must not misfire on unrelated Chinese
// summaries (the token path already returned no match, but embedding brings a
// real signal — verify it stays below threshold for different topics).
func TestSignificanceGate_CrossCycle_ChineseDissimilar(t *testing.T) {
	g := DefaultSignificanceGate()
	g.Embedder = runeEmbedder{}

	event := WriteEvent{
		Summary: "数据库连接池耗尽导致请求超时",
	}
	cycles := []WorkingMemoryCycle{
		{Summary: "前端组件渲染性能优化"},
		{Summary: "并发锁粒度问题"},
	}

	result := g.Evaluate(context.Background(), event, cycles)
	assert.False(t, result.ShouldPersist)
}

// F1: demonstrates the defect being fixed — keyword overlap sees zero matches
// for CJK, while the embedding path finds the cross-cycle signal.
func TestSignificanceGate_CrossCycle_TokenFallbackBlindToCJK(t *testing.T) {
	g := DefaultSignificanceGate()
	event := WriteEvent{Summary: "并发问题通常出在锁粒度"}
	cycles := []WorkingMemoryCycle{{Summary: "并发问题经常出在锁粒度上"}}

	// Token path alone: no overlap (empty token sets).
	assert.Zero(t, g.countRecentCycles(context.Background(), event, cycles))

	// Embedding path: the same content is recognized as a match.
	g.Embedder = runeEmbedder{}
	assert.Equal(t, 1, g.countRecentCycles(context.Background(), event, cycles))
}

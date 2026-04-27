package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSignificanceGate_UserConfirmed(t *testing.T) {
	g := DefaultSignificanceGate()
	event := WriteEvent{
		Summary:       "Redis connection pool exhausted",
		UserConfirmed: true,
	}
	result := g.Evaluate(event, nil)
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
	result := g.Evaluate(event, cycles)
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
	result := g.Evaluate(event, nil)
	assert.True(t, result.ShouldPersist)
	assert.Equal(t, "prediction_error", result.Reason)

	// Behavior change signal
	event2 := WriteEvent{
		Summary:              "Reduced pool size based on load test findings",
		CausedBehaviorChange: true,
	}
	result2 := g.Evaluate(event2, nil)
	assert.True(t, result2.ShouldPersist)
	assert.Equal(t, "prediction_error", result2.Reason)
}

func TestSignificanceGate_SubsequentReference(t *testing.T) {
	g := DefaultSignificanceGate()
	event := WriteEvent{
		Summary:                  "Redis MaxOpenConns=100",
		SubsequentReferenceCount: 3,
	}
	result := g.Evaluate(event, nil)
	assert.True(t, result.ShouldPersist)
	assert.Equal(t, "subsequent_reference", result.Reason)
}

func TestSignificanceGate_BelowThreshold(t *testing.T) {
	g := DefaultSignificanceGate()
	event := WriteEvent{
		Summary: "A single, unconfirmed, uncorrected, unreferenced event",
	}
	result := g.Evaluate(event, nil)
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
	result := g.Evaluate(event, nil)
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
	result := g.Evaluate(event, cycles)
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
	result := g.Evaluate(event, cycles)
	assert.False(t, result.ShouldPersist)

	// With MinSubsequentRefs=4, 3 refs is not enough
	event2 := WriteEvent{
		Summary:                  "Redis MaxOpenConns=100",
		SubsequentReferenceCount: 3,
	}
	result2 := g.Evaluate(event2, nil)
	assert.False(t, result2.ShouldPersist)
}

func TestTopicOverlap_SharedKeywords(t *testing.T) {
	assert.True(t, topicOverlap(
		"Redis connection pool exhausted at MaxOpenConns=50",
		"Redis pool size configuration issue in production",
	))
}

func TestTopicOverlap_NoSharedKeywords(t *testing.T) {
	assert.False(t, topicOverlap(
		"Redis connection pool exhausted",
		"Database migration rollback failure",
	))
}

func TestTopicOverlap_ShortWordsIgnored(t *testing.T) {
	// "the" "and" "is" are <= 3 chars and should be ignored
	assert.False(t, topicOverlap(
		"the cat is big",
		"the dog is big",
	))
}

func TestTokenize(t *testing.T) {
	tokens := tokenize("Redis connection pool exhausted")
	expected := []string{"redis", "connection", "pool", "exhausted"}
	assert.Equal(t, expected, tokens)
}

func TestTokenize_SkipsShortWords(t *testing.T) {
	tokens := tokenize("the cat is big")
	assert.Empty(t, tokens) // all words <= 3 chars
}

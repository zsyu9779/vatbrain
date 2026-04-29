package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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

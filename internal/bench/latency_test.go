package bench

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPercentile_NearestRank(t *testing.T) {
	sorted := []time.Duration{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	// Nearest-rank: p50 of 10 samples is the 5th-smallest (rank ceil(5)=5).
	assert.Equal(t, time.Duration(5), percentile(sorted, 50))
	assert.Equal(t, time.Duration(10), percentile(sorted, 100))
	assert.Equal(t, time.Duration(1), percentile(sorted, 1))
	assert.Equal(t, time.Duration(9), percentile(sorted, 90))

	// p95 of 20 samples is the 19th-smallest.
	twenty := make([]time.Duration, 20)
	for i := range twenty {
		twenty[i] = time.Duration(i + 1)
	}
	assert.Equal(t, time.Duration(19), percentile(twenty, 95))
	assert.Equal(t, time.Duration(20), percentile(twenty, 99.99))
}

func TestPercentile_Edges(t *testing.T) {
	// Empty input yields zero.
	assert.Equal(t, time.Duration(0), percentile(nil, 95))
	assert.Equal(t, time.Duration(0), percentile([]time.Duration{}, 50))

	// Single element is every percentile.
	single := []time.Duration{42}
	assert.Equal(t, time.Duration(42), percentile(single, 0))
	assert.Equal(t, time.Duration(42), percentile(single, 50))
	assert.Equal(t, time.Duration(42), percentile(single, 100))

	// Two elements: p50 is the 1st, p100 the 2nd.
	pair := []time.Duration{7, 9}
	assert.Equal(t, time.Duration(7), percentile(pair, 50))
	assert.Equal(t, time.Duration(9), percentile(pair, 100))
}

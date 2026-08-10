package bench

import (
	"math"
	"time"
)

// percentile returns the p-th percentile (0 <= p <= 100) of a sorted slice of
// durations using the nearest-rank method: the smallest value whose rank is at
// least ceil(p/100 × n). An empty slice returns 0. The micro-benchmarks use it
// to report p50/p95/p99 write, search, and consolidation latencies so the
// ROADMAP latency milestones (docs/ROADMAP.md) are verifiable.
func percentile(sorted []time.Duration, p float64) time.Duration {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	rank := int(math.Ceil(p/100*float64(n))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= n {
		rank = n - 1
	}
	return sorted[rank]
}

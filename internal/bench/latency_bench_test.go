// Latency micro-benchmarks (ticket 01: bench infra). They measure the latency
// percentiles the ROADMAP milestones are stated in (docs/ROADMAP.md: write
// < 200ms, retrieval hit < 100ms / miss < 500ms, consolidation) so the
// milestones are verifiable on every machine.
//
// Conditions (recorded in docs/v0.4/01-bench-infra.md):
//   - keyword embedder (no external API): the numbers cover the VatBrain
//     kernel (gate → search → persist) without paid embedding latency, which
//     is an external cost, not a memory-kernel cost;
//   - sqlite backend (WAL) unless noted, the production store;
//   - run with: go test ./internal/bench/ -bench . -benchtime 1s -count 1
//
// Percentiles use the nearest-rank method (percentile in latency.go).
package bench

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/config"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/provider"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
	"github.com/vatbrain/vatbrain/internal/store/sqlite"
)

// benchDeps builds the benchmark dependency set (keyword embedder + store).
func benchDeps(b testing.TB, backend string) core.WriteDeps {
	b.Helper()
	var st store.MemoryStore
	switch backend {
	case "sqlite":
		s, err := sqlite.NewStore(config.SQLiteConfig{Path: filepath.Join(b.TempDir(), "bench.db"), WAL: true})
		require.NoError(b, err)
		b.Cleanup(func() { s.Close() })
		st = s
	default:
		st = memory.NewStore()
	}
	return core.WriteDeps{
		Store:       st,
		Gate:        core.DefaultSignificanceGate(),
		PatternSep:  core.DefaultPatternSeparation(),
		WeightDecay: core.DefaultWeightDecayEngine(),
		Embedder:    embedder.NewKeywordEmbedder(models.DefaultEmbeddingDim),
		WorkingMem:  store.NewWorkingMemoryBuffer(20),
		// Matches the vatbrain-bench entrypoint (cmd/vatbrain-bench/main.go):
		// RELATES_TO edges are not consumed by the benchmark, and LinkOnWrite
		// would add up to ~40 embedding calls per write.
		SkipLinkOnWrite: true,
	}
}

// reportLatencyPercentiles reports p50/p95/p99 of per-op latencies. The input
// slice is sorted in place.
func reportLatencyPercentiles(b *testing.B, lats []time.Duration, prefix string) {
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	b.ReportMetric(float64(percentile(lats, 50)), prefix+"p50-ns")
	b.ReportMetric(float64(percentile(lats, 95)), prefix+"p95-ns")
	b.ReportMetric(float64(percentile(lats, 99)), prefix+"p99-ns")
}

// benchWords is the vocabulary for deterministic distinct benchmark summaries.
var benchWords = []string{
	"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel",
	"india", "juliet", "kilo", "lima", "mike", "november", "oscar", "papa",
	"quebec", "romeo", "sierra", "tango", "uniform", "victor", "whiskey",
	"xray", "yankee", "zulu", "falcon", "heron", "ibis", "jaguar", "koala",
	"lemur", "mantis", "newt", "ocelot", "panda", "quokka", "raccoon",
}

// benchCorpusSize bounds the distinct summaries in the write benchmark. The
// write loop cycles the corpus, so the store stabilizes at 2,000 memories and
// later writes merge with their corpus twin — bounded, reproducible runs that
// approximate mid-ingestion conditions instead of growing unboundedly.
const benchCorpusSize = 2000

// distinctSummary builds a deterministic summary for index i that is
// keyword-dissimilar from every other index in the corpus, so benchmark
// writes create new memories instead of merging into one (the store grows,
// matching real ingestion profiles). The text is generated from a seeded
// PRNG, so runs are reproducible.
func distinctSummary(i int) string {
	r := rand.New(rand.NewSource(int64(i)))
	words := make([]string, 6)
	for j := range words {
		words[j] = benchWords[r.Intn(len(benchWords))]
	}
	return fmt.Sprintf("memory %d: %s %s %s %s %s %s session",
		i, words[0], words[1], words[2], words[3], words[4], words[5])
}

// BenchmarkWriteLatency measures the full write pipeline
// (core.WriteMemory: gate → embedding → search → persist) and the
// ingestion path (core.WriteMemoryWithEmbedding: embedding skipped, the
// concurrent /v1/add phase-2 cost) on both backends.
func BenchmarkWriteLatency(b *testing.B) {
	for _, backend := range []string{"memory", "sqlite"} {
		for _, mode := range []string{"full", "precomputed"} {
			b.Run(backend+"/"+mode, func(b *testing.B) {
				deps := benchDeps(b, backend)
				ctx := context.Background()
				lats := make([]time.Duration, b.N)

				// The "precomputed" mode measures the ingestion phase-2 cost:
				// vectors are produced up front (outside the timer) exactly
				// like the concurrent /v1/add phase 1 does, then the write
				// pipeline reuses them. Embedding the corpus once keeps the
				// setup cost out of the measurement.
				preEmbedded := make([][]float32, benchCorpusSize)
				for i := range preEmbedded {
					preEmbedded[i], _ = deps.Embedder.Embed(ctx, distinctSummary(i))
				}

				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					summary := distinctSummary(i % benchCorpusSize)
					event := core.WriteEvent{
						Summary:       summary,
						UserConfirmed: true,
					}
					start := time.Now()
					var err error
					if mode == "precomputed" {
						_, err = core.WriteMemoryWithEmbedding(ctx, deps, event, preEmbedded[i%benchCorpusSize],
							"bench-user", "en", "", models.TaskTypeFeature)
					} else {
						_, err = core.WriteMemory(ctx, deps, event,
							"bench-user", "en", "", models.TaskTypeFeature)
					}
					require.NoError(b, err)
					lats[i] = time.Since(start)
				}
				reportLatencyPercentiles(b, lats, "write-")
			})
		}
	}
}

// seedMemories writes n distinct memories for the benchmark user and returns
// their summaries so search queries can be built from real content.
func seedMemories(b testing.TB, deps core.WriteDeps, n int) []string {
	b.Helper()
	ctx := context.Background()
	summaries := make([]string, n)
	for i := 0; i < n; i++ {
		summary := distinctSummary(i)
		_, err := core.WriteMemory(ctx, deps, core.WriteEvent{
			Summary:       summary,
			UserConfirmed: true,
		}, "bench-user", "en", "", models.TaskTypeFeature)
		require.NoError(b, err)
		summaries[i] = summary
	}
	return summaries
}

// BenchmarkSearchLatency measures provider.RetrieveEpisodic (the /v1/search
// path) against a store seeded with 500 memories. Hit queries are built from
// stored summaries; miss queries share no vocabulary with any stored memory.
func BenchmarkSearchLatency(b *testing.B) {
	deps := benchDeps(b, "sqlite")
	seeded := seedMemories(b, deps, 500)
	ctx := context.Background()

	run := func(b *testing.B, queries []string) {
		lats := make([]time.Duration, b.N)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			start := time.Now()
			_, err := provider.RetrieveEpisodic(ctx, deps, "bench-user", queries[i%len(queries)], 10)
			require.NoError(b, err)
			lats[i] = time.Since(start)
		}
		reportLatencyPercentiles(b, lats, "search-")
	}

	b.Run("hit", func(b *testing.B) {
		queries := make([]string, len(seeded))
		for i, s := range seeded {
			queries[i] = s
		}
		run(b, queries)
	})
	b.Run("miss", func(b *testing.B) {
		queries := []string{
			"quantum physics lecture notes",
			"baking sourdough with rye flour",
			"stock market trading strategies",
		}
		run(b, queries)
	})
}

// BenchmarkConsolidation measures one consolidation pass
// (ConsolidationEngine.Run: scan → cluster → extract → backtest) over 300
// seeded memories, on the in-memory store (a fresh store per run so runs are
// independent). Seeding is excluded from the timer. Note: with the no-LLM
// concatenation rule, the embedding-consistency backtest stays below the 0.7
// AccuracyThreshold for large clusters, so persistence is not exercised —
// the number covers scan → cluster → extract → backtest.
func BenchmarkConsolidation(b *testing.B) {
	deps := benchDeps(b, "memory")
	engine := core.DefaultConsolidationEngine()
	ctx := context.Background()

	b.Run("300-memories", func(b *testing.B) {
		lats := make([]time.Duration, b.N)
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			st := memory.NewStore()
			deps.Store = st
			seedMemories(b, deps, 300)
			b.StartTimer()

			start := time.Now()
			_, err := engine.Run(ctx, st, deps.Embedder)
			require.NoError(b, err)
			lats[i] = time.Since(start)
		}
		reportLatencyPercentiles(b, lats, "consolidate-")
	})
}

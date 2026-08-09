package watcher_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/provider"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/store/memory"
	"github.com/vatbrain/vatbrain/internal/watcher"
	"github.com/vatbrain/vatbrain/internal/watcher/adapters"
)

// TestWatcherGA_E2E_ObserveToInject is the v0.2.1 GA e2e demo: a native
// agent memory file → ScanAll refines episodes → sleep consolidation distills
// a Pitfall → prepare_edit_context retrieval injects it. The EVOLUTION_PLAN
// "Observe → Distill → Inject" mainline in one test.
func TestWatcherGA_E2E_ObserveToInject(t *testing.T) {
	// home 目录名模拟真实 ~/.hermes → profileID = "hermes"
	home := filepath.Join(t.TempDir(), ".hermes")
	require.NoError(t, os.MkdirAll(filepath.Join(home, "memories"), 0o755))

	// 3 条 debug 记忆，均指向同一实体 @clawfeed-push-v3.py（refiner 的 entityRe
	// 需要 @ 前缀；触发 HAC 聚类）
	require.NoError(t, os.WriteFile(filepath.Join(home, "memories", "MEMORY.md"), []byte(
		"错误：ClawFeed 推送用了 @clawfeed-push-v3.py 的旧脚本导致身份 bug\n"+
			"§\n"+
			"修复：@clawfeed-push-v3.py 必须用 --as bot，否则报错发不出去\n"+
			"§\n"+
			"@clawfeed-push-v3.py 崩溃排查：字段名应该是 total_score 而非 overall_score"),
		0o644))

	store_ := memory.NewStore()
	refiner := watcher.NewRefiner(nil, nil, "")
	emb := embedder.NewStubEmbedder()
	w := watcher.NewMemoryWatcher(
		[]watcher.MemoryProvider{adapters.NewHermesProvider(home)},
		refiner, emb, store_, time.Minute, 1000)
	ctx := context.Background()

	// 1) Observe → refine → episodes
	res := w.ScanAll(ctx)
	require.Equal(t, 3, res.TotalWritten, "3 条原生记忆应入库")
	items, err := store_.ScanRecent(ctx, time.Time{}, 100)
	require.NoError(t, err)
	require.Len(t, items, 3)

	// 2) Distill → sleep consolidation extracts a Pitfall
	consolidation := core.DefaultConsolidationEngine()
	consolidation.PitfallExtractor = &core.PitfallExtractor{
		MinClusterSize: 2,
		MergeThreshold: 0.85,
		DedupThreshold: 0.9,
		Embedder:       emb,
		LLMClient:      nil,
	}
	run, err := consolidation.Run(ctx, store_, emb)
	require.NoError(t, err)
	require.Greater(t, run.PitfallsPersisted, 0,
		"3 条同实体 debug 记忆应整合出 pitfall")

	// 3) Inject → prepare_edit_context retrieval surfaces the pitfall
	allPitfalls, serr := store_.SearchPitfall(ctx, store.PitfallSearchRequest{Limit: 10})
	require.NoError(t, serr)
	require.NotEmpty(t, allPitfalls, "整合应产生 pitfall 记录")
	t.Logf("extracted pitfall: entity=%s status=%s weight=%.2f occ=%d injectable=%v sig=%.40s",
		allPitfalls[0].EntityID, allPitfalls[0].Status, allPitfalls[0].Weight,
		allPitfalls[0].OccurrenceCount, allPitfalls[0].Injectable(), allPitfalls[0].Signature)

	// 检索诊断：SearchPitfall 按 hermes 项目 + Injectable 门
	hermesPitfalls, herr := store_.SearchPitfall(ctx, store.PitfallSearchRequest{
		ProjectID: "hermes", MinWeight: 0.5, Limit: 10,
	})
	require.NoError(t, herr)
	t.Logf("hermes project pitfalls: %d (injectable=%v)",
		len(hermesPitfalls), len(hermesPitfalls) > 0 && hermesPitfalls[0].Injectable())

	pitfalls, err := provider.RetrievePitfalls(ctx, core.WriteDeps{
		Store: store_, Embedder: emb,
	}, "hermes", "ClawFeed 推送脚本 修复", 3)
	require.NoError(t, err)
	require.NotEmpty(t, pitfalls, "检索应注入提炼出的 pitfall")
	assert.Equal(t, "clawfeed-push-v3.py", pitfalls[0].EntityID)

	t.Logf("GA e2e OK: 3 episodes → %d pitfall(s) → injection hit %s",
		run.PitfallsPersisted, pitfalls[0].EntityID)
}

// TestWatcherGA_ScanStats reports per-provider new/skipped after a scan
// (v0.2.1 GA: list_adapters new_count/skipped_count).
func TestWatcherGA_ScanStats(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "memories"), 0o755))
	memPath := filepath.Join(home, "memories", "MEMORY.md")
	require.NoError(t, os.WriteFile(memPath, []byte("记忆条目一\n§\n记忆条目二"), 0o644))

	store_ := memory.NewStore()
	refiner := watcher.NewRefiner(nil, nil, "")
	w := watcher.NewMemoryWatcher(
		[]watcher.MemoryProvider{adapters.NewHermesProvider(home)},
		refiner, nil, store_, time.Minute, 1000)
	ctx := context.Background()

	w.ScanAll(ctx)
	stats := w.ScanStats()
	require.Contains(t, stats, "hermes")
	assert.Equal(t, 2, stats["hermes"].NewCount, "首次扫描 2 条新增")
	assert.Equal(t, 0, stats["hermes"].SkippedCount)

	// 二次扫描：同内容全部去重跳过
	w.ScanAll(ctx)
	stats = w.ScanStats()
	assert.Equal(t, 0, stats["hermes"].NewCount)
	assert.Equal(t, 2, stats["hermes"].SkippedCount)
}

var _ = store.EpisodicScanItem{}

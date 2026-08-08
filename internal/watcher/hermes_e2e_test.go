package watcher_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/store/memory"
	"github.com/vatbrain/vatbrain/internal/watcher"
	"github.com/vatbrain/vatbrain/internal/watcher/adapters"
)

// TestHermesWatcher_RepeatedScan_NoDuplicates is the Phase 1 acceptance test:
// hermes MEMORY.md written → ingested; repeated polling produces no duplicate
// entries. Runs the real provider → refiner (heuristic) → store pipeline.
func TestHermesWatcher_RepeatedScan_NoDuplicates(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "memories"), 0o755))

	memPath := filepath.Join(home, "memories", "MEMORY.md")
	require.NoError(t, os.WriteFile(memPath, []byte(
		"软路由 OpenClash 调试记录：\n- Ruby YAML.load_file→dump 会把 VLESS 节点改写成无效 VMess 节点\n"+
			"§\n"+
			"MiniMax M2.7 API：max_tokens 必须设到 8000 才能同时输出 thinking + text"),
		0o644))

	store := memory.NewStore()
	refiner := watcher.NewRefiner(nil, nil, "")
	w := watcher.NewMemoryWatcher(
		[]watcher.MemoryProvider{adapters.NewHermesProvider(home)},
		refiner, nil, store, time.Minute, 1000)

	ctx := context.Background()

	// 第一次轮询：两条全部入库
	first := w.ScanAll(ctx)
	assert.Equal(t, 2, first.TotalFound)
	assert.Equal(t, 2, first.TotalWritten)
	assert.Equal(t, 0, first.TotalSkipped)
	assert.Equal(t, 0, first.TotalFailed)

	// 第二次轮询：同内容 → 全部被 seenSet 去重跳过
	second := w.ScanAll(ctx)
	assert.Equal(t, 2, second.TotalFound)
	assert.Equal(t, 0, second.TotalWritten)
	assert.Equal(t, 2, second.TotalSkipped)

	// 库内 episodic 恰好 2 条，无重复
	items, err := store.ScanRecent(ctx, time.Time{}, 100)
	require.NoError(t, err)
	require.Len(t, items, 2)
	summaries := []string{items[0].Summary, items[1].Summary}
	assert.Contains(t, summaries[0], "OpenClash")
	assert.Contains(t, summaries[1], "MiniMax")

	// hermes 追加新条目（整体原子重写）→ 第三次轮询只写入新增的一条
	require.NoError(t, os.WriteFile(memPath, []byte(
		"软路由 OpenClash 调试记录：\n- Ruby YAML.load_file→dump 会把 VLESS 节点改写成无效 VMess 节点\n"+
			"§\n"+
			"MiniMax M2.7 API：max_tokens 必须设到 8000 才能同时输出 thinking + text\n"+
			"§\n"+
			"飞书应用身份：cli_aa8ae18fe078dcc4 是个人飞书 CLI，三个 ID 绝对不能混用"),
		0o644))

	third := w.ScanAll(ctx)
	assert.Equal(t, 3, third.TotalFound)
	assert.Equal(t, 1, third.TotalWritten)
	assert.Equal(t, 2, third.TotalSkipped)

	items, err = store.ScanRecent(ctx, time.Time{}, 100)
	require.NoError(t, err)
	require.Len(t, items, 3)
}

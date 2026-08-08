package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/watcher"
)

// writeHermesMemory writes a hermes-format memory file (entries joined by
// "\n§\n", matching tools/memory_tool.py ENTRY_DELIMITER) into root/memories.
func writeHermesMemory(t *testing.T, root, filename, content string) {
	t.Helper()
	dir := filepath.Join(root, "memories")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644))
}

func TestHermesProvider_Scan_ChineseEntries(t *testing.T) {
	// home 目录名模拟真实默认 ~/.hermes → profileID = "hermes"
	home := filepath.Join(t.TempDir(), ".hermes")
	writeHermesMemory(t, home, "MEMORY.md",
		"软路由 OpenClash 调试记录：\n- Ruby YAML.load_file→dump 会把 VLESS 节点改写成无效 VMess 节点\n"+
			"- 修复后清缓存重启：rm -f /etc/openclash/history/*.db && /etc/init.d/openclash restart\n"+
			"§\n"+
			"MiniMax M2.7 API 关键行为：thinking block 消耗巨大，max_tokens 必须设到 8000")

	p := NewHermesProvider(home)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, raws, 2)

	r := raws[0]
	assert.Equal(t, "hermes", r.ProviderName)
	assert.Equal(t, "hermes", r.ProjectID)
	assert.Contains(t, r.Content, "软路由 OpenClash 调试记录")
	assert.Contains(t, r.Content, "Ruby YAML.load_file→dump")
	assert.NotEmpty(t, r.ContentHash)
	assert.Len(t, r.ContentHash, 64)
	assert.Equal(t, "MEMORY.md", r.Metadata["target"])
	assert.Equal(t, "0", r.Metadata["entry_index"])
	// hermes 无 frontmatter → nil，走 Refiner heuristic 路径
	assert.Nil(t, r.FrontMatter)

	// SourceURI: hermes://memories/<target>#<sha256 前 8>
	assert.Regexp(t, `^hermes://memories/MEMORY\.md#[0-9a-f]{8}$`, r.SourceURI)
	// 条目内容是 URI 哈希的输入，编辑后 URI 随内容变化
	assert.Contains(t, raws[1].Content, "MiniMax M2.7 API")
	assert.Regexp(t, `^hermes://memories/MEMORY\.md#[0-9a-f]{8}$`, raws[1].SourceURI)
	assert.NotEqual(t, raws[0].SourceURI, raws[1].SourceURI)
}

func TestHermesProvider_Scan_SplitsOnDelimiter(t *testing.T) {
	root := t.TempDir()
	// 多行条目、空条目、尾部空 §、首尾空白
	writeHermesMemory(t, root, "MEMORY.md",
		"第一条记忆：\n跨行内容在此\n\n§\n\n§\n第二条记忆：中文内容\n§\n")

	p := NewHermesProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, raws, 2)

	assert.Equal(t, "第一条记忆：\n跨行内容在此", raws[0].Content)
	assert.Equal(t, "第二条记忆：中文内容", raws[1].Content)
}

func TestHermesProvider_Scan_SkipsBlockHeader(t *testing.T) {
	root := t.TempDir()
	// hermes 文件不含块头（只进系统提示），但防御手改文件
	writeHermesMemory(t, root, "MEMORY.md",
		"MEMORY (your personal notes)\n第一条手写记忆\n§\n第二条记忆")

	p := NewHermesProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, raws, 2)

	assert.Equal(t, "第一条手写记忆", raws[0].Content)
	assert.Equal(t, "第二条记忆", raws[1].Content)

	// USER PROFILE 头同样跳过
	writeHermesMemory(t, root, "USER.md",
		"USER PROFILE (who the user is)\n用户是中文语境的开发者")
	p2 := NewHermesProvider(root)
	raws2, err := p2.Scan(context.Background())
	require.NoError(t, err)
	userRaw := findRawByTarget(t, raws2, "USER.md")
	require.NotNil(t, userRaw)
	assert.Equal(t, "用户是中文语境的开发者", userRaw.Content)
}

func TestHermesProvider_Scan_UserAndMemory(t *testing.T) {
	root := t.TempDir()
	writeHermesMemory(t, root, "MEMORY.md", "记忆条目 A")
	writeHermesMemory(t, root, "USER.md", "用户档案条目 B")

	p := NewHermesProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, raws, 2)

	memRaw := findRawByTarget(t, raws, "MEMORY.md")
	userRaw := findRawByTarget(t, raws, "USER.md")
	require.NotNil(t, memRaw)
	require.NotNil(t, userRaw)
	assert.Regexp(t, `^hermes://memories/MEMORY\.md#`, memRaw.SourceURI)
	assert.Regexp(t, `^hermes://memories/USER\.md#`, userRaw.SourceURI)
}

func TestHermesProvider_Scan_MissingMemoriesDir(t *testing.T) {
	root := t.TempDir() // 无 memories 目录
	p := NewHermesProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, raws)
	assert.True(t, p.Status().Healthy)
}

func TestHermesProvider_Scan_MissingFiles(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "memories"), 0o755))
	// 只有 MEMORY.md，没有 USER.md
	writeHermesMemory(t, root, "MEMORY.md", "只有记忆条目")

	p := NewHermesProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, raws, 1)
	assert.Equal(t, "MEMORY.md", raws[0].Metadata["target"])
}

func TestHermesProvider_Scan_StableHashAcrossScans(t *testing.T) {
	root := t.TempDir()
	writeHermesMemory(t, root, "MEMORY.md",
		"中文记忆：重复轮询不应产生重复条目，SourceURI 与 ContentHash 必须稳定")

	p := NewHermesProvider(root)
	first, err := p.Scan(context.Background())
	require.NoError(t, err)
	second, err := p.Scan(context.Background())
	require.NoError(t, err)

	require.Len(t, first, 1)
	require.Len(t, second, 1)
	assert.Equal(t, first[0].SourceURI, second[0].SourceURI)
	assert.Equal(t, first[0].ContentHash, second[0].ContentHash)
	// 文件 mtime 不变 → ModifiedAt 稳定
	assert.True(t, first[0].ModifiedAt.Equal(second[0].ModifiedAt))
}

func TestHermesProvider_Scan_EntryEditChangesURI(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "memories", "MEMORY.md")
	writeHermesMemory(t, root, "MEMORY.md", "旧内容：软路由配置")

	p := NewHermesProvider(root)
	before, err := p.Scan(context.Background())
	require.NoError(t, err)

	// hermes 整体原子重写文件（os.replace）
	require.NoError(t, os.WriteFile(path, []byte("新内容：软路由配置已修正"), 0o644))
	after, err := p.Scan(context.Background())
	require.NoError(t, err)

	require.Len(t, before, 1)
	require.Len(t, after, 1)
	// 内容变化 → 哈希与 URI 都变化，seenSet 按 (SourceURI, ContentHash) 判定为新条目
	assert.NotEqual(t, before[0].SourceURI, after[0].SourceURI)
	assert.NotEqual(t, before[0].ContentHash, after[0].ContentHash)
}

func TestHermesProvider_Scan_EmptyFile(t *testing.T) {
	root := t.TempDir()
	writeHermesMemory(t, root, "MEMORY.md", "")

	p := NewHermesProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, raws)
}

func TestHermesProvider_DefaultHome_UsesHermesHomeEnv(t *testing.T) {
	root := t.TempDir()
	writeHermesMemory(t, root, "MEMORY.md", "中文记忆条目")
	t.Setenv("HERMES_HOME", root)

	p := NewHermesProvider("")
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, raws, 1)
	// ProjectID 取 profile 名 = home 目录名去点前缀
	assert.Equal(t, filepath.Base(root), p.profileID())
	assert.Contains(t, p.Status().WatchPath, filepath.Join(root, "memories"))
}

func TestHermesProvider_Status(t *testing.T) {
	p := NewHermesProvider("/tmp/hermes-home")
	status := p.Status()

	assert.Equal(t, "hermes", status.Name)
	assert.Contains(t, status.Description, "hermes")
	assert.True(t, status.Healthy)
	assert.True(t, status.Watching)
	assert.Equal(t, filepath.Join("/tmp/hermes-home", "memories"), status.WatchPath)
	assert.Equal(t, "section_delimited", status.Config["format"])
}

func TestHermesProvider_ModifiedAt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "memories", "MEMORY.md")
	writeHermesMemory(t, root, "MEMORY.md", "中文记忆：时间戳验证")
	knownTime := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(path, knownTime, knownTime))

	p := NewHermesProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, raws, 1)
	assert.True(t, raws[0].ModifiedAt.Equal(knownTime))
}

// findRawByTarget returns the raw memory whose metadata target matches.
func findRawByTarget(t *testing.T, raws []watcher.RawMemory, target string) *watcher.RawMemory {
	t.Helper()
	for i := range raws {
		if raws[i].Metadata["target"] == target {
			return &raws[i]
		}
	}
	return nil
}

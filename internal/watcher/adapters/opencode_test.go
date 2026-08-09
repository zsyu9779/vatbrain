package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeProvider_Scan_MarkdownMemories(t *testing.T) {
	root := t.TempDir()
	// OpenCode 数据目录结构：<root>/memory/<project>/memory.md
	projDir := filepath.Join(root, "memory", "web-app")
	require.NoError(t, os.MkdirAll(projDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projDir, "memory.md"),
		[]byte("---\nname: 路由重构经验\n---\n中文记忆：路由参数校验必须在 handler 之前"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(projDir, "notes.md"),
		[]byte("纯文本记忆：避免使用弃用的 API"), 0o644))

	p := NewOpenCodeProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, raws, 2)

	for i := range raws {
		if raws[i].Metadata["name"] == "路由重构经验" {
			assert.Contains(t, raws[i].Content, "中文记忆")
			assert.Equal(t, "web-app", raws[i].ProjectID)
			assert.NotEmpty(t, raws[i].ContentHash)
		} else {
			assert.Equal(t, "纯文本记忆：避免使用弃用的 API", raws[i].Content)
		}
	}
}

func TestOpenCodeProvider_Scan_MissingDir_Unhealthy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "no-such-opencode")
	p := NewOpenCodeProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, raws)
	assert.False(t, p.Status().Healthy)
}

func TestOpenCodeProvider_Scan_SkipsNonMarkdown(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "memory"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "memory", "transcript.jsonl"),
		[]byte("{\"text\":\"not a memory file\"}\n"), 0o644))

	p := NewOpenCodeProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, raws)
}

func TestOpenCodeProvider_DefaultDataDir(t *testing.T) {
	dir := defaultOpenCodeDataDir()
	assert.NotEmpty(t, dir)
	p := NewOpenCodeProvider("")
	assert.Equal(t, "opencode", p.Name())
	// 默认路径应解析到 opencode 数据根
	assert.Contains(t, p.Status().WatchPath, "opencode")
}

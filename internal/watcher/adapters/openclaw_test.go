package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenClawProvider_Scan(t *testing.T) {
	root := t.TempDir()
	memoryDir := filepath.Join(root, "memory")
	require.NoError(t, os.MkdirAll(memoryDir, 0o755))

	// A memory file with frontmatter.
	require.NoError(t, os.WriteFile(
		filepath.Join(memoryDir, "feishu-bot.md"),
		[]byte("---\nname: feishu-bot\nmemory_type: project\n---\n飞书机器人必须用 v2 接口"), 0o644))
	// A plain-body memory file.
	require.NoError(t, os.WriteFile(
		filepath.Join(memoryDir, "plain.md"),
		[]byte("ClawFeed 推送用 v3.py"), 0o644))
	// An empty file (should be skipped).
	require.NoError(t, os.WriteFile(
		filepath.Join(memoryDir, "empty.md"), []byte(""), 0o644))

	p := NewOpenClawProvider(memoryDir)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)

	require.Len(t, raws, 2)
	assert.Equal(t, "openclaw", raws[0].ProviderName)
	assert.Equal(t, "飞书机器人必须用 v2 接口", raws[0].Content)
	assert.NotEmpty(t, raws[0].ContentHash)
	assert.Equal(t, "feishu-bot", raws[0].Metadata["name"])
	assert.Equal(t, "project", raws[0].Metadata["memory_type"])
}

func TestOpenClawProvider_MissingRoot(t *testing.T) {
	p := NewOpenClawProvider(filepath.Join(t.TempDir(), "does-not-exist"))
	assert.False(t, p.Status().Healthy)

	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, raws)
}

func TestOpenClawProvider_DefaultRoot(t *testing.T) {
	dir := defaultOpenClawMemoryDir()
	assert.NotEmpty(t, dir)
	assert.Equal(t, "memory", filepath.Base(dir))
}

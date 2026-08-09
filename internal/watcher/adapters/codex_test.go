package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/watcher"
)

func TestCodexProvider_Scan(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	projectDir := filepath.Join(sessionsDir, "my-project")
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	// A transcript with plain-string content and content-block content.
	transcript := `{"role":"user","content":"修复 ClawFeed 推送脚本","timestamp":"2026-08-09T10:00:00Z"}
{"role":"assistant","content":[{"type":"text","text":"已修复，使用 v3.py"}]}
{"role":"system","content":"忽略"}
`
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, "session.jsonl"),
		[]byte(transcript), 0o644))

	// A malformed line should be skipped, not fatal.
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, "other.jsonl"),
		[]byte("{not json}\n{\"role\":\"user\",\"content\":\"garbage\"}\n"), 0o644))

	p := NewCodexProvider(sessionsDir)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)

	require.Len(t, raws, 2, "both transcripts are parsed")

	var main *watcher.RawMemory
	for i := range raws {
		if raws[i].SourceURI == filepath.Join(projectDir, "session.jsonl") {
			main = &raws[i]
		}
	}
	require.NotNil(t, main, "session.jsonl parsed")
	assert.Contains(t, main.Content, "修复 ClawFeed 推送脚本")
	assert.Contains(t, main.Content, "已修复，使用 v3.py")
	assert.Equal(t, "codex", main.ProviderName)
	assert.NotEmpty(t, main.ContentHash)
	assert.Contains(t, main.Metadata["user_msg_count"], "1")
}

func TestCodexProvider_MissingRoot(t *testing.T) {
	p := NewCodexProvider(filepath.Join(t.TempDir(), "does-not-exist"))
	assert.False(t, p.Status().Healthy, "missing root marks provider unhealthy")

	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, raws)
}

func TestCodexProvider_DefaultRoot(t *testing.T) {
	dir := defaultCodexSessionsDir()
	assert.NotEmpty(t, dir)
	assert.Equal(t, ".codex", filepath.Base(filepath.Dir(dir)))
}

package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomProvider_YAMLFrontmatter(t *testing.T) {
	dir := t.TempDir()

	// Write config file.
	configPath := filepath.Join(dir, "adapter.yaml")
	configContent := `
name: test-adapter
description: A test custom adapter
enabled: true
watch:
  paths:
    - "` + filepath.Join(dir, "memories", "*.md") + `"
  exclude_patterns:
    - "*.tmp"
format:
  type: yaml_frontmatter
  field_mappings:
    content: "body"
    project_id: "project"
    session_id: "session"
  metadata:
    priority: "importance"
`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o644))

	// Write memory files.
	memDir := filepath.Join(dir, "memories")
	require.NoError(t, os.MkdirAll(memDir, 0o755))

	memFile := filepath.Join(memDir, "entry.md")
	memContent := `---
body: This is the memory body content.
project: my-project
session: sess-001
importance: high
---
Additional body text.`
	require.NoError(t, os.WriteFile(memFile, []byte(memContent), 0o644))

	// Write an excluded file.
	tmpFile := filepath.Join(memDir, "junk.tmp")
	require.NoError(t, os.WriteFile(tmpFile, []byte("should be excluded"), 0o644))

	p, err := NewCustomProvider(configPath)
	require.NoError(t, err)
	assert.Equal(t, "test-adapter", p.Name())
	assert.Equal(t, "A test custom adapter", p.Description())

	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, raws, 1)

	r := raws[0]
	assert.Equal(t, "test-adapter", r.ProviderName)
	assert.Equal(t, "my-project", r.ProjectID)
	assert.Equal(t, "sess-001", r.AgentSessionID)
	assert.Equal(t, "high", r.Metadata["priority"])
	assert.Contains(t, r.Content, "Additional body text.")
	assert.NotEmpty(t, r.ContentHash)
}

func TestCustomProvider_RawText(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, "adapter.yaml")
	configContent := `
name: raw-adapter
description: Raw text adapter
enabled: true
watch:
  paths:
    - "` + filepath.Join(dir, "logs", "*.txt") + `"
format:
  type: raw_text
  field_mappings:
    content: "text"
`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o644))

	logsDir := filepath.Join(dir, "logs")
	require.NoError(t, os.MkdirAll(logsDir, 0o755))

	require.NoError(t, os.WriteFile(
		filepath.Join(logsDir, "notes.txt"),
		[]byte("Plain text memory content."),
		0o644,
	))

	p, err := NewCustomProvider(configPath)
	require.NoError(t, err)

	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, raws, 1)

	r := raws[0]
	assert.Equal(t, "raw-adapter", r.ProviderName)
	assert.Equal(t, "Plain text memory content.", r.Content)
}

func TestCustomProvider_MissingContentMapping(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, "adapter.yaml")
	configContent := `
name: bad-adapter
description: Missing required field
enabled: true
watch:
  paths:
    - "` + filepath.Join(dir, "*.md") + `"
format:
  type: raw_text
  field_mappings: {}
`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o644))

	_, err := NewCustomProvider(configPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "content")
}

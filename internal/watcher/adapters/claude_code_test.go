package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeCodeProvider_Scan(t *testing.T) {
	// Create a temporary home directory structure.
	root := t.TempDir()

	// Simulate: ~/.claude/projects/test-project/memory/
	memoryDir := filepath.Join(root, ".claude", "projects", "test-project", "memory")
	require.NoError(t, os.MkdirAll(memoryDir, 0o755))

	// Write MEMORY.md (should be skipped).
	require.NoError(t, os.WriteFile(
		filepath.Join(memoryDir, "MEMORY.md"),
		[]byte("# Index\n- [Memory 1](mem1.md)\n"),
		0o644,
	))

	// Write a valid memory file with YAML frontmatter.
	memContent := `---
name: Test Memory
description: A test memory entry
type: project
originSessionId: sess-abc-123
enabled: true
---

This is the body of the memory. It contains useful information about the project.`

	require.NoError(t, os.WriteFile(
		filepath.Join(memoryDir, "test-memory.md"),
		[]byte(memContent),
		0o644,
	))

	// Write a memory file without frontmatter.
	require.NoError(t, os.WriteFile(
		filepath.Join(memoryDir, "no-fm.md"),
		[]byte("Just plain text content without frontmatter."),
		0o644,
	))

	p := NewClaudeCodeProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)

	// MEMORY.md is skipped, so we expect 2 raw memories.
	assert.Len(t, raws, 2)

	// Find the one with frontmatter.
	var fm, plain *struct {
		Name    string
		Project string
	}
	for _, r := range raws {
		assert.Equal(t, "claude-code", r.ProviderName)
		assert.Equal(t, "test-project", r.ProjectID)
		assert.NotEmpty(t, r.ContentHash)
		assert.Len(t, r.ContentHash, 64)

		if r.FrontMatter["name"] == "Test Memory" {
			assert.Equal(t, "project", r.FrontMatter["type"])
			assert.Equal(t, "sess-abc-123", r.AgentSessionID)
			assert.Equal(t, "Test Memory", r.Metadata["name"])
			assert.Equal(t, "project", r.Metadata["memory_type"])
			assert.Contains(t, r.Content, "body of the memory")
		}
	}

	_ = fm
	_ = plain
}

func TestClaudeCodeProvider_Scan_NoProjects(t *testing.T) {
	root := t.TempDir()
	// Create .claude dir but no projects dir.
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".claude"), 0o755))

	p := NewClaudeCodeProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, raws)
}

func TestClaudeCodeProvider_Scan_EmptyMemoryDir(t *testing.T) {
	root := t.TempDir()
	memoryDir := filepath.Join(root, ".claude", "projects", "empty-project", "memory")
	require.NoError(t, os.MkdirAll(memoryDir, 0o755))

	p := NewClaudeCodeProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, raws)
}

func TestClaudeCodeProvider_Status(t *testing.T) {
	p := NewClaudeCodeProvider("/tmp")
	status := p.Status()

	assert.Equal(t, "claude-code", status.Name)
	assert.Contains(t, status.Description, "Claude Code")
	assert.True(t, status.Healthy)
	assert.True(t, status.Watching)
	assert.Contains(t, status.WatchPath, ".claude", "projects")
}

func TestClaudeCodeProvider_ParseFile_NoFrontmatter(t *testing.T) {
	root := t.TempDir()
	memoryDir := filepath.Join(root, ".claude", "projects", "test", "memory")
	require.NoError(t, os.MkdirAll(memoryDir, 0o755))

	path := filepath.Join(memoryDir, "plain.md")
	require.NoError(t, os.WriteFile(path, []byte("This is plain text.\nNo frontmatter here."), 0o644))

	p := NewClaudeCodeProvider(root)
	raw, err := p.parseFile(path, "test")
	require.NoError(t, err)
	require.NotNil(t, raw)

	assert.Equal(t, "This is plain text.\nNo frontmatter here.", raw.Content)
	assert.Empty(t, raw.FrontMatter)
	assert.Equal(t, "test", raw.ProjectID)
	assert.NotEmpty(t, raw.ContentHash)
}

func TestClaudeCodeProvider_Scan_SkipsNonMarkdown(t *testing.T) {
	root := t.TempDir()
	memoryDir := filepath.Join(root, ".claude", "projects", "test-proj", "memory")
	require.NoError(t, os.MkdirAll(memoryDir, 0o755))

	// Write a .txt file — should be skipped.
	require.NoError(t, os.WriteFile(
		filepath.Join(memoryDir, "notes.txt"),
		[]byte("some text"),
		0o644,
	))

	// Write a subdirectory — should be skipped.
	require.NoError(t, os.MkdirAll(filepath.Join(memoryDir, "subdir"), 0o755))

	p := NewClaudeCodeProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, raws)
}

func TestClaudeCodeProvider_Scan_SeesAllFields(t *testing.T) {
	root := t.TempDir()
	memoryDir := filepath.Join(root, ".claude", "projects", "proj", "memory")
	require.NoError(t, os.MkdirAll(memoryDir, 0o755))

	content := `---
name: Full Test
description: A complete test
type: feedback
originSessionId: sess-xyz-789
enabled: false
---
Body content here.`

	path := filepath.Join(memoryDir, "full.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	// Set a known modification time.
	knownTime := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(path, knownTime, knownTime))

	p := NewClaudeCodeProvider(root)
	raws, err := p.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, raws, 1)

	r := raws[0]
	assert.Equal(t, "Full Test", r.Metadata["name"])
	assert.Equal(t, "feedback", r.Metadata["memory_type"])
	assert.Equal(t, "A complete test", r.Metadata["description"])
	assert.Equal(t, "false", r.Metadata["enabled"])
	assert.Equal(t, "sess-xyz-789", r.AgentSessionID)
	assert.Equal(t, "Body content here.", r.Content)
	assert.Equal(t, "proj", r.ProjectID)
	assert.True(t, r.ModifiedAt.Equal(knownTime))
}

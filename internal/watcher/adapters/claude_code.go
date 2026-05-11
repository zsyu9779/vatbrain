// Package adapters implements MemoryProvider adapters for specific AI agents.
package adapters

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/vatbrain/vatbrain/internal/watcher"
)

// ClaudeCodeProvider scans Claude Code memory files.
// Storage layout: ~/.claude/projects/{project-slug}/memory/*.md
// Each .md file has YAML frontmatter (between --- markers) followed by markdown body.
type ClaudeCodeProvider struct {
	homeDir  string
	mu       sync.Mutex
	status   watcher.ProviderStatus
}

// NewClaudeCodeProvider creates a Claude Code memory adapter. If homeDir is
// empty, os.UserHomeDir() is used.
func NewClaudeCodeProvider(homeDir string) *ClaudeCodeProvider {
	if homeDir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			homeDir = h
		}
	}
	return &ClaudeCodeProvider{
		homeDir: homeDir,
		status: watcher.ProviderStatus{
			Name:        "claude-code",
			Description: "Watches Claude Code memory files (~/.claude/projects/*/memory/)",
			Healthy:     true,
			WatchPath:   filepath.Join(homeDir, ".claude", "projects"),
			Config: map[string]string{
				"home_dir": homeDir,
				"format":   "yaml_frontmatter",
			},
		},
	}
}

// Name returns the provider identifier.
func (p *ClaudeCodeProvider) Name() string { return "claude-code" }

// Description returns a human-readable description.
func (p *ClaudeCodeProvider) Description() string {
	return "Watches Claude Code memory files (~/.claude/projects/*/memory/)"
}

// projectsDir returns the root projects directory.
func (p *ClaudeCodeProvider) projectsDir() string {
	return filepath.Join(p.homeDir, ".claude", "projects")
}

// Scan walks ~/.claude/projects/*/memory/ and parses all .md files except
// MEMORY.md. Each parsed file produces one RawMemory.
func (p *ClaudeCodeProvider) Scan(ctx context.Context) ([]watcher.RawMemory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	root := p.projectsDir()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		p.status.Healthy = true
		p.status.LastScanAt = time.Now()
		return nil, nil
	}

	var raws []watcher.RawMemory

	projectEntries, err := os.ReadDir(root)
	if err != nil {
		p.status.Healthy = false
		p.status.LastError = err.Error()
		return nil, err
	}

	for _, projectEntry := range projectEntries {
		if !projectEntry.IsDir() {
			continue
		}
		projectSlug := projectEntry.Name()
		memoryDir := filepath.Join(root, projectSlug, "memory")

		memoryEntries, err := os.ReadDir(memoryDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			slog.Warn("claude_code: read memory dir", "dir", memoryDir, "err", err)
			continue
		}

		for _, memEntry := range memoryEntries {
			if memEntry.IsDir() || memEntry.Name() == "MEMORY.md" {
				continue
			}
			if !strings.HasSuffix(memEntry.Name(), ".md") {
				continue
			}

			filePath := filepath.Join(memoryDir, memEntry.Name())
			raw, err := p.parseFile(filePath, projectSlug)
			if err != nil {
				slog.Warn("claude_code: parse memory file", "path", filePath, "err", err)
				continue
			}
			if raw != nil {
				raws = append(raws, *raw)
			}
		}
	}

	p.status.Healthy = true
	p.status.LastScanAt = time.Now()
	p.status.TotalSeen += len(raws)

	return raws, nil
}

// parseFile reads a single Claude Code memory .md file and extracts
// YAML frontmatter + markdown body into a RawMemory.
func (p *ClaudeCodeProvider) parseFile(path, projectSlug string) (*watcher.RawMemory, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var frontMatterLines []string
	var bodyLines []string
	inFrontMatter := false

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	lineNum := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineNum++

		if lineNum == 1 && strings.TrimSpace(line) == "---" {
			inFrontMatter = true
			continue
		}
		if inFrontMatter && strings.TrimSpace(line) == "---" {
			inFrontMatter = false
			continue
		}

		if inFrontMatter {
			frontMatterLines = append(frontMatterLines, line)
		} else {
			bodyLines = append(bodyLines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Parse YAML frontmatter into a flat map.
	fm := make(map[string]string)
	if len(frontMatterLines) > 0 {
		var yamlMap map[string]any
		yamlStr := strings.Join(frontMatterLines, "\n")
		if err := yaml.Unmarshal([]byte(yamlStr), &yamlMap); err == nil {
			for k, v := range yamlMap {
				fm[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	content := strings.TrimSpace(strings.Join(bodyLines, "\n"))
	if content == "" && len(fm) == 0 {
		return nil, nil // empty file
	}

	raw := &watcher.RawMemory{
		ProviderName:   "claude-code",
		SourceURI:      path,
		Content:        content,
		FrontMatter:    fm,
		ModifiedAt:     info.ModTime(),
		ProjectID:      projectSlug,
		Metadata:       make(map[string]string),
	}

	// Map known frontmatter fields.
	if name, ok := fm["name"]; ok {
		raw.Metadata["name"] = name
	}
	if memType, ok := fm["type"]; ok {
		raw.Metadata["memory_type"] = memType
	}
	if originSession, ok := fm["originSessionId"]; ok {
		raw.AgentSessionID = originSession
	}
	if desc, ok := fm["description"]; ok {
		raw.Metadata["description"] = desc
	}
	if enabled, ok := fm["enabled"]; ok {
		raw.Metadata["enabled"] = enabled
	}

	raw.HashContent()
	return raw, nil
}

// Status returns the current provider status.
func (p *ClaudeCodeProvider) Status() watcher.ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Watching = true
	return s
}

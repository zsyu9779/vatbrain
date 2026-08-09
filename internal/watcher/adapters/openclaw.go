package adapters

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vatbrain/vatbrain/internal/watcher"
)

// OpenClawProvider watches OpenClaw's persistent memory.
//
// Storage layout (oasysai/OpenClaw): agent memory lives as markdown files under
// the OpenClaw data root (~/.openclaw/memory/), each with optional YAML
// frontmatter (name/description/type) followed by the memory body — the same
// shape Claude Code and OpenCode use. This adapter walks that root and parses
// *.md files with the shared frontmatter helper.
//
// Default root:
//   - unix:   ~/.openclaw/memory/
//   - Windows: %USERPROFILE%\.openclaw\memory\
//
// Override with the watch path (VATBRAIN_OPENCLAW_MEMORY_PATH).
type OpenClawProvider struct {
	watchPath string
	mu        sync.Mutex
	status    watcher.ProviderStatus
}

// NewOpenClawProvider creates an OpenClaw memory adapter. If watchPath is
// empty, the platform default memory root is used.
func NewOpenClawProvider(watchPath string) *OpenClawProvider {
	if watchPath == "" {
		watchPath = defaultOpenClawMemoryDir()
	}

	healthy := true
	if _, err := os.Stat(watchPath); os.IsNotExist(err) {
		healthy = false
	}

	return &OpenClawProvider{
		watchPath: watchPath,
		status: watcher.ProviderStatus{
			Name:        "openclaw",
			Description: "Watches OpenClaw memory markdown files (~/.openclaw/memory)",
			Healthy:     healthy,
			WatchPath:   watchPath,
			Config: map[string]string{
				"watch_path": watchPath,
				"format":     "markdown",
			},
		},
	}
}

// defaultOpenClawMemoryDir resolves the platform default OpenClaw memory root.
func defaultOpenClawMemoryDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".openclaw", "memory")
}

func (p *OpenClawProvider) Name() string        { return "openclaw" }
func (p *OpenClawProvider) Description() string { return "Watches OpenClaw memory markdown files" }

// Scan walks the OpenClaw memory root for *.md files and emits one RawMemory
// per file. Non-markdown files and subdirectories are skipped.
func (p *OpenClawProvider) Scan(ctx context.Context) ([]watcher.RawMemory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.watchPath == "" {
		p.status.Healthy = false
		p.status.LastScanAt = time.Now()
		return nil, nil
	}
	if _, err := os.Stat(p.watchPath); os.IsNotExist(err) {
		p.status.Healthy = false
		p.status.LastScanAt = time.Now()
		return nil, nil
	}

	var raws []watcher.RawMemory
	err := filepath.WalkDir(p.watchPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			slog.Debug("openclaw: walk", "path", path, "err", err)
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		raw, perr := p.parseFile(path, info)
		if perr != nil {
			slog.Warn("openclaw: parse memory file", "path", path, "err", perr)
			return nil
		}
		if raw != nil {
			raws = append(raws, *raw)
		}
		return nil
	})
	if err != nil {
		p.status.Healthy = false
		p.status.LastError = err.Error()
		return nil, err
	}

	p.status.Healthy = true
	p.status.LastScanAt = time.Now()
	p.status.TotalSeen += len(raws)
	return raws, nil
}

// parseFile reads one OpenClaw memory file into a RawMemory, tolerating
// optional YAML frontmatter and falling back to raw markdown body.
func (p *OpenClawProvider) parseFile(path string, info os.FileInfo) (*watcher.RawMemory, error) {
	if info == nil {
		st, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		info = st
	}

	fm, body, err := parseYAMLFrontmatter(path)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(body)
	if content == "" && len(fm) == 0 {
		return nil, nil // empty file
	}

	raw := &watcher.RawMemory{
		ProviderName: "openclaw",
		SourceURI:    path,
		Content:      content,
		FrontMatter:  fm,
		ModifiedAt:   info.ModTime(),
		ProjectID:    projectIDFromPath(p.watchPath, path),
		Metadata:     make(map[string]string),
	}
	if name, ok := fm["name"]; ok {
		raw.Metadata["name"] = name
	}
	if memType, ok := fm["type"]; ok {
		raw.Metadata["memory_type"] = memType
	} else if memType, ok := fm["memory_type"]; ok {
		raw.Metadata["memory_type"] = memType
	}
	if desc, ok := fm["description"]; ok {
		raw.Metadata["description"] = desc
	}
	raw.HashContent()
	return raw, nil
}

func (p *OpenClawProvider) Status() watcher.ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Watching = true
	return s
}

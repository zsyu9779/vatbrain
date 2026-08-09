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

// OpenCodeProvider watches OpenCode's native memory storage.
//
// Storage layout (sst/opencode): the OpenCode data directory holds session
// transcripts as JSONL plus project/global memory as markdown files
// (memory.md). This adapter scans *.md files under the data root and parses
// them with the shared frontmatter/raw-text helpers, mirroring the
// Claude Code adapter's behaviour.
//
// Default roots:
//   - macOS:   ~/Library/Application Support/opencode/
//   - Linux:   ~/.local/share/opencode/   (or ~/.config/opencode/)
//   - Windows: %APPDATA%\opencode\
//
// Override with the watch path (VATBRAIN_OPENCODE_MEMORY_PATH).
type OpenCodeProvider struct {
	watchPath string
	mu        sync.Mutex
	status    watcher.ProviderStatus
}

// NewOpenCodeProvider creates an OpenCode memory adapter. If watchPath is
// empty, the platform default data directory is used.
func NewOpenCodeProvider(watchPath string) *OpenCodeProvider {
	if watchPath == "" {
		watchPath = defaultOpenCodeDataDir()
	}

	healthy := true
	if _, err := os.Stat(watchPath); os.IsNotExist(err) {
		healthy = false
	}

	return &OpenCodeProvider{
		watchPath: watchPath,
		status: watcher.ProviderStatus{
			Name:        "opencode",
			Description: "Watches OpenCode memory files (opencode data dir)",
			Healthy:     healthy,
			WatchPath:   watchPath,
			Config: map[string]string{
				"watch_path": watchPath,
				"format":     "markdown",
			},
		},
	}
}

// defaultOpenCodeDataDir resolves the platform default OpenCode data root.
func defaultOpenCodeDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch {
	case os.PathSeparator == '/': // unix-likes
		// macOS Application Support first, then Linux XDG.
		macOS := filepath.Join(home, "Library", "Application Support", "opencode")
		if _, err := os.Stat(macOS); err == nil {
			return macOS
		}
		return filepath.Join(home, ".local", "share", "opencode")
	default:
		return filepath.Join(home, "AppData", "Roaming", "opencode")
	}
}

func (p *OpenCodeProvider) Name() string        { return "opencode" }
func (p *OpenCodeProvider) Description() string { return "Watches OpenCode memory files" }

// Scan walks the OpenCode data root for *.md memory files and emits one
// RawMemory per file. Non-markdown files and subdirectories are skipped.
func (p *OpenCodeProvider) Scan(ctx context.Context) ([]watcher.RawMemory, error) {
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
			slog.Debug("opencode: walk", "path", path, "err", err)
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
		raw, err := p.parseFile(path, info)
		if err != nil {
			slog.Warn("opencode: parse memory file", "path", path, "err", err)
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

// parseFile reads one OpenCode memory file into a RawMemory, tolerating
// optional YAML frontmatter and falling back to raw markdown body.
func (p *OpenCodeProvider) parseFile(path string, info os.FileInfo) (*watcher.RawMemory, error) {
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
		ProviderName: "opencode",
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
	}
	raw.HashContent()
	return raw, nil
}

// projectIDFromPath derives a project identifier from the memory file path,
// preferring a project-named directory between the root and the file.
func projectIDFromPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "opencode"
	}
	dir := filepath.Dir(rel)
	if dir == "." {
		return "opencode"
	}
	parts := strings.Split(filepath.Clean(dir), string(os.PathSeparator))
	for _, part := range parts {
		if part != "" && part != "memory" && part != "memories" && part != "projects" {
			return part
		}
	}
	return "opencode"
}

func (p *OpenCodeProvider) Status() watcher.ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Watching = true
	return s
}

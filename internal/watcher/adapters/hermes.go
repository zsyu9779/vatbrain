package adapters

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vatbrain/vatbrain/internal/watcher"
)

// HermesProvider scans hermes memory files.
// Storage layout: $HERMES_HOME/memories/MEMORY.md + USER.md
// Format (hermes tools/memory_tool.py): entries are separated by the
// ENTRY_DELIMITER "\n§\n", with no frontmatter. The block headers
// ("MEMORY (your personal notes)" / "USER PROFILE (who the user is)")
// are rendered only in the system-prompt snapshot, never written to the
// file — the parser tolerates them defensively for hand-edited files.
type HermesProvider struct {
	homeDir string
	mu      sync.Mutex
	status  watcher.ProviderStatus
}

// hermesMemoryTargets lists the hermes memory files watched by this adapter.
// target doubles as the URI segment under hermes://memories/<target>#<hash>.
var hermesMemoryTargets = []struct{ filename, target string }{
	{"MEMORY.md", "MEMORY.md"},
	{"USER.md", "USER.md"},
}

// hermesBlockHeaders are the system-prompt block headers rendered by
// MemoryStore._render_block — kept in lockstep with hermes
// tools/memory_tool.py MEMORY_BLOCK_HEADERS.
var hermesBlockHeaders = []string{
	"MEMORY (your personal notes)",
	"USER PROFILE (who the user is)",
}

// NewHermesProvider creates a hermes memory adapter. If homeDir is empty,
// resolution order: $HERMES_HOME env (profile switch respected) →
// ~/.hermes. Non-empty homeDir always wins.
func NewHermesProvider(homeDir string) *HermesProvider {
	if homeDir == "" {
		if env := os.Getenv("HERMES_HOME"); env != "" {
			homeDir = env
		} else if h, err := os.UserHomeDir(); err == nil {
			homeDir = filepath.Join(h, ".hermes")
		}
	}
	memoriesDir := filepath.Join(homeDir, "memories")
	return &HermesProvider{
		homeDir: homeDir,
		status: watcher.ProviderStatus{
			Name:        "hermes",
			Description: "Watches hermes memory files ($HERMES_HOME/memories/)",
			Healthy:     true,
			WatchPath:   memoriesDir,
			Config: map[string]string{
				"home_dir":    homeDir,
				"memories_dir": memoriesDir,
				"format":      "section_delimited",
			},
		},
	}
}

// Name returns the provider identifier.
func (p *HermesProvider) Name() string { return "hermes" }

// Description returns a human-readable description.
func (p *HermesProvider) Description() string {
	return "Watches hermes memory files ($HERMES_HOME/memories/)"
}

// profileID derives the hermes profile identifier from the home directory
// name, e.g. "~/.hermes" → "hermes", "~/.hermes-work" → "hermes-work".
func (p *HermesProvider) profileID() string {
	return strings.TrimPrefix(filepath.Base(p.homeDir), ".")
}

// memoriesDir returns the profile-scoped memories directory.
func (p *HermesProvider) memoriesDir() string {
	return filepath.Join(p.homeDir, "memories")
}

// Scan reads $HERMES_HOME/memories/MEMORY.md and USER.md and emits one
// RawMemory per entry. Missing files are skipped; a missing memories dir is
// treated as healthy-and-empty so the watcher can pick up later.
func (p *HermesProvider) Scan(ctx context.Context) ([]watcher.RawMemory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	root := p.memoriesDir()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		p.status.Healthy = true
		p.status.LastScanAt = time.Now()
		return nil, nil
	}

	var raws []watcher.RawMemory
	for _, t := range hermesMemoryTargets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := filepath.Join(root, t.filename)
		fileRaws, err := p.parseFile(path, t.target)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			slog.Warn("hermes: parse memory file", "path", path, "err", err)
			continue
		}
		raws = append(raws, fileRaws...)
	}

	p.status.Healthy = true
	p.status.LastScanAt = time.Now()
	p.status.TotalSeen += len(raws)

	return raws, nil
}

// parseFile reads a hermes memory file and splits it into one RawMemory per
// entry. Entries are trimmed, empty entries skipped, and a leading block
// header line tolerated (see HermesProvider doc). hermes rewrites the whole
// file atomically on every write, so ModifiedAt is the file mtime.
func (p *HermesProvider) parseFile(path, target string) ([]watcher.RawMemory, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	entries := strings.Split(string(data), "\n§\n")
	raws := make([]watcher.RawMemory, 0, len(entries))
	for i, entry := range entries {
		entry = strings.TrimSpace(entry)
		entry = stripBlockHeader(entry)
		if entry == "" {
			continue
		}

		raw := watcher.RawMemory{
			ProviderName: "hermes",
			// Content-derived entry hash keeps the URI stable across
			// atomic whole-file rewrites; edits re-sync via ContentHash.
			SourceURI:   fmt.Sprintf("hermes://memories/%s#%s", target, entryHash(entry)),
			Content:     entry,
			FrontMatter: nil, // hermes has no frontmatter → Refiner heuristic path
			ModifiedAt:  info.ModTime(),
			ProjectID:   p.profileID(),
			Metadata: map[string]string{
				"target":      target,
				"entry_index": strconv.Itoa(i),
			},
		}
		raw.HashContent()
		raws = append(raws, raw)
	}
	return raws, nil
}

// stripBlockHeader removes a leading hermes block header line. hermes never
// writes these into files — this only guards hand-edited or imported ones.
func stripBlockHeader(entry string) string {
	for _, h := range hermesBlockHeaders {
		if entry == h {
			return ""
		}
		if strings.HasPrefix(entry, h+"\n") {
			return strings.TrimSpace(strings.TrimPrefix(entry, h+"\n"))
		}
	}
	return entry
}

// entryHash returns the first 8 hex chars of the SHA-256 digest of the
// entry content — the stable per-entry identifier embedded in SourceURI.
func entryHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum)[:8]
}

// Status returns the current provider status.
func (p *HermesProvider) Status() watcher.ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Watching = true
	return s
}

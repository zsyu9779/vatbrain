package adapters

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/vatbrain/vatbrain/internal/watcher"
)

// OpenCodeProvider watches OpenCode's native memory storage.
// Expected paths: macOS ~/Library/Application Support/OpenCode/, Linux ~/.config/opencode/
type OpenCodeProvider struct {
	watchPath string
	mu        sync.Mutex
	status    watcher.ProviderStatus
}

// NewOpenCodeProvider creates an OpenCode memory adapter. If watchPath is empty
// the provider will report unhealthy — Scan returns empty.
func NewOpenCodeProvider(watchPath string) *OpenCodeProvider {
	healthy := true
	if watchPath == "" {
		if h, err := os.UserHomeDir(); err == nil {
			watchPath = h + "/Library/Application Support/OpenCode"
		}
	}
	if _, err := os.Stat(watchPath); os.IsNotExist(err) {
		healthy = false
	}

	return &OpenCodeProvider{
		watchPath: watchPath,
		status: watcher.ProviderStatus{
			Name:        "opencode",
			Description: "Watches OpenCode memory files",
			Healthy:     healthy,
			WatchPath:   watchPath,
			Config: map[string]string{
				"watch_path": watchPath,
			},
		},
	}
}

func (p *OpenCodeProvider) Name() string        { return "opencode" }
func (p *OpenCodeProvider) Description() string  { return "Watches OpenCode memory files" }

func (p *OpenCodeProvider) Scan(ctx context.Context) ([]watcher.RawMemory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := os.Stat(p.watchPath); os.IsNotExist(err) {
		p.status.Healthy = false
		p.status.LastScanAt = time.Now()
		return nil, nil
	}

	// TODO: Implement OpenCode-specific memory format parsing.
	// OpenCode memory format and storage layout still need investigation.
	slog.Debug("opencode: scan not yet implemented", "path", p.watchPath)

	p.status.Healthy = true
	p.status.LastScanAt = time.Now()
	return nil, nil
}

func (p *OpenCodeProvider) Status() watcher.ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Watching = true
	return s
}

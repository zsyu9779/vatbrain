package watcher

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// MemoryWatcher orchestrates periodic scanning of agent-native memory stores,
// refinement of raw memories into episodic memories, and writing them into
// the VatBrain store via the standard pipeline.
type MemoryWatcher struct {
	registry     *ProviderRegistry
	refiner      *Refiner
	store        store.MemoryStore
	pollInterval time.Duration
	seenSet      *seenSet

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
}

// NewMemoryWatcher creates a MemoryWatcher. pollInterval controls how often
// all providers are scanned. seenMaxEntries controls the LRU seen set size.
func NewMemoryWatcher(
	providers []MemoryProvider,
	refiner *Refiner,
	s store.MemoryStore,
	pollInterval time.Duration,
	seenMaxEntries int,
) *MemoryWatcher {
	if seenMaxEntries <= 0 {
		seenMaxEntries = 10000
	}
	reg := NewProviderRegistry()
	for _, p := range providers {
		if err := reg.Register(p); err != nil {
			slog.Warn("watcher: provider registration", "name", p.Name(), "err", err)
		}
	}
	return &MemoryWatcher{
		registry:     reg,
		refiner:      refiner,
		store:        s,
		pollInterval: pollInterval,
		seenSet:      newSeenSet(seenMaxEntries),
	}
}

// Registry returns the provider registry for external access (e.g. MCP tools).
func (w *MemoryWatcher) Registry() *ProviderRegistry {
	return w.registry
}

// Start begins the periodic scan loop. Non-blocking — runs in a background
// goroutine. Call Stop to shut down.
func (w *MemoryWatcher) Start(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.stopCh = make(chan struct{})
	w.mu.Unlock()

	slog.Info("watcher: started",
		"interval", w.pollInterval,
		"providers", len(w.registry.List()))

	// Run initial scan.
	w.scanAll(ctx)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.scanAll(ctx)
		case <-w.stopCh:
			slog.Info("watcher: stopped")
			return
		case <-ctx.Done():
			slog.Info("watcher: context cancelled")
			return
		}
	}
}

// Stop gracefully shuts down the watcher daemon.
func (w *MemoryWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.running {
		return
	}
	w.running = false
	close(w.stopCh)
}

// ScanAll triggers an immediate scan of all providers. Useful for manual sync
// via MCP tool.
func (w *MemoryWatcher) ScanAll(ctx context.Context) SyncResult {
	return w.scanAll(ctx)
}

// SyncResult summarizes the outcome of a scan cycle.
type SyncResult struct {
	AdaptersScanned int `json:"adapters_scanned"`
	TotalFound      int `json:"total_found"`
	TotalWritten    int `json:"total_written"`
	TotalSkipped    int `json:"total_skipped"`
	TotalFailed     int `json:"total_failed"`
}

// scanAll iterates over all registered providers, scans for new memories,
// refines them, and writes them to the store.
func (w *MemoryWatcher) scanAll(ctx context.Context) SyncResult {
	var result SyncResult

	for _, provider := range w.registry.List() {
		result.AdaptersScanned++

		raws, err := provider.Scan(ctx)
		if err != nil {
			slog.Warn("watcher: scan provider", "name", provider.Name(), "err", err)
			continue
		}

		result.TotalFound += len(raws)

		for _, raw := range raws {
			// Dedup: skip already-seen memories.
			if w.seenSet.Has(raw.SourceURI, raw.ContentHash) {
				result.TotalSkipped++
				continue
			}
			w.seenSet.Mark(raw.SourceURI, raw.ContentHash)

			ep, err := w.refiner.Refine(ctx, raw)
			if err != nil {
				slog.Warn("watcher: refine memory", "source", raw.SourceURI, "err", err)
				result.TotalFailed++
				continue
			}
			if ep == nil {
				// Refiner decided to skip (low confidence).
				result.TotalSkipped++
				continue
			}

			if err := w.store.WriteEpisodic(ctx, ep); err != nil {
				slog.Warn("watcher: write episodic", "source", raw.SourceURI, "err", err)
				result.TotalFailed++
				continue
			}

			// Run LinkOnWrite to create edges (best-effort).
			core.LinkOnWrite(ctx, w.store, ep.ID, ep.Summary, ep.ProjectID,
				ep.EntityGroup, ep.TaskType)

			result.TotalWritten++
		}
	}

	if result.TotalFound > 0 {
		slog.Info("watcher: scan cycle complete",
			"found", result.TotalFound,
			"written", result.TotalWritten,
			"skipped", result.TotalSkipped,
			"failed", result.TotalFailed)
	}

	return result
}

// DumpSeenSet persists the seen set to a file.
func (w *MemoryWatcher) DumpSeenSet(path string) error {
	return DumpSeenSet(w.seenSet, path)
}

// RestoreSeenSet loads the seen set from a file.
func (w *MemoryWatcher) RestoreSeenSet(path string) error {
	return RestoreSeenSet(w.seenSet, path)
}

// Ensure models and store are referenced.
var _ = models.DefaultWeight
var _ = store.EpisodicSearchRequest{}

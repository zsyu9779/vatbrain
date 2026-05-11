// Package watcher implements the Agent Memory Watcher subsystem — a passive
// memory bypass that monitors agent-native memory stores, detects changes, and
// writes structured episodic memories into VatBrain via the existing
// Store.WriteEpisodic + LinkOnWrite pipeline.
package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// RawMemory is the canonical intermediate format produced by all MemoryProvider
// adapters. It normalises agent-specific memory formats into a common structure
// consumed by the refinement pipeline.
type RawMemory struct {
	ProviderName   string            `json:"provider_name"`
	SourceURI      string            `json:"source_uri"`
	Content        string            `json:"content"`
	FrontMatter    map[string]string `json:"frontmatter"`
	ContentHash    string            `json:"content_hash"`
	ModifiedAt     time.Time         `json:"modified_at"`
	AgentSessionID string            `json:"agent_session_id"`
	ProjectID      string            `json:"project_id"`
	Metadata       map[string]string `json:"metadata"`
}

// HashContent computes a SHA-256 hex digest of content and stores it in
// ContentHash. Called by adapters before emitting RawMemory.
func (r *RawMemory) HashContent() {
	r.ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(r.Content)))
}

// seenSet tracks processed (SourceURI, ContentHash) pairs to prevent
// re-processing the same memory across watch and scan cycles.
type seenSet struct {
	cache *lru.Cache[string, struct{}]
	mu    sync.Mutex
}

// newSeenSet creates a seenSet with the given max capacity.
func newSeenSet(maxEntries int) *seenSet {
	c, _ := lru.New[string, struct{}](maxEntries)
	return &seenSet{cache: c}
}

// key builds the dedup key from source URI and content hash.
func (s *seenSet) key(uri, hash string) string { return uri + "\x00" + hash }

// Has returns true if the pair has been seen.
func (s *seenSet) Has(uri, hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cache.Contains(s.key(uri, hash))
}

// Mark records the pair as seen.
func (s *seenSet) Mark(uri, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache.Add(s.key(uri, hash), struct{}{})
}

// Len returns the number of entries currently tracked.
func (s *seenSet) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cache.Len()
}

// dump returns all keys for persistence.
func (s *seenSet) dump() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cache.Keys()
}

// restore loads keys from a previous dump.
func (s *seenSet) restore(keys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		s.cache.Add(k, struct{}{})
	}
}

// DumpSeenSet writes the seen set to path as JSON.
func DumpSeenSet(ss *seenSet, path string) error {
	keys := ss.dump()
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// RestoreSeenSet reads the seen set from path.
func RestoreSeenSet(ss *seenSet, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	ss.restore(keys)
	return nil
}

// ProviderStatus reports the current health and statistics of a
// MemoryProvider.
type ProviderStatus struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Healthy     bool              `json:"healthy"`
	LastScanAt  time.Time         `json:"last_scan_at"`
	LastError   string            `json:"last_error,omitempty"`
	TotalSeen   int               `json:"total_seen"`
	Watching    bool              `json:"watching"`
	WatchPath   string            `json:"watch_path"`
	Config      map[string]string `json:"config"`
}

// MemoryProvider abstracts an agent's native memory storage. Each adapter
// knows how to scan and watch a specific agent's files.
type MemoryProvider interface {
	Name() string
	Description() string
	Scan(ctx context.Context) ([]RawMemory, error)
	Status() ProviderStatus
}

// ProviderRegistry manages a set of MemoryProvider instances.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]MemoryProvider
}

// NewProviderRegistry creates an empty registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]MemoryProvider)}
}

// Register adds a provider. Returns an error if a provider with the same name
// already exists.
func (r *ProviderRegistry) Register(p MemoryProvider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[p.Name()]; ok {
		return fmt.Errorf("watcher: provider %q already registered", p.Name())
	}
	r.providers[p.Name()] = p
	return nil
}

// List returns all registered providers.
func (r *ProviderRegistry) List() []MemoryProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]MemoryProvider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	return out
}

// Get returns a provider by name, or an error if not found.
func (r *ProviderRegistry) Get(name string) (MemoryProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("watcher: provider %q not found", name)
	}
	return p, nil
}

// Statuses returns the status of every registered provider.
func (r *ProviderRegistry) Statuses() []ProviderStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProviderStatus, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p.Status())
	}
	return out
}

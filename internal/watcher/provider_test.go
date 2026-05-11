package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubProvider implements MemoryProvider for testing.
type stubProvider struct {
	name        string
	description string
	raws        []RawMemory
	scanErr     error
	healthy     bool
	lastScanAt  time.Time
	lastError   string
	totalSeen   int
}

func (p *stubProvider) Name() string        { return p.name }
func (p *stubProvider) Description() string  { return p.description }
func (p *stubProvider) Scan(ctx context.Context) ([]RawMemory, error) {
	p.lastScanAt = time.Now()
	if p.scanErr != nil {
		return nil, p.scanErr
	}
	return p.raws, nil
}
func (p *stubProvider) Status() ProviderStatus {
	return ProviderStatus{
		Name:        p.name,
		Description: p.description,
		Healthy:     p.healthy,
		LastScanAt:  p.lastScanAt,
		LastError:   p.lastError,
		TotalSeen:   p.totalSeen,
	}
}

func TestRawMemoryHashContent(t *testing.T) {
	r := RawMemory{Content: "hello"}
	r.HashContent()
	assert.NotEmpty(t, r.ContentHash)
	assert.Len(t, r.ContentHash, 64) // SHA-256 hex

	// Same content → same hash.
	r2 := RawMemory{Content: "hello"}
	r2.HashContent()
	assert.Equal(t, r.ContentHash, r2.ContentHash)

	// Different content → different hash.
	r3 := RawMemory{Content: "world"}
	r3.HashContent()
	assert.NotEqual(t, r.ContentHash, r3.ContentHash)
}

func TestSeenSet(t *testing.T) {
	ss := newSeenSet(100)

	assert.False(t, ss.Has("uri1", "hash1"))
	ss.Mark("uri1", "hash1")
	assert.True(t, ss.Has("uri1", "hash1"))
	assert.False(t, ss.Has("uri1", "hash2"))
	assert.False(t, ss.Has("uri2", "hash1"))
	assert.Equal(t, 1, ss.Len())

	ss.Mark("uri1", "hash1") // duplicate mark is idempotent
	assert.Equal(t, 1, ss.Len())
}

func TestSeenSetLRUEviction(t *testing.T) {
	ss := newSeenSet(3) // small capacity
	for i := range 5 {
		uri := "uri" + string(rune('a'+i))
		hash := "hash" + string(rune('0'+i))
		ss.Mark(uri, hash)
	}
	assert.LessOrEqual(t, ss.Len(), 3) // oldest should be evicted
}

func TestSeenSetDumpRestore(t *testing.T) {
	ss := newSeenSet(100)
	ss.Mark("a", "h1")
	ss.Mark("b", "h2")
	ss.Mark("c", "h3")

	dir := t.TempDir()
	path := filepath.Join(dir, "seen.json")

	require.NoError(t, DumpSeenSet(ss, path))

	ss2 := newSeenSet(100)
	require.NoError(t, RestoreSeenSet(ss2, path))

	assert.True(t, ss2.Has("a", "h1"))
	assert.True(t, ss2.Has("b", "h2"))
	assert.True(t, ss2.Has("c", "h3"))
	assert.Equal(t, 3, ss2.Len())
}

func TestRestoreSeenSetMissingFile(t *testing.T) {
	ss := newSeenSet(100)
	err := RestoreSeenSet(ss, "/nonexistent/path/seen.json")
	assert.NoError(t, err) // missing file is not an error
}

func TestRestoreSeenSetCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seen.json")
	require.NoError(t, os.WriteFile(path, []byte("not-json"), 0o644))

	ss := newSeenSet(100)
	err := RestoreSeenSet(ss, path)
	assert.Error(t, err)
}

func TestProviderRegistry(t *testing.T) {
	reg := NewProviderRegistry()

	p1 := &stubProvider{name: "claude-code", description: "Claude Code adapter"}
	p2 := &stubProvider{name: "opencode", description: "OpenCode adapter"}

	require.NoError(t, reg.Register(p1))
	require.NoError(t, reg.Register(p2))

	// Duplicate registration.
	err := reg.Register(p1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")

	// List.
	list := reg.List()
	assert.Len(t, list, 2)

	// Get.
	got, err := reg.Get("claude-code")
	require.NoError(t, err)
	assert.Equal(t, p1, got)

	_, err = reg.Get("unknown")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Statuses.
	statuses := reg.Statuses()
	assert.Len(t, statuses, 2)
}

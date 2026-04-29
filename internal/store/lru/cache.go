// Package lru provides an in-process LRU cache with item-level TTL for
// hot-memory indexing. It replaces Redis sorted sets for frequently accessed
// memories in the SQLite backend.
package lru

import (
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

type cacheEntry[V any] struct {
	value   V
	expires time.Time
}

// HotCache is an in-process LRU cache with item-level TTL for frequently
// accessed memories. A zero TTL means no expiration.
type HotCache[K comparable, V any] struct {
	mu    sync.RWMutex
	cache *lru.Cache[K, cacheEntry[V]]
	ttl   time.Duration
}

// NewHotCache creates a new LRU cache with the given maxSize and TTL.
// A ttl of 0 means entries never expire.
func NewHotCache[K comparable, V any](maxSize int, ttl time.Duration) *HotCache[K, V] {
	c, _ := lru.New[K, cacheEntry[V]](maxSize)
	return &HotCache[K, V]{
		cache: c,
		ttl:   ttl,
	}
}

// Get returns the cached value and true if the key exists and has not expired.
func (c *HotCache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	entry, ok := c.cache.Get(key)
	c.mu.RUnlock()
	if !ok {
		var zero V
		return zero, false
	}
	if c.ttl > 0 && time.Now().After(entry.expires) {
		c.mu.Lock()
		c.cache.Remove(key)
		c.mu.Unlock()
		var zero V
		return zero, false
	}
	return entry.value, true
}

// Set adds a value to the cache. If the key already exists, it is replaced.
func (c *HotCache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := cacheEntry[V]{value: value}
	if c.ttl > 0 {
		entry.expires = time.Now().Add(c.ttl)
	}
	c.cache.Add(key, entry)
}

// Remove deletes a key from the cache.
func (c *HotCache[K, V]) Remove(key K) {
	c.mu.Lock()
	c.cache.Remove(key)
	c.mu.Unlock()
}

// Len returns the current number of items in the cache.
func (c *HotCache[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cache.Len()
}

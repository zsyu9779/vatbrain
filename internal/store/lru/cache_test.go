package lru

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewHotCache(t *testing.T) {
	c := NewHotCache[string, int](10, time.Minute)
	assert.NotNil(t, c)
	assert.Equal(t, 0, c.Len())
}

func TestHotCache_SetGet(t *testing.T) {
	c := NewHotCache[string, string](10, 0)
	c.Set("key1", "value1")
	v, ok := c.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", v)
}

func TestHotCache_Get_Miss(t *testing.T) {
	c := NewHotCache[string, int](10, 0)
	v, ok := c.Get("nonexistent")
	assert.False(t, ok)
	assert.Equal(t, 0, v) // zero value
}

func TestHotCache_Set_Overwrite(t *testing.T) {
	c := NewHotCache[string, string](10, 0)
	c.Set("k", "old")
	c.Set("k", "new")
	v, ok := c.Get("k")
	assert.True(t, ok)
	assert.Equal(t, "new", v)
}

func TestHotCache_Remove(t *testing.T) {
	c := NewHotCache[string, int](10, 0)
	c.Set("k", 42)
	assert.Equal(t, 1, c.Len())
	c.Remove("k")
	assert.Equal(t, 0, c.Len())
	_, ok := c.Get("k")
	assert.False(t, ok)
}

func TestHotCache_Len(t *testing.T) {
	c := NewHotCache[int, string](10, 0)
	assert.Equal(t, 0, c.Len())
	c.Set(1, "a")
	c.Set(2, "b")
	c.Set(3, "c")
	assert.Equal(t, 3, c.Len())
}

func TestHotCache_TTL_Expired(t *testing.T) {
	c := NewHotCache[string, string](10, 1*time.Millisecond)
	c.Set("k", "v")
	time.Sleep(5 * time.Millisecond)
	v, ok := c.Get("k")
	assert.False(t, ok)
	assert.Empty(t, v)
}

func TestHotCache_TTL_NotExpired(t *testing.T) {
	c := NewHotCache[string, string](10, time.Hour)
	c.Set("k", "v")
	time.Sleep(5 * time.Millisecond)
	v, ok := c.Get("k")
	assert.True(t, ok)
	assert.Equal(t, "v", v)
}

func TestHotCache_TTL_Zero_NoExpiry(t *testing.T) {
	c := NewHotCache[string, string](10, 0)
	c.Set("k", "v")
	time.Sleep(5 * time.Millisecond)
	v, ok := c.Get("k")
	assert.True(t, ok)
	assert.Equal(t, "v", v)
}

func TestHotCache_TTL_Expired_RemoveOnGet(t *testing.T) {
	c := NewHotCache[string, string](10, 1*time.Millisecond)
	c.Set("k", "v")
	time.Sleep(5 * time.Millisecond)
	// Get should detect expiry and remove the entry.
	c.Get("k")
	assert.Equal(t, 0, c.Len())
}

func TestHotCache_LRU_Eviction(t *testing.T) {
	c := NewHotCache[string, string](2, 0)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Set("c", "3") // should evict "a" (least recently used)
	_, ok := c.Get("a")
	assert.False(t, ok, "a should be evicted")
	v, ok := c.Get("b")
	assert.True(t, ok)
	assert.Equal(t, "2", v)
	v, ok = c.Get("c")
	assert.True(t, ok)
	assert.Equal(t, "3", v)
}

func TestHotCache_LRU_GetPromotes(t *testing.T) {
	c := NewHotCache[string, string](2, 0)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Get("a")        // "a" promoted, "b" is now LRU
	c.Set("c", "3")   // should evict "b"
	_, ok := c.Get("b")
	assert.False(t, ok, "b should be evicted")
	v, ok := c.Get("a")
	assert.True(t, ok)
	assert.Equal(t, "1", v)
}

func TestHotCache_GenericTypes(t *testing.T) {
	// Verify it works with non-string types.
	c := NewHotCache[int, []byte](10, 0)
	c.Set(1, []byte("hello"))
	v, ok := c.Get(1)
	assert.True(t, ok)
	assert.Equal(t, []byte("hello"), v)
}

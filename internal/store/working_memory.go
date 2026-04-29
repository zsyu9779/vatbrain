// Package store provides the MemoryStore abstraction over VatBrain persistence.
package store

import (
	"sync"
)

// ringBuffer is a fixed-size ring buffer for working-memory summaries.
type ringBuffer struct {
	items []string
	head  int
	size  int
}

func newRingBuffer(maxSize int) *ringBuffer {
	return &ringBuffer{items: make([]string, maxSize)}
}

func (rb *ringBuffer) push(s string) {
	rb.items[rb.head] = s
	rb.head = (rb.head + 1) % len(rb.items)
	if rb.size < len(rb.items) {
		rb.size++
	}
}

func (rb *ringBuffer) getAll() []string {
	out := make([]string, rb.size)
	for i := 0; i < rb.size; i++ {
		idx := (rb.head - rb.size + i + len(rb.items)) % len(rb.items)
		out[i] = rb.items[idx]
	}
	return out
}

// WorkingMemoryBuffer is an in-process ring buffer that replaces Redis for
// working-memory cycle storage in SQLite mode. Each project has its own buffer.
type WorkingMemoryBuffer struct {
	mu      sync.Mutex
	cycles  map[string]*ringBuffer // projectID -> buffer
	maxSize int
}

// NewWorkingMemoryBuffer creates a new working-memory buffer. maxSize is the
// maximum number of summaries per project.
func NewWorkingMemoryBuffer(maxSize int) *WorkingMemoryBuffer {
	if maxSize <= 0 {
		maxSize = 20
	}
	return &WorkingMemoryBuffer{
		cycles:  make(map[string]*ringBuffer),
		maxSize: maxSize,
	}
}

// Push adds a summary to the working-memory buffer for a given project.
func (w *WorkingMemoryBuffer) Push(projectID, summary string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rb, ok := w.cycles[projectID]
	if !ok {
		rb = newRingBuffer(w.maxSize)
		w.cycles[projectID] = rb
	}
	rb.push(summary)
}

// GetAll returns all summaries in the working-memory buffer for a given project,
// in insertion order (oldest first).
func (w *WorkingMemoryBuffer) GetAll(projectID string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	rb, ok := w.cycles[projectID]
	if !ok {
		return nil
	}
	return rb.getAll()
}

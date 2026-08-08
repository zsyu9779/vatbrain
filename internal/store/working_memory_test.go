package store

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewWorkingMemoryBuffer_DefaultMaxSize(t *testing.T) {
	w := NewWorkingMemoryBuffer(0)
	assert.NotNil(t, w)
	assert.Equal(t, 20, w.maxSize)
	assert.NotNil(t, w.cycles)

	w2 := NewWorkingMemoryBuffer(-5)
	assert.Equal(t, 20, w2.maxSize)
}

func TestNewWorkingMemoryBuffer_CustomMaxSize(t *testing.T) {
	w := NewWorkingMemoryBuffer(10)
	assert.Equal(t, 10, w.maxSize)
}

func TestRingBuffer_Empty(t *testing.T) {
	rb := newRingBuffer(5)
	assert.Empty(t, rb.getAll())
}

func TestRingBuffer_PushGetAll_Partial(t *testing.T) {
	rb := newRingBuffer(5)
	rb.push("a")
	rb.push("b")
	rb.push("c")
	result := rb.getAll()
	assert.Equal(t, []string{"a", "b", "c"}, result)
}

func TestRingBuffer_PushGetAll_Full(t *testing.T) {
	rb := newRingBuffer(3)
	rb.push("1")
	rb.push("2")
	rb.push("3")
	result := rb.getAll()
	assert.Equal(t, []string{"1", "2", "3"}, result)
}

func TestRingBuffer_PushGetAll_Overflow(t *testing.T) {
	rb := newRingBuffer(3)
	rb.push("1")
	rb.push("2")
	rb.push("3")
	rb.push("4") // overwrites "1"
	result := rb.getAll()
	assert.Equal(t, []string{"2", "3", "4"}, result)
}

func TestRingBuffer_PushGetAll_WrapTwice(t *testing.T) {
	rb := newRingBuffer(3)
	rb.push("a") // [a, -, -]
	rb.push("b") // [a, b, -]
	rb.push("c") // [a, b, c]
	rb.push("d") // [d, b, c]
	rb.push("e") // [d, e, c]
	rb.push("f") // [d, e, f]
	result := rb.getAll()
	assert.Equal(t, []string{"d", "e", "f"}, result)
}

func TestWorkingMemoryBuffer_PushGetAll(t *testing.T) {
	w := NewWorkingMemoryBuffer(20)
	w.Push("proj1", "summary A")
	w.Push("proj1", "summary B")
	w.Push("proj2", "summary C")

	result1 := w.GetAll("proj1")
	assert.Equal(t, []string{"summary A", "summary B"}, result1)

	result2 := w.GetAll("proj2")
	assert.Equal(t, []string{"summary C"}, result2)
}

func TestWorkingMemoryBuffer_GetAll_UnknownProject(t *testing.T) {
	w := NewWorkingMemoryBuffer(20)
	assert.Nil(t, w.GetAll("nonexistent"))
}

func TestWorkingMemoryBuffer_Concurrent(t *testing.T) {
	w := NewWorkingMemoryBuffer(100)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			w.Push("concurrent", "msg")
		}(i)
	}
	wg.Wait()
	result := w.GetAll("concurrent")
	assert.Len(t, result, 50)
}

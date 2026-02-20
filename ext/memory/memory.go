// Package memory provides conversation memory implementations for gollem agents,
// allowing message history to be stored and retrieved across agent runs.
package memory

import (
	"context"
	"sync"

	"github.com/fugue-labs/gollem/core"
)

// Memory is the interface for conversation memory stores.
type Memory interface {
	// Get returns stored messages.
	Get(ctx context.Context) ([]core.ModelMessage, error)

	// Add appends messages to the store.
	Add(ctx context.Context, messages ...core.ModelMessage) error

	// Clear removes all stored messages.
	Clear(ctx context.Context) error
}

// BufferMemory is an in-memory circular buffer that stores a fixed number of messages.
type BufferMemory struct {
	mu       sync.RWMutex
	messages []core.ModelMessage
	maxSize  int
}

// BufferOption configures a BufferMemory.
type BufferOption func(*BufferMemory)

// WithMaxMessages sets the maximum number of messages to store.
// When exceeded, the oldest messages are dropped. Default is 100.
func WithMaxMessages(n int) BufferOption {
	return func(b *BufferMemory) {
		b.maxSize = n
	}
}

// NewBuffer creates a new in-memory buffer memory.
func NewBuffer(opts ...BufferOption) *BufferMemory {
	b := &BufferMemory{
		maxSize: 100,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Get returns a copy of all stored messages.
func (b *BufferMemory) Get(_ context.Context) ([]core.ModelMessage, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	result := make([]core.ModelMessage, len(b.messages))
	copy(result, b.messages)
	return result, nil
}

// Add appends messages, dropping oldest if buffer exceeds max size.
func (b *BufferMemory) Add(_ context.Context, messages ...core.ModelMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.messages = append(b.messages, messages...)

	// Trim from the front if over capacity.
	if len(b.messages) > b.maxSize {
		excess := len(b.messages) - b.maxSize
		b.messages = b.messages[excess:]
	}

	return nil
}

// Clear removes all stored messages.
func (b *BufferMemory) Clear(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.messages = nil
	return nil
}

// Len returns the number of stored messages.
func (b *BufferMemory) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.messages)
}

// Verify BufferMemory implements Memory.
var _ Memory = (*BufferMemory)(nil)

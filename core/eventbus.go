package core

import (
	"reflect"
	"sync"
)

// EventBus provides typed publish-subscribe for agent coordination.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[reflect.Type][]subscriberEntry
	nextID      int
}

type subscriberEntry struct {
	id int
	fn reflect.Value
}

// NewEventBus creates a new event bus.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[reflect.Type][]subscriberEntry),
	}
}

// Subscribe registers a handler for events of a specific type.
// Returns an unsubscribe function.
func Subscribe[E any](bus *EventBus, handler func(E)) func() {
	bus.mu.Lock()
	defer bus.mu.Unlock()

	var zero E
	eventType := reflect.TypeOf(zero)

	id := bus.nextID
	bus.nextID++

	bus.subscribers[eventType] = append(bus.subscribers[eventType], subscriberEntry{
		id: id,
		fn: reflect.ValueOf(handler),
	})

	return func() {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		subs := bus.subscribers[eventType]
		for i, s := range subs {
			if s.id == id {
				bus.subscribers[eventType] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
	}
}

// Publish sends an event to all matching subscribers synchronously.
func Publish[E any](bus *EventBus, event E) {
	var zero E
	eventType := reflect.TypeOf(zero)

	bus.mu.RLock()
	subs := make([]subscriberEntry, len(bus.subscribers[eventType]))
	copy(subs, bus.subscribers[eventType])
	bus.mu.RUnlock()

	for _, sub := range subs {
		sub.fn.Call([]reflect.Value{reflect.ValueOf(event)})
	}
}

// PublishAsync sends an event to subscribers asynchronously (non-blocking).
func PublishAsync[E any](bus *EventBus, event E) {
	var zero E
	eventType := reflect.TypeOf(zero)

	bus.mu.RLock()
	subs := make([]subscriberEntry, len(bus.subscribers[eventType]))
	copy(subs, bus.subscribers[eventType])
	bus.mu.RUnlock()

	for _, sub := range subs {
		go sub.fn.Call([]reflect.Value{reflect.ValueOf(event)})
	}
}

// WithEventBus attaches an event bus to the agent, accessible via RunContext.
func WithEventBus[T any](bus *EventBus) AgentOption[T] {
	return func(a *Agent[T]) {
		a.eventBus = bus
	}
}

// --- Built-in Event Types ---

// RunStartedEvent is published when an agent run starts.
type RunStartedEvent struct {
	RunID  string
	Prompt string
}

// RunCompletedEvent is published when an agent run completes.
type RunCompletedEvent struct {
	RunID   string
	Success bool
	Error   string
}

// ToolCalledEvent is published when a tool is called.
type ToolCalledEvent struct {
	RunID    string
	ToolName string
	ArgsJSON string
}

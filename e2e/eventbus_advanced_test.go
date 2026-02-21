//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestEventBusMultipleSubscribers verifies multiple subscribers receive events.
func TestEventBusMultipleSubscribers(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	bus := core.NewEventBus()

	var mu sync.Mutex
	var sub1Events, sub2Events []string

	core.Subscribe[core.RunStartedEvent](bus, func(e core.RunStartedEvent) {
		mu.Lock()
		sub1Events = append(sub1Events, "sub1:"+e.RunID)
		mu.Unlock()
	})

	core.Subscribe[core.RunStartedEvent](bus, func(e core.RunStartedEvent) {
		mu.Lock()
		sub2Events = append(sub2Events, "sub2:"+e.RunID)
		mu.Unlock()
	})

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithEventBus[string](bus),
	)

	_, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(sub1Events) == 0 {
		t.Error("subscriber 1 received no events")
	}
	if len(sub2Events) == 0 {
		t.Error("subscriber 2 received no events")
	}

	t.Logf("Sub1 events: %v, Sub2 events: %v", sub1Events, sub2Events)
}

// TestEventBusUnsubscribe verifies that unsubscribing stops event delivery.
func TestEventBusUnsubscribe(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	bus := core.NewEventBus()

	var mu sync.Mutex
	var events []string

	unsub := core.Subscribe[core.RunStartedEvent](bus, func(e core.RunStartedEvent) {
		mu.Lock()
		events = append(events, e.RunID)
		mu.Unlock()
	})

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithEventBus[string](bus),
	)

	// First run - should receive event.
	_, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("first run failed: %v", err)
	}

	mu.Lock()
	countAfterFirst := len(events)
	mu.Unlock()

	if countAfterFirst == 0 {
		t.Fatal("expected at least one event before unsubscribe")
	}

	// Unsubscribe.
	unsub()

	// Second run - should NOT receive event.
	_, err = agent.Run(ctx, "Say goodbye")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("second run failed: %v", err)
	}

	mu.Lock()
	countAfterSecond := len(events)
	mu.Unlock()

	if countAfterSecond != countAfterFirst {
		t.Errorf("expected no new events after unsubscribe: before=%d after=%d", countAfterFirst, countAfterSecond)
	}

	t.Logf("Events before unsub: %d, after: %d", countAfterFirst, countAfterSecond)
}

// TestEventBusCustomEvents verifies custom event types work.
func TestEventBusCustomEvents(t *testing.T) {
	bus := core.NewEventBus()

	type CustomEvent struct {
		Value string
	}

	var mu sync.Mutex
	var received []string

	core.Subscribe[CustomEvent](bus, func(e CustomEvent) {
		mu.Lock()
		received = append(received, e.Value)
		mu.Unlock()
	})

	core.Publish(bus, CustomEvent{Value: "hello"})
	core.Publish(bus, CustomEvent{Value: "world"})

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 2 {
		t.Errorf("expected 2 events, got %d", len(received))
	}
	if len(received) > 0 && received[0] != "hello" {
		t.Errorf("expected first event 'hello', got %q", received[0])
	}
	if len(received) > 1 && received[1] != "world" {
		t.Errorf("expected second event 'world', got %q", received[1])
	}

	t.Logf("Custom events received: %v", received)
}

// TestHookToolStartEnd verifies OnToolStart and OnToolEnd fire correctly.
func TestHookToolStartEnd(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var mu sync.Mutex
	var toolStarts, toolEnds []string

	hook := core.Hook{
		OnToolStart: func(ctx context.Context, rc *core.RunContext, toolName string, argsJSON string) {
			mu.Lock()
			toolStarts = append(toolStarts, toolName)
			mu.Unlock()
		},
		OnToolEnd: func(ctx context.Context, rc *core.RunContext, toolName string, result string, err error) {
			mu.Lock()
			toolEnds = append(toolEnds, toolName)
			mu.Unlock()
		},
	}

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A+p.B), nil
	})

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
		core.WithHooks[string](hook),
	)

	_, err := agent.Run(ctx, "Use the add tool to compute 1+2.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(toolStarts) == 0 {
		t.Error("OnToolStart was never called")
	}
	if len(toolEnds) == 0 {
		t.Error("OnToolEnd was never called")
	}

	// Both should reference "add".
	for _, name := range toolStarts {
		if name != "add" {
			t.Errorf("unexpected tool start: %q", name)
		}
	}
	for _, name := range toolEnds {
		if name != "add" {
			t.Errorf("unexpected tool end: %q", name)
		}
	}

	t.Logf("ToolStarts=%v ToolEnds=%v", toolStarts, toolEnds)
}

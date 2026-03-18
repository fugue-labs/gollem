package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeEventConstructors_AdditionalObservability(t *testing.T) {
	t.Run("tool completed", func(t *testing.T) {
		when := time.Unix(1, 0)
		evt := NewToolCompletedEvent("run-1", "parent-1", "call-1", "echo", `"done"`, when, nil)
		if evt.RuntimeEventType() != RuntimeEventTypeToolCompleted {
			t.Fatalf("unexpected event type %q", evt.RuntimeEventType())
		}
		if evt.RunID != "run-1" || evt.ParentRunID != "parent-1" || evt.ToolCallID != "call-1" || evt.ToolName != "echo" {
			t.Fatalf("unexpected tool completed event: %+v", evt)
		}
		if evt.Result != `"done"` || evt.Error != "" || !evt.CompletedAt.Equal(when) {
			t.Fatalf("unexpected tool completed payload: %+v", evt)
		}
	})

	t.Run("approval requested", func(t *testing.T) {
		when := time.Unix(2, 0)
		evt := NewApprovalRequestedEvent("run-1", "parent-1", "call-1", "dangerous", `{}`, when)
		if evt.RuntimeEventType() != RuntimeEventTypeApprovalRequested {
			t.Fatalf("unexpected event type %q", evt.RuntimeEventType())
		}
		if evt.ToolName != "dangerous" || evt.ArgsJSON != `{}` || !evt.RequestedAt.Equal(when) {
			t.Fatalf("unexpected approval requested event: %+v", evt)
		}
	})

	t.Run("approval resolved", func(t *testing.T) {
		when := time.Unix(3, 0)
		evt := NewApprovalResolvedEvent("run-1", "parent-1", "call-1", "dangerous", `{}`, false, when, errors.New("denied"))
		if evt.RuntimeEventType() != RuntimeEventTypeApprovalResolved {
			t.Fatalf("unexpected event type %q", evt.RuntimeEventType())
		}
		if evt.Approved {
			t.Fatal("expected approval to be false")
		}
		if evt.Error != "denied" || !evt.ResolvedAt.Equal(when) {
			t.Fatalf("unexpected approval resolved event: %+v", evt)
		}
	})

	t.Run("deferred wait", func(t *testing.T) {
		when := time.Unix(4, 0)
		evt := NewDeferredWaitEvent("run-1", "parent-1", "call-1", "async_task", `{"job":1}`, "waiting", when)
		if evt.RuntimeEventType() != RuntimeEventTypeDeferredWait {
			t.Fatalf("unexpected event type %q", evt.RuntimeEventType())
		}
		if evt.Message != "waiting" || evt.ToolName != "async_task" || !evt.WaitingAt.Equal(when) {
			t.Fatalf("unexpected deferred wait event: %+v", evt)
		}
	})
}

func TestEventBus_PublishesApprovalAndDeferredEvents(t *testing.T) {
	bus := NewEventBus()

	var requested ApprovalRequestedEvent
	var resolved ApprovalResolvedEvent
	var deferred DeferredWaitEvent
	var completed ToolCompletedEvent
	var requestedCount atomic.Int32
	var resolvedCount atomic.Int32
	var deferredCount atomic.Int32
	var completedCount atomic.Int32

	Subscribe(bus, func(e ApprovalRequestedEvent) {
		requested = e
		requestedCount.Add(1)
	})
	Subscribe(bus, func(e ApprovalResolvedEvent) {
		resolved = e
		resolvedCount.Add(1)
	})
	Subscribe(bus, func(e DeferredWaitEvent) {
		deferred = e
		deferredCount.Add(1)
	})
	Subscribe(bus, func(e ToolCompletedEvent) {
		completed = e
		completedCount.Add(1)
	})

	type params struct {
		Target string `json:"target"`
	}
	tool := FuncTool[params]("dangerous", "dangerous action", func(_ context.Context, _ params) (string, error) {
		return "", &CallDeferred{Message: "awaiting operator"}
	}, WithRequiresApproval())

	model := NewTestModel(ToolCallResponse("dangerous", `{"target":"prod"}`))
	agent := NewAgent[string](model,
		WithTools[string](tool),
		WithToolApproval[string](func(_ context.Context, _ string, _ string) (bool, error) { return true, nil }),
		WithEventBus[string](bus),
	)

	_, err := agent.Run(context.Background(), "do dangerous thing")
	var deferredErr *ErrDeferred[string]
	if !errors.As(err, &deferredErr) {
		t.Fatalf("expected ErrDeferred, got %v", err)
	}

	if requestedCount.Load() != 1 || requested.ToolName != "dangerous" {
		t.Fatalf("unexpected approval requested events: count=%d event=%+v", requestedCount.Load(), requested)
	}
	if resolvedCount.Load() != 1 || !resolved.Approved || resolved.ToolName != "dangerous" {
		t.Fatalf("unexpected approval resolved events: count=%d event=%+v", resolvedCount.Load(), resolved)
	}
	if deferredCount.Load() != 1 || deferred.Message != "awaiting operator" {
		t.Fatalf("unexpected deferred wait events: count=%d event=%+v", deferredCount.Load(), deferred)
	}
	if completedCount.Load() != 1 || completed.ToolName != "dangerous" || completed.Error != "deferred: awaiting operator" {
		t.Fatalf("unexpected tool completed events: count=%d event=%+v", completedCount.Load(), completed)
	}
}

package core

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

type testDeferredRunError struct{}

func (testDeferredRunError) Error() string        { return "deferred" }
func (testDeferredRunError) deferredRunError()    {}
func (testDeferredRunError) Is(target error) bool { return target == errDeferredSentinel }

var errDeferredSentinel = errors.New("deferred sentinel")

type errorRaisedEventTestModel struct {
	err error
}

func (m errorRaisedEventTestModel) Request(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error) {
	return nil, m.err
}

func (m errorRaisedEventTestModel) RequestStream(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (StreamedResponse, error) {
	return nil, m.err
}

func (errorRaisedEventTestModel) ModelName() string {
	return "error-raised-event-test"
}

func TestRuntimeEventsExposeCanonicalMetadata(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	score := 0.9
	passed := true
	completed := NewRunCompletedEvent("run-1", "parent-1", now, now.Add(time.Second), testDeferredRunError{})
	if completed.Success || !completed.Deferred || completed.Error != "deferred" {
		t.Fatalf("unexpected completed event: %+v", completed)
	}
	if got := NewRunCompletedEvent("run-1", "", now, now, nil); !got.Success || got.Deferred || got.Error != "" {
		t.Fatalf("unexpected successful completed event: %+v", got)
	}
	if isDeferredRunError(nil) {
		t.Fatal("nil error should not be deferred")
	}

	events := []RuntimeEvent{
		NewRunStartedEvent("run-1", "parent-1", "prompt", now),
		completed,
		NewToolCalledEvent("run-1", "parent-1", "call-1", "shell", `{}`, now),
		ToolCompletedEvent{RunID: "run-1", ParentRunID: "parent-1", ToolCallID: "call-1", ToolName: "shell", Result: "ok", CompletedAt: now},
		ToolFailedEvent{RunID: "run-1", ParentRunID: "parent-1", ToolCallID: "call-1", ToolName: "shell", Error: "boom", FailedAt: now},
		TurnStartedEvent{RunID: "run-1", ParentRunID: "parent-1", TurnNumber: 1, StartedAt: now},
		TurnCompletedEvent{RunID: "run-1", ParentRunID: "parent-1", TurnNumber: 1, HasText: true, CompletedAt: now},
		ModelRequestStartedEvent{RunID: "run-1", ParentRunID: "parent-1", TurnNumber: 1, MessageCount: 2, StartedAt: now},
		ModelResponseCompletedEvent{RunID: "run-1", ParentRunID: "parent-1", TurnNumber: 1, FinishReason: "stop", InputTokens: 1, OutputTokens: 2, CompletedAt: now},
		ModelDeltaEvent{RunID: "run-1", ParentRunID: "parent-1", TurnNumber: 1, PartIndex: 0, DeltaKind: "text", ContentDelta: "hi", DeltaAt: now},
		ApprovalRequestedEvent{RunID: "run-1", ParentRunID: "parent-1", ToolCallID: "call-1", ToolName: "write", RequestedAt: now},
		ApprovalResolvedEvent{RunID: "run-1", ParentRunID: "parent-1", ToolCallID: "call-1", ToolName: "write", Approved: true, ResolvedAt: now},
		DeferredRequestedEvent{RunID: "run-1", ParentRunID: "parent-1", ToolCallID: "call-1", ToolName: "search", RequestedAt: now},
		DeferredResolvedEvent{RunID: "run-1", ParentRunID: "parent-1", ToolCallID: "call-1", ToolName: "search", Content: "done", ResolvedAt: now},
		RunWaitingEvent{RunID: "run-1", ParentRunID: "parent-1", Reason: "approval", WaitingAt: now},
		RunResumedEvent{RunID: "run-1", ParentRunID: "parent-1", ResumedAt: now},
		ArtifactChangedEvent{RunID: "run-1", ParentRunID: "parent-1", ToolCallID: "call-1", ToolName: "write", Path: "main.go", Operation: "write", ChangedAt: now},
		RetryScheduledEvent{RunID: "run-1", ParentRunID: "parent-1", TurnNumber: 1, ToolName: "shell", ToolCallID: "call-1", Reason: "invalid", ScheduledAt: now},
		CheckpointCreatedEvent{RunID: "run-1", ParentRunID: "parent-1", CheckpointID: "snap-1", SnapshotID: "snap-1", Step: 1, CreatedAt: now},
		TopologyTransitionedEvent{RunID: "run-1", ParentRunID: "parent-1", From: "single", To: "team", Reason: "test", TransitionedAt: now},
		EvaluatorCompletedEvent{RunID: "run-1", ParentRunID: "parent-1", Name: "tests", Score: &score, Passed: &passed, CompletedAt: now},
		ErrorRaisedEvent{RunID: "run-1", ParentRunID: "parent-1", TurnNumber: 1, Error: "boom", RaisedAt: now},
	}

	for _, event := range events {
		if event.RuntimeEventType() == "" {
			t.Fatalf("missing type for %#v", event)
		}
		if event.RuntimeRunID() != "run-1" {
			t.Fatalf("run id = %q for %#v", event.RuntimeRunID(), event)
		}
		if event.RuntimeParentRunID() != "parent-1" {
			t.Fatalf("parent id = %q for %#v", event.RuntimeParentRunID(), event)
		}
		if !event.RuntimeOccurredAt().Equal(now) && !event.RuntimeOccurredAt().Equal(now.Add(time.Second)) {
			t.Fatalf("unexpected occurrence time %s for %#v", event.RuntimeOccurredAt(), event)
		}
	}
}

func TestAgentErrorRaisedEventRetainsOriginalCauseForInProcessSubscribers(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	cause := &ModelHTTPError{
		Message:    "Authorization: Bearer super-secret",
		StatusCode: 429,
		Body:       "prompt=super-secret",
	}
	var received ErrorRaisedEvent
	Subscribe(bus, func(event ErrorRaisedEvent) {
		received = event
	})

	agent := NewAgent[string](errorRaisedEventTestModel{err: cause}, WithEventBus[string](bus))
	_, err := agent.Run(context.Background(), "fail")
	if !errors.Is(err, cause) {
		t.Fatalf("Run error = %v, want %v", err, cause)
	}

	var httpErr *ModelHTTPError
	if !errors.As(received.Cause, &httpErr) || httpErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("Cause = %#v, want HTTP 429 error", received.Cause)
	}
	if received.Error != err.Error() {
		t.Fatalf("Error = %q, want %q", received.Error, err.Error())
	}
}

func TestAgentRecordRetryScheduledPublishesTraceAndRuntimeEvent(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	var got RetryScheduledEvent
	Subscribe(bus, func(event RetryScheduledEvent) {
		got = event
	})

	agent := &Agent[string]{
		tracingEnabled: true,
		eventBus:       bus,
		maxRetries:     4,
	}
	state := &agentRunState{
		runID:       "run-retry",
		parentRunID: "parent-retry",
		runStep:     3,
		retries:     2,
	}

	agent.recordRetryScheduled(state, "tool result failed validation", "shell", "call-1")

	if got.RunID != "run-retry" || got.ParentRunID != "parent-retry" || got.ToolName != "shell" || got.ToolCallID != "call-1" {
		t.Fatalf("unexpected retry event: %+v", got)
	}
	if got.Retry != 2 || got.MaxRetries != 4 || got.TurnNumber != 3 || got.Reason != "tool result failed validation" {
		t.Fatalf("unexpected retry metadata: %+v", got)
	}
	if len(state.traceSteps) != 1 {
		t.Fatalf("trace steps = %+v, want one retry step", state.traceSteps)
	}
	step := state.traceSteps[0]
	if step.Kind != TraceRetryScheduled {
		t.Fatalf("step kind = %q, want %q", step.Kind, TraceRetryScheduled)
	}
	data, ok := step.Data.(map[string]any)
	if !ok {
		t.Fatalf("retry step data type = %T", step.Data)
	}
	if data["retry"] != 2 || data["max_retries"] != 4 || data["tool_name"] != "shell" {
		t.Fatalf("unexpected retry step data: %+v", data)
	}

	agent.recordRetryScheduled(nil, "ignored", "", "")
}

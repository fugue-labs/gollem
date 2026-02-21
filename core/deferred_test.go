package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDeferredTool_BasicDefer(t *testing.T) {
	// Tool that returns CallDeferred.
	deferTool := Tool{
		Definition: ToolDefinition{
			Name:        "async_task",
			Description: "An async task",
			Kind:        ToolKindFunction,
		},
		Handler: func(_ context.Context, _ *RunContext, _ string) (any, error) {
			return nil, &CallDeferred{Message: "waiting for human approval"}
		},
	}

	// First response: model calls the deferred tool.
	// Second response: model gives final text answer (used if resumed).
	model := NewTestModel(
		ToolCallResponseWithID("async_task", `{}`, "call_1"),
		TextResponse("done"),
	)

	agent := NewAgent[string](model, WithTools[string](deferTool))
	_, err := agent.Run(context.Background(), "do the async task")

	// Should get ErrDeferred.
	var deferredErr *ErrDeferred[string]
	if !errors.As(err, &deferredErr) {
		t.Fatalf("expected ErrDeferred, got %v", err)
	}

	if len(deferredErr.Result.DeferredRequests) != 1 {
		t.Fatalf("expected 1 deferred request, got %d", len(deferredErr.Result.DeferredRequests))
	}

	req := deferredErr.Result.DeferredRequests[0]
	if req.ToolName != "async_task" {
		t.Errorf("ToolName = %q, want %q", req.ToolName, "async_task")
	}
	if req.ToolCallID != "call_1" {
		t.Errorf("ToolCallID = %q, want %q", req.ToolCallID, "call_1")
	}
}

func TestDeferredTool_ResumeWithResults(t *testing.T) {
	callCount := 0
	deferTool := Tool{
		Definition: ToolDefinition{
			Name:        "async_task",
			Description: "An async task",
			Kind:        ToolKindFunction,
		},
		Handler: func(_ context.Context, _ *RunContext, _ string) (any, error) {
			callCount++
			return nil, &CallDeferred{Message: "waiting"}
		},
	}

	// First call: model calls async_task -> deferred.
	// Second call (after resume): model gets deferred result, returns text answer.
	model := NewTestModel(
		ToolCallResponseWithID("async_task", `{}`, "call_1"),
		TextResponse("completed with result"),
	)

	agent := NewAgent[string](model, WithTools[string](deferTool))

	// First run: should get deferred.
	_, err := agent.Run(context.Background(), "do async task")
	var deferredErr *ErrDeferred[string]
	if !errors.As(err, &deferredErr) {
		t.Fatalf("expected ErrDeferred, got %v", err)
	}

	// Resume with deferred results.
	model.Reset()
	model2 := NewTestModel(TextResponse("completed with result"))
	agent2 := NewAgent[string](model2, WithTools[string](deferTool))

	result, err := agent2.Run(context.Background(), "do async task",
		WithMessages(deferredErr.Result.Messages...),
		WithDeferredResults(DeferredToolResult{
			ToolCallID: "call_1",
			Content:    "async task completed successfully",
		}),
	)
	if err != nil {
		t.Fatalf("resume Run: %v", err)
	}
	if result.Output != "completed with result" {
		t.Errorf("Output = %q, want %q", result.Output, "completed with result")
	}
}

func TestDeferredTool_MultipleDeferrals(t *testing.T) {
	// Two tools that both defer.
	makeDeferTool := func(name string) Tool {
		return Tool{
			Definition: ToolDefinition{
				Name:        name,
				Description: "An async tool",
				Kind:        ToolKindFunction,
			},
			Handler: func(_ context.Context, _ *RunContext, _ string) (any, error) {
				return nil, &CallDeferred{Message: "waiting"}
			},
		}
	}

	model := NewTestModel(
		MultiToolCallResponse(
			ToolCallPart{ToolName: "task_a", ArgsJSON: `{}`, ToolCallID: "call_a"},
			ToolCallPart{ToolName: "task_b", ArgsJSON: `{}`, ToolCallID: "call_b"},
		),
	)

	agent := NewAgent[string](model,
		WithTools[string](makeDeferTool("task_a"), makeDeferTool("task_b")),
	)

	_, err := agent.Run(context.Background(), "do both tasks")

	var deferredErr *ErrDeferred[string]
	if !errors.As(err, &deferredErr) {
		t.Fatalf("expected ErrDeferred, got %v", err)
	}

	if len(deferredErr.Result.DeferredRequests) != 2 {
		t.Fatalf("expected 2 deferred requests, got %d", len(deferredErr.Result.DeferredRequests))
	}

	names := map[string]bool{}
	for _, req := range deferredErr.Result.DeferredRequests {
		names[req.ToolName] = true
	}
	if !names["task_a"] || !names["task_b"] {
		t.Errorf("expected task_a and task_b in deferred requests, got %v", names)
	}
}

func TestDeferredTool_MixedDeferredAndNormal(t *testing.T) {
	// One tool defers, another returns normally.
	deferTool := Tool{
		Definition: ToolDefinition{
			Name:        "async_task",
			Description: "An async tool",
			Kind:        ToolKindFunction,
		},
		Handler: func(_ context.Context, _ *RunContext, _ string) (any, error) {
			return nil, &CallDeferred{Message: "waiting"}
		},
	}
	normalTool := Tool{
		Definition: ToolDefinition{
			Name:        "sync_task",
			Description: "A sync tool",
			Kind:        ToolKindFunction,
		},
		Handler: func(_ context.Context, _ *RunContext, _ string) (any, error) {
			return "done", nil
		},
	}

	model := NewTestModel(
		MultiToolCallResponse(
			ToolCallPart{ToolName: "sync_task", ArgsJSON: `{}`, ToolCallID: "call_sync"},
			ToolCallPart{ToolName: "async_task", ArgsJSON: `{}`, ToolCallID: "call_async"},
		),
	)

	agent := NewAgent[string](model,
		WithTools[string](deferTool, normalTool),
	)

	_, err := agent.Run(context.Background(), "do both")

	// Should still get ErrDeferred because at least one tool deferred.
	var deferredErr *ErrDeferred[string]
	if !errors.As(err, &deferredErr) {
		t.Fatalf("expected ErrDeferred, got %v", err)
	}

	if len(deferredErr.Result.DeferredRequests) != 1 {
		t.Fatalf("expected 1 deferred request, got %d", len(deferredErr.Result.DeferredRequests))
	}

	if deferredErr.Result.DeferredRequests[0].ToolName != "async_task" {
		t.Errorf("expected async_task to be deferred, got %q", deferredErr.Result.DeferredRequests[0].ToolName)
	}
}

func TestDeferredTool_ErrorResult(t *testing.T) {
	// Resume with an error result -- the model should get a RetryPromptPart.
	model := NewTestModel(TextResponse("handled error"))

	agent := NewAgent[string](model)

	result, err := agent.Run(context.Background(), "handle the error",
		WithMessages(
			ModelRequest{
				Parts:     []ModelRequestPart{UserPromptPart{Content: "original prompt"}},
				Timestamp: time.Now(),
			},
			ModelResponse{
				Parts: []ModelResponsePart{
					ToolCallPart{ToolName: "async_task", ArgsJSON: `{}`, ToolCallID: "call_err"},
				},
				FinishReason: FinishReasonToolCall,
				Timestamp:    time.Now(),
			},
		),
		WithDeferredResults(DeferredToolResult{
			ToolCallID: "call_err",
			Content:    "external service failed",
			IsError:    true,
		}),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Output != "handled error" {
		t.Errorf("Output = %q, want %q", result.Output, "handled error")
	}

	// Verify the model received a RetryPromptPart from the deferred error.
	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}
	lastCall := calls[len(calls)-1]
	foundRetry := false
	for _, msg := range lastCall.Messages {
		if req, ok := msg.(ModelRequest); ok {
			for _, part := range req.Parts {
				if rp, ok := part.(RetryPromptPart); ok {
					if rp.Content == "external service failed" {
						foundRetry = true
					}
				}
			}
		}
	}
	if !foundRetry {
		t.Error("expected RetryPromptPart with deferred error content in model messages")
	}
}

func TestCallDeferred_Error(t *testing.T) {
	err := &CallDeferred{Message: "human approval needed"}
	if err.Error() != "deferred: human approval needed" {
		t.Errorf("Error() = %q, want %q", err.Error(), "deferred: human approval needed")
	}
}

func TestErrDeferred_Error(t *testing.T) {
	err := &ErrDeferred[string]{
		Result: RunResultDeferred[string]{
			DeferredRequests: []DeferredToolRequest{
				{ToolName: "a"},
				{ToolName: "b"},
			},
		},
	}
	expected := "agent run deferred: 2 tool call(s) require external resolution"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}

// Regression: DeferredToolResult was missing ToolName, causing ToolReturnPart
// to have an empty ToolName when resuming with deferred results.
func TestDeferredTool_ToolNamePreserved(t *testing.T) {
	model := NewTestModel(TextResponse("resumed"))

	agent := NewAgent[string](model)

	_, err := agent.Run(context.Background(), "resume",
		WithMessages(
			ModelRequest{
				Parts:     []ModelRequestPart{UserPromptPart{Content: "original"}},
				Timestamp: time.Now(),
			},
			ModelResponse{
				Parts: []ModelResponsePart{
					ToolCallPart{ToolName: "my_tool", ArgsJSON: `{}`, ToolCallID: "call_42"},
				},
				FinishReason: FinishReasonToolCall,
				Timestamp:    time.Now(),
			},
		),
		WithDeferredResults(DeferredToolResult{
			ToolName:   "my_tool",
			ToolCallID: "call_42",
			Content:    "result data",
		}),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify the model received a ToolReturnPart with the correct ToolName.
	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}
	found := false
	for _, msg := range calls[0].Messages {
		if req, ok := msg.(ModelRequest); ok {
			for _, part := range req.Parts {
				if trp, ok := part.(ToolReturnPart); ok && trp.ToolCallID == "call_42" {
					if trp.ToolName != "my_tool" {
						t.Errorf("ToolReturnPart.ToolName = %q, want %q", trp.ToolName, "my_tool")
					}
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("expected ToolReturnPart with ToolCallID 'call_42' in model messages")
	}
}

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

// TestIterBasicText verifies that Iter returns a text response and completes.
func TestIterBasicText(t *testing.T) {
	model := NewTestModel(TextResponse("Hello"))
	agent := NewAgent[string](model)

	iter := agent.Iter(context.Background(), "Say hello")
	if iter.Done() {
		t.Fatal("expected iter to not be done initially")
	}

	resp, err := iter.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.TextContent() != "Hello" {
		t.Errorf("expected 'Hello', got %q", resp.TextContent())
	}
	if !iter.Done() {
		t.Error("expected iter to be done after text response")
	}

	// Next call should return EOF.
	_, err = iter.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}

	result, err := iter.Result()
	if err != nil {
		t.Fatalf("unexpected result error: %v", err)
	}
	if result.Output != "Hello" {
		t.Errorf("expected output 'Hello', got %q", result.Output)
	}
}

// TestIterWithToolCall verifies that Iter handles tool calls step by step.
func TestIterWithToolCall(t *testing.T) {
	type Params struct {
		X int `json:"x"`
	}
	addTool := FuncTool[Params]("double", "doubles x", func(ctx context.Context, p Params) (string, error) {
		return fmt.Sprintf("%d", p.X*2), nil
	})

	model := NewTestModel(
		ToolCallResponse("double", `{"x":5}`),
		TextResponse("10"),
	)
	agent := NewAgent[string](model, WithTools[string](addTool))

	iter := agent.Iter(context.Background(), "Double 5")

	// Step 1: model returns tool call, agent executes it.
	resp1, err := iter.Next()
	if err != nil {
		t.Fatalf("step 1 error: %v", err)
	}
	if len(resp1.ToolCalls()) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp1.ToolCalls()))
	}
	if resp1.ToolCalls()[0].ToolName != "double" {
		t.Errorf("expected tool 'double', got %q", resp1.ToolCalls()[0].ToolName)
	}
	if iter.Done() {
		t.Error("expected iter to not be done after tool call")
	}

	// Messages should include tool result after step 1.
	msgs := iter.Messages()
	if len(msgs) < 3 {
		t.Fatalf("expected at least 3 messages (request, tool call response, tool result), got %d", len(msgs))
	}

	// Step 2: model returns text output.
	resp2, err := iter.Next()
	if err != nil {
		t.Fatalf("step 2 error: %v", err)
	}
	if resp2.TextContent() != "10" {
		t.Errorf("expected '10', got %q", resp2.TextContent())
	}
	if !iter.Done() {
		t.Error("expected iter to be done after text response")
	}

	result, err := iter.Result()
	if err != nil {
		t.Fatalf("unexpected result error: %v", err)
	}
	if result.Output != "10" {
		t.Errorf("expected '10', got %q", result.Output)
	}
}

// TestIterContextCancellation verifies that Iter respects context cancellation.
func TestIterContextCancellation(t *testing.T) {
	// Model returns tool call first, then we cancel during second Next.
	type Params struct {
		N int `json:"n"`
	}
	slowTool := FuncTool[Params]("slow", "slow", func(ctx context.Context, p Params) (string, error) {
		return "done", nil
	})

	model := NewTestModel(
		ToolCallResponse("slow", `{"n":1}`),
		TextResponse("result"),
	)
	agent := NewAgent[string](model, WithTools[string](slowTool))

	ctx, cancel := context.WithCancel(context.Background())
	iter := agent.Iter(ctx, "test")

	// Step 1 completes normally.
	_, err := iter.Next()
	if err != nil {
		t.Fatalf("step 1 error: %v", err)
	}

	// Cancel context before step 2.
	cancel()

	_, err = iter.Next()
	if err == nil {
		t.Error("expected error after context cancellation")
	}
	if !iter.Done() {
		t.Error("expected iter to be done after cancellation")
	}
}

// TestIterMessagesGrow verifies that message history grows with each step.
func TestIterMessagesGrow(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}
	echoTool := FuncTool[Params]("echo", "echo", func(ctx context.Context, p Params) (string, error) {
		return fmt.Sprintf("echo_%d", p.N), nil
	})

	model := NewTestModel(
		ToolCallResponse("echo", `{"n":1}`),
		ToolCallResponse("echo", `{"n":2}`),
		TextResponse("done"),
	)
	agent := NewAgent[string](model, WithTools[string](echoTool))

	iter := agent.Iter(context.Background(), "echo twice")

	initialCount := len(iter.Messages())
	if initialCount == 0 {
		t.Fatal("expected at least one initial message")
	}

	// Step 1.
	iter.Next()
	afterStep1 := len(iter.Messages())
	if afterStep1 <= initialCount {
		t.Errorf("messages should grow after step 1: initial=%d after=%d", initialCount, afterStep1)
	}

	// Step 2.
	iter.Next()
	afterStep2 := len(iter.Messages())
	if afterStep2 <= afterStep1 {
		t.Errorf("messages should grow after step 2: step1=%d step2=%d", afterStep1, afterStep2)
	}

	// Step 3 (final text).
	iter.Next()
	afterStep3 := len(iter.Messages())
	if afterStep3 <= afterStep2 {
		t.Errorf("messages should grow after step 3: step2=%d step3=%d", afterStep2, afterStep3)
	}
}

// TestIterResultBeforeDone verifies that Result returns error before iteration completes.
func TestIterResultBeforeDone(t *testing.T) {
	model := NewTestModel(TextResponse("Hello"))
	agent := NewAgent[string](model)

	iter := agent.Iter(context.Background(), "test")
	_, err := iter.Result()
	if err == nil {
		t.Error("expected error when calling Result before iteration completes")
	}
}

// TestIterMultipleToolCalls verifies Iter with concurrent tool calls.
func TestIterMultipleToolCalls(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}
	addTool := FuncTool[Params]("add", "adds 10", func(ctx context.Context, p Params) (string, error) {
		return fmt.Sprintf("%d", p.N+10), nil
	})

	model := NewTestModel(
		MultiToolCallResponse(
			ToolCallPart{ToolName: "add", ArgsJSON: `{"n":1}`, ToolCallID: "call_1"},
			ToolCallPart{ToolName: "add", ArgsJSON: `{"n":2}`, ToolCallID: "call_2"},
		),
		TextResponse("done"),
	)
	agent := NewAgent[string](model, WithTools[string](addTool))

	iter := agent.Iter(context.Background(), "add both")

	// Step 1: concurrent tool calls execute.
	resp, err := iter.Next()
	if err != nil {
		t.Fatalf("step 1 error: %v", err)
	}
	if len(resp.ToolCalls()) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls()))
	}

	// Step 2: final text.
	resp2, err := iter.Next()
	if err != nil {
		t.Fatalf("step 2 error: %v", err)
	}
	if resp2.TextContent() != "done" {
		t.Errorf("expected 'done', got %q", resp2.TextContent())
	}

	result, err := iter.Result()
	if err != nil {
		t.Fatalf("unexpected result error: %v", err)
	}
	if result.Output != "done" {
		t.Errorf("expected 'done', got %q", result.Output)
	}
}

// TestIterMaxRunDuration verifies that MaxRunDuration works correctly with Iter.
// Before the fix, Iter's RunContext was missing RunStartTime, causing
// time.Since(zero) to return ~50 years and immediately trigger MaxRunDuration.
func TestIterMaxRunDuration(t *testing.T) {
	model := NewTestModel(
		ToolCallResponse("echo", `{"n":1}`),
		TextResponse("done"),
	)
	type Params struct {
		N int `json:"n"`
	}
	echoTool := FuncTool[Params]("echo", "echo", func(ctx context.Context, p Params) (string, error) {
		return fmt.Sprintf("echo_%d", p.N), nil
	})

	agent := NewAgent[string](model,
		WithTools[string](echoTool),
		WithRunCondition[string](MaxRunDuration(5*time.Second)),
	)

	iter := agent.Iter(context.Background(), "echo once")

	// Step 1: tool call — should NOT trigger MaxRunDuration (run just started).
	resp1, err := iter.Next()
	if err != nil {
		t.Fatalf("step 1 error: %v", err)
	}
	if len(resp1.ToolCalls()) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp1.ToolCalls()))
	}
	if iter.Done() {
		t.Error("expected iter to not be done after tool call — MaxRunDuration should not trigger immediately")
	}

	// Step 2: text response — should complete normally.
	resp2, err := iter.Next()
	if err != nil {
		t.Fatalf("step 2 error: %v", err)
	}
	if resp2.TextContent() != "done" {
		t.Errorf("expected 'done', got %q", resp2.TextContent())
	}
	if !iter.Done() {
		t.Error("expected iter to be done after text response")
	}
}

// TestIterWithGuardrails verifies that input guardrails work with Iter.
func TestIterWithGuardrails(t *testing.T) {
	model := NewTestModel(TextResponse("should not reach"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("block-test", func(ctx context.Context, prompt string) (string, error) {
			if prompt == "blocked" {
				return "", fmt.Errorf("input blocked")
			}
			return prompt, nil
		}),
	)

	iter := agent.Iter(context.Background(), "blocked")
	if !iter.Done() {
		t.Error("expected iter to be done immediately when guardrail blocks")
	}

	_, err := iter.Next()
	if err == nil {
		t.Error("expected error from guardrail")
	}

	_, err = iter.Result()
	if err == nil {
		t.Error("expected error from Result when guardrail blocked")
	}
}

// TestIterGlobalRetryResetsOnToolSuccess verifies that the global result-retry
// counter resets after successful tool execution in Iter mode.
func TestIterGlobalRetryResetsOnToolSuccess(t *testing.T) {
	type Params struct {
		Q string `json:"q"`
	}
	searchTool := FuncTool[Params]("search", "search", func(_ context.Context, p Params) (string, error) {
		return "found: " + p.Q, nil
	})

	model := NewTestModel(
		&ModelResponse{Parts: []ModelResponsePart{}, FinishReason: FinishReasonStop}, // empty → retry
		ToolCallResponse("search", `{"q":"first"}`),                                  // success → reset
		&ModelResponse{Parts: []ModelResponsePart{}, FinishReason: FinishReasonStop}, // empty → retry (should be 1, not 2)
		ToolCallResponse("search", `{"q":"second"}`),                                 // success → reset
		TextResponse("done"),
	)

	agent := NewAgent[string](model,
		WithTools[string](searchTool),
		WithMaxRetries[string](1),
	)

	iter := agent.Iter(context.Background(), "test retries via iter")

	// Iterate until done.
	steps := 0
	for !iter.Done() {
		_, err := iter.Next()
		if err != nil {
			t.Fatalf("step %d error (retries may not be resetting in iter): %v", steps, err)
		}
		steps++
	}

	result, err := iter.Result()
	if err != nil {
		t.Fatalf("result error: %v", err)
	}
	if result.Output != "done" {
		t.Errorf("output = %q, want 'done'", result.Output)
	}
}

// TestIterDeferredMixedToolResults verifies that when the Iter API encounters
// mixed deferred and non-deferred tool calls, the non-deferred tool results are
// preserved in the message history. This is the Iter equivalent of the bug fixed
// in runLoop (agent.go) — without the fix, non-deferred tool results are lost
// when ErrDeferred is returned, causing API 400 errors on resume.
func TestIterDeferredMixedToolResults(t *testing.T) {
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
			return "sync result", nil
		},
	}

	model := NewTestModel(
		MultiToolCallResponse(
			ToolCallPart{ToolName: "sync_task", ArgsJSON: `{}`, ToolCallID: "call_sync"},
			ToolCallPart{ToolName: "async_task", ArgsJSON: `{}`, ToolCallID: "call_async"},
		),
	)

	agent := NewAgent[string](model, WithTools[string](deferTool, normalTool))
	iter := agent.Iter(context.Background(), "do both")

	// Step 1: model calls both tools, one defers.
	_, err := iter.Next()

	var deferredErr *ErrDeferred[string]
	if !errors.As(err, &deferredErr) {
		t.Fatalf("expected ErrDeferred, got %v", err)
	}
	if !iter.Done() {
		t.Error("expected iter to be done after deferred")
	}

	// Verify that the non-deferred tool result (sync_task) is preserved in Messages.
	foundSync := false
	for _, msg := range deferredErr.Result.Messages {
		if req, ok := msg.(ModelRequest); ok {
			for _, part := range req.Parts {
				if trp, ok := part.(ToolReturnPart); ok && trp.ToolCallID == "call_sync" {
					foundSync = true
				}
			}
		}
	}
	if !foundSync {
		t.Error("missing tool result for sync_task (call_sync) in Iter messages — non-deferred tool results were lost")
	}

	// Resume with the deferred result and verify both results are visible.
	model2 := NewTestModel(TextResponse("all done"))
	agent2 := NewAgent[string](model2, WithTools[string](deferTool, normalTool))
	result, err := agent2.Run(context.Background(), "do both",
		WithMessages(deferredErr.Result.Messages...),
		WithDeferredResults(DeferredToolResult{
			ToolName:   "async_task",
			ToolCallID: "call_async",
			Content:    "async result",
		}),
	)
	if err != nil {
		t.Fatalf("resume Run: %v", err)
	}
	if result.Output != "all done" {
		t.Errorf("Output = %q, want %q", result.Output, "all done")
	}

	// Verify the resumed model saw both tool results.
	calls := model2.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}
	foundSync = false
	foundAsync := false
	for _, msg := range calls[0].Messages {
		if req, ok := msg.(ModelRequest); ok {
			for _, part := range req.Parts {
				if trp, ok := part.(ToolReturnPart); ok {
					if trp.ToolCallID == "call_sync" {
						foundSync = true
					}
					if trp.ToolCallID == "call_async" {
						foundAsync = true
					}
				}
			}
		}
	}
	if !foundSync {
		t.Error("resumed model missing sync_task result — non-deferred tool results lost in Iter path")
	}
	if !foundAsync {
		t.Error("resumed model missing async_task result — deferred result not injected")
	}
}

// TestIterWithDeferredResultsResume verifies that the Iter API correctly handles
// WithDeferredResults for resuming after a deferred tool call. Before the fix,
// Iter silently ignored WithDeferredResults, dropping the deferred results.
func TestIterWithDeferredResultsResume(t *testing.T) {
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

	// Step 1: Run via Iter, get deferred.
	model1 := NewTestModel(
		ToolCallResponseWithID("async_task", `{}`, "call_1"),
	)
	agent1 := NewAgent[string](model1, WithTools[string](deferTool))
	iter1 := agent1.Iter(context.Background(), "do async")
	_, err := iter1.Next()
	var deferredErr *ErrDeferred[string]
	if !errors.As(err, &deferredErr) {
		t.Fatalf("expected ErrDeferred, got %v", err)
	}

	// Step 2: Resume via Iter with WithDeferredResults.
	model2 := NewTestModel(TextResponse("resumed ok"))
	agent2 := NewAgent[string](model2, WithTools[string](deferTool))
	iter2 := agent2.Iter(context.Background(), "do async",
		WithMessages(deferredErr.Result.Messages...),
		WithDeferredResults(DeferredToolResult{
			ToolName:   "async_task",
			ToolCallID: "call_1",
			Content:    "async result",
		}),
	)

	// The Iter should resume and produce a result.
	resp, err := iter2.Next()
	if err != nil {
		t.Fatalf("resume Iter Next: %v", err)
	}
	if resp.TextContent() != "resumed ok" {
		t.Errorf("expected 'resumed ok', got %q", resp.TextContent())
	}

	result, err := iter2.Result()
	if err != nil {
		t.Fatalf("resume Iter Result: %v", err)
	}
	if result.Output != "resumed ok" {
		t.Errorf("Output = %q, want %q", result.Output, "resumed ok")
	}

	// Verify the deferred result was actually sent to the model.
	calls := model2.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}
	foundDeferred := false
	for _, msg := range calls[0].Messages {
		if req, ok := msg.(ModelRequest); ok {
			for _, part := range req.Parts {
				if trp, ok := part.(ToolReturnPart); ok && trp.ToolCallID == "call_1" {
					foundDeferred = true
				}
			}
		}
	}
	if !foundDeferred {
		t.Error("deferred result (call_1) not found in model messages — Iter ignores WithDeferredResults")
	}
}

func TestIterHooksAndToolStateParity(t *testing.T) {
	stateful := &testSnapshotStatefulTool{}
	type Params struct{}
	counter := FuncTool[Params]("counter", "counter", func(_ context.Context, _ Params) (string, error) {
		stateful.count++
		return fmt.Sprintf("%d", stateful.count), nil
	})
	counter.Stateful = stateful

	var turnEnds int
	agent := NewAgent[string](
		NewTestModel(
			ToolCallResponse("counter", `{}`),
			TextResponse("done"),
		),
		WithTools[string](counter),
		WithHooks[string](Hook{
			OnTurnEnd: func(_ context.Context, _ *RunContext, _ int, _ *ModelResponse) {
				turnEnds++
			},
		}),
	)

	iter := agent.Iter(context.Background(), "increment")
	if _, err := iter.Next(); err != nil {
		t.Fatalf("step 1 error: %v", err)
	}
	if _, err := iter.Next(); err != nil {
		t.Fatalf("step 2 error: %v", err)
	}

	result, err := iter.Result()
	if err != nil {
		t.Fatal(err)
	}

	if turnEnds != 2 {
		t.Errorf("expected 2 OnTurnEnd hooks, got %d", turnEnds)
	}
	if got := snapshotCountFromAny(result.ToolState["counter"].(map[string]any)["count"]); got != 1 {
		t.Errorf("expected ToolState counter=1, got %d", got)
	}
}

func TestIterRunConditionHookParity(t *testing.T) {
	var condStopped bool
	var condReason string

	agent := NewAgent[string](
		NewTestModel(TextResponse("hello")),
		WithRunCondition[string](TextContains("hello")),
		WithHooks[string](Hook{
			OnRunConditionChecked: func(_ context.Context, _ *RunContext, stopped bool, reason string) {
				condStopped = stopped
				condReason = reason
			},
		}),
	)

	iter := agent.Iter(context.Background(), "say hello")
	resp, err := iter.Next()
	if err != nil {
		t.Fatal(err)
	}
	if resp.TextContent() != "hello" {
		t.Fatalf("expected hello response, got %q", resp.TextContent())
	}

	result, err := iter.Result()
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "hello" {
		t.Errorf("expected output hello, got %q", result.Output)
	}
	if !condStopped {
		t.Error("expected OnRunConditionChecked to report stop=true")
	}
	if condReason == "" {
		t.Error("expected non-empty run condition reason")
	}
}

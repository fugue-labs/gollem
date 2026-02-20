package core

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// --- Test: Dynamic System Prompts ---

func TestDynamicSystemPrompt(t *testing.T) {
	model := NewTestModel(TextResponse("Hello!"))
	agent := NewAgent[string](model,
		WithDynamicSystemPrompt[string](func(_ context.Context, _ *RunContext) (string, error) {
			return "You are a dynamic assistant.", nil
		}),
	)

	result, err := agent.Run(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Hello!" {
		t.Errorf("output = %q, want 'Hello!'", result.Output)
	}

	// Verify the model received the dynamic system prompt.
	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	req, ok := calls[0].Messages[0].(ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	foundDynamic := false
	for _, p := range req.Parts {
		if sp, ok := p.(SystemPromptPart); ok && sp.Content == "You are a dynamic assistant." {
			foundDynamic = true
		}
	}
	if !foundDynamic {
		t.Error("expected dynamic system prompt in request")
	}
}

func TestDynamicSystemPromptWithDeps(t *testing.T) {
	type Deps struct {
		UserName string
	}

	model := NewTestModel(TextResponse("Hi user!"))
	agent := NewAgent[string](model,
		WithDynamicSystemPrompt[string](func(_ context.Context, rc *RunContext) (string, error) {
			deps := rc.Deps.(*Deps)
			return fmt.Sprintf("The user's name is %s.", deps.UserName), nil
		}),
	)

	result, err := agent.Run(context.Background(), "Who am I?",
		WithRunDeps(&Deps{UserName: "Alice"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Hi user!" {
		t.Errorf("output = %q", result.Output)
	}

	calls := model.Calls()
	req, _ := calls[0].Messages[0].(ModelRequest)
	found := false
	for _, p := range req.Parts {
		if sp, ok := p.(SystemPromptPart); ok && sp.Content == "The user's name is Alice." {
			found = true
		}
	}
	if !found {
		t.Error("expected dynamic prompt with deps")
	}
}

func TestMultipleDynamicPrompts(t *testing.T) {
	model := NewTestModel(TextResponse("OK"))
	agent := NewAgent[string](model,
		WithSystemPrompt[string]("Static prompt."),
		WithDynamicSystemPrompt[string](func(_ context.Context, _ *RunContext) (string, error) {
			return "Dynamic 1.", nil
		}),
		WithDynamicSystemPrompt[string](func(_ context.Context, _ *RunContext) (string, error) {
			return "Dynamic 2.", nil
		}),
	)

	_, err := agent.Run(context.Background(), "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := model.Calls()
	req, _ := calls[0].Messages[0].(ModelRequest)
	var systemPrompts []string
	for _, p := range req.Parts {
		if sp, ok := p.(SystemPromptPart); ok {
			systemPrompts = append(systemPrompts, sp.Content)
		}
	}
	if len(systemPrompts) != 3 {
		t.Fatalf("expected 3 system prompts, got %d: %v", len(systemPrompts), systemPrompts)
	}
	if systemPrompts[0] != "Static prompt." {
		t.Errorf("first prompt = %q", systemPrompts[0])
	}
	if systemPrompts[1] != "Dynamic 1." {
		t.Errorf("second prompt = %q", systemPrompts[1])
	}
	if systemPrompts[2] != "Dynamic 2." {
		t.Errorf("third prompt = %q", systemPrompts[2])
	}
}

// --- Test: History Processors ---

func TestHistoryProcessor(t *testing.T) {
	model := NewTestModel(
		ToolCallResponse("search", `{"q":"test"}`),
		TextResponse("Found it."),
	)

	type Params struct {
		Q string `json:"q"`
	}

	var processedCount int
	searchTool := FuncTool[Params]("search", "Search",
		func(_ context.Context, _ Params) (string, error) {
			return "result", nil
		},
	)

	agent := NewAgent[string](model,
		WithTools[string](searchTool),
		WithHistoryProcessor[string](func(_ context.Context, messages []ModelMessage) ([]ModelMessage, error) {
			processedCount++
			return messages, nil
		}),
	)

	_, err := agent.Run(context.Background(), "Search")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Processor should be called before each model request (2 requests = 2 calls).
	if processedCount != 2 {
		t.Errorf("processor called %d times, want 2", processedCount)
	}
}

func TestHistoryProcessorChain(t *testing.T) {
	model := NewTestModel(TextResponse("OK"))

	var order []string
	agent := NewAgent[string](model,
		WithHistoryProcessor[string](func(_ context.Context, msgs []ModelMessage) ([]ModelMessage, error) {
			order = append(order, "proc1")
			return msgs, nil
		}),
		WithHistoryProcessor[string](func(_ context.Context, msgs []ModelMessage) ([]ModelMessage, error) {
			order = append(order, "proc2")
			return msgs, nil
		}),
	)

	_, err := agent.Run(context.Background(), "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 || order[0] != "proc1" || order[1] != "proc2" {
		t.Errorf("processors ran in wrong order: %v", order)
	}
}

func TestHistoryProcessorTrimming(t *testing.T) {
	model := NewTestModel(
		ToolCallResponse("action", `{}`),
		TextResponse("Done"),
	)

	type Params struct{}
	actionTool := FuncTool[Params]("action", "Action",
		func(_ context.Context, _ Params) (string, error) {
			return "ok", nil
		},
	)

	// Processor that limits to last 2 messages.
	agent := NewAgent[string](model,
		WithTools[string](actionTool),
		WithHistoryProcessor[string](func(_ context.Context, msgs []ModelMessage) ([]ModelMessage, error) {
			if len(msgs) > 2 {
				return msgs[len(msgs)-2:], nil
			}
			return msgs, nil
		}),
	)

	_, err := agent.Run(context.Background(), "Do it")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The second model request should have received trimmed history.
	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	// Second call should have at most 2 messages.
	if len(calls[1].Messages) > 2 {
		t.Errorf("expected at most 2 messages after trimming, got %d", len(calls[1].Messages))
	}
}

// --- Test: Tool Approval ---

func TestToolApproval_Approved(t *testing.T) {
	model := NewTestModel(
		ToolCallResponse("dangerous_action", `{"target":"prod"}`),
		TextResponse("Action completed."),
	)

	type Params struct {
		Target string `json:"target"`
	}

	var executed bool
	tool := FuncTool[Params]("dangerous_action", "Dangerous action",
		func(_ context.Context, _ Params) (string, error) {
			executed = true
			return "done", nil
		},
		WithRequiresApproval(),
	)

	agent := NewAgent[string](model,
		WithTools[string](tool),
		WithToolApproval[string](func(_ context.Context, _ string, _ string) (bool, error) {
			return true, nil
		}),
	)

	result, err := agent.Run(context.Background(), "Do dangerous thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !executed {
		t.Error("expected tool to be executed after approval")
	}
	if result.Output != "Action completed." {
		t.Errorf("output = %q", result.Output)
	}
}

func TestToolApproval_Denied(t *testing.T) {
	model := NewTestModel(
		ToolCallResponse("dangerous_action", `{"target":"prod"}`),
		TextResponse("OK, I won't do that."),
	)

	type Params struct {
		Target string `json:"target"`
	}

	var executed bool
	tool := FuncTool[Params]("dangerous_action", "Dangerous action",
		func(_ context.Context, _ Params) (string, error) {
			executed = true
			return "done", nil
		},
		WithRequiresApproval(),
	)

	agent := NewAgent[string](model,
		WithTools[string](tool),
		WithMaxRetries[string](3),
		WithToolApproval[string](func(_ context.Context, _ string, _ string) (bool, error) {
			return false, nil
		}),
	)

	result, err := agent.Run(context.Background(), "Do dangerous thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executed {
		t.Error("expected tool NOT to be executed after denial")
	}
	if result.Output != "OK, I won't do that." {
		t.Errorf("output = %q", result.Output)
	}
}

func TestToolApproval_NoApprovalFunc(t *testing.T) {
	model := NewTestModel(
		ToolCallResponse("dangerous_action", `{}`),
		TextResponse("Fallback."),
	)

	type Params struct{}
	tool := FuncTool[Params]("dangerous_action", "Dangerous action",
		func(_ context.Context, _ Params) (string, error) {
			return "done", nil
		},
		WithRequiresApproval(),
	)

	agent := NewAgent[string](model,
		WithTools[string](tool),
		WithMaxRetries[string](3),
		// No WithToolApproval set.
	)

	result, err := agent.Run(context.Background(), "Do dangerous thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should fall back to text since tool was denied.
	if result.Output != "Fallback." {
		t.Errorf("output = %q", result.Output)
	}
}

// --- Test: Agent Iteration ---

func TestAgentIter_TextResponse(t *testing.T) {
	model := NewTestModel(TextResponse("Hello from iter!"))
	agent := NewAgent[string](model)

	run := agent.Iter(context.Background(), "Hi")

	resp, err := run.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TextContent() != "Hello from iter!" {
		t.Errorf("response text = %q", resp.TextContent())
	}

	// Should be done.
	if !run.Done() {
		t.Error("expected run to be done")
	}

	result, err := run.Result()
	if err != nil {
		t.Fatalf("unexpected error getting result: %v", err)
	}
	if result.Output != "Hello from iter!" {
		t.Errorf("output = %q", result.Output)
	}
}

func TestAgentIter_ToolCalls(t *testing.T) {
	model := NewTestModel(
		ToolCallResponse("search", `{"q":"test"}`),
		TextResponse("Found it."),
	)

	type Params struct {
		Q string `json:"q"`
	}

	searchTool := FuncTool[Params]("search", "Search",
		func(_ context.Context, _ Params) (string, error) {
			return "result", nil
		},
	)

	agent := NewAgent[string](model, WithTools[string](searchTool))
	run := agent.Iter(context.Background(), "Search")

	// First step: tool call.
	resp1, err := run.Next()
	if err != nil {
		t.Fatalf("step 1 error: %v", err)
	}
	if len(resp1.ToolCalls()) != 1 {
		t.Errorf("step 1: expected 1 tool call, got %d", len(resp1.ToolCalls()))
	}
	if run.Done() {
		t.Error("expected run to NOT be done after tool call")
	}

	// Second step: text response.
	resp2, err := run.Next()
	if err != nil {
		t.Fatalf("step 2 error: %v", err)
	}
	if resp2.TextContent() != "Found it." {
		t.Errorf("step 2 text = %q", resp2.TextContent())
	}
	if !run.Done() {
		t.Error("expected run to be done")
	}

	result, err := run.Result()
	if err != nil {
		t.Fatalf("result error: %v", err)
	}
	if result.Output != "Found it." {
		t.Errorf("output = %q", result.Output)
	}
}

func TestAgentIter_EarlyTermination(t *testing.T) {
	model := NewTestModel(
		ToolCallResponse("search", `{"q":"test"}`),
		TextResponse("This should not be reached."),
	)

	type Params struct {
		Q string `json:"q"`
	}

	searchTool := FuncTool[Params]("search", "Search",
		func(_ context.Context, _ Params) (string, error) {
			return "result", nil
		},
	)

	agent := NewAgent[string](model, WithTools[string](searchTool))
	run := agent.Iter(context.Background(), "Search")

	// Only do first step, then abandon.
	_, err := run.Next()
	if err != nil {
		t.Fatalf("step 1 error: %v", err)
	}

	// Should have messages from the first step.
	msgs := run.Messages()
	if len(msgs) < 2 {
		t.Errorf("expected at least 2 messages, got %d", len(msgs))
	}
}

// --- Test: Tool Call Limits ---

func TestToolCallLimit(t *testing.T) {
	// Model calls a tool repeatedly.
	model := NewTestModel(
		ToolCallResponse("action", `{}`),
	)

	type Params struct{}
	actionTool := FuncTool[Params]("action", "Action",
		func(_ context.Context, _ Params) (string, error) {
			return "ok", nil
		},
	)

	limit := 2
	agent := NewAgent[string](model,
		WithTools[string](actionTool),
		WithUsageLimits[string](UsageLimits{
			RequestLimit:   IntPtr(50),
			ToolCallsLimit: &limit,
		}),
	)

	_, err := agent.Run(context.Background(), "Do things")
	if err == nil {
		t.Fatal("expected error for tool call limit exceeded")
	}
	var limitErr *UsageLimitExceeded
	if !errors.As(err, &limitErr) {
		t.Errorf("expected UsageLimitExceeded, got %T: %v", err, err)
	}
}

// --- Test: Max Concurrency ---

func TestMaxConcurrency(t *testing.T) {
	model := NewTestModel(
		MultiToolCallResponse(
			ToolCallPart{ToolName: "slow", ArgsJSON: `{"id":"1"}`, ToolCallID: "c1"},
			ToolCallPart{ToolName: "slow", ArgsJSON: `{"id":"2"}`, ToolCallID: "c2"},
			ToolCallPart{ToolName: "slow", ArgsJSON: `{"id":"3"}`, ToolCallID: "c3"},
			ToolCallPart{ToolName: "slow", ArgsJSON: `{"id":"4"}`, ToolCallID: "c4"},
		),
		TextResponse("All done."),
	)

	type Params struct {
		ID string `json:"id"`
	}

	var maxConcurrent atomic.Int32
	var currentConcurrent atomic.Int32

	slowTool := FuncTool[Params]("slow", "Slow tool",
		func(_ context.Context, _ Params) (string, error) {
			current := currentConcurrent.Add(1)
			// Track the maximum concurrent executions.
			for {
				old := maxConcurrent.Load()
				if current <= old || maxConcurrent.CompareAndSwap(old, current) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			currentConcurrent.Add(-1)
			return "done", nil
		},
	)

	agent := NewAgent[string](model,
		WithTools[string](slowTool),
		WithMaxConcurrency[string](2),
	)

	result, err := agent.Run(context.Background(), "Run all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "All done." {
		t.Errorf("output = %q", result.Output)
	}

	// Max concurrent should be at most 2.
	if maxConcurrent.Load() > 2 {
		t.Errorf("max concurrent = %d, want at most 2", maxConcurrent.Load())
	}
}

// --- Test: Toolsets ---

func TestToolset(t *testing.T) {
	model := NewTestModel(
		ToolCallResponse("math_add", `{"a":1,"b":2}`),
		TextResponse("Result is 3."),
	)

	type AddParams struct {
		A int `json:"a"`
		B int `json:"b"`
	}

	addTool := FuncTool[AddParams]("math_add", "Add numbers",
		func(_ context.Context, p AddParams) (string, error) {
			return fmt.Sprintf("%d", p.A+p.B), nil
		},
	)

	mathSet := NewToolset("math", addTool)
	agent := NewAgent[string](model,
		WithToolsets[string](mathSet),
	)

	result, err := agent.Run(context.Background(), "Add 1 and 2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Result is 3." {
		t.Errorf("output = %q", result.Output)
	}
}

func TestMultipleToolsets(t *testing.T) {
	model := NewTestModel(
		ToolCallResponse("tool_a", `{}`),
		ToolCallResponse("tool_b", `{}`),
		TextResponse("Both used."),
	)

	type Params struct{}

	var aCalled, bCalled bool

	toolA := FuncTool[Params]("tool_a", "Tool A",
		func(_ context.Context, _ Params) (string, error) {
			aCalled = true
			return "a", nil
		},
	)
	toolB := FuncTool[Params]("tool_b", "Tool B",
		func(_ context.Context, _ Params) (string, error) {
			bCalled = true
			return "b", nil
		},
	)

	setA := NewToolset("group_a", toolA)
	setB := NewToolset("group_b", toolB)

	agent := NewAgent[string](model,
		WithToolsets[string](setA, setB),
	)

	result, err := agent.Run(context.Background(), "Use both")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !aCalled || !bCalled {
		t.Errorf("aCalled=%v, bCalled=%v, want both true", aCalled, bCalled)
	}
	if result.Output != "Both used." {
		t.Errorf("output = %q", result.Output)
	}
}

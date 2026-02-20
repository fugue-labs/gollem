package core

import (
	"context"
	"errors"
	"testing"
)

func TestRunCondition_Or(t *testing.T) {
	never := func(_ context.Context, _ *RunContext, _ *ModelResponse) (bool, string) {
		return false, ""
	}
	always := func(_ context.Context, _ *RunContext, _ *ModelResponse) (bool, string) {
		return true, "always"
	}

	combined := Or(never, always)
	stop, reason := combined(context.Background(), nil, &ModelResponse{})
	if !stop {
		t.Error("expected Or to stop when one condition is true")
	}
	if reason != "always" {
		t.Errorf("expected reason 'always', got %q", reason)
	}

	noneTrue := Or(never, never)
	stop, _ = noneTrue(context.Background(), nil, &ModelResponse{})
	if stop {
		t.Error("expected Or(never, never) to not stop")
	}
}

func TestRunCondition_And(t *testing.T) {
	always1 := func(_ context.Context, _ *RunContext, _ *ModelResponse) (bool, string) {
		return true, "a"
	}
	always2 := func(_ context.Context, _ *RunContext, _ *ModelResponse) (bool, string) {
		return true, "b"
	}
	never := func(_ context.Context, _ *RunContext, _ *ModelResponse) (bool, string) {
		return false, ""
	}

	both := And(always1, always2)
	stop, reason := both(context.Background(), nil, &ModelResponse{})
	if !stop {
		t.Error("expected And(true, true) to stop")
	}
	if reason != "a; b" {
		t.Errorf("expected reason 'a; b', got %q", reason)
	}

	mixed := And(always1, never)
	stop, _ = mixed(context.Background(), nil, &ModelResponse{})
	if stop {
		t.Error("expected And(true, false) to not stop")
	}
}

func TestMaxRunDuration(t *testing.T) {
	// Use a very short duration so the condition triggers immediately on second check.
	cond := MaxRunDuration(0)

	stop, _ := cond(context.Background(), nil, &ModelResponse{})
	if !stop {
		t.Error("expected MaxRunDuration(0) to trigger immediately")
	}
}

func TestTextContains(t *testing.T) {
	cond := TextContains("DONE")

	resp := &ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "result is DONE"}}}
	stop, _ := cond(context.Background(), nil, resp)
	if !stop {
		t.Error("expected TextContains to match")
	}

	resp2 := &ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "not finished"}}}
	stop2, _ := cond(context.Background(), nil, resp2)
	if stop2 {
		t.Error("expected TextContains to not match")
	}
}

func TestToolCallCount(t *testing.T) {
	cond := ToolCallCount(3)

	rc := &RunContext{Usage: RunUsage{ToolCalls: 2}}
	stop, _ := cond(context.Background(), rc, &ModelResponse{})
	if stop {
		t.Error("expected ToolCallCount(3) to not trigger at 2 calls")
	}

	rc.Usage.ToolCalls = 3
	stop, _ = cond(context.Background(), rc, &ModelResponse{})
	if !stop {
		t.Error("expected ToolCallCount(3) to trigger at 3 calls")
	}
}

func TestRunCondition_NoneTriggered(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}
	tool := FuncTool[Params]("echo", "echo", func(ctx context.Context, p Params) (string, error) {
		return "echoed", nil
	})

	model := NewTestModel(
		ToolCallResponse("echo", `{"n":1}`),
		TextResponse("final"),
	)

	// Add a condition that never triggers.
	agent := NewAgent[string](model,
		WithTools[string](tool),
		WithRunCondition[string](func(_ context.Context, _ *RunContext, _ *ModelResponse) (bool, string) {
			return false, ""
		}),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "final" {
		t.Errorf("expected 'final', got %q", result.Output)
	}
}

func TestResponseContains(t *testing.T) {
	cond := ResponseContains(func(resp *ModelResponse) bool {
		return len(resp.ToolCalls()) > 0
	})

	respWithTool := &ModelResponse{Parts: []ModelResponsePart{
		ToolCallPart{ToolName: "test", ArgsJSON: "{}"},
	}}
	stop, _ := cond(context.Background(), nil, respWithTool)
	if !stop {
		t.Error("expected ResponseContains to match tool call response")
	}

	respText := &ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "text"}}}
	stop2, _ := cond(context.Background(), nil, respText)
	if stop2 {
		t.Error("expected ResponseContains to not match text-only response")
	}
}

func TestRunCondition_StopsRun(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}
	tool := FuncTool[Params]("echo", "echo", func(ctx context.Context, p Params) (string, error) {
		return "echoed", nil
	})

	model := NewTestModel(
		ToolCallResponse("echo", `{"n":1}`),
		ToolCallResponse("echo", `{"n":2}`),
		ToolCallResponse("echo", `{"n":3}`),
		TextResponse("final"),
	)

	// Stop after 1 tool call.
	agent := NewAgent[string](model,
		WithTools[string](tool),
		WithRunCondition[string](ToolCallCount(1)),
	)

	_, err := agent.Run(context.Background(), "test")
	var condErr *RunConditionError
	if !errors.As(err, &condErr) {
		t.Fatalf("expected RunConditionError, got %v", err)
	}
}

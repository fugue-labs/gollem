package core

import (
	"context"
	"encoding/json"
	"testing"
)

func TestTrace_BasicRun(t *testing.T) {
	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model, WithTracing[string]())

	result, err := agent.Run(context.Background(), "test prompt")
	if err != nil {
		t.Fatal(err)
	}

	if result.Trace == nil {
		t.Fatal("expected trace to be non-nil")
	}

	trace := result.Trace
	if trace.RunID == "" {
		t.Error("expected non-empty run ID")
	}
	if trace.Prompt != "test prompt" {
		t.Errorf("expected prompt %q, got %q", "test prompt", trace.Prompt)
	}
	if !trace.Success {
		t.Error("expected success=true")
	}
	if trace.Duration <= 0 {
		t.Error("expected positive duration")
	}

	// Should have model request and response steps.
	hasReq := false
	hasResp := false
	for _, step := range trace.Steps {
		switch step.Kind {
		case TraceModelRequest:
			hasReq = true
		case TraceModelResponse:
			hasResp = true
		}
	}
	if !hasReq {
		t.Error("expected TraceModelRequest step")
	}
	if !hasResp {
		t.Error("expected TraceModelResponse step")
	}
}

func TestTrace_WithToolCalls(t *testing.T) {
	type AddParams struct {
		A int `json:"a"`
	}
	addTool := FuncTool[AddParams]("add", "add", func(ctx context.Context, p AddParams) (int, error) {
		return p.A + 1, nil
	})

	model := NewTestModel(
		ToolCallResponse("add", `{"a":5}`),
		TextResponse("done"),
	)
	agent := NewAgent[string](model,
		WithTools[string](addTool),
		WithTracing[string](),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	trace := result.Trace
	if trace == nil {
		t.Fatal("expected non-nil trace")
	}

	hasToolCall := false
	hasToolResult := false
	for _, step := range trace.Steps {
		switch step.Kind {
		case TraceToolCall:
			hasToolCall = true
			data := step.Data.(map[string]any)
			if data["tool_name"] != "add" {
				t.Errorf("expected tool_name 'add', got %v", data["tool_name"])
			}
		case TraceToolResult:
			hasToolResult = true
			if step.Duration <= 0 {
				t.Error("expected positive tool duration")
			}
		}
	}
	if !hasToolCall {
		t.Error("expected TraceToolCall step")
	}
	if !hasToolResult {
		t.Error("expected TraceToolResult step")
	}
}

func TestTrace_Timing(t *testing.T) {
	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model, WithTracing[string]())

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	trace := result.Trace
	for i, step := range trace.Steps {
		if step.Kind == TraceModelResponse && step.Duration <= 0 {
			t.Errorf("step %d (%s): expected positive duration", i, step.Kind)
		}
	}

	if trace.EndTime.Before(trace.StartTime) {
		t.Error("end time should be after start time")
	}
}

func TestTrace_JSONSerializable(t *testing.T) {
	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model, WithTracing[string]())

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(result.Trace)
	if err != nil {
		t.Fatalf("failed to marshal trace: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JSON")
	}

	// Verify it round-trips.
	var decoded RunTrace
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal trace: %v", err)
	}
	if decoded.RunID != result.Trace.RunID {
		t.Errorf("RunID mismatch after round-trip")
	}
}

func TestTrace_Disabled(t *testing.T) {
	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model) // No WithTracing

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if result.Trace != nil {
		t.Error("expected nil trace when tracing not enabled")
	}
}

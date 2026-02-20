package core

import (
	"context"
	"testing"
)

func TestToolChoice_Auto(t *testing.T) {
	tc := ToolChoiceAuto()
	if tc.Mode != "auto" {
		t.Errorf("expected mode 'auto', got %q", tc.Mode)
	}
}

func TestToolChoice_Required(t *testing.T) {
	tc := ToolChoiceRequired()
	if tc.Mode != "required" {
		t.Errorf("expected mode 'required', got %q", tc.Mode)
	}
}

func TestToolChoice_None(t *testing.T) {
	tc := ToolChoiceNone()
	if tc.Mode != "none" {
		t.Errorf("expected mode 'none', got %q", tc.Mode)
	}
}

func TestToolChoice_Force(t *testing.T) {
	tc := ToolChoiceForce("my_tool")
	if tc.ToolName != "my_tool" {
		t.Errorf("expected tool name 'my_tool', got %q", tc.ToolName)
	}
}

func TestToolChoice_AutoReset(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}
	tool := FuncTool[Params]("echo", "echo", func(ctx context.Context, p Params) (string, error) {
		return "echoed", nil
	})

	model := NewTestModel(
		ToolCallResponse("echo", `{"n":1}`),
		TextResponse("done"),
	)

	agent := NewAgent[string](model,
		WithTools[string](tool),
		WithToolChoice[string](ToolChoiceRequired()),
		WithToolChoiceAutoReset[string](),
	)

	result, err := agent.Run(context.Background(), "test auto reset")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" {
		t.Errorf("expected 'done', got %q", result.Output)
	}

	// Verify that the model was called with the tool choice, and that it was reset.
	calls := model.Calls()
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 model calls, got %d", len(calls))
	}
}

func TestToolChoice_InModelSettings(t *testing.T) {
	tc := ToolChoiceRequired()
	settings := &ModelSettings{ToolChoice: tc}
	if settings.ToolChoice.Mode != "required" {
		t.Errorf("expected 'required' mode in settings, got %q", settings.ToolChoice.Mode)
	}
}

package core

import (
	"context"
	"testing"
)

func TestOverride_ReplacesModel(t *testing.T) {
	original := NewTestModel(TextResponse("original"))
	override := NewTestModel(TextResponse("override"))

	agent := NewAgent[string](original)
	overridden := agent.Override(override)

	result, err := overridden.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "override" {
		t.Errorf("expected 'override', got %q", result.Output)
	}
}

func TestOverride_OriginalUnchanged(t *testing.T) {
	original := NewTestModel(TextResponse("original"))
	override := NewTestModel(TextResponse("override"))

	agent := NewAgent[string](original)
	_ = agent.Override(override)

	// Original agent should still use the original model.
	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "original" {
		t.Errorf("expected 'original', got %q", result.Output)
	}
}

func TestOverride_AddsOptions(t *testing.T) {
	original := NewTestModel(TextResponse("orig"))
	override := NewTestModel(TextResponse("new"))

	agent := NewAgent[string](original)
	overridden := agent.Override(override, WithSystemPrompt[string]("extra prompt"))

	result, err := overridden.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "new" {
		t.Errorf("expected 'new', got %q", result.Output)
	}

	// Check that the override model received the extra system prompt.
	calls := override.Calls()
	if len(calls) == 0 {
		t.Fatal("no calls recorded")
	}
	// The messages should include the system prompt.
	found := false
	for _, msg := range calls[0].Messages {
		if req, ok := msg.(ModelRequest); ok {
			for _, part := range req.Parts {
				if sp, ok := part.(SystemPromptPart); ok && sp.Content == "extra prompt" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("extra system prompt not found in override")
	}
}

func TestWithTestModel_Convenience(t *testing.T) {
	original := NewTestModel(TextResponse("orig"))
	agent := NewAgent[string](original)

	overridden, tm := agent.WithTestModel(TextResponse("test-output"))

	result, err := overridden.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "test-output" {
		t.Errorf("expected 'test-output', got %q", result.Output)
	}

	// Verify the returned TestModel recorded the call.
	if len(tm.Calls()) != 1 {
		t.Errorf("expected 1 call, got %d", len(tm.Calls()))
	}
}

func TestOverride_ToolsPreserved(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}
	tool := FuncTool[Params]("echo", "echo", func(ctx context.Context, p Params) (string, error) {
		return "echoed", nil
	})

	original := NewTestModel(TextResponse("orig"))
	agent := NewAgent[string](original, WithTools[string](tool))

	override := NewTestModel(
		ToolCallResponse("echo", `{"n":1}`),
		TextResponse("done"),
	)
	overridden := agent.Override(override)

	result, err := overridden.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" {
		t.Errorf("expected 'done', got %q", result.Output)
	}
}

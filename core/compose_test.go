package core

import (
	"context"
	"testing"
)

func TestClone_IndependentCopy(t *testing.T) {
	model := NewTestModel(TextResponse("hello"))
	original := NewAgent[string](model,
		WithSystemPrompt[string]("original prompt"),
	)

	cloned := original.Clone()

	// Modify clone's system prompts — original should be unaffected.
	cloned.systemPrompts = append(cloned.systemPrompts, "added to clone")

	if len(original.systemPrompts) != 1 {
		t.Errorf("original should still have 1 system prompt, got %d", len(original.systemPrompts))
	}
	if len(cloned.systemPrompts) != 2 {
		t.Errorf("clone should have 2 system prompts, got %d", len(cloned.systemPrompts))
	}
}

func TestClone_WithOverrides(t *testing.T) {
	type AddParams struct {
		A int `json:"a"`
	}
	addTool := FuncTool[AddParams]("add", "add", func(ctx context.Context, p AddParams) (int, error) {
		return p.A, nil
	})
	mulTool := FuncTool[AddParams]("mul", "mul", func(ctx context.Context, p AddParams) (int, error) {
		return p.A * 2, nil
	})

	model := NewTestModel(TextResponse("hello"))
	original := NewAgent[string](model,
		WithTools[string](addTool),
		WithSystemPrompt[string]("original"),
	)

	cloned := original.Clone(
		WithTools[string](mulTool),
		WithSystemPrompt[string]("cloned"),
	)

	if len(original.tools) != 1 {
		t.Errorf("original should have 1 tool, got %d", len(original.tools))
	}
	if len(cloned.tools) != 2 {
		t.Errorf("clone should have 2 tools, got %d", len(cloned.tools))
	}
	if len(original.systemPrompts) != 1 {
		t.Errorf("original should have 1 prompt, got %d", len(original.systemPrompts))
	}
	if len(cloned.systemPrompts) != 2 {
		t.Errorf("clone should have 2 prompts, got %d", len(cloned.systemPrompts))
	}
}

func TestClone_RunsBoth(t *testing.T) {
	model1 := NewTestModel(TextResponse("from original"))
	model2 := NewTestModel(TextResponse("from clone"))

	original := NewAgent[string](model1)
	cloned := original.Clone()
	// Override the model on the clone by setting a new model.
	cloned.model = model2

	result1, err := original.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	result2, err := cloned.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if result1.Output != "from original" {
		t.Errorf("original: expected 'from original', got %q", result1.Output)
	}
	if result2.Output != "from clone" {
		t.Errorf("clone: expected 'from clone', got %q", result2.Output)
	}
}


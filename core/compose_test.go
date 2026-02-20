package core

import (
	"context"
	"fmt"
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

func TestChainRun(t *testing.T) {
	// First agent produces a number as text.
	firstModel := NewTestModel(TextResponse("42"))
	first := NewAgent[string](firstModel)

	// Second agent produces a string.
	secondModel := NewTestModel(TextResponse("The answer is 42"))
	second := NewAgent[string](secondModel)

	result, err := ChainRun(context.Background(), first, second, "what is the answer?",
		func(intermediate string) string {
			return fmt.Sprintf("The first agent said: %s. Elaborate.", intermediate)
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Output != "The answer is 42" {
		t.Errorf("expected 'The answer is 42', got %q", result.Output)
	}

	// Second model should have received the transformed prompt.
	calls := secondModel.Calls()
	if len(calls) == 0 {
		t.Fatal("second model not called")
	}
	firstMsg := calls[0].Messages[0].(ModelRequest)
	for _, part := range firstMsg.Parts {
		if up, ok := part.(UserPromptPart); ok {
			if up.Content != "The first agent said: 42. Elaborate." {
				t.Errorf("unexpected second prompt: %q", up.Content)
			}
		}
	}
}

func TestChainRunFull(t *testing.T) {
	firstModel := NewTestModel(TextResponse("intermediate-result"))
	first := NewAgent[string](firstModel)

	secondModel := NewTestModel(TextResponse("final-result"))
	second := NewAgent[string](secondModel)

	result, err := ChainRunFull(context.Background(), first, second, "start",
		func(a string) string { return "got: " + a },
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Intermediate != "intermediate-result" {
		t.Errorf("expected intermediate 'intermediate-result', got %q", result.Intermediate)
	}
	if result.Final != "final-result" {
		t.Errorf("expected final 'final-result', got %q", result.Final)
	}
}

func TestChainRun_FirstFails(t *testing.T) {
	// First agent will fail (cancelled context).
	firstModel := NewTestModel(TextResponse("ok"))
	first := NewAgent[string](firstModel,
		WithInputGuardrail[string]("fail", func(ctx context.Context, prompt string) (string, error) {
			return "", fmt.Errorf("first agent failed")
		}),
	)

	secondModel := NewTestModel(TextResponse("ok"))
	second := NewAgent[string](secondModel)

	_, err := ChainRun(context.Background(), first, second, "test",
		func(a string) string { return a },
	)
	if err == nil {
		t.Fatal("expected error from first agent")
	}
}

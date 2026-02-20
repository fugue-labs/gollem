package orchestration_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/orchestration"
)

func TestChainRun(t *testing.T) {
	// First agent produces a number as text.
	firstModel := core.NewTestModel(core.TextResponse("42"))
	first := core.NewAgent[string](firstModel)

	// Second agent produces a string.
	secondModel := core.NewTestModel(core.TextResponse("The answer is 42"))
	second := core.NewAgent[string](secondModel)

	result, err := orchestration.ChainRun(context.Background(), first, second, "what is the answer?",
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
	firstMsg := calls[0].Messages[0].(core.ModelRequest)
	for _, part := range firstMsg.Parts {
		if up, ok := part.(core.UserPromptPart); ok {
			if up.Content != "The first agent said: 42. Elaborate." {
				t.Errorf("unexpected second prompt: %q", up.Content)
			}
		}
	}
}

func TestChainRunFull(t *testing.T) {
	firstModel := core.NewTestModel(core.TextResponse("intermediate-result"))
	first := core.NewAgent[string](firstModel)

	secondModel := core.NewTestModel(core.TextResponse("final-result"))
	second := core.NewAgent[string](secondModel)

	result, err := orchestration.ChainRunFull(context.Background(), first, second, "start",
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
	firstModel := core.NewTestModel(core.TextResponse("ok"))
	first := core.NewAgent[string](firstModel,
		core.WithInputGuardrail[string]("fail", func(ctx context.Context, prompt string) (string, error) {
			return "", fmt.Errorf("first agent failed")
		}),
	)

	secondModel := core.NewTestModel(core.TextResponse("ok"))
	second := core.NewAgent[string](secondModel)

	_, err := orchestration.ChainRun(context.Background(), first, second, "test",
		func(a string) string { return a },
	)
	if err == nil {
		t.Fatal("expected error from first agent")
	}
}

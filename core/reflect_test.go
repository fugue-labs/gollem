package core

import (
	"context"
	"testing"
)

func TestRunWithReflection_AcceptedFirst(t *testing.T) {
	model := NewTestModel(TextResponse("correct answer"))
	agent := NewAgent[string](model)

	result, err := RunWithReflection(context.Background(), agent, "test prompt",
		func(_ context.Context, output string) (string, error) {
			return "", nil // Accept immediately
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Accepted {
		t.Error("expected output to be accepted")
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if result.Output != "correct answer" {
		t.Errorf("expected 'correct answer', got %q", result.Output)
	}
}

func TestRunWithReflection_CorrectedOnSecond(t *testing.T) {
	model := NewTestModel(
		TextResponse("wrong answer"),
		TextResponse("correct answer"),
	)
	agent := NewAgent[string](model)

	callCount := 0
	result, err := RunWithReflection(context.Background(), agent, "test prompt",
		func(_ context.Context, output string) (string, error) {
			callCount++
			if output == "wrong answer" {
				return "The answer should be 'correct answer'", nil
			}
			return "", nil // Accept
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Accepted {
		t.Error("expected output to be accepted")
	}
	if result.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", result.Iterations)
	}
	if result.Output != "correct answer" {
		t.Errorf("expected 'correct answer', got %q", result.Output)
	}
}

func TestRunWithReflection_MaxReflections(t *testing.T) {
	// Model always gives wrong answer
	model := NewTestModel(
		TextResponse("wrong 1"),
		TextResponse("wrong 2"),
		TextResponse("wrong 3"),
		TextResponse("wrong 4"),
	)
	agent := NewAgent[string](model)

	result, err := RunWithReflection(context.Background(), agent, "test prompt",
		func(_ context.Context, _ string) (string, error) {
			return "still wrong", nil // Never accept
		},
		WithMaxReflections(2),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Accepted {
		t.Error("expected output to NOT be accepted after max reflections")
	}
	if result.Iterations != 3 { // initial + 2 reflections
		t.Errorf("expected 3 iterations, got %d", result.Iterations)
	}
}

func TestRunWithReflection_UsageAggregation(t *testing.T) {
	model := NewTestModel(
		TextResponse("first"),
		TextResponse("second"),
	)
	agent := NewAgent[string](model)

	callCount := 0
	result, err := RunWithReflection(context.Background(), agent, "test prompt",
		func(_ context.Context, _ string) (string, error) {
			callCount++
			if callCount < 2 {
				return "try again", nil
			}
			return "", nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Usage.Requests != 2 {
		t.Errorf("expected 2 requests in usage, got %d", result.Usage.Requests)
	}
}

func TestRunWithReflection_CustomPrompt(t *testing.T) {
	model := NewTestModel(
		TextResponse("first"),
		TextResponse("second"),
	)
	agent := NewAgent[string](model)

	callCount := 0
	_, err := RunWithReflection(context.Background(), agent, "test prompt",
		func(_ context.Context, _ string) (string, error) {
			callCount++
			if callCount < 2 {
				return "feedback", nil
			}
			return "", nil
		},
		WithReflectPrompt("CUSTOM: %s"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The test just verifies it doesn't panic with custom prompt
}

func TestRunWithReflection_StructuredOutput(t *testing.T) {
	model := NewTestModel(
		TextResponse("first attempt"),
		TextResponse("second attempt"),
	)
	agent := NewAgent[string](model)

	callCount := 0
	result, err := RunWithReflection(context.Background(), agent, "compute 2+2",
		func(_ context.Context, output string) (string, error) {
			callCount++
			if callCount < 2 {
				return "please correct", nil
			}
			return "", nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "second attempt" {
		t.Errorf("expected 'second attempt', got %q", result.Output)
	}
}

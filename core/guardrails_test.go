package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInputGuardrail_Rejects(t *testing.T) {
	guardrail := func(ctx context.Context, prompt string) (string, error) {
		return "", errors.New("rejected")
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("blocker", guardrail),
	)

	_, err := agent.Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}

	var gErr *GuardrailError
	if !errors.As(err, &gErr) {
		t.Fatalf("expected GuardrailError, got %T: %v", err, err)
	}
	if gErr.GuardrailName != "blocker" {
		t.Errorf("expected guardrail name %q, got %q", "blocker", gErr.GuardrailName)
	}
}

func TestInputGuardrail_Transforms(t *testing.T) {
	guardrail := func(ctx context.Context, prompt string) (string, error) {
		return strings.ToUpper(prompt), nil
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("upper", guardrail),
	)

	_, err := agent.Run(context.Background(), "test prompt")
	if err != nil {
		t.Fatal(err)
	}

	// Verify the model received the transformed prompt.
	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected model call")
	}
	firstMsg := calls[0].Messages[0].(ModelRequest)
	for _, part := range firstMsg.Parts {
		if up, ok := part.(UserPromptPart); ok {
			if up.Content != "TEST PROMPT" {
				t.Errorf("expected transformed prompt %q, got %q", "TEST PROMPT", up.Content)
			}
		}
	}
}

func TestInputGuardrail_Passes(t *testing.T) {
	guardrail := func(ctx context.Context, prompt string) (string, error) {
		return prompt, nil
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("pass", guardrail),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "hello" {
		t.Errorf("expected %q, got %q", "hello", result.Output)
	}
}

func TestTurnGuardrail_Aborts(t *testing.T) {
	callCount := 0
	guardrail := func(ctx context.Context, rc *RunContext, messages []ModelMessage) error {
		callCount++
		if callCount > 1 {
			return errors.New("too many turns")
		}
		return nil
	}

	type AddParams struct {
		A int `json:"a"`
	}
	addTool := FuncTool[AddParams]("add", "add", func(ctx context.Context, p AddParams) (int, error) {
		return p.A, nil
	})

	model := NewTestModel(
		ToolCallResponse("add", `{"a":1}`),
		ToolCallResponse("add", `{"a":2}`), // second turn - guardrail should abort
		TextResponse("done"),
	)
	agent := NewAgent[string](model,
		WithTools[string](addTool),
		WithTurnGuardrail[string]("limiter", guardrail),
	)

	_, err := agent.Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error from turn guardrail")
	}
	var gErr *GuardrailError
	if !errors.As(err, &gErr) {
		t.Fatalf("expected GuardrailError, got %T: %v", err, err)
	}
}

func TestMaxPromptLength(t *testing.T) {
	model := NewTestModel(TextResponse("ok"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("max-length", MaxPromptLength(10)),
	)

	// Short prompt should pass.
	_, err := agent.Run(context.Background(), "short")
	if err != nil {
		t.Fatal(err)
	}

	// Long prompt should be rejected.
	_, err = agent.Run(context.Background(), "this is a very long prompt that exceeds the limit")
	if err == nil {
		t.Fatal("expected error for long prompt")
	}
	var gErr *GuardrailError
	if !errors.As(err, &gErr) {
		t.Fatalf("expected GuardrailError, got %T: %v", err, err)
	}
}

func TestContentFilter(t *testing.T) {
	model := NewTestModel(TextResponse("ok"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("filter", ContentFilter("forbidden", "blocked")),
	)

	// Clean prompt should pass.
	model.Reset()
	_, err := agent.Run(context.Background(), "hello world")
	if err != nil {
		t.Fatal(err)
	}

	// Prompt with blocked content (case-insensitive).
	_, err = agent.Run(context.Background(), "this contains FORBIDDEN stuff")
	if err == nil {
		t.Fatal("expected error for filtered content")
	}
	var gErr *GuardrailError
	if !errors.As(err, &gErr) {
		t.Fatalf("expected GuardrailError, got %T: %v", err, err)
	}
}

// Regression: RunStream did not run input guardrails, allowing bypass.
func TestInputGuardrail_RunStream(t *testing.T) {
	guardrail := func(ctx context.Context, prompt string) (string, error) {
		return "", errors.New("rejected by guardrail")
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("blocker", guardrail),
	)

	_, err := agent.RunStream(context.Background(), "test")
	if err == nil {
		t.Fatal("expected RunStream to run input guardrails")
	}
	var gErr *GuardrailError
	if !errors.As(err, &gErr) {
		t.Fatalf("expected GuardrailError from RunStream, got %T: %v", err, err)
	}
}

// Regression: Iter did not run input guardrails, allowing bypass.
func TestInputGuardrail_Iter(t *testing.T) {
	guardrail := func(ctx context.Context, prompt string) (string, error) {
		return "", errors.New("rejected by guardrail")
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("blocker", guardrail),
	)

	run := agent.Iter(context.Background(), "test")
	_, err := run.Next()
	if err == nil {
		t.Fatal("expected Iter.Next() to return guardrail error")
	}
	var gErr *GuardrailError
	if !errors.As(err, &gErr) {
		t.Fatalf("expected GuardrailError from Iter.Next(), got %T: %v", err, err)
	}
}

func TestMaxTurns(t *testing.T) {
	type AddParams struct {
		A int `json:"a"`
	}
	addTool := FuncTool[AddParams]("add", "add", func(ctx context.Context, p AddParams) (int, error) {
		return p.A, nil
	})

	model := NewTestModel(
		ToolCallResponse("add", `{"a":1}`),
		ToolCallResponse("add", `{"a":2}`),
		ToolCallResponse("add", `{"a":3}`),
		TextResponse("done"),
	)
	agent := NewAgent[string](model,
		WithTools[string](addTool),
		WithTurnGuardrail[string]("max-turns", MaxTurns(2)),
	)

	_, err := agent.Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error from MaxTurns guardrail")
	}
	var gErr *GuardrailError
	if !errors.As(err, &gErr) {
		t.Fatalf("expected GuardrailError, got %T: %v", err, err)
	}
}

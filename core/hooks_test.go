package core

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestHook_OnRunStart(t *testing.T) {
	var gotPrompt string
	hook := Hook{
		OnRunStart: func(ctx context.Context, rc *RunContext, prompt string) {
			gotPrompt = prompt
		},
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model, WithHooks[string](hook))

	_, err := agent.Run(context.Background(), "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	if gotPrompt != "test prompt" {
		t.Errorf("expected prompt %q, got %q", "test prompt", gotPrompt)
	}
}

func TestHook_OnRunEnd(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var gotMessages []ModelMessage
		var gotErr error
		called := false

		hook := Hook{
			OnRunEnd: func(ctx context.Context, rc *RunContext, messages []ModelMessage, err error) {
				called = true
				gotMessages = messages
				gotErr = err
			},
		}

		model := NewTestModel(TextResponse("hello"))
		agent := NewAgent[string](model, WithHooks[string](hook))

		_, err := agent.Run(context.Background(), "test")
		if err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("OnRunEnd was not called")
		}
		if gotErr != nil {
			t.Errorf("expected nil error, got %v", gotErr)
		}
		if len(gotMessages) == 0 {
			t.Error("expected non-empty messages")
		}
	})

	t.Run("error", func(t *testing.T) {
		var gotErr error
		called := false

		hook := Hook{
			OnRunEnd: func(ctx context.Context, rc *RunContext, messages []ModelMessage, err error) {
				called = true
				gotErr = err
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		model := NewTestModel(TextResponse("hello"))
		agent := NewAgent[string](model, WithHooks[string](hook))

		_, _ = agent.Run(ctx, "test")
		if !called {
			t.Fatal("OnRunEnd was not called on error")
		}
		if gotErr == nil {
			t.Error("expected non-nil error")
		}
	})
}

func TestHook_OnModelRequest(t *testing.T) {
	var gotMessages []ModelMessage
	hook := Hook{
		OnModelRequest: func(ctx context.Context, rc *RunContext, messages []ModelMessage) {
			gotMessages = messages
		},
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model, WithHooks[string](hook))

	_, err := agent.Run(context.Background(), "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	if len(gotMessages) == 0 {
		t.Error("expected non-empty messages in OnModelRequest")
	}
}

func TestHook_OnModelResponse(t *testing.T) {
	var gotResponse *ModelResponse
	hook := Hook{
		OnModelResponse: func(ctx context.Context, rc *RunContext, response *ModelResponse) {
			gotResponse = response
		},
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model, WithHooks[string](hook))

	_, err := agent.Run(context.Background(), "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	if gotResponse == nil {
		t.Fatal("OnModelResponse was not called")
	}
	if gotResponse.TextContent() != "hello" {
		t.Errorf("expected text %q, got %q", "hello", gotResponse.TextContent())
	}
}

func TestHook_OnToolStartEnd(t *testing.T) {
	var startName, startArgs string
	var endName, endResult string
	var endErr error

	hook := Hook{
		OnToolStart: func(ctx context.Context, rc *RunContext, toolCallID string, toolName string, argsJSON string) {
			startName = toolName
			startArgs = argsJSON
		},
		OnToolEnd: func(ctx context.Context, rc *RunContext, toolCallID string, toolName string, result string, err error) {
			endName = toolName
			endResult = result
			endErr = err
		},
	}

	type AddParams struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	addTool := FuncTool[AddParams]("add", "add two numbers", func(ctx context.Context, p AddParams) (int, error) {
		return p.A + p.B, nil
	})

	argsJSON := `{"a":1,"b":2}`
	model := NewTestModel(
		ToolCallResponse("add", argsJSON),
		TextResponse("done"),
	)
	agent := NewAgent[string](model,
		WithTools[string](addTool),
		WithHooks[string](hook),
	)

	_, err := agent.Run(context.Background(), "add 1 and 2")
	if err != nil {
		t.Fatal(err)
	}

	if startName != "add" {
		t.Errorf("expected start tool name %q, got %q", "add", startName)
	}
	if startArgs != argsJSON {
		t.Errorf("expected start args %q, got %q", argsJSON, startArgs)
	}
	if endName != "add" {
		t.Errorf("expected end tool name %q, got %q", "add", endName)
	}
	// The result should be the serialized int 3.
	expectedResult, _ := json.Marshal(3)
	if endResult != string(expectedResult) {
		t.Errorf("expected end result %q, got %q", string(expectedResult), endResult)
	}
	if endErr != nil {
		t.Errorf("expected nil end error, got %v", endErr)
	}
}

func TestHook_MultipleHooks(t *testing.T) {
	var mu sync.Mutex
	var order []int

	hook1 := Hook{
		OnRunStart: func(ctx context.Context, rc *RunContext, prompt string) {
			mu.Lock()
			order = append(order, 1)
			mu.Unlock()
		},
	}
	hook2 := Hook{
		OnRunStart: func(ctx context.Context, rc *RunContext, prompt string) {
			mu.Lock()
			order = append(order, 2)
			mu.Unlock()
		},
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model, WithHooks[string](hook1, hook2))

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if len(order) != 2 {
		t.Fatalf("expected 2 hook calls, got %d", len(order))
	}
	if order[0] != 1 || order[1] != 2 {
		t.Errorf("expected order [1, 2], got %v", order)
	}
}

func TestHook_NilFields(t *testing.T) {
	// A hook with all nil fields should not panic.
	hook := Hook{}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model, WithHooks[string](hook))

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
}

func TestHook_OnTurnStartEnd(t *testing.T) {
	var turns []int
	var turnEndCount int

	hook := Hook{
		OnTurnStart: func(ctx context.Context, rc *RunContext, turnNumber int) {
			turns = append(turns, turnNumber)
		},
		OnTurnEnd: func(ctx context.Context, rc *RunContext, turnNumber int, resp *ModelResponse) {
			turnEndCount++
		},
	}

	type AddParams struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	addTool := FuncTool[AddParams]("add", "add two numbers", func(ctx context.Context, p AddParams) (int, error) {
		return p.A + p.B, nil
	})

	model := NewTestModel(
		ToolCallResponse("add", `{"a":1,"b":2}`),
		TextResponse("done"),
	)
	agent := NewAgent[string](model,
		WithTools[string](addTool),
		WithHooks[string](hook),
	)

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if len(turns) != 2 {
		t.Errorf("expected 2 turns, got %d", len(turns))
	}
	if turnEndCount != 2 {
		t.Errorf("expected 2 turn ends, got %d", turnEndCount)
	}
}

func TestHook_OnGuardrailEvaluated(t *testing.T) {
	var guardrailName string
	var guardrailPassed bool

	hook := Hook{
		OnGuardrailEvaluated: func(ctx context.Context, rc *RunContext, name string, passed bool, err error) {
			guardrailName = name
			guardrailPassed = passed
		},
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("length_check", MaxPromptLength(1000)),
		WithHooks[string](hook),
	)

	_, err := agent.Run(context.Background(), "short prompt")
	if err != nil {
		t.Fatal(err)
	}

	if guardrailName != "length_check" {
		t.Errorf("expected guardrail name 'length_check', got %q", guardrailName)
	}
	if !guardrailPassed {
		t.Error("expected guardrail to pass")
	}
}

func TestHook_OnGuardrailEvaluated_Failed(t *testing.T) {
	var guardrailPassed bool
	var guardrailErr error

	hook := Hook{
		OnGuardrailEvaluated: func(ctx context.Context, rc *RunContext, name string, passed bool, err error) {
			guardrailPassed = passed
			guardrailErr = err
		},
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("max_len", MaxPromptLength(5)),
		WithHooks[string](hook),
	)

	_, err := agent.Run(context.Background(), "this is too long")
	if err == nil {
		t.Fatal("expected error from guardrail")
	}

	if guardrailPassed {
		t.Error("expected guardrail to fail")
	}
	if guardrailErr == nil {
		t.Error("expected guardrail error")
	}
}

func TestHook_OnRunConditionChecked(t *testing.T) {
	var condStopped bool
	var condReason string

	hook := Hook{
		OnRunConditionChecked: func(ctx context.Context, rc *RunContext, stopped bool, reason string) {
			condStopped = stopped
			condReason = reason
		},
	}

	model := NewTestModel(TextResponse("hello"))
	agent := NewAgent[string](model,
		WithRunCondition[string](TextContains("hello")),
		WithHooks[string](hook),
	)

	_, err := agent.Run(context.Background(), "say hello")
	if err != nil {
		t.Fatal(err)
	}

	if !condStopped {
		t.Error("expected condition to have stopped")
	}
	if condReason == "" {
		t.Error("expected non-empty reason")
	}
}

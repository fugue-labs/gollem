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
		OnToolStart: func(ctx context.Context, rc *RunContext, toolName string, argsJSON string) {
			startName = toolName
			startArgs = argsJSON
		},
		OnToolEnd: func(ctx context.Context, rc *RunContext, toolName string, result string, err error) {
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

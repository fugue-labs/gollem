package temporal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestTemporalizeTool_Wrapping(t *testing.T) {
	type Params struct {
		Query string `json:"query"`
	}

	tool := core.FuncTool[Params]("search", "Search for things",
		func(_ context.Context, p Params) (string, error) {
			return "found: " + p.Query, nil
		},
	)

	tt := TemporalizeTool("my-agent", tool, DefaultActivityConfig())

	if tt.ActivityName != "agent__my-agent__tool__search" {
		t.Errorf("unexpected activity name: %s", tt.ActivityName)
	}

	// Execute the activity function.
	result, err := tt.ActivityFn(context.Background(), toolParams{
		ArgsJSON:   `{"query": "test"}`,
		ToolCallID: "tc1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != "return" {
		t.Errorf("expected kind 'return', got %q", result.Kind)
	}

	var value string
	if err := json.Unmarshal(result.Value, &value); err != nil {
		t.Fatalf("unmarshal result value: %v", err)
	}
	if value != "found: test" {
		t.Errorf("unexpected result: %q", value)
	}
}

func TestTemporalizeTool_RetryError(t *testing.T) {
	type Params struct{}

	tool := core.FuncTool[Params]("risky", "A risky tool",
		func(_ context.Context, _ Params) (string, error) {
			return "", core.NewModelRetryError("try again with different input")
		},
	)

	tt := TemporalizeTool("agent1", tool, DefaultActivityConfig())

	result, err := tt.ActivityFn(context.Background(), toolParams{
		ArgsJSON:   `{}`,
		ToolCallID: "tc1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != "retry" {
		t.Errorf("expected kind 'retry', got %q", result.Kind)
	}
	if result.Message != "try again with different input" {
		t.Errorf("unexpected message: %q", result.Message)
	}
}

func TestTemporalizeTool_Error(t *testing.T) {
	type Params struct{}

	tool := core.FuncTool[Params]("failing", "A failing tool",
		func(_ context.Context, _ Params) (string, error) {
			return "", context.DeadlineExceeded
		},
	)

	tt := TemporalizeTool("agent1", tool, DefaultActivityConfig())

	result, err := tt.ActivityFn(context.Background(), toolParams{
		ArgsJSON:   `{}`,
		ToolCallID: "tc1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != "error" {
		t.Errorf("expected kind 'error', got %q", result.Kind)
	}
}

func TestTemporalizeTools_Multiple(t *testing.T) {
	type Params struct{}

	tools := []core.Tool{
		core.FuncTool[Params]("tool1", "Tool 1",
			func(_ context.Context, _ Params) (string, error) { return "1", nil }),
		core.FuncTool[Params]("tool2", "Tool 2",
			func(_ context.Context, _ Params) (string, error) { return "2", nil }),
	}

	tts := TemporalizeTools("agent1", tools, DefaultActivityConfig())
	if len(tts) != 2 {
		t.Fatalf("expected 2 temporal tools, got %d", len(tts))
	}
	if tts[0].ActivityName != "agent__agent1__tool__tool1" {
		t.Errorf("unexpected name: %s", tts[0].ActivityName)
	}
	if tts[1].ActivityName != "agent__agent1__tool__tool2" {
		t.Errorf("unexpected name: %s", tts[1].ActivityName)
	}
}

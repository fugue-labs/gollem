package temporal

import (
	"context"
	"testing"
	"time"

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
	result, err := tt.ActivityFn(context.Background(), ToolActivityInput{
		ArgsJSON:   `{"query": "test"}`,
		ToolCallID: "tc1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != "return" {
		t.Errorf("expected kind 'return', got %q", result.Kind)
	}

	if result.Content != "found: test" {
		t.Errorf("unexpected result: %q", result.Content)
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

	result, err := tt.ActivityFn(context.Background(), ToolActivityInput{
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

	result, err := tt.ActivityFn(context.Background(), ToolActivityInput{
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

func TestTemporalizeTool_RunStateSnapshotParity(t *testing.T) {
	type Params struct{}

	start := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "snapshot prompt", Timestamp: start}},
			Timestamp: start,
		},
	}
	serialized, err := core.EncodeMessages(messages)
	if err != nil {
		t.Fatalf("encode messages: %v", err)
	}

	tool := core.FuncTool[Params]("snapshot_tool", "Inspect snapshot",
		func(_ context.Context, rc *core.RunContext, _ Params) (string, error) {
			snap := rc.RunStateSnapshot()
			if snap == nil {
				t.Fatal("expected run state snapshot in temporal tool context")
			}
			if snap.Prompt != "snapshot prompt" {
				t.Fatalf("unexpected prompt %q", snap.Prompt)
			}
			if snap.RunID != "run-123" {
				t.Fatalf("unexpected run id %q", snap.RunID)
			}
			if snap.RunStep != 4 {
				t.Fatalf("unexpected run step %d", snap.RunStep)
			}
			if snap.LastInputTokens != 11 {
				t.Fatalf("unexpected last input tokens %d", snap.LastInputTokens)
			}
			if snap.Retries != 2 {
				t.Fatalf("unexpected retries %d", snap.Retries)
			}
			if snap.ToolRetries["snapshot_tool"] != 1 {
				t.Fatalf("unexpected tool retry map %+v", snap.ToolRetries)
			}
			if !snap.RunStartTime.Equal(start) {
				t.Fatalf("unexpected run start %v", snap.RunStartTime)
			}
			if got := snap.ToolState["snapshot_tool"].(map[string]any)["count"].(int); got != 9 {
				t.Fatalf("unexpected tool state %+v", snap.ToolState)
			}
			if len(snap.Messages) != 1 {
				t.Fatalf("unexpected snapshot messages %+v", snap.Messages)
			}
			if snap.Timestamp.IsZero() {
				t.Fatal("expected snapshot timestamp to be set")
			}
			return "ok", nil
		},
	)

	tt := TemporalizeTool("snapshot-agent", tool, DefaultActivityConfig())
	result, err := tt.ActivityFn(context.Background(), ToolActivityInput{
		ArgsJSON:        `{}`,
		ToolCallID:      "tc1",
		Prompt:          "snapshot prompt",
		RunStep:         4,
		RunID:           "run-123",
		RunStartTime:    start,
		Usage:           core.RunUsage{Requests: 3, ToolCalls: 1},
		LastInputTokens: 11,
		Retries:         2,
		ToolRetries:     map[string]int{"snapshot_tool": 1},
		Retry:           1,
		MaxRetries:      3,
		Messages:        serialized,
		ToolState:       map[string]any{"snapshot_tool": map[string]any{"count": 9}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != "return" {
		t.Fatalf("expected return result, got %q", result.Kind)
	}
}

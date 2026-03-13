package core

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type testSnapshotStatefulTool struct {
	count int
}

func (t *testSnapshotStatefulTool) ExportState() (any, error) {
	return map[string]any{"count": t.count}, nil
}

func (t *testSnapshotStatefulTool) RestoreState(state any) error {
	switch s := state.(type) {
	case map[string]any:
		t.count = snapshotCountFromAny(s["count"])
	case map[string]int:
		t.count = s["count"]
	case int:
		t.count = s
	}
	return nil
}

func snapshotCountFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func TestSnapshot_CaptureAndRestore(t *testing.T) {
	startTime := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	rc := &RunContext{
		Usage:        RunUsage{Requests: 2, ToolCalls: 1},
		RunID:        "test-run",
		RunStep:      3,
		RunStartTime: startTime,
		Prompt:       "hello",
		Messages: []ModelMessage{
			ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "hello"}}},
		},
	}

	snap := Snapshot(rc)
	if snap.RunID != "test-run" {
		t.Errorf("expected run_id 'test-run', got %q", snap.RunID)
	}
	if snap.RunStep != 3 {
		t.Errorf("expected run_step 3, got %d", snap.RunStep)
	}
	if snap.Prompt != "hello" {
		t.Errorf("expected prompt 'hello', got %q", snap.Prompt)
	}
	if !snap.RunStartTime.Equal(startTime) {
		t.Errorf("expected run_start_time %v, got %v", startTime, snap.RunStartTime)
	}
	if len(snap.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(snap.Messages))
	}

	// Verify that modifying the original RunContext doesn't affect the snapshot.
	rc.RunStep = 99
	if snap.RunStep != 3 {
		t.Error("snapshot should be independent of RunContext")
	}
}

func TestSnapshot_Serialize(t *testing.T) {
	runStartTime := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	snap := &RunSnapshot{
		Messages: []ModelMessage{
			ModelRequest{
				Parts:     []ModelRequestPart{UserPromptPart{Content: "test", Timestamp: time.Now()}},
				Timestamp: time.Now(),
			},
		},
		Usage:           RunUsage{Requests: 1},
		LastInputTokens: 42,
		Retries:         2,
		ToolRetries:     map[string]int{"echo": 1},
		RunID:           "snap-1",
		RunStep:         2,
		RunStartTime:    runStartTime,
		Prompt:          "test",
		ToolState:       map[string]any{"tool": map[string]any{"count": 3}},
		Timestamp:       time.Now(),
	}

	data, err := MarshalSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}

	restored, err := UnmarshalSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}

	if restored.RunID != snap.RunID {
		t.Errorf("expected RunID %q, got %q", snap.RunID, restored.RunID)
	}
	if restored.Prompt != snap.Prompt {
		t.Errorf("expected Prompt %q, got %q", snap.Prompt, restored.Prompt)
	}
	if restored.RunStep != snap.RunStep {
		t.Errorf("expected RunStep %d, got %d", snap.RunStep, restored.RunStep)
	}
	if restored.LastInputTokens != snap.LastInputTokens {
		t.Errorf("expected LastInputTokens %d, got %d", snap.LastInputTokens, restored.LastInputTokens)
	}
	if restored.Retries != snap.Retries {
		t.Errorf("expected Retries %d, got %d", snap.Retries, restored.Retries)
	}
	if got := restored.ToolRetries["echo"]; got != 1 {
		t.Errorf("expected ToolRetries echo=1, got %d", got)
	}
	if !restored.RunStartTime.Equal(runStartTime) {
		t.Errorf("expected RunStartTime %v, got %v", runStartTime, restored.RunStartTime)
	}
	if got := snapshotCountFromAny(restored.ToolState["tool"].(map[string]any)["count"]); got != 3 {
		t.Errorf("expected restored tool state count=3, got %d", got)
	}
}

func TestSnapshot_Branch(t *testing.T) {
	snap := &RunSnapshot{
		Messages: []ModelMessage{
			ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "original"}}},
		},
		RunID: "branch-test",
	}

	branched := snap.Branch(func(msgs []ModelMessage) []ModelMessage {
		// Add a new message.
		return append(msgs, ModelRequest{
			Parts: []ModelRequestPart{UserPromptPart{Content: "branched"}},
		})
	})

	// Original should be unchanged.
	if len(snap.Messages) != 1 {
		t.Errorf("original snapshot modified, has %d messages", len(snap.Messages))
	}
	// Branch should have 2 messages.
	if len(branched.Messages) != 2 {
		t.Errorf("expected 2 messages in branch, got %d", len(branched.Messages))
	}
	if branched.RunID != snap.RunID {
		t.Error("branch should preserve RunID")
	}
}

func TestSnapshot_WithSnapshotOption(t *testing.T) {
	// Create a snapshot with some messages.
	snap := &RunSnapshot{
		Messages: []ModelMessage{
			ModelRequest{
				Parts:     []ModelRequestPart{UserPromptPart{Content: "previous"}},
				Timestamp: time.Now(),
			},
			ModelResponse{
				Parts:     []ModelResponsePart{TextPart{Content: "prev response"}},
				Timestamp: time.Now(),
			},
		},
		RunID:  "snap-resume",
		Prompt: "test",
	}

	model := NewTestModel(TextResponse("resumed"))
	agent := NewAgent[string](model)

	result, err := agent.Run(context.Background(), "continue", WithSnapshot(snap))
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "resumed" {
		t.Errorf("expected 'resumed', got %q", result.Output)
	}

	// Model should have received the snapshot messages plus the new prompt.
	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	// Total messages = 2 from snapshot + 1 new request.
	if len(calls[0].Messages) != 3 {
		t.Errorf("expected 3 messages (2 snapshot + 1 new), got %d", len(calls[0].Messages))
	}
}

func TestSnapshot_WithSnapshotRestoresRunState(t *testing.T) {
	stateful := &testSnapshotStatefulTool{}
	type Params struct{}
	counter := FuncTool[Params]("counter", "counter", func(_ context.Context, rc *RunContext, _ Params) (string, error) {
		if got, ok := rc.ToolStateByName("counter"); !ok || snapshotCountFromAny(got.(map[string]any)["count"]) != 9 {
			t.Fatalf("expected restored tool state count=9, got %v", got)
		}
		stateful.count++
		return fmt.Sprintf("%d", stateful.count), nil
	})
	counter.Stateful = stateful

	var firstRunID string
	var firstRunStep int
	var firstUsage RunUsage
	var firstRunStart time.Time

	startTime := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Second)
	snap := &RunSnapshot{
		RunID:        "snap-resume",
		RunStep:      7,
		RunStartTime: startTime,
		Usage:        RunUsage{Requests: 3, ToolCalls: 1},
		ToolState:    map[string]any{"counter": map[string]any{"count": 9}},
	}

	model := NewTestModel(
		ToolCallResponse("counter", `{}`),
		TextResponse("done"),
	)
	agent := NewAgent[string](model,
		WithTools[string](counter),
		WithHooks[string](Hook{
			OnModelRequest: func(_ context.Context, rc *RunContext, _ []ModelMessage) {
				if firstRunID == "" {
					firstRunID = rc.RunID
					firstRunStep = rc.RunStep
					firstUsage = rc.Usage
					firstRunStart = rc.RunStartTime
				}
			},
		}),
	)

	result, err := agent.Run(context.Background(), "continue", WithSnapshot(snap))
	if err != nil {
		t.Fatal(err)
	}

	if firstRunID != "snap-resume" {
		t.Errorf("expected restored RunID snap-resume, got %q", firstRunID)
	}
	if firstRunStep != 8 {
		t.Errorf("expected restored RunStep to continue at 8, got %d", firstRunStep)
	}
	if firstUsage.Requests != 3 || firstUsage.ToolCalls != 1 {
		t.Errorf("expected restored usage {Requests:3 ToolCalls:1}, got %+v", firstUsage)
	}
	if !firstRunStart.Equal(startTime) {
		t.Errorf("expected restored RunStartTime %v, got %v", startTime, firstRunStart)
	}
	if result.RunID != "snap-resume" {
		t.Errorf("expected result RunID snap-resume, got %q", result.RunID)
	}
	if result.Usage.Requests != 5 {
		t.Errorf("expected total requests 5, got %d", result.Usage.Requests)
	}
	if got := snapshotCountFromAny(result.ToolState["counter"].(map[string]any)["count"]); got != 10 {
		t.Errorf("expected final tool state count=10, got %d", got)
	}
}

func TestSnapshot_WithSnapshotRetryMapRemainsWritable(t *testing.T) {
	type Params struct{}

	model := NewTestModel(
		ToolCallResponse("retry_tool", `{}`),
		TextResponse("done"),
	)
	tool := FuncTool[Params]("retry_tool", "retry", func(_ context.Context, _ Params) (string, error) {
		return "", NewModelRetryError("retry once")
	})

	agent := NewAgent[string](model, WithTools[string](tool))
	snap := &RunSnapshot{
		RunID:        "snap-retry",
		RunStartTime: time.Now().Add(-time.Minute).UTC().Truncate(time.Second),
		Prompt:       "resume",
	}

	result, err := agent.Run(context.Background(), "resume", WithSnapshot(snap))
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" {
		t.Fatalf("expected resumed output %q, got %q", "done", result.Output)
	}
}

func TestEncodeRunSnapshot_Nil(t *testing.T) {
	if _, err := EncodeRunSnapshot(nil); err == nil {
		t.Fatal("expected nil snapshot error")
	}
	if _, err := MarshalSnapshot(nil); err == nil {
		t.Fatal("expected MarshalSnapshot(nil) to fail")
	}
}

func TestSnapshot_FromHook(t *testing.T) {
	var captured *RunSnapshot

	model := NewTestModel(
		ToolCallResponse("echo", `{"n":1}`),
		TextResponse("done"),
	)

	type Params struct {
		N int `json:"n"`
	}
	tool := FuncTool[Params]("echo", "echo", func(ctx context.Context, p Params) (string, error) {
		return "echoed", nil
	})

	agent := NewAgent[string](model,
		WithTools[string](tool),
		WithHooks[string](Hook{
			OnModelResponse: func(ctx context.Context, rc *RunContext, resp *ModelResponse) {
				if captured == nil {
					captured = Snapshot(rc)
				}
			},
		}),
	)

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if captured == nil {
		t.Fatal("snapshot not captured from hook")
	}
	if captured.Prompt != "test" {
		t.Errorf("expected prompt 'test', got %q", captured.Prompt)
	}
}

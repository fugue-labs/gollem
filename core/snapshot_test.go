package core

import (
	"context"
	"testing"
	"time"
)

func TestSnapshot_CaptureAndRestore(t *testing.T) {
	rc := &RunContext{
		Usage:    RunUsage{Requests: 2, ToolCalls: 1},
		RunID:    "test-run",
		RunStep:  3,
		Prompt:   "hello",
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
	snap := &RunSnapshot{
		Messages: []ModelMessage{
			ModelRequest{
				Parts:     []ModelRequestPart{UserPromptPart{Content: "test", Timestamp: time.Now()}},
				Timestamp: time.Now(),
			},
		},
		Usage:     RunUsage{Requests: 1},
		RunID:     "snap-1",
		RunStep:   2,
		Prompt:    "test",
		Timestamp: time.Now(),
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

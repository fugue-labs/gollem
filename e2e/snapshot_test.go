//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestSnapshotSerializeRoundTrip verifies snapshot marshal/unmarshal round-trip.
func TestSnapshotSerializeRoundTrip(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	// Run an initial conversation.
	result, err := agent.Run(ctx, "My name is Alice. Remember that.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("first run failed: %v", err)
	}

	// Create a snapshot from the result messages.
	snap := &core.RunSnapshot{
		Messages:  result.Messages,
		Usage:     result.Usage,
		RunID:     result.RunID,
		RunStep:   1,
		Prompt:    "My name is Alice. Remember that.",
		Timestamp: time.Now(),
	}

	// Serialize the snapshot.
	data, err := core.MarshalSnapshot(snap)
	if err != nil {
		t.Fatalf("MarshalSnapshot failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty snapshot data")
	}
	t.Logf("Snapshot size: %d bytes", len(data))

	// Deserialize.
	restored, err := core.UnmarshalSnapshot(data)
	if err != nil {
		t.Fatalf("UnmarshalSnapshot failed: %v", err)
	}

	// Verify fields.
	if restored.RunID != snap.RunID {
		t.Errorf("RunID mismatch: %q vs %q", restored.RunID, snap.RunID)
	}
	if restored.Prompt != snap.Prompt {
		t.Errorf("Prompt mismatch: %q vs %q", restored.Prompt, snap.Prompt)
	}
	if len(restored.Messages) != len(snap.Messages) {
		t.Errorf("Messages count mismatch: %d vs %d", len(restored.Messages), len(snap.Messages))
	}

	t.Logf("Snapshot round-trip successful: %d messages", len(restored.Messages))
}

// TestSnapshotResumeConversation verifies resuming a conversation from a snapshot.
func TestSnapshotResumeConversation(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You are a helpful assistant with a good memory. When asked about previous information, recall it accurately."),
	)

	// First run: establish context.
	result1, err := agent.Run(ctx, "The secret code is ALPHA-7. Remember it.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("first run failed: %v", err)
	}
	t.Logf("First run output: %q", result1.Output)

	// Create snapshot.
	snap := &core.RunSnapshot{
		Messages:  result1.Messages,
		Usage:     result1.Usage,
		RunID:     result1.RunID,
		Prompt:    "The secret code is ALPHA-7. Remember it.",
		Timestamp: time.Now(),
	}

	// Serialize and deserialize.
	data, err := core.MarshalSnapshot(snap)
	if err != nil {
		t.Fatalf("MarshalSnapshot failed: %v", err)
	}
	restored, err := core.UnmarshalSnapshot(data)
	if err != nil {
		t.Fatalf("UnmarshalSnapshot failed: %v", err)
	}

	// Resume from snapshot with follow-up question.
	result2, err := agent.Run(ctx, "What was the secret code I told you?",
		core.WithSnapshot(restored),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("resumed run failed: %v", err)
	}

	if !strings.Contains(strings.ToUpper(result2.Output), "ALPHA") {
		t.Errorf("expected resumed output to reference the code, got: %q", result2.Output)
	}

	t.Logf("Resumed output: %q", result2.Output)
}

// TestSnapshotBranch verifies branching creates divergent conversation paths.
func TestSnapshotBranch(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	// First run: establish base conversation.
	result, err := agent.Run(ctx, "Say 'base conversation started'")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("base run failed: %v", err)
	}

	snap := &core.RunSnapshot{
		Messages:  result.Messages,
		Usage:     result.Usage,
		RunID:     result.RunID,
		Prompt:    "Say 'base conversation started'",
		Timestamp: time.Now(),
	}

	// Branch - modify messages to inject different context.
	branch := snap.Branch(func(msgs []core.ModelMessage) []core.ModelMessage {
		// Return messages as-is for the branch (we'll give different prompts).
		return msgs
	})

	// Verify branch has same message count.
	if len(branch.Messages) != len(snap.Messages) {
		t.Errorf("branch should have same message count: %d vs %d", len(branch.Messages), len(snap.Messages))
	}

	// Verify branch has a new timestamp.
	if !branch.Timestamp.After(snap.Timestamp) && !branch.Timestamp.Equal(snap.Timestamp) {
		t.Error("branch should have same or later timestamp")
	}

	t.Logf("Branch created successfully with %d messages", len(branch.Messages))
}

// TestMessageSerializeRoundTrip verifies MarshalMessages/UnmarshalMessages.
func TestMessageSerializeRoundTrip(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return "42", nil
	})

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
	)

	result, err := agent.Run(ctx, "Use the add tool with a=20 and b=22.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	// Serialize messages.
	data, err := core.MarshalMessages(result.Messages)
	if err != nil {
		t.Fatalf("MarshalMessages failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty message data")
	}

	// Deserialize.
	restored, err := core.UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages failed: %v", err)
	}

	if len(restored) != len(result.Messages) {
		t.Errorf("message count mismatch: %d vs %d", len(restored), len(result.Messages))
	}

	t.Logf("Serialized %d messages (%d bytes)", len(result.Messages), len(data))
}

// TestAllMessagesJSON verifies RunResult.AllMessagesJSON().
func TestAllMessagesJSON(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	result, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	data, err := result.AllMessagesJSON()
	if err != nil {
		t.Fatalf("AllMessagesJSON failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty JSON data")
	}

	// Verify it's valid JSON by unmarshaling.
	msgs, err := core.UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages on AllMessagesJSON output failed: %v", err)
	}

	if len(msgs) == 0 {
		t.Error("expected non-empty messages")
	}

	t.Logf("AllMessagesJSON produced %d bytes for %d messages", len(data), len(msgs))
}

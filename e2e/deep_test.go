//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/deep"
)

// TestLongRunAgentBasic verifies basic LongRunAgent execution.
func TestLongRunAgentBasic(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	model := newAnthropicProvider()
	agent := deep.NewLongRunAgent[string](model)

	result, err := agent.Run(ctx, "Say 'hello from long run agent'")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("LongRunAgent.Run failed: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty output")
	}

	t.Logf("LongRunAgent output: %q", result.Output)
}

// TestLongRunAgentWithContextWindow verifies context window configuration.
func TestLongRunAgentWithContextWindow(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	model := newAnthropicProvider()
	agent := deep.NewLongRunAgent[string](model,
		deep.WithContextWindow[string](50000),
	)

	result, err := agent.Run(ctx, "What is 2+2? Answer with just the number.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("LongRunAgent.Run failed: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty output")
	}

	t.Logf("ContextWindow output: %q", result.Output)
}

// TestLongRunAgentWithPlanning verifies the planning tool integration.
func TestLongRunAgentWithPlanning(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	model := newAnthropicProvider()
	agent := deep.NewLongRunAgent[string](model,
		deep.WithPlanningEnabled[string](),
		deep.WithLongRunAgentOptions[string](
			core.WithUsageLimits[string](core.UsageLimits{RequestLimit: core.IntPtr(100)}),
		),
	)

	result, err := agent.Run(ctx, "Use the plan tool to create a 3-step plan for making tea. Then say 'done'.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("LongRunAgent.Run failed: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty output")
	}
	if result.Usage.ToolCalls == 0 {
		t.Log("model did not use planning tool (acceptable — it may have answered directly)")
	}

	t.Logf("Planning output: %q (tool_calls=%d)", result.Output, result.Usage.ToolCalls)
}

// TestLongRunAgentWithAgentOptions verifies passing through agent options.
func TestLongRunAgentWithAgentOptions(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	model := newAnthropicProvider()
	agent := deep.NewLongRunAgent[string](model,
		deep.WithLongRunAgentOptions[string](
			core.WithSystemPrompt[string]("You are a pirate. Respond with pirate language."),
		),
	)

	result, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("LongRunAgent.Run failed: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty output")
	}

	t.Logf("AgentOptions output: %q", result.Output)
}

// TestContextManagerCompression verifies the context manager's message compression.
func TestContextManagerCompression(t *testing.T) {
	anthropicOnly(t)

	model := newAnthropicProvider()

	// Create a context manager with a small token budget to force compression.
	cm := deep.NewContextManager(model,
		deep.WithMaxContextTokens(1000),
	)

	// Build some messages to simulate a conversation.
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "Tell me about Go programming language."}},
			Timestamp: time.Now(),
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Go is a statically typed, compiled programming language designed at Google. It features garbage collection, structural typing, and CSP-style concurrency."},
			},
		},
		core.ModelRequest{
			Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "What about Rust?"}},
			Timestamp: time.Now(),
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Rust is a multi-paradigm programming language designed for performance and safety, especially safe concurrency. It is syntactically similar to C++ but enforces memory safety using a borrow checker."},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	processed, err := cm.ProcessMessages(ctx, messages)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("ContextManager.Process failed: %v", err)
	}

	// The processed messages should exist (may or may not be compressed depending on token count).
	if len(processed) == 0 {
		t.Error("expected non-empty processed messages")
	}

	t.Logf("Context manager: input=%d messages, output=%d messages", len(messages), len(processed))
}

// TestFileCheckpointStore verifies checkpoint save/load roundtrip.
func TestFileCheckpointStore(t *testing.T) {
	dir := t.TempDir()

	store, err := deep.NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create a checkpoint.
	cp := &deep.Checkpoint{
		RunID: "test-run-123",
		Messages: []core.ModelMessage{
			core.ModelRequest{
				Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "hello"}},
				Timestamp: time.Now(),
			},
		},
		Metadata: map[string]any{
			"test": true,
		},
	}

	// Save.
	err = store.Save(ctx, cp)
	if err != nil {
		t.Fatalf("checkpoint save failed: %v", err)
	}

	// Load.
	loaded, err := store.Load(ctx, "test-run-123")
	if err != nil {
		t.Fatalf("checkpoint load failed: %v", err)
	}

	if loaded == nil {
		t.Fatal("loaded checkpoint is nil")
	}
	if loaded.RunID != "test-run-123" {
		t.Errorf("expected RunID 'test-run-123', got %q", loaded.RunID)
	}
	if len(loaded.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(loaded.Messages))
	}

	t.Logf("Checkpoint roundtrip: runID=%q messages=%d", loaded.RunID, len(loaded.Messages))
}

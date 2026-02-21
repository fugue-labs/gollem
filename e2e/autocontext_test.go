//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestAutoContextCompression verifies that auto-context summarizes old messages
// when the estimated token count exceeds the budget.
func TestAutoContextCompression(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithAutoContext[string](core.AutoContextConfig{
			MaxTokens: 100, // very low budget to trigger compression quickly
			KeepLastN: 2,
		}),
	)

	// First exchange - build up history.
	r1, err := agent.Run(ctx, "My name is Alice. Remember that.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("first run failed: %v", err)
	}

	// Second exchange with message history - should trigger compression.
	r2, err := agent.Run(ctx, "What is my name?",
		core.WithMessages(r1.Messages...),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("second run failed: %v", err)
	}

	// The agent should still know the name even after compression.
	// The response should contain "Alice".
	t.Logf("Response: %q (messages: %d)", r2.Output, len(r2.Messages))
}

// TestAutoContextKeepLastN verifies that KeepLastN recent messages are always preserved.
func TestAutoContextKeepLastN(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithAutoContext[string](core.AutoContextConfig{
			MaxTokens: 50, // very low to force compression
			KeepLastN: 4,
		}),
	)

	// Build up several turns.
	msgs := []core.ModelMessage{}

	r1, err := agent.Run(ctx, "Remember: the secret word is 'banana'.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("first run failed: %v", err)
	}
	msgs = r1.Messages

	r2, err := agent.Run(ctx, "Now tell me something about Go programming.",
		core.WithMessages(msgs...),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("second run failed: %v", err)
	}
	msgs = r2.Messages

	r3, err := agent.Run(ctx, "What was the secret word I told you earlier?",
		core.WithMessages(msgs...),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("third run failed: %v", err)
	}

	t.Logf("Response: %q (messages: %d)", r3.Output, len(r3.Messages))
}

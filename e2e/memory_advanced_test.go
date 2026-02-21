//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/memory"
)

// TestTokenBudgetMemory verifies token-budget-based history trimming.
func TestTokenBudgetMemory(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// TokenBudgetMemory with a very small budget to force trimming.
	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithHistoryProcessor[string](memory.TokenBudgetMemory(100)),
	)

	// First run.
	result1, err := agent.Run(ctx, "My favorite color is blue.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("first run failed: %v", err)
	}

	// Second run with history — token budget should trim old messages.
	result2, err := agent.Run(ctx, "What is my favorite number? Just guess a number.",
		core.WithMessages(result1.Messages...),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("second run failed: %v", err)
	}

	t.Logf("First output: %q", result1.Output)
	t.Logf("Second output: %q (message count: %d)", result2.Output, len(result2.Messages))
}

// TestSummaryMemory verifies LLM-based history summarization.
func TestSummaryMemory(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	summarizer := newAnthropicProvider()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithHistoryProcessor[string](memory.SummaryMemory(summarizer, 2)),
	)

	// Build up conversation history.
	result1, err := agent.Run(ctx, "The password is 'swordfish'. Remember it.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("first run failed: %v", err)
	}

	result2, err := agent.Run(ctx, "I also like pizza.",
		core.WithMessages(result1.Messages...),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("second run failed: %v", err)
	}

	// Third run - with summary memory at maxMessages=2, older messages should be summarized.
	result3, err := agent.Run(ctx, "Tell me everything you know about me.",
		core.WithMessages(result2.Messages...),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("third run failed: %v", err)
	}

	t.Logf("Final output: %q (message count: %d)", result3.Output, len(result3.Messages))
}

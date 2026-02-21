//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/memory"
)

// --- Phase 8: Multi-turn conversations & memory ---

// TestMultiTurnConversation verifies conversation continuation with WithMessages.
func TestMultiTurnConversation(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	// Turn 1: Establish context.
	result1, err := agent.Run(ctx, "My name is Alice. Remember this.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("turn 1 failed: %v", err)
	}
	t.Logf("Turn 1: %q", result1.Output)

	// Turn 2: Ask about the context from turn 1.
	result2, err := agent.Run(ctx, "What is my name?",
		core.WithMessages(result1.Messages...),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("turn 2 failed: %v", err)
	}

	if !strings.Contains(strings.ToLower(result2.Output), "alice") {
		t.Errorf("expected 'alice' in turn 2, got: %q", result2.Output)
	}

	t.Logf("Turn 2: %q (messages=%d)", result2.Output, len(result2.Messages))
}

// TestMultiTurnWithTools verifies tools work across conversation turns.
func TestMultiTurnWithTools(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A+p.B), nil
	})

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
	)

	// Turn 1: Add some numbers.
	result1, err := agent.Run(ctx, "Use the add tool to compute 3+4. Remember the result.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("turn 1 failed: %v", err)
	}
	t.Logf("Turn 1: %q", result1.Output)

	// Turn 2: Refer to previous result.
	result2, err := agent.Run(ctx, "Now use the add tool to add 10 to the previous result.",
		core.WithMessages(result1.Messages...),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("turn 2 failed: %v", err)
	}

	if !strings.Contains(result2.Output, "17") {
		t.Errorf("expected '17' in turn 2 (7+10), got: %q", result2.Output)
	}

	t.Logf("Turn 2: %q", result2.Output)
}

// TestSlidingWindowMemory verifies history trimming with real conversations.
func TestSlidingWindowMemory(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithHistoryProcessor[string](memory.SlidingWindowMemory(2)),
	)

	// Build up conversation beyond window size.
	var messages []core.ModelMessage

	prompts := []string{
		"My favorite color is blue. Remember this.",
		"My favorite animal is cat. Remember this.",
		"My favorite food is pizza. Remember this.",
		"My favorite number is 42. Remember this.",
	}

	for _, p := range prompts {
		result, err := agent.Run(ctx, p, core.WithMessages(messages...))
		if err != nil {
			skipOnAccountError(t, err)
			t.Fatalf("Run failed for %q: %v", p, err)
		}
		messages = result.Messages
	}

	// With window size 2 (4 messages kept + first), older memories may be lost.
	// Ask about recent memory (should remember) and old memory (may not).
	result, err := agent.Run(ctx, "What is my favorite number?",
		core.WithMessages(messages...),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("final Run failed: %v", err)
	}

	if !strings.Contains(result.Output, "42") {
		t.Logf("model did not recall '42' (may be outside window): %q", result.Output)
	} else {
		t.Logf("model recalled recent memory: %q", result.Output)
	}

	t.Logf("Total messages in final conversation: %d", len(result.Messages))
}

// TestKnowledgeBaseRetrieval verifies that knowledge context is prepended.
func TestKnowledgeBaseRetrieval(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	kb := core.NewStaticKnowledgeBase("The user's favorite programming language is Go.")

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithKnowledgeBase[string](kb),
	)

	result, err := agent.Run(ctx, "What is my favorite programming language?")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	lower := strings.ToLower(result.Output)
	if !strings.Contains(lower, "go") {
		t.Errorf("expected 'go' in response (from knowledge base), got: %q", result.Output)
	}

	t.Logf("Output=%q", result.Output)
}

// TestKnowledgeBaseAutoStore verifies responses are stored automatically.
func TestKnowledgeBaseAutoStore(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	kb := core.NewStaticKnowledgeBase("You are a helpful assistant.")

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithKnowledgeBase[string](kb),
		core.WithKnowledgeBaseAutoStore[string](),
	)

	_, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	stored := kb.Stored()
	if len(stored) == 0 {
		t.Error("expected auto-stored response, got none")
	} else {
		t.Logf("Auto-stored %d responses: %v", len(stored), stored)
	}
}

// TestConversationMessageGrowth verifies messages accumulate correctly.
func TestConversationMessageGrowth(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	// Turn 1.
	r1, err := agent.Run(ctx, "Say 'one'")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("turn 1 failed: %v", err)
	}
	msgCount1 := len(r1.Messages)

	// Turn 2 with history.
	r2, err := agent.Run(ctx, "Say 'two'", core.WithMessages(r1.Messages...))
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("turn 2 failed: %v", err)
	}
	msgCount2 := len(r2.Messages)

	// Messages should grow: turn 1 has ~2 (request+response), turn 2 has ~4.
	if msgCount2 <= msgCount1 {
		t.Errorf("expected message count to grow: turn1=%d turn2=%d", msgCount1, msgCount2)
	}

	t.Logf("Turn1=%d messages, Turn2=%d messages", msgCount1, msgCount2)
}

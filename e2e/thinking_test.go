//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// --- Phase 12: Extended Thinking & Reasoning Effort ---

// TestExtendedThinking_Anthropic verifies extended thinking with Anthropic.
func TestExtendedThinking_Anthropic(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Use a thinking budget. Note: Haiku may not support thinking — if so, the
	// API will return an error and we'll log/skip gracefully.
	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithThinkingBudget[string](1024),
		core.WithMaxTokens[string](2048), // Must be > thinking budget
	)

	result, err := agent.Run(ctx, "Think step by step: if a train travels at 60mph for 2.5 hours, how far does it go?")
	if err != nil {
		skipOnAccountError(t, err)
		// Extended thinking may not be supported on Haiku.
		errStr := err.Error()
		if strings.Contains(errStr, "thinking") || strings.Contains(errStr, "not supported") || strings.Contains(errStr, "not available") {
			t.Skipf("Extended thinking not supported on this model: %v", err)
		}
		t.Fatalf("agent.Run failed: %v", err)
	}

	// Check response contains the answer.
	if !strings.Contains(result.Output, "150") {
		t.Logf("Answer may not contain '150' (LLM variability): %q", result.Output)
	}

	// Check for ThinkingPart in messages.
	var hasThinking bool
	for _, msg := range result.Messages {
		resp, ok := msg.(core.ModelResponse)
		if !ok {
			continue
		}
		for _, part := range resp.Parts {
			if tp, ok := part.(core.ThinkingPart); ok {
				hasThinking = true
				t.Logf("ThinkingPart content (first 200 chars): %q", truncate(tp.Content, 200))
				if tp.Content == "" {
					t.Error("ThinkingPart has empty content")
				}
			}
		}
	}
	if !hasThinking {
		t.Error("expected at least one ThinkingPart in response messages")
	}

	t.Logf("Output=%q", truncate(result.Output, 200))
}

// TestExtendedThinkingStream_Anthropic verifies streaming with extended thinking.
func TestExtendedThinkingStream_Anthropic(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithThinkingBudget[string](1024),
		core.WithMaxTokens[string](2048),
	)

	stream, err := agent.RunStream(ctx, "What is 7 * 8? Think through it step by step.")
	if err != nil {
		skipOnAccountError(t, err)
		errStr := err.Error()
		if strings.Contains(errStr, "thinking") || strings.Contains(errStr, "not supported") {
			t.Skipf("Extended thinking not supported: %v", err)
		}
		t.Fatalf("RunStream failed: %v", err)
	}

	// Consume stream, looking for thinking deltas.
	var thinkingDeltas int
	var textDeltas int
	for event, err := range stream.StreamEvents() {
		if err != nil {
			break
		}
		switch event.(type) {
		case core.PartStartEvent:
			// Check if it's a thinking part start
			if e, ok := event.(core.PartStartEvent); ok {
				if _, ok := e.Part.(core.ThinkingPart); ok {
					thinkingDeltas++
				}
			}
		case core.PartDeltaEvent:
			if e, ok := event.(core.PartDeltaEvent); ok {
				if _, ok := e.Delta.(core.ThinkingPartDelta); ok {
					thinkingDeltas++
				}
				if _, ok := e.Delta.(core.TextPartDelta); ok {
					textDeltas++
				}
			}
		}
	}

	if thinkingDeltas == 0 {
		t.Log("No thinking deltas received (model may not support thinking)")
	} else {
		t.Logf("ThinkingDeltas=%d TextDeltas=%d", thinkingDeltas, textDeltas)
	}
}

// TestThinkingOverridesTemperature verifies temperature is auto-stripped.
func TestThinkingOverridesTemperature(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Set BOTH thinking budget AND temperature. The framework should
	// strip temperature to avoid the Anthropic API error.
	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithThinkingBudget[string](1024),
		core.WithTemperature[string](0.5),
		core.WithMaxTokens[string](2048),
	)

	result, err := agent.Run(ctx, "What is 2+2?")
	if err != nil {
		skipOnAccountError(t, err)
		errStr := err.Error()
		if strings.Contains(errStr, "thinking") || strings.Contains(errStr, "not supported") {
			t.Skipf("Extended thinking not supported: %v", err)
		}
		// If we get a temperature conflict error, that means our fix didn't work.
		if strings.Contains(errStr, "temperature") {
			t.Fatalf("Temperature was not auto-stripped when thinking enabled: %v", err)
		}
		t.Fatalf("agent.Run failed: %v", err)
	}

	t.Logf("Thinking + temperature override works. Output=%q", truncate(result.Output, 200))
}

// TestReasoningEffort_XAI verifies reasoning effort with xAI/Grok.
func TestReasoningEffort_XAI(t *testing.T) {
	anthropicOnly(t) // Reuse anthropicOnly to skip if no credentials
	skipIfNoCredentials(t, "XAI_API_KEY")

	efforts := []string{"low", "medium", "high"}
	for _, effort := range efforts {
		t.Run(effort, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			agent := core.NewAgent[string](newXAIProvider(),
				core.WithReasoningEffort[string](effort),
			)

			result, err := agent.Run(ctx, "What is 3+4? Reply with just the number.")
			if err != nil {
				skipOnAccountError(t, err)
				errStr := err.Error()
				// Some models may not support reasoning_effort.
				if strings.Contains(errStr, "reasoning") || strings.Contains(errStr, "not supported") ||
					strings.Contains(errStr, "Unrecognized") || strings.Contains(errStr, "invalid") {
					t.Skipf("reasoning_effort=%q not supported: %v", effort, err)
				}
				t.Fatalf("agent.Run failed with effort=%q: %v", effort, err)
			}

			t.Logf("effort=%q output=%q", effort, result.Output)
		})
	}
}

// TestThinkingBudgetModelSettings verifies ThinkingBudget round-trips through ModelSettings.
func TestThinkingBudgetModelSettings(t *testing.T) {
	budget := 2048
	effort := "high"
	settings := core.ModelSettings{
		ThinkingBudget:  &budget,
		ReasoningEffort: &effort,
	}

	if settings.ThinkingBudget == nil || *settings.ThinkingBudget != 2048 {
		t.Errorf("expected ThinkingBudget=2048, got %v", settings.ThinkingBudget)
	}
	if settings.ReasoningEffort == nil || *settings.ReasoningEffort != "high" {
		t.Errorf("expected ReasoningEffort=high, got %v", settings.ReasoningEffort)
	}
	t.Log("ModelSettings fields work correctly")
}

// truncate returns the first n characters of s.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

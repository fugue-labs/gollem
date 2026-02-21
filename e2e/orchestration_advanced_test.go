//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/orchestration"
)

// TestPipelineTransformStep verifies TransformStep works in a pipeline.
func TestPipelineTransformStep(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("Reply with only the text you received, nothing else."),
	)

	pipeline := orchestration.NewPipeline(
		orchestration.TransformStep(func(s string) string {
			return strings.ToUpper(s)
		}),
		orchestration.AgentStep(agent),
	)

	result, err := pipeline.Run(ctx, "hello world")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("pipeline.Run failed: %v", err)
	}

	t.Logf("Pipeline output: %q", result)
}

// TestPipelineThenChaining verifies .Then() chaining works.
func TestPipelineThenChaining(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pipeline := orchestration.NewPipeline(
		orchestration.TransformStep(func(s string) string {
			return "prefix: " + s
		}),
	).Then(
		orchestration.TransformStep(func(s string) string {
			return s + " :suffix"
		}),
	)

	result, err := pipeline.Run(ctx, "hello")
	if err != nil {
		t.Fatalf("pipeline.Run failed: %v", err)
	}

	expected := "prefix: hello :suffix"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}

	t.Logf("Pipeline output: %q", result)
}

// TestPipelineConditionalStep verifies conditional branching in pipelines.
func TestPipelineConditionalStep(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pipeline := orchestration.NewPipeline(
		orchestration.ConditionalStep(
			func(s string) bool { return strings.Contains(s, "upper") },
			orchestration.TransformStep(func(s string) string { return strings.ToUpper(s) }),
			orchestration.TransformStep(func(s string) string { return strings.ToLower(s) }),
		),
	)

	// Should take the "true" branch (upper).
	result1, err := pipeline.Run(ctx, "make this upper")
	if err != nil {
		t.Fatalf("pipeline.Run failed: %v", err)
	}
	if result1 != "MAKE THIS UPPER" {
		t.Errorf("expected uppercase, got %q", result1)
	}

	// Should take the "false" branch (lower).
	result2, err := pipeline.Run(ctx, "MAKE THIS LOWER")
	if err != nil {
		t.Fatalf("pipeline.Run failed: %v", err)
	}
	if result2 != "make this lower" {
		t.Errorf("expected lowercase, got %q", result2)
	}

	t.Logf("Upper branch: %q", result1)
	t.Logf("Lower branch: %q", result2)
}

// TestHandoffStripSystemPrompts verifies the StripSystemPrompts filter.
func TestHandoffStripSystemPrompts(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent1 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You are agent 1. Say 'step 1 complete'."),
	)
	agent2 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You are agent 2. Summarize what you received."),
	)

	result, err := orchestration.ChainRunWithFilter(ctx, agent1, agent2,
		"Start the process.",
		func(a string) string { return "Agent 1 said: " + a },
		orchestration.StripSystemPrompts(),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("ChainRunWithFilter failed: %v", err)
	}

	t.Logf("Output: %q", result.Output)
}

// TestHandoffKeepLastN verifies the KeepLastN filter.
func TestHandoffKeepLastN(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent1 := core.NewAgent[string](newAnthropicProvider())
	agent2 := core.NewAgent[string](newAnthropicProvider())

	result, err := orchestration.ChainRunWithFilter(ctx, agent1, agent2,
		"Tell me about cats.",
		func(a string) string { return "Previous agent said: " + a + ". Now tell me about dogs." },
		orchestration.KeepLastN(2),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("ChainRunWithFilter failed: %v", err)
	}

	t.Logf("Output: %q", result.Output)
}

// TestHandoffChainFilters verifies chaining multiple filters.
func TestHandoffChainFilters(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent1 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You are a helpful assistant."),
	)
	agent2 := core.NewAgent[string](newAnthropicProvider())

	combined := orchestration.ChainFilters(
		orchestration.StripSystemPrompts(),
		orchestration.KeepLastN(3),
	)

	result, err := orchestration.ChainRunWithFilter(ctx, agent1, agent2,
		"Hello!",
		func(a string) string { return "Continue: " + a },
		combined,
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("ChainRunWithFilter failed: %v", err)
	}

	t.Logf("Output: %q", result.Output)
}

// TestMultiAgentHandoff verifies multi-step handoff between agents.
func TestMultiAgentHandoff(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	agent1 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You are step 1. Respond with exactly: 'Step 1 processed: ' followed by the input."),
	)

	agent2 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You are step 2. Respond with exactly: 'Step 2 processed: ' followed by the input."),
	)

	handoff := orchestration.NewHandoff[string]()
	handoff.AddStep("step1", agent1, func(prev string) string { return prev })
	handoff.AddStep("step2", agent2, func(prev string) string { return prev })

	result, err := handoff.Run(ctx, "test data")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("handoff.Run failed: %v", err)
	}

	t.Logf("Handoff output: %q", result.Output)
}

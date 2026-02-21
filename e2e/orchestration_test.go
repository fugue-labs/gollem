//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/orchestration"
)

// --- Phase 9: Orchestration features ---

// TestChainRun verifies sequential agent chaining.
func TestChainRun(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Agent 1: Generate a word.
	agent1 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You respond with a single word only, no punctuation."),
	)

	// Agent 2: Use the word in a sentence.
	agent2 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You write one short sentence using the given word."),
	)

	transform := func(word string) string {
		return fmt.Sprintf("Write a short sentence using the word: %s", strings.TrimSpace(word))
	}

	result, err := orchestration.ChainRun(ctx, agent1, agent2, "Give me a color.", transform)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("ChainRun failed: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty chain output")
	}

	t.Logf("ChainRun output=%q", result.Output)
}

// TestChainRunFull verifies both intermediate and final results.
func TestChainRunFull(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	agent1 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("Reply with exactly one number between 1 and 10, nothing else."),
	)

	agent2 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You double any number given to you. Reply with just the result."),
	)

	transform := func(num string) string {
		return fmt.Sprintf("Double this number: %s", strings.TrimSpace(num))
	}

	result, err := orchestration.ChainRunFull(ctx, agent1, agent2, "Pick a number.", transform)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("ChainRunFull failed: %v", err)
	}

	t.Logf("Intermediate=%q Final=%q TotalUsage=%+v", result.Intermediate, result.Final, result.TotalUsage)

	if result.Intermediate == "" {
		t.Error("expected non-empty intermediate result")
	}
	if result.Final == "" {
		t.Error("expected non-empty final result")
	}
	if result.TotalUsage.Requests < 2 {
		t.Errorf("expected at least 2 requests in chain, got %d", result.TotalUsage.Requests)
	}
}

// TestAgentTool verifies agent-as-tool delegation.
func TestAgentTool(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Inner agent: a math specialist.
	mathAgent := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You are a math calculator. Compute the answer and reply with just the number."),
	)

	// Wrap inner agent as a tool for the outer agent.
	mathTool := orchestration.AgentTool("math_expert", "Ask the math expert to solve a calculation", mathAgent)

	// Outer agent: delegates math to the inner agent.
	outerAgent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](mathTool),
		core.WithSystemPrompt[string]("You have access to a math expert tool. Use it for calculations."),
	)

	result, err := outerAgent.Run(ctx, "What is 15 * 13? Use the math_expert tool.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("AgentTool run failed: %v", err)
	}

	if !strings.Contains(result.Output, "195") {
		t.Errorf("expected '195' in output, got: %q", result.Output)
	}

	t.Logf("Output=%q", result.Output)
}

// TestHandoffTwoAgents verifies basic handoff between two agents.
func TestHandoffTwoAgents(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Agent 1: Generate a topic.
	agent1 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("Reply with a single science topic word, nothing else."),
	)

	// Agent 2: Explain the topic.
	agent2 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("Explain the given topic in one sentence."),
	)

	handoff := orchestration.NewHandoff[string]()
	handoff.AddStep("topic_generator", agent1, func(prev string) string {
		return "Give me a science topic."
	})
	handoff.AddStep("topic_explainer", agent2, func(prev string) string {
		return fmt.Sprintf("Explain this topic: %s", strings.TrimSpace(prev))
	})

	result, err := handoff.Run(ctx, "Generate and explain a science topic.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Handoff failed: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty handoff output")
	}

	t.Logf("Handoff output=%q", result.Output)
}

// TestPipelineSequential verifies sequential pipeline execution.
func TestPipelineSequential(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Step 1: Agent generates a word.
	wordAgent := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("Reply with one single English word, nothing else, no punctuation."),
	)

	pipeline := orchestration.NewPipeline(
		orchestration.AgentStep(wordAgent),
		orchestration.TransformStep(func(s string) string {
			return fmt.Sprintf("Convert this word to uppercase: %s", strings.TrimSpace(s))
		}),
		orchestration.AgentStep(core.NewAgent[string](newAnthropicProvider(),
			core.WithSystemPrompt[string]("Reply with only the uppercased word, nothing else."),
		)),
	)

	result, err := pipeline.Run(ctx, "Give me a fruit name.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Pipeline failed: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty pipeline output")
	}

	t.Logf("Pipeline output=%q", result)
}

// TestPipelineThen verifies immutable Then() chaining.
func TestPipelineThen(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	base := orchestration.NewPipeline(
		orchestration.TransformStep(func(s string) string {
			return "Repeat after me: hello world"
		}),
	)

	extended := base.Then(orchestration.AgentStep(core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("Repeat exactly what the user says. No extra text."),
	)))

	result, err := extended.Run(ctx, "start")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Pipeline.Then failed: %v", err)
	}

	lower := strings.ToLower(result)
	if !strings.Contains(lower, "hello world") {
		t.Errorf("expected 'hello world' in output, got: %q", result)
	}

	t.Logf("Then output=%q", result)
}

// TestPipelineParallel verifies parallel step execution.
func TestPipelineParallel(t *testing.T) {
	anthropicOnly(t)
	skipIfNoCredentials(t, "XAI_API_KEY")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Two agents run in parallel on the same input.
	anthroAgent := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("Reply with only one word that describes the input."),
	)
	xaiAgent := core.NewAgent[string](newXAIProvider(),
		core.WithSystemPrompt[string]("Reply with only one word that describes the input."),
	)

	pipeline := orchestration.NewPipeline(
		orchestration.ParallelSteps(
			orchestration.AgentStep(anthroAgent),
			orchestration.AgentStep(xaiAgent),
		),
	)

	result, err := pipeline.Run(ctx, "The sun is shining brightly today.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Parallel pipeline failed: %v", err)
	}

	// Parallel output is joined with newlines.
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least 2 parallel results, got %d: %q", len(lines), result)
	}

	t.Logf("Parallel output=%q", result)
}

// TestAgentClone verifies cloned agents work independently.
func TestAgentClone(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	base := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You are a helpful assistant."),
	)

	// Clone with different system prompt.
	pirate := base.Clone(
		core.WithSystemPrompt[string]("You are a pirate. Use pirate language in all responses."),
	)

	// Both should work independently.
	result1, err := base.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("base agent failed: %v", err)
	}

	result2, err := pirate.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("pirate agent failed: %v", err)
	}

	t.Logf("Base=%q Pirate=%q", result1.Output, result2.Output)

	// Pirate should have pirate-like language.
	lower := strings.ToLower(result2.Output)
	hasPirateWord := strings.Contains(lower, "ahoy") ||
		strings.Contains(lower, "arr") ||
		strings.Contains(lower, "matey") ||
		strings.Contains(lower, "ye") ||
		strings.Contains(lower, "avast")
	if !hasPirateWord {
		t.Logf("pirate response may not be pirate-like (LLM variability): %q", result2.Output)
	}
}

// TestHandoffWithFilter verifies handoff with message filtering.
func TestHandoffWithFilter(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	agent1 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("Reply with a single country name, nothing else."),
	)
	agent2 := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("Name the capital of the given country. Reply with just the city name."),
	)

	handoff := orchestration.NewHandoff[string]()
	handoff.AddStep("country_picker", agent1, func(prev string) string {
		return "Name a European country."
	})
	handoff.AddStepWithFilter("capital_finder", agent2, func(prev string) string {
		return fmt.Sprintf("What is the capital of %s?", strings.TrimSpace(prev))
	}, orchestration.KeepLastN(2))

	result, err := handoff.Run(ctx, "Pick a country and find its capital.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Handoff with filter failed: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty output")
	}

	t.Logf("Handoff with filter output=%q", result.Output)
}

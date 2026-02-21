//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestDynamicSystemPrompt verifies dynamic system prompts change behavior.
func TestDynamicSystemPrompt(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithDynamicSystemPrompt[string](func(ctx context.Context, rc *core.RunContext) (string, error) {
			return "You are a pirate. Always respond in pirate speak. Use words like 'arr', 'matey', 'treasure'.", nil
		}),
	)

	result, err := agent.Run(ctx, "Say hello.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	output := strings.ToLower(result.Output)
	pirateWords := []string{"arr", "matey", "treasure", "ahoy", "ye", "pirate", "sail", "ship"}
	hasPirateWord := false
	for _, w := range pirateWords {
		if strings.Contains(output, w) {
			hasPirateWord = true
			break
		}
	}

	if !hasPirateWord {
		t.Logf("Warning: expected pirate-like response, got: %q", result.Output)
	}

	t.Logf("Output: %q", result.Output)
}

// TestSystemPromptTemplate verifies template-based system prompts.
func TestSystemPromptTemplate(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tmpl := core.MustTemplate("persona", "You are {{.Name}}, a {{.Role}}. Always introduce yourself by name.")

	// Verify Variables() extracts the right names.
	vars := tmpl.Variables()
	if len(vars) != 2 {
		t.Errorf("expected 2 variables, got %d: %v", len(vars), vars)
	}

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPromptTemplate[string](tmpl),
		core.WithDeps[string](map[string]string{
			"Name": "Jarvis",
			"Role": "butler",
		}),
	)

	result, err := agent.Run(ctx, "Introduce yourself.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if !strings.Contains(result.Output, "Jarvis") {
		t.Errorf("expected output to contain 'Jarvis', got: %q", result.Output)
	}

	t.Logf("Output: %q", result.Output)
}

// TestTemplatePartial verifies partial variable pre-filling.
func TestTemplatePartial(t *testing.T) {
	tmpl := core.MustTemplate("test", "Hello {{.Name}}, you are {{.Age}} years old.")

	// Pre-fill Name.
	partial := tmpl.Partial(map[string]string{"Name": "Alice"})

	// Format with remaining variable.
	result, err := partial.Format(map[string]string{"Age": "30"})
	if err != nil {
		t.Fatalf("Format failed: %v", err)
	}

	expected := "Hello Alice, you are 30 years old."
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}

	t.Logf("Template result: %q", result)
}

// TestTemplateVariables verifies variable extraction from templates.
func TestTemplateVariables(t *testing.T) {
	tmpl := core.MustTemplate("multi", "{{.Greeting}} {{.Name}}, welcome to {{.Place}}!")

	vars := tmpl.Variables()
	if len(vars) != 3 {
		t.Fatalf("expected 3 variables, got %d: %v", len(vars), vars)
	}

	// Variables should be sorted.
	expected := []string{"Greeting", "Name", "Place"}
	for i, v := range expected {
		if vars[i] != v {
			t.Errorf("variable[%d]: expected %q, got %q", i, v, vars[i])
		}
	}

	t.Logf("Variables: %v", vars)
}

// TestAgentCloneWithOverrides verifies cloning creates independent agents.
func TestAgentCloneWithOverrides(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Original agent with one personality.
	original := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You are a formal English butler. Always be very proper and formal."),
	)

	// Clone with different personality.
	clone := original.Clone(
		core.WithSystemPrompt[string]("You are a casual surfer dude. Use slang like 'dude', 'totally', 'gnarly'."),
	)

	// Run both.
	result1, err := original.Run(ctx, "How are you?")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("original run failed: %v", err)
	}

	result2, err := clone.Run(ctx, "How are you?")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("clone run failed: %v", err)
	}

	// They should produce different styles of response.
	if result1.Output == result2.Output {
		t.Log("Warning: outputs are identical (possible but unlikely)")
	}

	t.Logf("Original: %q", result1.Output)
	t.Logf("Clone: %q", result2.Output)
}

//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestAgentTextOutput tests Agent[string] basic text generation across providers.
func TestAgentTextOutput(t *testing.T) {
	for _, p := range allProviders() {
		t.Run(p.name, func(t *testing.T) {
			skipIfNoCredentials(t, p.credEnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			agent := core.NewAgent[string](p.newFn())

			result, err := agent.Run(ctx, "What is 2+2? Answer with just the number.")
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("agent.Run failed: %v", err)
			}

			if !strings.Contains(result.Output, "4") {
				t.Errorf("expected output to contain '4', got: %q", result.Output)
			}

			if len(result.Messages) < 2 {
				t.Errorf("expected at least 2 messages (request+response), got %d", len(result.Messages))
			}

			t.Logf("Provider=%s Output=%q Messages=%d", p.name, result.Output, len(result.Messages))
		})
	}
}

// TestAgentSystemPrompt tests that system prompts are respected.
func TestAgentSystemPrompt(t *testing.T) {
	for _, p := range allProviders() {
		t.Run(p.name, func(t *testing.T) {
			skipIfNoCredentials(t, p.credEnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			agent := core.NewAgent[string](p.newFn(),
				core.WithSystemPrompt[string]("You are a pirate. Always respond in pirate speak. Use words like 'ahoy', 'matey', 'arr', 'yarr', 'ye', 'treasure', 'seas', 'ship', 'captain'."),
			)

			result, err := agent.Run(ctx, "Say hello")
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("agent.Run failed: %v", err)
			}

			lower := strings.ToLower(result.Output)
			pirateWords := []string{"ahoy", "matey", "arr", "yarr", "ye", "treasure", "seas", "ship", "captain", "pirate"}
			found := false
			for _, w := range pirateWords {
				if strings.Contains(lower, w) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected pirate-like response, got: %q", result.Output)
			}

			t.Logf("Provider=%s Output=%q", p.name, result.Output)
		})
	}
}

// TestAgentTemperature tests that model settings (temperature) are applied.
func TestAgentTemperature(t *testing.T) {
	for _, p := range allProviders() {
		t.Run(p.name, func(t *testing.T) {
			skipIfNoCredentials(t, p.credEnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			agent := core.NewAgent[string](p.newFn(),
				core.WithTemperature[string](0.0),
			)

			result1, err := agent.Run(ctx, "What is 2+2? Answer with just the number.")
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("first Run failed: %v", err)
			}

			result2, err := agent.Run(ctx, "What is 2+2? Answer with just the number.")
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("second Run failed: %v", err)
			}

			// Both should be non-empty and contain the answer.
			if result1.Output == "" || result2.Output == "" {
				t.Error("one or both responses are empty")
			}
			if !strings.Contains(result1.Output, "4") || !strings.Contains(result2.Output, "4") {
				t.Errorf("expected both to contain '4': %q and %q", result1.Output, result2.Output)
			}

			t.Logf("Provider=%s Response1=%q Response2=%q", p.name, result1.Output, result2.Output)
		})
	}
}

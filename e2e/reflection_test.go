//go:build e2e

package e2e

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestReflectionBasic verifies the reflection loop accepts valid output.
func TestReflectionBasic(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithSystemPrompt[string]("You are a helpful assistant. When asked for a number, respond with ONLY the number, nothing else."),
	)

	// Validator that accepts output containing a number > 100.
	validator := func(ctx context.Context, output string) (string, error) {
		trimmed := strings.TrimSpace(output)
		n, err := strconv.Atoi(trimmed)
		if err != nil {
			return "Output must be a single integer number, nothing else. Please respond with just a number greater than 100.", nil
		}
		if n <= 100 {
			return "The number must be greater than 100. Please provide a larger number.", nil
		}
		return "", nil // accepted
	}

	result, err := core.RunWithReflection(ctx, agent, "Give me a number greater than 100. Reply with ONLY the number.", validator,
		core.WithMaxReflections(5),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("RunWithReflection failed: %v", err)
	}

	if !result.Accepted {
		t.Errorf("expected output to be accepted, got Accepted=false, Output=%q", result.Output)
	}
	if result.Iterations == 0 {
		t.Error("expected at least one iteration")
	}
	if result.Usage.Requests == 0 {
		t.Error("expected non-zero request count in usage")
	}

	t.Logf("Output=%q Iterations=%d Accepted=%v Usage.Requests=%d", result.Output, result.Iterations, result.Accepted, result.Usage.Requests)
}

// TestReflectionMaxIterations verifies the loop stops after max reflections.
func TestReflectionMaxIterations(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	// Validator that always rejects.
	validator := func(ctx context.Context, output string) (string, error) {
		return "This is not acceptable, try again.", nil
	}

	result, err := core.RunWithReflection(ctx, agent, "Say hello.", validator,
		core.WithMaxReflections(2),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("RunWithReflection failed: %v", err)
	}

	if result.Accepted {
		t.Error("expected output NOT to be accepted (always-reject validator)")
	}
	// With maxReflections=2, we get 3 iterations total (0, 1, 2).
	if result.Iterations > 3 {
		t.Errorf("expected at most 3 iterations, got %d", result.Iterations)
	}

	t.Logf("Output=%q Iterations=%d Accepted=%v", result.Output, result.Iterations, result.Accepted)
}

// TestReflectionCustomPrompt verifies custom reflection prompts work.
func TestReflectionCustomPrompt(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	callCount := 0
	validator := func(ctx context.Context, output string) (string, error) {
		callCount++
		if callCount == 1 {
			return "Please say 'corrected' instead.", nil
		}
		return "", nil // accept second attempt
	}

	result, err := core.RunWithReflection(ctx, agent, "Say hello.", validator,
		core.WithMaxReflections(3),
		core.WithReflectPrompt("CORRECTION NEEDED: %s\n\nPlease fix your response."),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("RunWithReflection failed: %v", err)
	}

	if !result.Accepted {
		t.Error("expected output to be accepted on second attempt")
	}
	if result.Iterations < 2 {
		t.Errorf("expected at least 2 iterations, got %d", result.Iterations)
	}

	t.Logf("Output=%q Iterations=%d", result.Output, result.Iterations)
}

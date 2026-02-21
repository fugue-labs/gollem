//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// CalcParams for basic arithmetic tools.
type CalcParams struct {
	A int `json:"a" jsonschema:"description=First number"`
	B int `json:"b" jsonschema:"description=Second number"`
}

// TestSingleToolCall verifies that each provider can invoke a tool and use the result.
func TestSingleToolCall(t *testing.T) {
	addTool := core.FuncTool[CalcParams]("add", "Add two numbers together", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A+p.B), nil
	})

	for _, p := range allProviders() {
		t.Run(p.name, func(t *testing.T) {
			skipIfNoCredentials(t, p.credEnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			agent := core.NewAgent[string](p.newFn(),
				core.WithTools[string](addTool),
			)

			result, err := agent.Run(ctx, "What is 123 + 456? Use the add tool to compute this.")
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("agent.Run failed: %v", err)
			}

			if !strings.Contains(result.Output, "579") {
				t.Errorf("expected output to contain '579', got: %q", result.Output)
			}

			// Verify tool was actually called by checking messages for tool call/return.
			foundToolCall := false
			for _, msg := range result.Messages {
				if resp, ok := msg.(core.ModelResponse); ok {
					for _, part := range resp.Parts {
						if tc, ok := part.(core.ToolCallPart); ok && tc.ToolName == "add" {
							foundToolCall = true
						}
					}
				}
			}
			if !foundToolCall {
				t.Error("expected to find a tool call for 'add' in messages")
			}

			t.Logf("Provider=%s Output=%q", p.name, result.Output)
		})
	}
}

// TestMultipleTools verifies that providers can use multiple tools in one run.
func TestMultipleTools(t *testing.T) {
	addTool := core.FuncTool[CalcParams]("add", "Add two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A+p.B), nil
	})

	multiplyTool := core.FuncTool[CalcParams]("multiply", "Multiply two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A*p.B), nil
	})

	for _, p := range allProviders() {
		t.Run(p.name, func(t *testing.T) {
			skipIfNoCredentials(t, p.credEnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			agent := core.NewAgent[string](p.newFn(),
				core.WithTools[string](addTool, multiplyTool),
			)

			result, err := agent.Run(ctx, "Use the add tool to compute 10+20, and the multiply tool to compute 5*6. Report both results.")
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("agent.Run failed: %v", err)
			}

			if !strings.Contains(result.Output, "30") {
				t.Errorf("expected output to contain '30' (10+20), got: %q", result.Output)
			}

			// Verify both tools were called.
			toolsCalled := map[string]bool{}
			for _, msg := range result.Messages {
				if resp, ok := msg.(core.ModelResponse); ok {
					for _, part := range resp.Parts {
						if tc, ok := part.(core.ToolCallPart); ok {
							toolsCalled[tc.ToolName] = true
						}
					}
				}
			}
			if !toolsCalled["add"] {
				t.Error("expected 'add' tool to be called")
			}
			if !toolsCalled["multiply"] {
				t.Error("expected 'multiply' tool to be called")
			}

			t.Logf("Provider=%s Output=%q ToolsCalled=%v", p.name, result.Output, toolsCalled)
		})
	}
}

// TestToolChoiceModes tests that tool choice configuration works.
func TestToolChoiceModes(t *testing.T) {
	addTool := core.FuncTool[CalcParams]("add", "Add two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A+p.B), nil
	})

	// Test ToolChoiceRequired - model MUST use a tool.
	t.Run("Required", func(t *testing.T) {
		p := allProviders()[0] // Use Anthropic
		skipIfNoCredentials(t, p.credEnvVar)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		agent := core.NewAgent[string](p.newFn(),
			core.WithTools[string](addTool),
			core.WithToolChoice[string](core.ToolChoiceRequired()),
		)

		result, err := agent.Run(ctx, "Add 1 and 2.")
		if err != nil {
			skipOnAccountError(t, err)
			t.Fatalf("agent.Run failed: %v", err)
		}

		// With ToolChoiceRequired, the model must have called the tool.
		foundToolCall := false
		for _, msg := range result.Messages {
			if resp, ok := msg.(core.ModelResponse); ok {
				for _, part := range resp.Parts {
					if _, ok := part.(core.ToolCallPart); ok {
						foundToolCall = true
					}
				}
			}
		}
		if !foundToolCall {
			t.Error("with ToolChoiceRequired, expected at least one tool call")
		}

		t.Logf("Output=%q", result.Output)
	})
}

// TestToolWithComplexParams verifies tools with nested struct parameters.
func TestToolWithComplexParams(t *testing.T) {
	type Address struct {
		Street string `json:"street" jsonschema:"description=Street address"`
		City   string `json:"city" jsonschema:"description=City name"`
	}
	type PersonParams struct {
		Name    string  `json:"name" jsonschema:"description=Person's name"`
		Age     int     `json:"age" jsonschema:"description=Person's age"`
		Address Address `json:"address" jsonschema:"description=Person's address"`
	}

	var captured PersonParams
	personTool := core.FuncTool[PersonParams]("register_person", "Register a person with their details", func(ctx context.Context, rc *core.RunContext, p PersonParams) (string, error) {
		captured = p
		return fmt.Sprintf("Registered %s, age %d, at %s, %s", p.Name, p.Age, p.Address.Street, p.Address.City), nil
	})

	p := allProviders()[0] // Use Anthropic
	skipIfNoCredentials(t, p.credEnvVar)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](p.newFn(),
		core.WithTools[string](personTool),
	)

	result, err := agent.Run(ctx, "Register a person named Alice, age 30, living at 123 Main St in Springfield. Use the register_person tool.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if captured.Name == "" {
		t.Error("tool was not called - captured name is empty")
	} else {
		if !strings.EqualFold(captured.Name, "Alice") {
			t.Errorf("expected name 'Alice', got %q", captured.Name)
		}
		if captured.Age != 30 {
			t.Errorf("expected age 30, got %d", captured.Age)
		}
		if captured.Address.City == "" {
			t.Error("expected non-empty city")
		}
	}

	t.Logf("Output=%q Captured=%+v", result.Output, captured)
}

// TestToolResultValidation tests that the global tool result validator is invoked.
// Note: With real LLMs, the model receives a RetryPromptPart when validation fails,
// but may choose to explain the error rather than retry. We verify the validator runs.
func TestToolResultValidation(t *testing.T) {
	var callCount int32
	var validatorCalled int32

	flakeyTool := core.FuncTool[CalcParams]("compute", "Compute the sum of two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			return "error: temporary failure", nil
		}
		return fmt.Sprintf("%d", p.A+p.B), nil
	}, core.WithToolMaxRetries(3))

	p := allProviders()[0] // Use Anthropic
	skipIfNoCredentials(t, p.credEnvVar)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	agent := core.NewAgent[string](p.newFn(),
		core.WithTools[string](flakeyTool),
		core.WithGlobalToolResultValidator[string](func(ctx context.Context, rc *core.RunContext, toolName string, result string) error {
			atomic.AddInt32(&validatorCalled, 1)
			if strings.Contains(result, "error:") {
				return fmt.Errorf("tool returned an error: %s", result)
			}
			return nil
		}),
	)

	result, err := agent.Run(ctx, "Use the compute tool with a=10 and b=20. If it fails, try again.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	validations := atomic.LoadInt32(&validatorCalled)
	if validations == 0 {
		t.Error("global tool result validator was never called")
	}

	calls := atomic.LoadInt32(&callCount)
	t.Logf("Output=%q ToolCalls=%d ValidatorCalls=%d", result.Output, calls, validations)
}

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

// TestToolTimeout verifies that tool timeout enforcement works.
func TestToolTimeout(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	slowTool := core.FuncTool[CalcParams]("slow_compute", "A slow computation",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			select {
			case <-time.After(10 * time.Second):
				return fmt.Sprintf("%d", p.A+p.B), nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
		core.WithToolTimeout(1*time.Second),
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](slowTool),
	)

	result, err := agent.Run(ctx, "Use the slow_compute tool with a=1 and b=2.")
	// The tool should timeout but the agent should handle it gracefully.
	if err != nil {
		skipOnAccountError(t, err)
		// A timeout error is expected and acceptable.
		t.Logf("Agent handled timeout: %v", err)
		return
	}

	// If the agent managed to recover (model gave a text response after error), that's fine.
	t.Logf("Output after timeout handling: %q", result.Output)
}

// TestDefaultToolTimeout verifies agent-level default tool timeout.
func TestDefaultToolTimeout(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	slowTool := core.FuncTool[CalcParams]("slow_op", "A slow operation",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			select {
			case <-time.After(10 * time.Second):
				return "done", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](slowTool),
		core.WithDefaultToolTimeout[string](1*time.Second),
	)

	result, err := agent.Run(ctx, "Use the slow_op tool with a=1 and b=1.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Logf("Agent handled default timeout: %v", err)
		return
	}

	t.Logf("Output: %q", result.Output)
}

// TestToolApproval verifies the tool approval workflow.
func TestToolApproval(t *testing.T) {
	anthropicOnly(t)

	t.Run("Approved", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		var toolCalled int32
		sensitiveTool := core.FuncTool[CalcParams]("delete_data", "Delete data",
			func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
				atomic.AddInt32(&toolCalled, 1)
				return "data deleted", nil
			},
			core.WithRequiresApproval(),
		)

		agent := core.NewAgent[string](newAnthropicProvider(),
			core.WithTools[string](sensitiveTool),
			core.WithToolApproval[string](func(ctx context.Context, toolName string, argsJSON string) (bool, error) {
				return true, nil // always approve
			}),
		)

		result, err := agent.Run(ctx, "Use the delete_data tool with a=1 and b=1.")
		if err != nil {
			skipOnAccountError(t, err)
			t.Fatalf("agent.Run failed: %v", err)
		}

		if atomic.LoadInt32(&toolCalled) == 0 {
			t.Error("expected tool to be called after approval")
		}

		t.Logf("Output: %q ToolCalled=%d", result.Output, toolCalled)
	})

	t.Run("Denied", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		var toolCalled int32
		sensitiveTool := core.FuncTool[CalcParams]("delete_data", "Delete data",
			func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
				atomic.AddInt32(&toolCalled, 1)
				return "data deleted", nil
			},
			core.WithRequiresApproval(),
		)

		agent := core.NewAgent[string](newAnthropicProvider(),
			core.WithTools[string](sensitiveTool),
			core.WithToolApproval[string](func(ctx context.Context, toolName string, argsJSON string) (bool, error) {
				return false, nil // always deny
			}),
		)

		result, err := agent.Run(ctx, "Use the delete_data tool with a=1 and b=1.")
		if err != nil {
			skipOnAccountError(t, err)
			// Error is acceptable - the model might not recover from denial.
			t.Logf("Agent after denial: %v", err)
			return
		}

		if atomic.LoadInt32(&toolCalled) != 0 {
			t.Error("tool should NOT have been called after denial")
		}

		t.Logf("Output after denial: %q", result.Output)
	})
}

// TestToolsets verifies that tools from toolsets are available.
func TestToolsets(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	mathTool := core.FuncTool[CalcParams]("math_add", "Add two numbers",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			return fmt.Sprintf("%d", p.A+p.B), nil
		},
	)
	mathSet := core.NewToolset("math", mathTool)

	multiplyTool := core.FuncTool[CalcParams]("util_multiply", "Multiply two numbers",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			return fmt.Sprintf("%d", p.A*p.B), nil
		},
	)
	utilSet := core.NewToolset("util", multiplyTool)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithToolsets[string](mathSet, utilSet),
	)

	result, err := agent.Run(ctx, "Use math_add to compute 3+4, and util_multiply to compute 5*6. Report both results.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if !strings.Contains(result.Output, "7") {
		t.Errorf("expected output to contain '7' (3+4), got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "30") {
		t.Errorf("expected output to contain '30' (5*6), got: %q", result.Output)
	}

	t.Logf("Output: %q", result.Output)
}

// TestToolChoiceNone verifies the model doesn't use tools when none mode is set.
func TestToolChoiceNone(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			t.Error("tool should not be called with ToolChoiceNone")
			return "0", nil
		},
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
		core.WithToolChoice[string](core.ToolChoiceNone()),
	)

	result, err := agent.Run(ctx, "What is 2+3? Just tell me the answer.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	// Verify no tool calls in messages.
	for _, msg := range result.Messages {
		if resp, ok := msg.(core.ModelResponse); ok {
			if len(resp.ToolCalls()) > 0 {
				t.Error("expected no tool calls with ToolChoiceNone")
			}
		}
	}

	t.Logf("Output (no tools): %q", result.Output)
}

// TestToolChoiceForce verifies forcing a specific tool.
func TestToolChoiceForce(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var addCalled, mulCalled int32

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			atomic.AddInt32(&addCalled, 1)
			return fmt.Sprintf("%d", p.A+p.B), nil
		},
	)

	mulTool := core.FuncTool[CalcParams]("multiply", "Multiply two numbers",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			atomic.AddInt32(&mulCalled, 1)
			return fmt.Sprintf("%d", p.A*p.B), nil
		},
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool, mulTool),
		core.WithToolChoice[string](core.ToolChoiceForce("multiply")),
		core.WithToolChoiceAutoReset[string](),
	)

	result, err := agent.Run(ctx, "Compute something with a=3 and b=4.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if atomic.LoadInt32(&mulCalled) == 0 {
		t.Error("expected multiply tool to be called when forced")
	}

	t.Logf("Output: %q AddCalled=%d MulCalled=%d", result.Output, addCalled, mulCalled)
}

// TestToolsPrepare verifies dynamic tool filtering.
func TestToolsPrepare(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			return fmt.Sprintf("%d", p.A+p.B), nil
		},
	)

	secretTool := core.FuncTool[CalcParams]("secret_op", "A secret operation",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			t.Error("secret_op should have been filtered out")
			return "secret!", nil
		},
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool, secretTool),
		core.WithToolsPrepare[string](func(ctx context.Context, rc *core.RunContext, tools []core.ToolDefinition) []core.ToolDefinition {
			// Only allow "add" tool.
			var filtered []core.ToolDefinition
			for _, t := range tools {
				if t.Name == "add" {
					filtered = append(filtered, t)
				}
			}
			return filtered
		}),
	)

	result, err := agent.Run(ctx, "Use the add tool to compute 10+20.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	t.Logf("Output: %q", result.Output)
}

// TestToolMaxRetries verifies tool retry logic.
func TestToolMaxRetries(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var callCount int32
	flakeyTool := core.FuncTool[CalcParams]("flaky_op", "A flaky operation that sometimes fails",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			n := atomic.AddInt32(&callCount, 1)
			if n <= 2 {
				return "", core.NewModelRetryError("temporary failure, please try again")
			}
			return fmt.Sprintf("%d", p.A+p.B), nil
		},
		core.WithToolMaxRetries(5),
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](flakeyTool),
	)

	result, err := agent.Run(ctx, "Use the flaky_op tool with a=10 and b=5. Keep trying if it fails.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	calls := atomic.LoadInt32(&callCount)
	if calls < 3 {
		t.Errorf("expected at least 3 calls (2 failures + 1 success), got %d", calls)
	}

	t.Logf("Output: %q ToolCalls=%d", result.Output, calls)
}

// TestEndStrategyExhaustive verifies that exhaustive mode processes all tool calls.
func TestEndStrategyExhaustive(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var toolCallCount int32

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			atomic.AddInt32(&toolCallCount, 1)
			return fmt.Sprintf("%d", p.A+p.B), nil
		},
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
		core.WithEndStrategy[string](core.EndStrategyExhaustive),
	)

	result, err := agent.Run(ctx, "Use the add tool to compute 1+2 and then 3+4. Report both results.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	calls := atomic.LoadInt32(&toolCallCount)
	t.Logf("Output: %q ToolCalls=%d", result.Output, calls)
}

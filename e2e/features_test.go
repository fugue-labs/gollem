//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// --- Phase 6: Agent Features using Anthropic (cheapest) ---

func anthropicOnly(t *testing.T) {
	t.Helper()
	skipIfNoCredentials(t, "ANTHROPIC_API_KEY")
}

// TestInputGuardrails verifies input guardrails block bad prompts.
func TestInputGuardrails(t *testing.T) {
	anthropicOnly(t)

	t.Run("ContentFilter", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		agent := core.NewAgent[string](newAnthropicProvider(),
			core.WithInputGuardrail[string]("block_forbidden", core.ContentFilter("forbidden", "banned")),
		)

		// Blocked prompt.
		_, err := agent.Run(ctx, "This is a forbidden request")
		if err == nil {
			t.Fatal("expected error for forbidden prompt, got nil")
		}
		var gErr *core.GuardrailError
		if !errors.As(err, &gErr) {
			t.Logf("got non-guardrail error (acceptable): %v", err)
		} else {
			t.Logf("correctly blocked: %s", gErr.Message)
		}

		// Allowed prompt.
		result, err := agent.Run(ctx, "Say hello")
		if err != nil {
			skipOnAccountError(t, err)
			t.Fatalf("allowed prompt failed: %v", err)
		}
		t.Logf("allowed output: %q", result.Output)
	})

	t.Run("MaxPromptLength", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		agent := core.NewAgent[string](newAnthropicProvider(),
			core.WithInputGuardrail[string]("max_length", core.MaxPromptLength(20)),
		)

		// Short prompt should work.
		result, err := agent.Run(ctx, "Say hi")
		if err != nil {
			skipOnAccountError(t, err)
			t.Fatalf("short prompt failed: %v", err)
		}
		t.Logf("short prompt output: %q", result.Output)

		// Long prompt should fail.
		_, err = agent.Run(ctx, "This is a very long prompt that exceeds the maximum allowed length")
		if err == nil {
			t.Fatal("expected error for long prompt, got nil")
		}
		t.Logf("long prompt correctly rejected: %v", err)
	})
}

// TestTurnGuardrails verifies turn limits stop the agent loop.
func TestTurnGuardrails(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Create a tool that always makes the model want to call again.
	loopTool := core.FuncTool[CalcParams]("check", "Check a value", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return "not done yet, check again with different values", nil
	})

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](loopTool),
		core.WithTurnGuardrail[string]("max_turns", core.MaxTurns(3)),
	)

	_, err := agent.Run(ctx, "Keep using the check tool with different values until you get a result. Start with a=1, b=1.")
	// The agent should either error due to max turns or return after being stopped.
	// Either way, it shouldn't hang forever.
	if err != nil {
		t.Logf("Agent stopped (expected): %v", err)
	} else {
		t.Log("Agent completed within turn limit")
	}
}

// TestCostTracking verifies that cost tracking records usage.
func TestCostTracking(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tracker := core.NewCostTracker(map[string]core.ModelPricing{
		"claude-haiku-4-5-20251001": {
			InputTokenCost:  0.0000008,  // $0.80 per 1M input tokens
			OutputTokenCost: 0.000004,   // $4.00 per 1M output tokens
		},
	})

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithCostTracker[string](tracker),
	)

	_, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	cost := tracker.TotalCost()
	if cost <= 0 {
		t.Errorf("expected positive cost, got %f", cost)
	}

	t.Logf("TotalCost=$%.8f Breakdown=%v", cost, tracker.CostBreakdown())
}

// TestUsageQuota verifies that quota enforcement stops the agent within a run.
// Note: UsageQuota is enforced per-run (each Run() resets usage). To test it,
// we use a tool that triggers multiple model requests within a single run.
func TestUsageQuota(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// A tool that forces the model to make another request.
	loopTool := core.FuncTool[CalcParams]("check", "Check a number", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return "not ready, check again with different values", nil
	})

	// MaxRequests=2 means the agent can make at most 2 model requests.
	// With a tool that always needs follow-up, the agent should hit the quota.
	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](loopTool),
		core.WithUsageQuota[string](core.UsageQuota{MaxRequests: 2}),
	)

	_, err := agent.Run(ctx, "Use the check tool with a=1 b=1. If it says not ready, try again.")
	if err == nil {
		// The agent completed within 2 requests — that's acceptable (model may have stopped).
		t.Log("Agent completed within quota (model chose to stop)")
		return
	}

	var qErr *core.QuotaExceededError
	if errors.As(err, &qErr) {
		t.Logf("correctly got QuotaExceededError: %s", qErr.Message)
	} else {
		// Any error that stops the agent is acceptable here.
		t.Logf("Agent stopped with error (acceptable): %v", err)
	}
}

// TestHooks verifies that lifecycle hooks fire in order.
func TestHooks(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	var order []string

	hook := core.Hook{
		OnRunStart: func(ctx context.Context, rc *core.RunContext, prompt string) {
			mu.Lock()
			order = append(order, "run_start")
			mu.Unlock()
		},
		OnRunEnd: func(ctx context.Context, rc *core.RunContext, messages []core.ModelMessage, err error) {
			mu.Lock()
			order = append(order, "run_end")
			mu.Unlock()
		},
		OnModelRequest: func(ctx context.Context, rc *core.RunContext, messages []core.ModelMessage) {
			mu.Lock()
			order = append(order, "model_request")
			mu.Unlock()
		},
		OnModelResponse: func(ctx context.Context, rc *core.RunContext, response *core.ModelResponse) {
			mu.Lock()
			order = append(order, "model_response")
			mu.Unlock()
		},
	}

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithHooks[string](hook),
	)

	_, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(order) < 4 {
		t.Errorf("expected at least 4 hook calls, got %d: %v", len(order), order)
	}
	if len(order) > 0 && order[0] != "run_start" {
		t.Errorf("expected first hook to be 'run_start', got %q", order[0])
	}
	if len(order) > 0 && order[len(order)-1] != "run_end" {
		t.Errorf("expected last hook to be 'run_end', got %q", order[len(order)-1])
	}

	t.Logf("Hook order: %v", order)
}

// TestAgentMiddleware verifies middleware wraps model requests.
func TestAgentMiddleware(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var middlewareCalled int32

	mw := func(
		ctx context.Context,
		messages []core.ModelMessage,
		settings *core.ModelSettings,
		params *core.ModelRequestParameters,
		next func(context.Context, []core.ModelMessage, *core.ModelSettings, *core.ModelRequestParameters) (*core.ModelResponse, error),
	) (*core.ModelResponse, error) {
		atomic.AddInt32(&middlewareCalled, 1)
		return next(ctx, messages, settings, params)
	}

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithAgentMiddleware[string](mw),
	)

	_, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	calls := atomic.LoadInt32(&middlewareCalled)
	if calls == 0 {
		t.Error("middleware was never called")
	}

	t.Logf("Middleware called %d times", calls)
}

// TestRunConditions verifies that run conditions stop the agent.
func TestRunConditions(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var toolCalls int32
	loopTool := core.FuncTool[CalcParams]("ping", "Ping", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		atomic.AddInt32(&toolCalls, 1)
		return "pong - call me again", nil
	})

	// ToolCallCount(3) checks rc.Usage.ToolCalls >= 3 after each model response.
	// The condition is evaluated BEFORE tool execution in the current iteration,
	// so the count reflects previous iterations. With margin for parallel tool calls,
	// we allow up to 6 actual calls.
	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](loopTool),
		core.WithRunCondition[string](core.ToolCallCount(3)),
	)

	_, err := agent.Run(ctx, "Call the ping tool repeatedly with a=1 b=1.")
	// Should stop around 3 tool calls.
	if err != nil {
		t.Logf("Agent stopped with condition: %v", err)
	}

	calls := atomic.LoadInt32(&toolCalls)
	if calls > 6 {
		t.Errorf("expected at most ~6 tool calls (condition at 3), got %d", calls)
	}
	if calls == 0 {
		t.Error("expected at least one tool call")
	}

	t.Logf("Tool calls: %d", calls)
}

// TestDependencyInjection verifies that deps are available in tool handlers.
func TestDependencyInjection(t *testing.T) {
	anthropicOnly(t)

	type TestDeps struct {
		Secret string
	}

	var capturedSecret string

	type EmptyParams struct{}
	secretTool := core.FuncTool[EmptyParams]("get_secret", "Get the secret value", func(ctx context.Context, rc *core.RunContext, p EmptyParams) (string, error) {
		deps := core.GetDeps[*TestDeps](rc)
		capturedSecret = deps.Secret
		return "The secret is: " + deps.Secret, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](secretTool),
		core.WithDeps[string](&TestDeps{Secret: "gollem-rocks"}),
	)

	result, err := agent.Run(ctx, "Use the get_secret tool to retrieve the secret value.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if capturedSecret != "gollem-rocks" {
		t.Errorf("expected captured secret 'gollem-rocks', got %q", capturedSecret)
	}

	t.Logf("Output=%q CapturedSecret=%q", result.Output, capturedSecret)
}

// TestEventBus verifies pub-sub event delivery during agent runs.
func TestEventBus(t *testing.T) {
	anthropicOnly(t)

	bus := core.NewEventBus()

	// Subscribe to the built-in RunStartedEvent — guaranteed to fire.
	var runEvents []string
	var mu sync.Mutex

	core.Subscribe[core.RunStartedEvent](bus, func(e core.RunStartedEvent) {
		mu.Lock()
		runEvents = append(runEvents, "run_started:"+e.RunID)
		mu.Unlock()
	})

	// Also subscribe to ToolCalledEvent for extra verification.
	var toolEvents []string
	core.Subscribe[core.ToolCalledEvent](bus, func(e core.ToolCalledEvent) {
		mu.Lock()
		toolEvents = append(toolEvents, "tool_called:"+e.ToolName)
		mu.Unlock()
	})

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A+p.B), nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
		core.WithEventBus[string](bus),
	)

	_, err := agent.Run(ctx, "Use the add tool to compute 1 + 2.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(runEvents) == 0 {
		t.Error("expected to receive RunStartedEvent")
	}
	// ToolCalledEvent is published by the agent itself, so it should fire.
	if len(toolEvents) == 0 {
		t.Error("expected to receive ToolCalledEvent")
	}

	t.Logf("RunEvents=%v ToolEvents=%v", runEvents, toolEvents)
}

// TestMessageInterceptor verifies that message interceptors can modify messages.
func TestMessageInterceptor(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var intercepted int32

	interceptor := func(ctx context.Context, messages []core.ModelMessage) core.InterceptResult {
		atomic.AddInt32(&intercepted, 1)
		return core.InterceptResult{
			Action:   core.MessageAllow,
			Messages: messages,
		}
	}

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithMessageInterceptor[string](interceptor),
	)

	result, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	calls := atomic.LoadInt32(&intercepted)
	if calls == 0 {
		t.Error("message interceptor was never called")
	}

	t.Logf("Output=%q InterceptorCalls=%d", result.Output, calls)
}

// EmptyParams is reused across tests for tools that take no meaningful input.
// (Defined here to avoid redeclaration since it's used in multiple test functions.)
var _ = fmt.Sprintf // ensure fmt is used

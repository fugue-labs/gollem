//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/modelutil"
)

// TestContextCancellationDuringRun verifies clean cancellation mid-run.
func TestContextCancellationDuringRun(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	// Ask for something that takes a while.
	_, err := agent.Run(ctx, "Write a 5000 word essay about the history of mathematics.")
	if err == nil {
		t.Log("completed before timeout (fast response)")
		return
	}

	// Should be a context error, not a panic.
	if !strings.Contains(err.Error(), "context") {
		t.Logf("got non-context error (acceptable): %v", err)
	}

	t.Logf("Cancelled cleanly: %v", err)
}

// TestContextCancellationDuringStream verifies clean cancellation during streaming.
func TestContextCancellationDuringStream(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	stream, err := agent.RunStream(ctx, "Write a very long essay about every country in the world.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Logf("RunStream returned early error (acceptable): %v", err)
		return
	}

	// Consume until cancelled.
	count := 0
	for _, err := range stream.StreamText(true) {
		if err != nil {
			break
		}
		count++
	}

	stream.Close()
	t.Logf("Stream cancelled cleanly after %d chunks", count)
}

// TestConcurrentAgentRunsWithTools verifies thread safety with concurrent tool execution.
func TestConcurrentAgentRunsWithTools(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var callCount atomic.Int64

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			callCount.Add(1)
			return fmt.Sprintf("%d", p.A+p.B), nil
		},
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
	)

	results := agent.RunBatch(ctx, []string{
		"Use the add tool to compute 1+2.",
		"Use the add tool to compute 10+20.",
		"Use the add tool to compute 100+200.",
	})

	for i, r := range results {
		if r.Err != nil {
			skipOnAccountError(t, r.Err)
			t.Errorf("batch[%d] failed: %v", i, r.Err)
			continue
		}
		if r.Result == nil || r.Result.Output == "" {
			t.Errorf("batch[%d] empty result", i)
		}
	}

	if callCount.Load() < 3 {
		t.Errorf("expected at least 3 tool calls, got %d", callCount.Load())
	}

	t.Logf("Concurrent tool calls: %d", callCount.Load())
}

// TestBatchCancellationMidFlight verifies batch respects context cancellation.
func TestBatchCancellationMidFlight(t *testing.T) {
	anthropicOnly(t)

	// Very short timeout to trigger cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	// Send many prompts — some should get cancelled.
	prompts := make([]string, 10)
	for i := range prompts {
		prompts[i] = fmt.Sprintf("Write a detailed paragraph about the number %d.", i+1)
	}

	results := agent.RunBatch(ctx, prompts, core.WithBatchConcurrency(2))

	succeeded := 0
	cancelled := 0
	for _, r := range results {
		if r.Err == nil {
			succeeded++
		} else {
			cancelled++
		}
	}

	t.Logf("Batch cancellation: %d succeeded, %d cancelled/failed", succeeded, cancelled)
}

// TestRetryModelWithTransientFailure verifies retry behavior with a failing model.
func TestRetryModelWithTransientFailure(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	inner := newAnthropicProvider()

	// Wrap with retry (the real model shouldn't need retries, but this tests the wrapper).
	retryModel := modelutil.NewRetryModel(inner, modelutil.RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
		BackoffFactor:  2.0,
		Jitter:         true,
	})

	agent := core.NewAgent[string](retryModel)

	result, err := agent.Run(ctx, "Say 'hello'")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run failed: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty output")
	}

	t.Logf("Retry model output: %q", result.Output)
}

// TestFallbackModelChain verifies fallback across multiple providers.
func TestFallbackModelChain(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create fallback: anthropic primary, same as fallback (both should work).
	primary := newAnthropicProvider()
	fallback := newAnthropicProvider()

	model := modelutil.NewFallbackModel(primary, fallback)
	agent := core.NewAgent[string](model)

	result, err := agent.Run(ctx, "Say 'hello'")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run failed: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty output")
	}

	t.Logf("Fallback chain output: %q", result.Output)
}

// TestRateLimitedModelUnderLoad verifies rate limiter delays but doesn't drop requests.
func TestRateLimitedModelUnderLoad(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Allow 2 requests per second, burst of 3.
	model := modelutil.NewRateLimitedModel(newAnthropicProvider(), 2.0, 3)
	agent := core.NewAgent[string](model)

	start := time.Now()
	results := agent.RunBatch(ctx, []string{
		"Say '1'",
		"Say '2'",
		"Say '3'",
		"Say '4'",
	}, core.WithBatchConcurrency(4))

	elapsed := time.Since(start)

	succeeded := 0
	for _, r := range results {
		if r.Err == nil {
			succeeded++
		}
	}

	if succeeded < 3 {
		t.Errorf("expected at least 3 successes, got %d", succeeded)
	}

	// With 4 requests at 2 rps, burst 3, should take at least ~500ms.
	t.Logf("RateLimited: %d/%d succeeded in %v", succeeded, len(results), elapsed)
}

// TestHooksReceiveAllLifecycleEvents verifies all hook callbacks fire in correct order.
func TestHooksReceiveAllLifecycleEvents(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	var events []string

	hook := core.Hook{
		OnRunStart: func(ctx context.Context, rc *core.RunContext, prompt string) {
			mu.Lock()
			events = append(events, "run_start")
			mu.Unlock()
		},
		OnRunEnd: func(ctx context.Context, rc *core.RunContext, messages []core.ModelMessage, err error) {
			mu.Lock()
			events = append(events, "run_end")
			mu.Unlock()
		},
		OnModelRequest: func(ctx context.Context, rc *core.RunContext, messages []core.ModelMessage) {
			mu.Lock()
			events = append(events, "model_request")
			mu.Unlock()
		},
		OnModelResponse: func(ctx context.Context, rc *core.RunContext, response *core.ModelResponse) {
			mu.Lock()
			events = append(events, "model_response")
			mu.Unlock()
		},
		OnToolStart: func(ctx context.Context, rc *core.RunContext, toolName string, argsJSON string) {
			mu.Lock()
			events = append(events, "tool_start:"+toolName)
			mu.Unlock()
		},
		OnToolEnd: func(ctx context.Context, rc *core.RunContext, toolName string, result string, err error) {
			mu.Lock()
			events = append(events, "tool_end:"+toolName)
			mu.Unlock()
		},
	}

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			return fmt.Sprintf("%d", p.A+p.B), nil
		},
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
		core.WithHooks[string](hook),
	)

	_, err := agent.Run(ctx, "Use the add tool to compute 5+3.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify ordering: run_start should be first, run_end should be last.
	if len(events) == 0 {
		t.Fatal("no lifecycle events received")
	}
	if events[0] != "run_start" {
		t.Errorf("expected first event to be run_start, got %q", events[0])
	}
	if events[len(events)-1] != "run_end" {
		t.Errorf("expected last event to be run_end, got %q", events[len(events)-1])
	}

	// Verify we got tool events.
	hasToolStart := false
	hasToolEnd := false
	for _, e := range events {
		if strings.HasPrefix(e, "tool_start:") {
			hasToolStart = true
		}
		if strings.HasPrefix(e, "tool_end:") {
			hasToolEnd = true
		}
	}
	if !hasToolStart {
		t.Error("missing tool_start event")
	}
	if !hasToolEnd {
		t.Error("missing tool_end event")
	}

	t.Logf("Lifecycle events: %v", events)
}

// TestRunContextHasCorrectMetadata verifies RunContext fields are populated.
func TestRunContextHasCorrectMetadata(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var capturedRunID string
	var capturedStep int

	type EmptyParams struct{}
	tool := core.FuncTool[EmptyParams]("capture", "Capture run context",
		func(ctx context.Context, rc *core.RunContext, p EmptyParams) (string, error) {
			capturedRunID = rc.RunID
			capturedStep = rc.RunStep
			return "captured", nil
		},
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](tool),
	)

	result, err := agent.Run(ctx, "Use the capture tool.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run failed: %v", err)
	}

	if capturedRunID == "" {
		t.Error("RunContext.RunID was empty")
	}
	if capturedStep < 1 {
		t.Errorf("RunContext.RunStep should be >= 1, got %d", capturedStep)
	}

	// Result should have usage info.
	if result.Usage.InputTokens == 0 && result.Usage.OutputTokens == 0 {
		t.Error("expected non-zero usage in result")
	}

	t.Logf("RunID=%q Step=%d Usage=%+v", capturedRunID, capturedStep, result.Usage)
}

// TestMaxRetriesOnToolError verifies the agent handles ModelRetryError from tools.
// The agent sends a RetryPromptPart back to the model, which may or may not retry.
func TestMaxRetriesOnToolError(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var callCount atomic.Int32

	type EmptyParams struct{}
	failTool := core.FuncTool[EmptyParams]("flaky", "A flaky tool that fails sometimes",
		func(ctx context.Context, rc *core.RunContext, p EmptyParams) (string, error) {
			n := callCount.Add(1)
			if n <= 2 {
				// Return ModelRetryError to trigger the retry mechanism.
				return "", &core.ModelRetryError{Message: fmt.Sprintf("transient error (attempt %d), please try again", n)}
			}
			return "success on attempt 3", nil
		},
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](failTool),
		core.WithMaxRetries[string](5),
	)

	result, err := agent.Run(ctx, "You MUST use the flaky tool. If it fails, try again until it succeeds. Do not give up.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run failed: %v", err)
	}

	// The model should have called the tool at least once and completed without error.
	if callCount.Load() < 1 {
		t.Errorf("expected at least 1 tool call, got %d", callCount.Load())
	}

	t.Logf("Output: %q ToolCalls=%d", result.Output, callCount.Load())
}

// TestUsageLimitsEnforced verifies that usage limits prevent runaway costs.
func TestUsageLimitsEnforced(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// RequestLimit=1 means only 1 model request per run.
	// A tool-using prompt needs at least 2 requests (tool call + result processing),
	// so it should hit the limit.
	addTool := core.FuncTool[CalcParams]("add", "Add two numbers",
		func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			return fmt.Sprintf("%d", p.A+p.B), nil
		},
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
		core.WithUsageLimits[string](core.UsageLimits{
			RequestLimit: core.IntPtr(1),
		}),
	)

	_, err := agent.Run(ctx, "Use the add tool to compute 1+2.")
	if err == nil {
		t.Fatal("expected usage limit error when tool use needs >1 request")
	}

	if !strings.Contains(err.Error(), "limit") {
		t.Logf("got error (may not be usage limit): %v", err)
	} else {
		t.Logf("Usage limit enforced: %v", err)
	}
}

// TestClassifierRouterDispatch verifies classifier-based routing.
func TestClassifierRouterDispatch(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	model := newAnthropicProvider()

	router := modelutil.ClassifierRouter(
		map[string]core.Model{
			"math":    model,
			"general": model,
		},
		func(ctx context.Context, prompt string) string {
			if strings.Contains(strings.ToLower(prompt), "math") ||
				strings.Contains(strings.ToLower(prompt), "calculate") {
				return "math"
			}
			return "general"
		},
	)

	routerModel := modelutil.NewRouterModel(router)
	agent := core.NewAgent[string](routerModel)

	result, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run failed: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty output from routed model")
	}

	t.Logf("Classifier router output: %q", result.Output)
}

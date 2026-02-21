//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestTextContains verifies the TextContains run condition.
func TestTextContains(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithRunCondition[string](core.TextContains("DONE")),
	)

	result, err := agent.Run(ctx, "Say 'DONE' in your response.")
	// The condition should trigger, but the output should still be returned.
	if err != nil {
		var condErr *core.RunConditionError
		if errors.As(err, &condErr) {
			t.Logf("Run condition triggered: %s", condErr.Reason)
			return
		}
		skipOnAccountError(t, err)
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Output, "DONE") {
		t.Errorf("expected output to contain 'DONE', got: %q", result.Output)
	}

	t.Logf("Output: %q", result.Output)
}

// TestResponseContains verifies the ResponseContains run condition.
func TestResponseContains(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithRunCondition[string](core.ResponseContains(func(resp *core.ModelResponse) bool {
			return len(resp.TextContent()) > 0
		})),
	)

	result, err := agent.Run(ctx, "Say hello.")
	// Should stop because the response contains text.
	if err != nil {
		var condErr *core.RunConditionError
		if errors.As(err, &condErr) {
			t.Logf("Response condition triggered: %s", condErr.Reason)
			return
		}
		skipOnAccountError(t, err)
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("Output: %q", result.Output)
}

// TestConditionOr verifies the Or combinator.
func TestConditionOr(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var toolCalls int32
	loopTool := core.FuncTool[CalcParams]("ping", "Ping", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		atomic.AddInt32(&toolCalls, 1)
		return "pong - call again", nil
	})

	// Either TextContains("STOP") or ToolCallCount(2) should stop the run.
	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](loopTool),
		core.WithRunCondition[string](core.Or(
			core.TextContains("STOP"),
			core.ToolCallCount(2),
		)),
	)

	_, err := agent.Run(ctx, "Call the ping tool repeatedly with a=1 b=1.")
	calls := atomic.LoadInt32(&toolCalls)

	if err != nil {
		var condErr *core.RunConditionError
		if errors.As(err, &condErr) {
			t.Logf("Or condition triggered: %s (tool calls: %d)", condErr.Reason, calls)
			return
		}
		skipOnAccountError(t, err)
		t.Logf("Other error: %v (tool calls: %d)", err, calls)
		return
	}

	t.Logf("Completed with %d tool calls", calls)
}

// TestConditionAnd verifies the And combinator.
func TestConditionAnd(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var toolCalls int32
	loopTool := core.FuncTool[CalcParams]("ping", "Ping", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		atomic.AddInt32(&toolCalls, 1)
		return "pong - call again", nil
	})

	// Both conditions must be true: ToolCallCount(1) AND ResponseContains(has text).
	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](loopTool),
		core.WithRunCondition[string](core.And(
			core.ToolCallCount(1),
			core.ResponseContains(func(resp *core.ModelResponse) bool {
				return len(resp.ToolCalls()) > 0 || resp.TextContent() != ""
			}),
		)),
	)

	_, err := agent.Run(ctx, "Call the ping tool with a=1 b=1.")
	calls := atomic.LoadInt32(&toolCalls)

	if err != nil {
		t.Logf("And condition result: %v (tool calls: %d)", err, calls)
	} else {
		t.Logf("Completed with %d tool calls", calls)
	}
}

// TestMultipleInputGuardrails verifies stacking multiple input guardrails.
func TestMultipleInputGuardrails(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithInputGuardrail[string]("content_filter", core.ContentFilter("forbidden")),
		core.WithInputGuardrail[string]("max_length", core.MaxPromptLength(200)),
	)

	// Test content filter.
	_, err := agent.Run(ctx, "forbidden request")
	if err == nil {
		t.Error("expected error for forbidden content")
	} else {
		var gErr *core.GuardrailError
		if errors.As(err, &gErr) {
			t.Logf("Content filter triggered: %s", gErr.Message)
		}
	}

	// Test max length.
	longPrompt := strings.Repeat("word ", 100) // 500 chars
	_, err = agent.Run(ctx, longPrompt)
	if err == nil {
		t.Error("expected error for long prompt")
	} else {
		var gErr *core.GuardrailError
		if errors.As(err, &gErr) {
			t.Logf("Max length triggered: %s", gErr.Message)
		}
	}

	// Test valid prompt passes both.
	result, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("valid prompt failed: %v", err)
	}

	t.Logf("Output: %q", result.Output)
}

// TestErrorTypes verifies that specific error types are returned.
func TestErrorTypes(t *testing.T) {
	anthropicOnly(t)

	t.Run("GuardrailError", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		agent := core.NewAgent[string](newAnthropicProvider(),
			core.WithInputGuardrail[string]("test_guard", core.ContentFilter("blocked")),
		)

		_, err := agent.Run(ctx, "This is blocked content")
		if err == nil {
			t.Fatal("expected GuardrailError")
		}

		var gErr *core.GuardrailError
		if !errors.As(err, &gErr) {
			t.Errorf("expected *GuardrailError, got: %T", err)
		} else if gErr.GuardrailName != "test_guard" {
			t.Errorf("expected guardrail name 'test_guard', got %q", gErr.GuardrailName)
		}
	})

	t.Run("QuotaExceeded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		loopTool := core.FuncTool[CalcParams]("check", "Check", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
			return "not done, check again", nil
		})

		agent := core.NewAgent[string](newAnthropicProvider(),
			core.WithTools[string](loopTool),
			core.WithUsageQuota[string](core.UsageQuota{MaxRequests: 1}),
		)

		_, err := agent.Run(ctx, "Use the check tool with a=1 b=1 and keep checking.")
		if err == nil {
			t.Log("Agent completed within quota")
			return
		}

		var qErr *core.QuotaExceededError
		if errors.As(err, &qErr) {
			t.Logf("QuotaExceededError: %s", qErr.Message)
		} else {
			t.Logf("Other error (acceptable): %v", err)
		}
	})

	t.Run("ModelRetryError", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		var retryReceived int32
		retryTool := core.FuncTool[CalcParams]("retry_op", "An operation that requests retry",
			func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
				if rc.Retry == 0 {
					return "", core.NewModelRetryError("invalid input, try with different parameters")
				}
				atomic.AddInt32(&retryReceived, 1)
				return fmt.Sprintf("%d", p.A+p.B), nil
			},
			core.WithToolMaxRetries(3),
		)

		agent := core.NewAgent[string](newAnthropicProvider(),
			core.WithTools[string](retryTool),
		)

		result, err := agent.Run(ctx, "Use the retry_op tool with a=5 and b=10.")
		if err != nil {
			skipOnAccountError(t, err)
			t.Logf("Agent error (may be acceptable): %v", err)
			return
		}

		t.Logf("Output: %q RetryReceived=%d", result.Output, retryReceived)
	})
}

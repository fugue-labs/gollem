package core

import (
	"context"
	"strings"
	"time"
)

// RunCondition is a predicate checked after each model response.
// Return true to stop the run, with an optional reason message.
type RunCondition func(ctx context.Context, rc *RunContext, resp *ModelResponse) (stop bool, reason string)

// Or combines conditions — stop if ANY condition is met.
func Or(conditions ...RunCondition) RunCondition {
	return func(ctx context.Context, rc *RunContext, resp *ModelResponse) (bool, string) {
		for _, cond := range conditions {
			if stop, reason := cond(ctx, rc, resp); stop {
				return true, reason
			}
		}
		return false, ""
	}
}

// And combines conditions — stop only if ALL conditions are met.
func And(conditions ...RunCondition) RunCondition {
	return func(ctx context.Context, rc *RunContext, resp *ModelResponse) (bool, string) {
		var reasons []string
		for _, cond := range conditions {
			stop, reason := cond(ctx, rc, resp)
			if !stop {
				return false, ""
			}
			if reason != "" {
				reasons = append(reasons, reason)
			}
		}
		return true, strings.Join(reasons, "; ")
	}
}

// MaxRunDuration stops the run after a time limit.
func MaxRunDuration(d time.Duration) RunCondition {
	start := time.Now()
	return func(_ context.Context, _ *RunContext, _ *ModelResponse) (bool, string) {
		if time.Since(start) > d {
			return true, "max run duration exceeded"
		}
		return false, ""
	}
}

// TextContains stops when the model's text response contains a substring.
func TextContains(substr string) RunCondition {
	return func(_ context.Context, _ *RunContext, resp *ModelResponse) (bool, string) {
		if strings.Contains(resp.TextContent(), substr) {
			return true, "text contains " + substr
		}
		return false, ""
	}
}

// ToolCallCount stops after max total tool calls.
func ToolCallCount(max int) RunCondition {
	return func(_ context.Context, rc *RunContext, _ *ModelResponse) (bool, string) {
		if rc.Usage.ToolCalls >= max {
			return true, "tool call count limit reached"
		}
		return false, ""
	}
}

// ResponseContains stops when any response matches the predicate.
func ResponseContains(fn func(*ModelResponse) bool) RunCondition {
	return func(_ context.Context, _ *RunContext, resp *ModelResponse) (bool, string) {
		if fn(resp) {
			return true, "response predicate matched"
		}
		return false, ""
	}
}

// WithRunCondition adds a run condition to the agent.
func WithRunCondition[T any](cond RunCondition) AgentOption[T] {
	return func(a *Agent[T]) {
		a.runConditions = append(a.runConditions, cond)
	}
}

// RunConditionError indicates an agent run was stopped by a run condition.
type RunConditionError struct {
	Reason string
}

func (e *RunConditionError) Error() string {
	return "run stopped: " + e.Reason
}

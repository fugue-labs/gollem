package core

import "context"

// Hook provides callback functions for agent lifecycle events.
// Set only the fields you need — nil fields are skipped.
type Hook struct {
	// OnRunStart fires when an agent run begins.
	OnRunStart func(ctx context.Context, rc *RunContext, prompt string)
	// OnRunEnd fires when an agent run completes (success or error).
	OnRunEnd func(ctx context.Context, rc *RunContext, messages []ModelMessage, err error)
	// OnModelRequest fires before each model request.
	OnModelRequest func(ctx context.Context, rc *RunContext, messages []ModelMessage)
	// OnModelResponse fires after each model response.
	OnModelResponse func(ctx context.Context, rc *RunContext, response *ModelResponse)
	// OnToolStart fires before a tool executes.
	OnToolStart func(ctx context.Context, rc *RunContext, toolName string, argsJSON string)
	// OnToolEnd fires after a tool completes.
	OnToolEnd func(ctx context.Context, rc *RunContext, toolName string, result string, err error)
}

// WithHooks adds lifecycle hooks to the agent. Multiple hooks can be added;
// all fire in registration order.
func WithHooks[T any](hooks ...Hook) AgentOption[T] {
	return func(a *Agent[T]) {
		a.hooks = append(a.hooks, hooks...)
	}
}

// fireHook calls fn for each registered hook. Nil fields within each Hook
// are checked by the caller (via the field-specific closure).
func (a *Agent[T]) fireHook(fn func(Hook)) {
	for _, h := range a.hooks {
		fn(h)
	}
}

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
	OnToolStart func(ctx context.Context, rc *RunContext, toolCallID string, toolName string, argsJSON string)
	// OnToolEnd fires after a tool completes.
	OnToolEnd func(ctx context.Context, rc *RunContext, toolCallID string, toolName string, result string, err error)
	// OnTurnStart fires at the beginning of each agent loop iteration (turn).
	OnTurnStart func(ctx context.Context, rc *RunContext, turnNumber int)
	// OnTurnEnd fires at the end of each agent loop iteration (turn).
	OnTurnEnd func(ctx context.Context, rc *RunContext, turnNumber int, response *ModelResponse)
	// OnGuardrailEvaluated fires after a guardrail (input or turn) is evaluated.
	OnGuardrailEvaluated func(ctx context.Context, rc *RunContext, name string, passed bool, err error)
	// OnOutputValidation fires after structured output validation completes.
	OnOutputValidation func(ctx context.Context, rc *RunContext, passed bool, err error)
	// OnOutputRepair fires when output repair is attempted.
	OnOutputRepair func(ctx context.Context, rc *RunContext, succeeded bool, err error)
	// OnRunConditionChecked fires when a run condition is evaluated and stops the run.
	OnRunConditionChecked func(ctx context.Context, rc *RunContext, stopped bool, reason string)
	// OnContextCompaction fires when the message history is compressed to
	// fit within the context window. This includes auto-summarization
	// (AutoContext) and emergency truncation (ContextOverflowMiddleware).
	OnContextCompaction func(ctx context.Context, rc *RunContext, stats ContextCompactionStats)
}

// Compaction strategy constants identify which mechanism performed the compression.
const (
	CompactionStrategyAutoSummary         = "auto_summary"
	CompactionStrategyHistoryProcessor    = "history_processor"
	CompactionStrategyEmergencyTruncation = "emergency_truncation"
)

// ContextCompactionStats captures before/after state of a context compaction event.
type ContextCompactionStats struct {
	// Strategy identifies the compaction mechanism (use CompactionStrategy* constants).
	Strategy string

	// MessagesBefore is the message count before compaction.
	MessagesBefore int
	// MessagesAfter is the message count after compaction.
	MessagesAfter int

	// EstimatedTokensBefore is the estimated token count before compaction.
	EstimatedTokensBefore int
	// EstimatedTokensAfter is the estimated token count after compaction.
	EstimatedTokensAfter int
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

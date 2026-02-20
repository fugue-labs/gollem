package core

import "time"

// RunTrace captures the full execution trace of an agent run.
type RunTrace struct {
	RunID     string        `json:"run_id"`
	Prompt    string        `json:"prompt"`
	StartTime time.Time     `json:"start_time"`
	EndTime   time.Time     `json:"end_time"`
	Duration  time.Duration `json:"duration"`
	Steps     []TraceStep   `json:"steps"`
	Usage     RunUsage      `json:"usage"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
}

// TraceStep captures a single step in the agent execution.
type TraceStep struct {
	Kind      TraceStepKind `json:"kind"`
	Timestamp time.Time     `json:"timestamp"`
	Duration  time.Duration `json:"duration"`
	Data      any           `json:"data"`
}

// TraceStepKind identifies the type of trace step.
type TraceStepKind string

const (
	TraceModelRequest  TraceStepKind = "model_request"
	TraceModelResponse TraceStepKind = "model_response"
	TraceToolCall      TraceStepKind = "tool_call"
	TraceToolResult    TraceStepKind = "tool_result"
	TraceGuardrail     TraceStepKind = "guardrail"
)

// WithTracing enables execution tracing. The trace is available
// on the RunResult after the run completes.
func WithTracing[T any]() AgentOption[T] {
	return func(a *Agent[T]) {
		a.tracingEnabled = true
	}
}

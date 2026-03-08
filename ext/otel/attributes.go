// Package otel provides comprehensive OpenTelemetry tracing for Gollem agents.
//
// It produces hierarchical span trees that capture the full execution of
// multi-agent runs, including subagent delegation, tool execution, guardrail
// checks, and structured output validation.
package otel

// Semantic attribute keys for Gollem OTEL spans.
const (
	// Agent attributes.
	AttrAgentName        = "gollem.agent.name"
	AttrAgentPrompt      = "gollem.agent.prompt"
	AttrAgentRunID       = "gollem.agent.run_id"
	AttrAgentTurns       = "gollem.agent.turns"
	AttrAgentRunDuration = "gollem.agent.run_duration_ms"

	// Model attributes (aligned with OTEL GenAI semconv).
	AttrModelName    = "gen_ai.request.model"
	AttrModelSystem  = "gen_ai.system"
	AttrInputTokens  = "gen_ai.usage.input_tokens"
	AttrOutputTokens = "gen_ai.usage.output_tokens"
	AttrTemperature  = "gen_ai.request.temperature"
	AttrFinishReason = "gen_ai.response.finish_reason"
	AttrMessageCount = "gen_ai.request.message_count"

	// Tool attributes.
	AttrToolName     = "gollem.tool.name"
	AttrToolArgs     = "gollem.tool.args"
	AttrToolResult   = "gollem.tool.result"
	AttrToolDuration = "gollem.tool.duration_ms"
	AttrToolError    = "gollem.tool.error"

	// Cost attributes.
	AttrCostTotal        = "gollem.cost.total"
	AttrCostInputTokens  = "gollem.cost.input_tokens"
	AttrCostOutputTokens = "gollem.cost.output_tokens"

	// Token usage totals.
	AttrTotalInputTokens  = "gollem.usage.total_input_tokens"
	AttrTotalOutputTokens = "gollem.usage.total_output_tokens"
	AttrTotalRequests     = "gollem.usage.total_requests"
	AttrTotalToolCalls    = "gollem.usage.total_tool_calls"

	// Guardrail attributes.
	AttrGuardrailName   = "gollem.guardrail.name"
	AttrGuardrailPassed = "gollem.guardrail.passed"
	AttrGuardrailError  = "gollem.guardrail.error"

	// Structured output attributes.
	AttrOutputValid    = "gollem.output.valid"
	AttrOutputRepaired = "gollem.output.repaired"

	// Run condition attributes.
	AttrRunConditionStopped = "gollem.run_condition.stopped"
	AttrRunConditionReason  = "gollem.run_condition.reason"

	// Turn attributes.
	AttrTurnNumber = "gollem.turn.number"

	// Pipeline/orchestration attributes.
	AttrPipelineStepName  = "gollem.pipeline.step_name"
	AttrPipelineStepIndex = "gollem.pipeline.step_index"
	AttrHandoffStepName   = "gollem.handoff.step_name"
	AttrHandoffStepIndex  = "gollem.handoff.step_index"

	// Context compaction attributes.
	AttrCompactionStrategy     = "gollem.compaction.strategy"
	AttrCompactionMsgsBefore   = "gollem.compaction.messages_before"
	AttrCompactionMsgsAfter    = "gollem.compaction.messages_after"
	AttrCompactionTokensBefore = "gollem.compaction.estimated_tokens_before"
	AttrCompactionTokensAfter  = "gollem.compaction.estimated_tokens_after"
)

// Span names used by the tracing system.
const (
	SpanAgentRun          = "agent.run"
	SpanAgentTurn         = "agent.turn"
	SpanModelRequest      = "model.request"
	SpanToolExecute       = "tool.execute"
	SpanGuardrail         = "guardrail"
	SpanOutputValidation  = "output.validation"
	SpanOutputRepair      = "output.repair"
	SpanRunCondition      = "run_condition"
	SpanContextCompaction = "context.compaction"
	SpanPipelineRun       = "pipeline.run"
	SpanPipelineStep      = "pipeline.step"
	SpanHandoffRun        = "handoff.run"
	SpanHandoffStep       = "handoff.step"
)

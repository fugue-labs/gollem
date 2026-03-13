package temporal

import (
	"fmt"
	"strings"

	"github.com/fugue-labs/gollem/core"
)

// CompatibilityIssue describes one agent feature that the current Temporal
// integration cannot faithfully support yet.
type CompatibilityIssue struct {
	Feature string
	Message string
}

// CompatibilityReport summarizes whether an agent can be wrapped by the
// current Temporal integration.
type CompatibilityReport struct {
	Issues []CompatibilityIssue
}

// Supported reports whether the agent only uses features that the current
// Temporal integration can handle.
func (r CompatibilityReport) Supported() bool {
	return len(r.Issues) == 0
}

// Error summarizes all unsupported features.
func (r CompatibilityReport) Error() string {
	if r.Supported() {
		return ""
	}

	lines := make([]string, 0, len(r.Issues))
	for _, issue := range r.Issues {
		lines = append(lines, issue.Feature+": "+issue.Message)
	}
	return "gollem/temporal: unsupported agent features for the current Temporal integration: " + strings.Join(lines, "; ")
}

// CompatibilityReportFor builds a conservative compatibility report for the
// current Temporal integration. The report reflects what ext/temporal actually
// supports today, not future planned workflow behavior.
func CompatibilityReportFor[T any](agent *core.Agent[T]) CompatibilityReport {
	if agent == nil {
		return CompatibilityReport{
			Issues: []CompatibilityIssue{{
				Feature: "agent",
				Message: "nil agent",
			}},
		}
	}

	features := agent.ExecutionFeatures()
	var issues []CompatibilityIssue

	addCountIssue := func(feature string, count int, message string) {
		if count == 0 {
			return
		}
		issues = append(issues, CompatibilityIssue{
			Feature: feature,
			Message: fmt.Sprintf("%s (%d configured)", message, count),
		})
	}
	addBoolIssue := func(feature string, enabled bool, message string) {
		if !enabled {
			return
		}
		issues = append(issues, CompatibilityIssue{
			Feature: feature,
			Message: message,
		})
	}

	addCountIssue("toolsets", features.Toolsets, "toolset tools are not exported as Temporal activities")
	addCountIssue("dynamic_system_prompts", features.DynamicSystemPrompts, "dynamic system prompts still execute in-process and are not replay-safe")
	addCountIssue("history_processors", features.HistoryProcessors, "history processors still execute in-process and are not replay-safe")
	addBoolIssue("agent_tools_prepare", features.HasAgentToolsPrepare, "agent-wide tool preparation still executes in-process and is not replay-safe")
	addCountIssue("tool_prepare_funcs", features.ToolsWithPrepareFunc, "tool prepare callbacks still execute in-process and are not replay-safe")
	addCountIssue("tool_result_validators", features.ToolsWithResultValidator, "per-tool result validators still execute in-process and are not replay-safe")
	addCountIssue("tools_requiring_approval", features.ToolsRequiringApproval, "tool approval flows are not integrated with Temporal yet")
	addCountIssue("hooks", features.Hooks, "hooks may perform side effects and are not replay-safe in the current Temporal integration")
	addCountIssue("input_guardrails", features.InputGuardrails, "input guardrails still execute in-process and are not replay-safe")
	addCountIssue("turn_guardrails", features.TurnGuardrails, "turn guardrails still execute in-process and are not replay-safe")
	addCountIssue("output_validators", features.OutputValidators, "output validators still execute in-process and are not replay-safe")
	addBoolIssue("output_repair", features.HasOutputRepair, "output repair still executes in-process and is not replay-safe")
	addCountIssue("global_tool_result_validators", features.GlobalToolResultValidators, "global tool result validators still execute in-process and are not replay-safe")
	addCountIssue("run_conditions", features.RunConditions, "run conditions are not validated for workflow safety and are unsupported today")
	addCountIssue("trace_exporters", features.TraceExporters, "trace exporters may perform side effects and are not replay-safe")
	addBoolIssue("event_bus", features.HasEventBus, "event bus publishing is not integrated with Temporal replay semantics")
	addBoolIssue("agent_deps", features.HasAgentDeps, "agent-level dependencies are not serialized for Temporal activity execution")
	addCountIssue("message_interceptors", features.MessageInterceptors, "message interceptors still execute in-process and are not replay-safe")
	addCountIssue("response_interceptors", features.ResponseInterceptors, "response interceptors still execute in-process and are not replay-safe")
	addBoolIssue("knowledge_base", features.HasKnowledgeBase, "knowledge base retrieval/storage is not integrated with Temporal activities yet")
	addBoolIssue("knowledge_auto_store", features.HasKnowledgeAutoStore, "knowledge auto-store is not integrated with Temporal activities yet")
	addBoolIssue("cost_tracker", features.HasCostTracker, "cost trackers capture mutable process-local state and are unsupported today")
	addBoolIssue("auto_context", features.HasAutoContext, "auto-context compression may invoke models in-process and is unsupported today")
	addCountIssue("request_middleware", features.RequestMiddleware, "request middleware still executes in-process and is not replay-safe")
	addCountIssue("stream_middleware", features.StreamMiddleware, "stream middleware still executes in-process and is not replay-safe")

	return CompatibilityReport{Issues: issues}
}

// ValidateCompatibility returns an error when an agent uses features that the
// current Temporal integration cannot support yet.
func ValidateCompatibility[T any](agent *core.Agent[T]) error {
	report := CompatibilityReportFor(agent)
	if report.Supported() {
		return nil
	}
	return report
}

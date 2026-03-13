package temporal

import (
	"sort"
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
	return "gollem/temporal: unsupported features or configuration for the current Temporal integration: " + strings.Join(lines, "; ")
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

	_ = agent.ExecutionFeatures()
	return CompatibilityReport{}
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

func validateAgentConfig(cfg *agentConfig) error {
	if cfg == nil {
		return nil
	}

	report := CompatibilityReport{}
	if len(cfg.passthroughTools) > 0 {
		names := make([]string, 0, len(cfg.passthroughTools))
		for name := range cfg.passthroughTools {
			names = append(names, name)
		}
		sort.Strings(names)
		report.Issues = append(report.Issues, CompatibilityIssue{
			Feature: "WithToolPassthrough",
			Message: "passthrough tools are not supported by the built-in durable workflow: " + strings.Join(names, ", "),
		})
	}

	if report.Supported() {
		return nil
	}
	return report
}

package temporal

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fugue-labs/gollem/core"
)

// toolParams is the serializable parameter for tool call activities.
type toolParams struct {
	ArgsJSON   string `json:"args_json"`
	ToolCallID string `json:"tool_call_id"`
}

// toolResult wraps a tool call result for activity serialization.
type toolResult struct {
	Kind    string          `json:"kind"` // "return", "retry", "error"
	Value   json.RawMessage `json:"value,omitempty"`
	Message string          `json:"message,omitempty"`
}

// TemporalizeTool wraps a core.Tool's handler to execute as a Temporal activity.
// The returned tool has an activity function that can be registered with a worker.
func TemporalizeTool(agentName string, tool core.Tool, config ActivityConfig) TemporalTool {
	actName := fmt.Sprintf("agent__%s__tool__%s", agentName, tool.Definition.Name)

	activityFn := func(ctx context.Context, params toolParams) (*toolResult, error) {
		rc := &core.RunContext{
			ToolName:   tool.Definition.Name,
			ToolCallID: params.ToolCallID,
		}

		result, err := tool.Handler(ctx, rc, params.ArgsJSON)
		if err != nil {
			// Check if it's a retry error.
			if retryErr, ok := err.(*core.ModelRetryError); ok { //nolint:errorlint // type switch on concrete error type
				return &toolResult{
					Kind:    "retry",
					Message: retryErr.Error(),
				}, nil
			}
			return &toolResult{
				Kind:    "error",
				Message: err.Error(),
			}, nil
		}

		data, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("marshaling tool result: %w", err)
		}
		return &toolResult{
			Kind:  "return",
			Value: data,
		}, nil
	}

	return TemporalTool{
		Tool:         tool,
		ActivityName: actName,
		ActivityFn:   activityFn,
		Config:       config,
	}
}

// TemporalTool wraps a tool with its Temporal activity function.
type TemporalTool struct {
	Tool         core.Tool
	ActivityName string
	ActivityFn   func(ctx context.Context, params toolParams) (*toolResult, error)
	Config       ActivityConfig
}

// TemporalizeTools wraps multiple tools.
func TemporalizeTools(agentName string, tools []core.Tool, config ActivityConfig) []TemporalTool {
	result := make([]TemporalTool, len(tools))
	for i, tool := range tools {
		result[i] = TemporalizeTool(agentName, tool, config)
	}
	return result
}

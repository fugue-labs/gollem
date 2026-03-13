package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// ToolActivityInput is the serializable parameter for tool call activities.
type ToolActivityInput struct {
	ArgsJSON        string                   `json:"args_json"`
	ToolCallID      string                   `json:"tool_call_id"`
	Prompt          string                   `json:"prompt"`
	DepsJSON        []byte                   `json:"deps_json,omitempty"`
	RunStep         int                      `json:"run_step"`
	RunID           string                   `json:"run_id"`
	RunStartTime    time.Time                `json:"run_start_time"`
	Usage           core.RunUsage            `json:"usage"`
	LastInputTokens int                      `json:"last_input_tokens,omitempty"`
	Retries         int                      `json:"retries,omitempty"`
	ToolRetries     map[string]int           `json:"tool_retries,omitempty"`
	Retry           int                      `json:"retry"`
	MaxRetries      int                      `json:"max_retries"`
	Messages        []core.SerializedMessage `json:"messages,omitempty"`
	MessagesJSON    json.RawMessage          `json:"messages_json,omitempty"` // Deprecated: prefer Messages.
	ToolState       map[string]any           `json:"tool_state,omitempty"`
}

// ToolActivityOutput wraps a tool call result for activity serialization.
type ToolActivityOutput struct {
	Kind             string           `json:"kind"` // "return", "retry", "error", "deferred"
	Content          string           `json:"content,omitempty"`
	Images           []core.ImagePart `json:"images,omitempty"`
	Message          string           `json:"message,omitempty"`
	HasUpdatedState  bool             `json:"has_updated_state,omitempty"`
	UpdatedToolState any              `json:"updated_tool_state,omitempty"`
}

// TemporalizeTool wraps a core.Tool's handler to execute as a Temporal activity.
// The returned tool has an activity function that can be registered with a worker.
func TemporalizeTool(agentName string, tool core.Tool, config ActivityConfig) TemporalTool {
	return temporalizeTool(agentName, tool, config, 0, nil, nil, nil)
}

func temporalizeTool(
	agentName string,
	tool core.Tool,
	config ActivityConfig,
	defaultTimeout time.Duration,
	globalValidators []core.ToolResultValidatorFunc,
	resolveDeps func([]byte) (any, error),
	eventBus *core.EventBus,
) TemporalTool {
	actName := fmt.Sprintf("agent__%s__tool__%s", agentName, tool.Definition.Name)
	var stateMu sync.Mutex

	activityFn := func(ctx context.Context, input ToolActivityInput) (*ToolActivityOutput, error) {
		messages, err := decodeSerializedMessages(input.Messages, input.MessagesJSON)
		if err != nil {
			return nil, err
		}
		var deps any
		if resolveDeps != nil {
			deps, err = resolveDeps(input.DepsJSON)
			if err != nil {
				return nil, err
			}
		}

		rc := core.NewRunContext(core.RunContext{
			Deps:         deps,
			Usage:        input.Usage,
			Prompt:       input.Prompt,
			Retry:        input.Retry,
			MaxRetries:   input.MaxRetries,
			ToolName:     tool.Definition.Name,
			ToolCallID:   input.ToolCallID,
			Messages:     messages,
			RunStep:      input.RunStep,
			RunID:        input.RunID,
			RunStartTime: input.RunStartTime,
			EventBus:     eventBus,
		}, func() map[string]any {
			return cloneAnyMap(input.ToolState)
		}, func() *core.RunStateSnapshot {
			return buildTemporalRunStateSnapshot(
				input.Prompt,
				messages,
				input.Usage,
				input.LastInputTokens,
				input.Retries,
				input.ToolRetries,
				input.RunID,
				input.RunStep,
				input.RunStartTime,
				input.ToolState,
			)
		})

		toolCtx := core.ContextWithToolCallID(core.ContextWithRunID(ctx, input.RunID), input.ToolCallID)
		timeout := tool.Timeout
		if timeout == 0 && defaultTimeout > 0 {
			timeout = defaultTimeout
		}
		if timeout > 0 {
			var cancel context.CancelFunc
			toolCtx, cancel = context.WithTimeout(toolCtx, timeout)
			defer cancel()
		}

		runTool := func() (*ToolActivityOutput, error) {
			result, callErr := safeTemporalToolCall(tool.Handler, toolCtx, rc, input.ArgsJSON)
			updatedState, hasUpdatedState := exportTemporalToolState(tool)

			if callErr != nil {
				var deferredErr *core.CallDeferred
				if errors.As(callErr, &deferredErr) {
					return &ToolActivityOutput{
						Kind:             "deferred",
						Message:          deferredErr.Message,
						HasUpdatedState:  hasUpdatedState,
						UpdatedToolState: updatedState,
					}, nil
				}
				var retryErr *core.ModelRetryError
				if errors.As(callErr, &retryErr) {
					return &ToolActivityOutput{
						Kind:             "retry",
						Message:          retryErr.Message,
						HasUpdatedState:  hasUpdatedState,
						UpdatedToolState: updatedState,
					}, nil
				}
				return &ToolActivityOutput{
					Kind:             "error",
					Message:          callErr.Error(),
					HasUpdatedState:  hasUpdatedState,
					UpdatedToolState: updatedState,
				}, nil
			}

			content, images, err := serializeTemporalToolResult(result)
			if err != nil {
				return nil, fmt.Errorf("serialize tool result: %w", err)
			}

			if valErr := validateTemporalToolResult(tool, globalValidators, toolCtx, rc, content); valErr != nil {
				msg := "tool result validation failed: " + valErr.Error()
				if input.Retry >= input.MaxRetries {
					msg = fmt.Sprintf("tool %q exceeded maximum retries (%d): %s", tool.Definition.Name, input.MaxRetries, msg)
				}
				return &ToolActivityOutput{
					Kind:             "retry",
					Message:          msg,
					HasUpdatedState:  hasUpdatedState,
					UpdatedToolState: updatedState,
				}, nil
			}

			return &ToolActivityOutput{
				Kind:             "return",
				Content:          content,
				Images:           images,
				HasUpdatedState:  hasUpdatedState,
				UpdatedToolState: updatedState,
			}, nil
		}

		if tool.Stateful != nil {
			stateMu.Lock()
			defer stateMu.Unlock()
			if state, ok := input.ToolState[tool.Definition.Name]; ok {
				if err := tool.Stateful.RestoreState(state); err != nil {
					return nil, fmt.Errorf("restore tool state: %w", err)
				}
			}
			return runTool()
		}

		return runTool()
	}

	return TemporalTool{
		Tool:         tool,
		ActivityName: actName,
		ActivityFn:   activityFn,
		Config:       config,
	}
}

func safeTemporalToolCall(handler core.ToolHandler, ctx context.Context, rc *core.RunContext, argsJSON string) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool handler panicked: %v", r)
		}
	}()
	return handler(ctx, rc, argsJSON)
}

func exportTemporalToolState(tool core.Tool) (any, bool) {
	if tool.Stateful == nil {
		return nil, false
	}
	state, err := tool.Stateful.ExportState()
	if err != nil {
		return nil, false
	}
	return state, true
}

func serializeTemporalToolResult(result any) (string, []core.ImagePart, error) {
	if result == nil {
		return "", nil, nil
	}
	if multimodal, ok := result.(core.ToolResultWithImages); ok {
		return multimodal.Text, multimodal.Images, nil
	}
	if text, ok := result.(string); ok {
		return text, nil, nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", nil, err
	}
	return string(data), nil, nil
}

func validateTemporalToolResult(tool core.Tool, globalValidators []core.ToolResultValidatorFunc, ctx context.Context, rc *core.RunContext, content string) error {
	if tool.ResultValidator != nil {
		if err := tool.ResultValidator(ctx, rc, tool.Definition.Name, content); err != nil {
			return err
		}
	}
	for _, validator := range globalValidators {
		if err := validator(ctx, rc, tool.Definition.Name, content); err != nil {
			return err
		}
	}
	return nil
}

// TemporalTool wraps a tool with its Temporal activity function.
type TemporalTool struct {
	Tool         core.Tool
	ActivityName string
	ActivityFn   func(ctx context.Context, input ToolActivityInput) (*ToolActivityOutput, error)
	Config       ActivityConfig
}

// TemporalizeTools wraps multiple tools.
func TemporalizeTools(agentName string, tools []core.Tool, config ActivityConfig) []TemporalTool {
	return temporalizeTools(agentName, tools, config, 0, nil)
}

func temporalizeTools(agentName string, tools []core.Tool, config ActivityConfig, defaultTimeout time.Duration, globalValidators []core.ToolResultValidatorFunc) []TemporalTool {
	result := make([]TemporalTool, len(tools))
	for i, tool := range tools {
		result[i] = temporalizeTool(agentName, tool, config, defaultTimeout, globalValidators, nil, nil)
	}
	return result
}

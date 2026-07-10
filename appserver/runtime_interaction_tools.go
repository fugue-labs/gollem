package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

const (
	runtimeInteractionToolNamespace   = "interactions"
	runtimeInteractionPayloadMaxBytes = 64 * 1024
	runtimeInteractionPromptMaxBytes  = 8 * 1024
	runtimeInteractionMaxOptions      = 64
	runtimeInteractionDefaultTimeout  = 5 * time.Minute
	runtimeInteractionMaxTimeout      = 15 * time.Minute
)

type runtimeUserInputParams struct {
	Prompt         string         `json:"prompt"`
	Placeholder    string         `json:"placeholder,omitempty"`
	Required       bool           `json:"required,omitempty"`
	Options        []string       `json:"options,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	TimeoutSeconds int            `json:"timeoutSeconds,omitempty"`
}

type runtimeClientToolCallParams struct {
	ToolName       string         `json:"toolName"`
	Arguments      map[string]any `json:"arguments,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	TimeoutSeconds int            `json:"timeoutSeconds,omitempty"`
}

type runtimeMCPElicitationParams struct {
	Server         string         `json:"server"`
	Message        string         `json:"message"`
	Schema         map[string]any `json:"schema,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	TimeoutSeconds int            `json:"timeoutSeconds,omitempty"`
}

type runtimeInteractionResult struct {
	RequestID string `json:"requestId"`
	Method    string `json:"method"`
	Result    any    `json:"result"`
}

// InteractionRuntimeTools exposes the shared server-request service to active
// model turns. Requests carry durable runtime item correlation and are removed
// from the pending registry when the turn, caller, or per-request timeout ends.
func InteractionRuntimeTools(service *InteractionService) []core.Tool {
	if service == nil {
		return nil
	}
	tools := []core.Tool{
		core.FuncTool[runtimeUserInputParams](
			"request_user_input",
			"Request structured input from the connected client and wait for its JSON response.",
			func(ctx context.Context, rc *core.RunContext, params runtimeUserInputParams) (runtimeInteractionResult, error) {
				if strings.TrimSpace(params.Prompt) == "" {
					return runtimeInteractionResult{}, errors.New("prompt is required")
				}
				if len(params.Prompt) > runtimeInteractionPromptMaxBytes || len(params.Placeholder) > runtimeInteractionPromptMaxBytes {
					return runtimeInteractionResult{}, fmt.Errorf("prompt and placeholder must each be at most %d bytes", runtimeInteractionPromptMaxBytes)
				}
				if len(params.Options) > runtimeInteractionMaxOptions {
					return runtimeInteractionResult{}, fmt.Errorf("options exceeds %d entries", runtimeInteractionMaxOptions)
				}
				if err := validateRuntimeInteractionPayload(params); err != nil {
					return runtimeInteractionResult{}, err
				}
				requestCtx, cancel, err := runtimeInteractionContext(ctx, params.TimeoutSeconds)
				if err != nil {
					return runtimeInteractionResult{}, err
				}
				defer cancel()
				turn := runtimeTurnContextFrom(ctx)
				response, err := service.RequestUserInput(requestCtx, UserInputRequest{
					ThreadID:    turn.ThreadID,
					TurnID:      turn.TurnID,
					ItemID:      runtimeDurableToolItemID(ctx, rc),
					Prompt:      strings.TrimSpace(params.Prompt),
					Placeholder: params.Placeholder,
					Required:    params.Required,
					Options:     append([]string(nil), params.Options...),
					Metadata:    cloneStringAnyMap(params.Metadata),
				})
				return newRuntimeInteractionResult(response), err
			},
			core.WithToolSequential(true),
			core.WithToolTimeout(runtimeInteractionMaxTimeout+time.Second),
		),
		core.FuncTool[runtimeClientToolCallParams](
			"call_client_tool",
			"Request a named tool from the connected client and wait for its bounded JSON response.",
			func(ctx context.Context, rc *core.RunContext, params runtimeClientToolCallParams) (runtimeInteractionResult, error) {
				toolName := strings.TrimSpace(params.ToolName)
				if toolName == "" {
					return runtimeInteractionResult{}, errors.New("toolName is required")
				}
				if len(toolName) > runtimeInteractionPromptMaxBytes {
					return runtimeInteractionResult{}, fmt.Errorf("toolName exceeds %d bytes", runtimeInteractionPromptMaxBytes)
				}
				if err := validateRuntimeInteractionPayload(params); err != nil {
					return runtimeInteractionResult{}, err
				}
				arguments, err := json.Marshal(params.Arguments)
				if err != nil {
					return runtimeInteractionResult{}, fmt.Errorf("encode client tool arguments: %w", err)
				}
				if params.Arguments == nil {
					arguments = json.RawMessage(`{}`)
				}
				requestCtx, cancel, err := runtimeInteractionContext(ctx, params.TimeoutSeconds)
				if err != nil {
					return runtimeInteractionResult{}, err
				}
				defer cancel()
				turn := runtimeTurnContextFrom(ctx)
				response, err := service.RequestToolCall(requestCtx, DynamicToolCallRequest{
					ThreadID:  turn.ThreadID,
					TurnID:    turn.TurnID,
					ItemID:    runtimeDurableToolItemID(ctx, rc),
					ToolName:  toolName,
					Arguments: arguments,
					Metadata:  cloneStringAnyMap(params.Metadata),
				})
				return newRuntimeInteractionResult(response), err
			},
			core.WithToolSequential(true),
			core.WithToolTimeout(runtimeInteractionMaxTimeout+time.Second),
		),
		core.FuncTool[runtimeMCPElicitationParams](
			"request_mcp_elicitation",
			"Request structured MCP elicitation from the connected client and wait for its bounded JSON response.",
			func(ctx context.Context, rc *core.RunContext, params runtimeMCPElicitationParams) (runtimeInteractionResult, error) {
				serverName := strings.TrimSpace(params.Server)
				message := strings.TrimSpace(params.Message)
				if serverName == "" {
					return runtimeInteractionResult{}, errors.New("server is required")
				}
				if message == "" {
					return runtimeInteractionResult{}, errors.New("message is required")
				}
				if len(serverName) > runtimeInteractionPromptMaxBytes || len(message) > runtimeInteractionPromptMaxBytes {
					return runtimeInteractionResult{}, fmt.Errorf("server and message must each be at most %d bytes", runtimeInteractionPromptMaxBytes)
				}
				if err := validateRuntimeInteractionPayload(params); err != nil {
					return runtimeInteractionResult{}, err
				}
				requestCtx, cancel, err := runtimeInteractionContext(ctx, params.TimeoutSeconds)
				if err != nil {
					return runtimeInteractionResult{}, err
				}
				defer cancel()
				turn := runtimeTurnContextFrom(ctx)
				response, err := service.RequestMCPElicitation(requestCtx, MCPElicitationRequest{
					ThreadID: turn.ThreadID,
					TurnID:   turn.TurnID,
					ItemID:   runtimeDurableToolItemID(ctx, rc),
					ServerID: serverName,
					Message:  message,
					Schema:   cloneStringAnyMap(params.Schema),
					Metadata: cloneStringAnyMap(params.Metadata),
				})
				return newRuntimeInteractionResult(response), err
			},
			core.WithToolSequential(true),
			core.WithToolTimeout(runtimeInteractionMaxTimeout+time.Second),
		),
	}
	for i := range tools {
		tools[i].Definition.Namespace = runtimeInteractionToolNamespace
	}
	return tools
}

func validateRuntimeInteractionPayload(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode interaction request: %w", err)
	}
	if len(raw) > runtimeInteractionPayloadMaxBytes {
		return fmt.Errorf("interaction request exceeds %d bytes", runtimeInteractionPayloadMaxBytes)
	}
	return nil
}

func runtimeInteractionContext(ctx context.Context, timeoutSeconds int) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeoutSeconds < 0 || timeoutSeconds > int(runtimeInteractionMaxTimeout/time.Second) {
		return nil, nil, fmt.Errorf("timeoutSeconds must be between 1 and %d when provided", int(runtimeInteractionMaxTimeout/time.Second))
	}
	timeout := runtimeInteractionDefaultTimeout
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	return requestCtx, cancel, nil
}

func newRuntimeInteractionResult(response InteractionResponse) runtimeInteractionResult {
	return runtimeInteractionResult{
		RequestID: response.RequestID,
		Method:    response.Method,
		Result:    runtimeToolArguments(string(response.Result)),
	}
}

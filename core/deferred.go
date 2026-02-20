package core

import (
	"fmt"
	"time"
)

// incrementRetries increments the result retry counter and returns an error
// if the maximum has been exceeded. It also detects incomplete tool calls
// caused by the model hitting its token limit.
func incrementRetries(retries *int, maxRetries int, messages []ModelMessage) error {
	*retries++

	if *retries > maxRetries {
		// Check for incomplete tool call (finish_reason == length + malformed args).
		if lastResp := lastModelResponse(messages); lastResp != nil {
			if lastResp.FinishReason == FinishReasonLength {
				toolCalls := lastResp.ToolCalls()
				if len(toolCalls) > 0 {
					lastCall := toolCalls[len(toolCalls)-1]
					if _, err := lastCall.ArgsAsMap(); err != nil {
						return &IncompleteToolCall{
							UnexpectedModelBehavior: UnexpectedModelBehavior{
								Message: "model hit token limit while generating tool call arguments, consider increasing max_tokens",
							},
						}
					}
				}
			}
		}

		return &UnexpectedModelBehavior{
			Message: fmt.Sprintf("exceeded maximum retries (%d) for result validation", maxRetries),
		}
	}
	return nil
}

// buildRetryParts creates a RetryPromptPart with the given error message.
func buildRetryParts(msg string, toolName, toolCallID string) RetryPromptPart {
	return RetryPromptPart{
		Content:    msg,
		ToolName:   toolName,
		ToolCallID: toolCallID,
		Timestamp:  time.Now(),
	}
}

// lastModelResponse returns the last ModelResponse in the message history, or nil.
func lastModelResponse(messages []ModelMessage) *ModelResponse {
	for i := len(messages) - 1; i >= 0; i-- {
		if resp, ok := messages[i].(ModelResponse); ok {
			return &resp
		}
	}
	return nil
}

// DeferredToolRequest represents a tool call that requires external resolution.
type DeferredToolRequest struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id"`
	ArgsJSON   string `json:"args_json"`
}

// DeferredToolResult provides the result for a previously deferred tool call.
type DeferredToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

// CallDeferred is returned by tool handlers to indicate the tool call
// should be deferred for external resolution.
type CallDeferred struct {
	Message string
}

func (e *CallDeferred) Error() string { return "deferred: " + e.Message }

// RunResultDeferred is returned when an agent run ends with pending deferred tools.
type RunResultDeferred[T any] struct {
	DeferredRequests []DeferredToolRequest
	Messages         []ModelMessage
	Usage            RunUsage
}

// ErrDeferred wraps a RunResultDeferred and is returned from Run when
// tool calls are deferred for external resolution.
type ErrDeferred[T any] struct {
	Result RunResultDeferred[T]
}

func (e *ErrDeferred[T]) Error() string {
	return fmt.Sprintf("agent run deferred: %d tool call(s) require external resolution", len(e.Result.DeferredRequests))
}

// WithDeferredResults injects deferred tool results into the run.
// When provided, the results are sent as ToolReturnParts (or RetryPromptParts
// for error results) before the first model call.
func WithDeferredResults(results ...DeferredToolResult) RunOption {
	return func(c *runConfig) {
		c.deferredResults = results
	}
}

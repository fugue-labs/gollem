// Package gollem provides a production-grade Go agent framework for building
// LLM-powered agents with structured outputs, tool use, streaming, and
// multi-provider support.
package core

import (
	"fmt"
)

// ModelRetryError is returned by tool functions to request that the model
// retry with the given feedback message. The message is sent back to the
// model as a RetryPromptPart.
type ModelRetryError struct {
	Message string
}

func (e *ModelRetryError) Error() string {
	return e.Message
}

// NewModelRetryError creates a ModelRetryError with the given message.
func NewModelRetryError(msg string) *ModelRetryError {
	return &ModelRetryError{Message: msg}
}

// UserError represents a developer usage mistake.
type UserError struct {
	Message string
}

func (e *UserError) Error() string {
	return e.Message
}

// AgentRunError is the base error for errors occurring during an agent run.
type AgentRunError struct {
	Message string
}

func (e *AgentRunError) Error() string {
	return e.Message
}

// UsageLimitExceeded is returned when usage exceeds configured limits.
type UsageLimitExceeded struct {
	Message string
}

func (e *UsageLimitExceeded) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "usage limit exceeded"
}

// UnexpectedModelBehavior indicates the model responded in an unexpected way.
type UnexpectedModelBehavior struct {
	Message string
	Body    string // raw response body, if available
}

func (e *UnexpectedModelBehavior) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("%s (body: %s)", e.Message, e.Body)
	}
	return e.Message
}

// ContentFilterError indicates the provider's content filter was triggered.
type ContentFilterError struct {
	UnexpectedModelBehavior
}

// IncompleteToolCall indicates the model hit its token limit while generating
// a tool call, resulting in malformed arguments.
type IncompleteToolCall struct {
	UnexpectedModelBehavior
}

// ModelHTTPError indicates an HTTP-level failure from a provider.
type ModelHTTPError struct {
	Message    string
	StatusCode int
	Body       string
	ModelName  string
}

func (e *ModelHTTPError) Error() string {
	return fmt.Sprintf("%s (status %d)", e.Message, e.StatusCode)
}


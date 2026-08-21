// Package gollem provides a production-grade Go agent framework for building
// LLM-powered agents with structured outputs, tool use, streaming, and
// multi-provider support.
package core

import (
	"fmt"
	"time"
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

// FatalToolError is returned by a tool handler when its failure must terminate
// the agent run instead of becoming a tool result sent back to the model.
type FatalToolError struct {
	Err error
}

func (e *FatalToolError) Error() string {
	if e == nil || e.Err == nil {
		return "fatal tool error"
	}
	return e.Err.Error()
}

func (e *FatalToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewFatalToolError marks err as fatal to the current agent run.
func NewFatalToolError(err error) *FatalToolError {
	return &FatalToolError{Err: err}
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
	RetryAfter time.Duration // parsed from Retry-After header, if present
}

func (e *ModelHTTPError) Error() string {
	return fmt.Sprintf("%s (status %d)", e.Message, e.StatusCode)
}

// StreamIncompleteError indicates that a provider closed a stream before it
// supplied its documented terminal event. The partial response remains
// available from the stream for callers that can safely present it as partial.
type StreamIncompleteError struct {
	Provider string
}

func (e *StreamIncompleteError) Error() string {
	if e.Provider == "" {
		return "model stream ended before a terminal event"
	}
	return e.Provider + " stream ended before a terminal event"
}

// StreamProtocolError indicates malformed provider stream data. It deliberately
// excludes raw event data so protocol failures cannot disclose provider payloads.
type StreamProtocolError struct {
	Provider string
}

func (e *StreamProtocolError) Error() string {
	if e.Provider == "" {
		return "model stream contained malformed protocol data"
	}
	return e.Provider + " stream contained malformed protocol data"
}

// StreamTransportError indicates a non-context read failure after a streaming
// response has started. The original transport detail is intentionally omitted
// because it can contain provider or endpoint data; callers may inspect this
// type to offer an explicit fresh-run recovery path.
type StreamTransportError struct {
	Provider string
}

func (e *StreamTransportError) Error() string {
	if e.Provider == "" {
		return "model stream transport failed"
	}
	return e.Provider + " stream transport failed"
}

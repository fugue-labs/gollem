package core

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// ModelMessage is the interface for all messages in a conversation.
// A message is either a ModelRequest (sent to the model) or a ModelResponse
// (received from the model).
type ModelMessage interface {
	messageKind() string
}

// --- Request Parts ---

// ModelRequestPart is the interface for all parts of a model request.
type ModelRequestPart interface {
	requestPartKind() string
}

// SystemPromptPart provides system-level instructions to the model.
type SystemPromptPart struct {
	Content   string
	Timestamp time.Time
}

func (p SystemPromptPart) requestPartKind() string { return "system-prompt" }

// UserPromptPart contains the user's input.
type UserPromptPart struct {
	Content   string
	Timestamp time.Time
}

func (p UserPromptPart) requestPartKind() string { return "user-prompt" }

// ToolReturnPart is the result of a tool call sent back to the model.
type ToolReturnPart struct {
	ToolName   string
	Content    any // string or structured data (serialized to JSON)
	ToolCallID string
	Timestamp  time.Time
}

func (p ToolReturnPart) requestPartKind() string { return "tool-return" }

// RetryPromptPart tells the model to retry with feedback about what went wrong.
type RetryPromptPart struct {
	Content    string // error message or validation feedback
	ToolName   string // the tool that should be retried (empty for result retry)
	ToolCallID string // the tool call ID being retried
	Timestamp  time.Time
}

func (p RetryPromptPart) requestPartKind() string { return "retry-prompt" }

// ImagePart represents an image input in a user message.
type ImagePart struct {
	URL       string // image URL (https or data: URI with base64)
	MIMEType  string // e.g., "image/png", "image/jpeg"
	Detail    string // "auto", "low", "high" (optional)
	Timestamp time.Time
}

func (p ImagePart) requestPartKind() string { return "image" }

// AudioPart represents an audio input in a user message.
type AudioPart struct {
	URL       string // audio URL or data: URI
	MIMEType  string // e.g., "audio/mp3", "audio/wav"
	Timestamp time.Time
}

func (p AudioPart) requestPartKind() string { return "audio" }

// DocumentPart represents a document input (PDF, etc.) in a user message.
type DocumentPart struct {
	URL       string // document URL or data: URI
	MIMEType  string // e.g., "application/pdf"
	Title     string // optional display title
	Timestamp time.Time
}

func (p DocumentPart) requestPartKind() string { return "document" }

// BinaryContent creates a data: URI from raw bytes and MIME type.
func BinaryContent(data []byte, mimeType string) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	return "data:" + mimeType + ";base64," + encoded
}

// --- Response Parts ---

// ModelResponsePart is the interface for all parts of a model response.
type ModelResponsePart interface {
	responsePartKind() string
}

// TextPart contains text content from the model.
type TextPart struct {
	Content string
}

func (p TextPart) responsePartKind() string { return "text" }

// ToolCallPart represents the model requesting a tool call.
type ToolCallPart struct {
	ToolName   string
	ArgsJSON   string // raw JSON arguments
	ToolCallID string
	// Metadata carries provider-specific opaque data that must be round-tripped
	// (e.g., Gemini 3.x thought signatures). Providers set this on parse and
	// read it back when serializing tool calls in conversation history.
	Metadata map[string]string
}

func (p ToolCallPart) responsePartKind() string { return "tool-call" }

// ArgsAsMap deserializes the tool call arguments into a map.
func (p ToolCallPart) ArgsAsMap() (map[string]any, error) {
	var m map[string]any
	if p.ArgsJSON == "" || p.ArgsJSON == "{}" {
		return map[string]any{}, nil
	}
	err := json.Unmarshal([]byte(p.ArgsJSON), &m)
	return m, err
}

// ThinkingPart contains the model's chain-of-thought reasoning.
type ThinkingPart struct {
	Content   string
	Signature string // provider-specific signature
}

func (p ThinkingPart) responsePartKind() string { return "thinking" }

// --- Containers ---

// FinishReason indicates why the model stopped generating.
type FinishReason string

const (
	FinishReasonStop          FinishReason = "stop"
	FinishReasonLength        FinishReason = "length"
	FinishReasonContentFilter FinishReason = "content_filter"
	FinishReasonToolCall      FinishReason = "tool_call"
	FinishReasonError         FinishReason = "error"
)

// ModelRequest is a request message sent to the model.
type ModelRequest struct {
	Parts     []ModelRequestPart
	Timestamp time.Time
}

func (m ModelRequest) messageKind() string { return "request" }

// ModelResponse is the model's reply.
type ModelResponse struct {
	Parts        []ModelResponsePart
	Usage        Usage
	ModelName    string
	FinishReason FinishReason
	Timestamp    time.Time
}

func (m ModelResponse) messageKind() string { return "response" }

// ToolCalls returns all ToolCallParts from the response.
func (m ModelResponse) ToolCalls() []ToolCallPart {
	var calls []ToolCallPart
	for _, p := range m.Parts {
		if tc, ok := p.(ToolCallPart); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}

// TextContent returns the concatenated text content from the response.
func (m ModelResponse) TextContent() string {
	var parts []string
	for _, p := range m.Parts {
		if tp, ok := p.(TextPart); ok {
			parts = append(parts, tp.Content)
		}
	}
	return strings.Join(parts, "")
}

// --- Stream Events ---

// ModelResponseStreamEvent is the interface for streaming events.
type ModelResponseStreamEvent interface {
	streamEventKind() string
}

// PartStartEvent signals that a new response part has started streaming.
type PartStartEvent struct {
	Index int
	Part  ModelResponsePart
}

func (e PartStartEvent) streamEventKind() string { return "part-start" }

// PartDeltaEvent signals an incremental update to a response part.
type PartDeltaEvent struct {
	Index int
	Delta ModelResponsePartDelta
}

func (e PartDeltaEvent) streamEventKind() string { return "part-delta" }

// PartEndEvent signals that a response part has finished streaming.
type PartEndEvent struct {
	Index int
}

func (e PartEndEvent) streamEventKind() string { return "part-end" }

// --- Deltas ---

// ModelResponsePartDelta is the interface for incremental part updates.
type ModelResponsePartDelta interface {
	deltaKind() string
}

// TextPartDelta is an incremental text chunk.
type TextPartDelta struct {
	ContentDelta string
}

func (d TextPartDelta) deltaKind() string { return "text" }

// ToolCallPartDelta is an incremental tool call argument chunk.
type ToolCallPartDelta struct {
	ArgsJSONDelta string
}

func (d ToolCallPartDelta) deltaKind() string { return "tool-call" }

// ThinkingPartDelta is an incremental thinking chunk.
type ThinkingPartDelta struct {
	ContentDelta string
}

func (d ThinkingPartDelta) deltaKind() string { return "thinking" }

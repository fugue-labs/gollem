package core

import (
	"context"
	"io"
)

// Model is the core interface every LLM provider must implement.
type Model interface {
	// Request sends messages and returns a complete response.
	Request(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters) (*ModelResponse, error)

	// RequestStream sends messages and returns an event stream for
	// incremental consumption. Providers that don't support streaming
	// should return an error.
	RequestStream(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters) (StreamedResponse, error)

	// ModelName returns the identifier of the model (e.g., "claude-sonnet-4-6").
	ModelName() string
}

// ModelSettings holds provider-specific settings like temperature and max tokens.
type ModelSettings struct {
	MaxTokens      *int        `json:"max_tokens,omitempty"`
	Temperature    *float64    `json:"temperature,omitempty"`
	TopP           *float64    `json:"top_p,omitempty"`
	ToolChoice     *ToolChoice `json:"tool_choice,omitempty"`
	ThinkingBudget *int        `json:"thinking_budget,omitempty"` // Anthropic extended thinking budget tokens (manual mode)
	// AdaptiveThinking, when true, lets the model decide when and how much
	// to think before answering. Maps to Anthropic's
	// {thinking: {type: "adaptive"}} — supported on the Claude 4.6
	// generation and newer (Opus/Sonnet 4.6, Opus 4.7/4.8, Fable; the
	// 4.7+ models accept no other thinking mode). Mutually exclusive with
	// ThinkingBudget: the Anthropic providers reject requests setting
	// both. Thinking tokens count toward MaxTokens, so size MaxTokens
	// with headroom. Ignored by providers without an equivalent (OpenAI
	// reasoning models think implicitly; see ReasoningEffort).
	AdaptiveThinking *bool `json:"adaptive_thinking,omitempty"`
	// ReasoningEffort controls thinking depth. Values: "low", "medium", "high",
	// "xhigh", "max". Supported on OpenAI reasoning models and Anthropic effort-
	// capable models (Claude 4.5 Opus+, 4.6 Opus/Sonnet, 4.7 Opus, 4.8 Opus,
	// Fable). Per-provider gating rejects values a given model doesn't accept
	// (e.g., "xhigh" requires Opus 4.7+; "max" requires 4.6+).
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
	// StopSequences causes generation to halt when the model would emit any
	// listed string. Supported by Anthropic (stop_sequences) and Gemini
	// (stopSequences). Ignored by providers that don't support it (e.g.,
	// OpenAI Responses API).
	StopSequences []string `json:"stop_sequences,omitempty"`
}

// OutputMode determines how structured output is extracted from the model.
type OutputMode string

const (
	// OutputModeText extracts output from the model's text response.
	OutputModeText OutputMode = "text"
	// OutputModeTool uses a synthetic tool call to extract structured output.
	OutputModeTool OutputMode = "tool"
	// OutputModeNative uses the provider's native structured output support.
	OutputModeNative OutputMode = "native"
)

// OutputObjectDefinition describes the desired output schema for structured output modes.
type OutputObjectDefinition struct {
	Name        string
	Description string
	JSONSchema  Schema
	Strict      *bool
}

// ModelRequestParameters bundles tool definitions and output configuration for a request.
type ModelRequestParameters struct {
	// FunctionTools are the callable tools available to the model.
	FunctionTools []ToolDefinition

	// OutputMode determines how structured output is extracted.
	OutputMode OutputMode

	// OutputTools are synthetic tools for structured output extraction.
	OutputTools []ToolDefinition

	// OutputObject describes the output schema for native/prompted modes.
	OutputObject *OutputObjectDefinition

	// AllowTextOutput controls whether the model can return text directly.
	AllowTextOutput bool
}

// AllToolDefs returns all tool definitions (function + output) for the request.
func (p *ModelRequestParameters) AllToolDefs() []ToolDefinition {
	result := make([]ToolDefinition, 0, len(p.FunctionTools)+len(p.OutputTools))
	result = append(result, p.FunctionTools...)
	result = append(result, p.OutputTools...)
	return result
}

// StreamedResponse is the interface for streaming model responses.
type StreamedResponse interface {
	// Next returns the next stream event. Returns io.EOF when the stream is done.
	Next() (ModelResponseStreamEvent, error)

	// Response returns the ModelResponse built from data received so far.
	Response() *ModelResponse

	// Usage returns usage information gathered so far.
	Usage() Usage

	// Close cleans up resources (HTTP connections, etc.).
	Close() error
}

// Ensure io.EOF is accessible.
var _ = io.EOF

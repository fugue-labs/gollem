package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TemporalModel wraps a core.Model, providing activity functions for Temporal.
// When used outside a workflow, it passes through directly to the wrapped model.
type TemporalModel struct {
	wrapped          core.Model
	name             string
	config           ActivityConfig
	middleware       []core.RequestMiddlewareFunc
	streamMiddleware []core.AgentStreamMiddleware
}

// NewTemporalModel wraps a model for Temporal execution.
func NewTemporalModel(wrapped core.Model, name string, config ActivityConfig, middleware ...core.RequestMiddlewareFunc) *TemporalModel {
	return NewTemporalModelWithMiddleware(wrapped, name, config, middleware, nil)
}

// NewTemporalModelWithMiddleware wraps a model for Temporal execution with
// explicit request and stream middleware chains.
func NewTemporalModelWithMiddleware(
	wrapped core.Model,
	name string,
	config ActivityConfig,
	requestMiddleware []core.RequestMiddlewareFunc,
	streamMiddleware []core.AgentStreamMiddleware,
) *TemporalModel {
	return &TemporalModel{
		wrapped:          wrapped,
		name:             name,
		config:           config,
		middleware:       append([]core.RequestMiddlewareFunc(nil), requestMiddleware...),
		streamMiddleware: append([]core.AgentStreamMiddleware(nil), streamMiddleware...),
	}
}

// ModelActivityInput is the serializable parameter for model request activities.
type ModelActivityInput struct {
	Messages     []core.SerializedMessage     `json:"messages,omitempty"`
	MessagesJSON json.RawMessage              `json:"messages_json,omitempty"` // Deprecated: prefer Messages.
	Settings     *core.ModelSettings          `json:"settings,omitempty"`
	Parameters   *core.ModelRequestParameters `json:"parameters,omitempty"`
}

// ModelActivityOutput is the serializable result from a model request activity.
type ModelActivityOutput struct {
	Response     *core.SerializedMessage `json:"response,omitempty"`
	ResponseJSON json.RawMessage         `json:"response_json,omitempty"` // Deprecated: prefer Response.
}

// Request delegates directly to the wrapped model (used outside workflows).
func (m *TemporalModel) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	return m.wrapped.Request(ctx, messages, settings, params)
}

// RequestStream delegates directly to the wrapped model.
func (m *TemporalModel) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	return m.wrapped.RequestStream(ctx, messages, settings, params)
}

// ModelName returns the name of the wrapped model.
func (m *TemporalModel) ModelName() string {
	return m.wrapped.ModelName()
}

// ModelRequestActivityName returns the activity name for model requests.
func (m *TemporalModel) ModelRequestActivityName() string {
	return "agent__" + m.name + "__model_request"
}

// ModelRequestStreamActivityName returns the activity name for streaming model requests.
func (m *TemporalModel) ModelRequestStreamActivityName() string {
	return "agent__" + m.name + "__model_request_stream"
}

// ModelRequestActivity is the Temporal activity function for model requests.
func (m *TemporalModel) ModelRequestActivity(ctx context.Context, input ModelActivityInput) (*ModelActivityOutput, error) {
	messages, err := decodeSerializedMessages(input.Messages, input.MessagesJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal model messages: %w", err)
	}

	resp, err := m.runRequest(ctx, messages, input.Settings, input.Parameters)
	if err != nil {
		return nil, err
	}
	return encodeModelActivityOutput(resp)
}

// ModelRequestStreamActivity is the Temporal activity for streaming requests.
// It collects the stream into a complete response.
func (m *TemporalModel) ModelRequestStreamActivity(ctx context.Context, input ModelActivityInput) (*ModelActivityOutput, error) {
	messages, err := decodeSerializedMessages(input.Messages, input.MessagesJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal model messages: %w", err)
	}

	stream, err := m.runRequestStream(ctx, messages, input.Settings, input.Parameters)
	if err != nil {
		// Fallback to non-streaming.
		resp, reqErr := m.runRequest(ctx, messages, input.Settings, input.Parameters)
		if reqErr != nil {
			return nil, reqErr
		}
		return encodeModelActivityOutput(resp)
	}
	defer stream.Close()

	for {
		_, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("streaming model request: %w", err)
		}
	}
	return encodeModelActivityOutput(stream.Response())
}

func (m *TemporalModel) runRequest(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	if len(m.middleware) == 0 {
		return m.wrapped.Request(ctx, messages, settings, params)
	}
	chain := m.wrapped.Request
	for i := len(m.middleware) - 1; i >= 0; i-- {
		mw := m.middleware[i]
		next := chain
		chain = func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
			return mw(ctx, messages, settings, params, next)
		}
	}
	return chain(ctx, messages, settings, params)
}

func (m *TemporalModel) runRequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	if len(m.streamMiddleware) == 0 {
		return m.wrapped.RequestStream(ctx, messages, settings, params)
	}
	chain := m.wrapped.RequestStream
	for i := len(m.streamMiddleware) - 1; i >= 0; i-- {
		mw := m.streamMiddleware[i]
		next := chain
		chain = func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
			return mw(ctx, messages, settings, params, next)
		}
	}
	return chain(ctx, messages, settings, params)
}

func encodeModelActivityOutput(resp *core.ModelResponse) (*ModelActivityOutput, error) {
	response, err := core.EncodeModelResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal model response: %w", err)
	}
	return &ModelActivityOutput{Response: response}, nil
}

func decodeModelActivityOutput(output *ModelActivityOutput) (*core.ModelResponse, error) {
	if output == nil {
		return nil, errors.New("nil model activity output")
	}
	if output.Response != nil {
		resp, err := core.DecodeModelResponse(output.Response)
		if err != nil {
			return nil, fmt.Errorf("unmarshal model activity response: %w", err)
		}
		return resp, nil
	}
	messages, err := core.UnmarshalMessages(output.ResponseJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal model activity response: %w", err)
	}
	if len(messages) != 1 {
		return nil, fmt.Errorf("expected 1 model response message, got %d", len(messages))
	}
	resp, ok := messages[0].(core.ModelResponse)
	if !ok {
		return nil, fmt.Errorf("expected model response, got %T", messages[0])
	}
	return &resp, nil
}

// ActivityConfig configures Temporal activity execution.
type ActivityConfig struct {
	StartToCloseTimeout time.Duration // Default: 60s
	MaxRetries          int           // Default: 0 (no retries)
	InitialInterval     time.Duration // Default: 1s
}

// DefaultActivityConfig returns sensible defaults.
func DefaultActivityConfig() ActivityConfig {
	return ActivityConfig{
		StartToCloseTimeout: 60 * time.Second,
		MaxRetries:          0,
		InitialInterval:     time.Second,
	}
}

// completedStream wraps a completed ModelResponse as a StreamedResponse.
type completedStream struct {
	response *core.ModelResponse
	done     bool
}

func (s *completedStream) Next() (core.ModelResponseStreamEvent, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	return nil, io.EOF
}

func (s *completedStream) Response() *core.ModelResponse {
	return s.response
}

func (s *completedStream) Usage() core.Usage {
	return s.response.Usage
}

func (s *completedStream) Close() error {
	return nil
}

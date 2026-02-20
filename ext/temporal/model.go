package temporal

import (
	"context"
	"io"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TemporalModel wraps a core.Model, providing activity functions for Temporal.
// When used outside a workflow, it passes through directly to the wrapped model.
type TemporalModel struct {
	wrapped core.Model
	name    string
	config  ActivityConfig
}

// NewTemporalModel wraps a model for Temporal execution.
func NewTemporalModel(wrapped core.Model, name string, config ActivityConfig) *TemporalModel {
	return &TemporalModel{
		wrapped: wrapped,
		name:    name,
		config:  config,
	}
}

// requestParams is the serializable parameter for model request activities.
type requestParams struct {
	Messages   []core.ModelMessage          `json:"messages"`
	Settings   *core.ModelSettings          `json:"settings,omitempty"`
	Parameters *core.ModelRequestParameters `json:"parameters,omitempty"`
}

// Request delegates directly to the wrapped model (used in activities).
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
func (m *TemporalModel) ModelRequestActivity(ctx context.Context, params requestParams) (*core.ModelResponse, error) {
	return m.wrapped.Request(ctx, params.Messages, params.Settings, params.Parameters)
}

// ModelRequestStreamActivity is the Temporal activity for streaming requests.
// It collects the stream into a complete response.
func (m *TemporalModel) ModelRequestStreamActivity(ctx context.Context, params requestParams) (*core.ModelResponse, error) {
	stream, err := m.wrapped.RequestStream(ctx, params.Messages, params.Settings, params.Parameters)
	if err != nil {
		// Fallback to non-streaming.
		return m.wrapped.Request(ctx, params.Messages, params.Settings, params.Parameters)
	}
	defer stream.Close()

	// Consume the stream to get the final response.
	for {
		_, err := stream.Next()
		if err != nil {
			break
		}
	}
	return stream.Response(), nil
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

package core

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)


// TestModel is a mock Model for testing agents without real LLM calls.
// It returns canned responses in sequence and records all calls for assertions.
type TestModel struct {
	responses []*ModelResponse
	mu        sync.Mutex
	idx       int
	calls     []TestModelCall
	name      string
}

// TestModelCall records a call made to the test model.
type TestModelCall struct {
	Messages   []ModelMessage
	Settings   *ModelSettings
	Parameters *ModelRequestParameters
}

// NewTestModel creates a TestModel with canned responses.
func NewTestModel(responses ...*ModelResponse) *TestModel {
	return &TestModel{
		responses: responses,
		name:      "test-model",
	}
}

func (m *TestModel) ModelName() string {
	return m.name
}

// SetName sets the model name (useful in tests that need distinct model names).
func (m *TestModel) SetName(name string) {
	m.name = name
}

func (m *TestModel) Request(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters) (*ModelResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, TestModelCall{
		Messages:   messages,
		Settings:   settings,
		Parameters: params,
	})

	if len(m.responses) == 0 {
		return nil, errors.New("test model: no responses configured")
	}

	resp := m.responses[m.idx]
	if m.idx < len(m.responses)-1 {
		m.idx++
	}
	return resp, nil
}

func (m *TestModel) RequestStream(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters) (StreamedResponse, error) {
	resp, err := m.Request(ctx, messages, settings, params)
	if err != nil {
		return nil, err
	}
	return &testStreamedResponse{response: resp}, nil
}

// Calls returns the recorded calls for assertions.
func (m *TestModel) Calls() []TestModelCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]TestModelCall, len(m.calls))
	copy(result, m.calls)
	return result
}

// Reset clears the call history and resets the response index.
func (m *TestModel) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
	m.idx = 0
}

// TextResponse creates a simple ModelResponse with a TextPart.
func TextResponse(text string) *ModelResponse {
	return &ModelResponse{
		Parts:        []ModelResponsePart{TextPart{Content: text}},
		FinishReason: FinishReasonStop,
		ModelName:    "test-model",
		Timestamp:    time.Now(),
	}
}

// ToolCallResponse creates a ModelResponse with a ToolCallPart.
func ToolCallResponse(toolName, argsJSON string) *ModelResponse {
	return ToolCallResponseWithID(toolName, argsJSON, "call_"+toolName)
}

// ToolCallResponseWithID creates a ToolCallResponse with a specific call ID.
func ToolCallResponseWithID(toolName, argsJSON, callID string) *ModelResponse {
	return &ModelResponse{
		Parts: []ModelResponsePart{
			ToolCallPart{
				ToolName:   toolName,
				ArgsJSON:   argsJSON,
				ToolCallID: callID,
			},
		},
		FinishReason: FinishReasonToolCall,
		ModelName:    "test-model",
		Timestamp:    time.Now(),
	}
}

// MultiToolCallResponse creates a ModelResponse with multiple ToolCallParts.
func MultiToolCallResponse(calls ...ToolCallPart) *ModelResponse {
	parts := make([]ModelResponsePart, len(calls))
	for i, c := range calls {
		parts[i] = c
	}
	return &ModelResponse{
		Parts:        parts,
		FinishReason: FinishReasonToolCall,
		ModelName:    "test-model",
		Timestamp:    time.Now(),
	}
}

// testStreamedResponse wraps a canned ModelResponse as a StreamedResponse.
type testStreamedResponse struct {
	response *ModelResponse
	idx      int
	phase    int // 0=start events, 1=end events, 2=done
}

func (s *testStreamedResponse) Next() (ModelResponseStreamEvent, error) {
	if s.phase == 0 {
		if s.idx < len(s.response.Parts) {
			event := PartStartEvent{
				Index: s.idx,
				Part:  s.response.Parts[s.idx],
			}
			s.idx++
			if s.idx >= len(s.response.Parts) {
				s.idx = 0
				s.phase = 1
			}
			return event, nil
		}
		s.phase = 1
	}
	if s.phase == 1 {
		if s.idx < len(s.response.Parts) {
			event := PartEndEvent{Index: s.idx}
			s.idx++
			if s.idx >= len(s.response.Parts) {
				s.phase = 2
			}
			return event, nil
		}
		s.phase = 2
	}
	return nil, io.EOF
}

func (s *testStreamedResponse) Response() *ModelResponse {
	return s.response
}

func (s *testStreamedResponse) Usage() Usage {
	return s.response.Usage
}

func (s *testStreamedResponse) Close() error {
	return nil
}

package middleware

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// mockModel is a simple test model.
type mockModel struct {
	response *core.ModelResponse
	err      error
}

func (m *mockModel) ModelName() string { return "test-model" }
func (m *mockModel) Request(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
	return m.response, m.err
}
func (m *mockModel) RequestStream(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
	return nil, errors.New("not implemented")
}

func TestWrapNoMiddleware(t *testing.T) {
	model := &mockModel{response: &core.ModelResponse{
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "hello"}},
		ModelName: "test-model",
	}}
	wrapped := Wrap(model)
	// Should return the same model.
	if wrapped != model {
		t.Error("expected same model when no middlewares")
	}
}

func TestWrapModelName(t *testing.T) {
	model := &mockModel{response: &core.ModelResponse{ModelName: "test-model"}}
	noop := Func(func(next RequestFunc) RequestFunc {
		return next
	})
	wrapped := Wrap(model, noop)
	if wrapped.ModelName() != "test-model" {
		t.Errorf("expected test-model, got %s", wrapped.ModelName())
	}
}

func TestMiddlewareChainOrder(t *testing.T) {
	var order []string
	model := &mockModel{response: &core.ModelResponse{
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "result"}},
		ModelName: "test",
	}}

	mw1 := Func(func(next RequestFunc) RequestFunc {
		return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
			order = append(order, "mw1-before")
			resp, err := next(ctx, messages, settings, params)
			order = append(order, "mw1-after")
			return resp, err
		}
	})
	mw2 := Func(func(next RequestFunc) RequestFunc {
		return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
			order = append(order, "mw2-before")
			resp, err := next(ctx, messages, settings, params)
			order = append(order, "mw2-after")
			return resp, err
		}
	})

	wrapped := Wrap(model, mw1, mw2)
	wrapped.Request(context.Background(), nil, nil, nil)

	expected := []string{"mw1-before", "mw2-before", "mw2-after", "mw1-after"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d calls, got %d: %v", len(expected), len(order), order)
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("position %d: expected %s, got %s", i, v, order[i])
		}
	}
}

func TestLoggingMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	model := &mockModel{response: &core.ModelResponse{
		Parts:        []core.ModelResponsePart{core.TextPart{Content: "hello"}},
		ModelName:    "test-model",
		Usage:        core.Usage{InputTokens: 10, OutputTokens: 5},
		FinishReason: core.FinishReasonStop,
	}}

	logging := NewLogging(logger, slog.LevelInfo)
	wrapped := Wrap(model, logging)

	_, err := wrapped.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "test"}}},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "model request started") {
		t.Error("expected 'model request started' in logs")
	}
	if !strings.Contains(output, "model request completed") {
		t.Error("expected 'model request completed' in logs")
	}
	if !strings.Contains(output, "input_tokens=10") {
		t.Error("expected input_tokens=10 in logs")
	}
	if !strings.Contains(output, "output_tokens=5") {
		t.Error("expected output_tokens=5 in logs")
	}
}

func TestLoggingMiddlewareError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	model := &mockModel{err: errors.New("API error")}
	logging := NewLogging(logger, slog.LevelInfo)
	wrapped := Wrap(model, logging)

	_, err := wrapped.Request(context.Background(), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}

	output := buf.String()
	if !strings.Contains(output, "model request failed") {
		t.Error("expected 'model request failed' in logs")
	}
}

func TestLoggingMiddlewareToolCalls(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	model := &mockModel{response: &core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.ToolCallPart{ToolName: "get_weather", ToolCallID: "call_1", ArgsJSON: "{}"},
		},
		ModelName:    "test-model",
		FinishReason: core.FinishReasonToolCall,
	}}

	logging := NewLogging(logger, slog.LevelInfo)
	wrapped := Wrap(model, logging)
	wrapped.Request(context.Background(), nil, nil, nil)

	output := buf.String()
	if !strings.Contains(output, "get_weather") {
		t.Error("expected tool call name in logs")
	}
}

func TestMetricsMiddleware(t *testing.T) {
	metrics := &Metrics{}
	model := &mockModel{response: &core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.TextPart{Content: "hello"},
			core.ToolCallPart{ToolName: "tool1", ToolCallID: "c1", ArgsJSON: "{}"},
		},
		ModelName:    "test-model",
		Usage:        core.Usage{InputTokens: 100, OutputTokens: 50},
		FinishReason: core.FinishReasonStop,
	}}

	mw := NewMetrics(metrics)
	wrapped := Wrap(model, mw)

	// Make 3 requests.
	for range 3 {
		wrapped.Request(context.Background(), nil, nil, nil)
	}

	snap := metrics.Snapshot()
	if snap.RequestCount != 3 {
		t.Errorf("expected 3 requests, got %d", snap.RequestCount)
	}
	if snap.ErrorCount != 0 {
		t.Errorf("expected 0 errors, got %d", snap.ErrorCount)
	}
	if snap.InputTokens != 300 {
		t.Errorf("expected 300 input tokens, got %d", snap.InputTokens)
	}
	if snap.OutputTokens != 150 {
		t.Errorf("expected 150 output tokens, got %d", snap.OutputTokens)
	}
	if snap.ToolCalls != 3 {
		t.Errorf("expected 3 tool calls, got %d", snap.ToolCalls)
	}
	if snap.TotalDuration <= 0 {
		t.Error("expected positive duration")
	}
}

func TestMetricsMiddlewareErrors(t *testing.T) {
	metrics := &Metrics{}
	model := &mockModel{err: errors.New("fail")}

	mw := NewMetrics(metrics)
	wrapped := Wrap(model, mw)

	wrapped.Request(context.Background(), nil, nil, nil)

	snap := metrics.Snapshot()
	if snap.RequestCount != 1 {
		t.Errorf("expected 1 request, got %d", snap.RequestCount)
	}
	if snap.ErrorCount != 1 {
		t.Errorf("expected 1 error, got %d", snap.ErrorCount)
	}
}

func TestMetricsAverageLatency(t *testing.T) {
	snap := MetricsSnapshot{
		RequestCount:  10,
		TotalDuration: 100 * time.Millisecond,
	}
	avg := snap.AverageLatency()
	if avg != 10*time.Millisecond {
		t.Errorf("expected 10ms average, got %v", avg)
	}

	// Zero requests.
	snap2 := MetricsSnapshot{RequestCount: 0}
	if snap2.AverageLatency() != 0 {
		t.Error("expected 0 for no requests")
	}
}

func TestRequestStreamDelegates(t *testing.T) {
	model := &mockModel{}
	noop := Func(func(next RequestFunc) RequestFunc { return next })
	wrapped := Wrap(model, noop)

	_, err := wrapped.RequestStream(context.Background(), nil, nil, nil)
	if err == nil || err.Error() != "not implemented" {
		t.Errorf("expected 'not implemented' error, got %v", err)
	}
}

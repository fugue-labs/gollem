package middleware

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/fugue-labs/gollem/core"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func setupOTel(t *testing.T) (*OTelMiddleware, *tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()

	spanExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(spanExporter),
	)

	metricReader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(metricReader),
	)

	otelMW, err := NewOTel(
		WithTracerProvider(tp),
		WithMeterProvider(mp),
	)
	if err != nil {
		t.Fatalf("failed to create OTel middleware: %v", err)
	}

	return otelMW, spanExporter, metricReader
}

func TestOTelMiddlewareBasicRequest(t *testing.T) {
	otelMW, spanExporter, metricReader := setupOTel(t)

	model := &mockModel{response: &core.ModelResponse{
		Parts:        []core.ModelResponsePart{core.TextPart{Content: "hello"}},
		ModelName:    "test-model",
		Usage:        core.Usage{InputTokens: 100, OutputTokens: 50},
		FinishReason: core.FinishReasonStop,
	}}

	wrapped := Wrap(model, otelMW)

	_, err := wrapped.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "test"}}},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Check spans.
	spans := spanExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]
	if span.Name != "core.request" {
		t.Errorf("expected span name 'core.request', got %q", span.Name)
	}

	// Check span attributes.
	attrMap := make(map[string]any)
	for _, attr := range span.Attributes {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}

	if v, ok := attrMap["core.model"]; !ok || v != "test-model" {
		t.Errorf("expected core.model=test-model, got %v", v)
	}
	if v, ok := attrMap["core.input_tokens"]; !ok || v != int64(100) {
		t.Errorf("expected core.input_tokens=100, got %v", v)
	}
	if v, ok := attrMap["core.output_tokens"]; !ok || v != int64(50) {
		t.Errorf("expected core.output_tokens=50, got %v", v)
	}
	if v, ok := attrMap["core.finish_reason"]; !ok || v != "stop" {
		t.Errorf("expected core.finish_reason=stop, got %v", v)
	}

	// Check metrics.
	var rm metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	metricMap := collectMetrics(rm)

	if v, ok := metricMap["core.requests"]; !ok || v != int64(1) {
		t.Errorf("expected core.requests=1, got %v", v)
	}
	if v, ok := metricMap["core.tokens.input"]; !ok || v != int64(100) {
		t.Errorf("expected core.tokens.input=100, got %v", v)
	}
	if v, ok := metricMap["core.tokens.output"]; !ok || v != int64(50) {
		t.Errorf("expected core.tokens.output=50, got %v", v)
	}
}

func TestOTelMiddlewareError(t *testing.T) {
	otelMW, spanExporter, metricReader := setupOTel(t)

	model := &mockModel{err: errors.New("API error")}

	wrapped := Wrap(model, otelMW)

	_, err := wrapped.Request(context.Background(), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}

	// Check span has error status.
	spans := spanExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]
	if span.Status.Code != codes.Error {
		t.Errorf("expected error status code, got %d", span.Status.Code)
	}

	// Check error counter.
	var rm metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	metricMap := collectMetrics(rm)
	if v, ok := metricMap["core.errors"]; !ok || v != int64(1) {
		t.Errorf("expected core.errors=1, got %v", v)
	}
}

func TestOTelMiddlewareToolCalls(t *testing.T) {
	otelMW, spanExporter, metricReader := setupOTel(t)

	model := &mockModel{response: &core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.ToolCallPart{ToolName: "get_weather", ToolCallID: "call_1", ArgsJSON: "{}"},
			core.ToolCallPart{ToolName: "get_time", ToolCallID: "call_2", ArgsJSON: "{}"},
		},
		ModelName:    "test-model",
		FinishReason: core.FinishReasonToolCall,
	}}

	wrapped := Wrap(model, otelMW)
	wrapped.Request(context.Background(), nil, nil, nil)

	// Check tool call names in span.
	spans := spanExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	attrMap := make(map[string]any)
	for _, attr := range spans[0].Attributes {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}

	toolCalls, ok := attrMap["core.tool_calls"]
	if !ok {
		t.Fatal("expected core.tool_calls attribute")
	}

	toolCallSlice, ok := toolCalls.([]string)
	if !ok {
		t.Fatalf("expected []string for tool_calls, got %T", toolCalls)
	}
	if len(toolCallSlice) != 2 {
		t.Errorf("expected 2 tool calls, got %d", len(toolCallSlice))
	}

	// Check tool call counter.
	var rm metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	metricMap := collectMetrics(rm)
	if v, ok := metricMap["core.tool_calls"]; !ok || v != int64(2) {
		t.Errorf("expected core.tool_calls=2, got %v", v)
	}
}

func TestOTelMiddlewareWithDefaultProviders(t *testing.T) {
	// Test that NewOTel works with default (global) providers.
	otelMW, err := NewOTel()
	if err != nil {
		t.Fatalf("failed to create OTel middleware with defaults: %v", err)
	}

	if otelMW.tracer == nil {
		t.Error("expected tracer to be set")
	}
	if otelMW.meter == nil {
		t.Error("expected meter to be set")
	}
}

func TestOTelMiddlewareMultipleRequests(t *testing.T) {
	otelMW, _, metricReader := setupOTel(t)

	model := &mockModel{response: &core.ModelResponse{
		Parts:        []core.ModelResponsePart{core.TextPart{Content: "ok"}},
		ModelName:    "test-model",
		Usage:        core.Usage{InputTokens: 10, OutputTokens: 5},
		FinishReason: core.FinishReasonStop,
	}}

	wrapped := Wrap(model, otelMW)

	for range 5 {
		wrapped.Request(context.Background(), nil, nil, nil)
	}

	var rm metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	metricMap := collectMetrics(rm)

	if v, ok := metricMap["core.requests"]; !ok || v != int64(5) {
		t.Errorf("expected core.requests=5, got %v", v)
	}
	if v, ok := metricMap["core.tokens.input"]; !ok || v != int64(50) {
		t.Errorf("expected core.tokens.input=50, got %v", v)
	}
	if v, ok := metricMap["core.tokens.output"]; !ok || v != int64(25) {
		t.Errorf("expected core.tokens.output=25, got %v", v)
	}
}

// testStreamedResponse is a simple mock for testing stream middleware.
type testStreamedResponse struct {
	response *core.ModelResponse
	idx      int
	phase    int
}

func (s *testStreamedResponse) Next() (core.ModelResponseStreamEvent, error) {
	if s.phase == 0 {
		if s.idx < len(s.response.Parts) {
			event := core.PartStartEvent{
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
			event := core.PartEndEvent{Index: s.idx}
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

func (s *testStreamedResponse) Response() *core.ModelResponse {
	return s.response
}

func (s *testStreamedResponse) Usage() core.Usage {
	return s.response.Usage
}

func (s *testStreamedResponse) Close() error {
	return nil
}

// streamMockModel supports both Request and RequestStream.
type streamMockModel struct {
	response *core.ModelResponse
	err      error
}

func (m *streamMockModel) ModelName() string { return "test-model" }
func (m *streamMockModel) Request(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
	return m.response, m.err
}
func (m *streamMockModel) RequestStream(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &testStreamedResponse{response: m.response}, nil
}

func TestOTelMiddlewareStreamRequest(t *testing.T) {
	otelMW, spanExporter, metricReader := setupOTel(t)

	model := &streamMockModel{response: &core.ModelResponse{
		Parts:        []core.ModelResponsePart{core.TextPart{Content: "hello"}},
		ModelName:    "test-model",
		Usage:        core.Usage{InputTokens: 20, OutputTokens: 10},
		FinishReason: core.FinishReasonStop,
	}}

	wrapped := Wrap(model, otelMW)

	stream, err := wrapped.RequestStream(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("RequestStream failed: %v", err)
	}

	// Consume the stream.
	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}

	// Check that span was created and finalized.
	spans := spanExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]
	if span.Name != "core.stream_request" {
		t.Errorf("expected span name 'core.stream_request', got %q", span.Name)
	}

	// Check metrics.
	var rm metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	metricMap := collectMetrics(rm)
	if v, ok := metricMap["core.requests"]; !ok || v != int64(1) {
		t.Errorf("expected core.requests=1, got %v", v)
	}
	if v, ok := metricMap["core.tokens.input"]; !ok || v != int64(20) {
		t.Errorf("expected core.tokens.input=20, got %v", v)
	}
}

func TestOTelMiddlewareStreamRequestError(t *testing.T) {
	otelMW, spanExporter, metricReader := setupOTel(t)

	model := &streamMockModel{err: errors.New("stream error")}

	wrapped := Wrap(model, otelMW)

	_, err := wrapped.RequestStream(context.Background(), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}

	spans := spanExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	if spans[0].Status.Code != codes.Error {
		t.Errorf("expected error status code, got %d", spans[0].Status.Code)
	}

	var rm metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	metricMap := collectMetrics(rm)
	if v, ok := metricMap["core.errors"]; !ok || v != int64(1) {
		t.Errorf("expected core.errors=1, got %v", v)
	}
}

func TestOTelMiddlewareDurationRecorded(t *testing.T) {
	otelMW, _, metricReader := setupOTel(t)

	model := &mockModel{response: &core.ModelResponse{
		Parts:        []core.ModelResponsePart{core.TextPart{Content: "ok"}},
		ModelName:    "test-model",
		FinishReason: core.FinishReasonStop,
	}}

	wrapped := Wrap(model, otelMW)
	wrapped.Request(context.Background(), nil, nil, nil)

	var rm metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	// Check that duration histogram has data.
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "core.request.duration" {
				found = true
				h, ok := m.Data.(metricdata.Histogram[float64])
				if !ok {
					t.Fatalf("expected Histogram, got %T", m.Data)
				}
				if len(h.DataPoints) == 0 {
					t.Error("expected at least one data point")
				}
			}
		}
	}
	if !found {
		t.Error("expected core.request.duration metric")
	}
}

// collectMetrics extracts counter values from ResourceMetrics into a map.
func collectMetrics(rm metricdata.ResourceMetrics) map[string]int64 {
	result := make(map[string]int64)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				var total int64
				for _, dp := range data.DataPoints {
					total += dp.Value
				}
				result[m.Name] = total
			}
		}
	}
	return result
}

// Test streaming middleware support in middleware.go.

func TestStreamMiddlewareApplied(t *testing.T) {
	var order []string

	model := &streamMockModel{response: &core.ModelResponse{
		Parts:        []core.ModelResponsePart{core.TextPart{Content: "hello"}},
		ModelName:    "test",
		FinishReason: core.FinishReasonStop,
	}}

	// Create a StreamFunc that tracks both request and stream wrapping.
	mw := StreamFunc{
		Request: func(next RequestFunc) RequestFunc {
			return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
				order = append(order, "request-before")
				resp, err := next(ctx, messages, settings, params)
				order = append(order, "request-after")
				return resp, err
			}
		},
		Stream: func(next StreamRequestFunc) StreamRequestFunc {
			return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
				order = append(order, "stream-before")
				stream, err := next(ctx, messages, settings, params)
				order = append(order, "stream-after")
				return stream, err
			}
		},
	}

	wrapped := Wrap(model, mw)

	// Test normal request.
	wrapped.Request(context.Background(), nil, nil, nil)
	if len(order) != 2 || order[0] != "request-before" || order[1] != "request-after" {
		t.Errorf("unexpected request order: %v", order)
	}

	// Test stream request.
	order = nil
	wrapped.RequestStream(context.Background(), nil, nil, nil)
	if len(order) != 2 || order[0] != "stream-before" || order[1] != "stream-after" {
		t.Errorf("unexpected stream order: %v", order)
	}
}

func TestNonStreamMiddlewareSkippedForStream(t *testing.T) {
	model := &streamMockModel{response: &core.ModelResponse{
		Parts:        []core.ModelResponsePart{core.TextPart{Content: "hello"}},
		ModelName:    "test",
		FinishReason: core.FinishReasonStop,
	}}

	// Func only implements Middleware, not StreamMiddleware.
	callCount := 0
	mw := Func(func(next RequestFunc) RequestFunc {
		return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
			callCount++
			return next(ctx, messages, settings, params)
		}
	})

	wrapped := Wrap(model, mw)

	// Stream should bypass the Func middleware.
	stream, err := wrapped.RequestStream(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("RequestStream failed: %v", err)
	}

	// Consume stream.
	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if callCount != 0 {
		t.Errorf("expected Func middleware to not be called for stream, got %d calls", callCount)
	}
}

func TestStreamFuncNilHandlers(t *testing.T) {
	mw := StreamFunc{}

	// WrapRequest with nil should pass through.
	called := false
	next := RequestFunc(func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		called = true
		return nil, nil
	})
	wrapped := mw.WrapRequest(next)
	wrapped(context.Background(), nil, nil, nil)
	if !called {
		t.Error("expected next to be called")
	}

	// WrapStreamRequest with nil should pass through.
	streamCalled := false
	nextStream := StreamRequestFunc(func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
		streamCalled = true
		return nil, nil
	})
	wrappedStream := mw.WrapStreamRequest(nextStream)
	wrappedStream(context.Background(), nil, nil, nil)
	if !streamCalled {
		t.Error("expected next stream to be called")
	}
}

// Ensure OTelMiddleware implements StreamMiddleware.
var _ StreamMiddleware = (*OTelMiddleware)(nil)

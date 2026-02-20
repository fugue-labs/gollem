package middleware

import (
	"context"
	"time"

	"github.com/fugue-labs/gollem/core"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName = "github.com/fugue-labs/gollem/ext/middleware"
)

// OTelMiddleware provides OpenTelemetry tracing and metrics for model requests.
// It creates spans for each request and records token usage and latency metrics.
type OTelMiddleware struct {
	tracer trace.Tracer
	meter  metric.Meter

	// Metrics instruments.
	requestCounter  metric.Int64Counter
	errorCounter    metric.Int64Counter
	inputTokens     metric.Int64Counter
	outputTokens    metric.Int64Counter
	requestDuration metric.Float64Histogram
	toolCallCounter metric.Int64Counter
}

// OTelOption configures the OTel middleware.
type OTelOption func(*otelConfig)

type otelConfig struct {
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
}

// WithTracerProvider sets a custom tracer provider.
func WithTracerProvider(tp trace.TracerProvider) OTelOption {
	return func(c *otelConfig) {
		c.tracerProvider = tp
	}
}

// WithMeterProvider sets a custom meter provider.
func WithMeterProvider(mp metric.MeterProvider) OTelOption {
	return func(c *otelConfig) {
		c.meterProvider = mp
	}
}

// NewOTel creates a new OpenTelemetry middleware with tracing and metrics.
func NewOTel(opts ...OTelOption) (*OTelMiddleware, error) {
	cfg := &otelConfig{
		tracerProvider: otel.GetTracerProvider(),
		meterProvider:  otel.GetMeterProvider(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	tracer := cfg.tracerProvider.Tracer(instrumentationName)
	meter := cfg.meterProvider.Meter(instrumentationName)

	requestCounter, err := meter.Int64Counter("core.requests",
		metric.WithDescription("Total number of model requests"),
	)
	if err != nil {
		return nil, err
	}

	errorCounter, err := meter.Int64Counter("core.errors",
		metric.WithDescription("Total number of failed model requests"),
	)
	if err != nil {
		return nil, err
	}

	inputTokens, err := meter.Int64Counter("core.tokens.input",
		metric.WithDescription("Total input tokens consumed"),
	)
	if err != nil {
		return nil, err
	}

	outputTokens, err := meter.Int64Counter("core.tokens.output",
		metric.WithDescription("Total output tokens consumed"),
	)
	if err != nil {
		return nil, err
	}

	requestDuration, err := meter.Float64Histogram("core.request.duration",
		metric.WithDescription("Duration of model requests in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	toolCallCounter, err := meter.Int64Counter("core.tool_calls",
		metric.WithDescription("Total number of tool calls in responses"),
	)
	if err != nil {
		return nil, err
	}

	return &OTelMiddleware{
		tracer:          tracer,
		meter:           meter,
		requestCounter:  requestCounter,
		errorCounter:    errorCounter,
		inputTokens:     inputTokens,
		outputTokens:    outputTokens,
		requestDuration: requestDuration,
		toolCallCounter: toolCallCounter,
	}, nil
}

// WrapRequest implements Middleware.
func (o *OTelMiddleware) WrapRequest(next RequestFunc) RequestFunc {
	return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		ctx, span := o.tracer.Start(ctx, "core.request",
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.Int("core.message_count", len(messages)),
			),
		)
		defer span.End()

		start := time.Now()
		o.requestCounter.Add(ctx, 1)

		resp, err := next(ctx, messages, settings, params)
		duration := time.Since(start)

		o.requestDuration.Record(ctx, duration.Seconds())

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			o.errorCounter.Add(ctx, 1)
			return nil, err
		}

		span.SetAttributes(
			attribute.String("core.model", resp.ModelName),
			attribute.Int("core.input_tokens", resp.Usage.InputTokens),
			attribute.Int("core.output_tokens", resp.Usage.OutputTokens),
			attribute.String("core.finish_reason", string(resp.FinishReason)),
		)
		span.SetStatus(codes.Ok, "")

		o.inputTokens.Add(ctx, int64(resp.Usage.InputTokens))
		o.outputTokens.Add(ctx, int64(resp.Usage.OutputTokens))

		toolCalls := resp.ToolCalls()
		if len(toolCalls) > 0 {
			o.toolCallCounter.Add(ctx, int64(len(toolCalls)))
			toolNames := make([]string, len(toolCalls))
			for i, tc := range toolCalls {
				toolNames[i] = tc.ToolName
			}
			span.SetAttributes(attribute.StringSlice("core.tool_calls", toolNames))
		}

		return resp, nil
	}
}

// WrapStreamRequest implements StreamMiddleware.
func (o *OTelMiddleware) WrapStreamRequest(next StreamRequestFunc) StreamRequestFunc {
	return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
		ctx, span := o.tracer.Start(ctx, "core.stream_request",
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.Int("core.message_count", len(messages)),
			),
		)

		start := time.Now()
		o.requestCounter.Add(ctx, 1)

		stream, err := next(ctx, messages, settings, params)
		if err != nil {
			duration := time.Since(start)
			o.requestDuration.Record(ctx, duration.Seconds())
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			o.errorCounter.Add(ctx, 1)
			span.End()
			return nil, err
		}

		return &trackedStreamResponse{
			inner:     stream,
			span:      span,
			start:     start,
			otel:      o,
			ctx:       ctx,
		}, nil
	}
}

// Verify OTelMiddleware implements StreamMiddleware.
var _ StreamMiddleware = (*OTelMiddleware)(nil)

// trackedStreamResponse wraps a StreamedResponse to finalize the span when
// the stream is closed or fully consumed.
type trackedStreamResponse struct {
	inner  core.StreamedResponse
	span   trace.Span
	start  time.Time
	otel   *OTelMiddleware
	ctx    context.Context
	ended  bool
}

func (t *trackedStreamResponse) Next() (core.ModelResponseStreamEvent, error) {
	event, err := t.inner.Next()
	if err != nil {
		t.finalize(err)
	}
	return event, err
}

func (t *trackedStreamResponse) Response() *core.ModelResponse {
	return t.inner.Response()
}

func (t *trackedStreamResponse) Usage() core.Usage {
	return t.inner.Usage()
}

func (t *trackedStreamResponse) Close() error {
	err := t.inner.Close()
	t.finalize(nil)
	return err
}

func (t *trackedStreamResponse) finalize(streamErr error) {
	if t.ended {
		return
	}
	t.ended = true

	duration := time.Since(t.start)
	t.otel.requestDuration.Record(t.ctx, duration.Seconds())

	usage := t.inner.Usage()
	t.otel.inputTokens.Add(t.ctx, int64(usage.InputTokens))
	t.otel.outputTokens.Add(t.ctx, int64(usage.OutputTokens))

	t.span.SetAttributes(
		attribute.Int("core.input_tokens", usage.InputTokens),
		attribute.Int("core.output_tokens", usage.OutputTokens),
	)

	if resp := t.inner.Response(); resp != nil {
		t.span.SetAttributes(
			attribute.String("core.model", resp.ModelName),
			attribute.String("core.finish_reason", string(resp.FinishReason)),
		)
		toolCalls := resp.ToolCalls()
		if len(toolCalls) > 0 {
			t.otel.toolCallCounter.Add(t.ctx, int64(len(toolCalls)))
		}
	}

	t.span.SetStatus(codes.Ok, "")
	t.span.End()
}

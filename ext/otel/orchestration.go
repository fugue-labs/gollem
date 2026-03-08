package otel

import (
	"context"
	"fmt"

	"github.com/fugue-labs/gollem/core/orchestration"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TracingPipelineStep wraps a PipelineStep to produce an OTEL span.
func TracingPipelineStep(name string, step orchestration.PipelineStep, opts ...TracingOption) orchestration.PipelineStep {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	var tp trace.TracerProvider
	if cfg.tracerProvider != nil {
		tp = cfg.tracerProvider
	} else {
		tp = otel.GetTracerProvider()
	}
	tracer := tp.Tracer(tracingInstrumentationName)

	return func(ctx context.Context, input string) (string, error) {
		ctx, span := tracer.Start(ctx, cfg.spanName(SpanPipelineStep+"."+name),
			trace.WithAttributes(
				attribute.String(AttrPipelineStepName, name),
			),
		)
		defer span.End()

		result, err := step(ctx, input)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return "", err
		}
		span.SetStatus(codes.Ok, "")
		return result, nil
	}
}

// TracingPipeline wraps a Pipeline.Run call in a root pipeline span and
// wraps each step with its own child span.
func TracingPipeline(steps []orchestration.PipelineStep, opts ...TracingOption) *orchestration.Pipeline {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	var tp trace.TracerProvider
	if cfg.tracerProvider != nil {
		tp = cfg.tracerProvider
	} else {
		tp = otel.GetTracerProvider()
	}
	tracer := tp.Tracer(tracingInstrumentationName)

	wrappedSteps := make([]orchestration.PipelineStep, len(steps))
	for i, step := range steps {
		idx := i
		s := step
		wrappedSteps[i] = func(ctx context.Context, input string) (string, error) {
			ctx, span := tracer.Start(ctx, cfg.spanName(fmt.Sprintf("%s[%d]", SpanPipelineStep, idx)),
				trace.WithAttributes(
					attribute.Int(AttrPipelineStepIndex, idx),
				),
			)
			defer span.End()

			result, err := s(ctx, input)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return "", err
			}
			span.SetStatus(codes.Ok, "")
			return result, nil
		}
	}

	return orchestration.NewPipeline(wrappedSteps...)
}

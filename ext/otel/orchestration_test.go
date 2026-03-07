package otel

import (
	"context"
	"testing"

	"github.com/fugue-labs/gollem/core/orchestration"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTracingPipelineStep(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)

	step := TracingPipelineStep("uppercase", func(ctx context.Context, input string) (string, error) {
		return input + "!", nil
	}, WithTracerProvider(tp))

	result, err := step(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello!" {
		t.Errorf("expected 'hello!', got %q", result)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name != SpanPipelineStep+".uppercase" {
		t.Errorf("expected span name '%s.uppercase', got %q", SpanPipelineStep, spans[0].Name)
	}
}

func TestTracingPipeline(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)

	steps := []orchestration.PipelineStep{
		func(ctx context.Context, input string) (string, error) {
			return input + " step1", nil
		},
		func(ctx context.Context, input string) (string, error) {
			return input + " step2", nil
		},
	}

	pipeline := TracingPipeline(steps, WithTracerProvider(tp))
	result, err := pipeline.Run(context.Background(), "start")
	if err != nil {
		t.Fatal(err)
	}
	if result != "start step1 step2" {
		t.Errorf("expected 'start step1 step2', got %q", result)
	}

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
}

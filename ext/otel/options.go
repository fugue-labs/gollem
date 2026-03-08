package otel

import "go.opentelemetry.io/otel/trace"

// TracingOption configures the OTEL tracing behavior.
type TracingOption func(*tracingConfig)

type tracingConfig struct {
	tracerProvider     trace.TracerProvider
	serviceName        string
	captureToolArgs    bool
	captureToolResults bool
	captureModelMsgs   bool
	maxAttributeLength int
	spanNamePrefix     string
}

const (
	defaultServiceName         = "gollem"
	defaultMaxAttributeLength  = 4096
	tracingInstrumentationName = "github.com/fugue-labs/gollem/ext/otel"
)

func defaultConfig() *tracingConfig {
	return &tracingConfig{
		serviceName:        defaultServiceName,
		maxAttributeLength: defaultMaxAttributeLength,
	}
}

// WithTracerProvider sets a custom OTEL tracer provider.
// If not set, the global tracer provider is used.
func WithTracerProvider(tp trace.TracerProvider) TracingOption {
	return func(c *tracingConfig) {
		c.tracerProvider = tp
	}
}

// WithServiceName sets the service name used in span attributes.
func WithServiceName(name string) TracingOption {
	return func(c *tracingConfig) {
		c.serviceName = name
	}
}

// WithCaptureToolArgs includes tool call arguments in span attributes.
// This may contain sensitive data; off by default.
func WithCaptureToolArgs(capture bool) TracingOption {
	return func(c *tracingConfig) {
		c.captureToolArgs = capture
	}
}

// WithCaptureToolResults includes tool results in span attributes.
// This may contain sensitive data; off by default.
func WithCaptureToolResults(capture bool) TracingOption {
	return func(c *tracingConfig) {
		c.captureToolResults = capture
	}
}

// WithCaptureModelMessages includes full model message content in spans.
// This may contain PII; off by default.
func WithCaptureModelMessages(capture bool) TracingOption {
	return func(c *tracingConfig) {
		c.captureModelMsgs = capture
	}
}

// WithMaxAttributeLength sets the maximum character length for string attributes.
// Longer values are truncated. Default: 4096.
func WithMaxAttributeLength(n int) TracingOption {
	return func(c *tracingConfig) {
		c.maxAttributeLength = n
	}
}

// WithSpanNamePrefix adds a prefix to all span names (e.g. "gollem" → "gollem.agent.run").
func WithSpanNamePrefix(prefix string) TracingOption {
	return func(c *tracingConfig) {
		c.spanNamePrefix = prefix
	}
}

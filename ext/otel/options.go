package otel

import "go.opentelemetry.io/otel/trace"

// TracingOption configures the OTEL tracing behavior.
type TracingOption func(*tracingConfig)

type tracingConfig struct {
	tracerProvider     trace.TracerProvider
	captureToolArgs    bool
	captureToolResults bool
	captureModelMsgs   bool
	maxAttributeLength int
	spanNamePrefix     string
}

const (
	defaultMaxAttributeLength  = 4096
	tracingInstrumentationName = "github.com/fugue-labs/gollem/ext/otel"
)

func defaultConfig() *tracingConfig {
	return &tracingConfig{
		maxAttributeLength: defaultMaxAttributeLength,
		captureToolArgs:    true,
	}
}

// WithTracerProvider sets a custom OTEL tracer provider.
// If not set, the global tracer provider is used.
func WithTracerProvider(tp trace.TracerProvider) TracingOption {
	return func(c *tracingConfig) {
		c.tracerProvider = tp
	}
}

// WithCaptureToolArgs controls whether tool call arguments are included in
// span attributes. Enabled by default for debuggability. Disable with
// WithCaptureToolArgs(false) if tool arguments may contain sensitive data.
func WithCaptureToolArgs(capture bool) TracingOption {
	return func(c *tracingConfig) {
		c.captureToolArgs = capture
	}
}

// WithCaptureToolResults includes tool results in span attributes.
// WARNING: Tool results may contain sensitive data (API keys, PII).
// Only enable in secure, access-controlled environments. Off by default.
func WithCaptureToolResults(capture bool) TracingOption {
	return func(c *tracingConfig) {
		c.captureToolResults = capture
	}
}

// WithCaptureModelMessages includes full model message content in spans.
// WARNING: Model messages often contain PII and user data.
// Only enable in secure, access-controlled environments. Off by default.
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

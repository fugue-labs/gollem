// Package middleware provides a middleware chain for intercepting and
// wrapping gollem model requests, enabling cross-cutting concerns like
// logging, metrics, and tracing.
package middleware

import (
	"context"

	"github.com/fugue-labs/gollem/core"
)

// RequestFunc is the type for a model request handler.
type RequestFunc func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error)

// StreamRequestFunc is the type for a streaming model request handler.
type StreamRequestFunc func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error)

// Middleware intercepts model requests.
type Middleware interface {
	// WrapRequest wraps a request handler with middleware logic.
	WrapRequest(next RequestFunc) RequestFunc
}

// StreamMiddleware intercepts streaming model requests. Middleware
// implementations that also implement StreamMiddleware will have their
// WrapStreamRequest applied to streaming calls.
type StreamMiddleware interface {
	Middleware
	// WrapStreamRequest wraps a streaming request handler with middleware logic.
	WrapStreamRequest(next StreamRequestFunc) StreamRequestFunc
}

// Func is a function adapter for Middleware.
type Func func(next RequestFunc) RequestFunc

// WrapRequest implements Middleware.
func (f Func) WrapRequest(next RequestFunc) RequestFunc {
	return f(next)
}

// StreamFunc is a function adapter for StreamMiddleware.
type StreamFunc struct {
	// Request wraps a non-streaming request handler.
	Request func(next RequestFunc) RequestFunc
	// Stream wraps a streaming request handler.
	Stream func(next StreamRequestFunc) StreamRequestFunc
}

// WrapRequest implements Middleware.
func (f StreamFunc) WrapRequest(next RequestFunc) RequestFunc {
	if f.Request != nil {
		return f.Request(next)
	}
	return next
}

// WrapStreamRequest implements StreamMiddleware.
func (f StreamFunc) WrapStreamRequest(next StreamRequestFunc) StreamRequestFunc {
	if f.Stream != nil {
		return f.Stream(next)
	}
	return next
}

// wrappedModel applies a middleware chain to a core.Model.
type wrappedModel struct {
	inner       core.Model
	middlewares []Middleware
}

// Wrap creates a new model that applies the given middleware chain to all requests.
// Middlewares are applied in order: first middleware is outermost (executes first).
// If a middleware implements StreamMiddleware, it will also be applied to
// streaming requests via RequestStream.
func Wrap(model core.Model, middlewares ...Middleware) core.Model {
	if len(middlewares) == 0 {
		return model
	}
	return &wrappedModel{
		inner:       model,
		middlewares: middlewares,
	}
}

// ModelName returns the underlying model's name.
func (m *wrappedModel) ModelName() string {
	return m.inner.ModelName()
}

// Request sends a request through the middleware chain.
func (m *wrappedModel) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	handler := RequestFunc(func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return m.inner.Request(ctx, messages, settings, params)
	})

	// Apply middlewares in reverse order so first middleware is outermost.
	for i := len(m.middlewares) - 1; i >= 0; i-- {
		handler = m.middlewares[i].WrapRequest(handler)
	}

	return handler(ctx, messages, settings, params)
}

// RequestStream sends a streaming request through the middleware chain.
// Only middlewares that implement StreamMiddleware are applied.
func (m *wrappedModel) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	handler := StreamRequestFunc(func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
		return m.inner.RequestStream(ctx, messages, settings, params)
	})

	// Apply stream middlewares in reverse order so first middleware is outermost.
	for i := len(m.middlewares) - 1; i >= 0; i-- {
		if sm, ok := m.middlewares[i].(StreamMiddleware); ok {
			handler = sm.WrapStreamRequest(handler)
		}
	}

	return handler(ctx, messages, settings, params)
}

// Verify wrappedModel implements core.Model.
var _ core.Model = (*wrappedModel)(nil)

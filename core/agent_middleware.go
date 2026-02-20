package core

import (
	"context"
	"time"
)

// AgentMiddleware wraps a model call within the agent loop.
// next is the actual model call — middleware can modify inputs, outputs, or skip the call.
type AgentMiddleware func(
	ctx context.Context,
	messages []ModelMessage,
	settings *ModelSettings,
	params *ModelRequestParameters,
	next func(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error),
) (*ModelResponse, error)

// WithAgentMiddleware adds middleware to the agent's model call chain.
// Middleware is applied in order (first registered = outermost wrapper).
func WithAgentMiddleware[T any](mw AgentMiddleware) AgentOption[T] {
	return func(a *Agent[T]) {
		a.middleware = append(a.middleware, mw)
	}
}

// LoggingMiddleware logs model request/response summaries.
func LoggingMiddleware(logger func(msg string)) AgentMiddleware {
	return func(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters, next func(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error)) (*ModelResponse, error) {
		logger("model request: " + summarizeMessages(messages))
		resp, err := next(ctx, messages, settings, params)
		if err != nil {
			logger("model error: " + err.Error())
		} else {
			logger("model response: " + resp.TextContent())
		}
		return resp, err
	}
}

// MaxTokensMiddleware limits the number of tokens per request via ModelSettings.
func MaxTokensMiddleware(maxTokens int) AgentMiddleware {
	return func(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters, next func(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error)) (*ModelResponse, error) {
		if settings == nil {
			settings = &ModelSettings{}
		}
		s := *settings
		s.MaxTokens = &maxTokens
		return next(ctx, messages, &s, params)
	}
}

// TimingMiddleware records request duration and calls a callback.
func TimingMiddleware(callback func(duration time.Duration)) AgentMiddleware {
	return func(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters, next func(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error)) (*ModelResponse, error) {
		start := time.Now()
		resp, err := next(ctx, messages, settings, params)
		callback(time.Since(start))
		return resp, err
	}
}

// summarizeMessages creates a brief summary of a message list.
func summarizeMessages(messages []ModelMessage) string {
	if len(messages) == 0 {
		return "(empty)"
	}
	last := messages[len(messages)-1]
	if req, ok := last.(ModelRequest); ok {
		for i := len(req.Parts) - 1; i >= 0; i-- {
			if up, ok := req.Parts[i].(UserPromptPart); ok {
				if len(up.Content) > 50 {
					return up.Content[:50] + "..."
				}
				return up.Content
			}
		}
	}
	return "(messages)"
}

// buildMiddlewareChain wraps a model.Request call with the configured middleware.
func buildMiddlewareChain(
	middleware []AgentMiddleware,
	model Model,
) func(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error) {
	// Innermost: actual model call.
	chain := model.Request

	// Wrap in reverse order so first middleware is outermost.
	for i := len(middleware) - 1; i >= 0; i-- {
		mw := middleware[i]
		next := chain
		chain = func(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters) (*ModelResponse, error) {
			return mw(ctx, messages, settings, params, next)
		}
	}

	return chain
}

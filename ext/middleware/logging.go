package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// LoggingMiddleware logs each model request and response using slog.
type LoggingMiddleware struct {
	Logger *slog.Logger
	Level  slog.Level
}

// NewLogging creates a new logging middleware with the given logger and level.
func NewLogging(logger *slog.Logger, level slog.Level) *LoggingMiddleware {
	return &LoggingMiddleware{
		Logger: logger,
		Level:  level,
	}
}

// WrapRequest implements Middleware.
func (l *LoggingMiddleware) WrapRequest(next RequestFunc) RequestFunc {
	return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		start := time.Now()

		l.Logger.Log(ctx, l.Level, "model request started",
			"message_count", len(messages),
		)

		resp, err := next(ctx, messages, settings, params)
		duration := time.Since(start)

		if err != nil {
			l.Logger.Log(ctx, l.Level, "model request failed",
				"duration", duration,
				"error", err.Error(),
			)
			return nil, err
		}

		// Collect tool call names.
		var toolNames []string
		for _, tc := range resp.ToolCalls() {
			toolNames = append(toolNames, tc.ToolName)
		}

		attrs := []any{
			"duration", duration,
			"model", resp.ModelName,
			"input_tokens", resp.Usage.InputTokens,
			"output_tokens", resp.Usage.OutputTokens,
			"finish_reason", string(resp.FinishReason),
		}
		if len(toolNames) > 0 {
			attrs = append(attrs, "tool_calls", toolNames)
		}

		l.Logger.Log(ctx, l.Level, "model request completed", attrs...)

		return resp, nil
	}
}

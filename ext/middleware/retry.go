package middleware

import (
	"context"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// retryConfig holds configuration for the retry middleware.
type retryConfig struct {
	initialDelay time.Duration
	maxDelay     time.Duration
	retryIf      func(error) bool
}

// RetryOption configures retry behavior.
type RetryOption func(*retryConfig)

// WithInitialDelay sets the initial retry delay (default: 1s).
func WithInitialDelay(d time.Duration) RetryOption {
	return func(c *retryConfig) {
		c.initialDelay = d
	}
}

// WithMaxDelay sets the maximum delay between retries (default: 30s).
func WithMaxDelay(d time.Duration) RetryOption {
	return func(c *retryConfig) {
		c.maxDelay = d
	}
}

// WithRetryIf sets a function that determines if an error is retryable.
// By default, all errors are retried.
func WithRetryIf(fn func(error) bool) RetryOption {
	return func(c *retryConfig) {
		c.retryIf = fn
	}
}

// sleepFunc is used internally for sleeping; overridden in tests.
var sleepFunc = func(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// RetryMiddleware creates middleware that retries failed model requests
// with exponential backoff. It retries on any error up to maxRetries times.
func RetryMiddleware(maxRetries int, opts ...RetryOption) StreamFunc {
	cfg := &retryConfig{
		initialDelay: 1 * time.Second,
		maxDelay:     30 * time.Second,
		retryIf:      func(error) bool { return true },
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return StreamFunc{
		Request: func(next RequestFunc) RequestFunc {
			return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
				var lastErr error
				delay := cfg.initialDelay

				for attempt := 0; attempt <= maxRetries; attempt++ {
					resp, err := next(ctx, messages, settings, params)
					if err == nil {
						return resp, nil
					}
					lastErr = err

					// Check if error is retryable.
					if !cfg.retryIf(err) {
						return nil, err
					}

					// Don't sleep after the last attempt.
					if attempt < maxRetries {
						if err := sleepFunc(ctx, delay); err != nil {
							return nil, err
						}
						delay *= 2
						if delay > cfg.maxDelay {
							delay = cfg.maxDelay
						}
					}
				}

				return nil, lastErr
			}
		},
		Stream: func(next StreamRequestFunc) StreamRequestFunc {
			return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
				var lastErr error
				delay := cfg.initialDelay

				for attempt := 0; attempt <= maxRetries; attempt++ {
					resp, err := next(ctx, messages, settings, params)
					if err == nil {
						return resp, nil
					}
					lastErr = err

					// Check if error is retryable.
					if !cfg.retryIf(err) {
						return nil, err
					}

					// Don't sleep after the last attempt.
					if attempt < maxRetries {
						if err := sleepFunc(ctx, delay); err != nil {
							return nil, err
						}
						delay *= 2
						if delay > cfg.maxDelay {
							delay = cfg.maxDelay
						}
					}
				}

				return nil, lastErr
			}
		},
	}
}

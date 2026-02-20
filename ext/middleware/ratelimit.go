package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// rateLimiter implements a token bucket rate limiter using standard library primitives.
type rateLimiter struct {
	mu       sync.Mutex
	tokens   float64
	maxBurst float64
	rate     float64 // tokens per second
	last     time.Time
}

// newRateLimiter creates a new token bucket rate limiter.
func newRateLimiter(requestsPerSecond float64, burst int) *rateLimiter {
	return &rateLimiter{
		tokens:   float64(burst),
		maxBurst: float64(burst),
		rate:     requestsPerSecond,
		last:     time.Now(),
	}
}

// wait blocks until a token is available or the context is cancelled.
// It returns nil when a token is acquired, or the context error otherwise.
func (rl *rateLimiter) wait(ctx context.Context) error {
	for {
		rl.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(rl.last).Seconds()
		rl.last = now
		rl.tokens += elapsed * rl.rate
		if rl.tokens > rl.maxBurst {
			rl.tokens = rl.maxBurst
		}

		if rl.tokens >= 1.0 {
			rl.tokens--
			rl.mu.Unlock()
			return nil
		}

		// Calculate how long until the next token is available.
		deficit := 1.0 - rl.tokens
		waitDuration := time.Duration(deficit / rl.rate * float64(time.Second))
		rl.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitDuration):
			// Loop back to try acquiring a token.
		}
	}
}

// RateLimitMiddleware creates middleware that limits model request rate.
// It uses a token bucket algorithm with the specified requests per second
// and burst size.
func RateLimitMiddleware(requestsPerSecond float64, burst int) StreamFunc {
	rl := newRateLimiter(requestsPerSecond, burst)

	return StreamFunc{
		Request: func(next RequestFunc) RequestFunc {
			return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
				if err := rl.wait(ctx); err != nil {
					return nil, err
				}
				return next(ctx, messages, settings, params)
			}
		},
		Stream: func(next StreamRequestFunc) StreamRequestFunc {
			return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
				if err := rl.wait(ctx); err != nil {
					return nil, err
				}
				return next(ctx, messages, settings, params)
			}
		},
	}
}

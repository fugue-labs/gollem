package modelutil

import (
	"context"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// RateLimitedModel wraps a Model with a token-bucket rate limiter.
// Requests exceeding the rate are delayed, not rejected.
type RateLimitedModel struct {
	model    core.Model
	mu       sync.Mutex
	tokens   float64
	maxBurst float64
	rate     float64 // tokens per second
	last     time.Time
}

// NewRateLimitedModel creates a rate-limited model wrapper.
// requestsPerSecond is the sustained request rate.
// burst is the maximum number of concurrent requests allowed.
func NewRateLimitedModel(model core.Model, requestsPerSecond float64, burst int) *RateLimitedModel {
	return &RateLimitedModel{
		model:    model,
		tokens:   float64(burst),
		maxBurst: float64(burst),
		rate:     requestsPerSecond,
		last:     time.Now(),
	}
}

var _ core.Model = (*RateLimitedModel)(nil)

func (r *RateLimitedModel) ModelName() string {
	return r.model.ModelName()
}

func (r *RateLimitedModel) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	if err := r.wait(ctx); err != nil {
		return nil, err
	}
	return r.model.Request(ctx, messages, settings, params)
}

func (r *RateLimitedModel) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	if err := r.wait(ctx); err != nil {
		return nil, err
	}
	return r.model.RequestStream(ctx, messages, settings, params)
}

// wait blocks until a token is available or the context is cancelled.
func (r *RateLimitedModel) wait(ctx context.Context) error {
	for {
		r.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(r.last).Seconds()
		r.last = now
		r.tokens += elapsed * r.rate
		if r.tokens > r.maxBurst {
			r.tokens = r.maxBurst
		}

		if r.tokens >= 1.0 {
			r.tokens--
			r.mu.Unlock()
			return nil
		}

		deficit := 1.0 - r.tokens
		waitDuration := time.Duration(deficit / r.rate * float64(time.Second))
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitDuration):
			// Loop back to try acquiring a token.
		}
	}
}

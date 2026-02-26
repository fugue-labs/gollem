package modelutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// RetryConfig configures retry behavior.
type RetryConfig struct {
	MaxRetries        int              // maximum number of retries (default: 3)
	InitialBackoff    time.Duration    // initial wait time (default: 1s)
	MaxBackoff        time.Duration    // maximum wait time (default: 30s)
	BackoffFactor     float64          // multiplier per retry (default: 2.0)
	Jitter            bool             // add random jitter (default: true)
	MinRemaining      time.Duration    // skip retries when context deadline is too close (default: 20s)
	HeartbeatInterval time.Duration    // periodic log while waiting on provider response (default: 30s)
	IsRetryable       func(error) bool // determines if an error should be retried
}

// DefaultRetryConfig returns sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:        5,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        30 * time.Second,
		BackoffFactor:     2.0,
		Jitter:            true,
		MinRemaining:      20 * time.Second,
		HeartbeatInterval: 0,
		IsRetryable:       defaultIsRetryable,
	}
}

// isPermanent429 checks if a 429 response indicates a permanent condition
// (credits exhausted, billing issue) rather than a transient rate limit.
func isPermanent429(body string) bool {
	lower := strings.ToLower(body)
	for _, pattern := range []string{
		"credits",
		"spending limit",
		"billing",
		"quota exceeded",
		"plan limit",
		"subscription",
	} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// defaultIsRetryable checks for transient HTTP and connection errors.
func defaultIsRetryable(err error) bool {
	var httpErr *core.ModelHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusTooManyRequests:
			// Don't retry 429s caused by permanent billing/credit issues.
			if isPermanent429(httpErr.Body) {
				return false
			}
			return true
		case http.StatusRequestTimeout, // 408
			http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusServiceUnavailable,  // 503
			http.StatusGatewayTimeout:      // 504
			return true
		case 529: // Anthropic "Overloaded" — too many concurrent requests
			return true
		}
	}

	// Retry on connection-level errors (unexpected EOF, connection reset, timeout).
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// Check error message for common transient patterns.
	msg := err.Error()
	for _, pattern := range []string{"unexpected EOF", "connection reset", "connection refused", "broken pipe", "i/o timeout", "no such host", "dns", "temporary failure"} {
		if strings.Contains(strings.ToLower(msg), pattern) {
			return true
		}
	}

	return false
}

// RetryModel wraps a Model with exponential backoff retry for transient failures.
type RetryModel struct {
	model  core.Model
	config RetryConfig
}

type sessionModelCloner interface {
	NewSession() core.Model
}

type modelCloser interface {
	Close() error
}

// NewRetryModel creates a Model that retries transient failures.
func NewRetryModel(model core.Model, config RetryConfig) *RetryModel {
	if config.IsRetryable == nil {
		config.IsRetryable = defaultIsRetryable
	}
	if config.BackoffFactor == 0 {
		config.BackoffFactor = 2.0
	}
	if config.InitialBackoff == 0 {
		config.InitialBackoff = time.Second
	}
	if config.MaxBackoff == 0 {
		config.MaxBackoff = 30 * time.Second
	}
	if config.MinRemaining == 0 {
		config.MinRemaining = 20 * time.Second
	}
	return &RetryModel{model: model, config: config}
}

var _ core.Model = (*RetryModel)(nil)

func (r *RetryModel) ModelName() string {
	return r.model.ModelName()
}

// NewSession returns a retry-wrapped model with an isolated inner model
// session when supported by the wrapped model.
func (r *RetryModel) NewSession() core.Model {
	inner := r.model
	if cloner, ok := r.model.(sessionModelCloner); ok {
		inner = cloner.NewSession()
	}
	return NewRetryModel(inner, r.config)
}

func (r *RetryModel) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	return retryLoop(ctx, r.config, func() (*core.ModelResponse, error) {
		return r.model.Request(ctx, messages, settings, params)
	})
}

func (r *RetryModel) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	return retryLoop(ctx, r.config, func() (core.StreamedResponse, error) {
		return r.model.RequestStream(ctx, messages, settings, params)
	})
}

// Close forwards cleanup to the wrapped model when it supports explicit close.
func (r *RetryModel) Close() error {
	if closer, ok := r.model.(modelCloser); ok {
		return closer.Close()
	}
	return nil
}

func retryLoop[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	backoff := cfg.InitialBackoff

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		result, err := runWithHeartbeat(ctx, cfg.HeartbeatInterval, fn)
		if err == nil {
			return result, nil
		}
		lastErr = err

		if !cfg.IsRetryable(err) {
			return zero, err
		}

		// If the run is near its deadline, don't start another retry cycle.
		if deadline, ok := ctx.Deadline(); ok && cfg.MinRemaining > 0 {
			remaining := time.Until(deadline)
			if remaining <= cfg.MinRemaining {
				fmt.Fprintf(os.Stderr, "[gollem] retry: skipping further retries (only %s remaining)\n", remaining.Round(time.Second))
				return zero, err
			}
		}

		if attempt < cfg.MaxRetries {
			fmt.Fprintf(os.Stderr, "[gollem] retry: attempt %d/%d after error: %v\n", attempt+1, cfg.MaxRetries, err)
		}

		// Don't sleep after the last attempt.
		if attempt < cfg.MaxRetries {
			wait := backoff

			// Use Retry-After from the provider if available (e.g., 429 rate limits).
			// This is more accurate than exponential backoff for rate-limited APIs.
			var httpErr *core.ModelHTTPError
			if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
				wait = httpErr.RetryAfter
			}

			if cfg.Jitter {
				// Add 0-25% jitter.
				jitter := time.Duration(float64(wait) * 0.25 * rand.Float64())
				wait += jitter
			}

			// Avoid spending the remaining run budget on backoff sleeps that
			// leave no time for the next request to complete.
			if deadline, ok := ctx.Deadline(); ok && cfg.MinRemaining > 0 {
				remaining := time.Until(deadline)
				if remaining <= wait+cfg.MinRemaining {
					fmt.Fprintf(os.Stderr, "[gollem] retry: not enough time for another retry (remaining: %s, wait: %s)\n",
						remaining.Round(time.Second), wait.Round(time.Second))
					return zero, err
				}
			}

			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(wait):
			}

			backoff = time.Duration(float64(backoff) * cfg.BackoffFactor)
			if backoff > cfg.MaxBackoff {
				backoff = cfg.MaxBackoff
			}
		}
	}

	if lastErr != nil {
		fmt.Fprintf(os.Stderr, "[gollem] retry: all %d attempts exhausted, giving up: %v\n", cfg.MaxRetries+1, lastErr)
	}
	return zero, lastErr
}

func runWithHeartbeat[T any](ctx context.Context, interval time.Duration, fn func() (T, error)) (T, error) {
	if interval <= 0 {
		return fn()
	}

	var (
		zero   T
		result T
		err    error
	)
	done := make(chan struct{})
	start := time.Now()

	go func() {
		defer close(done)
		result, err = fn()
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return result, err
		case <-ticker.C:
			elapsed := time.Since(start).Round(time.Second)
			remaining := "unknown"
			if deadline, ok := ctx.Deadline(); ok {
				rem := time.Until(deadline)
				if rem < 0 {
					rem = 0
				}
				remaining = rem.Round(time.Second).String()
			}
			fmt.Fprintf(os.Stderr, "[gollem] model: still waiting for provider response (%s elapsed, %s remaining)\n", elapsed, remaining)
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
}

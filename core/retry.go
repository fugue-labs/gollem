package core

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"time"
)

// RetryConfig configures retry behavior.
type RetryConfig struct {
	MaxRetries     int           // maximum number of retries (default: 3)
	InitialBackoff time.Duration // initial wait time (default: 1s)
	MaxBackoff     time.Duration // maximum wait time (default: 30s)
	BackoffFactor  float64       // multiplier per retry (default: 2.0)
	Jitter         bool          // add random jitter (default: true)
	IsRetryable    func(error) bool // determines if an error should be retried
}

// DefaultRetryConfig returns sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		Jitter:         true,
		IsRetryable:    defaultIsRetryable,
	}
}

// defaultIsRetryable checks for transient HTTP errors.
func defaultIsRetryable(err error) bool {
	var httpErr *ModelHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
			return true
		}
	}
	return false
}

// RetryModel wraps a Model with exponential backoff retry for transient failures.
type RetryModel struct {
	model  Model
	config RetryConfig
}

// NewRetryModel creates a Model that retries transient failures.
func NewRetryModel(model Model, config RetryConfig) *RetryModel {
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
	return &RetryModel{model: model, config: config}
}

var _ Model = (*RetryModel)(nil)

func (r *RetryModel) ModelName() string {
	return r.model.ModelName()
}

func (r *RetryModel) Request(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters) (*ModelResponse, error) {
	return retryLoop(ctx, r.config, func() (*ModelResponse, error) {
		return r.model.Request(ctx, messages, settings, params)
	})
}

func (r *RetryModel) RequestStream(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters) (StreamedResponse, error) {
	return retryLoop(ctx, r.config, func() (StreamedResponse, error) {
		return r.model.RequestStream(ctx, messages, settings, params)
	})
}

func retryLoop[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	backoff := cfg.InitialBackoff

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err

		if !cfg.IsRetryable(err) {
			return zero, err
		}

		// Don't sleep after the last attempt.
		if attempt < cfg.MaxRetries {
			wait := backoff
			if cfg.Jitter {
				// Add 0-25% jitter.
				jitter := time.Duration(float64(wait) * 0.25 * rand.Float64())
				wait += jitter
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

	return zero, lastErr
}

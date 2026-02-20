package middleware

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestRetryMiddleware_SucceedsFirst(t *testing.T) {
	// Override sleep to be instant for tests.
	origSleep := sleepFunc
	sleepFunc = func(ctx context.Context, d time.Duration) error {
		return ctx.Err()
	}
	defer func() { sleepFunc = origSleep }()

	var callCount atomic.Int32
	model := &mockModel{response: &core.ModelResponse{
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "ok"}},
		ModelName: "test-model",
	}}

	retry := RetryMiddleware(3, WithInitialDelay(time.Millisecond))

	// Wrap the request manually to track call count.
	handler := retry.WrapRequest(func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		callCount.Add(1)
		return model.Request(ctx, messages, settings, params)
	})

	resp, err := handler(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if callCount.Load() != 1 {
		t.Errorf("expected 1 call, got %d", callCount.Load())
	}
}

func TestRetryMiddleware_RetriesOnFailure(t *testing.T) {
	// Override sleep to be instant for tests.
	origSleep := sleepFunc
	sleepFunc = func(ctx context.Context, d time.Duration) error {
		return ctx.Err()
	}
	defer func() { sleepFunc = origSleep }()

	var callCount atomic.Int32
	transientErr := errors.New("temporary failure")
	successResp := &core.ModelResponse{
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "ok"}},
		ModelName: "test-model",
	}

	retry := RetryMiddleware(3, WithInitialDelay(time.Millisecond))

	handler := retry.WrapRequest(func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		count := callCount.Add(1)
		// Fail first 2 times, succeed on 3rd.
		if count <= 2 {
			return nil, transientErr
		}
		return successResp, nil
	})

	resp, err := handler(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if callCount.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", callCount.Load())
	}
}

func TestRetryMiddleware_MaxRetriesExceeded(t *testing.T) {
	// Override sleep to be instant for tests.
	origSleep := sleepFunc
	sleepFunc = func(ctx context.Context, d time.Duration) error {
		return ctx.Err()
	}
	defer func() { sleepFunc = origSleep }()

	var callCount atomic.Int32
	persistentErr := errors.New("persistent failure")

	retry := RetryMiddleware(2, WithInitialDelay(time.Millisecond))

	handler := retry.WrapRequest(func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		callCount.Add(1)
		return nil, persistentErr
	})

	_, err := handler(context.Background(), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if !errors.Is(err, persistentErr) {
		t.Errorf("expected persistent failure error, got %v", err)
	}
	// 1 initial + 2 retries = 3 total calls.
	if callCount.Load() != 3 {
		t.Errorf("expected 3 calls (1 initial + 2 retries), got %d", callCount.Load())
	}
}

func TestRetryMiddleware_RetryIf(t *testing.T) {
	// Override sleep to be instant for tests.
	origSleep := sleepFunc
	sleepFunc = func(ctx context.Context, d time.Duration) error {
		return ctx.Err()
	}
	defer func() { sleepFunc = origSleep }()

	var callCount atomic.Int32
	retryableErr := errors.New("retryable")
	nonRetryableErr := errors.New("non-retryable")

	retry := RetryMiddleware(3,
		WithInitialDelay(time.Millisecond),
		WithRetryIf(func(err error) bool {
			return errors.Is(err, retryableErr)
		}),
	)

	// Test: non-retryable error should not be retried.
	callCount.Store(0)
	handler := retry.WrapRequest(func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		callCount.Add(1)
		return nil, nonRetryableErr
	})

	_, err := handler(context.Background(), nil, nil, nil)
	if !errors.Is(err, nonRetryableErr) {
		t.Errorf("expected non-retryable error, got %v", err)
	}
	if callCount.Load() != 1 {
		t.Errorf("expected 1 call (no retry for non-retryable), got %d", callCount.Load())
	}

	// Test: retryable error should be retried.
	callCount.Store(0)
	successResp := &core.ModelResponse{
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "ok"}},
		ModelName: "test-model",
	}
	handler = retry.WrapRequest(func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		count := callCount.Add(1)
		if count <= 2 {
			return nil, retryableErr
		}
		return successResp, nil
	})

	resp, err := handler(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if callCount.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", callCount.Load())
	}
}

func TestRetryMiddleware_ContextCancellation(t *testing.T) {
	// Use real sleep for this test, but with very short delays.
	origSleep := sleepFunc
	sleepFunc = func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}
	defer func() { sleepFunc = origSleep }()

	var callCount atomic.Int32
	persistentErr := errors.New("persistent failure")

	retry := RetryMiddleware(10,
		WithInitialDelay(500*time.Millisecond),
		WithMaxDelay(2*time.Second),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	handler := retry.WrapRequest(func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		callCount.Add(1)
		return nil, persistentErr
	})

	_, err := handler(ctx, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
	// Should have made at least 1 call but not all 11.
	count := callCount.Load()
	if count < 1 || count > 3 {
		t.Errorf("expected 1-3 calls before context cancellation, got %d", count)
	}
}

func TestRetryMiddleware_ExponentialBackoff(t *testing.T) {
	var delays []time.Duration
	origSleep := sleepFunc
	sleepFunc = func(ctx context.Context, d time.Duration) error {
		delays = append(delays, d)
		return ctx.Err()
	}
	defer func() { sleepFunc = origSleep }()

	persistentErr := errors.New("failure")

	retry := RetryMiddleware(4,
		WithInitialDelay(100*time.Millisecond),
		WithMaxDelay(500*time.Millisecond),
	)

	handler := retry.WrapRequest(func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return nil, persistentErr
	})

	handler(context.Background(), nil, nil, nil)

	// Expect 4 delays (between 5 attempts: initial + 4 retries).
	if len(delays) != 4 {
		t.Fatalf("expected 4 delays, got %d: %v", len(delays), delays)
	}

	expected := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		500 * time.Millisecond, // capped at maxDelay
	}
	for i, exp := range expected {
		if delays[i] != exp {
			t.Errorf("delay[%d]: expected %v, got %v", i, exp, delays[i])
		}
	}
}

func TestRetryMiddleware_StreamRequest(t *testing.T) {
	// Override sleep to be instant for tests.
	origSleep := sleepFunc
	sleepFunc = func(ctx context.Context, d time.Duration) error {
		return ctx.Err()
	}
	defer func() { sleepFunc = origSleep }()

	var callCount atomic.Int32
	transientErr := errors.New("stream error")

	retry := RetryMiddleware(2, WithInitialDelay(time.Millisecond))

	handler := retry.WrapStreamRequest(func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
		count := callCount.Add(1)
		if count <= 1 {
			return nil, transientErr
		}
		return nil, nil // success
	})

	_, err := handler(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount.Load() != 2 {
		t.Errorf("expected 2 calls, got %d", callCount.Load())
	}
}

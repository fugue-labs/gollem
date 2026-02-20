package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// retryFailingModel fails a specified number of times before succeeding.
type retryFailingModel struct {
	attempts   atomic.Int32
	failCount  int
	failErr    error
	successMsg string
}

func (m *retryFailingModel) ModelName() string { return "failing-model" }

func (m *retryFailingModel) Request(_ context.Context, _ []ModelMessage, _ *ModelSettings, _ *ModelRequestParameters) (*ModelResponse, error) {
	n := int(m.attempts.Add(1))
	if n <= m.failCount {
		return nil, m.failErr
	}
	return TextResponse(m.successMsg), nil
}

func (m *retryFailingModel) RequestStream(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters) (StreamedResponse, error) {
	resp, err := m.Request(ctx, messages, settings, params)
	if err != nil {
		return nil, err
	}
	return &testStreamedResponse{response: resp}, nil
}

func TestRetryModel_SucceedsAfterRetry(t *testing.T) {
	fm := &retryFailingModel{
		failCount:  2,
		failErr:    &ModelHTTPError{StatusCode: 429, Message: "rate limited"},
		successMsg: "success",
	}

	rm := NewRetryModel(fm, RetryConfig{
		MaxRetries:     3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		BackoffFactor:  2.0,
		Jitter:         false,
	})

	resp, err := rm.Request(context.Background(), nil, nil, &ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if resp.TextContent() != "success" {
		t.Errorf("expected 'success', got %q", resp.TextContent())
	}
	if int(fm.attempts.Load()) != 3 {
		t.Errorf("expected 3 attempts, got %d", fm.attempts.Load())
	}
}

func TestRetryModel_ExhaustsRetries(t *testing.T) {
	fm := &retryFailingModel{
		failCount: 100,
		failErr:   &ModelHTTPError{StatusCode: 500, Message: "server error"},
	}

	rm := NewRetryModel(fm, RetryConfig{
		MaxRetries:     2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		BackoffFactor:  2.0,
	})

	_, err := rm.Request(context.Background(), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if int(fm.attempts.Load()) != 3 { // 1 initial + 2 retries
		t.Errorf("expected 3 attempts, got %d", fm.attempts.Load())
	}
}

func TestRetryModel_NonRetryableError(t *testing.T) {
	fm := &retryFailingModel{
		failCount: 100,
		failErr:   errors.New("non-retryable error"),
	}

	rm := NewRetryModel(fm, RetryConfig{
		MaxRetries:     3,
		InitialBackoff: time.Millisecond,
	})

	_, err := rm.Request(context.Background(), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// Should fail immediately without retrying.
	if int(fm.attempts.Load()) != 1 {
		t.Errorf("expected 1 attempt (no retry), got %d", fm.attempts.Load())
	}
}

func TestRetryModel_BackoffIncreases(t *testing.T) {
	var timestamps []time.Time
	fm := &retryFailingModel{
		failCount: 3,
		failErr:   &ModelHTTPError{StatusCode: 503, Message: "unavailable"},
	}

	rm := NewRetryModel(fm, RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 20 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
		BackoffFactor:  2.0,
		Jitter:         false,
	})

	start := time.Now()
	_, _ = rm.Request(context.Background(), nil, nil, nil)
	totalDuration := time.Since(start)
	_ = timestamps

	// With 3 retries at 20ms, 40ms, 80ms = 140ms minimum.
	if totalDuration < 100*time.Millisecond {
		t.Errorf("expected backoff delays, but total duration was %v", totalDuration)
	}
}

func TestRetryModel_ContextCancel(t *testing.T) {
	fm := &retryFailingModel{
		failCount: 100,
		failErr:   &ModelHTTPError{StatusCode: 429, Message: "rate limited"},
	}

	rm := NewRetryModel(fm, RetryConfig{
		MaxRetries:     10,
		InitialBackoff: time.Second, // long backoff
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := rm.Request(ctx, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestRetryModel_ModelName(t *testing.T) {
	fm := &retryFailingModel{successMsg: "ok"}
	rm := NewRetryModel(fm, DefaultRetryConfig())
	if rm.ModelName() != "failing-model" {
		t.Errorf("expected 'failing-model', got %q", rm.ModelName())
	}
}

func TestRetryModel_StreamRetry(t *testing.T) {
	fm := &retryFailingModel{
		failCount:  1,
		failErr:    &ModelHTTPError{StatusCode: 502, Message: "bad gateway"},
		successMsg: "streamed",
	}

	rm := NewRetryModel(fm, RetryConfig{
		MaxRetries:     2,
		InitialBackoff: time.Millisecond,
		Jitter:         false,
	})

	stream, err := rm.RequestStream(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	resp := stream.Response()
	if resp.TextContent() != "streamed" {
		t.Errorf("expected 'streamed', got %q", resp.TextContent())
	}
}

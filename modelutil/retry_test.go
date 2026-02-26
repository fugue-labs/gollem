package modelutil

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// retryFailingModel fails a specified number of times before succeeding.
type retryFailingModel struct {
	attempts   atomic.Int32
	failCount  int
	failErr    error
	successMsg string
	closeCalls atomic.Int32
}

func (m *retryFailingModel) ModelName() string { return "failing-model" }

func (m *retryFailingModel) Request(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
	n := int(m.attempts.Add(1))
	if n <= m.failCount {
		return nil, m.failErr
	}
	return core.TextResponse(m.successMsg), nil
}

func (m *retryFailingModel) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	resp, err := m.Request(ctx, messages, settings, params)
	if err != nil {
		return nil, err
	}
	return &simpleStreamedResponse{response: resp}, nil
}

func (m *retryFailingModel) NewSession() core.Model {
	return &retryFailingModel{
		failCount:  m.failCount,
		failErr:    m.failErr,
		successMsg: m.successMsg,
	}
}

func (m *retryFailingModel) Close() error {
	m.closeCalls.Add(1)
	return nil
}

// simpleStreamedResponse wraps a ModelResponse as a StreamedResponse for tests.
type simpleStreamedResponse struct {
	response *core.ModelResponse
}

func (s *simpleStreamedResponse) Next() (core.ModelResponseStreamEvent, error) {
	return nil, io.EOF
}

func (s *simpleStreamedResponse) Response() *core.ModelResponse {
	return s.response
}

func (s *simpleStreamedResponse) Usage() core.Usage {
	return s.response.Usage
}

func (s *simpleStreamedResponse) Close() error {
	return nil
}

func TestRetryModel_SucceedsAfterRetry(t *testing.T) {
	fm := &retryFailingModel{
		failCount:  2,
		failErr:    &core.ModelHTTPError{StatusCode: 429, Message: "rate limited"},
		successMsg: "success",
	}

	rm := NewRetryModel(fm, RetryConfig{
		MaxRetries:     3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		BackoffFactor:  2.0,
		Jitter:         false,
	})

	resp, err := rm.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
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
		failErr:   &core.ModelHTTPError{StatusCode: 500, Message: "server error"},
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
		failErr:   &core.ModelHTTPError{StatusCode: 503, Message: "unavailable"},
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
		failErr:   &core.ModelHTTPError{StatusCode: 429, Message: "rate limited"},
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

func TestRetryModel_NewSession(t *testing.T) {
	fm := &retryFailingModel{successMsg: "ok"}
	rm := NewRetryModel(fm, RetryConfig{
		MaxRetries:     2,
		InitialBackoff: time.Millisecond,
	})

	session := rm.NewSession()
	sessionRM, ok := session.(*RetryModel)
	if !ok {
		t.Fatalf("expected *RetryModel from NewSession, got %T", session)
	}
	if sessionRM == rm {
		t.Fatal("expected distinct retry model instance")
	}
	if sessionRM.model == rm.model {
		t.Fatal("expected inner model clone in NewSession")
	}
	if sessionRM.config.MaxRetries != rm.config.MaxRetries ||
		sessionRM.config.InitialBackoff != rm.config.InitialBackoff {
		t.Fatal("expected retry config to be preserved")
	}
}

func TestRetryModel_CloseForwards(t *testing.T) {
	fm := &retryFailingModel{successMsg: "ok"}
	rm := NewRetryModel(fm, DefaultRetryConfig())
	if err := rm.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if got := fm.closeCalls.Load(); got != 1 {
		t.Fatalf("expected underlying close to be called once, got %d", got)
	}
}

func TestRetryModel_Permanent429CreditsExhausted(t *testing.T) {
	fm := &retryFailingModel{
		failCount: 100,
		failErr: &core.ModelHTTPError{
			StatusCode: 429,
			Message:    "rate limited",
			Body:       `{"code":"Some resource has been exhausted","error":"Your team has either used all available credits or reached its monthly spending limit."}`,
		},
	}

	rm := NewRetryModel(fm, RetryConfig{
		MaxRetries:     5,
		InitialBackoff: time.Millisecond,
		Jitter:         false,
	})

	_, err := rm.Request(context.Background(), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// Should fail immediately without retrying — credits exhausted is permanent.
	if int(fm.attempts.Load()) != 1 {
		t.Errorf("expected 1 attempt (no retry for credits exhausted), got %d", fm.attempts.Load())
	}
}

func TestRetryModel_Transient429StillRetries(t *testing.T) {
	fm := &retryFailingModel{
		failCount:  1,
		failErr:    &core.ModelHTTPError{StatusCode: 429, Message: "rate limited", Body: `{"error":"too many requests"}`},
		successMsg: "success",
	}

	rm := NewRetryModel(fm, RetryConfig{
		MaxRetries:     3,
		InitialBackoff: time.Millisecond,
		Jitter:         false,
	})

	resp, err := rm.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if resp.TextContent() != "success" {
		t.Errorf("expected 'success', got %q", resp.TextContent())
	}
	if int(fm.attempts.Load()) != 2 {
		t.Errorf("expected 2 attempts, got %d", fm.attempts.Load())
	}
}

func TestIsPermanent429(t *testing.T) {
	tests := []struct {
		body     string
		expected bool
	}{
		{`{"error":"too many requests"}`, false},
		{`{"error":"rate limited"}`, false},
		{`{"error":"Your team has used all available credits"}`, true},
		{`{"error":"reached its monthly spending limit"}`, true},
		{`{"error":"billing issue"}`, true},
		{`{"error":"quota exceeded for model"}`, true},
		{`{"error":"plan limit reached"}`, true},
		{`{"error":"subscription expired"}`, true},
		{``, false},
	}
	for _, tc := range tests {
		got := isPermanent429(tc.body)
		if got != tc.expected {
			t.Errorf("isPermanent429(%q) = %v, want %v", tc.body, got, tc.expected)
		}
	}
}

func TestRetryModel_GatewayTimeoutRetries(t *testing.T) {
	// Verify that 504 Gateway Timeout, 529 Overloaded, and 408 Request Timeout
	// are all treated as retryable.
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"504 Gateway Timeout", 504},
		{"529 Overloaded", 529},
		{"408 Request Timeout", 408},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fm := &retryFailingModel{
				failCount:  1,
				failErr:    &core.ModelHTTPError{StatusCode: tc.status, Message: "transient error"},
				successMsg: "recovered",
			}
			rm := NewRetryModel(fm, RetryConfig{
				MaxRetries:     2,
				InitialBackoff: time.Millisecond,
				Jitter:         false,
			})
			resp, err := rm.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
			if err != nil {
				t.Fatalf("expected success after retry, got: %v", err)
			}
			if resp.TextContent() != "recovered" {
				t.Errorf("expected 'recovered', got %q", resp.TextContent())
			}
			if int(fm.attempts.Load()) != 2 {
				t.Errorf("expected 2 attempts, got %d", fm.attempts.Load())
			}
		})
	}
}

func TestRetryModel_StreamRetry(t *testing.T) {
	fm := &retryFailingModel{
		failCount:  1,
		failErr:    &core.ModelHTTPError{StatusCode: 502, Message: "bad gateway"},
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

func TestDefaultRetryConfig_HeartbeatInterval(t *testing.T) {
	cfg := DefaultRetryConfig()
	if cfg.HeartbeatInterval != 0 {
		t.Fatalf("heartbeat interval = %v, want 0 (disabled by default)", cfg.HeartbeatInterval)
	}
}

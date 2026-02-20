package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestRateLimitMiddleware_AllowsBurst(t *testing.T) {
	model := &mockModel{response: &core.ModelResponse{
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "ok"}},
		ModelName: "test-model",
	}}

	// Allow burst of 5 at 10 rps.
	rl := RateLimitMiddleware(10, 5)
	wrapped := Wrap(model, rl)

	// All 5 burst requests should complete nearly instantly.
	start := time.Now()
	for range 5 {
		_, err := wrapped.Request(context.Background(), nil, nil, nil)
		if err != nil {
			t.Fatalf("burst request failed: %v", err)
		}
	}
	elapsed := time.Since(start)

	// 5 burst requests should take well under 200ms.
	if elapsed > 200*time.Millisecond {
		t.Errorf("burst requests took too long: %v (expected < 200ms)", elapsed)
	}
}

func TestRateLimitMiddleware_ThrottlesExcess(t *testing.T) {
	model := &mockModel{response: &core.ModelResponse{
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "ok"}},
		ModelName: "test-model",
	}}

	// 10 requests per second with burst of 1 — second request must wait ~100ms.
	rl := RateLimitMiddleware(10, 1)
	wrapped := Wrap(model, rl)

	// First request should go through immediately (burst).
	start := time.Now()
	_, err := wrapped.Request(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	firstElapsed := time.Since(start)
	if firstElapsed > 50*time.Millisecond {
		t.Errorf("first request took too long: %v", firstElapsed)
	}

	// Second request should be throttled (~100ms at 10 rps).
	start = time.Now()
	_, err = wrapped.Request(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	secondElapsed := time.Since(start)

	// Should take at least 50ms (some tolerance).
	if secondElapsed < 50*time.Millisecond {
		t.Errorf("second request was not throttled: %v (expected >= 50ms)", secondElapsed)
	}
}

func TestRateLimitMiddleware_ContextCancellation(t *testing.T) {
	model := &mockModel{response: &core.ModelResponse{
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "ok"}},
		ModelName: "test-model",
	}}

	// Very low rate with burst of 1 — exhaust the burst then cancel.
	rl := RateLimitMiddleware(0.5, 1) // 1 request per 2 seconds
	wrapped := Wrap(model, rl)

	// Exhaust the burst.
	_, err := wrapped.Request(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("burst request failed: %v", err)
	}

	// Now create a context that cancels quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = wrapped.Request(ctx, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestRateLimitMiddleware_StreamRequest(t *testing.T) {
	// Verify the stream variant works through the rate limiter.
	rl := RateLimitMiddleware(10, 5)

	// Test via the stream middleware path directly.
	handler := rl.WrapStreamRequest(func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
		return nil, nil
	})

	_, err := handler(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("stream request failed: %v", err)
	}
}

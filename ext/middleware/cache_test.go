package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestCacheMiddleware_CacheHit(t *testing.T) {
	callCount := 0
	handler := RequestFunc(func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		callCount++
		return &core.ModelResponse{
			Parts:     []core.ModelResponsePart{core.TextPart{Content: "hello"}},
			ModelName: "test-model",
		}, nil
	})

	mw := CacheMiddleware(5 * time.Minute)
	wrapped := mw.WrapRequest(handler)

	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "say hello"},
		}},
	}

	// First call — cache miss.
	resp1, err := wrapped(context.Background(), messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	// Second call with identical request — cache hit.
	resp2, err := wrapped(context.Background(), messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected still 1 call (cached), got %d", callCount)
	}

	// Should return the same response.
	if resp1.TextContent() != resp2.TextContent() {
		t.Errorf("expected same content, got %q vs %q", resp1.TextContent(), resp2.TextContent())
	}
}

func TestCacheMiddleware_CacheMiss(t *testing.T) {
	callCount := 0
	handler := RequestFunc(func(_ context.Context, messages []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		callCount++
		// Return different responses for different inputs.
		return &core.ModelResponse{
			Parts:     []core.ModelResponsePart{core.TextPart{Content: "response"}},
			ModelName: "test-model",
		}, nil
	})

	mw := CacheMiddleware(5 * time.Minute)
	wrapped := mw.WrapRequest(handler)

	messages1 := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "first request"},
		}},
	}
	messages2 := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "second request"},
		}},
	}

	// First request.
	_, err := wrapped(context.Background(), messages1, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	// Different request — should not hit cache.
	_, err = wrapped(context.Background(), messages2, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls (different requests), got %d", callCount)
	}
}

func TestCacheMiddleware_Expiration(t *testing.T) {
	callCount := 0
	handler := RequestFunc(func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		callCount++
		return &core.ModelResponse{
			Parts:     []core.ModelResponsePart{core.TextPart{Content: "hello"}},
			ModelName: "test-model",
		}, nil
	})

	// Use a very short TTL.
	mw := CacheMiddleware(50 * time.Millisecond)
	wrapped := mw.WrapRequest(handler)

	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "say hello"},
		}},
	}

	// First call — cache miss.
	_, err := wrapped(context.Background(), messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	// Wait for entry to expire.
	time.Sleep(100 * time.Millisecond)

	// Same request after expiry — should miss cache.
	_, err = wrapped(context.Background(), messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls after expiration, got %d", callCount)
	}
}

func TestCacheMiddleware_Stats(t *testing.T) {
	handler := RequestFunc(func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{
			Parts:     []core.ModelResponsePart{core.TextPart{Content: "ok"}},
			ModelName: "test-model",
		}, nil
	})

	mw, stats := CacheMiddlewareWithStats(5 * time.Minute)
	wrapped := mw.WrapRequest(handler)

	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "hello"},
		}},
	}

	// Initial state.
	if stats.Hits != 0 || stats.Misses != 0 {
		t.Fatal("expected zero hits and misses initially")
	}
	if stats.HitRate() != 0.0 {
		t.Fatalf("expected 0.0 hit rate, got %f", stats.HitRate())
	}

	// First call — miss.
	wrapped(context.Background(), messages, nil, nil)
	if stats.Hits != 0 || stats.Misses != 1 {
		t.Fatalf("expected 0 hits, 1 miss; got %d hits, %d misses", stats.Hits, stats.Misses)
	}

	// Second call — hit.
	wrapped(context.Background(), messages, nil, nil)
	if stats.Hits != 1 || stats.Misses != 1 {
		t.Fatalf("expected 1 hit, 1 miss; got %d hits, %d misses", stats.Hits, stats.Misses)
	}

	// Third call — hit.
	wrapped(context.Background(), messages, nil, nil)
	if stats.Hits != 2 || stats.Misses != 1 {
		t.Fatalf("expected 2 hits, 1 miss; got %d hits, %d misses", stats.Hits, stats.Misses)
	}

	// HitRate: 2 hits / 3 total = 0.666...
	rate := stats.HitRate()
	if rate < 0.66 || rate > 0.67 {
		t.Fatalf("expected ~0.667 hit rate, got %f", rate)
	}
}

func TestCacheMiddleware_StreamPassthrough(t *testing.T) {
	streamCallCount := 0
	streamHandler := StreamRequestFunc(func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
		streamCallCount++
		return nil, errors.New("not implemented")
	})

	mw := CacheMiddleware(5 * time.Minute)

	// The cache middleware must implement StreamMiddleware for WrapStreamRequest
	// to be applied. Verify that it passes through without caching.
	sm, ok := mw.(StreamMiddleware)
	if !ok {
		t.Fatal("CacheMiddleware must implement StreamMiddleware")
	}

	wrappedStream := sm.WrapStreamRequest(streamHandler)

	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "stream this"},
		}},
	}

	// Call twice — both should invoke the handler (no caching).
	wrappedStream(context.Background(), messages, nil, nil)
	wrappedStream(context.Background(), messages, nil, nil)

	if streamCallCount != 2 {
		t.Fatalf("expected 2 stream calls (no caching), got %d", streamCallCount)
	}
}

func TestCacheMiddleware_ErrorNotCached(t *testing.T) {
	callCount := 0
	handler := RequestFunc(func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		callCount++
		if callCount == 1 {
			return nil, errors.New("transient error")
		}
		return &core.ModelResponse{
			Parts:     []core.ModelResponsePart{core.TextPart{Content: "ok"}},
			ModelName: "test-model",
		}, nil
	})

	mw := CacheMiddleware(5 * time.Minute)
	wrapped := mw.WrapRequest(handler)

	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "hello"},
		}},
	}

	// First call fails.
	_, err := wrapped(context.Background(), messages, nil, nil)
	if err == nil {
		t.Fatal("expected error on first call")
	}

	// Second call should retry (error was not cached).
	resp, err := wrapped(context.Background(), messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if resp.TextContent() != "ok" {
		t.Errorf("expected 'ok', got %q", resp.TextContent())
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls (error not cached), got %d", callCount)
	}
}

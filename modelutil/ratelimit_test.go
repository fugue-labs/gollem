package modelutil

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestRateLimitedModel_Throttles(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("a"), core.TextResponse("b"), core.TextResponse("c"))
	// 2 requests per second, burst of 1. After the first request consumes the
	// burst token, the second must wait ~500ms.
	rl := NewRateLimitedModel(model, 2, 1)

	start := time.Now()
	_, err := rl.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rl.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	// The second request should have waited ~500ms (1/rate).
	if elapsed < 400*time.Millisecond {
		t.Errorf("expected at least 400ms of throttling, got %v", elapsed)
	}
}

func TestRateLimitedModel_Burst(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("a"), core.TextResponse("b"), core.TextResponse("c"))
	// Allow burst of 3 so 3 requests fire immediately.
	rl := NewRateLimitedModel(model, 1, 3)

	start := time.Now()
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = rl.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// All 3 should complete within burst, no significant delay.
	if elapsed > 200*time.Millisecond {
		t.Errorf("expected burst to allow immediate requests, got %v", elapsed)
	}
}

func TestRateLimitedModel_ContextCancel(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("a"), core.TextResponse("b"))
	// 1 rps, burst of 1. First consumes the token, second will wait.
	rl := NewRateLimitedModel(model, 1, 1)

	_, _ = rl.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := rl.Request(ctx, nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}

func TestRateLimitedModel_Delegates(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("hello"))
	rl := NewRateLimitedModel(model, 100, 10)

	resp, err := rl.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TextContent() != "hello" {
		t.Errorf("expected 'hello', got %q", resp.TextContent())
	}
	if rl.ModelName() != "test-model" {
		t.Errorf("expected 'test-model', got %q", rl.ModelName())
	}
}

func TestRateLimitedModel_Streaming(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("stream"))
	rl := NewRateLimitedModel(model, 100, 10)

	stream, err := rl.RequestStream(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	resp := stream.Response()
	if resp.TextContent() != "stream" {
		t.Errorf("expected 'stream', got %q", resp.TextContent())
	}
}

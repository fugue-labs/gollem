package modelutil

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestCachedModel_Hit(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("first"))
	cache := NewMemoryCache()
	cached := NewCachedModel(model, cache)

	// First call: cache miss.
	resp1, err := cached.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	if resp1.TextContent() != "first" {
		t.Fatalf("expected 'first', got %q", resp1.TextContent())
	}

	// Second call with same args: cache hit -- model should NOT be called again.
	resp2, err := cached.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	if resp2.TextContent() != "first" {
		t.Fatalf("expected cached 'first', got %q", resp2.TextContent())
	}

	// Model should have only been called once.
	if len(model.Calls()) != 1 {
		t.Errorf("expected 1 model call (cached on second), got %d", len(model.Calls()))
	}
}

func TestCachedModel_Miss(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("a"), core.TextResponse("b"))
	cache := NewMemoryCache()
	cached := NewCachedModel(model, cache)

	// Different messages produce different cache keys.
	msg1 := []core.ModelMessage{core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hello"}}}}
	msg2 := []core.ModelMessage{core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "world"}}}}

	resp1, err := cached.Request(context.Background(), msg1, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	resp2, err := cached.Request(context.Background(), msg2, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}

	if resp1.TextContent() != "a" || resp2.TextContent() != "b" {
		t.Errorf("expected different responses for different inputs, got %q and %q", resp1.TextContent(), resp2.TextContent())
	}
	if len(model.Calls()) != 2 {
		t.Errorf("expected 2 model calls, got %d", len(model.Calls()))
	}
}

func TestCachedModel_TTL(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("fresh"), core.TextResponse("fresh2"))
	cache := NewMemoryCacheWithTTL(50 * time.Millisecond)
	cached := NewCachedModel(model, cache)

	_, err := cached.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for TTL to expire.
	time.Sleep(100 * time.Millisecond)

	// This should be a cache miss due to TTL expiration.
	_, err = cached.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(model.Calls()) != 2 {
		t.Errorf("expected 2 model calls (TTL expired), got %d", len(model.Calls()))
	}
}

func TestCachedModel_StreamingNotCached(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("stream1"), core.TextResponse("stream2"))
	cache := NewMemoryCache()
	cached := NewCachedModel(model, cache)

	_, err := cached.RequestStream(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cached.RequestStream(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}

	// Both streaming calls should hit the model.
	if len(model.Calls()) != 2 {
		t.Errorf("expected 2 model calls for streaming, got %d", len(model.Calls()))
	}
}

func TestCachedModel_ThreadSafe(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("safe"))
	cache := NewMemoryCache()
	cached := NewCachedModel(model, cache)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cached.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestCachedModel_ModelName(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("name"))
	cache := NewMemoryCache()
	cached := NewCachedModel(model, cache)

	if cached.ModelName() != "test-model" {
		t.Errorf("expected 'test-model', got %q", cached.ModelName())
	}
}

// Regression: MemoryCache.Get() had a TOCTOU race — it released the read lock
// before re-acquiring a write lock for TTL expiry, allowing concurrent readers
// to see stale data. Now uses a single write lock for the entire operation.
func TestMemoryCache_TTLConcurrentAccess(t *testing.T) {
	cache := NewMemoryCacheWithTTL(10 * time.Millisecond)
	resp := &core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "cached"}}}
	cache.Set("key", resp)

	// Wait for entry to expire.
	time.Sleep(20 * time.Millisecond)

	// Concurrent reads should all see expired (no stale data).
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok := cache.Get("key")
			if ok {
				t.Error("expected cache miss for expired entry")
			}
		}()
	}
	wg.Wait()
}

func TestCachedModel_DifferentSettings(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("low"), core.TextResponse("high"))
	cache := NewMemoryCache()
	cached := NewCachedModel(model, cache)

	temp1 := 0.1
	temp2 := 0.9

	_, err := cached.Request(context.Background(), nil, &core.ModelSettings{Temperature: &temp1}, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cached.Request(context.Background(), nil, &core.ModelSettings{Temperature: &temp2}, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}

	// Different settings = different cache keys = 2 model calls.
	if len(model.Calls()) != 2 {
		t.Errorf("expected 2 model calls for different settings, got %d", len(model.Calls()))
	}
}

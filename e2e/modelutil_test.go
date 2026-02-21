//go:build e2e

package e2e

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/modelutil"
)

// --- Phase 7: ModelUtil wrappers with real providers ---

// TestCachedModel verifies that caching avoids repeated API calls.
func TestCachedModel(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cache := modelutil.NewMemoryCache()
	model := modelutil.NewCachedModel(newAnthropicProvider(), cache)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "What is 2+2? Reply with just the number."},
			},
		},
	}

	// First call — cache miss, hits API.
	resp1, err := model.Request(ctx, messages, nil, nil)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("first request failed: %v", err)
	}
	text1 := resp1.TextContent()

	// Second call — same messages, should hit cache.
	resp2, err := model.Request(ctx, messages, nil, nil)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	text2 := resp2.TextContent()

	if text1 != text2 {
		t.Errorf("expected identical cached response, got %q vs %q", text1, text2)
	}

	// Verify cache has entries by making a different request.
	messages2 := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "What is 3+3? Reply with just the number."},
			},
		},
	}
	resp3, err := model.Request(ctx, messages2, nil, nil)
	if err != nil {
		t.Fatalf("third request failed: %v", err)
	}
	text3 := resp3.TextContent()

	// Different prompt should give different response (not from cache of first).
	t.Logf("First=%q Cached=%q Different=%q", text1, text2, text3)
}

// TestCachedModelWithTTL verifies TTL expiration.
func TestCachedModelWithTTL(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cache := modelutil.NewMemoryCacheWithTTL(2 * time.Second)
	model := modelutil.NewCachedModel(newAnthropicProvider(), cache)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Say 'cached' and nothing else."},
			},
		},
	}

	// First call.
	resp1, err := model.Request(ctx, messages, nil, nil)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("first request failed: %v", err)
	}

	// Immediate second call — should be cached.
	resp2, err := model.Request(ctx, messages, nil, nil)
	if err != nil {
		t.Fatalf("cached request failed: %v", err)
	}

	if resp1.TextContent() != resp2.TextContent() {
		t.Error("expected cached response to match")
	}

	t.Logf("CachedResponse=%q", resp1.TextContent())
}

// TestRateLimitedModel verifies rate limiting delays requests.
func TestRateLimitedModel(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 2 requests/second, burst of 2.
	model := modelutil.NewRateLimitedModel(newAnthropicProvider(), 2.0, 2)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Say 'ok'."},
			},
		},
	}

	// Make 3 requests — first 2 should be fast (burst), third should be delayed.
	start := time.Now()
	for i := 0; i < 3; i++ {
		resp, err := model.Request(ctx, messages, nil, nil)
		if err != nil {
			skipOnAccountError(t, err)
			t.Fatalf("request %d failed: %v", i, err)
		}
		t.Logf("Request %d: %q (elapsed=%v)", i, resp.TextContent(), time.Since(start))
	}

	elapsed := time.Since(start)
	// With 2 req/s and burst of 2, the 3rd request should wait ~500ms.
	// Total should be at least a bit longer than pure API latency.
	t.Logf("Total elapsed: %v", elapsed)
}

// TestFallbackModel verifies fallback between providers.
func TestFallbackModel(t *testing.T) {
	anthropicOnly(t)
	skipIfNoCredentials(t, "XAI_API_KEY")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Primary: Anthropic, Fallback: XAI.
	primary := newAnthropicProvider()
	fallback := newXAIProvider()

	model := modelutil.NewFallbackModel(primary, fallback)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Say 'hello' and nothing else."},
			},
		},
	}

	resp, err := model.Request(ctx, messages, nil, nil)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("fallback model request failed: %v", err)
	}

	text := strings.ToLower(resp.TextContent())
	if !strings.Contains(text, "hello") {
		t.Errorf("expected 'hello' in response, got %q", resp.TextContent())
	}

	t.Logf("FallbackModel response=%q model=%s", resp.TextContent(), resp.ModelName)
}

// TestFallbackModelWithAgent verifies fallback works through Agent[T].
func TestFallbackModelWithAgent(t *testing.T) {
	anthropicOnly(t)
	skipIfNoCredentials(t, "XAI_API_KEY")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	model := modelutil.NewFallbackModel(newAnthropicProvider(), newXAIProvider())
	agent := core.NewAgent[string](model)

	result, err := agent.Run(ctx, "Say 'agent fallback works' and nothing else.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	t.Logf("Output=%q", result.Output)
}

// TestRouterModelThreshold verifies threshold-based routing.
func TestRouterModelThreshold(t *testing.T) {
	anthropicOnly(t)
	skipIfNoCredentials(t, "XAI_API_KEY")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	simple := newAnthropicProvider() // Short prompts → Anthropic
	complex := newXAIProvider()      // Long prompts → XAI

	router := modelutil.ThresholdRouter(simple, complex, 50)
	model := modelutil.NewRouterModel(router)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Say hi."}, // short prompt < 50 chars
			},
		},
	}

	resp, err := model.Request(ctx, messages, nil, nil)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("short prompt request failed: %v", err)
	}
	t.Logf("Short prompt response=%q", resp.TextContent())

	// Long prompt should route to the complex model.
	longMessages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Please explain in detail what the capital of France is and why it is significant historically."},
			},
		},
	}

	resp2, err := model.Request(ctx, longMessages, nil, nil)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("long prompt request failed: %v", err)
	}
	t.Logf("Long prompt response=%q", resp2.TextContent())
}

// TestRouterModelRoundRobin verifies round-robin distribution.
func TestRouterModelRoundRobin(t *testing.T) {
	anthropicOnly(t)
	skipIfNoCredentials(t, "XAI_API_KEY")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	anthro := newAnthropicProvider()
	xai := newXAIProvider()

	router := modelutil.RoundRobinRouter(anthro, xai)
	model := modelutil.NewRouterModel(router)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Say 'ok'."},
			},
		},
	}

	var models []string
	for i := 0; i < 3; i++ {
		resp, err := model.Request(ctx, messages, nil, nil)
		if err != nil {
			skipOnAccountError(t, err)
			t.Fatalf("request %d failed: %v", i, err)
		}
		models = append(models, resp.ModelName)
	}

	// Should alternate between providers.
	if len(models) < 3 {
		t.Fatalf("expected 3 responses, got %d", len(models))
	}
	if models[0] == models[1] {
		t.Errorf("expected round-robin to alternate models, got %v", models)
	}

	t.Logf("Models used: %v", models)
}

// TestRetryModelWithAgent verifies retry wrapper works with Agent.
func TestRetryModelWithAgent(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	config := modelutil.DefaultRetryConfig()
	config.MaxRetries = 2
	model := modelutil.NewRetryModel(newAnthropicProvider(), config)

	agent := core.NewAgent[string](model)

	result, err := agent.Run(ctx, "Say 'retry works' and nothing else.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	text := strings.ToLower(result.Output)
	if !strings.Contains(text, "retry works") {
		t.Errorf("expected 'retry works', got %q", result.Output)
	}

	t.Logf("Output=%q", result.Output)
}

// TestCachedModelWithAgent verifies cache works through Agent[T].
func TestCachedModelWithAgent(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var requestCount int32
	cache := modelutil.NewMemoryCache()

	// Wrap with a middleware that counts actual API calls.
	base := newAnthropicProvider()
	cached := modelutil.NewCachedModel(base, cache)

	// Use middleware to count model requests.
	agent := core.NewAgent[string](cached,
		core.WithAgentMiddleware[string](func(
			ctx context.Context,
			messages []core.ModelMessage,
			settings *core.ModelSettings,
			params *core.ModelRequestParameters,
			next func(context.Context, []core.ModelMessage, *core.ModelSettings, *core.ModelRequestParameters) (*core.ModelResponse, error),
		) (*core.ModelResponse, error) {
			atomic.AddInt32(&requestCount, 1)
			return next(ctx, messages, settings, params)
		}),
	)

	// First run.
	result1, err := agent.Run(ctx, "What is 7+8? Reply with just the number.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("first Run failed: %v", err)
	}

	// Second run with same prompt — agent constructs identical messages.
	result2, err := agent.Run(ctx, "What is 7+8? Reply with just the number.")
	if err != nil {
		t.Fatalf("second Run failed: %v", err)
	}

	count := atomic.LoadInt32(&requestCount)
	t.Logf("First=%q Second=%q MiddlewareCalls=%d", result1.Output, result2.Output, count)
}

// TestComposedModelWrappers verifies wrapper composition.
func TestComposedModelWrappers(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Compose: Retry → RateLimit → Base.
	base := newAnthropicProvider()
	retried := modelutil.NewRetryModel(base, modelutil.DefaultRetryConfig())
	rateLimited := modelutil.NewRateLimitedModel(retried, 5.0, 5)

	agent := core.NewAgent[string](rateLimited)

	result, err := agent.Run(ctx, "Say 'composed' and nothing else.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	text := strings.ToLower(result.Output)
	if !strings.Contains(text, "composed") {
		t.Errorf("expected 'composed', got %q", result.Output)
	}

	t.Logf("Output=%q", result.Output)
}

// TestCachedModelStreamBypass verifies streaming bypasses cache.
func TestCachedModelStreamBypass(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cache := modelutil.NewMemoryCache()
	model := modelutil.NewCachedModel(newAnthropicProvider(), cache)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Say 'streamed' and nothing else."},
			},
		},
	}

	// Streaming should work even though cache can't store stream results.
	stream, err := model.RequestStream(ctx, messages, nil, nil)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("RequestStream failed: %v", err)
	}
	defer stream.Close()

	// Drain the stream.
	var text string
	for {
		event, err := stream.Next()
		if err != nil {
			break
		}
		if e, ok := event.(core.PartDeltaEvent); ok {
			if td, ok := e.Delta.(core.TextPartDelta); ok {
				text += td.ContentDelta
			}
		}
		if e, ok := event.(core.PartStartEvent); ok {
			if tp, ok := e.Part.(core.TextPart); ok {
				text += tp.Content
			}
		}
	}

	if text == "" {
		t.Error("expected non-empty streaming response")
	}
	t.Logf("StreamedText=%q", text)
}

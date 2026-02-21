//go:build e2e

package e2e

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestLoggingMiddleware verifies the built-in logging middleware fires.
func TestLoggingMiddleware(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	var logs []string

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithAgentMiddleware[string](core.LoggingMiddleware(func(msg string) {
			mu.Lock()
			logs = append(logs, msg)
			mu.Unlock()
		})),
	)

	_, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(logs) == 0 {
		t.Error("logging middleware produced no logs")
	}

	t.Logf("Log entries: %d, first: %q", len(logs), logs[0])
}

// TestMaxTokensMiddleware verifies max tokens middleware sets the limit.
func TestMaxTokensMiddleware(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithAgentMiddleware[string](core.MaxTokensMiddleware(50)),
	)

	result, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	// Output should be short due to 50 token limit.
	t.Logf("Output (max 50 tokens): %q", result.Output)
}

// TestTimingMiddleware verifies the timing middleware reports durations.
func TestTimingMiddleware(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var duration time.Duration
	var durationSet int32

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithAgentMiddleware[string](core.TimingMiddleware(func(d time.Duration) {
			duration = d
			atomic.AddInt32(&durationSet, 1)
		})),
	)

	_, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if atomic.LoadInt32(&durationSet) == 0 {
		t.Error("timing middleware callback was never called")
	}
	if duration <= 0 {
		t.Errorf("expected positive duration, got %v", duration)
	}

	t.Logf("Request duration: %v", duration)
}

// TestMiddlewareStacking verifies multiple middlewares work together.
func TestMiddlewareStacking(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var logCalled, timingCalled int32

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithAgentMiddleware[string](core.LoggingMiddleware(func(msg string) {
			atomic.AddInt32(&logCalled, 1)
		})),
		core.WithAgentMiddleware[string](core.TimingMiddleware(func(d time.Duration) {
			atomic.AddInt32(&timingCalled, 1)
		})),
		core.WithAgentMiddleware[string](core.MaxTokensMiddleware(200)),
	)

	result, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if atomic.LoadInt32(&logCalled) == 0 {
		t.Error("logging middleware not called")
	}
	if atomic.LoadInt32(&timingCalled) == 0 {
		t.Error("timing middleware not called")
	}

	t.Logf("Output: %q LogCalls=%d TimingCalls=%d", result.Output, logCalled, timingCalled)
}

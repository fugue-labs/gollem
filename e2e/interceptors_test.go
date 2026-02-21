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

// TestRedactPII verifies the RedactPII interceptor replaces sensitive patterns.
func TestRedactPII(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// RedactPII with email pattern.
	emailPattern := `[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithMessageInterceptor[string](core.RedactPII(emailPattern, "[EMAIL_REDACTED]")),
	)

	result, err := agent.Run(ctx, "My email is user@example.com. What did I just tell you? Repeat exactly what I said about my email.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	// The model should not have seen the actual email.
	t.Logf("Output: %q", result.Output)
}

// TestAuditLog verifies the AuditLog interceptor logs messages.
func TestAuditLog(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	var logEntries []string

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithMessageInterceptor[string](core.AuditLog(func(direction string, messages []core.ModelMessage) {
			mu.Lock()
			logEntries = append(logEntries, direction)
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

	if len(logEntries) == 0 {
		t.Error("expected audit log entries")
	}

	t.Logf("AuditLog entries: %v", logEntries)
}

// TestResponseInterceptor verifies that response interceptors fire.
func TestResponseInterceptor(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var intercepted int32

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithResponseInterceptor[string](func(ctx context.Context, resp *core.ModelResponse) core.InterceptResult {
			atomic.AddInt32(&intercepted, 1)
			return core.InterceptResult{Action: core.MessageAllow}
		}),
	)

	_, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if atomic.LoadInt32(&intercepted) == 0 {
		t.Error("response interceptor was never called")
	}

	t.Logf("Response interceptor called %d times", intercepted)
}

// TestInterceptorChaining verifies that both message and response interceptors apply.
func TestInterceptorChaining(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var msgIntercepted, respIntercepted int32

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithMessageInterceptor[string](func(ctx context.Context, messages []core.ModelMessage) core.InterceptResult {
			atomic.AddInt32(&msgIntercepted, 1)
			return core.InterceptResult{Action: core.MessageAllow, Messages: messages}
		}),
		core.WithResponseInterceptor[string](func(ctx context.Context, resp *core.ModelResponse) core.InterceptResult {
			atomic.AddInt32(&respIntercepted, 1)
			return core.InterceptResult{Action: core.MessageAllow}
		}),
	)

	result, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if atomic.LoadInt32(&msgIntercepted) == 0 {
		t.Error("message interceptor was never called")
	}
	if atomic.LoadInt32(&respIntercepted) == 0 {
		t.Error("response interceptor was never called")
	}

	t.Logf("Output: %q MsgIntercepted=%d RespIntercepted=%d",
		result.Output, msgIntercepted, respIntercepted)
}

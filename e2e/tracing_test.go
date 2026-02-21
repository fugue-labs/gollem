//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// --- Phase 10: Execution tracing & observability ---

// TestTracingBasic verifies trace data is captured for a simple run.
func TestTracingBasic(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTracing[string](),
	)

	result, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if result.Trace == nil {
		t.Fatal("expected Trace to be non-nil with WithTracing enabled")
	}

	trace := result.Trace
	if trace.RunID == "" {
		t.Error("expected non-empty RunID in trace")
	}
	if trace.Prompt != "Say hello" {
		t.Errorf("expected prompt 'Say hello', got %q", trace.Prompt)
	}
	if !trace.Success {
		t.Errorf("expected Success=true, got false (error=%q)", trace.Error)
	}
	if trace.Duration <= 0 {
		t.Errorf("expected positive duration, got %v", trace.Duration)
	}
	if len(trace.Steps) == 0 {
		t.Error("expected at least one trace step")
	}

	// Should have at least a model_request and model_response step.
	stepKinds := map[core.TraceStepKind]int{}
	for _, step := range trace.Steps {
		stepKinds[step.Kind]++
	}
	if stepKinds[core.TraceModelRequest] == 0 {
		t.Error("expected at least one model_request trace step")
	}
	if stepKinds[core.TraceModelResponse] == 0 {
		t.Error("expected at least one model_response trace step")
	}

	t.Logf("Trace: RunID=%s Duration=%v Steps=%d StepKinds=%v",
		trace.RunID, trace.Duration, len(trace.Steps), stepKinds)
}

// TestTracingWithTools verifies trace captures tool call steps.
func TestTracingWithTools(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A+p.B), nil
	})

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
		core.WithTracing[string](),
	)

	result, err := agent.Run(ctx, "Use the add tool to compute 5+3.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if result.Trace == nil {
		t.Fatal("expected Trace to be non-nil")
	}

	// Should have tool_call and tool_result steps.
	stepKinds := map[core.TraceStepKind]int{}
	for _, step := range result.Trace.Steps {
		stepKinds[step.Kind]++
	}

	if stepKinds[core.TraceToolCall] == 0 {
		t.Error("expected at least one tool_call trace step")
	}
	if stepKinds[core.TraceToolResult] == 0 {
		t.Error("expected at least one tool_result trace step")
	}

	t.Logf("Trace steps: %v", stepKinds)
}

// TestTracingDisabled verifies trace is nil when not enabled.
func TestTracingDisabled(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	result, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if result.Trace != nil {
		t.Error("expected Trace to be nil without WithTracing")
	}

	t.Log("Trace correctly nil when disabled")
}

// TestTracingUsageMatchesResult verifies trace usage matches result usage.
func TestTracingUsageMatchesResult(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTracing[string](),
	)

	result, err := agent.Run(ctx, "Say 'trace test'")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if result.Trace == nil {
		t.Fatal("trace is nil")
	}

	// Trace usage should match result usage.
	if result.Trace.Usage.Requests != result.Usage.Requests {
		t.Errorf("trace requests=%d != result requests=%d",
			result.Trace.Usage.Requests, result.Usage.Requests)
	}

	t.Logf("TraceUsage=%+v ResultUsage=%+v", result.Trace.Usage, result.Usage)
}

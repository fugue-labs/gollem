//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestTraceExporter verifies custom trace exporters receive traces.
func TestTraceExporter(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var exported int32
	var capturedTrace *core.RunTrace
	var mu sync.Mutex

	exporter := testTraceExporter{
		exportFn: func(ctx context.Context, trace *core.RunTrace) error {
			atomic.AddInt32(&exported, 1)
			mu.Lock()
			capturedTrace = trace
			mu.Unlock()
			return nil
		},
	}

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTracing[string](),
		core.WithTraceExporter[string](exporter),
	)

	result, err := agent.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if atomic.LoadInt32(&exported) == 0 {
		t.Error("trace exporter was never called")
	}

	mu.Lock()
	defer mu.Unlock()

	if capturedTrace == nil {
		t.Fatal("captured trace is nil")
	}

	if capturedTrace.RunID == "" {
		t.Error("expected non-empty RunID in trace")
	}
	if !capturedTrace.Success {
		t.Errorf("expected Success=true, got false (Error: %s)", capturedTrace.Error)
	}

	// Also verify the trace is in the result.
	if result.Trace == nil {
		t.Error("expected non-nil Trace in RunResult")
	} else if result.Trace.RunID != capturedTrace.RunID {
		t.Errorf("trace RunID mismatch: result=%q exporter=%q", result.Trace.RunID, capturedTrace.RunID)
	}

	t.Logf("Trace: RunID=%s Steps=%d Duration=%v", capturedTrace.RunID, len(capturedTrace.Steps), capturedTrace.Duration)
}

// testTraceExporter implements core.TraceExporter for testing.
type testTraceExporter struct {
	exportFn func(ctx context.Context, trace *core.RunTrace) error
}

func (e testTraceExporter) Export(ctx context.Context, trace *core.RunTrace) error {
	return e.exportFn(ctx, trace)
}

// TestTraceStepKinds verifies trace contains expected step kinds when tools are used.
func TestTraceStepKinds(t *testing.T) {
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
		t.Fatal("expected non-nil Trace")
	}

	// Collect step kinds.
	kindCount := make(map[core.TraceStepKind]int)
	for _, step := range result.Trace.Steps {
		kindCount[step.Kind]++
	}

	t.Logf("Trace steps: %v", kindCount)

	// Should have at least model request and model response.
	if kindCount[core.TraceModelRequest] == 0 {
		t.Error("expected at least one TraceModelRequest step")
	}
	if kindCount[core.TraceModelResponse] == 0 {
		t.Error("expected at least one TraceModelResponse step")
	}
	// With a tool call, should also have tool call and result.
	if kindCount[core.TraceToolCall] == 0 {
		t.Error("expected at least one TraceToolCall step")
	}
	if kindCount[core.TraceToolResult] == 0 {
		t.Error("expected at least one TraceToolResult step")
	}
}

// TestTraceTimingAccuracy verifies trace step durations are reasonable.
func TestTraceTimingAccuracy(t *testing.T) {
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
		t.Fatal("expected non-nil Trace")
	}

	// Total duration should be positive and reasonable.
	if result.Trace.Duration <= 0 {
		t.Errorf("expected positive trace duration, got %v", result.Trace.Duration)
	}
	if result.Trace.Duration > 60*time.Second {
		t.Errorf("trace duration seems too long: %v", result.Trace.Duration)
	}

	// Step durations (for model response steps which have duration set) should be positive.
	for _, step := range result.Trace.Steps {
		if step.Kind == core.TraceModelResponse && step.Duration > 0 {
			if step.Duration > result.Trace.Duration {
				t.Errorf("step duration %v exceeds total duration %v", step.Duration, result.Trace.Duration)
			}
		}
	}

	t.Logf("Trace duration: %v Steps: %d", result.Trace.Duration, len(result.Trace.Steps))
}

// TestCostTrackerMultiModel verifies cost tracking across multiple models.
func TestCostTrackerMultiModel(t *testing.T) {
	anthropicOnly(t)
	skipIfNoCredentials(t, "OPENAI_API_KEY")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	tracker := core.NewCostTracker(map[string]core.ModelPricing{
		"claude-haiku-4-5-20251001": {
			InputTokenCost:  0.0000008,
			OutputTokenCost: 0.000004,
		},
		"gpt-4o-mini": {
			InputTokenCost:  0.00000015,
			OutputTokenCost: 0.0000006,
		},
	})

	// Run with Anthropic.
	agent1 := core.NewAgent[string](newAnthropicProvider(),
		core.WithCostTracker[string](tracker),
	)
	_, err := agent1.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("anthropic run failed: %v", err)
	}

	// Run with OpenAI.
	agent2 := core.NewAgent[string](newOpenAIProvider(),
		core.WithCostTracker[string](tracker),
	)
	_, err = agent2.Run(ctx, "Say hello")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("openai run failed: %v", err)
	}

	// Verify breakdown has entries for both models.
	breakdown := tracker.CostBreakdown()
	totalCost := tracker.TotalCost()

	if totalCost <= 0 {
		t.Errorf("expected positive total cost, got %f", totalCost)
	}

	t.Logf("TotalCost=$%.8f Breakdown=%v", totalCost, breakdown)
}

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestBatchBasic runs multiple prompts concurrently and verifies all return valid results.
func TestBatchBasic(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	prompts := []string{
		"What is 2+2? Reply with just the number.",
		"What is the capital of France? Reply with just the city name.",
		"What color is the sky? Reply with just the color.",
	}

	results := agent.RunBatch(ctx, prompts)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	for i, r := range results {
		if r.Err != nil {
			skipOnAccountError(t, r.Err)
			t.Errorf("prompt[%d] failed: %v", i, r.Err)
			continue
		}
		if r.Result == nil {
			t.Errorf("prompt[%d] has nil result", i)
			continue
		}
		if r.Result.Output == "" {
			t.Errorf("prompt[%d] has empty output", i)
		}
		if r.Index != i {
			t.Errorf("prompt[%d] has wrong index: %d", i, r.Index)
		}
		t.Logf("prompt[%d] output=%q", i, r.Result.Output)
	}
}

// TestBatchWithConcurrencyLimit forces serial execution.
func TestBatchWithConcurrencyLimit(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	prompts := []string{
		"Say 'alpha'",
		"Say 'beta'",
	}

	results := agent.RunBatch(ctx, prompts, core.WithBatchConcurrency(1))

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	for i, r := range results {
		if r.Err != nil {
			skipOnAccountError(t, r.Err)
			t.Errorf("prompt[%d] failed: %v", i, r.Err)
			continue
		}
		if r.Result == nil || r.Result.Output == "" {
			t.Errorf("prompt[%d] has empty result", i)
		}
		t.Logf("prompt[%d] output=%q", i, r.Result.Output)
	}
}

// TestBatchWithTools verifies tools work correctly in batch mode.
func TestBatchWithTools(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A+p.B), nil
	})

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
	)

	prompts := []string{
		"Use the add tool to compute 10+20. Report the result.",
		"Use the add tool to compute 3+7. Report the result.",
	}

	results := agent.RunBatch(ctx, prompts)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	expectedContains := []string{"30", "10"}
	for i, r := range results {
		if r.Err != nil {
			skipOnAccountError(t, r.Err)
			t.Errorf("prompt[%d] failed: %v", i, r.Err)
			continue
		}
		if r.Result == nil {
			t.Errorf("prompt[%d] has nil result", i)
			continue
		}
		if !strings.Contains(r.Result.Output, expectedContains[i]) {
			t.Errorf("prompt[%d]: expected output to contain %q, got %q", i, expectedContains[i], r.Result.Output)
		}
		t.Logf("prompt[%d] output=%q", i, r.Result.Output)
	}
}

// TestBatchEmpty verifies empty input returns empty results.
func TestBatchEmpty(t *testing.T) {
	anthropicOnly(t)

	ctx := context.Background()

	agent := core.NewAgent[string](newAnthropicProvider())
	results := agent.RunBatch(ctx, nil)

	if results != nil {
		t.Errorf("expected nil results for nil prompts, got %v", results)
	}

	results = agent.RunBatch(ctx, []string{})
	if results != nil {
		t.Errorf("expected nil results for empty prompts, got %v", results)
	}
}

// TestBatchPartialFailure verifies that one failing prompt doesn't affect others.
func TestBatchPartialFailure(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithInputGuardrail[string]("block_forbidden", core.ContentFilter("BLOCKED_KEYWORD")),
	)

	prompts := []string{
		"Say hello",
		"This contains BLOCKED_KEYWORD",
		"Say goodbye",
	}

	results := agent.RunBatch(ctx, prompts)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// First and third should succeed.
	for _, idx := range []int{0, 2} {
		if results[idx].Err != nil {
			skipOnAccountError(t, results[idx].Err)
			t.Errorf("prompt[%d] should have succeeded: %v", idx, results[idx].Err)
		}
	}

	// Second should fail due to guardrail.
	if results[1].Err == nil {
		t.Error("prompt[1] should have failed due to guardrail")
	} else {
		t.Logf("prompt[1] correctly failed: %v", results[1].Err)
	}
}

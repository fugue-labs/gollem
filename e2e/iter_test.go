//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestIterBasic verifies step-by-step iteration through an agent run.
func TestIterBasic(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	iter := agent.Iter(ctx, "Say hello")

	var steps int
	for !iter.Done() {
		resp, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			skipOnAccountError(t, err)
			t.Fatalf("iter.Next() failed at step %d: %v", steps, err)
		}
		steps++
		if resp != nil {
			t.Logf("step %d: text=%q toolCalls=%d", steps, resp.TextContent(), len(resp.ToolCalls()))
		}
	}

	if steps == 0 {
		t.Fatal("expected at least one iteration step")
	}

	result, err := iter.Result()
	if err != nil {
		t.Fatalf("iter.Result() failed: %v", err)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}

	t.Logf("Steps=%d Output=%q", steps, result.Output)
}

// TestIterWithTools verifies iteration through a run with tool calls.
func TestIterWithTools(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A+p.B), nil
	})

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
	)

	iter := agent.Iter(ctx, "Use the add tool to compute 5+3.")

	var steps int
	var sawToolCall bool
	for !iter.Done() {
		resp, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			skipOnAccountError(t, err)
			t.Fatalf("iter.Next() failed at step %d: %v", steps, err)
		}
		steps++
		if resp != nil && len(resp.ToolCalls()) > 0 {
			sawToolCall = true
			t.Logf("step %d: tool call to %q", steps, resp.ToolCalls()[0].ToolName)
		}
	}

	if !sawToolCall {
		t.Error("expected at least one tool call during iteration")
	}

	result, err := iter.Result()
	if err != nil {
		t.Fatalf("iter.Result() failed: %v", err)
	}

	t.Logf("Steps=%d Output=%q", steps, result.Output)
}

// TestIterMessages verifies message history grows with each step.
func TestIterMessages(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	iter := agent.Iter(ctx, "Say hello")

	initialMsgCount := len(iter.Messages())
	if initialMsgCount == 0 {
		t.Fatal("expected at least one initial message (the request)")
	}
	t.Logf("initial message count: %d", initialMsgCount)

	for !iter.Done() {
		_, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			skipOnAccountError(t, err)
			t.Fatalf("iter.Next() failed: %v", err)
		}
	}

	finalMsgCount := len(iter.Messages())
	if finalMsgCount <= initialMsgCount {
		t.Errorf("expected message count to grow: initial=%d final=%d", initialMsgCount, finalMsgCount)
	}
	t.Logf("message count: initial=%d final=%d", initialMsgCount, finalMsgCount)
}

// TestIterDoneAfterComplete verifies that calling Next() after done returns EOF.
func TestIterDoneAfterComplete(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())
	iter := agent.Iter(ctx, "Say hello")

	// Consume all steps.
	for !iter.Done() {
		_, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			skipOnAccountError(t, err)
			t.Fatalf("iter.Next() failed: %v", err)
		}
	}

	// Calling Next() again should return EOF.
	_, err := iter.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF after completion, got: %v", err)
	}
}

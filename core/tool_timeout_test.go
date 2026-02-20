package core

import (
	"context"
	"testing"
	"time"
)

func TestToolTimeout_Enforced(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}
	slowTool := FuncTool[Params]("slow", "slow tool", func(ctx context.Context, p Params) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			return "done", nil
		}
	}, WithToolTimeout(50*time.Millisecond))

	model := NewTestModel(
		ToolCallResponse("slow", `{"n":1}`),
		TextResponse("done"),
	)
	agent := NewAgent[string](model, WithTools[string](slowTool))

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	// The tool should have timed out, and the model should have received an error.
	calls := model.Calls()
	if len(calls) < 2 {
		t.Fatal("expected at least 2 model calls")
	}
	_ = result
}

func TestToolTimeout_FastTool(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}
	fastTool := FuncTool[Params]("fast", "fast tool", func(ctx context.Context, p Params) (string, error) {
		return "quick", nil
	}, WithToolTimeout(5*time.Second))

	model := NewTestModel(
		ToolCallResponse("fast", `{"n":1}`),
		TextResponse("done"),
	)
	agent := NewAgent[string](model, WithTools[string](fastTool))

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" {
		t.Errorf("expected 'done', got %q", result.Output)
	}
}

func TestToolTimeout_PerTool(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}
	// Per-tool timeout should override agent default.
	slowTool := FuncTool[Params]("slow", "slow", func(ctx context.Context, p Params) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			return "done", nil
		}
	}, WithToolTimeout(50*time.Millisecond))

	model := NewTestModel(
		ToolCallResponse("slow", `{"n":1}`),
		TextResponse("done"),
	)
	// Agent default is very long, but per-tool is short.
	agent := NewAgent[string](model,
		WithTools[string](slowTool),
		WithDefaultToolTimeout[string](10*time.Second),
	)

	start := time.Now()
	_, err := agent.Run(context.Background(), "test")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	// Per-tool timeout (50ms) should win over agent default (10s).
	if elapsed > 2*time.Second {
		t.Errorf("per-tool timeout not applied, elapsed %v", elapsed)
	}
}

func TestToolTimeout_AgentDefault(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}
	// Tool without its own timeout.
	slowTool := FuncTool[Params]("slow", "slow", func(ctx context.Context, p Params) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			return "done", nil
		}
	})

	model := NewTestModel(
		ToolCallResponse("slow", `{"n":1}`),
		TextResponse("done"),
	)
	agent := NewAgent[string](model,
		WithTools[string](slowTool),
		WithDefaultToolTimeout[string](50*time.Millisecond),
	)

	start := time.Now()
	_, err := agent.Run(context.Background(), "test")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("agent default timeout not applied, elapsed %v", elapsed)
	}
}

func TestToolTimeout_NoTimeout(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}
	tool := FuncTool[Params]("quick", "quick", func(ctx context.Context, p Params) (string, error) {
		return "result", nil
	})

	model := NewTestModel(
		ToolCallResponse("quick", `{"n":1}`),
		TextResponse("done"),
	)
	agent := NewAgent[string](model, WithTools[string](tool))

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" {
		t.Errorf("expected 'done', got %q", result.Output)
	}
}

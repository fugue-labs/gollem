//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/graph"
)

// TestGraphLinearWorkflow tests a simple linear A -> B -> end graph.
func TestGraphLinearWorkflow(t *testing.T) {
	type State struct {
		Steps []string
	}

	g := graph.NewGraph[State]().
		AddNode(graph.Node[State]{
			Name: "step_a",
			Run: func(ctx context.Context, s *State) (string, error) {
				s.Steps = append(s.Steps, "A")
				return "step_b", nil
			},
		}).
		AddNode(graph.Node[State]{
			Name: "step_b",
			Run: func(ctx context.Context, s *State) (string, error) {
				s.Steps = append(s.Steps, "B")
				return graph.EndNode, nil
			},
		}).
		SetEntryPoint("step_a")

	ctx := context.Background()
	result, err := g.Run(ctx, State{})
	if err != nil {
		t.Fatalf("graph.Run failed: %v", err)
	}

	if len(result.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(result.Steps))
	}
	if result.Steps[0] != "A" || result.Steps[1] != "B" {
		t.Errorf("expected [A B], got %v", result.Steps)
	}

	t.Logf("Steps: %v", result.Steps)
}

// TestGraphConditionalBranching tests dynamic routing based on state.
func TestGraphConditionalBranching(t *testing.T) {
	type State struct {
		Input  string
		Output string
	}

	g := graph.NewGraph[State]().
		AddNode(graph.Node[State]{
			Name: "router",
			Run: func(ctx context.Context, s *State) (string, error) {
				if strings.Contains(s.Input, "urgent") {
					return "fast_path", nil
				}
				return "slow_path", nil
			},
		}).
		AddNode(graph.Node[State]{
			Name: "fast_path",
			Run: func(ctx context.Context, s *State) (string, error) {
				s.Output = "FAST: " + s.Input
				return graph.EndNode, nil
			},
		}).
		AddNode(graph.Node[State]{
			Name: "slow_path",
			Run: func(ctx context.Context, s *State) (string, error) {
				s.Output = "SLOW: " + s.Input
				return graph.EndNode, nil
			},
		}).
		SetEntryPoint("router")

	ctx := context.Background()

	fast, err := g.Run(ctx, State{Input: "urgent task"})
	if err != nil {
		t.Fatalf("fast path: %v", err)
	}
	if !strings.HasPrefix(fast.Output, "FAST:") {
		t.Errorf("expected FAST prefix, got %q", fast.Output)
	}

	slow, err := g.Run(ctx, State{Input: "normal task"})
	if err != nil {
		t.Fatalf("slow path: %v", err)
	}
	if !strings.HasPrefix(slow.Output, "SLOW:") {
		t.Errorf("expected SLOW prefix, got %q", slow.Output)
	}

	t.Logf("Fast: %q, Slow: %q", fast.Output, slow.Output)
}

// TestGraphFanOut tests parallel fan-out with reducer.
func TestGraphFanOut(t *testing.T) {
	type State struct {
		Value  int
		Values []int
	}

	g := graph.NewGraph[State]().
		AddFanOutNode("split", func(ctx context.Context, s *State) ([]graph.Send[State], string, error) {
			return []graph.Send[State]{
				{Node: "double", State: State{Value: s.Value}},
				{Node: "triple", State: State{Value: s.Value}},
			}, "collect", nil
		}).
		AddNode(graph.Node[State]{
			Name: "double",
			Run: func(ctx context.Context, s *State) (string, error) {
				s.Value = s.Value * 2
				return graph.EndNode, nil
			},
		}).
		AddNode(graph.Node[State]{
			Name: "triple",
			Run: func(ctx context.Context, s *State) (string, error) {
				s.Value = s.Value * 3
				return graph.EndNode, nil
			},
		}).
		AddNode(graph.Node[State]{
			Name: "collect",
			Run: func(ctx context.Context, s *State) (string, error) {
				return graph.EndNode, nil
			},
		}).
		SetReducer(func(states []State) State {
			var values []int
			for _, s := range states {
				values = append(values, s.Value)
			}
			return State{Values: values}
		}).
		SetEntryPoint("split")

	ctx := context.Background()
	result, err := g.Run(ctx, State{Value: 5})
	if err != nil {
		t.Fatalf("graph.Run failed: %v", err)
	}

	if len(result.Values) != 2 {
		t.Fatalf("expected 2 values, got %d: %v", len(result.Values), result.Values)
	}

	// Values should be 10 (5*2) and 15 (5*3) in some order.
	sum := 0
	for _, v := range result.Values {
		sum += v
	}
	if sum != 25 {
		t.Errorf("expected sum 25 (10+15), got %d from %v", sum, result.Values)
	}

	t.Logf("Fan-out values: %v (sum=%d)", result.Values, sum)
}

// TestGraphMaxIterations tests cycle protection.
func TestGraphMaxIterations(t *testing.T) {
	type State struct {
		Count int
	}

	g := graph.NewGraph[State]().
		AddNode(graph.Node[State]{
			Name: "loop",
			Run: func(ctx context.Context, s *State) (string, error) {
				s.Count++
				return "loop", nil // infinite loop
			},
		}).
		SetEntryPoint("loop").
		SetMaxIterations(5)

	ctx := context.Background()
	result, err := g.Run(ctx, State{})
	if err == nil {
		t.Fatal("expected max iterations error")
	}

	if !strings.Contains(err.Error(), "max iterations") {
		t.Errorf("expected max iterations error, got: %v", err)
	}

	if result.Count != 5 {
		t.Errorf("expected 5 iterations, got %d", result.Count)
	}

	t.Logf("Max iterations enforced: count=%d err=%v", result.Count, err)
}

// TestGraphContextCancellation tests that cancellation propagates.
func TestGraphContextCancellation(t *testing.T) {
	type State struct {
		Count int
	}

	g := graph.NewGraph[State]().
		AddNode(graph.Node[State]{
			Name: "slow",
			Run: func(ctx context.Context, s *State) (string, error) {
				s.Count++
				time.Sleep(50 * time.Millisecond)
				return "slow", nil
			},
		}).
		SetEntryPoint("slow").
		SetMaxIterations(1000)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := g.Run(ctx, State{})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}

	if result.Count == 0 {
		t.Error("expected at least one iteration before cancellation")
	}

	t.Logf("Cancelled after %d iterations: %v", result.Count, err)
}

// TestGraphWithAgentNode tests a graph node that calls an LLM agent.
func TestGraphWithAgentNode(t *testing.T) {
	anthropicOnly(t)

	type State struct {
		Question string
		Answer   string
	}

	agent := core.NewAgent[string](newAnthropicProvider())

	g := graph.NewGraph[State]().
		AddNode(graph.Node[State]{
			Name: "ask",
			Run: func(ctx context.Context, s *State) (string, error) {
				result, err := agent.Run(ctx, s.Question)
				if err != nil {
					return "", err
				}
				s.Answer = result.Output
				return graph.EndNode, nil
			},
		}).
		SetEntryPoint("ask")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := g.Run(ctx, State{Question: "What is 2+2? Reply with just the number."})
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("graph.Run failed: %v", err)
	}

	if result.Answer == "" {
		t.Error("expected non-empty answer")
	}

	t.Logf("Agent answer: %q", result.Answer)
}

// TestGraphMermaid tests Mermaid diagram generation.
func TestGraphMermaid(t *testing.T) {
	type State struct{}

	g := graph.NewGraph[State]().
		AddNode(graph.Node[State]{
			Name: "start",
			Run: func(ctx context.Context, s *State) (string, error) {
				return "end", nil
			},
		}).
		AddNode(graph.Node[State]{
			Name: "end",
			Run: func(ctx context.Context, s *State) (string, error) {
				return graph.EndNode, nil
			},
		}).
		SetEntryPoint("start")

	mermaid := g.Mermaid()
	if !strings.Contains(mermaid, "graph TD") {
		t.Error("expected Mermaid graph header")
	}
	if !strings.Contains(mermaid, "start") {
		t.Error("expected start node in Mermaid output")
	}
	if !strings.Contains(mermaid, "Start") {
		t.Error("expected entry point marker in Mermaid output")
	}

	t.Logf("Mermaid:\n%s", mermaid)
}

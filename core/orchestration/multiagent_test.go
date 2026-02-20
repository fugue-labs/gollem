package orchestration_test

import (
	"context"
	"testing"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/orchestration"
)

func TestAgentTool_Delegation(t *testing.T) {
	// Inner agent: given a prompt, returns a response.
	innerModel := core.NewTestModel(core.TextResponse("Inner agent completed the task."))
	innerAgent := core.NewAgent[string](innerModel)

	// Outer agent: calls inner agent as a tool.
	outerModel := core.NewTestModel(
		core.ToolCallResponseWithID("delegate", `{"prompt":"Do the inner task"}`, "tc1"),
		core.TextResponse("Outer done."),
	)
	outerAgent := core.NewAgent[string](outerModel,
		core.WithTools[string](orchestration.AgentTool("delegate", "Delegate to inner agent", innerAgent)),
	)

	result, err := outerAgent.Run(context.Background(), "Do the task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Outer done." {
		t.Errorf("expected 'Outer done.', got %q", result.Output)
	}
}

func TestAgentTool_ChainedDelegation(t *testing.T) {
	// Level 3: innermost agent.
	level3Model := core.NewTestModel(core.TextResponse("Level 3 result"))
	level3Agent := core.NewAgent[string](level3Model)

	// Level 2: calls level 3.
	level2Model := core.NewTestModel(
		core.ToolCallResponseWithID("level3", `{"prompt":"Go deeper"}`, "tc1"),
		core.TextResponse("Level 2 done."),
	)
	level2Agent := core.NewAgent[string](level2Model,
		core.WithTools[string](orchestration.AgentTool("level3", "Call level 3", level3Agent)),
	)

	// Level 1: calls level 2.
	level1Model := core.NewTestModel(
		core.ToolCallResponseWithID("level2", `{"prompt":"Go to level 2"}`, "tc1"),
		core.TextResponse("Level 1 done."),
	)
	level1Agent := core.NewAgent[string](level1Model,
		core.WithTools[string](orchestration.AgentTool("level2", "Call level 2", level2Agent)),
	)

	result, err := level1Agent.Run(context.Background(), "Start chain")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Level 1 done." {
		t.Errorf("expected 'Level 1 done.', got %q", result.Output)
	}
}

func TestHandoff_TwoAgents(t *testing.T) {
	agentA := core.NewAgent[string](core.NewTestModel(core.TextResponse("Step A output")))
	agentB := core.NewAgent[string](core.NewTestModel(core.TextResponse("Step B final")))

	pipeline := orchestration.NewHandoff[string]()
	pipeline.AddStep("agent-a", agentA, nil)
	pipeline.AddStep("agent-b", agentB, func(prevOutput string) string {
		return "Continue from: " + prevOutput
	})

	result, err := pipeline.Run(context.Background(), "Start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Step B final" {
		t.Errorf("expected 'Step B final', got %q", result.Output)
	}
}

func TestHandoff_UsageAggregation(t *testing.T) {
	agentA := core.NewAgent[string](core.NewTestModel(core.TextResponse("A")))
	agentB := core.NewAgent[string](core.NewTestModel(core.TextResponse("B")))

	pipeline := orchestration.NewHandoff[string]()
	pipeline.AddStep("a", agentA, nil)
	pipeline.AddStep("b", agentB, func(_ string) string { return "B prompt" })

	result, err := pipeline.Run(context.Background(), "Start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both agents made one request each.
	if result.Usage.Requests != 2 {
		t.Errorf("expected 2 total requests, got %d", result.Usage.Requests)
	}
}

func TestHandoff_EmptyPipeline(t *testing.T) {
	pipeline := orchestration.NewHandoff[string]()
	_, err := pipeline.Run(context.Background(), "Start")
	if err == nil {
		t.Fatal("expected error for empty pipeline")
	}
}

func TestHandoff_ThreeAgents(t *testing.T) {
	agentA := core.NewAgent[string](core.NewTestModel(core.TextResponse("A result")))
	agentB := core.NewAgent[string](core.NewTestModel(core.TextResponse("B result")))
	agentC := core.NewAgent[string](core.NewTestModel(core.TextResponse("C final")))

	pipeline := orchestration.NewHandoff[string]()
	pipeline.AddStep("a", agentA, nil)
	pipeline.AddStep("b", agentB, func(prev string) string { return "From A: " + prev })
	pipeline.AddStep("c", agentC, func(prev string) string { return "From B: " + prev })

	result, err := pipeline.Run(context.Background(), "Initial")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "C final" {
		t.Errorf("expected 'C final', got %q", result.Output)
	}
	if result.Usage.Requests != 3 {
		t.Errorf("expected 3 requests, got %d", result.Usage.Requests)
	}
}

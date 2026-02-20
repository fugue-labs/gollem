package temporal

import (
	"context"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestTemporalAgent_Construction(t *testing.T) {
	type Params struct {
		Q string `json:"q"`
	}

	model := core.NewTestModel(core.TextResponse("Hello!"))
	tool := core.FuncTool[Params]("search", "Search",
		func(_ context.Context, p Params) (string, error) {
			return "result", nil
		},
	)

	agent := core.NewAgent[string](model, core.WithTools[string](tool))
	ta := NewTemporalAgent(agent, WithName("test-agent"))

	if ta.Name() != "test-agent" {
		t.Errorf("expected name 'test-agent', got %q", ta.Name())
	}

	activities := ta.Activities()
	expectedNames := []string{
		"agent__test-agent__model_request",
		"agent__test-agent__model_request_stream",
		"agent__test-agent__tool__search",
	}
	for _, name := range expectedNames {
		if _, ok := activities[name]; !ok {
			t.Errorf("expected activity %q not found", name)
		}
	}
}

func TestTemporalAgent_RunOutsideWorkflow(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Direct response"))
	agent := core.NewAgent[string](model)
	ta := NewTemporalAgent(agent, WithName("direct-agent"))

	result, err := ta.Run(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Direct response" {
		t.Errorf("expected 'Direct response', got %q", result.Output)
	}
}

func TestTemporalAgent_Activities(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Hi"))

	type P1 struct{ Q string `json:"q"` }
	type P2 struct{ N int `json:"n"` }

	tool1 := core.FuncTool[P1]("tool1", "Tool 1",
		func(_ context.Context, p P1) (string, error) { return p.Q, nil })
	tool2 := core.FuncTool[P2]("tool2", "Tool 2",
		func(_ context.Context, p P2) (int, error) { return p.N * 2, nil })

	agent := core.NewAgent[string](model, core.WithTools[string](tool1, tool2))
	ta := NewTemporalAgent(agent, WithName("multi-tool"))

	activities := ta.Activities()

	// Should have 2 model activities + 2 tool activities = 4.
	if len(activities) != 4 {
		t.Errorf("expected 4 activities, got %d", len(activities))
	}
}

func TestTemporalAgent_NameRequired(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when name not set")
		}
	}()

	model := core.NewTestModel(core.TextResponse("Hi"))
	agent := core.NewAgent[string](model)
	_ = NewTemporalAgent(agent) // Should panic.
}

func TestTemporalAgent_WithPassthrough(t *testing.T) {
	type P struct{}

	model := core.NewTestModel(core.TextResponse("Hi"))
	tool1 := core.FuncTool[P]("fast_tool", "Fast",
		func(_ context.Context, _ P) (string, error) { return "fast", nil })
	tool2 := core.FuncTool[P]("slow_tool", "Slow",
		func(_ context.Context, _ P) (string, error) { return "slow", nil })

	agent := core.NewAgent[string](model, core.WithTools[string](tool1, tool2))
	ta := NewTemporalAgent(agent,
		WithName("passthrough-test"),
		WithToolPassthrough("fast_tool"),
	)

	activities := ta.Activities()
	// fast_tool should be skipped (passthrough).
	if _, ok := activities["agent__passthrough-test__tool__fast_tool"]; ok {
		t.Error("fast_tool should be passthrough, not an activity")
	}
	// slow_tool should be an activity.
	if _, ok := activities["agent__passthrough-test__tool__slow_tool"]; !ok {
		t.Error("slow_tool should be an activity")
	}
}

func TestTemporalAgent_WithActivityConfig(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Hi"))
	agent := core.NewAgent[string](model)

	config := ActivityConfig{
		StartToCloseTimeout: 120000000000, // 2 minutes
		MaxRetries:          3,
	}

	ta := NewTemporalAgent(agent,
		WithName("config-test"),
		WithActivityConfig(config),
	)

	if ta.config.defaultConfig.MaxRetries != 3 {
		t.Errorf("expected 3 max retries, got %d", ta.config.defaultConfig.MaxRetries)
	}
}

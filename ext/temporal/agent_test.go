package temporal

import (
	"context"
	"strings"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"

	"github.com/fugue-labs/gollem/core"
)

type recordingWorker struct {
	workflowNames []string
	activityNames []string
}

func (w *recordingWorker) RegisterWorkflow(interface{}) {}
func (w *recordingWorker) RegisterWorkflowWithOptions(_ interface{}, options workflow.RegisterOptions) {
	w.workflowNames = append(w.workflowNames, options.Name)
}
func (w *recordingWorker) RegisterDynamicWorkflow(interface{}, workflow.DynamicRegisterOptions) {}
func (w *recordingWorker) RegisterActivity(interface{})                                         {}
func (w *recordingWorker) RegisterActivityWithOptions(_ interface{}, options activity.RegisterOptions) {
	w.activityNames = append(w.activityNames, options.Name)
}
func (w *recordingWorker) RegisterDynamicActivity(interface{}, activity.DynamicRegisterOptions) {}
func (w *recordingWorker) RegisterNexusService(*nexus.Service)                                  {}
func (w *recordingWorker) Start() error                                                         { return nil }
func (w *recordingWorker) Run(<-chan interface{}) error                                         { return nil }
func (w *recordingWorker) Stop()                                                                {}

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

func TestTemporalAgent_EventHandlerAccessor(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	agent := core.NewAgent[string](model)
	handler := func(_ context.Context, _ core.ModelResponseStreamEvent) error { return nil }

	ta := NewTemporalAgent(agent, WithName("handler-agent"), WithEventHandler(handler))
	if ta.EventHandler() == nil {
		t.Fatal("expected event handler accessor to return configured handler")
	}
}

func TestTemporalAgent_Activities(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Hi"))

	type P1 struct {
		Q string `json:"q"`
	}
	type P2 struct {
		N int `json:"n"`
	}

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

func TestTemporalAgent_WithPassthroughRejected(t *testing.T) {
	type P struct{}

	model := core.NewTestModel(core.TextResponse("Hi"))
	tool1 := core.FuncTool[P]("fast_tool", "Fast",
		func(_ context.Context, _ P) (string, error) { return "fast", nil })
	tool2 := core.FuncTool[P]("slow_tool", "Slow",
		func(_ context.Context, _ P) (string, error) { return "slow", nil })

	agent := core.NewAgent[string](model, core.WithTools[string](tool1, tool2))

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when passthrough tools are configured")
		}
		if !strings.Contains(r.(error).Error(), "WithToolPassthrough") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()

	_ = NewTemporalAgent(agent,
		WithName("passthrough-test"),
		WithToolPassthrough("fast_tool"),
	)
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

func TestTemporalAgent_WithVersion(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Hi"))
	agent := core.NewAgent[string](model)
	ta := NewTemporalAgent(agent, WithName("versioned"), WithVersion("2026_03"))

	if ta.Name() != "versioned" {
		t.Fatalf("expected logical name %q, got %q", "versioned", ta.Name())
	}
	if ta.Version() != "2026_03" {
		t.Fatalf("expected version %q, got %q", "2026_03", ta.Version())
	}
	if ta.RegistrationName() != "versioned__v__2026_03" {
		t.Fatalf("unexpected registration name %q", ta.RegistrationName())
	}
	if ta.WorkflowName() != "agent__versioned__v__2026_03__workflow" {
		t.Fatalf("unexpected workflow name %q", ta.WorkflowName())
	}
	if _, ok := ta.Activities()["agent__versioned__v__2026_03__model_request"]; !ok {
		t.Fatal("expected versioned model activity name")
	}
}

func TestRegisterAll(t *testing.T) {
	worker := &recordingWorker{}

	agent1 := NewTemporalAgent(core.NewAgent[string](core.NewTestModel(core.TextResponse("one"))), WithName("one"))
	agent2 := NewTemporalAgent(core.NewAgent[string](core.NewTestModel(core.TextResponse("two"))), WithName("two"), WithVersion("v2"))

	if err := RegisterAll(worker, agent1, agent2); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if len(worker.workflowNames) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(worker.workflowNames))
	}
	if worker.workflowNames[0] != agent1.WorkflowName() || worker.workflowNames[1] != agent2.WorkflowName() {
		t.Fatalf("unexpected workflow registrations: %v", worker.workflowNames)
	}
	if len(worker.activityNames) == 0 {
		t.Fatal("expected activities to be registered")
	}
}

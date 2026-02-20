package deep

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestPlanningTool_CreatePlan(t *testing.T) {
	tool := PlanningTool()

	argsJSON := `{
		"command": "create",
		"tasks": [
			{"id": "1", "description": "First task", "status": "pending"},
			{"id": "2", "description": "Second task", "status": "pending"}
		]
	}`

	result, err := tool.Handler(context.Background(), &core.RunContext{}, argsJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if m["status"] != "created" {
		t.Errorf("expected status 'created', got %v", m["status"])
	}
	if m["tasks"] != 2 {
		t.Errorf("expected 2 tasks, got %v", m["tasks"])
	}
}

func TestPlanningTool_UpdateTask(t *testing.T) {
	tool := PlanningTool()

	// Create plan first.
	createArgs := `{
		"command": "create",
		"tasks": [
			{"id": "t1", "description": "Task one", "status": "pending"},
			{"id": "t2", "description": "Task two", "status": "pending"}
		]
	}`
	_, err := tool.Handler(context.Background(), &core.RunContext{}, createArgs)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Update task status.
	updateArgs := `{"command": "update", "task_id": "t1", "status": "in_progress", "notes": "Working on it"}`
	result, err := tool.Handler(context.Background(), &core.RunContext{}, updateArgs)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if m["status"] != "updated" {
		t.Errorf("expected status 'updated', got %v", m["status"])
	}
	task, ok := m["task"].(PlanTask)
	if !ok {
		t.Fatalf("expected PlanTask, got %T", m["task"])
	}
	if task.Status != "in_progress" {
		t.Errorf("expected task status 'in_progress', got %q", task.Status)
	}
	if task.Notes != "Working on it" {
		t.Errorf("expected notes 'Working on it', got %q", task.Notes)
	}
}

func TestPlanningTool_GetPlan(t *testing.T) {
	tool := PlanningTool()

	// Create plan.
	createArgs := `{
		"command": "create",
		"tasks": [
			{"id": "x1", "description": "Do X", "status": "pending"},
			{"id": "x2", "description": "Do Y", "status": "completed"}
		]
	}`
	_, err := tool.Handler(context.Background(), &core.RunContext{}, createArgs)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Get plan.
	getArgs := `{"command": "get"}`
	result, err := tool.Handler(context.Background(), &core.RunContext{}, getArgs)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	raw, ok := result.(json.RawMessage)
	if !ok {
		t.Fatalf("expected json.RawMessage, got %T", result)
	}

	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if len(plan.Tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(plan.Tasks))
	}
}

func TestPlanningTool_UpdateNotFound(t *testing.T) {
	tool := PlanningTool()

	// Create plan.
	_, err := tool.Handler(context.Background(), &core.RunContext{}, `{
		"command": "create",
		"tasks": [{"id": "a", "description": "A", "status": "pending"}]
	}`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Try to update non-existent task.
	_, err = tool.Handler(context.Background(), &core.RunContext{}, `{"command": "update", "task_id": "z", "status": "done"}`)
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestPlanningTool_UnknownCommand(t *testing.T) {
	tool := PlanningTool()
	_, err := tool.Handler(context.Background(), &core.RunContext{}, `{"command": "delete"}`)
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestPlanningTool_Integration(t *testing.T) {
	// Simulate: model creates a plan, marks task in_progress, completes it.
	tool := PlanningTool()

	model := core.NewTestModel(
		core.ToolCallResponseWithID("planning", `{
			"command": "create",
			"tasks": [
				{"id": "step1", "description": "Research topic", "status": "pending"},
				{"id": "step2", "description": "Write draft", "status": "pending"}
			]
		}`, "tc1"),
		core.ToolCallResponseWithID("planning", `{"command": "update", "task_id": "step1", "status": "completed"}`, "tc2"),
		core.TextResponse("Done with planning and execution."),
	)

	agent := core.NewAgent[string](model, core.WithTools[string](tool))
	result, err := agent.Run(context.Background(), "Do the task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Done with planning and execution." {
		t.Errorf("unexpected output: %s", result.Output)
	}
}

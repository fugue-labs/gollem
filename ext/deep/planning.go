package deep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/fugue-labs/gollem/core"
)

// Plan represents the agent's current plan.
type Plan struct {
	Tasks []PlanTask `json:"tasks"`
}

// PlanTask represents a single task in the plan.
type PlanTask struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"` // "pending", "in_progress", "completed", "blocked"
	Notes       string `json:"notes,omitempty"`
}

// planCommand is the input schema for the planning tool.
type planCommand struct {
	Command string     `json:"command" jsonschema:"description=The command to execute: create, update, or get"`
	Tasks   []PlanTask `json:"tasks,omitempty" jsonschema:"description=Tasks for create command"`
	TaskID  string     `json:"task_id,omitempty" jsonschema:"description=Task ID for update command"`
	Status  string     `json:"status,omitempty" jsonschema:"description=New status for update command"`
	Notes   string     `json:"notes,omitempty" jsonschema:"description=Notes for update command"`
}

// planState holds the current plan, shared across tool calls within a run.
type planState struct {
	mu   sync.Mutex
	plan Plan
}

// PlanningTool creates a tool that maintains a persistent todo list.
// The model uses this tool to plan, track progress, and manage tasks.
func PlanningTool() core.Tool {
	state := &planState{}

	return core.FuncTool[planCommand](
		"planning",
		"Manage a persistent todo list. Commands: 'create' (create a new plan with tasks), 'update' (update a task's status or notes), 'get' (retrieve the current plan).",
		func(_ context.Context, cmd planCommand) (any, error) {
			return executePlanCommand(state, cmd)
		},
	)
}

func executePlanCommand(state *planState, cmd planCommand) (any, error) {
	state.mu.Lock()
	defer state.mu.Unlock()

	switch cmd.Command {
	case "create":
		if len(cmd.Tasks) == 0 {
			return nil, errors.New("create command requires at least one task")
		}
		state.plan = Plan{Tasks: cmd.Tasks}
		return map[string]any{
			"status":  "created",
			"tasks":   len(state.plan.Tasks),
			"plan_id": "plan_1",
		}, nil

	case "update":
		if cmd.TaskID == "" {
			return nil, errors.New("update command requires task_id")
		}
		for i := range state.plan.Tasks {
			if state.plan.Tasks[i].ID == cmd.TaskID {
				if cmd.Status != "" {
					state.plan.Tasks[i].Status = cmd.Status
				}
				if cmd.Notes != "" {
					state.plan.Tasks[i].Notes = cmd.Notes
				}
				return map[string]any{
					"status": "updated",
					"task":   state.plan.Tasks[i],
				}, nil
			}
		}
		return nil, fmt.Errorf("task %q not found", cmd.TaskID)

	case "get":
		data, err := json.Marshal(state.plan)
		if err != nil {
			return nil, fmt.Errorf("marshaling plan: %w", err)
		}
		return json.RawMessage(data), nil

	default:
		return nil, fmt.Errorf("unknown command %q (use create, update, or get)", cmd.Command)
	}
}

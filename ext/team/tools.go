package team

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fugue-labs/gollem/core"
)

// --- Tool parameter types ---

type spawnParams struct {
	Name string `json:"name" jsonschema:"description=A short descriptive name for the teammate (e.g. 'researcher' or 'test-runner')"`
	Task string `json:"task" jsonschema:"description=The initial task for this teammate. Include all necessary context — the teammate starts with a fresh context window."`
}

type sendMessageParams struct {
	To      string `json:"to" jsonschema:"description=Name of the recipient teammate"`
	Content string `json:"content" jsonschema:"description=Message content"`
	Summary string `json:"summary,omitempty" jsonschema:"description=Brief summary of the message (5-10 words)"`
}

type shutdownParams struct {
	Name   string `json:"name" jsonschema:"description=Name of the teammate to shut down"`
	Reason string `json:"reason,omitempty" jsonschema:"description=Reason for shutdown"`
}

type taskCreateParams struct {
	Subject     string `json:"subject" jsonschema:"description=Brief task title in imperative form (e.g. 'Fix auth bug')"`
	Description string `json:"description" jsonschema:"description=Detailed description of what needs to be done"`
}

type taskUpdateParams struct {
	ID           string         `json:"id" jsonschema:"description=Task ID to update"`
	Status       string         `json:"status,omitempty" jsonschema:"description=New status: pending, in_progress, completed, or blocked"`
	Owner        string         `json:"owner,omitempty" jsonschema:"description=Assign to a teammate by name"`
	Subject      string         `json:"subject,omitempty" jsonschema:"description=New subject"`
	Description  string         `json:"description,omitempty" jsonschema:"description=New description"`
	AddBlocks    []string       `json:"add_blocks,omitempty" jsonschema:"description=Task IDs that this task blocks"`
	AddBlockedBy []string       `json:"add_blocked_by,omitempty" jsonschema:"description=Task IDs that block this task"`
	Metadata     map[string]any `json:"metadata,omitempty" jsonschema:"description=Key-value metadata to merge"`
}

type taskGetParams struct {
	ID string `json:"id" jsonschema:"description=Task ID to retrieve"`
}

// LeaderTools returns tools available only to the team leader.
func LeaderTools(t *Team) []core.Tool {
	shared := SharedTools(t, t.leaderSenderName())
	leader := []core.Tool{
		spawnTool(t),
		shutdownTool(t),
	}
	return append(leader, shared...)
}

// WorkerTools returns tools available to a worker teammate.
func WorkerTools(t *Team, tm *Teammate) []core.Tool {
	return SharedTools(t, tm.name)
}

// SharedTools returns tools available to both leaders and workers.
func SharedTools(t *Team, selfName string) []core.Tool {
	return []core.Tool{
		sendMessageTool(t, selfName),
		taskCreateTool(t),
		taskUpdateTool(t),
		taskListTool(t),
		taskGetTool(t),
	}
}

func spawnTool(t *Team) core.Tool {
	return core.FuncTool[spawnParams](
		"spawn_teammate",
		"Spawn a new teammate agent that runs concurrently as a goroutine. "+
			"The teammate gets the same coding tools (bash, view, edit, write, grep, glob, ls) "+
			"plus team coordination tools (send_message, task_*). "+
			"Use for: parallel exploration, divide-and-conquer implementation, "+
			"background test running, or speculative parallel attempts. "+
			"Each teammate has a fresh context window.",
		func(ctx context.Context, params spawnParams) (any, error) {
			if params.Name == "" {
				return nil, &core.ModelRetryError{Message: "name must not be empty"}
			}
			if params.Task == "" {
				return nil, &core.ModelRetryError{Message: "task must not be empty"}
			}

			tm, err := t.SpawnTeammate(ctx, params.Name, params.Task)
			if err != nil {
				return nil, fmt.Errorf("failed to spawn teammate: %w", err)
			}

			return map[string]any{
				"status": "spawned",
				"name":   tm.Name(),
				"message": fmt.Sprintf("Teammate %q spawned and working on task. "+
					"They will send you a message when done or when they need help.", tm.Name()),
			}, nil
		},
	)
}

func shutdownTool(t *Team) core.Tool {
	return core.FuncTool[shutdownParams](
		"shutdown_teammate",
		"Request a teammate to gracefully shut down. Use when a teammate's work is complete or no longer needed.",
		func(_ context.Context, params shutdownParams) (any, error) {
			if params.Name == "" {
				return nil, &core.ModelRetryError{Message: "name must not be empty"}
			}

			reason := params.Reason
			if reason == "" {
				reason = "work complete"
			}

			msg, err := t.requestShutdown(params.Name, t.leaderSenderName(), reason, "")
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"status":         "shutdown_requested",
				"name":           params.Name,
				"requested_by":   msg.From,
				"shutdown_id":    msg.ID,
				"correlation_id": msg.CorrelationID,
				"message":        fmt.Sprintf("Shutdown request sent to %q", params.Name),
			}, nil
		},
	)
}

func sendMessageTool(t *Team, selfName string) core.Tool {
	return core.FuncTool[sendMessageParams](
		"send_message",
		"Send a message to a teammate. Messages are delivered to their mailbox "+
			"and processed between model turns. Use for coordination, status updates, "+
			"sharing findings, or requesting help.",
		func(_ context.Context, params sendMessageParams) (any, error) {
			if params.To == "" {
				return nil, &core.ModelRetryError{Message: "recipient name must not be empty"}
			}
			if params.Content == "" {
				return nil, &core.ModelRetryError{Message: "message content must not be empty"}
			}

			mb := t.getMailbox(params.To)
			if mb == nil {
				return nil, fmt.Errorf("teammate %q not found", params.To)
			}

			summary := params.Summary
			if summary == "" && len(params.Content) > 50 {
				summary = params.Content[:50] + "..."
			} else if summary == "" {
				summary = params.Content
			}

			msg := newMessage(selfName, params.To, MessageText, params.Content, summary, "")
			if err := mb.TrySend(msg); err != nil {
				return nil, fmt.Errorf("failed to deliver message to %q: %w", params.To, err)
			}

			// Wake the recipient if idle.
			if tm := t.GetTeammate(params.To); tm != nil {
				tm.Wake()
			}

			if t.eventBus != nil {
				core.PublishAsync(t.eventBus, MessageSentEvent{
					TeamName:      t.name,
					MessageID:     msg.ID,
					CorrelationID: msg.CorrelationID,
					From:          selfName,
					To:            params.To,
					Type:          msg.Type,
					Summary:       summary,
				})
			}

			return map[string]any{
				"status":         "sent",
				"to":             params.To,
				"message_id":     msg.ID,
				"correlation_id": msg.CorrelationID,
				"message":        fmt.Sprintf("Message sent to %q", params.To),
			}, nil
		},
	)
}

func taskCreateTool(t *Team) core.Tool {
	return core.FuncTool[taskCreateParams](
		"task_create",
		"Create a new task on the shared task board. Tasks are visible to all teammates.",
		func(_ context.Context, params taskCreateParams) (any, error) {
			if params.Subject == "" {
				return nil, &core.ModelRetryError{Message: "subject must not be empty"}
			}

			id := t.taskBoard.Create(params.Subject, params.Description)

			if t.eventBus != nil {
				core.PublishAsync(t.eventBus, TaskCreatedEvent{
					TeamName: t.name,
					TaskID:   id,
					Subject:  params.Subject,
				})
			}

			return map[string]any{
				"status":  "created",
				"task_id": id,
			}, nil
		},
	)
}

func taskUpdateTool(t *Team) core.Tool {
	return core.FuncTool[taskUpdateParams](
		"task_update",
		"Update a task on the shared task board. Can change status, owner, subject, "+
			"description, blocks, and blockedBy.",
		func(_ context.Context, params taskUpdateParams) (any, error) {
			if params.ID == "" {
				return nil, &core.ModelRetryError{Message: "task ID must not be empty"}
			}

			var opts []TaskUpdateOption
			if params.Status != "" {
				switch TaskStatus(params.Status) {
				case TaskPending, TaskInProgress, TaskCompleted, TaskBlocked:
					opts = append(opts, WithStatus(TaskStatus(params.Status)))
				default:
					return nil, &core.ModelRetryError{
						Message: fmt.Sprintf("invalid status %q: must be pending, in_progress, completed, or blocked", params.Status),
					}
				}
			}
			if params.Owner != "" {
				opts = append(opts, WithOwner(params.Owner))
			}
			if params.Subject != "" {
				opts = append(opts, WithSubject(params.Subject))
			}
			if params.Description != "" {
				opts = append(opts, WithDescription(params.Description))
			}
			if len(params.AddBlocks) > 0 {
				opts = append(opts, WithAddBlocks(params.AddBlocks...))
			}
			if len(params.AddBlockedBy) > 0 {
				opts = append(opts, WithAddBlockedBy(params.AddBlockedBy...))
			}
			if len(params.Metadata) > 0 {
				opts = append(opts, WithMetadata(params.Metadata))
			}

			if err := t.taskBoard.Update(params.ID, opts...); err != nil {
				return nil, err
			}

			// Publish completion event.
			if params.Status == string(TaskCompleted) && t.eventBus != nil {
				task, _ := t.taskBoard.Get(params.ID)
				owner := ""
				if task != nil {
					owner = task.Owner
				}
				core.PublishAsync(t.eventBus, TaskCompletedEvent{
					TeamName: t.name,
					TaskID:   params.ID,
					Owner:    owner,
				})
			}

			return map[string]any{
				"status":  "updated",
				"task_id": params.ID,
			}, nil
		},
	)
}

func taskListTool(t *Team) core.Tool {
	return core.FuncTool[struct{}](
		"task_list",
		"List all tasks on the shared task board with their status and owner.",
		func(_ context.Context, _ struct{}) (any, error) {
			tasks := t.taskBoard.List()
			// Return as JSON-friendly structure.
			result := make([]map[string]any, len(tasks))
			for i, task := range tasks {
				entry := map[string]any{
					"id":      task.ID,
					"subject": task.Subject,
					"status":  task.Status,
				}
				if task.Owner != "" {
					entry["owner"] = task.Owner
				}
				if len(task.BlockedBy) > 0 {
					// Filter to only incomplete blockers.
					entry["blocked_by"] = task.BlockedBy
				}
				result[i] = entry
			}
			data, _ := json.Marshal(result)
			return json.RawMessage(data), nil
		},
	)
}

func taskGetTool(t *Team) core.Tool {
	return core.FuncTool[taskGetParams](
		"task_get",
		"Get full details of a specific task by ID.",
		func(_ context.Context, params taskGetParams) (any, error) {
			if params.ID == "" {
				return nil, &core.ModelRetryError{Message: "task ID must not be empty"}
			}
			task, err := t.taskBoard.Get(params.ID)
			if err != nil {
				return nil, err
			}
			data, _ := json.Marshal(task)
			return json.RawMessage(data), nil
		},
	)
}

package team

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestSpawnTool(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "tool-test",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tool := spawnTool(tm)
	if tool.Definition.Name != "spawn_teammate" {
		t.Errorf("expected tool name 'spawn_teammate', got %q", tool.Definition.Name)
	}

	// Call the tool via the agent's handler.
	result, err := tool.Handler(ctx, nil, `{"name":"helper","task":"do something"}`)
	if err != nil {
		t.Fatal(err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if resultMap["status"] != "spawned" {
		t.Errorf("expected status 'spawned', got %v", resultMap["status"])
	}
	if resultMap["name"] != "helper" {
		t.Errorf("expected name 'helper', got %v", resultMap["name"])
	}

	// Wait for worker to go idle and clean up.
	w := tm.GetTeammate("helper")
	if w == nil {
		t.Fatal("helper not found")
	}
	waitForState(t, w, TeammateIdle, 3*time.Second)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestSpawnTool_EmptyName(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{Name: "test", Model: model})

	tool := spawnTool(tm)
	_, err := tool.Handler(context.Background(), nil, `{"name":"","task":"x"}`)
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestSpawnTool_EmptyTask(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{Name: "test", Model: model})

	tool := spawnTool(tm)
	_, err := tool.Handler(context.Background(), nil, `{"name":"x","task":""}`)
	if err == nil {
		t.Error("expected error for empty task")
	}
}

func TestShutdownTool(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "test",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := tm.SpawnTeammate(ctx, "worker", "task")
	if err != nil {
		t.Fatal(err)
	}
	w := tm.GetTeammate("worker")
	waitForState(t, w, TeammateIdle, 3*time.Second)

	tool := shutdownTool(tm)
	result, err := tool.Handler(ctx, nil, `{"name":"worker","reason":"all done"}`)
	if err != nil {
		t.Fatal(err)
	}
	resultMap := result.(map[string]any)
	if resultMap["status"] != "shutdown_requested" {
		t.Errorf("expected status 'shutdown_requested', got %v", resultMap["status"])
	}
	if resultMap["requested_by"] != "leader" {
		t.Errorf("expected requested_by 'leader', got %v", resultMap["requested_by"])
	}
	if resultMap["shutdown_id"] == "" {
		t.Error("expected non-empty shutdown_id")
	}
	if resultMap["correlation_id"] == "" {
		t.Error("expected non-empty correlation_id")
	}

	// Worker should stop.
	deadline := time.After(3 * time.Second)
	for w.State() != TeammateStopped {
		select {
		case <-deadline:
			t.Fatalf("worker did not stop")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestShutdownTool_NotFound(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{Name: "test", Model: model})

	tool := shutdownTool(tm)
	_, err := tool.Handler(context.Background(), nil, `{"name":"nobody"}`)
	if err == nil {
		t.Error("expected error for missing teammate")
	}
}

func TestShutdownTool_UsesConfiguredLeaderName(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "test",
		Leader: "lead",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := tm.SpawnTeammate(ctx, "worker", "task")
	if err != nil {
		t.Fatal(err)
	}
	w := tm.GetTeammate("worker")
	waitForState(t, w, TeammateIdle, 3*time.Second)

	tool := shutdownTool(tm)
	result, err := tool.Handler(ctx, nil, `{"name":"worker","reason":"all done"}`)
	if err != nil {
		t.Fatal(err)
	}
	resultMap := result.(map[string]any)
	if resultMap["requested_by"] != "lead" {
		t.Errorf("expected requested_by 'lead', got %v", resultMap["requested_by"])
	}
}

func TestSendMessageTool(t *testing.T) {
	bus := core.NewEventBus()

	var mu sync.Mutex
	var sentEvents []MessageSentEvent
	core.Subscribe(bus, func(e MessageSentEvent) {
		mu.Lock()
		sentEvents = append(sentEvents, e)
		mu.Unlock()
	})

	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:     "test",
		Leader:   "leader",
		Model:    model,
		EventBus: bus,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := tm.SpawnTeammate(ctx, "bob", "task")
	if err != nil {
		t.Fatal(err)
	}
	bob := tm.GetTeammate("bob")
	waitForState(t, bob, TeammateIdle, 3*time.Second)

	tool := sendMessageTool(tm, "alice")
	result, err := tool.Handler(ctx, nil, `{"to":"bob","content":"check handler.go","summary":"review request"}`)
	if err != nil {
		t.Fatal(err)
	}
	resultMap := result.(map[string]any)
	if resultMap["status"] != "sent" {
		t.Errorf("expected status 'sent', got %v", resultMap["status"])
	}
	if resultMap["message_id"] == "" {
		t.Error("expected non-empty message_id")
	}
	if resultMap["correlation_id"] == "" {
		t.Error("expected non-empty correlation_id")
	}

	// The send_message tool also wakes bob, which causes the teammate loop
	// to drain the mailbox and run. So instead of checking the mailbox directly
	// (it gets drained by the run loop), we verify the message was sent via
	// the event and that bob woke up and ran again.
	time.Sleep(50 * time.Millisecond)
	waitForState(t, bob, TeammateIdle, 3*time.Second)

	// Verify event was fired.
	mu.Lock()
	defer mu.Unlock()
	if len(sentEvents) != 1 {
		t.Fatalf("expected 1 sent event, got %d", len(sentEvents))
	}
	if sentEvents[0].From != "alice" {
		t.Errorf("expected from 'alice', got %q", sentEvents[0].From)
	}
	if sentEvents[0].To != "bob" {
		t.Errorf("expected to 'bob', got %q", sentEvents[0].To)
	}
	if sentEvents[0].Summary != "review request" {
		t.Errorf("expected summary 'review request', got %q", sentEvents[0].Summary)
	}
	if sentEvents[0].MessageID == "" {
		t.Error("expected sent event to include message ID")
	}
	if sentEvents[0].CorrelationID == "" {
		t.Error("expected sent event to include correlation ID")
	}
	if sentEvents[0].Type != MessageText {
		t.Errorf("expected message type %q, got %q", MessageText, sentEvents[0].Type)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestSendMessageTool_NotFound(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	tool := sendMessageTool(tm, "alice")
	_, err := tool.Handler(context.Background(), nil, `{"to":"nobody","content":"hello"}`)
	if err == nil {
		t.Error("expected error for missing recipient")
	}
}

func TestSendMessageTool_MailboxFullReturnsError(t *testing.T) {
	tm := NewTeam(TeamConfig{
		Name:        "test",
		Leader:      "lead",
		Model:       core.NewTestModel(core.TextResponse("done")),
		MailboxSize: 1,
	})
	_ = tm.RegisterLeader("lead")

	leaderMB := tm.getMailbox("lead")
	if leaderMB == nil {
		t.Fatal("expected leader mailbox")
	}
	leaderMB.Send(Message{From: "existing", To: "lead", Type: MessageText, Content: "already full"})

	tool := sendMessageTool(tm, "alice")
	_, err := tool.Handler(context.Background(), nil, `{"to":"lead","content":"overflow"}`)
	if err == nil {
		t.Fatal("expected mailbox full error")
	}
}

func TestSendMessageTool_EmptyContent(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	tool := sendMessageTool(tm, "alice")
	_, err := tool.Handler(context.Background(), nil, `{"to":"bob","content":""}`)
	if err == nil {
		t.Error("expected error for empty content")
	}
}

func TestSendMessageTool_AutoSummary(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "test",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := tm.SpawnTeammate(ctx, "bob", "task")
	if err != nil {
		t.Fatal(err)
	}
	bob := tm.GetTeammate("bob")
	waitForState(t, bob, TeammateIdle, 3*time.Second)

	// Send with long content but no explicit summary.
	longContent := "This is a very long message that exceeds fifty characters and should be auto-truncated for the summary field."
	tool := sendMessageTool(tm, "alice")
	_, err = tool.Handler(ctx, nil, `{"to":"bob","content":"`+longContent+`"}`)
	if err != nil {
		t.Fatal(err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestTaskCreateTool(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	tool := taskCreateTool(tm)

	result, err := tool.Handler(context.Background(), nil, `{"subject":"Fix bug","description":"In auth module"}`)
	if err != nil {
		t.Fatal(err)
	}
	resultMap := result.(map[string]any)
	if resultMap["status"] != "created" {
		t.Errorf("expected status 'created', got %v", resultMap["status"])
	}
	taskID := resultMap["task_id"].(string)
	if taskID == "" {
		t.Error("expected non-empty task_id")
	}

	// Verify task exists on board.
	task, err := tm.TaskBoard().Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Subject != "Fix bug" {
		t.Errorf("expected subject 'Fix bug', got %q", task.Subject)
	}
}

func TestTaskCreateTool_EmptySubject(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	tool := taskCreateTool(tm)
	_, err := tool.Handler(context.Background(), nil, `{"subject":"","description":"x"}`)
	if err == nil {
		t.Error("expected error for empty subject")
	}
}

func TestTaskUpdateTool(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	tb := tm.TaskBoard()
	id := tb.Create("Task", "Desc")

	tool := taskUpdateTool(tm)
	_, err := tool.Handler(context.Background(), nil, `{"id":"`+id+`","status":"in_progress","owner":"worker-1"}`)
	if err != nil {
		t.Fatal(err)
	}

	task, _ := tb.Get(id)
	if task.Status != TaskInProgress {
		t.Errorf("expected in_progress, got %v", task.Status)
	}
	if task.Owner != "worker-1" {
		t.Errorf("expected owner 'worker-1', got %q", task.Owner)
	}
}

func TestTaskUpdateTool_NotFound(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	tool := taskUpdateTool(tm)
	_, err := tool.Handler(context.Background(), nil, `{"id":"999","status":"completed"}`)
	if err == nil {
		t.Error("expected error for missing task")
	}
}

func TestTaskListTool(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	tb := tm.TaskBoard()
	tb.Create("Task A", "")
	tb.Create("Task B", "")

	tool := taskListTool(tm)
	result, err := tool.Handler(context.Background(), nil, `{}`)
	if err != nil {
		t.Fatal(err)
	}

	// Result should be json.RawMessage.
	raw, ok := result.(json.RawMessage)
	if !ok {
		t.Fatalf("expected json.RawMessage, got %T", result)
	}

	var tasks []map[string]any
	if err := json.Unmarshal(raw, &tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestTaskGetTool(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	tb := tm.TaskBoard()
	id := tb.Create("My Task", "Details here")

	tool := taskGetTool(tm)
	result, err := tool.Handler(context.Background(), nil, `{"id":"`+id+`"}`)
	if err != nil {
		t.Fatal(err)
	}

	raw := result.(json.RawMessage)
	var task Task
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatal(err)
	}
	if task.Subject != "My Task" {
		t.Errorf("expected 'My Task', got %q", task.Subject)
	}
	if task.Description != "Details here" {
		t.Errorf("expected 'Details here', got %q", task.Description)
	}
}

func TestTaskGetTool_NotFound(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	tool := taskGetTool(tm)
	_, err := tool.Handler(context.Background(), nil, `{"id":"999"}`)
	if err == nil {
		t.Error("expected error for missing task")
	}
}

func TestTaskGetTool_EmptyID(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	tool := taskGetTool(tm)
	_, err := tool.Handler(context.Background(), nil, `{"id":""}`)
	if err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestLeaderTools_Count(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	tools := LeaderTools(tm)

	// Leader gets: spawn_teammate, shutdown_teammate + shared (send_message, task_create, task_update, task_list, task_get) = 7
	if len(tools) != 7 {
		t.Errorf("expected 7 leader tools, got %d", len(tools))
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Definition.Name] = true
	}
	expected := []string{
		"spawn_teammate", "shutdown_teammate", "send_message",
		"task_create", "task_update", "task_list", "task_get",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestWorkerTools_Count(t *testing.T) {
	tm := NewTeam(TeamConfig{Name: "test", Model: core.NewTestModel(core.TextResponse("done"))})
	dummy := &Teammate{name: "worker"}
	tools := WorkerTools(tm, dummy)

	// Worker gets: shared only (send_message, task_create, task_update, task_list, task_get) = 5
	if len(tools) != 5 {
		t.Errorf("expected 5 worker tools, got %d", len(tools))
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Definition.Name] = true
	}
	// Workers should NOT have spawn_teammate, shutdown_teammate, or ask_user.
	if names["spawn_teammate"] {
		t.Error("workers should not have spawn_teammate")
	}
	if names["shutdown_teammate"] {
		t.Error("workers should not have shutdown_teammate")
	}
	if names["ask_user"] {
		t.Error("workers should not have ask_user")
	}
}

// TestTaskUpdateTool_CompletionEvent tests that completing a task emits an event.
func TestTaskUpdateTool_CompletionEvent(t *testing.T) {
	bus := core.NewEventBus()

	var mu sync.Mutex
	var completedEvents []TaskCompletedEvent
	core.Subscribe(bus, func(e TaskCompletedEvent) {
		mu.Lock()
		completedEvents = append(completedEvents, e)
		mu.Unlock()
	})

	tm := NewTeam(TeamConfig{
		Name:     "test",
		Model:    core.NewTestModel(core.TextResponse("done")),
		EventBus: bus,
	})
	tb := tm.TaskBoard()
	id := tb.Create("Task", "")
	tb.Update(id, WithOwner("worker"))

	tool := taskUpdateTool(tm)
	_, err := tool.Handler(context.Background(), nil, `{"id":"`+id+`","status":"completed"}`)
	if err != nil {
		t.Fatal(err)
	}

	// Give async event time.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(completedEvents) != 1 {
		t.Fatalf("expected 1 completion event, got %d", len(completedEvents))
	}
	if completedEvents[0].TaskID != id {
		t.Errorf("expected task ID %q, got %q", id, completedEvents[0].TaskID)
	}
	if completedEvents[0].Owner != "worker" {
		t.Errorf("expected owner 'worker', got %q", completedEvents[0].Owner)
	}
}

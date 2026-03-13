package team

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/modelutil"
)

// --- E2E / Smoke Tests ---

// TestE2E_FullTeamLifecycle exercises the full lifecycle:
// leader spawns 2 workers → workers complete → leader shuts them down.
func TestE2E_FullTeamLifecycle(t *testing.T) {
	bus := core.NewEventBus()

	var mu sync.Mutex
	var events []string
	core.Subscribe(bus, func(e TeammateSpawnedEvent) {
		mu.Lock()
		events = append(events, "spawned:"+e.TeammateName)
		mu.Unlock()
	})
	core.Subscribe(bus, func(e TeammateIdleEvent) {
		mu.Lock()
		events = append(events, "idle:"+e.TeammateName)
		mu.Unlock()
	})
	core.Subscribe(bus, func(e TeammateTerminatedEvent) {
		mu.Lock()
		events = append(events, "terminated:"+e.TeammateName)
		mu.Unlock()
	})

	model := core.NewTestModel(core.TextResponse("task complete"))
	tm := NewTeam(TeamConfig{
		Name:     "e2e-team",
		Leader:   "leader",
		Model:    model,
		EventBus: bus,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Spawn 2 workers.
	w1, err := tm.SpawnTeammate(ctx, "worker-1", "implement feature A")
	if err != nil {
		t.Fatal(err)
	}
	w2, err := tm.SpawnTeammate(ctx, "worker-2", "implement feature B")
	if err != nil {
		t.Fatal(err)
	}

	// Wait for both to go idle.
	waitForState(t, w1, TeammateIdle, 3*time.Second)
	waitForState(t, w2, TeammateIdle, 3*time.Second)

	// Verify members.
	members := tm.Members()
	if len(members) != 2 {
		t.Errorf("expected 2 members, got %d", len(members))
	}

	// Shutdown.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := tm.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}

	// Verify both stopped.
	if w1.State() != TeammateStopped {
		t.Errorf("worker-1 state = %v, want stopped", w1.State())
	}
	if w2.State() != TeammateStopped {
		t.Errorf("worker-2 state = %v, want stopped", w2.State())
	}

	// Verify events fired (give async events time to settle).
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	hasSpawned1, hasSpawned2, hasTerminated1, hasTerminated2 := false, false, false, false
	for _, e := range events {
		switch e {
		case "spawned:worker-1":
			hasSpawned1 = true
		case "spawned:worker-2":
			hasSpawned2 = true
		case "terminated:worker-1":
			hasTerminated1 = true
		case "terminated:worker-2":
			hasTerminated2 = true
		}
	}
	if !hasSpawned1 || !hasSpawned2 {
		t.Errorf("missing spawned events: w1=%v w2=%v", hasSpawned1, hasSpawned2)
	}
	if !hasTerminated1 || !hasTerminated2 {
		t.Errorf("missing terminated events: w1=%v w2=%v", hasTerminated1, hasTerminated2)
	}
}

// TestE2E_WorkerToWorkerMessaging tests that workers can message each other.
func TestE2E_WorkerToWorkerMessaging(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "msg-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	w1, err := tm.SpawnTeammate(ctx, "alice", "task A")
	if err != nil {
		t.Fatal(err)
	}
	w2, err := tm.SpawnTeammate(ctx, "bob", "task B")
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, w1, TeammateIdle, 3*time.Second)
	waitForState(t, w2, TeammateIdle, 3*time.Second)

	// Send message from outside (simulating a tool call) to bob's mailbox.
	bobMB := tm.getMailbox("bob")
	if bobMB == nil {
		t.Fatal("bob has no mailbox")
	}
	bobMB.Send(Message{
		From:    "alice",
		To:      "bob",
		Type:    MessageText,
		Content: "Hey bob, I found a bug in handler.go",
	})
	w2.Wake()

	// Bob should wake up, run again, and go idle.
	time.Sleep(50 * time.Millisecond)
	waitForState(t, w2, TeammateIdle, 3*time.Second)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

// TestE2E_TaskBoardCoordination tests that tasks flow through the board correctly.
func TestE2E_TaskBoardCoordination(t *testing.T) {
	tb := NewTaskBoard()

	// Create dependency chain: task2 blocked by task1.
	id1 := tb.Create("Build API handler", "Implement GET /users endpoint")
	id2 := tb.Create("Write API tests", "Test the GET /users endpoint")
	tb.Update(id2, WithAddBlockedBy(id1))

	// task2 should not be available.
	avail := tb.Available()
	if len(avail) != 1 || avail[0].ID != id1 {
		t.Errorf("only task1 should be available, got %v", avail)
	}

	// Worker claims task1.
	if err := tb.Claim(id1, "worker-1"); err != nil {
		t.Fatal(err)
	}

	// No tasks available now (task1 claimed, task2 blocked).
	avail = tb.Available()
	if len(avail) != 0 {
		t.Errorf("expected 0 available, got %d", len(avail))
	}

	// Worker completes task1.
	tb.Update(id1, WithStatus(TaskCompleted))

	// task2 should now be unblocked and available.
	avail = tb.Available()
	if len(avail) != 1 || avail[0].ID != id2 {
		t.Errorf("task2 should be available now, got %v", avail)
	}

	// Another worker claims and completes task2.
	if err := tb.Claim(id2, "worker-2"); err != nil {
		t.Fatal(err)
	}
	tb.Update(id2, WithStatus(TaskCompleted))

	// All tasks completed.
	for _, task := range tb.List() {
		if task.Status != TaskCompleted {
			t.Errorf("task %s status = %v, want completed", task.ID, task.Status)
		}
	}
}

// TestE2E_ContextCancellation tests that context cancellation stops all teammates.
func TestE2E_ContextCancellation(t *testing.T) {
	// Model that responds slowly (but the test model returns immediately,
	// so the teammate will go idle, and context cancel will stop the idle wait).
	model := core.NewTestModel(core.TextResponse("working..."))
	tm := NewTeam(TeamConfig{
		Name:   "cancel-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	w1, err := tm.SpawnTeammate(ctx, "worker", "long task")
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, w1, TeammateIdle, 3*time.Second)

	// Cancel the context.
	cancel()

	// Worker should stop.
	deadline := time.After(3 * time.Second)
	for w1.State() != TeammateStopped {
		select {
		case <-deadline:
			t.Fatalf("worker did not stop after context cancel, state: %v", w1.State())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestE2E_LeaderMailbox tests that the leader can receive messages from workers.
func TestE2E_LeaderMailbox(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "leader-mb-team",
		Leader: "leader",
		Model:  model,
	})

	// Register the leader and get its middleware.
	_ = tm.RegisterLeader("leader")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Spawn a worker.
	w, err := tm.SpawnTeammate(ctx, "worker", "do stuff")
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, w, TeammateIdle, 3*time.Second)

	// Worker's completion should have sent a status update to the leader's mailbox.
	leaderMB := tm.getMailbox("leader")
	if leaderMB == nil {
		t.Fatal("leader has no mailbox after RegisterLeader")
	}

	// Give time for the async status message.
	time.Sleep(100 * time.Millisecond)
	msgs := leaderMB.DrainAll()
	if len(msgs) == 0 {
		t.Error("expected leader to receive a status update from worker")
	} else {
		found := false
		for _, msg := range msgs {
			if msg.From == "worker" && msg.Type == MessageStatusUpdate {
				found = true
			}
		}
		if !found {
			t.Error("expected a status_update message from worker")
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

// TestE2E_ShutdownViaMessage tests that a shutdown message stops a teammate.
func TestE2E_ShutdownViaMessage(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "shutdown-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	w, err := tm.SpawnTeammate(ctx, "worker", "task")
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, w, TeammateIdle, 3*time.Second)

	// Send shutdown message directly.
	w.mailbox.Send(Message{
		From:      "leader",
		To:        "worker",
		Type:      MessageShutdownRequest,
		Content:   "all done",
		Timestamp: time.Now(),
	})
	w.Wake()

	// Worker should stop.
	deadline := time.After(3 * time.Second)
	for w.State() != TeammateStopped {
		select {
		case <-deadline:
			t.Fatalf("worker did not stop after shutdown message, state: %v", w.State())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestE2E_InFlightShutdownMessageSurvivesMiddlewareDrain verifies that a
// shutdown message delivered during a running turn still stops the teammate
// after the current run, even when the middleware drains the mailbox entry
// before the run finishes.
func TestE2E_InFlightShutdownMessageSurvivesMiddlewareDrain(t *testing.T) {
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	waitTool := core.FuncTool[struct{}](
		"wait",
		"Wait until released",
		func(ctx context.Context, _ struct{}) (string, error) {
			select {
			case <-toolStarted:
			default:
				close(toolStarted)
			}
			select {
			case <-releaseTool:
				return "released", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	)

	model := core.NewTestModel(
		core.ToolCallResponseWithID("wait", `{}`, "call_wait"),
		core.TextResponse("final"),
	)
	tm := NewTeam(TeamConfig{
		Name:             "inflight-shutdown-team",
		Leader:           "leader",
		Model:            model,
		WorkerExtraTools: []core.Tool{waitTool},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	w, err := tm.SpawnTeammate(ctx, "worker", "task")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-toolStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for blocking tool to start")
	}

	w.mailbox.Send(Message{
		From:    "leader",
		To:      "worker",
		Type:    MessageShutdownRequest,
		Content: "all done",
	})
	w.Wake()

	close(releaseTool)

	deadline := time.After(3 * time.Second)
	for w.State() != TeammateStopped {
		select {
		case <-deadline:
			t.Fatalf("worker did not stop after in-flight shutdown message, state: %v", w.State())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestE2E_MultipleWakeCycles tests that a teammate can be woken multiple times.
func TestE2E_MultipleWakeCycles(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "wake-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	w, err := tm.SpawnTeammate(ctx, "worker", "initial task")
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, w, TeammateIdle, 3*time.Second)

	// Wake 3 more times.
	for range 3 {
		w.mailbox.Send(Message{
			From:    "leader",
			To:      "worker",
			Type:    MessageText,
			Content: "more work",
		})
		w.Wake()
		time.Sleep(50 * time.Millisecond)
		waitForState(t, w, TeammateIdle, 3*time.Second)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

// --- Personality Generation E2E Tests ---

// TestE2E_PersonalityGeneration tests that teammates receive dynamically
// generated system prompts when a PersonalityGenerator is configured.
func TestE2E_PersonalityGeneration(t *testing.T) {
	var genCalls atomic.Int32
	personalityGen := func(ctx context.Context, req modelutil.PersonalityRequest) (string, error) {
		genCalls.Add(1)
		return fmt.Sprintf("You are a specialist for: %s. Role: %s", req.Task, req.Role), nil
	}

	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:                 "personality-team",
		Leader:               "leader",
		Model:                model,
		PersonalityGenerator: personalityGen,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	w, err := tm.SpawnTeammate(ctx, "specialist", "implement OAuth2 flow")
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, w, TeammateIdle, 3*time.Second)

	// Personality generator should have been called once.
	if genCalls.Load() != 1 {
		t.Errorf("expected 1 personality generation call, got %d", genCalls.Load())
	}

	shutdownCtx2, shutdownCancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel2()
	tm.Shutdown(shutdownCtx2)
}

// TestE2E_PersonalityFallback tests that teammates fall back to the default
// system prompt when the personality generator returns an error.
func TestE2E_PersonalityFallback(t *testing.T) {
	personalityGen := func(ctx context.Context, req modelutil.PersonalityRequest) (string, error) {
		return "", fmt.Errorf("model unavailable")
	}

	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:                 "fallback-team",
		Leader:               "leader",
		Model:                model,
		PersonalityGenerator: personalityGen,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Should still spawn successfully despite personality gen failure.
	w, err := tm.SpawnTeammate(ctx, "worker", "some task")
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, w, TeammateIdle, 3*time.Second)

	shutdownCtx2, shutdownCancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel2()
	tm.Shutdown(shutdownCtx2)
}

// TestE2E_PersonalityWithExplicitOverride tests that WithTeammateSystemPrompt
// takes precedence over the personality generator.
func TestE2E_PersonalityWithExplicitOverride(t *testing.T) {
	var genCalls atomic.Int32
	personalityGen := func(ctx context.Context, req modelutil.PersonalityRequest) (string, error) {
		genCalls.Add(1)
		return "should not be used", nil
	}

	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:                 "override-team",
		Leader:               "leader",
		Model:                model,
		PersonalityGenerator: personalityGen,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Explicit system prompt should skip personality generation entirely.
	w, err := tm.SpawnTeammate(ctx, "worker", "task",
		WithTeammateSystemPrompt("Custom prompt override"))
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, w, TeammateIdle, 3*time.Second)

	// Generator should NOT have been called since explicit prompt was given.
	if genCalls.Load() != 0 {
		t.Errorf("expected 0 personality generation calls with explicit override, got %d", genCalls.Load())
	}

	shutdownCtx2, shutdownCancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel2()
	tm.Shutdown(shutdownCtx2)
}

// TestE2E_PersonalityMultipleTeammates tests that each teammate gets its
// own unique personality based on its task.
func TestE2E_PersonalityMultipleTeammates(t *testing.T) {
	var mu sync.Mutex
	generatedPrompts := make(map[string]string)
	personalityGen := func(ctx context.Context, req modelutil.PersonalityRequest) (string, error) {
		prompt := fmt.Sprintf("Specialist for: %s", req.Task)
		mu.Lock()
		generatedPrompts[req.Task] = prompt
		mu.Unlock()
		return prompt, nil
	}

	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:                 "multi-personality-team",
		Leader:               "leader",
		Model:                model,
		PersonalityGenerator: personalityGen,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	w1, err := tm.SpawnTeammate(ctx, "frontend", "build React components")
	if err != nil {
		t.Fatal(err)
	}
	w2, err := tm.SpawnTeammate(ctx, "backend", "implement API endpoints")
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, w1, TeammateIdle, 3*time.Second)
	waitForState(t, w2, TeammateIdle, 3*time.Second)

	mu.Lock()
	if len(generatedPrompts) != 2 {
		t.Errorf("expected 2 unique prompts, got %d", len(generatedPrompts))
	}
	if _, ok := generatedPrompts["build React components"]; !ok {
		t.Error("missing personality for frontend task")
	}
	if _, ok := generatedPrompts["implement API endpoints"]; !ok {
		t.Error("missing personality for backend task")
	}
	mu.Unlock()

	shutdownCtx2, shutdownCancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel2()
	tm.Shutdown(shutdownCtx2)
}

// TestE2E_SpuriousWakeNoRerun verifies that a spurious Wake() (with no
// pending messages) does NOT cause the teammate to re-run its previous task.
// Bug: when a message is sent to a RUNNING teammate, the middleware consumes
// it during the current run, but the Wake() signal remains in wakeCh. After
// the run, the teammate receives the stale wake, finds an empty mailbox,
// and `continue`s — re-executing the previous task with the old prompt.
func TestE2E_SpuriousWakeNoRerun(t *testing.T) {
	var requestCount atomic.Int32
	inner := core.NewTestModel(
		core.TextResponse("first result"),
		core.TextResponse("spurious rerun"),
	)
	model := &requestCountingModel{inner: inner, count: &requestCount}

	tm := NewTeam(TeamConfig{
		Name:   "spurious-wake-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	w, err := tm.SpawnTeammate(ctx, "worker", "initial task")
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, w, TeammateIdle, 3*time.Second)

	firstCount := requestCount.Load()
	if firstCount == 0 {
		t.Fatal("expected at least 1 model call after initial run")
	}

	// Send a spurious wake with NO message in the mailbox.
	w.Wake()

	// Give time for the teammate to process the wake.
	time.Sleep(300 * time.Millisecond)

	// The teammate should still be idle and the model should NOT have been called again.
	if w.State() != TeammateIdle {
		t.Errorf("expected idle after spurious wake, got %v", w.State())
	}
	if got := requestCount.Load(); got != firstCount {
		t.Errorf("spurious wake caused re-run: model called %d times after initial %d (expected no additional calls)", got, firstCount)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

// requestCountingModel wraps a model and counts Request calls.
type requestCountingModel struct {
	inner core.Model
	count *atomic.Int32
}

func (m *requestCountingModel) ModelName() string { return m.inner.ModelName() }
func (m *requestCountingModel) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	m.count.Add(1)
	return m.inner.Request(ctx, messages, settings, params)
}
func (m *requestCountingModel) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	return m.inner.RequestStream(ctx, messages, settings, params)
}

// TestE2E_NoPersonalityGenerator tests that teams work normally without
// a personality generator (backwards compatibility).
func TestE2E_NoPersonalityGenerator(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "no-personality-team",
		Leader: "leader",
		Model:  model,
		// No PersonalityGenerator — should use WorkerSystemPrompt.
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	w, err := tm.SpawnTeammate(ctx, "worker", "do work")
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, w, TeammateIdle, 3*time.Second)

	shutdownCtx2, shutdownCancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel2()
	tm.Shutdown(shutdownCtx2)
}

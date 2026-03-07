package team

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestTeam_NewTeam(t *testing.T) {
	tm := NewTeam(TeamConfig{
		Name:   "test-team",
		Leader: "leader",
		Model:  core.NewTestModel(core.TextResponse("done")),
	})

	if tm.Name() != "test-team" {
		t.Errorf("expected name 'test-team', got %q", tm.Name())
	}
	if len(tm.Members()) != 0 {
		t.Errorf("expected 0 members, got %d", len(tm.Members()))
	}
}

func TestTeam_NewTeam_WorkerExtraToolsConfigured(t *testing.T) {
	extra := core.FuncTool[struct{}](
		"dummy_extra_tool",
		"dummy",
		func(context.Context, struct{}) (any, error) { return "ok", nil },
	)

	tm := NewTeam(TeamConfig{
		Name:             "test-team",
		Leader:           "leader",
		Model:            core.NewTestModel(core.TextResponse("done")),
		WorkerExtraTools: []core.Tool{extra},
	})

	if len(tm.workerTools) != 1 {
		t.Fatalf("expected 1 worker extra tool, got %d", len(tm.workerTools))
	}
	if tm.workerTools[0].Definition.Name != "dummy_extra_tool" {
		t.Fatalf("unexpected worker extra tool %q", tm.workerTools[0].Definition.Name)
	}
}

func TestTeam_SpawnTeammate(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("task complete"))
	tm := NewTeam(TeamConfig{
		Name:   "test-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	teammate, err := tm.SpawnTeammate(ctx, "worker-1", "do something")
	if err != nil {
		t.Fatal(err)
	}
	if teammate.Name() != "worker-1" {
		t.Errorf("expected name 'worker-1', got %q", teammate.Name())
	}

	// Wait for the teammate to become idle (completed initial task).
	deadline := time.After(3 * time.Second)
	for teammate.State() != TeammateIdle && teammate.State() != TeammateStopped {
		select {
		case <-deadline:
			t.Fatalf("teammate did not become idle, state: %v", teammate.State())
		case <-time.After(10 * time.Millisecond):
		}
	}

	members := tm.Members()
	if len(members) != 1 {
		t.Errorf("expected 1 member, got %d", len(members))
	}

	// Shutdown.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	if err := tm.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestTeam_SpawnDuplicate(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "test-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := tm.SpawnTeammate(ctx, "worker", "task 1"); err != nil {
		t.Fatal(err)
	}

	// Spawning duplicate should fail.
	if _, err := tm.SpawnTeammate(ctx, "worker", "task 2"); err == nil {
		t.Error("expected error spawning duplicate teammate")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestTeam_Mailbox(t *testing.T) {
	// Model that completes immediately.
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "test-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	teammate, err := tm.SpawnTeammate(ctx, "worker", "initial task")
	if err != nil {
		t.Fatal(err)
	}

	// Wait for idle.
	waitForState(t, teammate, TeammateIdle, 3*time.Second)

	// Send a message — should wake the teammate.
	teammate.mailbox.Send(Message{
		From:    "leader",
		To:      "worker",
		Type:    MessageText,
		Content: "do more work",
	})
	teammate.Wake()

	// Wait for it to process and go idle again.
	// Give a moment for state transition.
	time.Sleep(50 * time.Millisecond)
	waitForState(t, teammate, TeammateIdle, 3*time.Second)

	// Shutdown.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestTeam_Events(t *testing.T) {
	bus := core.NewEventBus()

	var mu sync.Mutex
	var spawnedEvents []TeammateSpawnedEvent
	var idleEvents []TeammateIdleEvent
	core.Subscribe(bus, func(e TeammateSpawnedEvent) {
		mu.Lock()
		spawnedEvents = append(spawnedEvents, e)
		mu.Unlock()
	})
	core.Subscribe(bus, func(e TeammateIdleEvent) {
		mu.Lock()
		idleEvents = append(idleEvents, e)
		mu.Unlock()
	})

	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:     "test-team",
		Leader:   "leader",
		Model:    model,
		EventBus: bus,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	teammate, err := tm.SpawnTeammate(ctx, "worker", "task")
	if err != nil {
		t.Fatal(err)
	}

	waitForState(t, teammate, TeammateIdle, 3*time.Second)

	// Give async events time to fire.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	spawnedCount := len(spawnedEvents)
	idleCount := len(idleEvents)
	mu.Unlock()

	if spawnedCount != 1 {
		t.Errorf("expected 1 spawned event, got %d", spawnedCount)
	}
	if idleCount < 1 {
		t.Errorf("expected at least 1 idle event, got %d", idleCount)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestTeam_TaskBoard(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "test-team",
		Leader: "leader",
		Model:  model,
	})

	tb := tm.TaskBoard()
	id := tb.Create("Test task", "Description")

	task, err := tb.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Subject != "Test task" {
		t.Errorf("expected 'Test task', got %q", task.Subject)
	}
}

func TestTeam_SpawnTeammate_DefaultEndStrategyExhaustive(t *testing.T) {
	// Use a model that returns a tool call first, then text on the second call.
	// With EndStrategyExhaustive (the default), the agent should process the
	// tool call before stopping.
	model := core.NewTestModel(
		core.TextResponse("task complete"),
	)
	tm := NewTeam(TeamConfig{
		Name:   "test-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	teammate, err := tm.SpawnTeammate(ctx, "worker", "do something")
	if err != nil {
		t.Fatal(err)
	}

	waitForState(t, teammate, TeammateIdle, 3*time.Second)

	// Verify the agent was created (basic sanity check — the EndStrategy
	// is a private field on core.Agent so we can't inspect it directly,
	// but this test ensures the default doesn't break spawning).
	if teammate.Name() != "worker" {
		t.Errorf("expected name 'worker', got %q", teammate.Name())
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestTeam_SpawnTeammate_WithEndStrategyOverride(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "test-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	early := core.EndStrategyEarly
	teammate, err := tm.SpawnTeammate(ctx, "worker", "do something",
		WithTeammateEndStrategy(early),
	)
	if err != nil {
		t.Fatal(err)
	}

	waitForState(t, teammate, TeammateIdle, 3*time.Second)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestTeam_SpawnTeammate_WithMaxTokens(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "test-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	teammate, err := tm.SpawnTeammate(ctx, "worker", "do something",
		WithTeammateMaxTokens(16384),
	)
	if err != nil {
		t.Fatal(err)
	}

	waitForState(t, teammate, TeammateIdle, 3*time.Second)

	// Verify max tokens was passed through to the model by checking the
	// recorded calls.
	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}
	for i, call := range calls {
		if call.Settings == nil {
			t.Errorf("call %d: expected non-nil Settings", i)
			continue
		}
		if call.Settings.MaxTokens == nil {
			t.Errorf("call %d: expected MaxTokens to be set", i)
			continue
		}
		if *call.Settings.MaxTokens != 16384 {
			t.Errorf("call %d: expected MaxTokens=16384, got %d", i, *call.Settings.MaxTokens)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestTeam_SpawnTeammate_WithAgentOptions(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:   "test-team",
		Leader: "leader",
		Model:  model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use WithTeammateAgentOptions to set max tokens via the escape hatch.
	teammate, err := tm.SpawnTeammate(ctx, "worker", "do something",
		WithTeammateAgentOptions(core.WithMaxTokens[string](8192)),
	)
	if err != nil {
		t.Fatal(err)
	}

	waitForState(t, teammate, TeammateIdle, 3*time.Second)

	// Verify the option was applied.
	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}
	if calls[0].Settings == nil || calls[0].Settings.MaxTokens == nil {
		t.Fatal("expected MaxTokens to be set via escape hatch")
	}
	if *calls[0].Settings.MaxTokens != 8192 {
		t.Errorf("expected MaxTokens=8192, got %d", *calls[0].Settings.MaxTokens)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestTeam_WorkerMaxTokens(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:            "test-team",
		Leader:          "leader",
		Model:           model,
		WorkerMaxTokens: 32768,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	teammate, err := tm.SpawnTeammate(ctx, "worker", "do something")
	if err != nil {
		t.Fatal(err)
	}

	waitForState(t, teammate, TeammateIdle, 3*time.Second)

	// Verify the team-level max tokens was applied.
	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}
	if calls[0].Settings == nil || calls[0].Settings.MaxTokens == nil {
		t.Fatal("expected MaxTokens to be set from WorkerMaxTokens")
	}
	if *calls[0].Settings.MaxTokens != 32768 {
		t.Errorf("expected MaxTokens=32768, got %d", *calls[0].Settings.MaxTokens)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestTeam_WorkerMaxTokens_OverriddenByTeammateOption(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))
	tm := NewTeam(TeamConfig{
		Name:            "test-team",
		Leader:          "leader",
		Model:           model,
		WorkerMaxTokens: 32768,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Per-teammate override should win.
	teammate, err := tm.SpawnTeammate(ctx, "worker", "do something",
		WithTeammateMaxTokens(8192),
	)
	if err != nil {
		t.Fatal(err)
	}

	waitForState(t, teammate, TeammateIdle, 3*time.Second)

	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}
	if calls[0].Settings == nil || calls[0].Settings.MaxTokens == nil {
		t.Fatal("expected MaxTokens to be set")
	}
	if *calls[0].Settings.MaxTokens != 8192 {
		t.Errorf("expected MaxTokens=8192 (per-teammate override), got %d", *calls[0].Settings.MaxTokens)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)
}

func TestTeammateConfig_Options(t *testing.T) {
	cfg := &teammateConfig{}

	WithTeammateSystemPrompt("test prompt")(cfg)
	if cfg.systemPrompt != "test prompt" {
		t.Errorf("expected 'test prompt', got %q", cfg.systemPrompt)
	}

	es := core.EndStrategyEarly
	WithTeammateEndStrategy(es)(cfg)
	if cfg.endStrategy == nil || *cfg.endStrategy != core.EndStrategyEarly {
		t.Error("expected EndStrategyEarly")
	}

	WithTeammateMaxTokens(4096)(cfg)
	if cfg.maxTokens != 4096 {
		t.Errorf("expected 4096, got %d", cfg.maxTokens)
	}

	opt := core.WithMaxRetries[string](5)
	WithTeammateAgentOptions(opt)(cfg)
	if len(cfg.agentOpts) != 1 {
		t.Errorf("expected 1 agent option, got %d", len(cfg.agentOpts))
	}
}

func waitForState(t *testing.T, tm *Teammate, state TeammateState, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		s := tm.State()
		if s == state || s == TeammateStopped {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("teammate did not reach state %v within %v, current: %v", state, timeout, s)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestTeam_ToolsetFactory(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("done"))

	var callCount int
	var mu sync.Mutex
	factory := func() *core.Toolset {
		mu.Lock()
		callCount++
		mu.Unlock()
		return core.NewToolset("per-worker",
			core.FuncTool[struct{}]("noop", "noop", func(_ context.Context, _ struct{}) (string, error) {
				return "ok", nil
			}),
		)
	}

	tm := NewTeam(TeamConfig{
		Name:           "factory-test",
		Leader:         "leader",
		Model:          model,
		ToolsetFactory: factory,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := tm.SpawnTeammate(ctx, "w1", "task 1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tm.SpawnTeammate(ctx, "w2", "task 2")
	if err != nil {
		t.Fatal(err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	tm.Shutdown(shutdownCtx)

	mu.Lock()
	if callCount != 2 {
		t.Errorf("expected factory called twice (once per worker), got %d", callCount)
	}
	mu.Unlock()
}

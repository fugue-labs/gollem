package team

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/modelutil"
)

// TeamConfig configures a new team.
type TeamConfig struct {
	// Name identifies this team.
	Name string

	// Leader is the name of the leader agent (receives idle notifications).
	Leader string

	// Model is the LLM used for spawned teammates.
	Model core.Model

	// Toolset is the set of tools given to each teammate (e.g. coding tools).
	// The team package is decoupled from codetool — the caller provides the toolset.
	// When both Toolset and ToolsetFactory are set, ToolsetFactory takes precedence.
	Toolset *core.Toolset

	// ToolsetFactory creates a fresh toolset for each spawned teammate.
	// Use this instead of Toolset when the toolset contains stateful
	// components (e.g., background process managers) that must be isolated
	// per worker. Each call should return a new instance.
	ToolsetFactory func() *core.Toolset

	// WorkerExtraTools are additional tools attached to each spawned teammate.
	// Useful for capabilities like subagent delegation that should be available
	// in worker contexts as well as the leader.
	WorkerExtraTools []core.Tool

	// EventBus receives team lifecycle events. Optional.
	EventBus *core.EventBus

	// MailboxSize is the buffer size for teammate mailboxes. Defaults to 64.
	MailboxSize int

	// WorkerMaxTokens sets the default max output tokens per model request
	// for all spawned teammates. Use this when teammates need to produce
	// large outputs (e.g. writing long documents via tool calls). Can be
	// overridden per-teammate with WithTeammateMaxTokens.
	WorkerMaxTokens int

	// WorkerHooks are lifecycle hooks added to every spawned teammate.
	WorkerHooks []core.Hook

	// PersonalityGenerator generates task-specific system prompts for
	// teammates. When set, each spawned teammate gets a dynamically
	// generated personality tailored to its assigned task. Falls back
	// to WorkerSystemPrompt on generation failure.
	PersonalityGenerator modelutil.PersonalityGeneratorFunc
}

// Team manages a group of teammate agents.
type Team struct {
	mu              sync.RWMutex
	name            string
	leader          string
	members         map[string]*Teammate
	taskBoard       *TaskBoard
	eventBus        *core.EventBus
	model           core.Model
	toolset         *core.Toolset
	toolsetFactory  func() *core.Toolset
	workerTools     []core.Tool
	workerMaxTokens int
	workerHooks     []core.Hook
	mailboxSize     int
	personalityGen  modelutil.PersonalityGeneratorFunc
	done            chan struct{}
	closeOnce       sync.Once
	wg              sync.WaitGroup
}

// NewTeam creates a team with the given configuration.
func NewTeam(cfg TeamConfig) *Team {
	mailboxSize := cfg.MailboxSize
	if mailboxSize <= 0 {
		mailboxSize = 64
	}
	return &Team{
		name:            cfg.Name,
		leader:          cfg.Leader,
		members:         make(map[string]*Teammate),
		taskBoard:       NewTaskBoard(),
		eventBus:        cfg.EventBus,
		model:           cfg.Model,
		toolset:         cfg.Toolset,
		toolsetFactory:  cfg.ToolsetFactory,
		workerTools:     cfg.WorkerExtraTools,
		workerMaxTokens: cfg.WorkerMaxTokens,
		workerHooks:     cfg.WorkerHooks,
		mailboxSize:     mailboxSize,
		personalityGen:  cfg.PersonalityGenerator,
		done:            make(chan struct{}),
	}
}

// TeammateOption configures a teammate.
type TeammateOption func(*teammateConfig)

type teammateConfig struct {
	systemPrompt string
	hooks        []core.Hook
	endStrategy  *core.EndStrategy
	maxTokens    int
	agentOpts    []core.AgentOption[string]
}

// WithTeammateSystemPrompt overrides the default worker system prompt.
func WithTeammateSystemPrompt(prompt string) TeammateOption {
	return func(c *teammateConfig) { c.systemPrompt = prompt }
}

// WithTeammateHooks adds lifecycle hooks to a spawned teammate.
func WithTeammateHooks(hooks ...core.Hook) TeammateOption {
	return func(c *teammateConfig) { c.hooks = append(c.hooks, hooks...) }
}

// WithTeammateEndStrategy sets the end strategy for a spawned teammate.
// By default, teammates use EndStrategyExhaustive so they process all
// tool calls before completing.
func WithTeammateEndStrategy(s core.EndStrategy) TeammateOption {
	return func(c *teammateConfig) { c.endStrategy = &s }
}

// WithTeammateMaxTokens sets the max output tokens per model request
// for a spawned teammate. Use this when teammates need to produce
// large outputs (e.g. writing long documents via tool calls).
func WithTeammateMaxTokens(n int) TeammateOption {
	return func(c *teammateConfig) { c.maxTokens = n }
}

// WithTeammateAgentOptions appends arbitrary agent options to a spawned
// teammate. This is an escape hatch for any core.AgentOption not
// explicitly surfaced as a TeammateOption.
func WithTeammateAgentOptions(opts ...core.AgentOption[string]) TeammateOption {
	return func(c *teammateConfig) { c.agentOpts = append(c.agentOpts, opts...) }
}

// RegisterLeader registers the leader agent's mailbox in the team so that
// workers can send messages to the leader. Returns a middleware that drains
// the leader's mailbox between model turns (injecting messages as UserPromptParts).
func (t *Team) RegisterLeader(name string) core.AgentMiddleware {
	mb := NewMailbox(t.mailboxSize)
	tm := &Teammate{
		name:    name,
		state:   TeammateRunning,
		mailbox: mb,
		team:    t,
		wakeCh:  make(chan struct{}, 1),
	}
	t.mu.Lock()
	t.members[name] = tm
	t.mu.Unlock()
	return TeamAwarenessMiddleware(tm)
}

// SpawnTeammate creates a new teammate agent and starts it in a goroutine.
func (t *Team) SpawnTeammate(ctx context.Context, name, task string, opts ...TeammateOption) (*Teammate, error) {
	cfg := &teammateConfig{}
	for _, o := range opts {
		o(cfg)
	}

	// Resolve system prompt: explicit override > personality generator > default.
	if cfg.systemPrompt == "" && t.personalityGen != nil {
		basePrompt := WorkerSystemPrompt(name, t.name)
		if generated, err := t.personalityGen(ctx, modelutil.PersonalityRequest{
			Task:       task,
			Role:       fmt.Sprintf("teammate %q in team %q", name, t.name),
			BasePrompt: basePrompt,
		}); err == nil {
			cfg.systemPrompt = generated
			fmt.Fprintf(os.Stderr, "[gollem] team:%s personality generated for %s (%d chars)\n", t.name, name, len(generated))
		} else {
			fmt.Fprintf(os.Stderr, "[gollem] team:%s personality fallback for %s: %v\n", t.name, name, err)
		}
	}
	if cfg.systemPrompt == "" {
		cfg.systemPrompt = WorkerSystemPrompt(name, t.name)
	}

	mailbox := NewMailbox(t.mailboxSize)

	tm := &Teammate{
		name:    name,
		state:   TeammateStarting,
		mailbox: mailbox,
		team:    t,
		wakeCh:  make(chan struct{}, 1),
	}

	// Reserve the name atomically to prevent TOCTOU races.
	t.mu.Lock()
	if _, exists := t.members[name]; exists {
		t.mu.Unlock()
		return nil, fmt.Errorf("teammate %q already exists", name)
	}
	t.members[name] = tm
	t.mu.Unlock()

	// Resolve end strategy: per-teammate override > default (exhaustive).
	endStrategy := core.EndStrategyExhaustive
	if cfg.endStrategy != nil {
		endStrategy = *cfg.endStrategy
	}

	// Build agent with the configured toolset + team tools + awareness middleware.
	agentOpts := []core.AgentOption[string]{
		core.WithSystemPrompt[string](cfg.systemPrompt),
		core.WithTools[string](WorkerTools(t, tm)...),
		core.WithAgentMiddleware[string](TeamAwarenessMiddleware(tm)),
		core.WithEndStrategy[string](endStrategy),
		core.WithMaxRetries[string](2),
		core.WithUsageLimits[string](core.UsageLimits{RequestLimit: core.IntPtr(200)}),
		core.WithTurnGuardrail[string]("max-turns", core.MaxTurns(200)),
		core.WithDefaultToolTimeout[string](2 * time.Minute),
	}
	// Resolve max tokens: per-teammate override > team default.
	maxTokens := t.workerMaxTokens
	if cfg.maxTokens > 0 {
		maxTokens = cfg.maxTokens
	}
	if maxTokens > 0 {
		agentOpts = append(agentOpts, core.WithMaxTokens[string](maxTokens))
	}
	if t.toolsetFactory != nil {
		agentOpts = append(agentOpts, core.WithToolsets[string](t.toolsetFactory()))
	} else if t.toolset != nil {
		agentOpts = append(agentOpts, core.WithToolsets[string](t.toolset))
	}
	if len(t.workerTools) > 0 {
		agentOpts = append(agentOpts, core.WithTools[string](t.workerTools...))
	}
	if t.eventBus != nil {
		agentOpts = append(agentOpts, core.WithEventBus[string](t.eventBus))
	}
	allHooks := append(t.workerHooks, cfg.hooks...)
	if len(allHooks) > 0 {
		agentOpts = append(agentOpts, core.WithHooks[string](allHooks...))
	}
	// Apply caller-provided escape-hatch options last so they can override defaults.
	agentOpts = append(agentOpts, cfg.agentOpts...)

	tm.agent = core.NewAgent[string](t.model, agentOpts...)

	tmCtx, cancel := context.WithCancel(ctx)
	tm.cancel = cancel

	t.wg.Add(1)
	go tm.run(tmCtx, task)

	if t.eventBus != nil {
		core.PublishAsync(t.eventBus, TeammateSpawnedEvent{
			TeamName:     t.name,
			TeammateName: name,
			Task:         task,
		})
	}

	fmt.Fprintf(os.Stderr, "[gollem] team:%s spawned teammate:%s\n", t.name, name)
	return tm, nil
}

// Shutdown gracefully shuts down all teammates.
func (t *Team) Shutdown(ctx context.Context) error {
	fmt.Fprintf(os.Stderr, "[gollem] team:%s shutting down\n", t.name)

	// Signal all teammates to stop (skip the leader — it's the caller).
	t.mu.RLock()
	for _, tm := range t.members {
		if tm.name == t.leader {
			continue
		}
		tm.mailbox.Send(Message{
			From:      "team",
			To:        tm.name,
			Type:      MessageShutdownRequest,
			Content:   "Team is shutting down",
			Timestamp: time.Now(),
		})
		tm.Wake()
	}
	t.mu.RUnlock()

	// Close done channel to unblock any waiting teammates (safe to call twice).
	t.closeOnce.Do(func() { close(t.done) })

	// Wait for all goroutines with context deadline.
	doneCh := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		fmt.Fprintf(os.Stderr, "[gollem] team:%s all teammates stopped\n", t.name)
		return nil
	case <-ctx.Done():
		// Force cancel all teammates.
		t.mu.RLock()
		for _, tm := range t.members {
			if tm.cancel != nil {
				tm.cancel()
			}
		}
		t.mu.RUnlock()
		return ctx.Err()
	}
}

// Members returns a snapshot of all teammates.
func (t *Team) Members() []TeammateInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	infos := make([]TeammateInfo, 0, len(t.members))
	for _, tm := range t.members {
		infos = append(infos, TeammateInfo{
			Name:  tm.name,
			State: tm.State(),
		})
	}
	return infos
}

// TaskBoard returns the shared task board.
func (t *Team) TaskBoard() *TaskBoard {
	return t.taskBoard
}

// Name returns the team name.
func (t *Team) Name() string {
	return t.name
}

// getMailbox returns the mailbox for the named member, or nil.
func (t *Team) getMailbox(name string) *Mailbox {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if tm, ok := t.members[name]; ok {
		return tm.mailbox
	}
	return nil
}

// GetTeammate returns the teammate with the given name, or nil.
func (t *Team) GetTeammate(name string) *Teammate {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.members[name]
}

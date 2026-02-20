package temporal

import (
	"context"
	"errors"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"

	"github.com/fugue-labs/gollem/core"
)

// TemporalAgent wraps a core.Agent for durable execution via Temporal.
// Model requests and tool calls are executed as Temporal activities,
// providing automatic checkpointing and fault tolerance.
type TemporalAgent[T any] struct {
	wrapped      *core.Agent[T]
	name         string
	model        *TemporalModel
	tools        []TemporalTool
	config       agentConfig
	eventHandler EventHandler
}

// Option configures a TemporalAgent.
type Option func(*agentConfig)

type agentConfig struct {
	name                string
	defaultConfig       ActivityConfig
	modelConfig         *ActivityConfig
	toolConfigs         map[string]ActivityConfig
	passthroughTools    map[string]bool
	eventHandler        EventHandler
}

// WithName sets the agent name (REQUIRED — used for stable activity names).
func WithName(name string) Option {
	return func(c *agentConfig) {
		c.name = name
	}
}

// WithActivityConfig sets the default activity config for all activities.
func WithActivityConfig(config ActivityConfig) Option {
	return func(c *agentConfig) {
		c.defaultConfig = config
	}
}

// WithModelActivityConfig overrides activity config for model requests.
func WithModelActivityConfig(config ActivityConfig) Option {
	return func(c *agentConfig) {
		c.modelConfig = &config
	}
}

// WithToolActivityConfig sets activity config for specific tools.
func WithToolActivityConfig(configs map[string]ActivityConfig) Option {
	return func(c *agentConfig) {
		c.toolConfigs = configs
	}
}

// WithToolPassthrough marks specific tools to run directly (not as activities).
func WithToolPassthrough(toolNames ...string) Option {
	return func(c *agentConfig) {
		if c.passthroughTools == nil {
			c.passthroughTools = make(map[string]bool)
		}
		for _, name := range toolNames {
			c.passthroughTools[name] = true
		}
	}
}

// WithEventHandler sets a handler that receives streaming events.
func WithEventHandler(handler EventHandler) Option {
	return func(c *agentConfig) {
		c.eventHandler = handler
	}
}

// NewTemporalAgent wraps a core.Agent for Temporal durable execution.
func NewTemporalAgent[T any](agent *core.Agent[T], opts ...Option) *TemporalAgent[T] {
	cfg := &agentConfig{
		defaultConfig: DefaultActivityConfig(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.name == "" {
		panic("gollem/temporal: WithName is required for TemporalAgent")
	}

	modelConfig := cfg.defaultConfig
	if cfg.modelConfig != nil {
		modelConfig = *cfg.modelConfig
	}

	// Wrap the model.
	model := NewTemporalModel(agent.GetModel(), cfg.name, modelConfig)

	// Wrap tools.
	agentTools := agent.GetTools()
	temporalTools := make([]TemporalTool, 0, len(agentTools))
	for _, tool := range agentTools {
		if cfg.passthroughTools[tool.Definition.Name] {
			continue
		}
		toolCfg := cfg.defaultConfig
		if tc, ok := cfg.toolConfigs[tool.Definition.Name]; ok {
			toolCfg = tc
		}
		temporalTools = append(temporalTools, TemporalizeTool(cfg.name, tool, toolCfg))
	}

	return &TemporalAgent[T]{
		wrapped:      agent,
		name:         cfg.name,
		model:        model,
		tools:        temporalTools,
		config:       *cfg,
		eventHandler: cfg.eventHandler,
	}
}

// Run executes the agent. Outside a Temporal workflow, runs normally.
func (ta *TemporalAgent[T]) Run(ctx context.Context, prompt string, opts ...core.RunOption) (*core.RunResult[T], error) {
	return ta.wrapped.Run(ctx, prompt, opts...)
}

// Name returns the agent name.
func (ta *TemporalAgent[T]) Name() string {
	return ta.name
}

// Activities returns all Temporal activity functions for worker registration.
// Returns a map of activity name → activity function.
func (ta *TemporalAgent[T]) Activities() map[string]any {
	activities := make(map[string]any)

	// Model activities.
	activities[ta.model.ModelRequestActivityName()] = ta.model.ModelRequestActivity
	activities[ta.model.ModelRequestStreamActivityName()] = ta.model.ModelRequestStreamActivity

	// Tool activities.
	for _, tt := range ta.tools {
		activities[tt.ActivityName] = tt.ActivityFn
	}

	return activities
}

// RegisterActivities registers all activities from temporal agents with a Temporal worker.
func RegisterActivities(w worker.Worker, agents ...any) error {
	for _, agent := range agents {
		type activityProvider interface {
			Activities() map[string]any
			Name() string
		}

		provider, ok := agent.(activityProvider)
		if !ok {
			return errors.New("agent does not implement Activities() and Name()")
		}

		for name, fn := range provider.Activities() {
			w.RegisterActivityWithOptions(fn, activity.RegisterOptions{
				Name: name,
			})
		}
	}
	return nil
}

// EventHandler receives streaming events during a Temporal workflow run.
type EventHandler func(ctx context.Context, event core.ModelResponseStreamEvent) error

// GetModel returns the wrapped model (needed for activity registration).
func (ta *TemporalAgent[T]) GetModel() *TemporalModel {
	return ta.model
}

// GetTools returns the temporal tools.
func (ta *TemporalAgent[T]) GetTools() []TemporalTool {
	return ta.tools
}

// Verify TemporalAgent implements the interface needed for RegisterActivities.
func verifyInterface[T any]() {
	type activityProvider interface {
		Activities() map[string]any
		Name() string
	}
	var _ activityProvider = (*TemporalAgent[T])(nil)
}

func init() {
	verifyInterface[string]()
}

// Compile-time check that Agent exposes GetModel and GetTools.
var _ = func() {
	var a *core.Agent[string]
	_ = a.GetModel()
	_ = a.GetTools()
}

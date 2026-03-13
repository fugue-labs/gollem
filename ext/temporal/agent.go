package temporal

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/fugue-labs/gollem/core"
)

// TemporalAgent wraps a core.Agent with a durable Temporal workflow plus the
// supporting model/tool activities. Run itself still delegates to the wrapped
// agent directly for in-process execution.
type TemporalAgent[T any] struct {
	wrapped      *core.Agent[T]
	runtime      core.AgentRuntimeConfig[T]
	name         string
	version      string
	regName      string
	model        *TemporalModel
	tools        []TemporalTool
	toolsByName  map[string]TemporalTool
	config       agentConfig
	eventHandler EventHandler
	depsCodec    DepsCodec
	depsType     reflect.Type
}

// Option configures a TemporalAgent.
type Option func(*agentConfig)

type agentConfig struct {
	name             string
	version          string
	defaultConfig    ActivityConfig
	modelConfig      *ActivityConfig
	toolConfigs      map[string]ActivityConfig
	passthroughTools map[string]bool
	eventHandler     EventHandler
	continueAsNew    ContinueAsNewConfig
	depsCodec        DepsCodec
	depsPrototype    any
}

var temporalVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// WithName sets the agent name (REQUIRED — used for stable activity names).
func WithName(name string) Option {
	return func(c *agentConfig) {
		c.name = name
	}
}

// WithVersion adds an explicit registration version suffix to workflow and
// activity names so new deployments can coexist with old workers.
func WithVersion(version string) Option {
	return func(c *agentConfig) {
		c.version = version
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

// WithToolPassthrough is reserved for future custom-workflow support.
// The built-in durable workflow rejects passthrough tools at construction time.
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

// WithEventHandler stores a handler for custom workflow integrations that
// want to forward streaming events. TemporalAgent.Run does not invoke it.
func WithEventHandler(handler EventHandler) Option {
	return func(c *agentConfig) {
		c.eventHandler = handler
	}
}

// WithContinueAsNew configures workflow rollover thresholds.
func WithContinueAsNew(config ContinueAsNewConfig) Option {
	return func(c *agentConfig) {
		c.continueAsNew = config
	}
}

// WithDepsCodec customizes how workflow dep overrides are serialized.
func WithDepsCodec(codec DepsCodec) Option {
	return func(c *agentConfig) {
		c.depsCodec = codec
	}
}

// WithDepsPrototype declares the concrete dep type for workflow dep overrides
// when the wrapped agent has no default deps value to infer from.
func WithDepsPrototype(example any) Option {
	return func(c *agentConfig) {
		c.depsPrototype = example
	}
}

// NewTemporalAgent wraps a core.Agent and exports Temporal activity helpers.
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
	if cfg.version != "" && !temporalVersionPattern.MatchString(cfg.version) {
		panic(fmt.Sprintf("gollem/temporal: invalid version %q", cfg.version))
	}
	if cfg.depsCodec == nil {
		cfg.depsCodec = JSONDepsCodec{}
	}
	if err := ValidateCompatibility(agent); err != nil {
		panic(err)
	}
	if err := validateAgentConfig(cfg); err != nil {
		panic(err)
	}

	modelConfig := cfg.defaultConfig
	if cfg.modelConfig != nil {
		modelConfig = *cfg.modelConfig
	}
	runtime := agent.RuntimeConfig()
	regName := cfg.name
	if cfg.version != "" {
		regName = cfg.name + "__v__" + cfg.version
	}
	depsType := reflect.TypeOf(runtime.AgentDeps)
	if depsType == nil && cfg.depsPrototype != nil {
		depsType = reflect.TypeOf(cfg.depsPrototype)
	}

	// Wrap the model.
	model := NewTemporalModelWithMiddleware(
		agent.GetModel(),
		regName,
		modelConfig,
		runtime.RequestMiddleware,
		runtime.StreamMiddleware,
	)

	// Wrap tools.
	agentTools := runtime.Tools
	temporalTools := make([]TemporalTool, 0, len(agentTools))
	toolsByName := make(map[string]TemporalTool, len(agentTools))
	for _, tool := range agentTools {
		if cfg.passthroughTools[tool.Definition.Name] {
			continue
		}
		toolCfg := cfg.defaultConfig
		if tc, ok := cfg.toolConfigs[tool.Definition.Name]; ok {
			toolCfg = tc
		}
		tt := temporalizeTool(
			regName,
			tool,
			toolCfg,
			runtime.DefaultToolTimeout,
			runtime.GlobalToolResultValidators,
			func(data []byte) (any, error) {
				return decodeTemporalDeps(cfg.depsCodec, depsType, runtime.AgentDeps, data)
			},
			runtime.EventBus,
		)
		temporalTools = append(temporalTools, tt)
		toolsByName[tool.Definition.Name] = tt
	}

	return &TemporalAgent[T]{
		wrapped:      agent,
		runtime:      runtime,
		name:         cfg.name,
		version:      cfg.version,
		regName:      regName,
		model:        model,
		tools:        temporalTools,
		toolsByName:  toolsByName,
		config:       *cfg,
		eventHandler: cfg.eventHandler,
		depsCodec:    cfg.depsCodec,
		depsType:     depsType,
	}
}

// Run executes the wrapped agent directly.
// Use the exported activities from a custom Temporal workflow for durable execution.
func (ta *TemporalAgent[T]) Run(ctx context.Context, prompt string, opts ...core.RunOption) (*core.RunResult[T], error) {
	return ta.wrapped.Run(ctx, prompt, opts...)
}

// Name returns the agent name.
func (ta *TemporalAgent[T]) Name() string {
	return ta.name
}

// Version returns the explicit registration version suffix, if any.
func (ta *TemporalAgent[T]) Version() string {
	return ta.version
}

// RegistrationName returns the stable base used for workflow and activity names.
func (ta *TemporalAgent[T]) RegistrationName() string {
	return ta.regName
}

// WorkflowName returns the stable workflow name for durable execution.
func (ta *TemporalAgent[T]) WorkflowName() string {
	return "agent__" + ta.regName + "__workflow"
}

// Register registers the durable workflow and all supporting activities.
func (ta *TemporalAgent[T]) Register(w worker.Worker) error {
	w.RegisterWorkflowWithOptions(ta.RunWorkflow, workflow.RegisterOptions{
		Name: ta.WorkflowName(),
	})
	return RegisterActivities(w, ta)
}

// RegisterAll registers durable workflows and activities for multiple Temporal agents.
func RegisterAll(w worker.Worker, agents ...any) error {
	type workflowProvider interface {
		Register(worker.Worker) error
	}

	for _, agent := range agents {
		provider, ok := agent.(workflowProvider)
		if !ok {
			return errors.New("agent does not implement Register(worker.Worker)")
		}
		if err := provider.Register(w); err != nil {
			return err
		}
	}
	return nil
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

	for name, fn := range ta.callbackActivities() {
		activities[name] = fn
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

// EventHandler returns the optional custom streaming event handler configured
// on the wrapper for custom workflow integrations.
func (ta *TemporalAgent[T]) EventHandler() EventHandler {
	if ta == nil {
		return nil
	}
	return ta.eventHandler
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

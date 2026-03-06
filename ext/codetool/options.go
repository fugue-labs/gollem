package codetool

import (
	"time"

	montygo "github.com/fugue-labs/monty-go"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/modelutil"
)

// Config holds shared configuration for coding tools.
type Config struct {
	// WorkDir is the working directory for shell commands and file operations.
	// Defaults to the current working directory.
	WorkDir string

	// BashTimeout is the default timeout for shell commands.
	// Defaults to 5 minutes.
	BashTimeout time.Duration

	// MaxFileSize is the maximum file size in bytes that View will read.
	// Defaults to 1MB.
	MaxFileSize int64

	// MaxOutputLen is the maximum output length in bytes from Bash.
	// Defaults to 100KB.
	MaxOutputLen int

	// Runner is an optional monty-go WASM Python runner for code mode.
	// When set, the agent gets an execute_code tool that lets it batch
	// multiple tool calls into a single Python script (N calls per round-trip).
	Runner *montygo.Runner

	// Model is the LLM model used for subagent delegation.
	// When set, the agent gets a delegate tool for spawning subagents.
	Model core.Model

	// Timeout is the overall run timeout for the agent. Used by the
	// TimeBudgetMiddleware to inject time-remaining warnings.
	Timeout time.Duration

	// TeamMode enables multi-agent team coordination. When set, the agent
	// becomes a team leader with tools to spawn teammate agents, send
	// messages, and manage a shared task board. Requires Model to be set.
	TeamMode bool

	// PersonalityGenerator generates task-specific system prompts for
	// subagents and teammates. When set, each spawned agent gets a
	// dynamically generated personality tailored to its assigned task.
	PersonalityGenerator modelutil.PersonalityGeneratorFunc

	// AutoContextConfig overrides the default auto-context compression settings.
	// When set, both the main agent and subagents use these limits. This allows
	// provider-aware tuning (e.g., 150K for Claude's 200K context vs 80K for grok).
	AutoContextConfig *core.AutoContextConfig

	// ReasoningSandwichConfig overrides phase-specific reasoning settings.
	// When set, both the main agent and subagents use this as the base profile.
	ReasoningSandwichConfig *ReasoningSandwichConfig

	// DisableGreedyThinkingPressure disables time-budget-based reasoning caps
	// (effort/thinking/max_tokens). Time warnings are still injected.
	DisableGreedyThinkingPressure bool

	// DisableDelegate disables the delegate (subagent) tool. When set, the
	// agent cannot spawn subagents for delegation. Useful for benchmarks
	// or constrained environments where single-agent execution is preferred.
	DisableDelegate bool

	// BackgroundProcessManager manages background processes started by the
	// bash tool with background=true. When nil, one is created automatically
	// by Toolset/AgentOptions. Share a single manager across tools so that
	// bash_status can query processes started by bash.
	BackgroundProcessManager *BackgroundProcessManager
}

// Option configures coding tools.
type Option func(*Config)

func defaults() *Config {
	return &Config{
		BashTimeout:  5 * time.Minute,
		MaxFileSize:  1 << 20,    // 1MB
		MaxOutputLen: 100 * 1024, // 100KB — smart head+tail truncation preserves error info
	}
}

func applyOpts(opts []Option) *Config {
	cfg := defaults()
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

// WithWorkDir sets the working directory for tools.
func WithWorkDir(dir string) Option {
	return func(c *Config) { c.WorkDir = dir }
}

// WithBashTimeout sets the default timeout for bash commands.
func WithBashTimeout(d time.Duration) Option {
	return func(c *Config) { c.BashTimeout = d }
}

// WithMaxFileSize sets the maximum readable file size.
func WithMaxFileSize(n int64) Option {
	return func(c *Config) { c.MaxFileSize = n }
}

// WithMaxOutputLen sets the maximum output length from bash commands.
func WithMaxOutputLen(n int) Option {
	return func(c *Config) { c.MaxOutputLen = n }
}

// WithCodeMode enables code mode using a monty-go WASM Python runner.
// The agent gets an execute_code tool alongside individual tools, letting
// it batch multiple operations into a single Python script per API call.
// The caller owns the runner lifecycle (create with montygo.New(), close
// when done).
func WithCodeMode(runner *montygo.Runner) Option {
	return func(c *Config) { c.Runner = runner }
}

// WithModel sets the model for subagent delegation. When provided, the
// agent gets a "delegate" tool that can spawn focused subagents for
// subtask execution. The subagent uses the same coding tools but runs
// with its own context and limited turns.
func WithModel(model core.Model) Option {
	return func(c *Config) { c.Model = model }
}

// WithTimeout sets the overall run timeout. When set, a time budget
// middleware injects warnings as the deadline approaches, helping the
// agent prioritize completion.
func WithTimeout(d time.Duration) Option {
	return func(c *Config) { c.Timeout = d }
}

// WithTeamMode enables multi-agent team coordination. The agent becomes
// a team leader that can spawn teammate agents for parallel work.
// Requires WithModel to be set (teammates use the same model).
func WithTeamMode() Option {
	return func(c *Config) { c.TeamMode = true }
}

// WithPersonalityGenerator sets a function that generates task-specific
// system prompts for subagents and teammates. Use modelutil.GeneratePersonality
// to create a generator backed by an LLM, or provide a custom function.
func WithPersonalityGenerator(gen modelutil.PersonalityGeneratorFunc) Option {
	return func(c *Config) { c.PersonalityGenerator = gen }
}

// WithAutoContextConfig overrides the default auto-context compression settings
// for both the main agent and subagents. Use this to tune context limits per
// provider (e.g., 150K for Claude, 80K for grok).
func WithAutoContextConfig(cfg core.AutoContextConfig) Option {
	return func(c *Config) { c.AutoContextConfig = &cfg }
}

// WithReasoningSandwichConfig overrides the default reasoning sandwich profile.
func WithReasoningSandwichConfig(cfg ReasoningSandwichConfig) Option {
	return func(c *Config) { c.ReasoningSandwichConfig = &cfg }
}

// WithDisableGreedyThinkingPressure disables time-budget-based reasoning caps
// while keeping time warnings enabled.
func WithDisableGreedyThinkingPressure() Option {
	return func(c *Config) { c.DisableGreedyThinkingPressure = true }
}

// WithDisableDelegate disables the delegate (subagent) tool so the agent
// operates strictly in single-agent mode without any delegation capability.
func WithDisableDelegate() Option {
	return func(c *Config) { c.DisableDelegate = true }
}

// WithBackgroundProcessManager sets a shared background process manager.
// When provided, all tools (bash, bash_status) share this manager. If not
// set, Toolset and AgentOptions create one automatically.
func WithBackgroundProcessManager(m *BackgroundProcessManager) Option {
	return func(c *Config) { c.BackgroundProcessManager = m }
}

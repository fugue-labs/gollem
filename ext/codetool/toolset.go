package codetool

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/deep"
	"github.com/fugue-labs/gollem/ext/monty"
	"github.com/fugue-labs/gollem/ext/team"
	"github.com/fugue-labs/gollem/modelutil"
)

// Toolset returns all coding agent tools as a core.Toolset.
// It is a stateless tool registry — safe to share across multiple agents
// (e.g., ext/team workers).
//
// Background process support (background=true, bash_status) requires a
// [BackgroundProcessManager] via [WithBackgroundProcessManager]. Without
// one, those features degrade gracefully with retry errors.
//
// Background lifecycle (cleanup on run end, completion notifications)
// is NOT wired here. Use [AgentOptions] for automatic lifecycle, or
// wire hooks manually.
//
//	ts := codetool.Toolset(codetool.WithWorkDir("/my/project"))
//	agent := core.NewAgent(model, "...", core.WithToolsets[string](ts))
func Toolset(opts ...Option) *core.Toolset {
	return &core.Toolset{
		Name: "codetool",
		Tools: []core.Tool{
			Bash(opts...),
			BashStatus(opts...),
			View(opts...),
			Write(opts...),
			Edit(opts...),
			MultiEdit(opts...),
			Grep(opts...),
			Glob(opts...),
			Ls(opts...),
			LSP(opts...),
		},
	}
}

// AllTools returns all coding agent tools as a slice.
//
// Like [Toolset], background process support requires an explicit
// [BackgroundProcessManager] via [WithBackgroundProcessManager], and
// lifecycle (cleanup, completion notifications) must be wired manually.
// Use [AgentOptions] for the automatic single-agent path.
func AllTools(opts ...Option) []core.Tool {
	return []core.Tool{
		Bash(opts...),
		BashStatus(opts...),
		View(opts...),
		Write(opts...),
		Edit(opts...),
		MultiEdit(opts...),
		Grep(opts...),
		Glob(opts...),
		Ls(opts...),
		LSP(opts...),
	}
}

// ensureBackgroundManager ensures that opts contain a shared BackgroundProcessManager.
// If one is not already configured, a new manager is created and appended.
func ensureBackgroundManager(opts []Option) []Option {
	cfg := applyOpts(opts)
	if cfg.BackgroundProcessManager == nil {
		return append(opts, WithBackgroundProcessManager(NewBackgroundProcessManager()))
	}
	return opts
}

// AgentOptions returns the recommended set of agent options for a coding agent.
// This includes the system prompt, toolset, middleware (loop detection, context
// injection, verification checkpoint), guardrails, tracing, hooks, auto-context
// management, and tool timeouts.
//
// Usage:
//
//	opts := codetool.AgentOptions("/path/to/project")
//	agent := core.NewAgent[string](model, opts...)
func AgentOptions(workDir string, toolOpts ...Option) []core.AgentOption[string] {
	if workDir != "" {
		toolOpts = append([]Option{WithWorkDir(workDir)}, toolOpts...)
	}
	// Ensure shared background process manager for all tools.
	toolOpts = ensureBackgroundManager(toolOpts)
	cfg := applyOpts(toolOpts)
	verifyMW, verifyValidator := VerificationCheckpoint(workDir, cfg.Timeout)
	var kickoffMW core.AgentMiddleware
	if cfg.Model != nil {
		if cfg.TeamMode {
			kickoffMW = EarlyTeamKickoffMiddleware()
		} else {
			kickoffMW = EarlyDelegateKickoffMiddleware()
		}
	}

	// Build system prompt and tool options.
	// When code mode is enabled, the agent gets both individual tools AND
	// an execute_code tool that batches N tool calls per API round-trip.
	systemPrompt := SystemPrompt
	if cfg.DisableDelegate {
		systemPrompt = stripDelegateFromPrompt(systemPrompt)
	}
	// teamRef is set below if team mode is enabled; the leader's OnRunEnd
	// hook closure reads it to coordinate shutdown before cleanup.
	var teamRef *team.Team

	toolOptions := []core.AgentOption[string]{
		core.WithToolsets[string](Toolset(toolOpts...)),

		// Background process lifecycle: cleanup non-keep_alive processes when
		// the agent run ends, and inject completion notifications between turns.
		// These are agent-level (not toolset-level) so they fire once for the
		// top-level agent, not per-worker in team mode.
		core.WithHooks[string](core.Hook{
			OnRunEnd: func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage, _ error) {
				// In team mode, shut down workers before cleaning up the
				// leader's background processes. This ensures per-worker
				// cleanup hooks fire first (each worker has its own
				// BackgroundProcessManager via ToolsetFactory).
				if teamRef != nil {
					shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					if err := teamRef.Shutdown(shutdownCtx); err != nil {
						fmt.Fprintf(os.Stderr, "[gollem] team shutdown error: %v\n", err)
					}
				}
				cfg.BackgroundProcessManager.Cleanup()
			},
		}),
		core.WithDynamicSystemPrompt[string](cfg.BackgroundProcessManager.CompletionPrompt),
	}
	if cfg.Runner != nil {
		cm := monty.New(cfg.Runner, AllTools(toolOpts...))
		toolOptions = append(toolOptions, core.WithTools[string](cm.Tool()))
		systemPrompt += "\n\n" + cm.SystemPrompt()
	}
	toolOptions = append(toolOptions,
		core.WithToolsPrepare[string](disableExecuteCodeOnImportFailuresPrepare()),
	)

	// Planning tool: persistent task list for tracking progress on multi-step work.
	toolOptions = append(toolOptions, core.WithTools[string](deep.PlanningTool()))
	if cfg.Model != nil {
		toolOptions = append(toolOptions, core.WithTools[string](InvariantsTool(cfg.Model)))
	}

	// Default personality generation: when a model is available but no
	// explicit generator was provided, auto-create a cached generator.
	// Append to toolOpts so it flows through to SubAgentTool as well.
	if cfg.Model != nil && cfg.PersonalityGenerator == nil {
		cfg.PersonalityGenerator = modelutil.CachedPersonalityGenerator(
			modelutil.GeneratePersonality(cfg.Model),
		)
		toolOpts = append(toolOpts, WithPersonalityGenerator(cfg.PersonalityGenerator))
	}

	// SubAgent delegation: available unless explicitly disabled.
	// Even in team mode, delegate is useful for one-shot focused work.
	if cfg.Model != nil && !cfg.DisableDelegate {
		toolOptions = append(toolOptions, core.WithTools[string](SubAgentTool(cfg.Model, toolOpts...)))
	}

	// open_image: available when the model supports vision.
	if cfg.Model != nil && modelutil.GetProfile(cfg.Model).SupportsVision {
		toolOptions = append(toolOptions, core.WithTools[string](OpenImage(toolOpts...)))
		systemPrompt += "\n\n" + openImageHint
	}

	// Team mode: leader agent with tools to spawn teammates and coordinate work.
	var teamLeaderMW core.AgentMiddleware
	if cfg.TeamMode && cfg.Model != nil {
		t := team.NewTeam(team.TeamConfig{
			Name:   "coding-team",
			Leader: "leader",
			Model:  cfg.Model,
			// Each worker gets its own toolset with an isolated
			// BackgroundProcessManager so bash_status, cleanup, and
			// completion notifications are per-worker.
			ToolsetFactory: func() *core.Toolset {
				mgr := NewBackgroundProcessManager()
				workerOpts := make([]Option, len(toolOpts))
				copy(workerOpts, toolOpts)
				workerOpts = append(workerOpts, WithBackgroundProcessManager(mgr))
				ts := Toolset(workerOpts...)
				ts.Hooks = []core.Hook{{
					OnRunEnd: func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage, _ error) {
						mgr.Cleanup()
					},
				}}
				ts.DynamicSystemPrompts = []core.SystemPromptFunc{mgr.CompletionPrompt}
				return ts
			},
			WorkerExtraTools:     []core.Tool{SubAgentTool(cfg.Model, toolOpts...)},
			PersonalityGenerator: cfg.PersonalityGenerator,
		})
		teamRef = t
		// Register the leader so workers can send messages to it.
		teamLeaderMW = t.RegisterLeader("leader")
		toolOptions = append(toolOptions,
			core.WithTools[string](team.LeaderTools(t)...),
		)
		systemPrompt += "\n\n" + team.LeaderSystemPrompt("coding-team")
	}

	reasoningCfg := DefaultReasoningSandwichConfig()
	if cfg.ReasoningSandwichConfig != nil {
		reasoningCfg = *cfg.ReasoningSandwichConfig
	}

	opts := []core.AgentOption[string]{
		// System prompt with coding agent instructions (+ code mode if enabled).
		core.WithSystemPrompt[string](systemPrompt),
	}
	opts = append(opts, toolOptions...)
	opts = append(opts,
		// Retry malformed output up to 3 times.
		core.WithMaxRetries[string](3),

		// Middleware chain (outermost first):
		// 1. Loop detection — break doom loops of repeated edits.
		core.WithAgentMiddleware[string](LoopDetectionMiddleware(4)),
		// 2. Progress tracking — nudge agent to produce output files early.
		//    Pass timeout so it can also use time-based triggers.
		core.WithAgentMiddleware[string](ProgressTrackingMiddleware(workDir, cfg.Timeout)),
		// 3. Context injection — discover environment on first turn.
		core.WithAgentMiddleware[string](ContextInjectionMiddleware(workDir, cfg.Timeout)),
		// 4. Reasoning sandwich — vary thinking budget by phase.
		core.WithAgentMiddleware[string](ReasoningSandwichMiddleware(reasoningCfg)),
		// 5. Verification tracking — track whether agent runs tests.
		core.WithAgentMiddleware[string](verifyMW),
		// 6. Context overflow recovery — catches 413 and retries with compressed messages.
		core.WithAgentMiddleware[string](ContextOverflowMiddleware()),
		// 7. Stderr logging — real-time tool call observability.
		core.WithAgentMiddleware[string](stderrLoggingMiddleware()),

		// Output validator: reject completion without verification.
		core.WithOutputValidator[string](verifyValidator),

		// Override default request limit of 50 — coding tasks need more turns.
		core.WithUsageLimits[string](core.UsageLimits{RequestLimit: core.IntPtr(500)}),

		// Guardrails: prevent infinite loops.
		core.WithTurnGuardrail[string]("max-turns", core.MaxTurns(500)),

		// Tool timeout: individual tools get 3 minutes max.
		// Some compute-heavy tasks (compilation, data processing) need extra time.
		core.WithDefaultToolTimeout[string](3*time.Minute),

		// History processor: truncate oversized content blocks before
		// auto-context sees them. Ensures token estimates are accurate
		// and prevents a single large tool result from dominating context.
		core.WithHistoryProcessor[string](ContentTruncationProcessor(20000)),

		// Auto context: compress old messages when context grows too large.
		// Default: 80K to leave headroom before provider limits (xAI/grok
		// returns 413 at ~130K tokens). Provider-aware configs override this
		// (e.g., 150K for Claude's 200K context). ContextOverflowMiddleware
		// provides a safety net if we still exceed the limit.
		core.WithAutoContext[string](autoContextForConfig(cfg)),

		// Tracing: capture full execution trace for post-run analysis.
		core.WithTracing[string](),

		// Hooks: real-time observability to stderr.
		core.WithHooks[string](core.Hook{
			OnRunStart: func(_ context.Context, _ *core.RunContext, prompt string) {
				if len(prompt) > 100 {
					n := 100
					for n > 0 && !utf8.RuneStart(prompt[n]) {
						n--
					}
					prompt = prompt[:n] + "..."
				}
				fmt.Fprintf(os.Stderr, "[gollem] run started: %s\n", prompt)
			},
			OnRunEnd: func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage, err error) {
				if err != nil {
					fmt.Fprintf(os.Stderr, "[gollem] run ended with error: %v\n", err)
				} else {
					fmt.Fprintf(os.Stderr, "[gollem] run completed successfully\n")
				}
			},
			OnToolStart: func(_ context.Context, _ *core.RunContext, name string, argsJSON string) {
				summary := argsJSON
				if len(summary) > 200 {
					n := 200
					for n > 0 && !utf8.RuneStart(summary[n]) {
						n--
					}
					summary = summary[:n] + "..."
				}
				fmt.Fprintf(os.Stderr, "[gollem] tool:start %s %s\n", name, summary)
			},
			OnToolEnd: func(_ context.Context, _ *core.RunContext, name string, result string, err error) {
				if err != nil {
					fmt.Fprintf(os.Stderr, "[gollem] tool:end   %s ERROR: %v\n", name, err)
				} else {
					summary := result
					if len(summary) > 150 {
						n := 150
						for n > 0 && !utf8.RuneStart(summary[n]) {
							n--
						}
						summary = summary[:n] + "..."
					}
					fmt.Fprintf(os.Stderr, "[gollem] tool:end   %s %s\n", name, strings.ReplaceAll(summary, "\n", "\\n"))
				}
			},
		}),
	)

	if kickoffMW != nil {
		// Keep kickoff outside the fixed append block so middleware state
		// persists for the whole run (turn counters, spawn tracking).
		opts = append(opts, core.WithAgentMiddleware[string](kickoffMW))
	}

	// Team leader middleware: drain incoming messages from workers between turns.
	if teamLeaderMW != nil {
		opts = append(opts, core.WithAgentMiddleware[string](teamLeaderMW))
	}

	// Time budget awareness: warn the agent when approaching timeout.
	if cfg.Timeout > 0 {
		if cfg.DisableGreedyThinkingPressure {
			opts = append(opts, core.WithAgentMiddleware[string](TimeBudgetMiddlewareNoGreedy(cfg.Timeout)))
		} else {
			opts = append(opts, core.WithAgentMiddleware[string](TimeBudgetMiddleware(cfg.Timeout)))
		}
	}

	// Export traces to /tmp/gollem-traces if the dir is writable.
	traceDir := "/tmp/gollem-traces"
	if err := os.MkdirAll(traceDir, 0o755); err == nil {
		opts = append(opts, core.WithTraceExporter[string](core.NewJSONFileExporter(traceDir)))
	}

	return opts
}

// autoContextForConfig returns the auto-context config to use, preferring
// an explicit override from WithAutoContextConfig, with sensible defaults.
func autoContextForConfig(cfg *Config) core.AutoContextConfig {
	if cfg.AutoContextConfig != nil {
		return *cfg.AutoContextConfig
	}
	return core.AutoContextConfig{
		MaxTokens: 80000,
		KeepLastN: 10,
	}
}

// subagentAutoContextConfig returns auto-context config scaled for subagents.
// Subagents get the same provider-aware limits but with fewer kept messages
// since they run shorter tasks.
func subagentAutoContextConfig(cfg *Config) core.AutoContextConfig {
	if cfg.AutoContextConfig != nil {
		return core.AutoContextConfig{
			MaxTokens: cfg.AutoContextConfig.MaxTokens,
			KeepLastN: max(cfg.AutoContextConfig.KeepLastN-2, 8),
		}
	}
	return core.AutoContextConfig{
		MaxTokens: 80000,
		KeepLastN: 8,
	}
}

// stderrLoggingMiddleware logs each model turn to stderr with timing.
func stderrLoggingMiddleware() core.AgentMiddleware {
	turn := 0
	return func(
		ctx context.Context,
		messages []core.ModelMessage,
		settings *core.ModelSettings,
		params *core.ModelRequestParameters,
		next func(context.Context, []core.ModelMessage, *core.ModelSettings, *core.ModelRequestParameters) (*core.ModelResponse, error),
	) (*core.ModelResponse, error) {
		turn++
		fmt.Fprintf(os.Stderr, "[gollem] turn %d: sending %d messages to model\n", turn, len(messages))
		start := time.Now()
		resp, err := next(ctx, messages, settings, params)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[gollem] turn %d: model error after %v: %v\n", turn, elapsed, err)
		} else {
			// Count tool calls in response.
			toolCalls := 0
			for _, part := range resp.Parts {
				if _, ok := part.(core.ToolCallPart); ok {
					toolCalls++
				}
			}
			textLen := len(resp.TextContent())
			reasoningTokens := 0
			if resp.Usage.Details != nil {
				reasoningTokens = resp.Usage.Details["reasoning_tokens"]
			}
			fmt.Fprintf(os.Stderr,
				"[gollem] turn %d: response in %v (tools: %d, text: %d chars, tokens: in=%d out=%d cache_read=%d reasoning=%d)\n",
				turn,
				elapsed.Round(time.Millisecond),
				toolCalls,
				textLen,
				resp.Usage.InputTokens,
				resp.Usage.OutputTokens,
				resp.Usage.CacheReadTokens,
				reasoningTokens,
			)
		}
		return resp, err
	}
}

// stripDelegateFromPrompt removes the "- **delegate**: ..." paragraph from
// the system prompt so the model doesn't reference a tool that isn't available.
func stripDelegateFromPrompt(prompt string) string {
	const marker = "- **delegate**:"
	idx := strings.Index(prompt, marker)
	if idx < 0 {
		return prompt
	}
	// Find the end of this bullet: next "\n\n" or "\n- **" signals a new section.
	rest := prompt[idx:]
	end := len(rest)
	if i := strings.Index(rest, "\n\n"); i >= 0 {
		end = i
	}
	return prompt[:idx] + prompt[idx+end:]
}

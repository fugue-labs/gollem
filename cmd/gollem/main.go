// Command gollem provides a CLI for running coding agents and debugging via TUI.
//
// Usage:
//
//	gollem run --provider anthropic --model claude-opus-4-6 "Fix the failing tests"
//	gollem debug --provider openai --model gpt-4o "prompt"
package main

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	langsmith "github.com/fugue-labs/gollem-langsmith"
	lf "github.com/fugue-labs/langfuse-go"
	montygo "github.com/fugue-labs/monty-go"
	"github.com/google/uuid"

	"github.com/fugue-labs/gollem/appserver/catalog"
	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/codetool"
	"github.com/fugue-labs/gollem/ext/middleware"
	traceutil "github.com/fugue-labs/gollem/ext/trace"
	"github.com/fugue-labs/gollem/ext/tui"
	"github.com/fugue-labs/gollem/modelutil"
	"github.com/fugue-labs/gollem/provider/anthropic"
	"github.com/fugue-labs/gollem/provider/openai"
	"github.com/fugue-labs/gollem/provider/vertexai"
	"github.com/fugue-labs/gollem/provider/vertexai_anthropic"
)

// Default thinking budget for Anthropic models with extended thinking.
// Balanced for quality vs speed: allows deep reasoning without burning
// too many seconds per turn on time-limited benchmark tasks.
const defaultThinkingBudget = 16000

// gitCommit is set via -ldflags "-X main.gitCommit=..." at build time.
var gitCommit string

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "run":
		runAgent()
	case "debug":
		runDebug()
	case "serve":
		runServe()
	case "app-server":
		runAppServer()
	case "trace":
		runTraceCommand()
	case "--help", "-h", "help":
		printUsage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

type flags struct {
	provider        string
	modelName       string
	location        string // GCP region for vertexai providers
	project         string // GCP project ID for vertexai providers
	workDir         string
	prompt          string
	traceOut        string
	traceStream     string
	resumeSnapshot  string
	timeout         time.Duration
	thinkingBudget  int
	reasoningEffort string // OpenAI: "low", "medium", "high", "xhigh"
	teamMode        string // "auto", "on", "off"
	toolPolicy      string
	evaluator       string
	noCodeMode      bool
	noReasoning     bool // Disable all reasoning (thinking + effort)
	modelExplicit   bool
	teamExplicit    bool
}

func parseFlags(args []string) flags {
	teamMode := normalizeTeamMode(os.Getenv("GOLLEM_TEAM_MODE"))
	if teamMode == "" {
		teamMode = "auto"
	}
	f := flags{
		timeout:        30 * time.Minute,
		thinkingBudget: -1, // -1 means "use default"
		teamMode:       teamMode,
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--provider":
			if i+1 < len(args) {
				f.provider = args[i+1]
				i++
			}
		case "--model":
			if i+1 < len(args) {
				f.modelName = args[i+1]
				f.modelExplicit = true
				i++
			}
		case "--location":
			if i+1 < len(args) {
				f.location = args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(args) {
				f.project = args[i+1]
				i++
			}
		case "--workdir":
			if i+1 < len(args) {
				f.workDir = args[i+1]
				i++
			}
		case "--trace-out":
			if i+1 < len(args) {
				f.traceOut = args[i+1]
				i++
			}
		case "--trace-stream":
			if i+1 < len(args) {
				f.traceStream = args[i+1]
				i++
			}
		case "--resume-snapshot":
			if i+1 < len(args) {
				f.resumeSnapshot = args[i+1]
				i++
			}
		case "--timeout":
			if i+1 < len(args) {
				if d, err := time.ParseDuration(args[i+1]); err == nil {
					f.timeout = d
				}
				i++
			}
		case "--thinking-budget":
			if i+1 < len(args) {
				if _, err := fmt.Sscanf(args[i+1], "%d", &f.thinkingBudget); err != nil {
					f.thinkingBudget = -1
				}
				i++
			}
		case "--reasoning-effort":
			if i+1 < len(args) {
				f.reasoningEffort = args[i+1]
				i++
			}
		case "--team-mode":
			if i+1 < len(args) {
				f.teamMode = normalizeTeamMode(args[i+1])
				f.teamExplicit = true
				if f.teamMode == "" {
					f.teamMode = "auto"
				}
				i++
			}
		case "--no-thinking", "--no-reasoning":
			f.noReasoning = true
		case "--no-code-mode":
			f.noCodeMode = true
		case "--help", "-h":
			printUsage()
			os.Exit(0)
		default:
			if !strings.HasPrefix(args[i], "-") && f.prompt == "" {
				f.prompt = args[i]
			}
		}
	}

	if f.workDir == "" {
		f.workDir, _ = os.Getwd()
	}

	return f
}

func runAgent() {
	f := parseFlags(os.Args[2:])

	resumeSnapshotPath := strings.TrimSpace(f.resumeSnapshot)
	if resumeSnapshotPath == "" {
		resumeSnapshotPath = strings.TrimSpace(os.Getenv("GOLLEM_RESUME_SNAPSHOT"))
	}
	var resumeSnapshot *core.RunSnapshot
	if resumeSnapshotPath != "" {
		var err error
		resumeSnapshot, err = loadRunSnapshotFile(resumeSnapshotPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading resume snapshot: %v\n", err)
			os.Exit(1)
		}
		if f.prompt == "" {
			f.prompt = resumeSnapshot.Prompt
		}
		if f.prompt == "" {
			f.prompt = "resume"
		}
		overrides, overrideErr := applyResumeSnapshotOverrides(&f, resumeSnapshot)
		if overrideErr != nil {
			fmt.Fprintf(os.Stderr, "error applying resume snapshot overrides: %v\n", overrideErr)
			os.Exit(1)
		}
		if len(overrides.Applied) > 0 {
			fmt.Fprintf(os.Stderr, "gollem: fork overrides applied (%s)\n", strings.Join(overrides.Applied, ", "))
		}
		fmt.Fprintf(os.Stderr, "gollem: resume snapshot loaded (%s, step=%d)\n", resumeSnapshotPath, resumeSnapshot.RunStep)
	}

	if f.prompt == "" {
		fmt.Fprintln(os.Stderr, "error: prompt is required")
		printRunUsage()
		os.Exit(1)
	}

	// Detect the real task deadline for accurate time budget warnings.
	// GOLLEM_TIMEOUT_SEC (from Harbor) or task.toml represents the outer
	// deadline. The --timeout flag (f.timeout) may be shorter to leave a
	// cleanup buffer. We track both:
	//   - f.timeout:    execution timeout for context.WithTimeout (shorter)
	//   - budgetTimeout: real deadline for TimeBudgetMiddleware warnings
	budgetTimeout := f.timeout
	if taskTimeout := detectTaskTimeout(f.workDir); taskTimeout > 0 {
		budgetTimeout = taskTimeout
		// Also shorten exec timeout if task.toml is shorter than --timeout.
		if taskTimeout < f.timeout {
			fmt.Fprintf(os.Stderr, "gollem: detected task timeout: %v (overriding %v)\n", taskTimeout, f.timeout)
			f.timeout = taskTimeout
		}
	}
	if envTimeout := os.Getenv("GOLLEM_TIMEOUT_SEC"); envTimeout != "" {
		var secs float64
		if _, err := fmt.Sscanf(envTimeout, "%f", &secs); err == nil && secs > 0 {
			envDuration := time.Duration(secs) * time.Second
			// Use env timeout for budget tracking (accurate deadline).
			budgetTimeout = envDuration
			fmt.Fprintf(os.Stderr, "gollem: budget timeout: %v, exec timeout: %v\n", envDuration, f.timeout)
		}
	}

	if f.provider == "" {
		f.provider = detectProvider()
		if f.provider == "" {
			fmt.Fprintln(os.Stderr, "error: --provider is required (or set ANTHROPIC_API_KEY / OPENAI_API_KEY)")
			os.Exit(1)
		}
	}

	// Optional per-task reasoning override, intended for single-run TB2 routing.
	// Example:
	//   GOLLEM_REASONING_BY_TASK="model-extraction-relu-logits=xhigh,regex-chess=xhigh,*=high"
	//
	// Exact task match wins; "*" acts as fallback.
	if f.provider == "openai" {
		if eff, source := resolveReasoningEffortByTask(f.workDir, f.reasoningEffort); eff != "" {
			f.reasoningEffort = eff
			if source != "" {
				fmt.Fprintf(os.Stderr, "gollem: reasoning override applied (%s -> %s)\n", source, eff)
			}
		}
		// Provider/model defaults still apply when override is absent.
		if derived := deriveOpenAIReasoningEffort(f.modelName, f.reasoningEffort); derived != "" {
			f.reasoningEffort = derived
		}
	}

	requestTimeout := deriveRequestTimeout(f.timeout)
	baseModel, err := createModel(f.provider, f.modelName, f.location, f.project, requestTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating model: %v\n", err)
		os.Exit(1)
	}
	if f.modelName == "" {
		f.modelName = strings.TrimSpace(baseModel.ModelName())
	}
	if f.provider == "openai" {
		// Re-derive once we know the effective model name (including provider defaults).
		if derived := deriveOpenAIReasoningEffort(f.modelName, f.reasoningEffort); derived != "" {
			f.reasoningEffort = derived
		}
	}
	fmt.Fprintf(os.Stderr, "gollem: model request timeout: %v\n", requestTimeout)
	if f.provider == "openai" {
		cacheKey := strings.TrimSpace(os.Getenv("OPENAI_PROMPT_CACHE_KEY"))
		cacheRetention := strings.TrimSpace(os.Getenv("OPENAI_PROMPT_CACHE_RETENTION"))
		serviceTier := strings.TrimSpace(os.Getenv("OPENAI_SERVICE_TIER"))
		transport := strings.TrimSpace(os.Getenv("OPENAI_TRANSPORT"))
		if transport == "" {
			transport = "http"
		}
		wsHTTPFallback := strings.TrimSpace(os.Getenv("OPENAI_WEBSOCKET_HTTP_FALLBACK"))
		if wsHTTPFallback == "" {
			wsHTTPFallback = "0"
		}
		if cacheKey != "" {
			if cacheRetention == "" {
				cacheRetention = "default"
			}
			fmt.Fprintf(os.Stderr, "gollem: openai prompt cache enabled (key=%s, retention=%s)\n", cacheKey, cacheRetention)
		} else {
			fmt.Fprintf(os.Stderr, "gollem: openai prompt cache disabled (OPENAI_PROMPT_CACHE_KEY not set)\n")
		}
		if serviceTier != "" {
			fmt.Fprintf(os.Stderr, "gollem: openai service tier: %s\n", serviceTier)
		}
		fmt.Fprintf(os.Stderr, "gollem: openai transport: %s\n", transport)
		fmt.Fprintf(os.Stderr, "gollem: openai websocket->http fallback: %s\n", wsHTTPFallback)
	}
	if f.provider == "vertexai-anthropic" {
		cacheTTL := strings.TrimSpace(os.Getenv("VERTEXAI_ANTHROPIC_PROMPT_CACHE_TTL"))
		cacheEnabled := envBool("VERTEXAI_ANTHROPIC_PROMPT_CACHE", false)
		if cacheTTL != "" {
			cacheEnabled = true
		}
		if cacheEnabled {
			if cacheTTL == "" {
				cacheTTL = "default"
			}
			fmt.Fprintf(os.Stderr, "gollem: vertexai-anthropic prompt cache enabled (type=ephemeral, ttl=%s)\n", cacheTTL)
		} else {
			fmt.Fprintf(os.Stderr, "gollem: vertexai-anthropic prompt cache disabled (set VERTEXAI_ANTHROPIC_PROMPT_CACHE=1)\n")
		}
	}

	// Apply middleware layers to the model.
	var mws []middleware.Middleware

	// Langfuse observability (if configured via environment).
	var langfuseProcessor *lf.BatchProcessor
	if os.Getenv("LANGFUSE_SECRET_KEY") != "" {
		lfOpts := []lf.Option{
			lf.WithKeys(os.Getenv("LANGFUSE_PUBLIC_KEY"), os.Getenv("LANGFUSE_SECRET_KEY")),
		}
		if baseURL := os.Getenv("LANGFUSE_BASE_URL"); baseURL != "" {
			lfOpts = append(lfOpts, lf.WithBaseURL(baseURL))
		}
		client := lf.New(lfOpts...)
		langfuseProcessor = lf.NewBatchProcessor(client)
		mws = append(mws, newLangfuseMiddleware(langfuseProcessor, f))
		fmt.Fprintf(os.Stderr, "gollem: langfuse tracing enabled\n")
	}

	// LangSmith observability (if configured via environment).
	var langsmithHandler *langsmith.Handler
	if os.Getenv("LANGSMITH_API_KEY") != "" {
		taskName := strings.TrimSpace(os.Getenv("GOLLEM_TASK_NAME"))
		traceID := uuid.New().String()

		lsOpts := []langsmith.Option{
			langsmith.WithTraceID(traceID),
		}
		if projectName := os.Getenv("LANGSMITH_PROJECT"); projectName != "" {
			lsOpts = append(lsOpts, langsmith.WithProjectName(projectName))
		} else {
			lsOpts = append(lsOpts, langsmith.WithProjectName("gollem"))
		}

		// Build tags for faceted search.
		var lsTags []string
		if taskName != "" {
			lsTags = append(lsTags, taskName)
		}
		if f.provider != "" {
			lsTags = append(lsTags, f.provider)
		}
		if f.modelName != "" {
			lsTags = append(lsTags, f.modelName)
		}
		if len(lsTags) > 0 {
			lsOpts = append(lsOpts, langsmith.WithTags(lsTags...))
		}

		// Attach rich metadata matching the Langfuse pattern.
		lsMeta := map[string]any{
			"provider":         f.provider,
			"model":            f.modelName,
			"task_name":        taskName,
			"timeout":          f.timeout.String(),
			"thinking_budget":  f.thinkingBudget,
			"reasoning_effort": f.reasoningEffort,
			"team_mode":        f.teamMode,
			"no_reasoning":     f.noReasoning,
			"prompt_preview":   truncate(f.prompt, 500),
		}
		if gitCommit != "" {
			lsMeta["git_commit"] = gitCommit
		}
		lsOpts = append(lsOpts, langsmith.WithMetadata(lsMeta))

		langsmithHandler = langsmith.New(lsOpts...)
		fmt.Fprintf(os.Stderr, "gollem: langsmith tracing enabled\n")
		fmt.Fprintf(os.Stderr, "gollem: langsmith trace_id=%s\n", traceID)
	}

	// Flush all trace backends on termination signals so traces survive
	// harness timeouts (SIGTERM before SIGKILL). Single handler avoids
	// the race of competing goroutines each calling os.Exit.
	if langfuseProcessor != nil || langsmithHandler != nil {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			sig := <-sigCh
			fmt.Fprintf(os.Stderr, "gollem: caught %v, flushing traces...\n", sig)
			if langfuseProcessor != nil {
				_ = langfuseProcessor.Close()
			}
			if langsmithHandler != nil {
				_ = langsmithHandler.Close()
			}
			os.Exit(1)
		}()
	}

	var model = baseModel
	if len(mws) > 0 {
		model = middleware.Wrap(model, mws...)
	}

	// Wrap with retry for API resilience (exponential backoff on 429/5xx).
	retryCfg := buildRetryConfig(f.provider, f.modelName, f.timeout)
	model = modelutil.NewRetryModel(model, retryCfg)
	fmt.Fprintf(os.Stderr, "gollem: retry config: max_retries=%d backoff=%v..%v min_remaining=%v\n",
		retryCfg.MaxRetries, retryCfg.InitialBackoff, retryCfg.MaxBackoff, retryCfg.MinRemaining)
	if closer, ok := model.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}

	// Build tool options, including code mode if enabled.
	var toolOpts []codetool.Option
	var runner *montygo.Runner
	if !f.noCodeMode {
		runner, err = montygo.New()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: code mode unavailable: %v\n", err)
		} else {
			toolOpts = append(toolOpts, codetool.WithCodeMode(runner))
			fmt.Fprintf(os.Stderr, "gollem: code mode enabled (monty-go WASM Python)\n")
		}
	}
	if runner != nil {
		defer runner.Close()
	}
	if f.toolPolicy != "" {
		toolOpts = append(toolOpts, codetool.WithToolPolicy(f.toolPolicy))
		fmt.Fprintf(os.Stderr, "gollem: tool policy enabled (%s)\n", f.toolPolicy)
	}

	// Pass the model so the coding agent can spawn subagents for delegation.
	toolOpts = append(toolOpts, codetool.WithModel(model))
	// Pass the budget timeout (real deadline) for time budget warnings.
	// This is separate from f.timeout (exec timeout) which may be shorter.
	toolOpts = append(toolOpts, codetool.WithTimeout(budgetTimeout))

	// Disable delegate tool if requested. Per-task override via
	// GOLLEM_DELEGATE_BY_TASK (format: "task1=1,task2=0,*=0") takes precedence
	// over the global GOLLEM_DISABLE_DELEGATE flag.
	delegateDisabled := isTruthyEnv("GOLLEM_DISABLE_DELEGATE")
	if os.Getenv("GOLLEM_DELEGATE_BY_TASK") != "" {
		enabled, source := resolveBoolByTask("GOLLEM_DELEGATE_BY_TASK", f.workDir, !delegateDisabled)
		delegateDisabled = !enabled
		if source != "" {
			fmt.Fprintf(os.Stderr, "gollem: delegate per-task: enabled=%v (%s)\n", enabled, source)
		}
	}
	if delegateDisabled {
		toolOpts = append(toolOpts, codetool.WithDisableDelegate())
		fmt.Fprintln(os.Stderr, "gollem: delegate tool disabled")
	}

	// LLM-routed team mode: classifier decides whether delegation overhead is worth it.
	// No task-name hardcoding; forced on/off still supported.
	teamEnabled, teamReason := decideTeamModeWithModel(f.teamMode, f.workDir, f.prompt, budgetTimeout, model)
	if teamEnabled {
		toolOpts = append(toolOpts, codetool.WithTeamMode())
		fmt.Fprintf(os.Stderr, "gollem: team mode enabled (%s)\n", teamReason)
	} else {
		fmt.Fprintf(os.Stderr, "gollem: team mode disabled (%s)\n", teamReason)
	}

	// Provider-aware auto-context limits. This flows through to BOTH the main
	// agent AND subagents via WithAutoContextConfig. Previously, only the main
	// agent got the override and subagents were stuck at 80K — wasting 70K of
	// Claude's context window on every subagent invocation.
	switch f.provider {
	case "anthropic", "vertexai-anthropic":
		toolOpts = append(toolOpts, codetool.WithAutoContextConfig(core.AutoContextConfig{
			MaxTokens: 150000,
			KeepLastN: 12,
		}))
		fmt.Fprintf(os.Stderr, "gollem: auto-context limit: 150K tokens (Claude) — main + subagents\n")
	case "vertexai":
		toolOpts = append(toolOpts, codetool.WithAutoContextConfig(core.AutoContextConfig{
			MaxTokens: 900000,
			KeepLastN: 20,
		}))
		fmt.Fprintf(os.Stderr, "gollem: auto-context limit: 900K tokens (Gemini 1M) — main + subagents\n")
	case "openai":
		toolOpts = append(toolOpts, codetool.WithAutoContextConfig(core.AutoContextConfig{
			MaxTokens: 350000,
			KeepLastN: 20,
		}))
		fmt.Fprintf(os.Stderr, "gollem: auto-context limit: 350K tokens (OpenAI 400K) — main + subagents\n")
	case "xai":
		toolOpts = append(toolOpts, codetool.WithAutoContextConfig(core.AutoContextConfig{
			MaxTokens: 900000,
			KeepLastN: 20,
		}))
		fmt.Fprintf(os.Stderr, "gollem: auto-context limit: 900K tokens (xAI 2M) — main + subagents\n")
	}

	// OpenAI reasoning profile: scale sandwich phases from the selected maximum.
	// If max is xhigh, implementation runs at high; if max is high, implementation
	// runs at medium. For task-specific overrides, GOLLEM_REASONING_NO_SANDWICH_BY_TASK
	// forces a flat profile (planning/implementation/verification all equal).
	if f.provider == "openai" && !f.noReasoning && f.reasoningEffort != "" {
		if disableGreedy, source := shouldDisableGreedyPressureByTask(f.workDir); disableGreedy {
			toolOpts = append(toolOpts, codetool.WithDisableGreedyThinkingPressure())
			fmt.Fprintf(os.Stderr, "gollem: time-budget greedy scaling disabled (%s)\n", source)
		}

		sandwichCfg := codetool.ReasoningSandwichConfigForMaxEffort(f.reasoningEffort)
		if disable, source := shouldDisableReasoningSandwichByTask(f.workDir); disable {
			sandwichCfg.Implementation = sandwichCfg.Planning
			sandwichCfg.Verification = sandwichCfg.Planning
			toolOpts = append(toolOpts, codetool.WithReasoningSandwichConfig(sandwichCfg))
			if source == "" {
				source = "task override"
			}
			fmt.Fprintf(os.Stderr, "gollem: reasoning sandwich disabled (%s): flat effort=%s\n",
				source,
				sandwichCfg.Planning.ReasoningEffort,
			)
		} else {
			toolOpts = append(toolOpts, codetool.WithReasoningSandwichConfig(sandwichCfg))
			fmt.Fprintf(os.Stderr, "gollem: reasoning sandwich profile (max=%s, impl=%s)\n",
				sandwichCfg.Planning.ReasoningEffort,
				sandwichCfg.Implementation.ReasoningEffort,
			)
		}
	}

	traceOut := strings.TrimSpace(f.traceOut)
	if traceOut == "" {
		traceOut = strings.TrimSpace(os.Getenv("GOLLEM_TRACE_OUT"))
	}
	traceStream := strings.TrimSpace(f.traceStream)
	if traceStream == "" {
		traceStream = strings.TrimSpace(os.Getenv("GOLLEM_TRACE_STREAM"))
	}
	traceEnabled := traceOut != "" || traceStream != ""
	traceMetadata := buildCLITraceMetadata(f, resumeSnapshot, resumeSnapshotPath)
	var traceSnapshots []*core.RunSnapshot
	var traceRuntimeRecorder *traceutil.RuntimeRecorder
	var traceEventBus *core.EventBus
	var traceStreamWriter *traceutil.JSONLStreamWriter
	if traceEnabled {
		traceEventBus = core.NewEventBus()
		defer traceEventBus.Close()
		traceRuntimeRecorder = traceutil.NewRuntimeRecorder(traceEventBus)
		defer traceRuntimeRecorder.Close()
		toolOpts = append(toolOpts, codetool.WithEventBus(traceEventBus))
		if traceStream != "" {
			var streamErr error
			traceStreamWriter, streamErr = traceutil.NewJSONLStreamWriter(traceStream)
			if streamErr != nil {
				fmt.Fprintf(os.Stderr, "error opening trace stream: %v\n", streamErr)
				os.Exit(1)
			}
			defer traceStreamWriter.Close()
			unsubStream := traceRuntimeRecorder.OnEvent(traceStreamWriter.WriteEvent)
			defer unsubStream()
			var registryMu sync.Mutex
			var registeredRunID string
			unsubStreamRegistry := traceRuntimeRecorder.OnEvent(func(event traceutil.Event) {
				if event.Kind != "run.started" {
					return
				}
				runID := strings.TrimSpace(event.AgentID)
				if runID == "" {
					return
				}
				registryMu.Lock()
				defer registryMu.Unlock()
				if registeredRunID != "" {
					return
				}
				registeredRunID = runID
				if err := writeLocalTraceStreamRegistry(traceOut, traceStream, runID, "running"); err != nil {
					fmt.Fprintf(os.Stderr, "warning: update live trace registry: %v\n", err)
				}
			})
			defer unsubStreamRegistry()
			fmt.Fprintf(os.Stderr, "gollem: live trace stream enabled (%s)\n", traceStream)
		}
	}

	// Build the coding agent with the full recommended setup.
	agentOpts := codetool.AgentOptions(f.workDir, toolOpts...)
	agentOpts = append(agentOpts, core.WithRunCondition[string](core.MaxRunDuration(f.timeout)))
	if traceEnabled {
		agentOpts = append(agentOpts, core.WithTracing[string]())
		agentOpts = append(agentOpts, core.WithEventBus[string](traceEventBus))
		agentOpts = append(agentOpts, core.WithHooks[string](core.Hook{
			OnTurnEnd: func(ctx context.Context, rc *core.RunContext, turnNumber int, response *core.ModelResponse) {
				if rc == nil {
					return
				}
				if snap := rc.RunStateSnapshot(); snap != nil {
					traceSnapshots = append(traceSnapshots, snap)
					core.PublishCheckpointCreated(traceEventBus, snap)
					if traceOut != "" && traceOut != "-" {
						if err := writeCLIPartialTrace(traceOut, traceMetadata, traceSnapshots, traceRuntimeRecorder, snap); err != nil {
							fmt.Fprintf(os.Stderr, "warning: write partial trace artifact: %v\n", err)
						}
					}
				}
			},
		}))
		if traceOut != "" {
			agentOpts = append(agentOpts, core.WithTraceExporter[string](&cliTraceFileExporter{
				path:     traceOut,
				metadata: traceMetadata,
				snapshots: func() []*core.RunSnapshot {
					return append([]*core.RunSnapshot(nil), traceSnapshots...)
				},
				recorder: traceRuntimeRecorder,
			}))
			fmt.Fprintf(os.Stderr, "gollem: trace artifact enabled (%s)\n", traceOut)
		}
	}

	// Add LangSmith hook if configured.
	if langsmithHandler != nil {
		agentOpts = append(agentOpts, core.WithHooks[string](langsmithHandler.Hook()))
	}

	// Optional top-level dynamic personality generation.
	// Per-task override via GOLLEM_PERSONALITY_BY_TASK (format: "task1=1,*=0")
	// takes precedence over the global GOLLEM_TOP_LEVEL_PERSONALITY flag.
	personalityEnabled := isTruthyEnv("GOLLEM_TOP_LEVEL_PERSONALITY")
	if os.Getenv("GOLLEM_PERSONALITY_BY_TASK") != "" {
		enabled, source := resolveBoolByTask("GOLLEM_PERSONALITY_BY_TASK", f.workDir, personalityEnabled)
		personalityEnabled = enabled
		if source != "" {
			fmt.Fprintf(os.Stderr, "gollem: personality per-task: enabled=%v (%s)\n", enabled, source)
		}
	}
	if personalityEnabled {
		personalityGen := modelutil.CachedPersonalityGenerator(modelutil.GeneratePersonality(model))
		agentOpts = append(agentOpts, core.WithDynamicSystemPrompt[string](func(ctx context.Context, rc *core.RunContext) (string, error) {
			req := modelutil.PersonalityRequest{
				Task:       rc.Prompt,
				Role:       "terminal coding agent",
				BasePrompt: "Prioritize verifier-defined success criteria, finish the user's original request instead of stopping at a clean checkpoint, and deliver the minimal correct fix quickly.",
				Context: map[string]string{
					"provider": f.provider,
					"model":    baseModel.ModelName(),
					"workdir":  f.workDir,
				},
			}
			generated, err := personalityGen(ctx, req)
			if err != nil {
				// Graceful fallback: keep static prompts if generation fails.
				fmt.Fprintf(os.Stderr, "gollem: top-level personality fallback: %v\n", err)
				return "", nil
			}
			fmt.Fprintf(os.Stderr, "gollem: top-level personality generated (%d chars)\n", len(generated))
			return generated, nil
		}))
		fmt.Fprintf(os.Stderr, "gollem: top-level dynamic personality enabled (GOLLEM_TOP_LEVEL_PERSONALITY=1)\n")
	}

	// Enable reasoning by default for providers that support it.
	// This is provider-agnostic: Anthropic uses adaptive thinking on
	// Claude 4.6+ (a manual ThinkingBudget on older models), OpenAI uses
	// ReasoningEffort. The reasoning sandwich middleware will vary these
	// per-turn for optimal performance.
	if !f.noReasoning {
		switch f.provider {
		case "anthropic", "vertexai-anthropic":
			if f.thinkingBudget < 0 && anthropicModelPrefersAdaptive(f.modelName) {
				// Claude 4.6+ default: adaptive thinking. A manual budget
				// is rejected outright on Opus 4.7+ and deprecated on 4.6,
				// so the budget default would make these models unusable
				// from the CLI. An explicit -thinking-budget still wins
				// below (and gets the provider's clear rejection on
				// adaptive-only models).
				agentOpts = append(agentOpts, core.WithAdaptiveThinking[string](true))
				maxTokens := defaultThinkingBudget + 16000
				agentOpts = append(agentOpts, core.WithMaxTokens[string](maxTokens))
				fmt.Fprintf(os.Stderr, "gollem: adaptive thinking enabled (max_tokens: %d)\n", maxTokens)
				break
			}
			if f.thinkingBudget < 0 {
				f.thinkingBudget = defaultThinkingBudget
			}
			if f.thinkingBudget > 0 {
				agentOpts = append(agentOpts, core.WithThinkingBudget[string](f.thinkingBudget))
				maxTokens := f.thinkingBudget + 16000
				agentOpts = append(agentOpts, core.WithMaxTokens[string](maxTokens))
				fmt.Fprintf(os.Stderr, "gollem: thinking enabled (budget: %d, max_tokens: %d)\n",
					f.thinkingBudget, maxTokens)
			}
		case "openai":
			// Auto-enable reasoning effort for models that support it:
			// - O-series models (o3, o4-mini, etc.): default to "high"
			// - Codex models (gpt-5.x-codex): default to "high"
			// Other OpenAI-compatible models (grok, together, etc.) may not
			// support the parameter, so don't enable by default.
			lowerModel := strings.ToLower(f.modelName)
			isOSeries := strings.HasPrefix(lowerModel, "o") && len(lowerModel) >= 2
			isCodex := strings.Contains(lowerModel, "codex")
			if isCodex {
				if f.reasoningEffort == "" {
					f.reasoningEffort = "high"
				}
				agentOpts = append(agentOpts, core.WithReasoningEffort[string](f.reasoningEffort))
				// Codex reasoning can consume output budget, so keep max_output high
				// for deeper chains and larger tool-heavy turns.
				//
				// reasoning tokens count toward max_output_tokens.
				agentOpts = append(agentOpts, core.WithMaxTokens[string](50000))
				agentOpts = append(agentOpts, core.WithTemperature[string](0))
				fmt.Fprintf(os.Stderr, "gollem: codex mode (reasoning: %s, max_output: 50000, temp: 0)\n", f.reasoningEffort)
			} else if isOSeries {
				if f.reasoningEffort == "" {
					f.reasoningEffort = "high"
				}
				agentOpts = append(agentOpts, core.WithReasoningEffort[string](f.reasoningEffort))
				fmt.Fprintf(os.Stderr, "gollem: reasoning effort enabled (%s)\n", f.reasoningEffort)
			} else if f.reasoningEffort != "" {
				agentOpts = append(agentOpts, core.WithReasoningEffort[string](f.reasoningEffort))
				fmt.Fprintf(os.Stderr, "gollem: reasoning effort enabled (%s)\n", f.reasoningEffort)
			}
		case "vertexai":
			// Gemini 2.5+ and 3.x support thinkingConfig.
			if f.thinkingBudget < 0 {
				f.thinkingBudget = defaultThinkingBudget
			}
			if f.thinkingBudget > 0 {
				agentOpts = append(agentOpts, core.WithThinkingBudget[string](f.thinkingBudget))
				// Keep Gemini output budget moderate by default to reduce long
				// 5+ minute turns that appear "hung" in benchmark logs.
				maxTokens := deriveGeminiMaxOutputTokens()
				agentOpts = append(agentOpts, core.WithMaxTokens[string](maxTokens))
				fmt.Fprintf(os.Stderr, "gollem: gemini thinking enabled (budget: %d, max_output: %d)\n",
					f.thinkingBudget, maxTokens)
			}
		default:
			if f.thinkingBudget > 0 {
				agentOpts = append(agentOpts, core.WithThinkingBudget[string](f.thinkingBudget))
				maxTokens := f.thinkingBudget + 16000
				agentOpts = append(agentOpts, core.WithMaxTokens[string](maxTokens))
				fmt.Fprintf(os.Stderr, "gollem: thinking enabled (budget: %d, max_tokens: %d)\n",
					f.thinkingBudget, maxTokens)
			}
		}
	}

	agent := core.NewAgent[string](model, agentOpts...)

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	fmt.Fprintf(os.Stderr, "gollem: running with %s in %s (timeout: %v)\n",
		baseModel.ModelName(), f.workDir, f.timeout)

	var runOpts []core.RunOption
	if resumeSnapshot != nil {
		runOpts = append(runOpts, core.WithSnapshot(resumeSnapshot))
	}
	if imageParts := detectPromptImageParts(f.prompt, f.workDir); len(imageParts) > 0 {
		if f.provider == "openai" {
			parts := make([]core.ModelRequestPart, 0, len(imageParts))
			for _, p := range imageParts {
				parts = append(parts, p)
			}
			runOpts = append(runOpts, core.WithInitialRequestParts(parts...))
			fmt.Fprintf(os.Stderr, "gollem: multimodal attached %d image(s) to initial prompt\n", len(imageParts))
		} else {
			fmt.Fprintf(os.Stderr, "gollem: multimodal images detected but provider %q does not support ImagePart yet\n", f.provider)
		}
	}

	result, err := agent.Run(ctx, f.prompt, runOpts...)

	// Flush traces before exiting.
	if langfuseProcessor != nil {
		_ = langfuseProcessor.Close()
	}
	if langsmithHandler != nil {
		_ = langsmithHandler.Close()
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Print the result.
	fmt.Println(result.Output)
	fmt.Fprintf(os.Stderr, "\ngollem: done (tokens: %d in, %d out, cache_read: %d, tools: %d)\n",
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.CacheReadTokens, result.Usage.ToolCalls)
}

// newLangfuseMiddleware creates a provider-level middleware that sends traces
// to Langfuse. It wraps each model request with a generation event containing
// token usage, timing, input messages, and output.
func newLangfuseMiddleware(processor *lf.BatchProcessor, f flags) middleware.Middleware {
	traceID := lf.NewID()
	taskName := strings.TrimSpace(os.Getenv("GOLLEM_TASK_NAME"))

	// Build rich metadata from runtime config.
	meta := map[string]any{
		"provider":         f.provider,
		"model":            f.modelName,
		"task_name":        taskName,
		"timeout":          f.timeout.String(),
		"thinking_budget":  f.thinkingBudget,
		"reasoning_effort": f.reasoningEffort,
		"team_mode":        f.teamMode,
		"no_reasoning":     f.noReasoning,
		"prompt_preview":   truncate(f.prompt, 500),
	}

	// Add environment-derived config (only non-empty values).
	envMeta := map[string]string{
		"openai_prompt_cache_key":        "OPENAI_PROMPT_CACHE_KEY",
		"openai_prompt_cache_retention":  "OPENAI_PROMPT_CACHE_RETENTION",
		"openai_service_tier":            "OPENAI_SERVICE_TIER",
		"openai_transport":               "OPENAI_TRANSPORT",
		"openai_websocket_http_fallback": "OPENAI_WEBSOCKET_HTTP_FALLBACK",
		"reasoning_by_task":              "GOLLEM_REASONING_BY_TASK",
		"reasoning_no_sandwich_by_task":  "GOLLEM_REASONING_NO_SANDWICH_BY_TASK",
		"reasoning_no_greedy_by_task":    "GOLLEM_REASONING_NO_GREEDY_BY_TASK",
		"model_request_timeout_sec":      "GOLLEM_MODEL_REQUEST_TIMEOUT_SEC",
		"top_level_personality":          "GOLLEM_TOP_LEVEL_PERSONALITY",
		"require_invariant_checklist":    "GOLLEM_REQUIRE_INVARIANT_CHECKLIST",
		"xhigh_tasks":                    "GOLLEM_XHIGH_TASKS",
	}
	for key, envVar := range envMeta {
		if v := os.Getenv(envVar); v != "" {
			meta[key] = v
		}
	}

	if gitCommit != "" {
		meta["git_commit"] = gitCommit
	}

	// Build tags for faceted search.
	tags := []string{}
	if taskName != "" {
		tags = append(tags, taskName)
	}
	if f.provider != "" {
		tags = append(tags, f.provider)
	}
	if f.modelName != "" {
		tags = append(tags, f.modelName)
	}

	traceName := taskName
	if traceName == "" {
		traceName = "gollem-run"
	}

	traceEvent := lf.TraceEvent{
		ID:        traceID,
		Name:      traceName,
		Timestamp: time.Now().UTC(),
		Metadata:  meta,
		Tags:      tags,
		Release:   gitCommit,
		Version:   f.modelName,
		Input:     map[string]any{"prompt": f.prompt},
		SessionID: taskName,
	}
	processor.Enqueue(traceEvent)
	fmt.Fprintf(os.Stderr, "gollem: langfuse trace_id=%s\n", traceID)

	turn := 0
	return middleware.Func(func(next middleware.RequestFunc) middleware.RequestFunc {
		return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
			turn++
			genID := lf.NewID()
			startTime := time.Now().UTC()

			// Capture input: summarize messages for Langfuse.
			input := langfuseSummarizeMessages(messages)

			resp, err := next(ctx, messages, settings, params)

			endTime := time.Now().UTC()
			gen := lf.GenerationEvent{
				ID:        genID,
				TraceID:   traceID,
				Name:      fmt.Sprintf("turn-%d", turn),
				StartTime: startTime,
				EndTime:   &endTime,
				Input:     input,
			}

			if err == nil && resp != nil {
				gen.Model = resp.ModelName
				gen.Output = langfuseSummarizeResponse(resp)
				gen.Usage = &lf.Usage{
					Input:  resp.Usage.InputTokens,
					Output: resp.Usage.OutputTokens,
					Total:  resp.Usage.TotalTokens(),
					Unit:   "TOKENS",
				}
			} else if err != nil {
				gen.Level = "ERROR"
				gen.StatusMessage = err.Error()
			}

			processor.Enqueue(gen)
			return resp, err
		}
	})
}

// langfuseSummarizeMessages extracts a simplified view of messages for tracing.
func langfuseSummarizeMessages(messages []core.ModelMessage) []map[string]any {
	var result []map[string]any
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.ModelRequest:
			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.SystemPromptPart:
					result = append(result, map[string]any{"role": "system", "content": p.Content})
				case core.UserPromptPart:
					result = append(result, map[string]any{"role": "user", "content": p.Content})
				case core.ToolReturnPart:
					result = append(result, map[string]any{"role": "tool", "tool": p.ToolName, "content": fmt.Sprintf("%v", p.Content)})
				case core.RetryPromptPart:
					result = append(result, map[string]any{"role": "retry", "content": p.Content})
				}
			}
		case core.ModelResponse:
			entry := map[string]any{"role": "assistant", "content": m.TextContent()}
			var tools []map[string]string
			for _, part := range m.Parts {
				if tc, ok := part.(core.ToolCallPart); ok {
					tools = append(tools, map[string]string{"tool": tc.ToolName, "args": tc.ArgsJSON})
				}
			}
			if len(tools) > 0 {
				entry["tool_calls"] = tools
			}
			result = append(result, entry)
		}
	}
	return result
}

// langfuseSummarizeResponse extracts text + tool calls from a model response.
func langfuseSummarizeResponse(resp *core.ModelResponse) map[string]any {
	out := map[string]any{}
	if text := resp.TextContent(); text != "" {
		out["text"] = text
	}
	var tools []map[string]string
	for _, part := range resp.Parts {
		if tc, ok := part.(core.ToolCallPart); ok {
			tools = append(tools, map[string]string{"tool": tc.ToolName, "args": tc.ArgsJSON})
		}
	}
	if len(tools) > 0 {
		out["tool_calls"] = tools
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func runDebug() {
	f := parseFlags(os.Args[2:])
	provider, modelName, prompt := f.provider, f.modelName, f.prompt

	if prompt == "" {
		fmt.Fprintln(os.Stderr, "error: prompt is required")
		printUsage()
		os.Exit(1)
	}

	if provider == "" {
		provider = "test"
	}

	model, err := createModel(provider, modelName, f.location, f.project, deriveRequestTimeout(f.timeout))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating model: %v\n", err)
		os.Exit(1)
	}

	agent := core.NewAgent[string](model,
		core.WithSystemPrompt[string]("You are a helpful assistant."),
	)

	result, err := tui.DebugUI(agent, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if result != nil {
		fmt.Printf("\nResult: %s\n", result.Output)
		fmt.Printf("Tokens: %d input, %d output\n",
			result.Usage.InputTokens, result.Usage.OutputTokens)
	}
}

func loadRunSnapshotFile(path string) (*core.RunSnapshot, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("snapshot path is required")
	}
	//nolint:gosec // Snapshot path is explicitly supplied by the local operator.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	snap, err := core.UnmarshalSnapshot(data)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

type cliTraceFileExporter struct {
	path      string
	metadata  map[string]any
	snapshots func() []*core.RunSnapshot
	recorder  *traceutil.RuntimeRecorder
}

func (e *cliTraceFileExporter) Export(_ context.Context, runTrace *core.RunTrace) error {
	if e == nil {
		return errors.New("cli trace exporter: nil exporter")
	}
	if runTrace == nil {
		return errors.New("cli trace exporter: nil run trace")
	}
	var runtimeEvents []traceutil.Event
	if e.recorder != nil {
		runtimeEvents = e.recorder.EventsForTrace(runTrace.RunID)
	}
	var snapshots []*core.RunSnapshot
	if e.snapshots != nil {
		snapshots = e.snapshots()
	}
	artifact, err := traceutil.FromRunTraceWithSnapshotsAndEvents(runTrace, snapshots, runtimeEvents, cloneTraceMetadata(e.metadata))
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: build trace artifact: %v\n", err)
		return err
	}
	if err := traceutil.WriteFile(e.path, artifact); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write trace artifact: %v\n", err)
		return err
	}
	if err := writeLocalTraceRegistry(e.path, runTrace.RunID, artifact.Summary.Status); err != nil {
		fmt.Fprintf(os.Stderr, "warning: update local trace registry: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "gollem: trace artifact written to %s\n", e.path)
	return nil
}

func writeCLIPartialTrace(path string, metadata map[string]any, snapshots []*core.RunSnapshot, recorder *traceutil.RuntimeRecorder, snap *core.RunSnapshot) error {
	if strings.TrimSpace(path) == "" || path == "-" {
		return nil
	}
	if snap == nil {
		return errors.New("partial trace snapshot is nil")
	}
	ended := snap.Timestamp
	if ended.IsZero() {
		ended = time.Now()
	}
	started := snap.RunStartTime
	if started.IsZero() {
		started = ended
	}
	runID := strings.TrimSpace(snap.RunID)
	if runID == "" {
		runID = "running"
	}
	trace := &core.RunTrace{
		RunID:     runID,
		Prompt:    snap.Prompt,
		StartTime: started,
		Duration:  ended.Sub(started),
		Usage:     snap.Usage,
		Success:   false,
	}
	if trace.Duration < 0 {
		trace.Duration = 0
	}
	var runtimeEvents []traceutil.Event
	if recorder != nil {
		runtimeEvents = recorder.EventsForTrace(runID)
	}
	partialMetadata := cloneTraceMetadata(metadata)
	partialMetadata["partial"] = true
	partialMetadata["partial_reason"] = "running"
	artifact, err := traceutil.FromRunTraceWithSnapshotsAndEvents(trace, snapshots, runtimeEvents, partialMetadata)
	if err != nil {
		return err
	}
	artifact.Summary.Status = "running"
	artifact.Summary.Success = false
	artifact.Summary.Error = ""
	if err := traceutil.WriteFile(path, artifact); err != nil {
		return err
	}
	return writeLocalTraceRegistry(path, runID, "running")
}

func buildCLITraceMetadata(f flags, resumeSnapshot *core.RunSnapshot, resumeSnapshotPath string) map[string]any {
	metadata := map[string]any{
		"provider": f.provider,
		"model":    f.modelName,
		"topology": f.teamMode,
		"workdir":  f.workDir,
	}
	if f.toolPolicy != "" {
		metadata["tool_policy"] = f.toolPolicy
	}
	if f.evaluator != "" {
		metadata["evaluator"] = map[string]any{"name": f.evaluator}
	}
	if resumeSnapshot != nil {
		metadata["resume_snapshot"] = resumeSnapshotPath
		metadata["resume_parent_run_id"] = resumeSnapshot.ParentRunID
		metadata["resume_step"] = resumeSnapshot.RunStep
		metadata["resume_trace_policy"] = "fresh_segment"
		if resumeSnapshot.SourceTraceRunID != "" {
			metadata["resume_source_trace_run_id"] = resumeSnapshot.SourceTraceRunID
		}
		if resumeSnapshot.SourceSnapshotID != "" {
			metadata["resume_source_snapshot_id"] = resumeSnapshot.SourceSnapshotID
		}
	}
	return metadata
}

func cloneTraceMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

type resumeSnapshotOverrides struct {
	Applied []string
}

func applyResumeSnapshotOverrides(f *flags, snap *core.RunSnapshot) (resumeSnapshotOverrides, error) {
	var applied resumeSnapshotOverrides
	if f == nil || snap == nil {
		return applied, nil
	}
	overrides := snapshotForkOverrides(snap)
	if len(overrides) == 0 {
		return applied, nil
	}

	if model := overrides["model"]; model != "" {
		if !f.modelExplicit {
			f.modelName = model
			applied.Applied = append(applied.Applied, "model="+model)
		}
	}
	if topology := overrides["topology"]; topology != "" {
		mode, err := forkTopologyTeamMode(topology)
		if err != nil {
			return applied, err
		}
		if mode != "" && !f.teamExplicit {
			f.teamMode = mode
			applied.Applied = append(applied.Applied, "topology="+topology)
		}
	}
	if policy := overrides["tool_policy"]; policy != "" {
		appliedPolicy, err := applyForkToolPolicy(f, policy)
		if err != nil {
			return applied, err
		}
		if appliedPolicy != "" {
			applied.Applied = append(applied.Applied, "tool_policy="+appliedPolicy)
		}
	}
	if middleware := overrides["middleware"]; middleware != "" {
		appliedMiddleware, err := applyForkMiddleware(f, middleware)
		if err != nil {
			return applied, err
		}
		if appliedMiddleware != "" {
			applied.Applied = append(applied.Applied, "middleware="+appliedMiddleware)
		}
	}
	if evaluator := overrides["evaluator"]; evaluator != "" {
		f.evaluator = evaluator
		applied.Applied = append(applied.Applied, "evaluator="+evaluator)
	}
	return applied, nil
}

func snapshotForkOverrides(snap *core.RunSnapshot) map[string]string {
	if snap == nil || len(snap.ToolState) == 0 {
		return nil
	}
	raw, ok := snap.ToolState["_gollem_fork_overrides"]
	if !ok || raw == nil {
		return nil
	}
	out := make(map[string]string)
	switch overrides := raw.(type) {
	case map[string]any:
		for key, value := range overrides {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" {
				out[key] = text
			}
		}
	case map[string]string:
		for key, value := range overrides {
			if text := strings.TrimSpace(value); text != "" {
				out[key] = text
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func forkTopologyTeamMode(topology string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(topology)) {
	case "", "default":
		return "", nil
	case "auto":
		return "auto", nil
	case "team", "teams", "team-mode", "multi-agent", "multiagent", "on":
		return "on", nil
	case "single", "solo", "single-agent", "singleagent", "local", "off":
		return "off", nil
	default:
		return "", fmt.Errorf("unsupported fork topology override %q (supported: team, single, auto)", topology)
	}
}

func applyForkToolPolicy(f *flags, policy string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", "default":
		return "", nil
	case "read-only", "readonly", "read_only", "no-mutation", "no_mutation":
		f.toolPolicy = "read-only"
		return "read-only", nil
	case "no-code", "no-code-mode", "no_code", "no_code_mode":
		f.noCodeMode = true
		return "no-code-mode", nil
	default:
		return "", fmt.Errorf("unsupported fork tool policy override %q (supported: read-only, no-code-mode)", policy)
	}
}

func applyForkMiddleware(f *flags, middleware string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(middleware)) {
	case "", "default", "none":
		return "", nil
	case "no-reasoning", "disable-reasoning":
		f.noReasoning = true
		return "no-reasoning", nil
	case "no-code", "no-code-mode", "disable-code-mode":
		f.noCodeMode = true
		return "no-code-mode", nil
	default:
		return "", fmt.Errorf("unsupported fork middleware override %q (supported: no-reasoning, no-code-mode)", middleware)
	}
}

// detectTaskTimeout reads the agent timeout from task.toml (Terminal-Bench format).
// This ensures the agent's time budget warnings match the actual deadline.
func detectTaskTimeout(workDir string) time.Duration {
	candidates := []string{
		workDir + "/task.toml",
		"/app/task_file/task.toml",
	}
	for _, path := range candidates {
		//nolint:gosec // Paths are fixed or derived from local workspace root.
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		inAgentSection := false
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "[agent]" {
				inAgentSection = true
				continue
			}
			if strings.HasPrefix(trimmed, "[") {
				inAgentSection = false
				continue
			}
			if inAgentSection && strings.Contains(trimmed, "timeout_sec") {
				parts := strings.SplitN(trimmed, "=", 2)
				if len(parts) == 2 {
					var secs float64
					if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%f", &secs); err == nil && secs > 0 {
						return time.Duration(secs) * time.Second
					}
				}
			}
		}
	}
	return 0
}

func detectPromptImageParts(prompt, workDir string) []core.ImagePart {
	candidates := imagePathCandidatesFromPrompt(prompt)
	if len(candidates) == 0 && promptMentionsImageCue(prompt) {
		// Conservative fallback: if task text clearly references an image and
		// exactly one image exists in the working directory root, attach it.
		entries, err := os.ReadDir(workDir)
		if err == nil {
			var rootImages []string
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				name := entry.Name()
				ext := strings.ToLower(filepath.Ext(name))
				if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif" || ext == ".bmp" {
					rootImages = append(rootImages, name)
				}
			}
			if len(rootImages) == 1 {
				candidates = append(candidates, rootImages[0])
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	maxBytes := promptImageMaxBytes()
	seen := make(map[string]struct{}, len(candidates))
	parts := make([]core.ImagePart, 0, len(candidates))

	for _, candidate := range candidates {
		path, ok := resolvePromptImagePath(workDir, candidate)
		if !ok {
			fmt.Fprintf(os.Stderr, "gollem: multimodal skip image %q (outside workdir)\n", candidate)
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}

		//nolint:gosec // path is validated by resolvePromptImagePath(workDir, candidate).
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if maxBytes > 0 && info.Size() > maxBytes {
			fmt.Fprintf(os.Stderr, "gollem: multimodal skip image %s (size=%d exceeds max=%d)\n", path, info.Size(), maxBytes)
			continue
		}

		//nolint:gosec // path is validated by resolvePromptImagePath(workDir, candidate).
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gollem: multimodal skip image %s (read error: %v)\n", path, err)
			continue
		}

		mimeType := imageMIMEType(path)
		parts = append(parts, core.ImagePart{
			URL:       core.BinaryContent(data, mimeType),
			MIMEType:  mimeType,
			Detail:    "high",
			Timestamp: time.Now(),
		})
		fmt.Fprintf(os.Stderr, "gollem: multimodal attached image %s (%d bytes)\n", path, len(data))
	}

	return parts
}

func resolvePromptImagePath(workDir, candidate string) (string, bool) {
	if strings.TrimSpace(workDir) == "" || strings.TrimSpace(candidate) == "" {
		return "", false
	}
	rootAbs, err := filepath.Abs(workDir)
	if err != nil {
		return "", false
	}
	rootAbs = filepath.Clean(rootAbs)

	path := candidate
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootAbs, path)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	pathAbs = filepath.Clean(pathAbs)

	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return pathAbs, true
}

func imagePathCandidatesFromPrompt(prompt string) []string {
	fields := strings.Fields(prompt)
	candidates := make([]string, 0, 2)
	for _, field := range fields {
		token := strings.Trim(field, "\"'`.,:;!?()[]{}<>")
		if token == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(token))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp":
			candidates = append(candidates, token)
		}
	}
	return candidates
}

func promptMentionsImageCue(prompt string) bool {
	lower := strings.ToLower(prompt)
	return strings.Contains(lower, "image") ||
		strings.Contains(lower, "photo") ||
		strings.Contains(lower, "picture")
}

func promptImageMaxBytes() int64 {
	const defaultMax = int64(10 * 1024 * 1024) // 10MB
	raw := strings.TrimSpace(os.Getenv("GOLLEM_PROMPT_IMAGE_MAX_BYTES"))
	if raw == "" {
		return defaultMax
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return defaultMax
	}
	return n
}

func imageMIMEType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	mimeType := mime.TypeByExtension(ext)
	if idx := strings.Index(mimeType, ";"); idx >= 0 {
		mimeType = mimeType[:idx]
	}
	if strings.HasPrefix(mimeType, "image/") {
		return mimeType
	}
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	default:
		return "image/png"
	}
}

type teamModeLLMDecision struct {
	EnableTeam      bool     `json:"enable_team" jsonschema:"description=Set true when Team mode should be enabled."`
	ComplexityScore int      `json:"complexity_score" jsonschema:"description=Estimated task complexity from 0 to 10,minimum=0,maximum=10"`
	Confidence      string   `json:"confidence" jsonschema:"description=Confidence level: low, medium, or high"`
	Reasons         []string `json:"reasons" jsonschema:"description=Up to 4 short reasons"`
}

const teamModeScoreOverrideThreshold = 8

func normalizeTeamMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		return "auto"
	case "on", "true", "1", "yes":
		return "on"
	case "off", "false", "0", "no":
		return "off"
	default:
		return ""
	}
}

func normalizeReasoningEffortLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	default:
		return ""
	}
}

func effortAboveHigh(level string) bool {
	return normalizeReasoningEffortLevel(level) == "xhigh"
}

func parseReasoningByTask(raw string) map[string]string {
	m := make(map[string]string)
	raw = trimMatchingQuotes(strings.TrimSpace(raw))
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k = trimMatchingQuotes(strings.TrimSpace(k))
		v = normalizeReasoningEffortLevel(trimMatchingQuotes(v))
		if k == "" || v == "" {
			continue
		}
		m[k] = v
	}
	return m
}

func parseTaskSelectorSet(raw string) map[string]struct{} {
	m := make(map[string]struct{})
	raw = trimMatchingQuotes(strings.TrimSpace(raw))
	if raw == "" {
		return m
	}
	raw = strings.ReplaceAll(raw, ",", " ")
	for _, token := range strings.Fields(raw) {
		task := trimMatchingQuotes(strings.TrimSpace(token))
		if task == "" {
			continue
		}
		m[task] = struct{}{}
	}
	return m
}

func trimMatchingQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func resolveReasoningTaskName(workDir string) string {
	if taskName := strings.TrimSpace(os.Getenv("GOLLEM_TASK_NAME")); taskName != "" {
		return taskName
	}
	// Fallback for non-Harbor runs where workdir may be task-specific.
	base := strings.TrimSpace(filepath.Base(filepath.Clean(workDir)))
	switch base {
	case "", ".", "/", "app":
		return ""
	default:
		return base
	}
}

func resolveReasoningEffortByTask(workDir, current string) (string, string) {
	current = normalizeReasoningEffortLevel(current)
	raw := strings.TrimSpace(os.Getenv("GOLLEM_REASONING_BY_TASK"))
	if raw == "" {
		return current, ""
	}
	mapping := parseReasoningByTask(raw)
	if len(mapping) == 0 {
		return current, ""
	}

	taskName := resolveReasoningTaskName(workDir)
	if taskName != "" {
		if v, ok := mapping[taskName]; ok {
			return v, "task:" + taskName
		}
	}
	if v, ok := mapping["*"]; ok {
		return v, "task:*"
	}
	return current, ""
}

func shouldDisableReasoningSandwichByTask(workDir string) (bool, string) {
	raw := strings.TrimSpace(os.Getenv("GOLLEM_REASONING_NO_SANDWICH_BY_TASK"))
	if raw == "" {
		return false, ""
	}
	taskSet := parseTaskSelectorSet(raw)
	if len(taskSet) == 0 {
		return false, ""
	}
	taskName := resolveReasoningTaskName(workDir)
	if taskName != "" {
		if _, ok := taskSet[taskName]; ok {
			return true, "task:" + taskName
		}
	}
	if _, ok := taskSet["*"]; ok {
		return true, "task:*"
	}
	return false, ""
}

// resolveBoolByTask looks up a per-task boolean from an env var with format
// "task1=1,task2=0,*=0". Returns the resolved value and a source string for
// logging. Falls back to defaultVal if env var is empty or task not matched.
func resolveBoolByTask(envVar, workDir string, defaultVal bool) (bool, string) {
	raw := strings.TrimSpace(os.Getenv(envVar))
	if raw == "" {
		return defaultVal, ""
	}
	raw = trimMatchingQuotes(raw)
	m := make(map[string]string)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			m[k] = v
		}
	}
	if len(m) == 0 {
		return defaultVal, ""
	}
	taskName := resolveReasoningTaskName(workDir)
	if taskName != "" {
		if v, ok := m[taskName]; ok {
			return v == "1" || strings.EqualFold(v, "true"), "task:" + taskName
		}
	}
	if v, ok := m["*"]; ok {
		return v == "1" || strings.EqualFold(v, "true"), "task:*"
	}
	return defaultVal, ""
}

func shouldDisableGreedyPressureByTask(workDir string) (bool, string) {
	raw := strings.TrimSpace(os.Getenv("GOLLEM_REASONING_NO_GREEDY_BY_TASK"))
	if raw == "" {
		return false, ""
	}
	taskSet := parseTaskSelectorSet(raw)
	if len(taskSet) == 0 {
		return false, ""
	}
	taskName := resolveReasoningTaskName(workDir)
	if taskName != "" {
		if _, ok := taskSet[taskName]; ok {
			return true, "task:" + taskName
		}
	}
	if _, ok := taskSet["*"]; ok {
		return true, "task:*"
	}
	return false, ""
}

func deriveOpenAIReasoningEffort(modelName, current string) string {
	if v := normalizeReasoningEffortLevel(current); v != "" {
		return v
	}
	lowerModel := strings.ToLower(strings.TrimSpace(modelName))
	isCodex := strings.Contains(lowerModel, "codex")
	isOSeries := strings.HasPrefix(lowerModel, "o") && len(lowerModel) >= 2
	if isCodex || isOSeries {
		return "high"
	}
	return ""
}

// isTruthyEnv checks if an environment variable is set to a truthy value.
// Mirrors codetool.envEnabled — kept local to avoid a dependency on ext/codetool
// from cmd/gollem (which would create a circular-ish layering concern).
func isTruthyEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func decideTeamModeWithModel(mode, workDir, prompt string, budgetTimeout time.Duration, model core.Model) (bool, string) {
	normalized := normalizeTeamMode(mode)
	if normalized == "" {
		normalized = "auto"
	}
	switch normalized {
	case "on":
		return true, "forced by --team-mode=on (or GOLLEM_TEAM_MODE=on)"
	case "off":
		return false, "forced by --team-mode=off (or GOLLEM_TEAM_MODE=off)"
	default:
		instruction := loadTaskInstruction(workDir)
		workspaceFiles := boundedWorkspaceFileCount(workDir, 160)

		llmDecision, err := classifyTeamModeAuto(model, prompt, instruction, budgetTimeout, workspaceFiles)
		if err != nil {
			return false, fmt.Sprintf("auto llm-classifier-error (%s)", compactError(err, 120))
		}

		scoreOverride := llmDecision.ComplexityScore >= teamModeScoreOverrideThreshold
		if llmDecision.Confidence == "low" && !scoreOverride {
			return false, fmt.Sprintf(
				"auto llm-low-confidence score=%d reasons=%s",
				llmDecision.ComplexityScore,
				strings.Join(llmDecision.Reasons, ", "),
			)
		}

		enabled := llmDecision.EnableTeam
		if scoreOverride {
			enabled = true
		}
		shortTimeoutGuard := false
		if budgetTimeout > 0 && budgetTimeout < 10*time.Minute && llmDecision.ComplexityScore < teamModeScoreOverrideThreshold {
			enabled = false
			shortTimeoutGuard = true
		}

		reason := fmt.Sprintf(
			"llm score=%d confidence=%s reasons=%s",
			llmDecision.ComplexityScore,
			llmDecision.Confidence,
			strings.Join(llmDecision.Reasons, ", "),
		)
		if shortTimeoutGuard {
			reason += ", short-timeout-guard"
		}
		if scoreOverride {
			reason += ", score-override"
		}
		return enabled, reason
	}
}

func classifyTeamModeAuto(
	model core.Model,
	prompt string,
	instruction string,
	budgetTimeout time.Duration,
	workspaceFileCount int,
) (teamModeLLMDecision, error) {
	var zero teamModeLLMDecision
	if model == nil {
		return zero, errors.New("model unavailable")
	}

	classifierCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	agent := core.NewAgent[teamModeLLMDecision](
		model,
		core.WithSystemPrompt[teamModeLLMDecision](
			"You are a routing classifier. Decide whether Team mode (delegation to multiple sub-agents) is worth the overhead for this coding task. Prefer false for simple or narrow tasks. Return only the structured result.",
		),
		core.WithMaxRetries[teamModeLLMDecision](0),
		core.WithUsageLimits[teamModeLLMDecision](core.UsageLimits{
			RequestLimit: core.IntPtr(1),
		}),
		core.WithMaxTokens[teamModeLLMDecision](900),
	)

	result, err := agent.Run(classifierCtx, buildTeamModeClassifierPrompt(prompt, instruction, budgetTimeout, workspaceFileCount))
	if err != nil {
		return zero, err
	}
	return sanitizeTeamModeLLMDecision(result.Output), nil
}

func sanitizeTeamModeLLMDecision(decision teamModeLLMDecision) teamModeLLMDecision {
	if decision.ComplexityScore < 0 {
		decision.ComplexityScore = 0
	}
	if decision.ComplexityScore > 10 {
		decision.ComplexityScore = 10
	}

	decision.Confidence = strings.ToLower(strings.TrimSpace(decision.Confidence))
	switch decision.Confidence {
	case "high", "medium", "low":
	default:
		decision.Confidence = "low"
	}

	cleanedReasons := make([]string, 0, len(decision.Reasons))
	for _, reason := range decision.Reasons {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			continue
		}
		if len(reason) > 100 {
			reason = reason[:100]
		}
		cleanedReasons = append(cleanedReasons, reason)
		if len(cleanedReasons) >= 4 {
			break
		}
	}
	decision.Reasons = cleanedReasons
	if len(decision.Reasons) == 0 {
		decision.Reasons = []string{"no reasons provided"}
	}
	return decision
}

func buildTeamModeClassifierPrompt(
	prompt string,
	instruction string,
	budgetTimeout time.Duration,
	workspaceFileCount int,
) string {
	prompt = trimForClassifier(prompt, 2500)
	instruction = trimForClassifier(instruction, 6000)

	timeoutLabel := "unknown"
	if budgetTimeout > 0 {
		timeoutLabel = budgetTimeout.String()
	}

	return fmt.Sprintf(
		"Classify this task for Team mode routing.\n\n"+
			"Team mode means spawning specialized sub-agents and coordinating their work. It helps on complex multi-step tasks but adds overhead.\n\n"+
			"Routing target: set enable_team=true only when expected coordination benefits outweigh overhead.\n\n"+
			"Task prompt:\n%s\n\n"+
			"Instruction excerpt:\n%s\n\n"+
			"Signals:\n- timeout_budget=%s\n- workspace_file_count=%d",
		prompt,
		instruction,
		timeoutLabel,
		workspaceFileCount,
	)
}

func trimForClassifier(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max]
}

func compactError(err error, max int) string {
	if err == nil {
		return ""
	}
	s := strings.TrimSpace(err.Error())
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max]
}

func loadTaskInstruction(workDir string) string {
	candidates := []string{
		filepath.Join(workDir, "instruction.md"),
		filepath.Join(workDir, "task_file", "instruction.md"),
		"/app/instruction.md",
		"/app/task_file/instruction.md",
		filepath.Join(workDir, "README.md"),
		"/app/README.md",
	}

	for _, path := range candidates {
		if path == "" {
			continue
		}
		//nolint:gosec // Paths are fixed or derived from local workspace root.
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			continue
		}
		// Cap for predictable startup overhead; enough for signal extraction.
		if len(data) > 20000 {
			data = data[:20000]
		}
		return string(data)
	}
	return ""
}

func boundedWorkspaceFileCount(workDir string, max int) int {
	if workDir == "" || max <= 0 {
		return 0
	}

	count := 0
	stop := errors.New("stop-count")
	skipDirs := map[string]bool{
		".git":         true,
		".venv":        true,
		"venv":         true,
		"node_modules": true,
		"__pycache__":  true,
		"dist":         true,
		"build":        true,
		".cache":       true,
	}

	//nolint:gosec // workDir is the local workspace root path provided by the runner.
	err := filepath.WalkDir(workDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && path != workDir {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		if count >= max {
			return stop
		}
		return nil
	})

	if err != nil && !errors.Is(err, stop) {
		return count
	}
	return count
}

func detectProvider() string {
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "anthropic"
	}
	if os.Getenv("XAI_API_KEY") != "" {
		return "xai"
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		if strings.HasPrefix(key, "xai-") {
			return "xai"
		}
		return "openai"
	}
	if os.Getenv("GOOGLE_CLOUD_PROJECT") != "" {
		return "vertexai"
	}
	if os.Getenv(openai.DeepSeekAPIKeyEnv) != "" {
		return "deepseek"
	}
	return ""
}

// anthropicModelPrefersAdaptive reports whether the selected Claude model
// should default to adaptive thinking instead of a manual budget: the 4.6
// generation and newer (Opus/Sonnet 4.6, Opus 4.7/4.8, Fable, Mythos, and
// the Claude 5 generation). Manual budgets are rejected on 4.7+ and Claude 5
// and deprecated on 4.6. An empty model means the provider default, which is
// adaptive-capable. The rule itself lives in the anthropic provider so the
// CLI and the app-server catalog cannot drift from the request builder.
func anthropicModelPrefersAdaptive(model string) bool {
	if strings.TrimSpace(model) == "" {
		return true
	}
	return anthropic.SupportsAdaptiveThinking(model)
}

func deriveGeminiMaxOutputTokens() int {
	const (
		defaultMax = 12000
		minMax     = 1024
		hardMax    = 40000
	)
	raw := strings.TrimSpace(os.Getenv("GOLLEM_GEMINI_MAX_OUTPUT_TOKENS"))
	if raw == "" {
		return defaultMax
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultMax
	}
	if n < minMax {
		return minMax
	}
	if n > hardMax {
		return hardMax
	}
	return n
}

func buildRetryConfig(provider, modelName string, runTimeout time.Duration) modelutil.RetryConfig {
	cfg := modelutil.DefaultRetryConfig()

	switch {
	case runTimeout <= 5*time.Minute:
		cfg.MaxRetries = 1
		cfg.InitialBackoff = 500 * time.Millisecond
		cfg.MaxBackoff = 2 * time.Second
	case runTimeout <= 12*time.Minute:
		cfg.MaxRetries = 2
		cfg.InitialBackoff = 500 * time.Millisecond
		cfg.MaxBackoff = 5 * time.Second
	default:
		cfg.MaxRetries = 3
		cfg.InitialBackoff = 1 * time.Second
		cfg.MaxBackoff = 8 * time.Second
	}

	// Gemini on Vertex is more prone to transient 429/deadline spikes under
	// benchmark load; allow more retry attempts before giving up.
	if provider == "vertexai" && strings.Contains(strings.ToLower(modelName), "gemini") {
		switch {
		case runTimeout <= 5*time.Minute:
			if cfg.MaxRetries < 2 {
				cfg.MaxRetries = 2
			}
		case runTimeout <= 12*time.Minute:
			if cfg.MaxRetries < 4 {
				cfg.MaxRetries = 4
			}
		default:
			if cfg.MaxRetries < 6 {
				cfg.MaxRetries = 6
			}
		}
		if cfg.MaxBackoff < 12*time.Second {
			cfg.MaxBackoff = 12 * time.Second
		}
	}

	cfg.MinRemaining = 20 * time.Second
	// Emit periodic wait telemetry while a provider call is in-flight.
	cfg.HeartbeatInterval = 30 * time.Second
	return cfg
}

func deriveRequestTimeout(runTimeout time.Duration) time.Duration {
	// Allow explicit override in seconds for benchmark tuning.
	if raw := strings.TrimSpace(os.Getenv("GOLLEM_MODEL_REQUEST_TIMEOUT_SEC")); raw != "" {
		if secs, err := strconv.ParseFloat(raw, 64); err == nil && secs > 0 {
			override := time.Duration(secs * float64(time.Second))
			// Avoid a single call consuming the entire run.
			if runTimeout > 0 && override > runTimeout-10*time.Second {
				override = runTimeout - 10*time.Second
			}
			if override < 30*time.Second {
				override = 30 * time.Second
			}
			return override
		}
	}

	// Keep per-request timeouts much shorter than the full run timeout so
	// a single hung provider call doesn't consume the whole benchmark budget.
	timeout := runTimeout / 5
	if timeout < 60*time.Second {
		timeout = 60 * time.Second
	}
	if timeout > 6*time.Minute {
		timeout = 6 * time.Minute
	}
	if runTimeout > 0 && timeout > runTimeout-10*time.Second {
		timeout = runTimeout - 10*time.Second
	}
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}
	return timeout
}

func createModel(provider, modelName, location, project string, requestTimeout time.Duration) (core.Model, error) {
	return createModelWith(nil, provider, modelName, location, project, requestTimeout)
}

// createModelWith is createModel with an explicit catalog. The catalog
// resolves an empty model to the provider's catalog default and backs
// registry-only providers. A nil catalog uses the process-wide one, built on
// first use and only when one of those two lookups is needed.
func createModelWith(catalogSvc *catalog.Catalog, provider, modelName, location, project string, requestTimeout time.Duration) (core.Model, error) {
	// Use an HTTP client with a bounded per-request timeout so transient
	// provider hangs fail fast and can be retried within the run budget.
	httpClient := &http.Client{Timeout: requestTimeout}

	// An empty model means the catalog's advertised default, so the model
	// that was validated and shown is the one that runs. The test fixture and
	// the local endpoint carry their own defaults.
	if modelName == "" && provider != "test" && provider != "openai-compatible-local" {
		if catalogSvc == nil {
			catalogSvc = cliCatalog()
		}
		if name, ok := catalogSvc.DefaultModelName(provider); ok {
			modelName = name
		}
	}

	switch provider {
	case "test":
		model := core.NewTestModel(
			core.TextResponse("Hello! I'm a test model. This is a demonstration of the TUI debugger."),
		)
		if delayText := strings.TrimSpace(os.Getenv("GOLLEM_TEST_MODEL_DELAY")); delayText != "" {
			delay, err := time.ParseDuration(delayText)
			if err != nil || delay < 0 {
				return nil, fmt.Errorf("invalid GOLLEM_TEST_MODEL_DELAY %q", delayText)
			}
			if delay > 0 {
				return delayedTestModel{inner: model, delay: delay}, nil
			}
		}
		return model, nil
	case "anthropic":
		opts := []anthropic.Option{anthropic.WithHTTPClient(httpClient)}
		if modelName != "" {
			opts = append(opts, anthropic.WithModel(modelName))
		}
		return anthropic.New(opts...), nil
	case "openai":
		opts := []openai.Option{openai.WithHTTPClient(httpClient)}
		if modelName != "" {
			opts = append(opts, openai.WithModel(modelName))
		}
		return openai.New(opts...), nil
	case "openai-compatible-local":
		config, err := openai.LocalEndpointConfigFromLookup(os.LookupEnv)
		if err != nil {
			return nil, err
		}
		if modelName != "" {
			config.Model = modelName
		}
		return openai.NewLocalEndpoint(config, openai.WithHTTPClient(httpClient))
	case "xai":
		opts := []openai.Option{openai.WithHTTPClient(httpClient)}
		if modelName != "" {
			opts = append(opts, openai.WithModel(modelName))
		}
		return openai.NewXAI(opts...), nil
	case "deepseek":
		opts := []openai.Option{openai.WithHTTPClient(httpClient)}
		if modelName != "" {
			opts = append(opts, openai.WithModel(modelName))
		}
		return openai.NewDeepSeek(opts...), nil
	case "vertexai":
		opts := []vertexai.Option{vertexai.WithHTTPClient(httpClient)}
		if modelName != "" {
			opts = append(opts, vertexai.WithModel(modelName))
		}
		if location != "" {
			opts = append(opts, vertexai.WithLocation(location))
		}
		if project != "" {
			opts = append(opts, vertexai.WithProject(project))
		}
		return vertexai.New(opts...), nil
	case "vertexai-anthropic":
		opts := []vertexai_anthropic.Option{vertexai_anthropic.WithHTTPClient(httpClient)}
		if modelName != "" {
			opts = append(opts, vertexai_anthropic.WithModel(modelName))
		}
		if location != "" {
			opts = append(opts, vertexai_anthropic.WithLocation(location))
		}
		if project != "" {
			opts = append(opts, vertexai_anthropic.WithProject(project))
		}
		return vertexai_anthropic.New(opts...), nil
	default:
		if catalogSvc == nil {
			catalogSvc = cliCatalog()
		}
		endpoint, ok := catalogSvc.RuntimeEndpoint(provider)
		if !ok {
			return nil, fmt.Errorf("provider %q not supported (available: test, anthropic, openai, openai-compatible-local, xai, vertexai, vertexai-anthropic, deepseek, or a configured model-registry provider such as groq)", provider)
		}
		return newRegistryModel(endpoint, modelName, httpClient, catalogSvc.EnvLookup())
	}
}

// envBool reads a boolean environment flag: 1/true/yes/on enable,
// 0/false/no/off disable, anything else (including unset) yields the default.
func envBool(name string, defaultValue bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}

var (
	cliCatalogOnce sync.Once
	cliCatalogSvc  *catalog.Catalog
)

// cliCatalog is the process-wide catalog for CLI commands that were not given
// one; it is built at most once per process.
func cliCatalog() *catalog.Catalog {
	cliCatalogOnce.Do(func() { cliCatalogSvc = newAppServerCatalog() })
	return cliCatalogSvc
}

// newRegistryModel constructs a model for a registry-backed provider that
// Gollem drives through its generic Anthropic or OpenAI transport. The API
// key is read from the environment variable the registry names; it is never
// embedded in the catalog.
func newRegistryModel(endpoint catalog.RuntimeEndpoint, modelName string, httpClient *http.Client, lookup catalog.EnvLookup) (core.Model, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	value, _ := lookup(endpoint.APIKeyEnv)
	apiKey := strings.TrimSpace(value)
	if apiKey == "" {
		return nil, fmt.Errorf("provider %q requires %s to be set", endpoint.ProviderID, endpoint.APIKeyEnv)
	}
	if modelName == "" {
		modelName = endpoint.DefaultModel
	}
	if modelName == "" {
		return nil, fmt.Errorf("provider %q has no default model; pass --model", endpoint.ProviderID)
	}
	switch endpoint.Family {
	case catalog.RuntimeFamilyAnthropic:
		return anthropic.New(
			anthropic.WithHTTPClient(httpClient),
			anthropic.WithBaseURL(endpoint.BaseURL),
			anthropic.WithAPIKey(apiKey),
			anthropic.WithModel(modelName),
		), nil
	case catalog.RuntimeFamilyOpenAI:
		opts := []openai.Option{
			openai.WithoutOpenAIEnvironment(),
			openai.WithHTTPClient(httpClient),
			openai.WithBaseURL(endpoint.BaseURL),
			openai.WithAPIKey(apiKey),
			openai.WithModel(modelName),
			openai.WithExtraHeaders(endpoint.Headers),
		}
		if endpoint.ChatCompletionsOnly {
			opts = append(opts, openai.WithChatCompletionsOnly())
		}
		return openai.New(opts...), nil
	default:
		return nil, fmt.Errorf("provider %q uses unsupported transport family %q", endpoint.ProviderID, endpoint.Family)
	}
}

type delayedTestModel struct {
	inner core.Model
	delay time.Duration
}

func (m delayedTestModel) ModelName() string {
	return m.inner.ModelName()
}

func (m delayedTestModel) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	if err := waitTestModelDelay(ctx, m.delay); err != nil {
		return nil, err
	}
	return m.inner.Request(ctx, messages, settings, params)
}

func (m delayedTestModel) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	if err := waitTestModelDelay(ctx, m.delay); err != nil {
		return nil, err
	}
	return m.inner.RequestStream(ctx, messages, settings, params)
}

func waitTestModelDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `gollem - Coding agent framework CLI

Usage:
  gollem <command> [options] [prompt]

Commands:
  run     Run a coding agent with tools (for benchmarks and automation)
  debug   Run the interactive TUI debugger
  serve   Start the browser dashboard UI server
  app-server  Start the Codex-style JSON-RPC app-server daemon
  trace   Inspect, replay, and diff Gollem trace artifacts

Run 'gollem <command> --help' for command-specific help.
`)
}

func printRunUsage() {
	fmt.Fprintf(os.Stderr, `gollem run - Run a coding agent

Usage:
  gollem run [options] "prompt"

Options:
  --provider <name>        Model provider (auto-detected from API keys if not set)
                           Available: anthropic, openai, xai, deepseek, vertexai, vertexai-anthropic,
                           or any model-registry provider whose API key is set (e.g. groq)
  --model <name>           Model name (uses the model catalog's default for the provider if not set;
                           anthropic defaults to claude-opus-5)
  --location <region>      GCP region for vertexai providers (default: us-central1)
  --project <id>           GCP project ID for vertexai providers (default: GOOGLE_CLOUD_PROJECT)
  --workdir <path>         Working directory (default: current directory)
  --trace-out <path>       Write a gollem.trace.v1 artifact after the run
  --trace-stream <path>    Stream live runtime-boundary events as JSONL during the run
  --resume-snapshot <path> Resume from a snapshot produced by gollem trace fork
  --timeout <duration>     Maximum run time (default: 30m)
  --team-mode <mode>       Team tool routing: auto, on, off (default: auto)
  --thinking-budget <n>    Thinking token budget for Anthropic (default: 16000)
  --reasoning-effort <l>   Reasoning effort for OpenAI: low, medium, high, xhigh (default: high)
  --no-reasoning           Disable all reasoning (thinking + effort)
  --no-code-mode           Disable code mode (monty-go WASM Python batching)
  -h, --help               Show this help

Examples:
  gollem run --provider anthropic "Fix the failing tests in this project"
  gollem run --provider openai --model gpt-5.3 "Implement the TODO items"
  gollem run --workdir /path/to/project "Add error handling to main.go"
  gollem run --provider vertexai --model gemini-3-flash-preview --location global "What is 2+2"
  gollem run --provider anthropic --thinking-budget 64000 "Refactor the auth system"

Environment variables:
  ANTHROPIC_API_KEY        API key for the anthropic provider
  OPENAI_API_KEY           API key for the openai provider
  OPENAI_PROMPT_CACHE_KEY  Optional stable key for OpenAI prompt caching
  OPENAI_PROMPT_CACHE_RETENTION Optional OpenAI cache retention policy (e.g. in_memory, 24h)
  OPENAI_SERVICE_TIER      Optional OpenAI service tier (e.g. default, flex, priority)
  OPENAI_TRANSPORT         Optional OpenAI transport: http (default) or websocket (Responses API only)
  OPENAI_WEBSOCKET_HTTP_FALLBACK Optional fallback to HTTP when websocket transport fails (0/1, default: 0)
  GOLLEM_REASONING_BY_TASK Optional per-task OpenAI reasoning map, e.g.
                           "model-extraction-relu-logits=xhigh,regex-chess=xhigh,*=high"
  GOLLEM_REASONING_NO_SANDWICH_BY_TASK Optional task list where OpenAI uses flat reasoning (no sandwich),
                           e.g. "model-extraction-relu-logits" or "model-extraction-relu-logits,*"
  GOLLEM_REASONING_NO_GREEDY_BY_TASK Optional task list where time-budget greedy caps are disabled,
                           e.g. "model-extraction-relu-logits"
  GOLLEM_TASK_NAME         Optional current task name used by GOLLEM_REASONING_BY_TASK
  VERTEXAI_ANTHROPIC_PROMPT_CACHE Enable Anthropic prompt caching on Vertex AI (1/true/yes/on)
  VERTEXAI_ANTHROPIC_PROMPT_CACHE_TTL Optional Anthropic prompt cache TTL (e.g. 5m, 1h)
  GOLLEM_MODEL_REQUEST_TIMEOUT_SEC Optional per-model-call timeout in seconds (default derived from --timeout, capped at 6m)
  GOLLEM_TRACE_OUT        Optional path for writing a gollem.trace.v1 artifact
  GOLLEM_RESUME_SNAPSHOT  Optional snapshot path for resuming/forking a run
  GOLLEM_TEAM_MODE         Team mode override: auto, on, off (default: auto)
  GOLLEM_DISABLE_DELEGATE  Disable the delegate (subagent) tool (1/true/yes/on; default: off)
  GOLLEM_TOP_LEVEL_PERSONALITY Enable top-level dynamic personality generation (1/true/yes/on; default: off)
  GOLLEM_CODE_MODE_FAILURE_THRESHOLD Consecutive execute_code capability failures before cooldown (default: 3)
  GOLLEM_CODE_MODE_COOLDOWN_TURNS Turns to keep execute_code disabled before retrying (default: 2)
  GOLLEM_CODE_MODE_MAX_RECENT_RESULTS Number of recent execute_code results to inspect (default: 12)
  GOOGLE_CLOUD_PROJECT     GCP project for vertexai and vertexai-anthropic providers
  LANGFUSE_SECRET_KEY      Langfuse secret key (enables tracing)
  LANGFUSE_PUBLIC_KEY      Langfuse public key
  LANGFUSE_BASE_URL        Langfuse API URL (default: https://cloud.langfuse.com)
`)
}

func printServeUsage() {
	fmt.Fprintf(os.Stderr, `gollem serve - Start the browser dashboard UI server

Usage:
  gollem serve [options]

Options:
  --port <n>              Local HTTP port to listen on (default: 8080)
  --provider <name>       Model provider (auto-detected from API keys if not set)
                          Available: anthropic, openai, xai, deepseek, vertexai, vertexai-anthropic,
                          or any model-registry provider whose API key is set (e.g. groq)
  --model <name>          Model name (uses the model catalog's default for the provider if not set)
  --location <region>     GCP region for vertexai providers (default: us-central1)
  --project <id>          GCP project ID for vertexai providers (default: GOOGLE_CLOUD_PROJECT)
  --workdir <path>        Working directory for tool-enabled runs (default: current directory)
  --tools[=bool]          Enable coding tools in dashboard runs (default: false)
  --open[=bool]           Open the dashboard URL in the default browser (default: false)
  -h, --help              Show this help

Examples:
  gollem serve --provider anthropic --open
  gollem serve --provider openai --model gpt-5.3 --port 9090
  gollem serve --provider anthropic --tools --workdir /path/to/project
`)
}

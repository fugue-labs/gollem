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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	lf "github.com/fugue-labs/langfuse-go"
	montygo "github.com/fugue-labs/monty-go"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/codetool"
	"github.com/fugue-labs/gollem/ext/middleware"
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
	timeout         time.Duration
	thinkingBudget  int
	reasoningEffort string // OpenAI: "low", "medium", "high"
	teamMode        string // "auto", "on", "off"
	noCodeMode      bool
	noReasoning     bool // Disable all reasoning (thinking + effort)
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

	requestTimeout := deriveRequestTimeout(f.timeout)
	baseModel, err := createModel(f.provider, f.modelName, f.location, f.project, requestTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating model: %v\n", err)
		os.Exit(1)
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
		cacheEnabled := false
		switch strings.ToLower(strings.TrimSpace(os.Getenv("VERTEXAI_ANTHROPIC_PROMPT_CACHE"))) {
		case "1", "true", "yes", "on":
			cacheEnabled = true
		}
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
		mws = append(mws, newLangfuseMiddleware(langfuseProcessor))
		fmt.Fprintf(os.Stderr, "gollem: langfuse tracing enabled\n")
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

	// Pass the model so the coding agent can spawn subagents for delegation.
	toolOpts = append(toolOpts, codetool.WithModel(model))
	// Pass the budget timeout (real deadline) for time budget warnings.
	// This is separate from f.timeout (exec timeout) which may be shorter.
	toolOpts = append(toolOpts, codetool.WithTimeout(budgetTimeout))

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
	}

	// Build the coding agent with the full recommended setup.
	agentOpts := codetool.AgentOptions(f.workDir, toolOpts...)
	agentOpts = append(agentOpts, core.WithRunCondition[string](core.MaxRunDuration(f.timeout)))

	// Enable reasoning by default for providers that support it.
	// This is provider-agnostic: Anthropic uses ThinkingBudget,
	// OpenAI uses ReasoningEffort. The reasoning sandwich middleware
	// will vary these per-turn for optimal performance.
	if !f.noReasoning {
		switch f.provider {
		case "anthropic", "vertexai-anthropic":
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
			// - Codex models (gpt-5.x-codex): default to "high" (ceiling)
			// Other OpenAI-compatible models (grok, together, etc.) may not
			// support the parameter, so don't enable by default.
			isOSeries := strings.HasPrefix(f.modelName, "o") && len(f.modelName) >= 2
			isCodex := strings.Contains(f.modelName, "codex")
			if isCodex {
				if f.reasoningEffort == "" {
					f.reasoningEffort = "high"
				}
				if effortAboveHigh(f.reasoningEffort) {
					fmt.Fprintf(os.Stderr, "gollem: codex reasoning effort clamped from %q to \"high\"\n", f.reasoningEffort)
					f.reasoningEffort = "high"
				}
				agentOpts = append(agentOpts, core.WithReasoningEffort[string](f.reasoningEffort))
				// Codex reasoning can consume output budget, so keep max_output high
				// while capping effort at "high" for latency stability.
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

	// Flush Langfuse traces before exiting.
	if langfuseProcessor != nil {
		_ = langfuseProcessor.Close()
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
func newLangfuseMiddleware(processor *lf.BatchProcessor) middleware.Middleware {
	traceID := lf.NewID()
	processor.Enqueue(lf.TraceEvent{
		ID:        traceID,
		Name:      "gollem-run",
		Timestamp: time.Now().UTC(),
	})

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
					content := p.Content
					if len(content) > 500 {
						content = content[:500] + "..."
					}
					result = append(result, map[string]any{"role": "system", "content": content})
				case core.UserPromptPart:
					content := p.Content
					if len(content) > 2000 {
						content = content[:2000] + "..."
					}
					result = append(result, map[string]any{"role": "user", "content": content})
				case core.ToolReturnPart:
					content := fmt.Sprintf("%v", p.Content)
					if len(content) > 1000 {
						content = content[:1000] + "..."
					}
					result = append(result, map[string]any{"role": "tool", "tool": p.ToolName, "content": content})
				}
			}
		case core.ModelResponse:
			result = append(result, map[string]any{"role": "assistant", "content": m.TextContent()})
		}
	}
	return result
}

// langfuseSummarizeResponse extracts text + tool calls from a model response.
func langfuseSummarizeResponse(resp *core.ModelResponse) map[string]any {
	out := map[string]any{}
	if text := resp.TextContent(); text != "" {
		if len(text) > 2000 {
			text = text[:2000] + "..."
		}
		out["text"] = text
	}
	var tools []map[string]string
	for _, part := range resp.Parts {
		if tc, ok := part.(core.ToolCallPart); ok {
			args := tc.ArgsJSON
			if len(args) > 500 {
				args = args[:500] + "..."
			}
			tools = append(tools, map[string]string{"tool": tc.ToolName, "args": args})
		}
	}
	if len(tools) > 0 {
		out["tool_calls"] = tools
	}
	return out
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

func effortAboveHigh(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "xhigh":
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
	if os.Getenv("OPENAI_API_KEY") != "" {
		return "openai"
	}
	if os.Getenv("GOOGLE_CLOUD_PROJECT") != "" {
		return "vertexai"
	}
	return ""
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
	// Use an HTTP client with a bounded per-request timeout so transient
	// provider hangs fail fast and can be retried within the run budget.
	httpClient := &http.Client{Timeout: requestTimeout}

	switch provider {
	case "test":
		return core.NewTestModel(
			core.TextResponse("Hello! I'm a test model. This is a demonstration of the TUI debugger."),
		), nil
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
		return nil, fmt.Errorf("provider %q not supported (available: test, anthropic, openai, vertexai, vertexai-anthropic)", provider)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `gollem - Coding agent framework CLI

Usage:
  gollem <command> [options] "prompt"

Commands:
  run     Run a coding agent with tools (for benchmarks and automation)
  debug   Run the interactive TUI debugger

Run 'gollem <command> --help' for command-specific help.
`)
}

func printRunUsage() {
	fmt.Fprintf(os.Stderr, `gollem run - Run a coding agent

Usage:
  gollem run [options] "prompt"

Options:
  --provider <name>        Model provider (auto-detected from API keys if not set)
                           Available: anthropic, openai, vertexai, vertexai-anthropic
  --model <name>           Model name (uses provider default if not set)
  --location <region>      GCP region for vertexai providers (default: us-central1)
  --project <id>           GCP project ID for vertexai providers (default: GOOGLE_CLOUD_PROJECT)
  --workdir <path>         Working directory (default: current directory)
  --timeout <duration>     Maximum run time (default: 30m)
  --team-mode <mode>       Team tool routing: auto, on, off (default: auto)
  --thinking-budget <n>    Thinking token budget for Anthropic (default: 16000)
  --reasoning-effort <l>   Reasoning effort for OpenAI: low, medium, high (default: high)
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
  VERTEXAI_ANTHROPIC_PROMPT_CACHE Enable Anthropic prompt caching on Vertex AI (1/true/yes/on)
  VERTEXAI_ANTHROPIC_PROMPT_CACHE_TTL Optional Anthropic prompt cache TTL (e.g. 5m, 1h)
  GOLLEM_MODEL_REQUEST_TIMEOUT_SEC Optional per-model-call timeout in seconds (default derived from --timeout, capped at 6m)
  GOLLEM_TEAM_MODE         Team mode override: auto, on, off (default: auto)
  GOLLEM_CODE_MODE_FAILURE_THRESHOLD Consecutive execute_code capability failures before cooldown (default: 3)
  GOLLEM_CODE_MODE_COOLDOWN_TURNS Turns to keep execute_code disabled before retrying (default: 2)
  GOLLEM_CODE_MODE_MAX_RECENT_RESULTS Number of recent execute_code results to inspect (default: 12)
  GOOGLE_CLOUD_PROJECT     GCP project for vertexai and vertexai-anthropic providers
  LANGFUSE_SECRET_KEY      Langfuse secret key (enables tracing)
  LANGFUSE_PUBLIC_KEY      Langfuse public key
  LANGFUSE_BASE_URL        Langfuse API URL (default: https://cloud.langfuse.com)
`)
}

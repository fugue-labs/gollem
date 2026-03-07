// Package researcher implements the autonomous researcher agent that optimizes
// Gollem agent configurations through iterative experimentation.
package researcher

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/codetool"
	"github.com/fugue-labs/gollem/ext/middleware"
	"github.com/fugue-labs/gollem/modelutil"
	"github.com/fugue-labs/gollem/provider/anthropic"
	"github.com/fugue-labs/gollem/provider/openai"
	"github.com/fugue-labs/gollem/provider/vertexai"
	"github.com/fugue-labs/gollem/provider/vertexai_anthropic"
)

// NewResearcherAgent constructs the researcher agent with all tools,
// middleware, guardrails, and observability configured. Follows the same
// patterns as cmd/gollem for provider setup, retry wrapping, and reasoning.
func NewResearcherAgent(cfg Config) *core.Agent[ExperimentResult] {
	baseModel := selectModel(cfg)

	// Wrap with retry for API resilience (exponential backoff on 429/5xx).
	retryModel := modelutil.NewRetryModel(baseModel, modelutil.RetryConfig{
		MaxRetries:     5,
		InitialBackoff: time.Second,
		MaxBackoff:     30 * time.Second,
		MinRemaining:   20 * time.Second,
	})

	// Provider-level middleware for observability.
	loggingMW := middleware.NewLogging(slog.Default(), slog.LevelInfo)
	model := middleware.Wrap(retryModel, loggingMW)

	// Coding tools via codetool.Toolset — production-grade Bash, View,
	// Edit, Grep, Glob, Ls with proper hooks and dynamic system prompts.
	codeToolset := codetool.Toolset(
		codetool.WithWorkDir(cfg.SubjectDir),
		codetool.WithBashTimeout(10*time.Minute),
	)

	costTracker := core.NewCostTracker(modelPricing(cfg))

	// Parse experiment timeout.
	expTimeout := 30 * time.Minute
	if cfg.ExperimentTimeout != "" {
		if d, err := time.ParseDuration(cfg.ExperimentTimeout); err == nil {
			expTimeout = d
		}
	}

	opts := []core.AgentOption[ExperimentResult]{
		// Identity.
		core.WithSystemPrompt[ExperimentResult](BuildSystemPrompt(cfg.SubjectDir)),

		// Coding tools from ext/codetool (Bash, View, Edit, Grep, Glob, Ls).
		core.WithToolsets[ExperimentResult](codeToolset),

		// Domain-specific tools (eval, git, traces, results, scoped write).
		core.WithTools[ExperimentResult](BuildDomainTools(cfg)...),

		// Safety: turn guardrail limits steps per experiment cycle.
		core.WithTurnGuardrail[ExperimentResult]("max_turns", core.MaxTurns(100)),

		// Safety: hard timeout per experiment run.
		core.WithRunCondition[ExperimentResult](core.MaxRunDuration(expTimeout)),

		// Observability.
		core.WithCostTracker[ExperimentResult](costTracker),
		core.WithTracing[ExperimentResult](),
		core.WithTraceExporter[ExperimentResult](
			core.NewJSONFileExporter(cfg.HarnessTracesDir),
		),

		// Lifecycle hooks.
		core.WithHooks[ExperimentResult](core.Hook{
			OnToolStart: func(_ context.Context, _ *core.RunContext, name, _ string) {
				log.Printf("[researcher] tool: %s", name)
			},
			OnToolEnd: func(_ context.Context, _ *core.RunContext, name, _ string, err error) {
				if err != nil {
					log.Printf("[researcher] tool %s error: %v", name, err)
				}
			},
		}),

		// Middleware: timing.
		core.WithAgentMiddleware[ExperimentResult](
			core.TimingMiddleware(func(d time.Duration) {
				log.Printf("[researcher] model call: %v", d)
			}),
		),
	}

	// Provider-aware auto-context limits (matches cmd/gollem patterns).
	opts = append(opts, autoContextForProvider(cfg.Provider))

	// Reasoning/thinking support per provider.
	opts = append(opts, reasoningOptsForProvider(cfg)...)

	return core.NewAgent[ExperimentResult](model, opts...)
}

// selectModel creates the appropriate model provider based on config.
// Mirrors cmd/gollem createModel() supporting all gollem providers.
func selectModel(cfg Config) core.Model {
	switch cfg.Provider {
	case "anthropic":
		var opts []anthropic.Option
		if cfg.Model != "" {
			opts = append(opts, anthropic.WithModel(cfg.Model))
		}
		return anthropic.New(opts...)

	case "openai":
		var opts []openai.Option
		if cfg.Model != "" {
			opts = append(opts, openai.WithModel(cfg.Model))
		}
		return openai.New(opts...)

	case "xai":
		var opts []openai.Option
		if cfg.Model != "" {
			opts = append(opts, openai.WithModel(cfg.Model))
		}
		return openai.NewXAI(opts...)

	case "ollama":
		var opts []openai.Option
		if cfg.Model != "" {
			opts = append(opts, openai.WithModel(cfg.Model))
		}
		if cfg.OllamaBaseURL != "" {
			opts = append(opts, openai.WithBaseURL(cfg.OllamaBaseURL))
		}
		return openai.NewOllama(opts...)

	case "vertexai":
		var opts []vertexai.Option
		if cfg.Model != "" {
			opts = append(opts, vertexai.WithModel(cfg.Model))
		}
		if cfg.Location != "" {
			opts = append(opts, vertexai.WithLocation(cfg.Location))
		}
		if cfg.Project != "" {
			opts = append(opts, vertexai.WithProject(cfg.Project))
		}
		return vertexai.New(opts...)

	case "vertexai-anthropic":
		var opts []vertexai_anthropic.Option
		if cfg.Model != "" {
			opts = append(opts, vertexai_anthropic.WithModel(cfg.Model))
		}
		if cfg.Location != "" {
			opts = append(opts, vertexai_anthropic.WithLocation(cfg.Location))
		}
		if cfg.Project != "" {
			opts = append(opts, vertexai_anthropic.WithProject(cfg.Project))
		}
		return vertexai_anthropic.New(opts...)

	default:
		log.Fatalf("unknown provider: %s (supported: anthropic, openai, xai, ollama, vertexai, vertexai-anthropic)", cfg.Provider)
		return nil
	}
}

// autoContextForProvider returns provider-tuned auto-context settings.
func autoContextForProvider(provider string) core.AgentOption[ExperimentResult] {
	switch provider {
	case "anthropic", "vertexai-anthropic":
		return core.WithAutoContext[ExperimentResult](core.AutoContextConfig{
			MaxTokens: 150000,
			KeepLastN: 12,
		})
	case "vertexai":
		return core.WithAutoContext[ExperimentResult](core.AutoContextConfig{
			MaxTokens: 900000,
			KeepLastN: 20,
		})
	case "openai":
		return core.WithAutoContext[ExperimentResult](core.AutoContextConfig{
			MaxTokens: 350000,
			KeepLastN: 20,
		})
	case "xai":
		return core.WithAutoContext[ExperimentResult](core.AutoContextConfig{
			MaxTokens: 900000,
			KeepLastN: 20,
		})
	default:
		return core.WithAutoContext[ExperimentResult](core.AutoContextConfig{
			MaxTokens: 80000,
			KeepLastN: 12,
		})
	}
}

// reasoningOptsForProvider enables thinking/reasoning per provider,
// matching the patterns in cmd/gollem.
func reasoningOptsForProvider(cfg Config) []core.AgentOption[ExperimentResult] {
	var opts []core.AgentOption[ExperimentResult]

	switch cfg.Provider {
	case "anthropic", "vertexai-anthropic", "vertexai":
		budget := cfg.ThinkingBudget
		if budget == 0 {
			budget = 16000
		}
		opts = append(opts, core.WithThinkingBudget[ExperimentResult](budget))
		maxTokens := budget + 16000
		opts = append(opts, core.WithMaxTokens[ExperimentResult](maxTokens))

	case "openai":
		effort := cfg.ReasoningEffort
		if effort == "" {
			effort = "high"
		}
		opts = append(opts, core.WithReasoningEffort[ExperimentResult](effort))
	}

	return opts
}

// modelPricing returns pricing info for cost tracking.
func modelPricing(cfg Config) map[string]core.ModelPricing {
	pricing := map[string]core.ModelPricing{
		"claude-sonnet-4-5-20250929": {
			InputTokenCost:  0.000003,
			OutputTokenCost: 0.000015,
		},
		"claude-opus-4-6": {
			InputTokenCost:  0.000015,
			OutputTokenCost: 0.000075,
		},
		"claude-haiku-4-5-20251001": {
			InputTokenCost:  0.0000008,
			OutputTokenCost: 0.000004,
		},
	}

	// Local models are free.
	if cfg.Provider == "ollama" {
		pricing[cfg.Model] = core.ModelPricing{}
	}

	return pricing
}

// LoadConfig loads configuration from environment variables with sensible defaults.
func LoadConfig() Config {
	cfg := DefaultConfig()

	if v := os.Getenv("AUTOEVAL_PROVIDER"); v != "" {
		cfg.Provider = v
	}
	if v := os.Getenv("AUTOEVAL_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv("AUTOEVAL_LOCATION"); v != "" {
		cfg.Location = v
	}
	if v := os.Getenv("AUTOEVAL_PROJECT"); v != "" {
		cfg.Project = v
	}
	if v := os.Getenv("AUTOEVAL_SUBJECT_DIR"); v != "" {
		cfg.SubjectDir = v
	}
	if v := os.Getenv("AUTOEVAL_TRACES_DIR"); v != "" {
		cfg.TracesDir = v
	}
	if v := os.Getenv("AUTOEVAL_EVAL_COMMAND"); v != "" {
		cfg.EvalCommand = v
	}
	if v := os.Getenv("AUTOEVAL_EVAL_RESULTS_PATH"); v != "" {
		cfg.EvalResultsPath = v
	}
	if v := os.Getenv("AUTOEVAL_OLLAMA_URL"); v != "" {
		cfg.OllamaBaseURL = v
	}
	if v := os.Getenv("AUTOEVAL_RESULTS_FILE"); v != "" {
		cfg.ResultsFile = v
	}
	if v := os.Getenv("AUTOEVAL_EXPERIMENT_TIMEOUT"); v != "" {
		cfg.ExperimentTimeout = v
	}
	if v := os.Getenv("AUTOEVAL_THINKING_BUDGET"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			cfg.ThinkingBudget = n
		}
	}
	if v := os.Getenv("AUTOEVAL_REASONING_EFFORT"); v != "" {
		cfg.ReasoningEffort = v
	}
	if v := os.Getenv("AUTOEVAL_MAX_EXPERIMENTS"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			cfg.MaxExperiments = n
		}
	}

	return cfg
}

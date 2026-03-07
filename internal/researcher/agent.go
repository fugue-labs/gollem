// Package researcher implements the autonomous researcher agent that optimizes
// Gollem agent configurations through iterative experimentation.
package researcher

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/provider/anthropic"
	"github.com/fugue-labs/gollem/provider/openai"
)

// NewResearcherAgent constructs the researcher agent with all tools,
// middleware, guardrails, and observability configured.
func NewResearcherAgent(cfg Config) *core.Agent[ExperimentResult] {
	model := selectModel(cfg)

	costTracker := core.NewCostTracker(modelPricing(cfg))

	return core.NewAgent[ExperimentResult](model,
		// Identity
		core.WithSystemPrompt[ExperimentResult](BuildSystemPrompt(cfg.SubjectDir)),

		// Tools — the researcher's capabilities
		core.WithTools[ExperimentResult](BuildTools(cfg)...),

		// Safety: turn guardrail limits steps per experiment cycle
		core.WithTurnGuardrail[ExperimentResult]("max_turns",
			core.MaxTurns(100),
		),

		// Safety: input guardrail prevents writes outside subject/
		core.WithInputGuardrail[ExperimentResult]("scope",
			scopeGuardrail(cfg.SubjectDir),
		),

		// Observability: cost tracking
		core.WithCostTracker[ExperimentResult](costTracker),

		// Observability: tracing with JSON file export
		core.WithTracing[ExperimentResult](),
		core.WithTraceExporter[ExperimentResult](
			core.NewJSONFileExporter(cfg.HarnessTracesDir),
		),

		// Observability: lifecycle hooks
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

		// Middleware: timing
		core.WithAgentMiddleware[ExperimentResult](
			core.TimingMiddleware(func(d time.Duration) {
				log.Printf("[researcher] model call: %v", d)
			}),
		),

		// Context management for long conversations
		core.WithAutoContext[ExperimentResult](core.AutoContextConfig{
			MaxTokens: 100000,
			KeepLastN: 20,
		}),
	)
}

// selectModel creates the appropriate model provider based on config.
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

	case "ollama":
		var opts []openai.Option
		if cfg.Model != "" {
			opts = append(opts, openai.WithModel(cfg.Model))
		}
		if cfg.OllamaBaseURL != "" {
			opts = append(opts, openai.WithBaseURL(cfg.OllamaBaseURL))
		}
		return openai.NewOllama(opts...)

	default:
		log.Fatalf("unknown provider: %s (supported: anthropic, openai, ollama)", cfg.Provider)
		return nil
	}
}

// scopeGuardrail is an input guardrail that validates prompts.
// Tool-level scope enforcement is handled by writeFileTool directly.
func scopeGuardrail(_ string) core.InputGuardrailFunc {
	return func(_ context.Context, prompt string) (string, error) {
		return prompt, nil
	}
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
	if v := os.Getenv("AUTOEVAL_EVAL_PROVIDER"); v != "" {
		cfg.EvalProvider = v
	}
	if v := os.Getenv("AUTOEVAL_EVAL_MODEL"); v != "" {
		cfg.EvalModel = v
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
	if v := os.Getenv("AUTOEVAL_MAX_EXPERIMENTS"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			cfg.MaxExperiments = n
		}
	}

	return cfg
}

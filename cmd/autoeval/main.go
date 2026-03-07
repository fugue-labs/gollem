// Command autoeval runs the autonomous Gollem improvement harness.
//
// The harness is itself a Gollem agent that iteratively modifies, evaluates,
// and selects improvements to a target agent configuration. Inspired by
// karpathy/autoresearch, with the critical twist that the harness uses the
// same framework it optimizes.
//
// Configuration is via environment variables:
//
//	AUTOEVAL_PROVIDER       - Model provider: anthropic, openai, ollama (default: ollama)
//	AUTOEVAL_MODEL          - Model name (default: qwen3:70b)
//	AUTOEVAL_EVAL_COMMAND   - Shell command to run evaluation ({mode} is substituted)
//	AUTOEVAL_SUBJECT_DIR    - Directory containing the agent config to optimize
//	AUTOEVAL_MAX_EXPERIMENTS - Maximum experiments to run (0 = unlimited)
//
// Usage:
//
//	go run ./cmd/autoeval
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/fugue-labs/gollem/internal/researcher"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	cfg := researcher.LoadConfig()
	log.Printf("AutoEval Harness starting")
	log.Printf("  Provider:    %s", cfg.Provider)
	log.Printf("  Model:       %s", cfg.Model)
	log.Printf("  Subject dir: %s", cfg.SubjectDir)
	log.Printf("  Results:     %s", cfg.ResultsFile)
	if cfg.MaxExperiments > 0 {
		log.Printf("  Max experiments: %d", cfg.MaxExperiments)
	} else {
		log.Printf("  Max experiments: unlimited")
	}

	agent := researcher.NewResearcherAgent(cfg)

	// Graceful shutdown on interrupt.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		log.Println("Interrupt received, finishing current experiment...")
		cancel()
	}()

	// The outer loop: each iteration is one full experiment cycle.
	// The agent's single Run() call handles the full cycle:
	//   hypothesize → modify → commit → eval → parse → keep/discard
	// Then we call Run() again for the next experiment.
	experimentNum := 0
	for {
		select {
		case <-ctx.Done():
			log.Printf("Stopped after %d experiments", experimentNum)
			return
		default:
		}

		experimentNum++
		if cfg.MaxExperiments > 0 && experimentNum > cfg.MaxExperiments {
			log.Printf("Reached max experiments (%d), stopping", cfg.MaxExperiments)
			return
		}

		log.Printf("=== Experiment %d ===", experimentNum)

		prompt := researcher.BuildExperimentPrompt(experimentNum, cfg)
		result, err := agent.Run(ctx, prompt)
		if err != nil {
			log.Printf("Researcher agent error: %v", err)
			continue
		}

		log.Printf("Result: %s | pass_rate: %.4f | status: %s | %s",
			result.Output.Commit,
			result.Output.PassRate,
			result.Output.Status,
			result.Output.Description,
		)
		log.Printf("Next idea: %s", result.Output.NextIdea)

		if result.Cost != nil {
			log.Printf("Cost this cycle: $%.4f", result.Cost.TotalCost)
		}

		log.Printf("Tokens: in=%d out=%d | tool_calls=%d",
			result.Usage.InputTokens,
			result.Usage.OutputTokens,
			result.Usage.ToolCalls,
		)
	}
}

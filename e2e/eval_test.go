//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/eval"
)

// TestEvalRunnerContains verifies the evaluation runner with a Contains evaluator.
func TestEvalRunnerContains(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	runner := eval.NewRunner[string](agent, eval.Contains())
	dataset := eval.Dataset[string]{
		Name: "basic-greetings",
		Cases: []eval.Case[string]{
			{
				Name:     "hello",
				Prompt:   "Say exactly: hello world",
				Expected: "hello",
			},
			{
				Name:     "goodbye",
				Prompt:   "Say exactly: goodbye friend",
				Expected: "goodbye",
			},
		},
	}

	report, err := runner.Run(ctx, dataset)
	if err != nil {
		t.Fatalf("eval runner failed: %v", err)
	}

	if report.TotalCases != 2 {
		t.Errorf("expected 2 total cases, got %d", report.TotalCases)
	}
	if report.PassedCases < 1 {
		t.Errorf("expected at least 1 passed case, got %d", report.PassedCases)
	}

	for _, r := range report.Results {
		if r.Error != nil {
			skipOnAccountError(t, r.Error)
		}
	}

	t.Logf("Eval report: %d/%d passed, avg=%.2f", report.PassedCases, report.TotalCases, report.AvgScore)
}

// TestEvalRunnerCustom verifies the evaluation runner with a custom evaluator.
func TestEvalRunnerCustom(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	type MathAnswer struct {
		Value int `json:"value"`
	}

	agent := core.NewAgent[MathAnswer](newAnthropicProvider())

	customEval := eval.Custom[MathAnswer](func(ctx context.Context, output, expected MathAnswer) (*eval.Score, error) {
		if output.Value == expected.Value {
			return &eval.Score{Value: 1.0, Reason: "exact match"}, nil
		}
		// Partial credit for close answers.
		diff := output.Value - expected.Value
		if diff < 0 {
			diff = -diff
		}
		if diff <= 1 {
			return &eval.Score{Value: 0.8, Reason: "close"}, nil
		}
		return &eval.Score{Value: 0.0, Reason: "wrong"}, nil
	})

	runner := eval.NewRunner[MathAnswer](agent, customEval).WithPassScore(0.7)
	dataset := eval.Dataset[MathAnswer]{
		Name: "simple-math",
		Cases: []eval.Case[MathAnswer]{
			{
				Name:     "addition",
				Prompt:   "Return a JSON object with 'value' set to the result of 15+27.",
				Expected: MathAnswer{Value: 42},
			},
		},
	}

	report, err := runner.Run(ctx, dataset)
	if err != nil {
		t.Fatalf("eval runner failed: %v", err)
	}

	for _, r := range report.Results {
		if r.Error != nil {
			skipOnAccountError(t, r.Error)
		}
	}

	if report.PassedCases < 1 {
		t.Errorf("expected at least 1 passed case, got %d", report.PassedCases)
	}

	t.Logf("Custom eval: %d/%d passed, avg=%.2f", report.PassedCases, report.TotalCases, report.AvgScore)
}

// TestEvalRunnerWithStepEvaluator verifies step evaluators score individual steps.
func TestEvalRunnerWithStepEvaluator(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	runner := eval.NewRunner[string](agent, eval.Contains()).
		WithStepEvaluators(eval.MaxStepsEvaluator(10))

	dataset := eval.Dataset[string]{
		Name: "step-eval",
		Cases: []eval.Case[string]{
			{
				Name:     "simple",
				Prompt:   "Say hello",
				Expected: "hello",
			},
		},
	}

	report, err := runner.Run(ctx, dataset)
	if err != nil {
		t.Fatalf("eval runner failed: %v", err)
	}

	for _, r := range report.Results {
		if r.Error != nil {
			skipOnAccountError(t, r.Error)
		}
	}

	// Step evaluator should have passed (well under 10 steps).
	if report.TotalStepEvals == 0 {
		t.Error("expected at least 1 step evaluation")
	}
	if report.StepPassRate < 1.0 {
		t.Errorf("expected all steps to pass (within 10 step limit), got pass rate %.2f", report.StepPassRate)
	}

	t.Logf("Step eval: %d evals, pass=%.2f fail=%.2f", report.TotalStepEvals, report.StepPassRate, report.StepFailRate)
}

// TestEvalRunnerLLMJudge verifies the LLM-based evaluation judge.
func TestEvalRunnerLLMJudge(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	model := newAnthropicProvider()
	agent := core.NewAgent[string](model)

	judge := eval.LLMJudge[string](model, "The output should be a polite greeting that mentions the user's name.")

	runner := eval.NewRunner[string](agent, judge)
	dataset := eval.Dataset[string]{
		Name: "llm-judge",
		Cases: []eval.Case[string]{
			{
				Name:     "greeting",
				Prompt:   "Greet the user named Alice politely.",
				Expected: "A polite greeting mentioning Alice",
			},
		},
	}

	report, err := runner.Run(ctx, dataset)
	if err != nil {
		t.Fatalf("eval runner failed: %v", err)
	}

	for _, r := range report.Results {
		if r.Error != nil {
			skipOnAccountError(t, r.Error)
		}
	}

	if report.AvgScore < 0.3 {
		t.Errorf("expected reasonable LLM judge score, got %.2f", report.AvgScore)
	}

	t.Logf("LLM judge: avg=%.2f, scores=%v", report.AvgScore, func() []float64 {
		var scores []float64
		for _, r := range report.Results {
			for _, s := range r.Scores {
				scores = append(scores, s.Value)
			}
		}
		return scores
	}())
}

// TestEvalRunnerNoRetryEvaluator verifies the NoRetryEvaluator step evaluator.
func TestEvalRunnerNoRetryEvaluator(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	// Simple prompt shouldn't trigger retries.
	runner := eval.NewRunner[string](agent, eval.Contains()).
		WithStepEvaluators(eval.NoRetryEvaluator())

	dataset := eval.Dataset[string]{
		Name: "no-retry",
		Cases: []eval.Case[string]{
			{
				Name:     "simple",
				Prompt:   "Say the word 'test'",
				Expected: "test",
			},
		},
	}

	report, err := runner.Run(ctx, dataset)
	if err != nil {
		t.Fatalf("eval runner failed: %v", err)
	}

	for _, r := range report.Results {
		if r.Error != nil {
			skipOnAccountError(t, r.Error)
		}
	}

	// All step evals should pass (no retries expected).
	for _, r := range report.Results {
		for _, s := range r.StepScores {
			if s.Value < 1.0 {
				t.Logf("unexpected retry detected: %s", s.Reason)
			}
		}
	}

	t.Logf("NoRetry eval: step_evals=%d pass_rate=%.2f", report.TotalStepEvals, report.StepPassRate)
}

// TestEvalRunnerDatasetError verifies graceful handling when agent errors during eval.
func TestEvalRunnerDatasetError(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Agent with very low usage limit to force an error.
	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithUsageLimits[string](core.UsageLimits{
			RequestLimit: core.IntPtr(1),
		}),
		core.WithTools[string](core.FuncTool[CalcParams]("add", "Add numbers",
			func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
				return "result", nil
			},
		)),
	)

	runner := eval.NewRunner[string](agent, eval.Contains())
	dataset := eval.Dataset[string]{
		Name: "error-handling",
		Cases: []eval.Case[string]{
			{
				Name:     "will-error",
				Prompt:   "Use the add tool to compute 1+2, then explain the result in detail.",
				Expected: "3",
			},
		},
	}

	report, err := runner.Run(ctx, dataset)
	if err != nil {
		t.Fatalf("eval runner should not return top-level error: %v", err)
	}

	// The case should have an error but the runner should still complete.
	hasError := false
	for _, r := range report.Results {
		if r.Error != nil {
			hasError = true
			if !strings.Contains(r.Error.Error(), "limit") {
				skipOnAccountError(t, r.Error)
			}
		}
	}

	if !hasError {
		t.Log("agent completed despite limits (fast response), not an error")
	} else {
		t.Logf("Eval gracefully handled error: failed=%d", report.FailedCases)
	}
}

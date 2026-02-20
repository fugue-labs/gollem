package eval

import (
	"context"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

type greetParams struct {
	Name string `json:"name"`
}

func TestStepEvaluator_MaxSteps(t *testing.T) {
	// Create a model that requires multiple steps: tool call then text response.
	model := core.NewTestModel(
		core.ToolCallResponse("greet", `{"name":"Alice"}`),
		core.TextResponse("Hello Alice!"),
	)

	greetTool := core.FuncTool[greetParams]("greet", "Greet a person", func(_ context.Context, _ *core.RunContext, params greetParams) (string, error) {
		return "Hi " + params.Name, nil
	})

	agent := core.NewAgent[string](model, core.WithTools[string](greetTool))

	dataset := Dataset[string]{
		Name: "max-steps-test",
		Cases: []Case[string]{
			{Name: "greet", Prompt: "Greet Alice", Expected: "Hello Alice!"},
		},
	}

	// MaxSteps=1 should fail since the agent takes 2 steps (tool call + final response).
	runner := NewRunner(agent, ExactMatch[string]()).
		WithStepEvaluators(MaxStepsEvaluator(1))

	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(report.Results))
	}

	cr := report.Results[0]
	if len(cr.StepScores) == 0 {
		t.Fatal("expected step scores, got none")
	}

	// All step scores should be 0.0 because total steps (2) > max (1).
	for i, ss := range cr.StepScores {
		if ss.Value != 0.0 {
			t.Errorf("step score %d: expected 0.0, got %f (reason: %s)", i, ss.Value, ss.Reason)
		}
	}
}

func TestStepEvaluator_NoRetry(t *testing.T) {
	// Create a model that first gives an empty response (triggers retry), then responds.
	model := core.NewTestModel(
		&core.ModelResponse{
			Parts:        []core.ModelResponsePart{},
			FinishReason: core.FinishReasonStop,
			ModelName:    "test-model",
		},
		core.TextResponse("Hello!"),
	)

	agent := core.NewAgent[string](model)

	dataset := Dataset[string]{
		Name: "no-retry-test",
		Cases: []Case[string]{
			{Name: "retry-case", Prompt: "Say hello", Expected: "Hello!"},
		},
	}

	runner := NewRunner(agent, Contains()).
		WithStepEvaluators(NoRetryEvaluator())

	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(report.Results))
	}

	cr := report.Results[0]
	if len(cr.StepScores) == 0 {
		t.Fatal("expected step scores, got none")
	}

	// At least one step score should be 0.0 because a retry occurred.
	hasFailure := false
	for _, ss := range cr.StepScores {
		if ss.Value == 0.0 {
			hasFailure = true
			break
		}
	}
	if !hasFailure {
		t.Error("expected at least one step score of 0.0 due to retry, but all passed")
	}
}

func TestStepEvaluator_AllPass(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Hello!"))
	agent := core.NewAgent[string](model)

	dataset := Dataset[string]{
		Name: "all-pass-test",
		Cases: []Case[string]{
			{Name: "simple", Prompt: "Say hello", Expected: "Hello"},
		},
	}

	runner := NewRunner(agent, Contains()).
		WithStepEvaluators(MaxStepsEvaluator(10), NoRetryEvaluator())

	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cr := report.Results[0]
	if len(cr.StepScores) == 0 {
		t.Fatal("expected step scores, got none")
	}

	for i, ss := range cr.StepScores {
		if ss.Value != 1.0 {
			t.Errorf("step score %d: expected 1.0, got %f (reason: %s)", i, ss.Value, ss.Reason)
		}
	}
}

func TestStepEvaluator_Integration(t *testing.T) {
	// Step evaluator runs alongside output evaluator.
	model := core.NewTestModel(core.TextResponse("Hello World"))
	agent := core.NewAgent[string](model)

	dataset := Dataset[string]{
		Name: "integration-test",
		Cases: []Case[string]{
			{Name: "test", Prompt: "Say hello world", Expected: "Hello World"},
		},
	}

	runner := NewRunner(agent, ExactMatch[string]()).
		WithStepEvaluators(MaxStepsEvaluator(5))

	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cr := report.Results[0]

	// Output evaluator should have scored.
	if len(cr.Scores) != 1 {
		t.Errorf("expected 1 output score, got %d", len(cr.Scores))
	}
	if cr.Scores[0].Value != 1.0 {
		t.Errorf("expected output score 1.0, got %f", cr.Scores[0].Value)
	}

	// Step evaluator should have scored.
	if len(cr.StepScores) == 0 {
		t.Fatal("expected step scores, got none")
	}
	for i, ss := range cr.StepScores {
		if ss.Value != 1.0 {
			t.Errorf("step score %d: expected 1.0, got %f", i, ss.Value)
		}
	}
}

func TestStepReport_Aggregation(t *testing.T) {
	// Two cases: one that passes step eval, one that fails.
	model := core.NewTestModel(
		core.TextResponse("Match"),
		// Second case: the model needs two responses (tool call + text).
		core.ToolCallResponse("greet", `{"name":"Bob"}`),
		core.TextResponse("NoMatch"),
	)

	greetTool := core.FuncTool[greetParams]("greet", "Greet", func(_ context.Context, _ *core.RunContext, params greetParams) (string, error) {
		return "Hi " + params.Name, nil
	})

	agent := core.NewAgent[string](model, core.WithTools[string](greetTool))

	dataset := Dataset[string]{
		Name: "aggregation-test",
		Cases: []Case[string]{
			{Name: "simple", Prompt: "Say Match", Expected: "Match"},
			{Name: "multi-step", Prompt: "Greet Bob", Expected: "NoMatch"},
		},
	}

	// MaxSteps=1 means the first case (1 step) passes, second case (2 steps) fails.
	runner := NewRunner(agent, ExactMatch[string]()).
		WithStepEvaluators(MaxStepsEvaluator(1))

	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify step scores appear in report.
	if report.TotalStepEvals == 0 {
		t.Fatal("expected step evaluations in report, got 0")
	}

	// First case: 1 step, within limit -> pass.
	// Second case: 2 steps, exceeds limit -> both step evals fail.
	// So we expect: 1 passing step eval + 2 failing step evals = 3 total.
	if report.TotalStepEvals != 3 {
		t.Errorf("expected 3 total step evals, got %d", report.TotalStepEvals)
	}

	// Step pass rate should be 1/3.
	expectedPassRate := 1.0 / 3.0
	if diff := report.StepPassRate - expectedPassRate; diff > 0.01 || diff < -0.01 {
		t.Errorf("expected step pass rate ~%.4f, got %.4f", expectedPassRate, report.StepPassRate)
	}

	// Step fail rate should be 2/3.
	expectedFailRate := 2.0 / 3.0
	if diff := report.StepFailRate - expectedFailRate; diff > 0.01 || diff < -0.01 {
		t.Errorf("expected step fail rate ~%.4f, got %.4f", expectedFailRate, report.StepFailRate)
	}

	// Verify individual case step scores.
	if len(report.Results[0].StepScores) != 1 {
		t.Errorf("case 0: expected 1 step score, got %d", len(report.Results[0].StepScores))
	}
	if report.Results[0].StepScores[0].Value != 1.0 {
		t.Errorf("case 0 step score: expected 1.0, got %f", report.Results[0].StepScores[0].Value)
	}

	if len(report.Results[1].StepScores) != 2 {
		t.Errorf("case 1: expected 2 step scores, got %d", len(report.Results[1].StepScores))
	}
	for i, ss := range report.Results[1].StepScores {
		if ss.Value != 0.0 {
			t.Errorf("case 1 step score %d: expected 0.0, got %f", i, ss.Value)
		}
	}
}

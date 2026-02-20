package eval

import (
	"context"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestExactMatch(t *testing.T) {
	evaluator := ExactMatch[string]()

	score, err := evaluator.Evaluate(context.Background(), "hello", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.Value != 1.0 {
		t.Errorf("expected score 1.0 for exact match, got %f", score.Value)
	}

	score, err = evaluator.Evaluate(context.Background(), "hello", "world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.Value != 0.0 {
		t.Errorf("expected score 0.0 for mismatch, got %f", score.Value)
	}
}

func TestExactMatch_Struct(t *testing.T) {
	type Result struct {
		Name string
		Age  int
	}

	evaluator := ExactMatch[Result]()

	score, err := evaluator.Evaluate(context.Background(), Result{"Alice", 30}, Result{"Alice", 30})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.Value != 1.0 {
		t.Errorf("expected score 1.0, got %f", score.Value)
	}

	score, err = evaluator.Evaluate(context.Background(), Result{"Alice", 30}, Result{"Bob", 25})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.Value != 0.0 {
		t.Errorf("expected score 0.0, got %f", score.Value)
	}
}

func TestContains(t *testing.T) {
	evaluator := Contains()

	score, err := evaluator.Evaluate(context.Background(), "Hello World!", "World")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.Value != 1.0 {
		t.Errorf("expected score 1.0, got %f", score.Value)
	}

	score, err = evaluator.Evaluate(context.Background(), "Hello World!", "Goodbye")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.Value != 0.0 {
		t.Errorf("expected score 0.0, got %f", score.Value)
	}
}

func TestJSONMatch(t *testing.T) {
	type Data struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	evaluator := JSONMatch[Data]()

	score, err := evaluator.Evaluate(context.Background(), Data{"Alice", 30}, Data{"Alice", 30})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.Value != 1.0 {
		t.Errorf("expected score 1.0, got %f", score.Value)
	}
}

func TestCustomEvaluator(t *testing.T) {
	evaluator := Custom[string](func(_ context.Context, output, expected string) (*Score, error) {
		if len(output) >= len(expected) {
			return &Score{Value: 1.0, Reason: "length check passed"}, nil
		}
		return &Score{Value: 0.0, Reason: "output too short"}, nil
	})

	score, err := evaluator.Evaluate(context.Background(), "long output", "short")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.Value != 1.0 {
		t.Errorf("expected score 1.0, got %f", score.Value)
	}
}

func TestLLMJudge(t *testing.T) {
	// Mock model returns "Score: 1.0 - perfect match".
	model := core.NewTestModel(core.TextResponse("Score: 1.0 - perfect match"))
	evaluator := LLMJudge[string](model, "Check if the greeting is friendly")

	score, err := evaluator.Evaluate(context.Background(), "Hello, how are you?", "A friendly greeting")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.Value != 1.0 {
		t.Errorf("expected score 1.0 for 'perfect', got %f", score.Value)
	}
}

func TestLLMJudge_Moderate(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Score: 0.5 - partial match"))
	evaluator := LLMJudge[string](model, "Check quality")

	score, err := evaluator.Evaluate(context.Background(), "OK output", "Great output")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.Value != 0.5 {
		t.Errorf("expected score 0.5 for 'partial', got %f", score.Value)
	}
}

func TestRunner_FullDataset(t *testing.T) {
	model := core.NewTestModel(
		core.TextResponse("Hello, World!"),
		core.TextResponse("Goodbye!"),
		core.TextResponse("Thanks!"),
	)
	agent := core.NewAgent[string](model)

	dataset := Dataset[string]{
		Name: "greeting-test",
		Cases: []Case[string]{
			{Name: "hello", Prompt: "Say hello", Expected: "Hello"},
			{Name: "goodbye", Prompt: "Say goodbye", Expected: "Goodbye"},
			{Name: "thanks", Prompt: "Say thanks", Expected: "Thanks"},
		},
	}

	runner := NewRunner(agent, Contains())
	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalCases != 3 {
		t.Errorf("expected 3 total cases, got %d", report.TotalCases)
	}
	if report.PassedCases != 3 {
		t.Errorf("expected 3 passed cases, got %d", report.PassedCases)
	}
	if report.AvgScore != 1.0 {
		t.Errorf("expected avg score 1.0, got %f", report.AvgScore)
	}
}

func TestReport_Aggregation(t *testing.T) {
	model := core.NewTestModel(
		core.TextResponse("Match"),
		core.TextResponse("NoMatch"),
	)
	agent := core.NewAgent[string](model)

	dataset := Dataset[string]{
		Name: "mixed-test",
		Cases: []Case[string]{
			{Name: "match", Prompt: "Say Match", Expected: "Match"},
			{Name: "nomatch", Prompt: "Say something", Expected: "Expected"},
		},
	}

	runner := NewRunner(agent, ExactMatch[string]())
	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.TotalCases != 2 {
		t.Errorf("expected 2 total, got %d", report.TotalCases)
	}
	if report.PassedCases != 1 {
		t.Errorf("expected 1 passed, got %d", report.PassedCases)
	}
	if report.FailedCases != 1 {
		t.Errorf("expected 1 failed, got %d", report.FailedCases)
	}
	if report.AvgScore != 0.5 {
		t.Errorf("expected avg score 0.5, got %f", report.AvgScore)
	}
}

func TestRunner_MultipleEvaluators(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Hello World"))
	agent := core.NewAgent[string](model)

	dataset := Dataset[string]{
		Name: "multi-eval",
		Cases: []Case[string]{
			{Name: "test", Prompt: "Say hello world", Expected: "Hello World"},
		},
	}

	runner := NewRunner(agent, ExactMatch[string](), Contains())
	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Results[0].Scores) != 2 {
		t.Errorf("expected 2 scores, got %d", len(report.Results[0].Scores))
	}
}

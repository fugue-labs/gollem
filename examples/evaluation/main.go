// Example evaluation demonstrates how to test agent quality using the eval
// package. It defines a dataset of test cases, runs them through an agent,
// and scores the results with built-in evaluators.
//
// This example uses TestModel with canned responses so it works without any
// API keys or external services.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/eval"
)

func main() {
	// Create a TestModel that returns the final_result tool call.
	// Each response corresponds to one evaluation case.
	// The TestModel reuses the last response for any extra calls.
	model := core.NewTestModel(
		// Response for "capital of France" - correct.
		core.ToolCallResponse("final_result", `"Paris"`),
	)

	// Create a simple string agent.
	agent := core.NewAgent[string](model,
		core.WithSystemPrompt[string]("You are a geography quiz assistant. Answer with just the city name."),
	)

	// Define an evaluation dataset.
	dataset := eval.Dataset[string]{
		Name: "world-capitals",
		Cases: []eval.Case[string]{
			{
				Name:     "france",
				Prompt:   "What is the capital of France?",
				Expected: "Paris",
			},
			{
				Name:     "japan",
				Prompt:   "What is the capital of Japan?",
				Expected: "Tokyo",
			},
			{
				Name:     "brazil",
				Prompt:   "What is the capital of Brazil?",
				Expected: "Brasilia",
			},
		},
	}

	// Create an evaluation runner with the ExactMatch evaluator.
	runner := eval.NewRunner(agent, eval.ExactMatch[string]())
	runner.WithPassScore(0.5)

	// Run the evaluation.
	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		log.Fatal(err)
	}

	// Print the report.
	fmt.Printf("Dataset: %s\n", report.DatasetName)
	fmt.Printf("Total cases: %d\n", report.TotalCases)
	fmt.Printf("Passed: %d\n", report.PassedCases)
	fmt.Printf("Failed: %d\n", report.FailedCases)
	fmt.Printf("Average score: %.2f\n\n", report.AvgScore)

	for _, cr := range report.Results {
		fmt.Printf("  Case %q:\n", cr.CaseName)
		if cr.Error != nil {
			fmt.Printf("    Error: %v\n", cr.Error)
			continue
		}
		fmt.Printf("    Output: %v\n", cr.Output)
		fmt.Printf("    Duration: %s\n", cr.Duration)
		for _, score := range cr.Scores {
			fmt.Printf("    Score: %.2f (%s)\n", score.Value, score.Reason)
		}
	}

	// Demonstrate the Contains evaluator with a separate runner.
	fmt.Println("\n--- Contains Evaluator ---")
	containsRunner := eval.NewRunner(agent, eval.Contains())
	containsDataset := eval.Dataset[string]{
		Name: "partial-match",
		Cases: []eval.Case[string]{
			{
				Name:     "france-contains",
				Prompt:   "What is the capital of France?",
				Expected: "Par", // "Paris" contains "Par"
			},
		},
	}
	containsReport, err := containsRunner.Run(context.Background(), containsDataset)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Contains test: %.2f average score\n", containsReport.AvgScore)

	// Demonstrate the Custom evaluator.
	fmt.Println("\n--- Custom Evaluator ---")
	customEval := eval.Custom[string](func(_ context.Context, output, expected string) (*eval.Score, error) {
		if len(output) > 0 && len(expected) > 0 {
			return &eval.Score{Value: 0.75, Reason: "non-empty output"}, nil
		}
		return &eval.Score{Value: 0.0, Reason: "empty output"}, nil
	})
	customRunner := eval.NewRunner(agent, customEval)
	customReport, err := customRunner.Run(context.Background(), containsDataset)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Custom test: %.2f average score\n", customReport.AvgScore)
}

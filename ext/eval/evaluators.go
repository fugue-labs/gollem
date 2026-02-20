package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// exactMatch checks if output equals expected (using reflect.DeepEqual).
type exactMatch[T any] struct{}

// ExactMatch creates an evaluator that checks for exact equality.
func ExactMatch[T any]() Evaluator[T] {
	return &exactMatch[T]{}
}

func (e *exactMatch[T]) Evaluate(_ context.Context, output T, expected T) (*Score, error) {
	if reflect.DeepEqual(output, expected) {
		return &Score{Value: 1.0, Reason: "exact match"}, nil
	}
	return &Score{
		Value:  0.0,
		Reason: fmt.Sprintf("expected %v, got %v", expected, output),
	}, nil
}

// containsEval checks if string output contains expected substring.
type containsEval struct{}

// Contains creates an evaluator that checks if output contains the expected string.
func Contains() Evaluator[string] {
	return &containsEval{}
}

func (e *containsEval) Evaluate(_ context.Context, output string, expected string) (*Score, error) {
	if strings.Contains(output, expected) {
		return &Score{Value: 1.0, Reason: "contains expected string"}, nil
	}
	return &Score{
		Value:  0.0,
		Reason: fmt.Sprintf("output %q does not contain %q", output, expected),
	}, nil
}

// jsonMatch checks if JSON-serialized outputs match.
type jsonMatch[T any] struct{}

// JSONMatch creates an evaluator that compares JSON representations.
func JSONMatch[T any]() Evaluator[T] {
	return &jsonMatch[T]{}
}

func (e *jsonMatch[T]) Evaluate(_ context.Context, output T, expected T) (*Score, error) {
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("marshaling output: %w", err)
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		return nil, fmt.Errorf("marshaling expected: %w", err)
	}

	// Normalize by unmarshaling and re-marshaling.
	var outputNorm, expectedNorm any
	if err := json.Unmarshal(outputJSON, &outputNorm); err != nil {
		return nil, fmt.Errorf("normalizing output: %w", err)
	}
	if err := json.Unmarshal(expectedJSON, &expectedNorm); err != nil {
		return nil, fmt.Errorf("normalizing expected: %w", err)
	}

	if reflect.DeepEqual(outputNorm, expectedNorm) {
		return &Score{Value: 1.0, Reason: "JSON match"}, nil
	}
	return &Score{
		Value:  0.0,
		Reason: fmt.Sprintf("JSON mismatch: %s vs %s", outputJSON, expectedJSON),
	}, nil
}

// custom wraps a function as an evaluator.
type custom[T any] struct {
	fn func(ctx context.Context, output, expected T) (*Score, error)
}

// Custom creates an evaluator from a function.
func Custom[T any](fn func(ctx context.Context, output, expected T) (*Score, error)) Evaluator[T] {
	return &custom[T]{fn: fn}
}

func (e *custom[T]) Evaluate(ctx context.Context, output T, expected T) (*Score, error) {
	return e.fn(ctx, output, expected)
}

// llmJudge uses an LLM to evaluate output quality against criteria.
type llmJudge[T any] struct {
	model    core.Model
	criteria string
}

// LLMJudge uses an LLM to evaluate output quality against criteria.
func LLMJudge[T any](model core.Model, criteria string) Evaluator[T] {
	return &llmJudge[T]{
		model:    model,
		criteria: criteria,
	}
}

func (e *llmJudge[T]) Evaluate(ctx context.Context, output T, expected T) (*Score, error) {
	prompt := fmt.Sprintf(
		"Evaluate the following output against the expected result.\n\n"+
			"Criteria: %s\n\n"+
			"Expected: %v\n\n"+
			"Actual Output: %v\n\n"+
			"Respond with a score from 0.0 to 1.0 and a brief explanation.",
		e.criteria, expected, output,
	)

	req := core.ModelRequest{
		Parts: []core.ModelRequestPart{
			core.SystemPromptPart{Content: "You are an evaluation judge. Score outputs on a 0.0 to 1.0 scale."},
			core.UserPromptPart{Content: prompt},
		},
		Timestamp: time.Now(),
	}

	resp, err := e.model.Request(ctx, []core.ModelMessage{req}, nil, &core.ModelRequestParameters{
		AllowTextOutput: true,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM judge request failed: %w", err)
	}

	text := resp.TextContent()
	// For simplicity, if the response contains "1.0" or "perfect" or "excellent", score high.
	// In production, you'd parse the structured response.
	score := 0.5 // default moderate score
	lowerText := strings.ToLower(text)
	if strings.Contains(lowerText, "1.0") || strings.Contains(lowerText, "perfect") || strings.Contains(lowerText, "excellent") {
		score = 1.0
	} else if strings.Contains(lowerText, "0.0") || strings.Contains(lowerText, "fail") || strings.Contains(lowerText, "incorrect") {
		score = 0.0
	} else if strings.Contains(lowerText, "0.8") || strings.Contains(lowerText, "good") {
		score = 0.8
	} else if strings.Contains(lowerText, "0.5") || strings.Contains(lowerText, "partial") {
		score = 0.5
	}

	return &Score{
		Value:  score,
		Reason: text,
	}, nil
}

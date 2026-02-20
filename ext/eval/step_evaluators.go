package eval

import (
	"context"
	"fmt"

	"github.com/fugue-labs/gollem/core"
)

// maxStepsEvaluator fails if the agent takes more than N steps.
type maxStepsEvaluator struct {
	maxSteps int
}

// MaxStepsEvaluator creates a step evaluator that fails if the agent takes more than N steps.
func MaxStepsEvaluator(maxSteps int) StepEvaluator {
	return &maxStepsEvaluator{maxSteps: maxSteps}
}

func (e *maxStepsEvaluator) EvaluateStep(_ context.Context, _ *core.ModelResponse, _ int, state *StepState) (*Score, error) {
	if state.TotalSteps > e.maxSteps {
		return &Score{
			Value:  0.0,
			Reason: fmt.Sprintf("agent took %d steps, exceeding limit of %d", state.TotalSteps, e.maxSteps),
		}, nil
	}
	return &Score{
		Value:  1.0,
		Reason: fmt.Sprintf("step count %d within limit of %d", state.TotalSteps, e.maxSteps),
	}, nil
}

// noRetryEvaluator fails if any step contains a RetryPromptPart.
type noRetryEvaluator struct{}

// NoRetryEvaluator creates a step evaluator that fails if any step's surrounding
// request messages contain a RetryPromptPart.
func NoRetryEvaluator() StepEvaluator {
	return &noRetryEvaluator{}
}

func (e *noRetryEvaluator) EvaluateStep(_ context.Context, _ *core.ModelResponse, _ int, state *StepState) (*Score, error) {
	for _, msg := range state.Messages {
		if req, ok := msg.(core.ModelRequest); ok {
			for _, part := range req.Parts {
				if _, isRetry := part.(core.RetryPromptPart); isRetry {
					return &Score{
						Value:  0.0,
						Reason: "conversation contains a retry prompt",
					}, nil
				}
			}
		}
	}
	return &Score{
		Value:  1.0,
		Reason: "no retry prompts found",
	}, nil
}

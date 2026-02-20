package orchestration

import (
	"context"
	"strings"
	"sync"

	"github.com/fugue-labs/gollem/core"
)

// PipelineStep is a single step in a pipeline.
type PipelineStep func(ctx context.Context, input string) (string, error)

// Pipeline chains multiple steps sequentially.
type Pipeline struct {
	steps []PipelineStep
}

// NewPipeline creates a new pipeline.
func NewPipeline(steps ...PipelineStep) *Pipeline {
	return &Pipeline{steps: steps}
}

// Run executes the pipeline with an initial input.
func (p *Pipeline) Run(ctx context.Context, input string) (string, error) {
	current := input
	for _, step := range p.steps {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		result, err := step(ctx, current)
		if err != nil {
			return "", err
		}
		current = result
	}
	return current, nil
}

// Then appends a step to the pipeline, returning a new pipeline.
func (p *Pipeline) Then(step PipelineStep) *Pipeline {
	steps := make([]PipelineStep, len(p.steps)+1)
	copy(steps, p.steps)
	steps[len(p.steps)] = step
	return &Pipeline{steps: steps}
}

// AgentStep wraps an Agent[string] as a PipelineStep.
func AgentStep(agent *core.Agent[string]) PipelineStep {
	return func(ctx context.Context, input string) (string, error) {
		result, err := agent.Run(ctx, input)
		if err != nil {
			return "", err
		}
		return result.Output, nil
	}
}

// TransformStep creates a step that transforms the input string.
func TransformStep(fn func(string) string) PipelineStep {
	return func(_ context.Context, input string) (string, error) {
		return fn(input), nil
	}
}

// ParallelSteps runs multiple steps concurrently and joins results with newlines.
func ParallelSteps(steps ...PipelineStep) PipelineStep {
	return func(ctx context.Context, input string) (string, error) {
		results := make([]string, len(steps))
		errs := make([]error, len(steps))
		var wg sync.WaitGroup

		for i, step := range steps {
			wg.Add(1)
			go func(i int, step PipelineStep) {
				defer wg.Done()
				results[i], errs[i] = step(ctx, input)
			}(i, step)
		}
		wg.Wait()

		// Return first error.
		for _, err := range errs {
			if err != nil {
				return "", err
			}
		}

		return strings.Join(results, "\n"), nil
	}
}

// ConditionalStep runs one of two steps based on a predicate.
func ConditionalStep(predicate func(string) bool, ifTrue, ifFalse PipelineStep) PipelineStep {
	return func(ctx context.Context, input string) (string, error) {
		if predicate(input) {
			return ifTrue(ctx, input)
		}
		return ifFalse(ctx, input)
	}
}

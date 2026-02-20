package orchestration

import (
	"context"

	"github.com/fugue-labs/gollem/core"
)

// ChainRunResult includes both intermediate and final results.
type ChainRunResult[A, B any] struct {
	Intermediate A
	Final        B
	TotalUsage   core.RunUsage
}

// ChainRun runs the first agent, transforms its output into a prompt,
// then runs the second agent with that prompt. Returns the second agent's result.
func ChainRun[A, B any](ctx context.Context, first *core.Agent[A], second *core.Agent[B], prompt string, transform func(A) string, opts ...core.RunOption) (*core.RunResult[B], error) {
	firstResult, err := first.Run(ctx, prompt, opts...)
	if err != nil {
		return nil, err
	}

	secondPrompt := transform(firstResult.Output)
	secondResult, err := second.Run(ctx, secondPrompt, opts...)
	if err != nil {
		return nil, err
	}

	// Combine usage.
	secondResult.Usage.IncrRun(firstResult.Usage)

	return secondResult, nil
}

// ChainRunFull is like ChainRun but returns both intermediate and final results.
func ChainRunFull[A, B any](ctx context.Context, first *core.Agent[A], second *core.Agent[B], prompt string, transform func(A) string, opts ...core.RunOption) (*ChainRunResult[A, B], error) {
	firstResult, err := first.Run(ctx, prompt, opts...)
	if err != nil {
		return nil, err
	}

	secondPrompt := transform(firstResult.Output)
	secondResult, err := second.Run(ctx, secondPrompt, opts...)
	if err != nil {
		return nil, err
	}

	totalUsage := firstResult.Usage
	totalUsage.IncrRun(secondResult.Usage)

	return &ChainRunResult[A, B]{
		Intermediate: firstResult.Output,
		Final:        secondResult.Output,
		TotalUsage:   totalUsage,
	}, nil
}

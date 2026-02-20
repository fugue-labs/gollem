package core

import (
	"context"
	"errors"
	"fmt"
)

// ReflectOption configures the reflection loop.
type ReflectOption func(*reflectConfig)

type reflectConfig struct {
	maxReflections int
	reflectPrompt  string
}

// WithMaxReflections sets the maximum number of reflection iterations (default: 3).
func WithMaxReflections(n int) ReflectOption {
	return func(c *reflectConfig) {
		c.maxReflections = n
	}
}

// WithReflectPrompt sets a custom reflection prompt template.
// Use %s as placeholder for the output being reviewed.
func WithReflectPrompt(prompt string) ReflectOption {
	return func(c *reflectConfig) {
		c.reflectPrompt = prompt
	}
}

// ReflectResult contains the output of a reflection loop.
type ReflectResult[T any] struct {
	// Output is the final (possibly corrected) output.
	Output T
	// Iterations is the number of reflection iterations performed.
	Iterations int
	// Usage is the combined usage across all iterations.
	Usage RunUsage
	// Accepted indicates whether the output was accepted by the validator.
	Accepted bool
}

// RunWithReflection runs the agent with a reflection/self-correction loop.
// After each run, the validator function checks the output. If the validator
// returns an error message, the agent is re-run with that feedback appended
// to the conversation. If the validator returns empty string, the output is accepted.
//
// The validator receives the output and returns:
//   - "" (empty string) if the output is acceptable
//   - A non-empty feedback string describing what needs to be corrected
func RunWithReflection[T any](
	ctx context.Context,
	agent *Agent[T],
	prompt string,
	validator func(ctx context.Context, output T) (feedback string, err error),
	opts ...ReflectOption,
) (*ReflectResult[T], error) {
	cfg := &reflectConfig{
		maxReflections: 3,
		reflectPrompt:  "Your previous output was reviewed and needs correction:\n\n%s\n\nPlease provide a corrected response.",
	}
	for _, opt := range opts {
		opt(cfg)
	}

	var totalUsage RunUsage
	currentPrompt := prompt

	for i := 0; i <= cfg.maxReflections; i++ {
		result, err := agent.Run(ctx, currentPrompt)
		if err != nil {
			return nil, fmt.Errorf("reflection iteration %d: %w", i, err)
		}

		// Aggregate usage.
		totalUsage.IncrRun(result.Usage)

		// Validate.
		feedback, err := validator(ctx, result.Output)
		if err != nil {
			return nil, fmt.Errorf("validator error at iteration %d: %w", i, err)
		}

		if feedback == "" {
			// Output accepted.
			return &ReflectResult[T]{
				Output:     result.Output,
				Iterations: i + 1,
				Usage:      totalUsage,
				Accepted:   true,
			}, nil
		}

		if i == cfg.maxReflections {
			// Max reflections reached, return last output.
			return &ReflectResult[T]{
				Output:     result.Output,
				Iterations: i + 1,
				Usage:      totalUsage,
				Accepted:   false,
			}, nil
		}

		// Build correction prompt.
		currentPrompt = fmt.Sprintf(cfg.reflectPrompt, feedback)
	}

	// Should not reach here.
	return nil, errors.New("reflection loop unexpectedly ended")
}

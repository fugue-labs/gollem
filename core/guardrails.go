package core

import (
	"context"
	"fmt"
	"strings"
)

// InputGuardrailFunc validates or transforms the user prompt before the agent run begins.
// Return the (possibly modified) prompt, or error to reject.
type InputGuardrailFunc func(ctx context.Context, prompt string) (string, error)

// TurnGuardrailFunc validates messages before each model request within the run loop.
// Return error to abort the run. This catches issues that develop during multi-turn conversations.
type TurnGuardrailFunc func(ctx context.Context, rc *RunContext, messages []ModelMessage) error

// GuardrailError is returned when a guardrail rejects the input.
type GuardrailError struct {
	GuardrailName string
	Message       string
}

func (e *GuardrailError) Error() string {
	return fmt.Sprintf("guardrail %q: %s", e.GuardrailName, e.Message)
}

type namedInputGuardrail struct {
	name string
	fn   InputGuardrailFunc
}

type namedTurnGuardrail struct {
	name string
	fn   TurnGuardrailFunc
}

// WithInputGuardrail adds an input guardrail that runs before the agent loop.
func WithInputGuardrail[T any](name string, fn InputGuardrailFunc) AgentOption[T] {
	return func(a *Agent[T]) {
		a.inputGuardrails = append(a.inputGuardrails, namedInputGuardrail{name: name, fn: fn})
	}
}

// WithTurnGuardrail adds a turn guardrail that runs before each model request.
func WithTurnGuardrail[T any](name string, fn TurnGuardrailFunc) AgentOption[T] {
	return func(a *Agent[T]) {
		a.turnGuardrails = append(a.turnGuardrails, namedTurnGuardrail{name: name, fn: fn})
	}
}

// MaxPromptLength rejects prompts exceeding a character limit.
func MaxPromptLength(maxChars int) InputGuardrailFunc {
	return func(ctx context.Context, prompt string) (string, error) {
		if len(prompt) > maxChars {
			return "", fmt.Errorf("prompt length %d exceeds maximum %d characters", len(prompt), maxChars)
		}
		return prompt, nil
	}
}

// ContentFilter rejects prompts containing any of the given substrings (case-insensitive).
func ContentFilter(blocked ...string) InputGuardrailFunc {
	return func(ctx context.Context, prompt string) (string, error) {
		lower := strings.ToLower(prompt)
		for _, b := range blocked {
			if strings.Contains(lower, strings.ToLower(b)) {
				return "", fmt.Errorf("prompt contains blocked content: %q", b)
			}
		}
		return prompt, nil
	}
}

// MaxTurns aborts the run if it exceeds N model requests.
func MaxTurns(n int) TurnGuardrailFunc {
	return func(ctx context.Context, rc *RunContext, messages []ModelMessage) error {
		if rc.RunStep > n {
			return fmt.Errorf("exceeded maximum of %d turns", n)
		}
		return nil
	}
}

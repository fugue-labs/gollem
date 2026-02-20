package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fugue-labs/gollem/core"
)

// agentToolParams is the input schema for an agent-as-tool.
type agentToolParams struct {
	Prompt string `json:"prompt" jsonschema:"description=The prompt to send to the inner agent"`
}

// AgentTool wraps an agent as a tool that can be called by another agent.
// The inner agent receives the tool call arguments as its prompt and returns
// its output as the tool result. Usage is aggregated to the outer agent.
func AgentTool[T any](name, description string, agent *core.Agent[T]) core.Tool {
	return core.FuncTool[agentToolParams](
		name,
		description,
		func(ctx context.Context, rc *core.RunContext, params agentToolParams) (any, error) {
			result, err := agent.Run(ctx, params.Prompt)
			if err != nil {
				return nil, fmt.Errorf("inner agent %q failed: %w", name, err)
			}

			// Aggregate inner agent usage to the outer agent.
			// We serialize the output to return as tool result.
			output, marshalErr := json.Marshal(result.Output)
			if marshalErr != nil {
				// If we can't serialize as JSON, return the raw output.
				return result.Output, nil //nolint:nilerr // graceful fallback
			}
			return json.RawMessage(output), nil
		},
	)
}

// handoffStep represents a single step in a handoff pipeline.
type handoffStep[T any] struct {
	name     string
	agent    *core.Agent[T]
	promptFn func(prevOutput T) string
	filter   HandoffFilter // optional context filter
}

// Handoff runs agents in sequence, passing each agent's output to the next.
// The message history accumulates across agents.
type Handoff[T any] struct {
	steps []handoffStep[T]
}

// NewHandoff creates a multi-agent handoff pipeline.
func NewHandoff[T any]() *Handoff[T] {
	return &Handoff[T]{}
}

// AddStep adds an agent step to the pipeline.
// The promptFn generates the prompt for this agent from the previous agent's output.
func (h *Handoff[T]) AddStep(name string, agent *core.Agent[T], promptFn func(prevOutput T) string) *Handoff[T] {
	h.steps = append(h.steps, handoffStep[T]{
		name:     name,
		agent:    agent,
		promptFn: promptFn,
	})
	return h
}

// AddStepWithFilter adds a step with a context filter applied before the agent sees messages.
func (h *Handoff[T]) AddStepWithFilter(name string, agent *core.Agent[T], promptFn func(prevOutput T) string, filter HandoffFilter) *Handoff[T] {
	h.steps = append(h.steps, handoffStep[T]{
		name:     name,
		agent:    agent,
		promptFn: promptFn,
		filter:   filter,
	})
	return h
}

// Run executes the pipeline with the initial prompt.
func (h *Handoff[T]) Run(ctx context.Context, initialPrompt string) (*core.RunResult[T], error) {
	if len(h.steps) == 0 {
		return nil, errors.New("handoff pipeline has no steps")
	}

	var totalUsage core.RunUsage
	var lastResult *core.RunResult[T]

	for i, step := range h.steps {
		var prompt string
		if i == 0 {
			prompt = initialPrompt
		} else {
			prompt = step.promptFn(lastResult.Output)
		}

		var opts []core.RunOption
		if lastResult != nil {
			msgs := lastResult.Messages
			// Apply handoff filter if present.
			if step.filter != nil {
				var filterErr error
				msgs, filterErr = step.filter(ctx, msgs)
				if filterErr != nil {
					return nil, fmt.Errorf("handoff step %q filter: %w", step.name, filterErr)
				}
			}
			opts = append(opts, core.WithMessages(msgs...))
		}

		result, err := step.agent.Run(ctx, prompt, opts...)
		if err != nil {
			return nil, fmt.Errorf("handoff step %q failed: %w", step.name, err)
		}

		totalUsage.IncrRun(result.Usage)
		lastResult = result
	}

	lastResult.Usage = totalUsage
	return lastResult, nil
}

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// AgentRun represents an in-progress agent execution that can be iterated step-by-step.
type AgentRun[T any] struct {
	agent    *Agent[T]
	ctx      context.Context
	state    *agentRunState
	settings *ModelSettings
	limits   UsageLimits
	deps     any
	prompt   string
	params   *ModelRequestParameters
	toolMap  map[string]*Tool
	outNames map[string]bool
	done     bool
	result   *RunResult[T]
	err      error
}

// Iter starts an agent run that can be iterated step-by-step.
func (a *Agent[T]) Iter(ctx context.Context, prompt string, opts ...RunOption) *AgentRun[T] {
	cfg := &runConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	if a.outputSchema == nil {
		a.outputSchema = buildOutputSchema[T](a.outputOpts...)
	}

	state := &agentRunState{
		toolRetries: make(map[string]int),
		runID:       newRunID(),
	}
	if len(cfg.messages) > 0 {
		state.messages = make([]ModelMessage, len(cfg.messages))
		copy(state.messages, cfg.messages)
	}

	settings := a.modelSettings
	if cfg.modelSettings != nil {
		settings = cfg.modelSettings
	}
	limits := a.usageLimits
	if cfg.usageLimits != nil {
		limits = *cfg.usageLimits
	}

	// Gather all tools.
	allTools := a.allTools()

	// Build the initial request.
	req := a.buildInitialRequest(prompt)
	state.messages = append(state.messages, req)

	params := buildModelRequestParams(allTools, a.outputSchema)

	toolMap := make(map[string]*Tool)
	for i := range allTools {
		toolMap[allTools[i].Definition.Name] = &allTools[i]
	}
	outNames := make(map[string]bool)
	for _, ot := range a.outputSchema.OutputTools {
		outNames[ot.Name] = true
	}

	return &AgentRun[T]{
		agent:    a,
		ctx:      ctx,
		state:    state,
		settings: settings,
		limits:   limits,
		deps:     cfg.deps,
		prompt:   prompt,
		params:   params,
		toolMap:  toolMap,
		outNames: outNames,
	}
}

// Next executes one iteration of the agent loop (one model call + tool execution).
// Returns the ModelResponse for that step, or (nil, io.EOF) when done.
func (ar *AgentRun[T]) Next() (*ModelResponse, error) {
	if ar.done {
		return nil, io.EOF
	}

	if err := ar.ctx.Err(); err != nil {
		ar.done = true
		ar.err = err
		return nil, err
	}

	if err := ar.limits.CheckBeforeRequest(ar.state.usage); err != nil {
		ar.done = true
		ar.err = err
		return nil, err
	}

	ar.state.runStep++

	resp, err := ar.agent.model.Request(ar.ctx, ar.state.messages, ar.settings, ar.params)
	if err != nil {
		ar.done = true
		ar.err = fmt.Errorf("model request failed: %w", err)
		return nil, ar.err
	}

	ar.state.usage.IncrRequest(resp.Usage)
	ar.state.messages = append(ar.state.messages, *resp)

	if ar.limits.HasTokenLimits() {
		if err := ar.limits.CheckTokens(ar.state.usage); err != nil {
			ar.done = true
			ar.err = err
			return resp, err
		}
	}

	result, nextParts, deferredReqs, err := ar.agent.processResponse(ar.ctx, ar.state, resp, ar.toolMap, ar.outNames, ar.deps, ar.prompt)
	if err != nil {
		ar.done = true
		ar.err = err
		return resp, err
	}

	// If there are deferred tool calls, signal via ErrDeferred.
	if len(deferredReqs) > 0 {
		ar.done = true
		ar.err = &ErrDeferred[T]{
			Result: RunResultDeferred[T]{
				DeferredRequests: deferredReqs,
				Messages:         ar.state.messages,
				Usage:            ar.state.usage,
			},
		}
		return resp, ar.err
	}

	if result != nil {
		ar.done = true
		ar.result = &RunResult[T]{
			Output:   result.output,
			Messages: ar.state.messages,
			Usage:    ar.state.usage,
			RunID:    ar.state.runID,
		}
		return resp, nil
	}

	if len(nextParts) > 0 {
		nextReq := ModelRequest{
			Parts:     nextParts,
			Timestamp: time.Now(),
		}
		ar.state.messages = append(ar.state.messages, nextReq)
	}

	return resp, nil
}

// Result returns the final result after iteration completes. Returns error if not done.
func (ar *AgentRun[T]) Result() (*RunResult[T], error) {
	if ar.err != nil {
		return nil, ar.err
	}
	if !ar.done {
		return nil, errors.New("agent run not yet complete")
	}
	if ar.result == nil {
		return nil, errors.New("agent run completed without a result")
	}
	return ar.result, nil
}

// Messages returns the current message history.
func (ar *AgentRun[T]) Messages() []ModelMessage {
	return ar.state.messages
}

// Done returns true if the agent run has completed.
func (ar *AgentRun[T]) Done() bool {
	return ar.done
}

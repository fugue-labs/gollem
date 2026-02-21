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
	allTools []Tool
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

	outputSchema := a.ensureOutputSchema()

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
	state.limits = limits

	// Resolve deps: run-level deps override agent-level deps.
	deps := a.deps
	if cfg.deps != nil {
		deps = cfg.deps
	}

	// Gather all tools.
	allTools := a.allTools()

	// Build the initial request.
	req := a.buildInitialRequest(prompt)
	state.messages = append(state.messages, req)

	toolMap := make(map[string]*Tool)
	for i := range allTools {
		toolMap[allTools[i].Definition.Name] = &allTools[i]
	}
	outNames := make(map[string]bool)
	for _, ot := range outputSchema.OutputTools {
		outNames[ot.Name] = true
	}

	return &AgentRun[T]{
		agent:    a,
		ctx:      ctx,
		state:    state,
		settings: settings,
		limits:   limits,
		deps:     deps,
		prompt:   prompt,
		allTools: allTools,
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

	for {
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
		if ar.limits.ToolCallsLimit != nil {
			if err := ar.limits.CheckToolCalls(ar.state.usage); err != nil {
				ar.done = true
				ar.err = err
				return nil, err
			}
		}
		if err := checkQuota(ar.agent.usageQuota, ar.state.usage); err != nil {
			ar.done = true
			ar.err = err
			return nil, err
		}

		ar.state.runStep++

		preparedTools := ar.agent.prepareTools(ar.ctx, ar.state, ar.allTools, ar.deps, ar.prompt)
		params := buildModelRequestParams(preparedTools, ar.agent.outputSchema)

		settings := ar.settings
		if ar.agent.toolChoice != nil {
			if settings == nil {
				settings = &ModelSettings{}
			}
			if settings.ToolChoice == nil {
				settings.ToolChoice = ar.agent.toolChoice
			}
		}

		messages := ar.state.messages
		for _, proc := range ar.agent.historyProcessors {
			processed, procErr := proc(ar.ctx, messages)
			if procErr != nil {
				ar.done = true
				ar.err = fmt.Errorf("history processor failed: %w", procErr)
				return nil, ar.err
			}
			messages = processed
		}

		if ar.agent.autoContext != nil {
			compressed, compErr := autoCompressMessages(ar.ctx, messages, ar.agent.autoContext, ar.agent.model)
			if compErr == nil {
				messages = compressed
			}
		}

		if len(ar.agent.messageInterceptors) > 0 {
			var dropped bool
			messages, dropped = runMessageInterceptors(ar.ctx, ar.agent.messageInterceptors, messages)
			if dropped {
				ar.done = true
				ar.err = errors.New("message interceptor dropped the request")
				return nil, ar.err
			}
		}

		turnRC := &RunContext{
			Deps:     ar.deps,
			Usage:    ar.state.usage,
			Prompt:   ar.prompt,
			Messages: messages,
			RunStep:  ar.state.runStep,
			RunID:    ar.state.runID,
			EventBus: ar.agent.eventBus,
		}
		for _, g := range ar.agent.turnGuardrails {
			if gErr := g.fn(ar.ctx, turnRC, messages); gErr != nil {
				guardrailErr := &GuardrailError{
					GuardrailName: g.name,
					Message:       gErr.Error(),
				}
				ar.done = true
				ar.err = guardrailErr
				return nil, guardrailErr
			}
		}

		modelRC := &RunContext{
			Deps:     ar.deps,
			Usage:    ar.state.usage,
			Prompt:   ar.prompt,
			Messages: messages,
			RunStep:  ar.state.runStep,
			RunID:    ar.state.runID,
			EventBus: ar.agent.eventBus,
		}
		ar.agent.fireHook(func(h Hook) {
			if h.OnModelRequest != nil {
				h.OnModelRequest(ar.ctx, modelRC, messages)
			}
		})

		modelReqStart := time.Now()
		if ar.agent.tracingEnabled {
			ar.state.traceSteps = append(ar.state.traceSteps, TraceStep{
				Kind:      TraceModelRequest,
				Timestamp: modelReqStart,
				Data:      map[string]any{"message_count": len(messages)},
			})
		}

		var (
			resp *ModelResponse
			err  error
		)
		if len(ar.agent.middleware) > 0 {
			chain := buildMiddlewareChain(ar.agent.middleware, ar.agent.model)
			resp, err = chain(ar.ctx, messages, settings, params)
		} else {
			resp, err = ar.agent.model.Request(ar.ctx, messages, settings, params)
		}
		if err != nil {
			ar.done = true
			ar.err = fmt.Errorf("model request failed: %w", err)
			return nil, ar.err
		}

		ar.state.usage.IncrRequest(resp.Usage)
		if ar.agent.costTracker != nil {
			singleUsage := RunUsage{}
			singleUsage.Incr(resp.Usage)
			ar.agent.costTracker.Record(ar.agent.model.ModelName(), singleUsage)
		}

		if len(ar.agent.responseInterceptors) > 0 {
			if runResponseInterceptors(ar.ctx, ar.agent.responseInterceptors, resp) {
				continue
			}
		}

		if ar.agent.toolChoiceAutoReset && len(resp.ToolCalls()) > 0 && settings != nil && settings.ToolChoice != nil {
			s := *settings
			s.ToolChoice = ToolChoiceAuto()
			settings = &s
		}
		ar.settings = settings

		ar.state.messages = append(ar.state.messages, *resp)
		ar.agent.fireHook(func(h Hook) {
			if h.OnModelResponse != nil {
				h.OnModelResponse(ar.ctx, modelRC, resp)
			}
		})

		if ar.agent.tracingEnabled {
			ar.state.traceSteps = append(ar.state.traceSteps, TraceStep{
				Kind:      TraceModelResponse,
				Timestamp: time.Now(),
				Duration:  time.Since(modelReqStart),
				Data:      map[string]any{"text": resp.TextContent(), "tool_calls": len(resp.ToolCalls())},
			})
		}

		if ar.limits.HasTokenLimits() {
			if err := ar.limits.CheckTokens(ar.state.usage); err != nil {
				ar.done = true
				ar.err = err
				return resp, err
			}
		}

		if len(ar.agent.runConditions) > 0 {
			condRC := &RunContext{
				Deps:     ar.deps,
				Usage:    ar.state.usage,
				Prompt:   ar.prompt,
				Messages: ar.state.messages,
				RunStep:  ar.state.runStep,
				RunID:    ar.state.runID,
			}
			for _, cond := range ar.agent.runConditions {
				if stop, reason := cond(ar.ctx, condRC, resp); stop {
					if hasText := resp.TextContent() != ""; hasText && ar.agent.outputSchema.AllowsText {
						text := resp.TextContent()
						output, parseErr := deserializeOutput[T](text, ar.agent.outputSchema.OuterTypedDictKey)
						if parseErr != nil && ar.agent.outputSchema.Mode == OutputModeText {
							output = any(text).(T)
							parseErr = nil
						}
						if parseErr == nil {
							ar.done = true
							ar.result = &RunResult[T]{
								Output:   output,
								Messages: ar.state.messages,
								Usage:    ar.state.usage,
								RunID:    ar.state.runID,
							}
							return resp, nil
						}
					}
					runCondErr := &RunConditionError{Reason: reason}
					ar.done = true
					ar.err = runCondErr
					return resp, runCondErr
				}
			}
		}

		result, nextParts, deferredReqs, err := ar.agent.processResponse(ar.ctx, ar.state, resp, ar.toolMap, ar.outNames, ar.deps, ar.prompt)
		if err != nil {
			ar.done = true
			ar.err = err
			return resp, err
		}

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
			if ar.agent.kbAutoStore && ar.agent.knowledgeBase != nil {
				responseText := resp.TextContent()
				if responseText != "" {
					if storeErr := ar.agent.knowledgeBase.Store(ar.ctx, responseText); storeErr != nil {
						ar.done = true
						ar.err = fmt.Errorf("knowledge base store failed: %w", storeErr)
						return resp, ar.err
					}
				}
			}
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

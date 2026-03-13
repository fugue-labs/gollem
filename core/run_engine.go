package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type runExecution[T any] struct {
	state    *RunState
	settings *ModelSettings
	limits   UsageLimits
	deps     any
}

func (a *Agent[T]) initializeRunExecution(cfg *runConfig) *runExecution[T] {
	limits := a.usageLimits
	if cfg.usageLimits != nil {
		limits = *cfg.usageLimits
	}

	state := newRunState(cfg.detach, limits)
	if cfg.snapshot != nil {
		state.applySnapshot(cfg.snapshot)
	}
	state.detach = cfg.detach
	state.limits = limits

	if len(cfg.messages) > 0 {
		state.messages = cloneMessages(cfg.messages)
	}

	restoreToolState := map[string]any(nil)
	if cfg.snapshot != nil {
		restoreToolState = cloneAnyMap(cfg.snapshot.ToolState)
	}
	if len(cfg.toolState) > 0 {
		restoreToolState = cloneAnyMap(cfg.toolState)
	}
	if len(restoreToolState) > 0 {
		a.restoreToolState(restoreToolState)
	}

	settings := a.modelSettings
	if cfg.modelSettings != nil {
		settings = cfg.modelSettings
	}

	deps := a.deps
	if cfg.deps != nil {
		deps = cfg.deps
	}

	return &runExecution[T]{
		state:    state,
		settings: settings,
		limits:   limits,
		deps:     deps,
	}
}

func (a *Agent[T]) applyInputGuardrails(ctx context.Context, state *RunState, deps any, prompt string, emitHooks bool) (string, error) {
	for _, g := range a.inputGuardrails {
		var gErr error
		prompt, gErr = g.fn(ctx, prompt)
		if emitHooks {
			passed := gErr == nil
			guardRC := a.buildRunContext(state, deps, prompt)
			a.fireHook(func(h Hook) {
				if h.OnGuardrailEvaluated != nil {
					h.OnGuardrailEvaluated(ctx, guardRC, g.name, passed, gErr)
				}
			})
		}
		if gErr != nil {
			return prompt, &GuardrailError{
				GuardrailName: g.name,
				Message:       gErr.Error(),
			}
		}
	}
	return prompt, nil
}

func (a *Agent[T]) bootstrapRunMessages(ctx context.Context, state *RunState, prompt string, deps any, deferredResults []DeferredToolResult, initialRequestParts []ModelRequestPart) error {
	if len(deferredResults) > 0 && len(state.messages) > 0 {
		injectDeferredResults(state, deferredResults)
		return nil
	}

	req, err := a.buildInitialRequestWithDynamic(ctx, prompt, state, deps, initialRequestParts)
	if err != nil {
		return fmt.Errorf("failed to build initial request: %w", err)
	}
	state.messages = append(state.messages, req)
	return nil
}

func injectDeferredResults(state *RunState, deferredResults []DeferredToolResult) {
	var deferredParts []ModelRequestPart
	for _, dr := range deferredResults {
		if dr.IsError {
			deferredParts = append(deferredParts, RetryPromptPart{
				Content:    dr.Content,
				ToolName:   dr.ToolName,
				ToolCallID: dr.ToolCallID,
				Timestamp:  time.Now(),
			})
		} else {
			deferredParts = append(deferredParts, ToolReturnPart{
				ToolName:   dr.ToolName,
				Content:    dr.Content,
				ToolCallID: dr.ToolCallID,
				Timestamp:  time.Now(),
			})
		}
	}

	merged := false
	if lastIdx := len(state.messages) - 1; lastIdx >= 0 {
		if lastReq, ok := state.messages[lastIdx].(ModelRequest); ok {
			lastReq.Parts = append(lastReq.Parts, deferredParts...)
			state.messages[lastIdx] = lastReq
			merged = true
		}
	}
	if !merged {
		state.messages = append(state.messages, ModelRequest{
			Parts:     deferredParts,
			Timestamp: time.Now(),
		})
	}
}

func (a *Agent[T]) buildRunResult(state *RunState, output T) *RunResult[T] {
	return &RunResult[T]{
		Output:    output,
		Messages:  state.messages,
		Usage:     state.usage,
		RunID:     state.runID,
		ToolState: a.exportToolState(),
	}
}

type turnEngine[T any] struct {
	agent           *Agent[T]
	ctx             context.Context
	state           *RunState
	prompt          string
	settings        *ModelSettings
	limits          UsageLimits
	deps            any
	allTools        []Tool
	toolMap         map[string]*Tool
	outputToolNames map[string]bool
}

func (a *Agent[T]) newTurnEngine(ctx context.Context, state *RunState, prompt string, settings *ModelSettings, limits UsageLimits, deps any) *turnEngine[T] {
	allTools := a.allTools()
	toolMap := make(map[string]*Tool, len(allTools))
	for i := range allTools {
		toolMap[allTools[i].Definition.Name] = &allTools[i]
	}
	outputToolNames := make(map[string]bool, len(a.outputSchema.OutputTools))
	for _, ot := range a.outputSchema.OutputTools {
		outputToolNames[ot.Name] = true
	}
	return &turnEngine[T]{
		agent:           a,
		ctx:             ctx,
		state:           state,
		prompt:          prompt,
		settings:        settings,
		limits:          limits,
		deps:            deps,
		allTools:        allTools,
		toolMap:         toolMap,
		outputToolNames: outputToolNames,
	}
}

// Step executes one non-streaming turn: one model response and any resulting
// tool execution, including retries and deferred tool handling.
func (e *turnEngine[T]) Step() (*ModelResponse, *RunResult[T], error) {
	for {
		if err := e.ctx.Err(); err != nil {
			return nil, nil, err
		}
		if err := e.limits.CheckBeforeRequest(e.state.usage); err != nil {
			return nil, nil, err
		}
		if e.limits.ToolCallsLimit != nil {
			if err := e.limits.CheckToolCalls(e.state.usage); err != nil {
				return nil, nil, err
			}
		}
		if err := checkQuota(e.agent.usageQuota, e.state.usage); err != nil {
			return nil, nil, err
		}

		e.state.runStep++

		turnRC := e.agent.buildRunContext(e.state, e.deps, e.prompt)
		e.agent.fireHook(func(h Hook) {
			if h.OnTurnStart != nil {
				h.OnTurnStart(e.ctx, turnRC, e.state.runStep)
			}
		})

		preparedTools := e.agent.prepareTools(e.ctx, e.state, e.allTools, e.deps, e.prompt)
		params := buildModelRequestParams(preparedTools, e.agent.outputSchema)

		settings := e.settings
		if e.agent.toolChoice != nil {
			if settings == nil {
				settings = &ModelSettings{}
			}
			if settings.ToolChoice == nil {
				settings.ToolChoice = e.agent.toolChoice
			}
		}

		if e.agent.autoContext != nil {
			beforeCount := len(e.state.messages)
			beforeTokens := currentContextTokenCount(e.state.messages, e.state.lastInputTokens)
			compressed, compErr := autoCompressMessages(e.ctx, e.state.messages, e.agent.autoContext, e.agent.model, beforeTokens)
			if compErr == nil && len(compressed) < beforeCount {
				e.state.messages = compressed
				afterTokens := estimateTokens(compressed)
				e.agent.fireHook(func(h Hook) {
					if h.OnContextCompaction != nil {
						h.OnContextCompaction(e.ctx, turnRC, ContextCompactionStats{
							Strategy:              CompactionStrategyAutoSummary,
							MessagesBefore:        beforeCount,
							MessagesAfter:         len(compressed),
							EstimatedTokensBefore: beforeTokens,
							EstimatedTokensAfter:  afterTokens,
						})
					}
				})
			}
		}

		messages := e.state.messages
		for _, proc := range e.agent.historyProcessors {
			beforeCount := len(messages)
			beforeTokens := estimateTokens(messages)
			processed, procErr := proc(e.ctx, messages)
			if procErr != nil {
				return nil, nil, fmt.Errorf("history processor failed: %w", procErr)
			}
			afterCount := len(processed)
			afterTokens := estimateTokens(processed)
			tokenDelta := beforeTokens - afterTokens
			meaningfulChange := afterCount < beforeCount ||
				(beforeTokens > 0 && tokenDelta > 0 && float64(tokenDelta)/float64(beforeTokens) > 0.05)
			if meaningfulChange {
				e.agent.fireHook(func(h Hook) {
					if h.OnContextCompaction != nil {
						h.OnContextCompaction(e.ctx, turnRC, ContextCompactionStats{
							Strategy:              CompactionStrategyHistoryProcessor,
							MessagesBefore:        beforeCount,
							MessagesAfter:         len(processed),
							EstimatedTokensBefore: beforeTokens,
							EstimatedTokensAfter:  afterTokens,
						})
					}
				})
			}
			messages = processed
		}

		if len(e.agent.messageInterceptors) > 0 {
			var dropped bool
			messages, dropped = runMessageInterceptors(e.ctx, e.agent.messageInterceptors, messages)
			if dropped {
				return nil, nil, errors.New("message interceptor dropped the request")
			}
		}

		turnGuardRC := e.agent.buildRunContext(e.state, e.deps, e.prompt)
		turnGuardRC.Messages = messages
		for _, g := range e.agent.turnGuardrails {
			gErr := g.fn(e.ctx, turnGuardRC, messages)
			passed := gErr == nil
			e.agent.fireHook(func(h Hook) {
				if h.OnGuardrailEvaluated != nil {
					h.OnGuardrailEvaluated(e.ctx, turnGuardRC, g.name, passed, gErr)
				}
			})
			if gErr != nil {
				return nil, nil, &GuardrailError{
					GuardrailName: g.name,
					Message:       gErr.Error(),
				}
			}
		}

		modelRC := e.agent.buildRunContext(e.state, e.deps, e.prompt)
		modelRC.Messages = messages
		e.agent.fireHook(func(h Hook) {
			if h.OnModelRequest != nil {
				h.OnModelRequest(e.ctx, modelRC, messages)
			}
		})

		modelReqStart := time.Now()
		if e.agent.tracingEnabled {
			e.state.traceSteps = append(e.state.traceSteps, TraceStep{
				Kind:      TraceModelRequest,
				Timestamp: modelReqStart,
				Data:      map[string]any{"message_count": len(messages)},
			})
		}

		var (
			resp *ModelResponse
			err  error
		)
		if len(e.agent.middleware) > 0 {
			mwCtx := ContextWithCompactionCallback(e.ctx, func(stats ContextCompactionStats) {
				e.agent.fireHook(func(h Hook) {
					if h.OnContextCompaction != nil {
						h.OnContextCompaction(e.ctx, turnRC, stats)
					}
				})
			})
			chain := buildMiddlewareChain(e.agent.middleware, e.agent.model)
			resp, err = chain(mwCtx, messages, settings, params)
		} else {
			resp, err = e.agent.model.Request(e.ctx, messages, settings, params)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("model request failed: %w", err)
		}

		e.state.usage.IncrRequest(resp.Usage)
		if resp.Usage.InputTokens > 0 {
			e.state.lastInputTokens = resp.Usage.InputTokens
		}

		if e.agent.costTracker != nil {
			singleUsage := RunUsage{}
			singleUsage.Incr(resp.Usage)
			e.agent.costTracker.Record(e.agent.model.ModelName(), singleUsage)
		}

		if len(e.agent.responseInterceptors) > 0 {
			if runResponseInterceptors(e.ctx, e.agent.responseInterceptors, resp) {
				continue
			}
		}

		if e.agent.toolChoiceAutoReset && len(resp.ToolCalls()) > 0 && settings != nil && settings.ToolChoice != nil {
			s := *settings
			s.ToolChoice = ToolChoiceAuto()
			settings = &s
		}
		e.settings = settings

		e.state.messages = append(e.state.messages, *resp)
		e.agent.fireHook(func(h Hook) {
			if h.OnModelResponse != nil {
				h.OnModelResponse(e.ctx, modelRC, resp)
			}
		})

		if e.agent.tracingEnabled {
			e.state.traceSteps = append(e.state.traceSteps, TraceStep{
				Kind:      TraceModelResponse,
				Timestamp: time.Now(),
				Duration:  time.Since(modelReqStart),
				Data:      map[string]any{"text": resp.TextContent(), "tool_calls": len(resp.ToolCalls())},
			})
		}

		if e.limits.HasTokenLimits() {
			if err := e.limits.CheckTokens(e.state.usage); err != nil {
				return resp, nil, err
			}
		}

		if len(e.agent.runConditions) > 0 {
			condRC := e.agent.buildRunContext(e.state, e.deps, e.prompt)
			for _, cond := range e.agent.runConditions {
				if stop, reason := cond(e.ctx, condRC, resp); stop {
					e.agent.fireHook(func(h Hook) {
						if h.OnRunConditionChecked != nil {
							h.OnRunConditionChecked(e.ctx, condRC, true, reason)
						}
					})
					if hasText := resp.TextContent() != ""; hasText && e.agent.outputSchema.AllowsText {
						text := resp.TextContent()
						output, parseErr := deserializeOutput[T](text, e.agent.outputSchema.OuterTypedDictKey)
						if parseErr != nil && e.agent.outputSchema.Mode == OutputModeText {
							if textOutput, ok := any(text).(T); ok {
								output = textOutput
								parseErr = nil
							}
						}
						if parseErr == nil {
							return resp, e.agent.buildRunResult(e.state, output), nil
						}
					}
					return resp, nil, &RunConditionError{Reason: reason}
				}
			}
		}

		result, nextParts, deferredReqs, err := e.agent.processResponse(e.ctx, e.state, resp, e.toolMap, e.outputToolNames, e.deps, e.prompt)
		e.agent.fireHook(func(h Hook) {
			if h.OnTurnEnd != nil {
				h.OnTurnEnd(e.ctx, turnRC, e.state.runStep, resp)
			}
		})
		if err != nil {
			return resp, nil, err
		}
		if len(deferredReqs) > 0 {
			if len(nextParts) > 0 {
				e.state.messages = append(e.state.messages, ModelRequest{
					Parts:     nextParts,
					Timestamp: time.Now(),
				})
			}
			return resp, nil, &ErrDeferred[T]{
				Result: RunResultDeferred[T]{
					DeferredRequests: deferredReqs,
					Messages:         e.state.messages,
					Usage:            e.state.usage,
				},
			}
		}
		if result != nil {
			if e.agent.kbAutoStore && e.agent.knowledgeBase != nil {
				responseText := resp.TextContent()
				if responseText != "" {
					if storeErr := e.agent.knowledgeBase.Store(e.ctx, responseText); storeErr != nil {
						return resp, nil, fmt.Errorf("knowledge base store failed: %w", storeErr)
					}
				}
			}
			return resp, e.agent.buildRunResult(e.state, result.output), nil
		}
		if len(nextParts) > 0 {
			e.state.messages = append(e.state.messages, ModelRequest{
				Parts:     nextParts,
				Timestamp: time.Now(),
			})
		}
		return resp, nil, nil
	}
}

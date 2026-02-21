package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// EndStrategy determines when to stop after finding a final result.
type EndStrategy string

const (
	// EndStrategyEarly stops at the first valid result, skipping remaining tool calls.
	EndStrategyEarly EndStrategy = "early"
	// EndStrategyExhaustive processes all tool calls even after finding a result.
	EndStrategyExhaustive EndStrategy = "exhaustive"
)

// SystemPromptFunc generates a system prompt dynamically using RunContext.
type SystemPromptFunc func(ctx context.Context, runCtx *RunContext) (string, error)

// HistoryProcessor transforms the message history before each model request.
// It receives context for operations that may need I/O (e.g., LLM summarization).
type HistoryProcessor func(ctx context.Context, messages []ModelMessage) ([]ModelMessage, error)

// ToolApprovalFunc is called before executing a tool that requires approval.
// Return true to approve, false to deny (sends denial message back to model).
type ToolApprovalFunc func(ctx context.Context, toolName string, argsJSON string) (bool, error)

// Agent is the central type for running LLM interactions with structured output.
type Agent[T any] struct {
	model                Model
	systemPrompts        []string
	dynamicSystemPrompts []SystemPromptFunc
	historyProcessors    []HistoryProcessor
	tools                []Tool
	toolsets             []*Toolset
	outputSchemaMu       sync.Mutex
	outputSchema         *OutputSchema
	outputValidators     []OutputValidatorFunc[T]
	outputOpts           []OutputOption
	maxRetries           int
	endStrategy          EndStrategy
	usageLimits          UsageLimits
	modelSettings        *ModelSettings
	toolApprovalFunc     ToolApprovalFunc
	maxConcurrency       int
	knowledgeBase        KnowledgeBase
	kbAutoStore          bool
	toolsPrepareFunc     AgentToolsPrepareFunc
	hooks                []Hook
	inputGuardrails      []namedInputGuardrail
	turnGuardrails       []namedTurnGuardrail
	repairFunc           any // RepairFunc[T], stored as any to avoid generic field
	tracingEnabled             bool
	globalToolResultValidators []ToolResultValidatorFunc
	defaultToolTimeout         time.Duration
	runConditions              []RunCondition
	traceExporters             []TraceExporter
	eventBus                   *EventBus
	deps                       any
	usageQuota                 *UsageQuota
	messageInterceptors        []MessageInterceptor
	responseInterceptors       []ResponseInterceptor
	costTracker                *CostTracker
	autoContext                *AutoContextConfig
	middleware                 []AgentMiddleware
	toolChoice                 *ToolChoice
	toolChoiceAutoReset        bool
}

// RunResult is the outcome of a successful agent run.
type RunResult[T any] struct {
	// Output is the parsed/validated output of type T.
	Output T
	// Messages is the full conversation history.
	Messages []ModelMessage
	// Usage is the aggregate token usage for this run.
	Usage RunUsage
	// RunID is the unique identifier for this run.
	RunID string
	// Trace is the execution trace when WithTracing is enabled. Nil otherwise.
	Trace *RunTrace
	// Cost is the estimated cost when a CostTracker is configured. Nil otherwise.
	Cost *RunCost
}

// AgentOption configures the agent via functional options.
type AgentOption[T any] func(*Agent[T])

// WithSystemPrompt adds a system prompt to the agent.
func WithSystemPrompt[T any](prompt string) AgentOption[T] {
	return func(a *Agent[T]) {
		a.systemPrompts = append(a.systemPrompts, prompt)
	}
}

// WithTools adds tools to the agent.
func WithTools[T any](tools ...Tool) AgentOption[T] {
	return func(a *Agent[T]) {
		a.tools = append(a.tools, tools...)
	}
}

// WithMaxRetries sets the maximum number of result validation retries.
func WithMaxRetries[T any](n int) AgentOption[T] {
	return func(a *Agent[T]) {
		a.maxRetries = n
	}
}

// WithEndStrategy sets the end strategy for the agent.
func WithEndStrategy[T any](s EndStrategy) AgentOption[T] {
	return func(a *Agent[T]) {
		a.endStrategy = s
	}
}

// WithUsageLimits sets the usage limits for the agent.
func WithUsageLimits[T any](l UsageLimits) AgentOption[T] {
	return func(a *Agent[T]) {
		a.usageLimits = l
	}
}

// WithModelSettings sets the model settings for the agent.
func WithModelSettings[T any](s ModelSettings) AgentOption[T] {
	return func(a *Agent[T]) {
		a.modelSettings = &s
	}
}

// WithTemperature sets the temperature for the agent.
func WithTemperature[T any](t float64) AgentOption[T] {
	return func(a *Agent[T]) {
		if a.modelSettings == nil {
			a.modelSettings = &ModelSettings{}
		}
		a.modelSettings.Temperature = &t
	}
}

// WithMaxTokens sets the max tokens for the agent.
func WithMaxTokens[T any](n int) AgentOption[T] {
	return func(a *Agent[T]) {
		if a.modelSettings == nil {
			a.modelSettings = &ModelSettings{}
		}
		a.modelSettings.MaxTokens = &n
	}
}

// WithThinkingBudget enables extended thinking with the given token budget.
// Supported by Anthropic (direct and Vertex AI). When thinking is enabled,
// temperature is automatically stripped (Anthropic requirement).
func WithThinkingBudget[T any](budget int) AgentOption[T] {
	return func(a *Agent[T]) {
		if a.modelSettings == nil {
			a.modelSettings = &ModelSettings{}
		}
		a.modelSettings.ThinkingBudget = &budget
	}
}

// WithReasoningEffort sets the reasoning effort level for OpenAI o-series models.
// Valid values: "low", "medium", "high".
func WithReasoningEffort[T any](effort string) AgentOption[T] {
	return func(a *Agent[T]) {
		if a.modelSettings == nil {
			a.modelSettings = &ModelSettings{}
		}
		a.modelSettings.ReasoningEffort = &effort
	}
}

// WithOutputValidator adds an output validator to the agent.
func WithOutputValidator[T any](fn OutputValidatorFunc[T]) AgentOption[T] {
	return func(a *Agent[T]) {
		a.outputValidators = append(a.outputValidators, fn)
	}
}

// WithOutputOptions sets output configuration options.
func WithOutputOptions[T any](opts ...OutputOption) AgentOption[T] {
	return func(a *Agent[T]) {
		a.outputOpts = append(a.outputOpts, opts...)
	}
}

// WithDynamicSystemPrompt adds a function that generates system prompts at runtime.
func WithDynamicSystemPrompt[T any](fn SystemPromptFunc) AgentOption[T] {
	return func(a *Agent[T]) {
		a.dynamicSystemPrompts = append(a.dynamicSystemPrompts, fn)
	}
}

// WithHistoryProcessor adds a processor that transforms message history before each model request.
func WithHistoryProcessor[T any](proc HistoryProcessor) AgentOption[T] {
	return func(a *Agent[T]) {
		a.historyProcessors = append(a.historyProcessors, proc)
	}
}

// WithToolApproval sets the approval function for tools marked as requiring approval.
func WithToolApproval[T any](fn ToolApprovalFunc) AgentOption[T] {
	return func(a *Agent[T]) {
		a.toolApprovalFunc = fn
	}
}

// WithMaxConcurrency limits the number of tools that can execute concurrently.
func WithMaxConcurrency[T any](n int) AgentOption[T] {
	return func(a *Agent[T]) {
		a.maxConcurrency = n
	}
}

// WithToolsets adds one or more toolsets to the agent.
func WithToolsets[T any](toolsets ...*Toolset) AgentOption[T] {
	return func(a *Agent[T]) {
		a.toolsets = append(a.toolsets, toolsets...)
	}
}

// WithToolsPrepare sets an agent-wide function that filters/modifies all tool
// definitions before each model request.
func WithToolsPrepare[T any](fn AgentToolsPrepareFunc) AgentOption[T] {
	return func(a *Agent[T]) {
		a.toolsPrepareFunc = fn
	}
}

// WithGlobalToolResultValidator adds a validator that runs on all tool results.
func WithGlobalToolResultValidator[T any](fn ToolResultValidatorFunc) AgentOption[T] {
	return func(a *Agent[T]) {
		a.globalToolResultValidators = append(a.globalToolResultValidators, fn)
	}
}

// WithDefaultToolTimeout sets a default timeout for all tools that don't have
// their own timeout configured.
func WithDefaultToolTimeout[T any](d time.Duration) AgentOption[T] {
	return func(a *Agent[T]) {
		a.defaultToolTimeout = d
	}
}

// NewAgent creates a new Agent with the given model and options.
func NewAgent[T any](model Model, opts ...AgentOption[T]) *Agent[T] {
	a := &Agent[T]{
		model:       model,
		maxRetries:  1,
		endStrategy: EndStrategyEarly,
		usageLimits: DefaultUsageLimits(),
	}
	for _, opt := range opts {
		opt(a)
	}
	// Build output schema lazily on first run if not already set.
	return a
}

// GetModel returns the agent's model.
func (a *Agent[T]) GetModel() Model {
	return a.model
}

// GetTools returns the agent's direct tools (not including toolset tools).
func (a *Agent[T]) GetTools() []Tool {
	return a.tools
}

// RunOption configures a specific run invocation.
type RunOption func(*runConfig)

type runConfig struct {
	deps             any
	modelSettings    *ModelSettings
	usageLimits      *UsageLimits
	messages         []ModelMessage
	deferredResults  []DeferredToolResult
	batchConcurrency int
}

// WithRunDeps sets dependencies available to tools via RunContext.
func WithRunDeps(deps any) RunOption {
	return func(c *runConfig) {
		c.deps = deps
	}
}

// WithRunModelSettings overrides model settings for this run.
func WithRunModelSettings(s ModelSettings) RunOption {
	return func(c *runConfig) {
		c.modelSettings = &s
	}
}

// WithRunUsageLimits overrides usage limits for this run.
func WithRunUsageLimits(l UsageLimits) RunOption {
	return func(c *runConfig) {
		c.usageLimits = &l
	}
}

// WithMessages sets initial conversation history for the run.
// This is used to resume from checkpoints or continue conversations.
func WithMessages(msgs ...ModelMessage) RunOption {
	return func(c *runConfig) {
		c.messages = msgs
	}
}

// agentRunState tracks mutable state across the agent loop.
type agentRunState struct {
	messages    []ModelMessage
	usage       RunUsage
	retries     int
	toolRetries map[string]int
	runStep     int
	runID       string
	limits      UsageLimits
	mu          sync.Mutex // protects usage, toolRetries, and traceSteps during concurrent tool execution
	traceSteps  []TraceStep
}

func (a *Agent[T]) ensureOutputSchema() *OutputSchema {
	a.outputSchemaMu.Lock()
	defer a.outputSchemaMu.Unlock()
	if a.outputSchema == nil {
		a.outputSchema = buildOutputSchema[T](a.outputOpts...)
	}
	return a.outputSchema
}

// Run executes the agent loop synchronously and returns the final result.
func (a *Agent[T]) Run(ctx context.Context, prompt string, opts ...RunOption) (*RunResult[T], error) {
	cfg := &runConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Build output schema.
	a.ensureOutputSchema()

	// Initialize state.
	state := &agentRunState{
		toolRetries: make(map[string]int),
		runID:       newRunID(),
	}

	// Copy any provided history.
	if len(cfg.messages) > 0 {
		state.messages = make([]ModelMessage, len(cfg.messages))
		copy(state.messages, cfg.messages)
	}

	// Resolve settings.
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

	// Run input guardrails.
	for _, g := range a.inputGuardrails {
		var gErr error
		prompt, gErr = g.fn(ctx, prompt)
		if gErr != nil {
			return nil, &GuardrailError{
				GuardrailName: g.name,
				Message:       gErr.Error(),
			}
		}
	}

	// Fire OnRunStart hooks.
	rc := &RunContext{
		Deps:     deps,
		Usage:    state.usage,
		Prompt:   prompt,
		RunStep:  state.runStep,
		RunID:    state.runID,
		EventBus: a.eventBus,
	}

	// Publish RunStartedEvent.
	if a.eventBus != nil {
		Publish(a.eventBus, RunStartedEvent{RunID: state.runID, Prompt: prompt})
	}
	a.fireHook(func(h Hook) {
		if h.OnRunStart != nil {
			h.OnRunStart(ctx, rc, prompt)
		}
	})

	// Track start time for tracing.
	startTime := time.Now()

	result, runErr := a.runLoop(ctx, state, prompt, settings, limits, deps, cfg.deferredResults)

	endRC := &RunContext{
		Deps:     deps,
		Usage:    state.usage,
		Prompt:   prompt,
		RunStep:  state.runStep,
		RunID:    state.runID,
		EventBus: a.eventBus,
	}

	// Publish RunCompletedEvent.
	if a.eventBus != nil {
		evt := RunCompletedEvent{RunID: state.runID, Success: runErr == nil}
		if runErr != nil {
			evt.Error = runErr.Error()
		}
		Publish(a.eventBus, evt)
	}
	a.fireHook(func(h Hook) {
		if h.OnRunEnd != nil {
			h.OnRunEnd(ctx, endRC, state.messages, runErr)
		}
	})

	// Build trace if enabled.
	if a.tracingEnabled {
		endTime := time.Now()
		trace := &RunTrace{
			RunID:     state.runID,
			Prompt:    prompt,
			StartTime: startTime,
			EndTime:   endTime,
			Duration:  endTime.Sub(startTime),
			Steps:     state.traceSteps,
			Usage:     state.usage,
			Success:   runErr == nil,
		}
		if runErr != nil {
			trace.Error = runErr.Error()
		}
		if result != nil {
			result.Trace = trace
		}

		// Export trace to all registered exporters.
		for _, exporter := range a.traceExporters {
			// Exporter errors are non-fatal — don't break the run.
			_ = exporter.Export(ctx, trace)
		}
	}

	// Attach cost to result if cost tracker is configured.
	if result != nil && a.costTracker != nil {
		result.Cost = a.costTracker.buildRunCost()
	}

	return result, runErr
}

// RunStream executes the agent with streaming output.
func (a *Agent[T]) RunStream(ctx context.Context, prompt string, opts ...RunOption) (*StreamResult[T], error) {
	cfg := &runConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Build output schema.
	outputSchema := a.ensureOutputSchema()

	// Initialize state.
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

	// Build the initial request.
	req := a.buildInitialRequest(prompt)
	state.messages = append(state.messages, req)

	// Build model request parameters.
	params := buildModelRequestParams(a.tools, outputSchema)

	// Make streaming request.
	stream, err := a.model.RequestStream(ctx, state.messages, settings, params)
	if err != nil {
		return nil, fmt.Errorf("model stream request failed: %w", err)
	}

	return newStreamResult[T](stream, outputSchema, a.outputValidators, state.messages), nil
}

// allTools returns all tools from both direct tools and toolsets.
func (a *Agent[T]) allTools() []Tool {
	all := make([]Tool, len(a.tools))
	copy(all, a.tools)
	for _, ts := range a.toolsets {
		all = append(all, ts.Tools...)
	}
	return all
}

// prepareTools applies per-tool and agent-wide prepare functions to filter/modify
// the tool list before each model request.
func (a *Agent[T]) prepareTools(ctx context.Context, state *agentRunState, tools []Tool, deps any, prompt string) []Tool {
	// If no prepare functions are set, return tools as-is.
	if a.toolsPrepareFunc == nil && !a.hasToolPrepareFuncs(tools) {
		return tools
	}

	rc := &RunContext{
		Deps:     deps,
		Usage:    state.usage,
		Prompt:   prompt,
		Messages: state.messages,
		RunStep:  state.runStep,
		RunID:    state.runID,
	}

	// Apply per-tool prepare functions.
	var prepared []Tool
	for _, t := range tools {
		if t.PrepareFunc != nil {
			def := t.PrepareFunc(ctx, rc, t.Definition)
			if def == nil {
				// Tool excluded.
				continue
			}
			// Use the (possibly modified) definition.
			modified := t
			modified.Definition = *def
			prepared = append(prepared, modified)
		} else {
			prepared = append(prepared, t)
		}
	}

	// Apply agent-wide prepare function.
	if a.toolsPrepareFunc != nil {
		defs := make([]ToolDefinition, len(prepared))
		for i, t := range prepared {
			defs[i] = t.Definition
		}
		filteredDefs := a.toolsPrepareFunc(ctx, rc, defs)

		// Build a set of retained definition names for fast lookup.
		retained := make(map[string]ToolDefinition, len(filteredDefs))
		for _, d := range filteredDefs {
			retained[d.Name] = d
		}

		var result []Tool
		for _, t := range prepared {
			if def, ok := retained[t.Definition.Name]; ok {
				modified := t
				modified.Definition = def
				result = append(result, modified)
			}
		}
		prepared = result
	}

	return prepared
}

// hasToolPrepareFuncs checks if any tool has a PrepareFunc set.
func (a *Agent[T]) hasToolPrepareFuncs(tools []Tool) bool {
	for _, t := range tools {
		if t.PrepareFunc != nil {
			return true
		}
	}
	return false
}

// runLoop is the core agent loop.
func (a *Agent[T]) runLoop(ctx context.Context, state *agentRunState, prompt string, settings *ModelSettings, limits UsageLimits, deps any, deferredResults []DeferredToolResult) (*RunResult[T], error) {
	// When deferred results are provided, inject them BEFORE the new initial request.
	// This ensures tool_result blocks immediately follow the tool_use blocks in the
	// existing message history, which providers like Anthropic require.
	if len(deferredResults) > 0 {
		var deferredParts []ModelRequestPart
		for _, dr := range deferredResults {
			if dr.IsError {
				deferredParts = append(deferredParts, RetryPromptPart{
					Content:    dr.Content,
					ToolCallID: dr.ToolCallID,
					Timestamp:  time.Now(),
				})
			} else {
				deferredParts = append(deferredParts, ToolReturnPart{
					Content:    dr.Content,
					ToolCallID: dr.ToolCallID,
					Timestamp:  time.Now(),
				})
			}
		}
		state.messages = append(state.messages, ModelRequest{
			Parts:     deferredParts,
			Timestamp: time.Now(),
		})
	}

	// Build the initial request with dynamic system prompts.
	req, err := a.buildInitialRequestWithDynamic(ctx, prompt, state, deps)
	if err != nil {
		return nil, fmt.Errorf("failed to build initial request: %w", err)
	}
	state.messages = append(state.messages, req)

	// Gather all tools (direct + toolsets).
	allTools := a.allTools()

	// Build tool lookup map.
	toolMap := make(map[string]*Tool)
	for i := range allTools {
		toolMap[allTools[i].Definition.Name] = &allTools[i]
	}

	// Build output tool name set.
	outputToolNames := make(map[string]bool)
	for _, ot := range a.outputSchema.OutputTools {
		outputToolNames[ot.Name] = true
	}

	for {
		// Check context.
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// Check usage limits before request.
		if err := limits.CheckBeforeRequest(state.usage); err != nil {
			return nil, err
		}

		// Check tool call limits before request.
		if limits.ToolCallsLimit != nil {
			if err := limits.CheckToolCalls(state.usage); err != nil {
				return nil, err
			}
		}

		// Check usage quota before request.
		if err := checkQuota(a.usageQuota, state.usage); err != nil {
			return nil, err
		}

		state.runStep++

		// Apply prepare functions to filter/modify tools for this iteration.
		preparedTools := a.prepareTools(ctx, state, allTools, deps, prompt)

		// Build model request parameters with prepared tools.
		params := buildModelRequestParams(preparedTools, a.outputSchema)

		// Apply tool choice to settings.
		if a.toolChoice != nil {
			if settings == nil {
				settings = &ModelSettings{}
			}
			if settings.ToolChoice == nil {
				settings.ToolChoice = a.toolChoice
			}
		}

		// Apply history processors.
		messages := state.messages
		for _, proc := range a.historyProcessors {
			processed, procErr := proc(ctx, messages)
			if procErr != nil {
				return nil, fmt.Errorf("history processor failed: %w", procErr)
			}
			messages = processed
		}

		// Apply auto context compression.
		if a.autoContext != nil {
			compressed, compErr := autoCompressMessages(ctx, messages, a.autoContext, a.model)
			if compErr == nil {
				messages = compressed
			}
		}

		// Run message interceptors.
		if len(a.messageInterceptors) > 0 {
			var dropped bool
			messages, dropped = runMessageInterceptors(ctx, a.messageInterceptors, messages)
			if dropped {
				// Message dropped — return empty text response.
				return nil, errors.New("message interceptor dropped the request")
			}
		}

		// Run turn guardrails.
		turnRC := &RunContext{
			Deps:     deps,
			Usage:    state.usage,
			Prompt:   prompt,
			Messages: messages,
			RunStep:  state.runStep,
			RunID:    state.runID,
			EventBus: a.eventBus,
		}
		for _, g := range a.turnGuardrails {
			if gErr := g.fn(ctx, turnRC, messages); gErr != nil {
				return nil, &GuardrailError{
					GuardrailName: g.name,
					Message:       gErr.Error(),
				}
			}
		}

		// Fire OnModelRequest hooks.
		modelRC := &RunContext{
			Deps:     deps,
			Usage:    state.usage,
			Prompt:   prompt,
			Messages: messages,
			RunStep:  state.runStep,
			RunID:    state.runID,
			EventBus: a.eventBus,
		}
		a.fireHook(func(h Hook) {
			if h.OnModelRequest != nil {
				h.OnModelRequest(ctx, modelRC, messages)
			}
		})

		// Add trace step for model request.
		modelReqStart := time.Now()
		if a.tracingEnabled {
			state.traceSteps = append(state.traceSteps, TraceStep{
				Kind:      TraceModelRequest,
				Timestamp: modelReqStart,
				Data:      map[string]any{"message_count": len(messages)},
			})
		}

		// Call the model (through middleware chain if configured).
		var resp *ModelResponse
		if len(a.middleware) > 0 {
			chain := buildMiddlewareChain(a.middleware, a.model)
			resp, err = chain(ctx, messages, settings, params)
		} else {
			resp, err = a.model.Request(ctx, messages, settings, params)
		}
		if err != nil {
			return nil, fmt.Errorf("model request failed: %w", err)
		}

		// Track usage.
		state.usage.IncrRequest(resp.Usage)

		// Track cost.
		if a.costTracker != nil {
			singleUsage := RunUsage{}
			singleUsage.Incr(resp.Usage)
			a.costTracker.Record(a.model.ModelName(), singleUsage)
		}

		// Run response interceptors.
		if len(a.responseInterceptors) > 0 {
			if runResponseInterceptors(ctx, a.responseInterceptors, resp) {
				// Response dropped — treat as empty and retry.
				continue
			}
		}

		// Reset tool choice to auto after first tool call if auto-reset is enabled.
		if a.toolChoiceAutoReset && len(resp.ToolCalls()) > 0 && settings != nil && settings.ToolChoice != nil {
			s := *settings
			s.ToolChoice = ToolChoiceAuto()
			settings = &s
		}

		// Append response to history.
		state.messages = append(state.messages, *resp)

		// Fire OnModelResponse hooks.
		a.fireHook(func(h Hook) {
			if h.OnModelResponse != nil {
				h.OnModelResponse(ctx, modelRC, resp)
			}
		})

		// Add trace step for model response.
		if a.tracingEnabled {
			state.traceSteps = append(state.traceSteps, TraceStep{
				Kind:      TraceModelResponse,
				Timestamp: time.Now(),
				Duration:  time.Since(modelReqStart),
				Data:      map[string]any{"text": resp.TextContent(), "tool_calls": len(resp.ToolCalls())},
			})
		}

		// Check token limits.
		if limits.HasTokenLimits() {
			if err := limits.CheckTokens(state.usage); err != nil {
				return nil, err
			}
		}

		// Check run conditions.
		if len(a.runConditions) > 0 {
			condRC := &RunContext{
				Deps:     deps,
				Usage:    state.usage,
				Prompt:   prompt,
				Messages: state.messages,
				RunStep:  state.runStep,
				RunID:    state.runID,
			}
			for _, cond := range a.runConditions {
				if stop, reason := cond(ctx, condRC, resp); stop {
					// Return the text output if available, otherwise error.
					if hasText := resp.TextContent() != ""; hasText && a.outputSchema.AllowsText {
						text := resp.TextContent()
						output, parseErr := deserializeOutput[T](text, a.outputSchema.OuterTypedDictKey)
						if parseErr != nil {
							if a.outputSchema.Mode == OutputModeText {
								output = any(text).(T)
								parseErr = nil
							}
						}
						if parseErr == nil {
							return &RunResult[T]{
								Output:   output,
								Messages: state.messages,
								Usage:    state.usage,
								RunID:    state.runID,
							}, nil
						}
					}
					return nil, &RunConditionError{Reason: reason}
				}
			}
		}

		// Process the response.
		result, nextParts, deferredReqs, err := a.processResponse(ctx, state, resp, toolMap, outputToolNames, deps, prompt)
		if err != nil {
			return nil, err
		}
		// If there are deferred tool calls, return ErrDeferred.
		if len(deferredReqs) > 0 {
			return nil, &ErrDeferred[T]{
				Result: RunResultDeferred[T]{
					DeferredRequests: deferredReqs,
					Messages:         state.messages,
					Usage:            state.usage,
				},
			}
		}
		if result != nil {
			// Auto-store successful response in knowledge base.
			if a.kbAutoStore && a.knowledgeBase != nil {
				responseText := resp.TextContent()
				if responseText != "" {
					if storeErr := a.knowledgeBase.Store(ctx, responseText); storeErr != nil {
						return nil, fmt.Errorf("knowledge base store failed: %w", storeErr)
					}
				}
			}
			return &RunResult[T]{
				Output:   result.output,
				Messages: state.messages,
				Usage:    state.usage,
				RunID:    state.runID,
			}, nil
		}

		// No final result yet — append tool results and continue.
		if len(nextParts) > 0 {
			nextReq := ModelRequest{
				Parts:     nextParts,
				Timestamp: time.Now(),
			}
			state.messages = append(state.messages, nextReq)
		}
	}
}

type finalResult[T any] struct {
	output     T
	toolName   string
	toolCallID string
}

// deferredToolPart is an internal sentinel type used to signal that a tool call
// was deferred for external resolution. It implements ModelRequestPart but is
// never added to actual messages.
type deferredToolPart struct {
	ToolName   string
	ToolCallID string
	ArgsJSON   string
}

func (d deferredToolPart) requestPartKind() string { return "deferred-tool" }

// processResponse handles a model response: executes tool calls or extracts final result.
//
//nolint:cyclop
func (a *Agent[T]) processResponse(
	ctx context.Context,
	state *agentRunState,
	resp *ModelResponse,
	toolMap map[string]*Tool,
	outputToolNames map[string]bool,
	deps any,
	prompt string,
) (*finalResult[T], []ModelRequestPart, []DeferredToolRequest, error) {
	toolCalls := resp.ToolCalls()
	hasText := resp.TextContent() != ""

	// If no tool calls and text is allowed, try text output.
	if len(toolCalls) == 0 && hasText && a.outputSchema.AllowsText {
		text := resp.TextContent()
		output, err := deserializeOutput[T](text, a.outputSchema.OuterTypedDictKey)
		if err != nil {
			// For text mode with T=string, the text IS the output.
			// Try direct assignment.
			if a.outputSchema.Mode == OutputModeText {
				// T should be string in text mode.
				output = any(text).(T)
			} else if a.repairFunc != nil {
				// Try repair before failing.
				repairFn := a.repairFunc.(RepairFunc[T])
				repaired, repairErr := repairFn(ctx, text, err)
				if repairErr != nil {
					return nil, nil, nil, fmt.Errorf("failed to parse text output: %w", err)
				}
				output = repaired
			} else {
				return nil, nil, nil, fmt.Errorf("failed to parse text output: %w", err)
			}
		}

		// Validate output.
		rc := &RunContext{
			Deps:    deps,
			Usage:   state.usage,
			Prompt:  prompt,
			RunStep: state.runStep,
			RunID:   state.runID,
		}
		output, err = validateOutput(ctx, rc, output, a.outputValidators)
		if err != nil {
			var retryErr *ModelRetryError
			if errors.As(err, &retryErr) {
				if retryErr := incrementRetries(&state.retries, a.maxRetries, state.messages); retryErr != nil {
					return nil, nil, nil, retryErr
				}
				part := buildRetryParts(retryErr.Message, "", "")
				return nil, []ModelRequestPart{part}, nil, nil
			}
			return nil, nil, nil, err
		}

		return &finalResult[T]{output: output}, nil, nil, nil
	}

	// If no tool calls and no valid text, handle empty response.
	if len(toolCalls) == 0 {
		if resp.FinishReason == FinishReasonLength {
			return nil, nil, nil, &UnexpectedModelBehavior{
				Message: "model response ended due to token limit without producing a result",
			}
		}
		if resp.FinishReason == FinishReasonContentFilter {
			return nil, nil, nil, &ContentFilterError{
				UnexpectedModelBehavior: UnexpectedModelBehavior{
					Message: "content filter triggered",
				},
			}
		}
		// Retry on empty response.
		if retryErr := incrementRetries(&state.retries, a.maxRetries, state.messages); retryErr != nil {
			return nil, nil, nil, retryErr
		}
		part := buildRetryParts("empty response, please provide a result", "", "")
		return nil, []ModelRequestPart{part}, nil, nil
	}

	// Process tool calls.
	var resultParts []ModelRequestPart
	var result *finalResult[T]

	// Separate output tool calls from function tool calls.
	var outputCalls []ToolCallPart
	var functionCalls []ToolCallPart
	var unknownCalls []ToolCallPart

	for _, tc := range toolCalls {
		if outputToolNames[tc.ToolName] {
			outputCalls = append(outputCalls, tc)
		} else if _, ok := toolMap[tc.ToolName]; ok {
			functionCalls = append(functionCalls, tc)
		} else {
			unknownCalls = append(unknownCalls, tc)
		}
	}

	// Handle unknown tools.
	for _, tc := range unknownCalls {
		if retryErr := incrementRetries(&state.retries, a.maxRetries, state.messages); retryErr != nil {
			return nil, nil, nil, retryErr
		}
		part := buildRetryParts(
			fmt.Sprintf("unknown tool %q, available tools: %s", tc.ToolName, a.availableToolNames()),
			tc.ToolName,
			tc.ToolCallID,
		)
		resultParts = append(resultParts, part)
	}

	// Handle output tool calls.
	for _, tc := range outputCalls {
		output, err := deserializeOutput[T](tc.ArgsJSON, a.outputSchema.OuterTypedDictKey)
		if err != nil {
			// Try repair if available.
			if a.repairFunc != nil {
				repairFn := a.repairFunc.(RepairFunc[T])
				repaired, repairErr := repairFn(ctx, tc.ArgsJSON, err)
				if repairErr == nil {
					output = repaired
					err = nil
				}
			}
		}
		if err != nil {
			if retryErr := incrementRetries(&state.retries, a.maxRetries, state.messages); retryErr != nil {
				return nil, nil, nil, retryErr
			}
			part := buildRetryParts(
				"failed to parse output: "+err.Error(),
				tc.ToolName,
				tc.ToolCallID,
			)
			resultParts = append(resultParts, part)
			continue
		}

		// Validate output.
		rc := &RunContext{
			Deps:       deps,
			Usage:      state.usage,
			Prompt:     prompt,
			ToolName:   tc.ToolName,
			ToolCallID: tc.ToolCallID,
			RunStep:    state.runStep,
			RunID:      state.runID,
		}
		output, err = validateOutput(ctx, rc, output, a.outputValidators)
		if err != nil {
			var retryErr *ModelRetryError
			if errors.As(err, &retryErr) {
				if incErr := incrementRetries(&state.retries, a.maxRetries, state.messages); incErr != nil {
					return nil, nil, nil, incErr
				}
				part := buildRetryParts(retryErr.Message, tc.ToolName, tc.ToolCallID)
				resultParts = append(resultParts, part)
				continue
			}
			return nil, nil, nil, err
		}

		if result == nil {
			result = &finalResult[T]{
				output:     output,
				toolName:   tc.ToolName,
				toolCallID: tc.ToolCallID,
			}
			if a.endStrategy == EndStrategyEarly {
				// Return tool result part and skip remaining.
				resultParts = append(resultParts, ToolReturnPart{
					ToolName:   tc.ToolName,
					Content:    "output accepted",
					ToolCallID: tc.ToolCallID,
					Timestamp:  time.Now(),
				})
				// Skip remaining function calls in early mode.
				return result, resultParts, nil, nil
			}
		}
	}

	// Execute function tool calls.
	var deferredReqs []DeferredToolRequest
	if len(functionCalls) > 0 {
		if limit := state.limits.ToolCallsLimit; limit != nil {
			projected := state.usage.ToolCalls + len(functionCalls)
			if projected > *limit {
				return nil, nil, nil, &UsageLimitExceeded{
					Message: fmt.Sprintf("tool call limit of %d exceeded (used %d, pending %d)", *limit, state.usage.ToolCalls, len(functionCalls)),
				}
			}
		}
		funcParts, err := a.executeFunctionTools(ctx, state, functionCalls, toolMap, deps, prompt)
		if err != nil {
			return nil, nil, nil, err
		}
		// Separate deferred tool parts from normal parts.
		for _, part := range funcParts {
			if dp, ok := part.(deferredToolPart); ok {
				deferredReqs = append(deferredReqs, DeferredToolRequest(dp))
			} else {
				resultParts = append(resultParts, part)
			}
		}
	}

	// If there are deferred requests, return them.
	if len(deferredReqs) > 0 {
		return result, resultParts, deferredReqs, nil
	}

	// If we found a result in exhaustive mode, return it.
	if result != nil {
		return result, resultParts, nil, nil
	}

	return nil, resultParts, nil, nil
}

// executeFunctionTools executes function tool calls, running non-sequential
// tools concurrently.
func (a *Agent[T]) executeFunctionTools(
	ctx context.Context,
	state *agentRunState,
	calls []ToolCallPart,
	toolMap map[string]*Tool,
	deps any,
	prompt string,
) ([]ModelRequestPart, error) {
	// Separate sequential and concurrent calls.
	type indexedCall struct {
		idx  int
		call ToolCallPart
		tool *Tool
	}
	var sequentialCalls []indexedCall
	var concurrentCalls []indexedCall

	for i, tc := range calls {
		tool := toolMap[tc.ToolName]
		if tool.Definition.Sequential {
			sequentialCalls = append(sequentialCalls, indexedCall{idx: i, call: tc, tool: tool})
		} else {
			concurrentCalls = append(concurrentCalls, indexedCall{idx: i, call: tc, tool: tool})
		}
	}

	results := make([]ModelRequestPart, len(calls))

	// Execute concurrent tools.
	if len(concurrentCalls) > 0 {
		var wg sync.WaitGroup
		var mu sync.Mutex

		// Use a semaphore if max concurrency is set.
		var sem chan struct{}
		if a.maxConcurrency > 0 {
			sem = make(chan struct{}, a.maxConcurrency)
		}

		for _, ic := range concurrentCalls {
			wg.Add(1)
			go func(ic indexedCall) {
				defer wg.Done()
				if sem != nil {
					sem <- struct{}{}
					defer func() { <-sem }()
				}
				part := a.executeSingleTool(ctx, state, ic.call, ic.tool, deps, prompt)
				mu.Lock()
				results[ic.idx] = part
				mu.Unlock()
			}(ic)
		}
		wg.Wait()
	}

	// Execute sequential tools.
	for _, ic := range sequentialCalls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		part := a.executeSingleTool(ctx, state, ic.call, ic.tool, deps, prompt)
		results[ic.idx] = part
	}

	return results, nil
}

// executeSingleTool executes a single tool call and returns the result part.
func (a *Agent[T]) executeSingleTool(
	ctx context.Context,
	state *agentRunState,
	call ToolCallPart,
	tool *Tool,
	deps any,
	prompt string,
) ModelRequestPart {
	// Lock state for reading/writing shared fields.
	state.mu.Lock()
	state.usage.IncrToolCall()

	// Determine max retries for this tool.
	maxRetries := a.maxRetries
	if tool.MaxRetries != nil {
		maxRetries = *tool.MaxRetries
	}

	rc := &RunContext{
		Deps:       deps,
		Usage:      state.usage,
		Prompt:     prompt,
		Retry:      state.toolRetries[call.ToolName],
		MaxRetries: maxRetries,
		ToolName:   call.ToolName,
		ToolCallID: call.ToolCallID,
		Messages:   state.messages,
		RunStep:    state.runStep,
		RunID:      state.runID,
		EventBus:   a.eventBus,
	}
	state.mu.Unlock()

	// Publish ToolCalledEvent.
	if a.eventBus != nil {
		Publish(a.eventBus, ToolCalledEvent{RunID: state.runID, ToolName: call.ToolName, ArgsJSON: call.ArgsJSON})
	}

	// Check tool approval if required.
	if tool.RequiresApproval {
		if a.toolApprovalFunc == nil {
			return RetryPromptPart{
				Content:    fmt.Sprintf("tool %q requires approval but no approval function is configured", call.ToolName),
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				Timestamp:  time.Now(),
			}
		}
		approved, approvalErr := a.toolApprovalFunc(ctx, call.ToolName, call.ArgsJSON)
		if approvalErr != nil {
			return ToolReturnPart{
				ToolName:   call.ToolName,
				Content:    "error checking tool approval: " + approvalErr.Error(),
				ToolCallID: call.ToolCallID,
				Timestamp:  time.Now(),
			}
		}
		if !approved {
			return RetryPromptPart{
				Content:    fmt.Sprintf("tool call %q was denied by the user", call.ToolName),
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				Timestamp:  time.Now(),
			}
		}
	}

	// Fire OnToolStart hooks.
	a.fireHook(func(h Hook) {
		if h.OnToolStart != nil {
			h.OnToolStart(ctx, rc, call.ToolName, call.ArgsJSON)
		}
	})

	// Add trace step for tool call.
	toolStart := time.Now()
	if a.tracingEnabled {
		state.mu.Lock()
		state.traceSteps = append(state.traceSteps, TraceStep{
			Kind:      TraceToolCall,
			Timestamp: toolStart,
			Data:      map[string]any{"tool_name": call.ToolName, "args": call.ArgsJSON},
		})
		state.mu.Unlock()
	}

	// Apply tool timeout.
	toolCtx := ctx
	timeout := tool.Timeout
	if timeout == 0 && a.defaultToolTimeout > 0 {
		timeout = a.defaultToolTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		toolCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	result, err := tool.Handler(toolCtx, rc, call.ArgsJSON)

	// Fire OnToolEnd hooks with the result or error.
	{
		var resultStr string
		if err == nil {
			resultStr, _ = serializeToolResult(result)
		}
		a.fireHook(func(h Hook) {
			if h.OnToolEnd != nil {
				h.OnToolEnd(ctx, rc, call.ToolName, resultStr, err)
			}
		})

		// Add trace step for tool result.
		if a.tracingEnabled {
			var errStr string
			if err != nil {
				errStr = err.Error()
			}
			state.mu.Lock()
			state.traceSteps = append(state.traceSteps, TraceStep{
				Kind:      TraceToolResult,
				Timestamp: time.Now(),
				Duration:  time.Since(toolStart),
				Data:      map[string]any{"tool_name": call.ToolName, "result": resultStr, "error": errStr},
			})
			state.mu.Unlock()
		}
	}

	if err != nil {
		// Check for CallDeferred.
		var deferredErr *CallDeferred
		if errors.As(err, &deferredErr) {
			return deferredToolPart{
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				ArgsJSON:   call.ArgsJSON,
			}
		}

		// Check for ModelRetryError.
		var retryErr *ModelRetryError
		if errors.As(err, &retryErr) {
			state.mu.Lock()
			state.toolRetries[call.ToolName]++
			retryCount := state.toolRetries[call.ToolName]
			state.mu.Unlock()
			if retryCount > maxRetries {
				return RetryPromptPart{
					Content:    fmt.Sprintf("tool %q exceeded maximum retries (%d): %s", call.ToolName, maxRetries, retryErr.Message),
					ToolName:   call.ToolName,
					ToolCallID: call.ToolCallID,
					Timestamp:  time.Now(),
				}
			}
			return RetryPromptPart{
				Content:    retryErr.Message,
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				Timestamp:  time.Now(),
			}
		}

		// Other errors become tool return with error content.
		return ToolReturnPart{
			ToolName:   call.ToolName,
			Content:    "error: " + err.Error(),
			ToolCallID: call.ToolCallID,
			Timestamp:  time.Now(),
		}
	}

	// Serialize result.
	content, err := serializeToolResult(result)
	if err != nil {
		return ToolReturnPart{
			ToolName:   call.ToolName,
			Content:    "error serializing result: " + err.Error(),
			ToolCallID: call.ToolCallID,
			Timestamp:  time.Now(),
		}
	}

	// Run per-tool result validator.
	if tool.ResultValidator != nil {
		if valErr := tool.ResultValidator(ctx, rc, call.ToolName, content); valErr != nil {
			return RetryPromptPart{
				Content:    "tool result validation failed: " + valErr.Error(),
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				Timestamp:  time.Now(),
			}
		}
	}

	// Run global result validators.
	for _, validator := range a.globalToolResultValidators {
		if valErr := validator(ctx, rc, call.ToolName, content); valErr != nil {
			return RetryPromptPart{
				Content:    "tool result validation failed: " + valErr.Error(),
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				Timestamp:  time.Now(),
			}
		}
	}

	return ToolReturnPart{
		ToolName:   call.ToolName,
		Content:    content,
		ToolCallID: call.ToolCallID,
		Timestamp:  time.Now(),
	}
}

// serializeToolResult converts a tool result to a string.
func serializeToolResult(result any) (string, error) {
	if result == nil {
		return "", nil
	}
	if s, ok := result.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// buildInitialRequest constructs the initial ModelRequest with static system prompts and user prompt.
func (a *Agent[T]) buildInitialRequest(prompt string) ModelRequest {
	var parts []ModelRequestPart

	// Add system prompts.
	for _, sp := range a.systemPrompts {
		parts = append(parts, SystemPromptPart{
			Content:   sp,
			Timestamp: time.Now(),
		})
	}

	// Add user prompt.
	parts = append(parts, UserPromptPart{
		Content:   prompt,
		Timestamp: time.Now(),
	})

	return ModelRequest{
		Parts:     parts,
		Timestamp: time.Now(),
	}
}

// buildInitialRequestWithDynamic constructs the initial request including dynamic system prompts.
func (a *Agent[T]) buildInitialRequestWithDynamic(ctx context.Context, prompt string, state *agentRunState, deps any) (ModelRequest, error) {
	var parts []ModelRequestPart

	// Add static system prompts.
	for _, sp := range a.systemPrompts {
		parts = append(parts, SystemPromptPart{
			Content:   sp,
			Timestamp: time.Now(),
		})
	}

	// Add dynamic system prompts.
	for _, fn := range a.dynamicSystemPrompts {
		rc := &RunContext{
			Deps:    deps,
			Usage:   state.usage,
			Prompt:  prompt,
			RunStep: state.runStep,
			RunID:   state.runID,
		}
		content, err := fn(ctx, rc)
		if err != nil {
			return ModelRequest{}, fmt.Errorf("dynamic system prompt failed: %w", err)
		}
		if content != "" {
			parts = append(parts, SystemPromptPart{
				Content:   content,
				Timestamp: time.Now(),
			})
		}
	}

	// Retrieve knowledge base context.
	if a.knowledgeBase != nil {
		kbContent, err := a.knowledgeBase.Retrieve(ctx, prompt)
		if err != nil {
			return ModelRequest{}, fmt.Errorf("knowledge base retrieve failed: %w", err)
		}
		if kbContent != "" {
			parts = append(parts, SystemPromptPart{
				Content:   "[Knowledge Context] " + kbContent,
				Timestamp: time.Now(),
			})
		}
	}

	// Add user prompt.
	parts = append(parts, UserPromptPart{
		Content:   prompt,
		Timestamp: time.Now(),
	})

	return ModelRequest{
		Parts:     parts,
		Timestamp: time.Now(),
	}, nil
}

// newRunID generates a random run identifier.
func newRunID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// availableToolNames returns a formatted string of available tool names.
func (a *Agent[T]) availableToolNames() string {
	var names []string
	for _, t := range a.tools {
		names = append(names, t.Definition.Name)
	}
	for _, ot := range a.outputSchema.OutputTools {
		names = append(names, ot.Name)
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

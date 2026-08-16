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
	model                      Model
	systemPrompts              []string
	dynamicSystemPrompts       []SystemPromptFunc
	historyProcessors          []HistoryProcessor
	tools                      []Tool
	toolsets                   []*Toolset
	outputSchemaMu             sync.Mutex
	outputSchema               *OutputSchema
	outputValidators           []OutputValidatorFunc[T]
	outputOpts                 []OutputOption
	maxRetries                 int
	endStrategy                EndStrategy
	usageLimits                UsageLimits
	modelSettings              *ModelSettings
	toolApprovalFunc           ToolApprovalFunc
	maxConcurrency             int
	knowledgeBase              KnowledgeBase
	kbAutoStore                bool
	toolsPrepareFunc           AgentToolsPrepareFunc
	hooks                      []Hook
	inputGuardrails            []namedInputGuardrail
	turnGuardrails             []namedTurnGuardrail
	repairFunc                 any // RepairFunc[T], stored as any to avoid generic field
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
	middleware                 []RequestMiddlewareFunc
	streamMiddleware           []AgentStreamMiddleware
	toolChoice                 *ToolChoice
	toolChoiceAutoReset        bool
	truncationConfig           *TruncationConfig
	dynamicPromptCache         map[int]string
	dynamicPromptCacheMu       sync.Mutex
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
	// ToolState is the exported state of stateful tools at the end of this run.
	// Pass this to WithToolState() on the next Run() to restore tool state
	// across turns in a multi-turn conversation.
	ToolState map[string]any
}

// AgentOption configures the agent via functional options.
type AgentOption[T any] func(*Agent[T])

type sessionModel interface {
	NewSession() Model
}

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

// WithToolOutputTruncation enables head/tail truncation of tool outputs before
// they are recorded into conversation history. This prevents large tool results
// (e.g., a grep returning thousands of lines) from bloating context on every
// subsequent model request. Off by default for backward compatibility.
func WithToolOutputTruncation[T any](config TruncationConfig) AgentOption[T] {
	return func(a *Agent[T]) {
		a.truncationConfig = &config
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
		for _, ts := range toolsets {
			if ts == nil {
				continue
			}
			if len(ts.Hooks) > 0 {
				a.hooks = append(a.hooks, ts.Hooks...)
			}
			if len(ts.DynamicSystemPrompts) > 0 {
				a.dynamicSystemPrompts = append(a.dynamicSystemPrompts, ts.DynamicSystemPrompts...)
			}
		}
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
	if cloner, ok := model.(sessionModel); ok {
		model = cloner.NewSession()
	}

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

// exportToolState collects state from all stateful tools on the agent.
func (a *Agent[T]) exportToolState() map[string]any {
	state := make(map[string]any)
	for _, t := range a.tools {
		if t.Stateful != nil {
			if s, err := t.Stateful.ExportState(); err == nil {
				state[t.Definition.Name] = s
			}
		}
	}
	for _, ts := range a.toolsets {
		for _, t := range ts.Tools {
			if t.Stateful != nil {
				if s, err := t.Stateful.ExportState(); err == nil {
					state[t.Definition.Name] = s
				}
			}
		}
	}
	if len(state) == 0 {
		return nil
	}
	return state
}

func (a *Agent[T]) buildRunContext(state *agentRunState, deps any, prompt string) *RunContext {
	return &RunContext{
		Deps:         deps,
		Usage:        state.usage,
		Prompt:       prompt,
		Messages:     state.messages,
		RunStep:      state.runStep,
		RunID:        state.runID,
		ParentRunID:  state.parentRunID,
		RunStartTime: state.startTime,
		EventBus:     a.eventBus,
		Detach:       state.detach,
		toolStateGetter: func() map[string]any {
			return a.exportToolState()
		},
		runStateSnapshotGetter: func() *RunStateSnapshot {
			return state.snapshot(prompt, a.exportToolState())
		},
	}
}

func (a *Agent[T]) beginRun(ctx context.Context, state *agentRunState, deps any, prompt string) context.Context {
	rc := a.buildRunContext(state, deps, prompt)
	if a.eventBus != nil {
		Publish(a.eventBus, NewRunStartedEvent(state.runID, state.parentRunID, prompt, state.currentTraceStartTime()))
	}
	a.fireHook(func(h Hook) {
		if h.OnRunStart != nil {
			h.OnRunStart(ctx, rc, prompt)
		}
	})
	return ContextWithRunID(ctx, state.runID)
}

func (a *Agent[T]) endRun(ctx context.Context, state *agentRunState, deps any, prompt string, runErr error) {
	endRC := a.buildRunContext(state, deps, prompt)
	if runErr != nil && a.tracingEnabled {
		state.mu.Lock()
		state.traceSteps = append(state.traceSteps, TraceStep{
			Kind:      TraceErrorRaised,
			Timestamp: time.Now(),
			Data: map[string]any{
				"turn_number": state.runStep,
				"error":       runErr.Error(),
			},
		})
		state.mu.Unlock()
	}
	if a.eventBus != nil {
		if runErr != nil {
			Publish(a.eventBus, ErrorRaisedEvent{
				RunID:       state.runID,
				ParentRunID: state.parentRunID,
				TurnNumber:  state.runStep,
				Error:       runErr.Error(),
				Cause:       runErr,
				RaisedAt:    time.Now(),
			})
		}
		Publish(a.eventBus, NewRunCompletedEvent(
			state.runID,
			state.parentRunID,
			state.currentTraceStartTime(),
			time.Now(),
			runErr,
		))
	}
	a.fireHook(func(h Hook) {
		if h.OnRunEnd != nil {
			h.OnRunEnd(ctx, endRC, state.messages, runErr)
		}
	})
}

func (a *Agent[T]) runCostSnapshot() *RunCost {
	if a == nil || a.costTracker == nil {
		return nil
	}
	return a.costTracker.buildRunCost()
}

// restoreToolState restores state to stateful tools from a previous export.
func (a *Agent[T]) restoreToolState(state map[string]any) {
	for i := range a.tools {
		if a.tools[i].Stateful != nil {
			if s, ok := state[a.tools[i].Definition.Name]; ok {
				_ = a.tools[i].Stateful.RestoreState(s)
			}
		}
	}
	for _, ts := range a.toolsets {
		for i := range ts.Tools {
			if ts.Tools[i].Stateful != nil {
				if s, ok := state[ts.Tools[i].Definition.Name]; ok {
					_ = ts.Tools[i].Stateful.RestoreState(s)
				}
			}
		}
	}
}

// RunOption configures a specific run invocation.
type RunOption func(*runConfig)

type runConfig struct {
	deps                any
	modelSettings       *ModelSettings
	usageLimits         *UsageLimits
	messages            []ModelMessage
	snapshot            *RunStateSnapshot
	initialRequestParts []ModelRequestPart
	deferredResults     []DeferredToolResult
	batchConcurrency    int
	detach              <-chan struct{}
	toolState           map[string]any
	steerQueue          *SteerQueue
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

// WithInitialRequestParts appends additional parts to the first user request
// generated by Run/Iter. Useful for multimodal inputs such as ImagePart.
func WithInitialRequestParts(parts ...ModelRequestPart) RunOption {
	return func(c *runConfig) {
		if len(parts) == 0 {
			return
		}
		cp := make([]ModelRequestPart, len(parts))
		copy(cp, parts)
		c.initialRequestParts = append(c.initialRequestParts, cp...)
	}
}

// WithToolState restores stateful tool state from a previous run.
// Use this with WithMessages to continue multi-turn conversations where
// tools like planning and invariants need to retain their state.
func WithToolState(state map[string]any) RunOption {
	return func(c *runConfig) {
		c.toolState = state
	}
}

// WithDetach provides a channel that the UI layer can close to signal the
// currently executing tool to move its work to the background. Tools that
// support detach (e.g., bash) will select on this channel alongside their
// blocking operation. When closed, the tool adopts the running process into
// the background process pool and returns immediately.
func WithDetach(ch <-chan struct{}) RunOption {
	return func(c *runConfig) {
		c.detach = ch
	}
}

// WithSteerQueue attaches a queue consumed at safe model-request boundaries.
// It is supported by RunStream; synchronous Run rejects this option.
func WithSteerQueue(queue *SteerQueue) RunOption {
	return func(c *runConfig) {
		c.steerQueue = queue
	}
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
	if cfg.steerQueue != nil {
		cfg.steerQueue.close(ErrSteeringNeedsStreaming)
		return nil, ErrSteeringNeedsStreaming
	}

	// Build output schema.
	a.ensureOutputSchema()

	exec := a.initializeRunExecution(ctx, cfg)
	state := exec.state
	settings := exec.settings
	limits := exec.limits
	deps := exec.deps

	var err error
	prompt, err = a.applyInputGuardrails(ctx, state, deps, prompt, true)
	if err != nil {
		return nil, err
	}

	if err := a.bootstrapRunMessages(ctx, state, prompt, deps, cfg.deferredResults, cfg.initialRequestParts); err != nil {
		return nil, err
	}

	// Fire OnRunStart hooks BEFORE injecting this run's RunID into context.
	// This ordering is critical: hooks need to see the parent's RunID in
	// context (if any) to establish parent-child relationships. After hooks
	// fire, inject this run's RunID so child work propagates the correct lineage.
	ctx = a.beginRun(ctx, state, deps, prompt)

	// Emit deferred resolution events AFTER RunStarted so subscribers
	// see them in the correct order: RunStarted → RunResumed → DeferredResolved.
	if a.eventBus != nil && len(cfg.deferredResults) > 0 {
		Publish(a.eventBus, RunResumedEvent{
			RunID:       state.runID,
			ParentRunID: state.parentRunID,
			ResumedAt:   time.Now(),
		})
		for _, dr := range cfg.deferredResults {
			Publish(a.eventBus, DeferredResolvedEvent{
				RunID:       state.runID,
				ParentRunID: state.parentRunID,
				ToolCallID:  dr.ToolCallID,
				ToolName:    dr.ToolName,
				Content:     dr.Content,
				IsError:     dr.IsError,
				ResolvedAt:  time.Now(),
			})
		}
	}

	result, runErr := a.runLoop(ctx, state, prompt, settings, limits, deps)
	a.endRun(ctx, state, deps, prompt, runErr)

	runCost := a.runCostSnapshot()
	if result != nil {
		result.Cost = runCost
	}

	// Build trace if enabled.
	if a.tracingEnabled {
		trace := buildRunTrace(state, prompt, runErr)
		trace.Cost = runCost
		if result != nil {
			result.Trace = trace
		}

		// Export trace to all registered exporters.
		for _, exporter := range a.traceExporters {
			// Exporter errors are non-fatal — don't break the run.
			_ = exporter.Export(ctx, trace)
		}
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
	a.ensureOutputSchema()

	exec := a.initializeRunExecution(ctx, cfg)
	state := exec.state
	settings := exec.settings
	limits := exec.limits
	deps := exec.deps

	var err error
	prompt, err = a.applyInputGuardrails(ctx, state, deps, prompt, true)
	if err != nil {
		cfg.steerQueue.close(err)
		return nil, err
	}

	if err := a.bootstrapRunMessages(ctx, state, prompt, deps, cfg.deferredResults, cfg.initialRequestParts); err != nil {
		cfg.steerQueue.close(err)
		return nil, err
	}

	ctx = a.beginRun(ctx, state, deps, prompt)

	// Emit deferred resolution events AFTER RunStarted (same as Run).
	if a.eventBus != nil && len(cfg.deferredResults) > 0 {
		Publish(a.eventBus, RunResumedEvent{
			RunID:       state.runID,
			ParentRunID: state.parentRunID,
			ResumedAt:   time.Now(),
		})
		for _, dr := range cfg.deferredResults {
			Publish(a.eventBus, DeferredResolvedEvent{
				RunID:       state.runID,
				ParentRunID: state.parentRunID,
				ToolCallID:  dr.ToolCallID,
				ToolName:    dr.ToolName,
				Content:     dr.Content,
				IsError:     dr.IsError,
				ResolvedAt:  time.Now(),
			})
		}
	}

	// Gather all tools and build lookup maps.
	allTools := a.allTools()
	toolMap := make(map[string]*Tool)
	for i := range allTools {
		toolMap[allTools[i].Definition.Name] = &allTools[i]
	}
	outputToolNames := make(map[string]bool)
	for _, ot := range a.outputSchema.OutputTools {
		outputToolNames[ot.Name] = true
	}

	stream := newAgentStream(
		a,
		ctx,
		state,
		settings,
		limits,
		deps,
		prompt,
		allTools,
		toolMap,
		outputToolNames,
		cfg.steerQueue,
	)

	initialMessages := make([]ModelMessage, len(state.messages))
	copy(initialMessages, state.messages)

	if err := stream.startTurn(); err != nil {
		stream.finish(nil, err)
		return nil, err
	}

	return newStreamResult(stream, initialMessages, stream.Result), nil
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

	rc := a.buildRunContext(state, deps, prompt)

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
func (a *Agent[T]) runLoop(ctx context.Context, state *agentRunState, prompt string, settings *ModelSettings, limits UsageLimits, deps any) (*RunResult[T], error) {
	engine := a.newTurnEngine(ctx, state, prompt, settings, limits, deps)
	for {
		_, result, err := engine.Step()
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
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
				textOutput, ok := any(text).(T)
				if !ok {
					return nil, nil, nil, fmt.Errorf("output mode is text but type %T is not compatible with string", output)
				}
				output = textOutput
			} else if a.repairFunc != nil {
				// Try repair before failing.
				repairFn, ok := a.repairFunc.(RepairFunc[T])
				if !ok {
					return nil, nil, nil, fmt.Errorf("failed to parse text output: %w", err)
				}
				repaired, repairErr := repairFn(ctx, text, err)
				repairSucceeded := repairErr == nil
				a.fireHook(func(h Hook) {
					if h.OnOutputRepair != nil {
						repairRC := &RunContext{
							Deps: deps, Usage: state.usage, Prompt: prompt,
							RunStep: state.runStep, RunID: state.runID, RunStartTime: state.startTime,
						}
						h.OnOutputRepair(ctx, repairRC, repairSucceeded, repairErr)
					}
				})
				if repairErr != nil {
					return nil, nil, nil, fmt.Errorf("failed to parse text output: %w", err)
				}
				output = repaired
			} else {
				return nil, nil, nil, fmt.Errorf("failed to parse text output: %w", err)
			}
		}

		// Validate output.
		rc := a.buildRunContext(state, deps, prompt)
		output, err = validateOutput(ctx, rc, output, a.outputValidators)
		validationPassed := err == nil
		a.fireHook(func(h Hook) {
			if h.OnOutputValidation != nil {
				h.OnOutputValidation(ctx, rc, validationPassed, err)
			}
		})
		if err != nil {
			var retryErr *ModelRetryError
			if errors.As(err, &retryErr) {
				if incErr := incrementRetries(&state.retries, a.maxRetries, state.messages); incErr != nil {
					return nil, nil, nil, incErr
				}
				a.recordRetryScheduled(state, retryErr.Message, "", "")
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
		reason := "empty response, please provide a result"
		a.recordRetryScheduled(state, reason, "", "")
		part := buildRetryParts(reason, "", "")
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

	// Handle unknown tools. Increment retries once for the batch, not per-call,
	// so a single response with multiple unknown tools doesn't exhaust retries.
	if len(unknownCalls) > 0 {
		if retryErr := incrementRetries(&state.retries, a.maxRetries, state.messages); retryErr != nil {
			return nil, nil, nil, retryErr
		}
		for _, tc := range unknownCalls {
			reason := fmt.Sprintf("unknown tool %q, available tools: %s", tc.ToolName, a.availableToolNames())
			a.recordRetryScheduled(state, reason, tc.ToolName, tc.ToolCallID)
			part := buildRetryParts(reason, tc.ToolName, tc.ToolCallID)
			resultParts = append(resultParts, part)
		}
	}

	// Handle output tool calls.
	for _, tc := range outputCalls {
		output, err := deserializeOutput[T](tc.ArgsJSON, a.outputSchema.OuterTypedDictKey)
		if err != nil {
			// Try repair if available.
			if a.repairFunc != nil {
				if repairFn, ok := a.repairFunc.(RepairFunc[T]); ok {
					repaired, repairErr := repairFn(ctx, tc.ArgsJSON, err)
					repairSucceeded := repairErr == nil
					a.fireHook(func(h Hook) {
						if h.OnOutputRepair != nil {
							repairRC := &RunContext{
								Deps: deps, Usage: state.usage, Prompt: prompt,
								ToolName: tc.ToolName, ToolCallID: tc.ToolCallID,
								RunStep: state.runStep, RunID: state.runID, RunStartTime: state.startTime,
							}
							h.OnOutputRepair(ctx, repairRC, repairSucceeded, repairErr)
						}
					})
					if repairErr == nil {
						output = repaired
						err = nil
					}
				}
			}
		}
		if err != nil {
			if retryErr := incrementRetries(&state.retries, a.maxRetries, state.messages); retryErr != nil {
				return nil, nil, nil, retryErr
			}
			reason := "failed to parse output: " + err.Error()
			a.recordRetryScheduled(state, reason, tc.ToolName, tc.ToolCallID)
			part := buildRetryParts(reason, tc.ToolName, tc.ToolCallID)
			resultParts = append(resultParts, part)
			continue
		}

		// Validate output.
		rc := a.buildRunContext(state, deps, prompt)
		rc.ToolName = tc.ToolName
		rc.ToolCallID = tc.ToolCallID
		output, err = validateOutput(ctx, rc, output, a.outputValidators)
		validationPassed := err == nil
		a.fireHook(func(h Hook) {
			if h.OnOutputValidation != nil {
				h.OnOutputValidation(ctx, rc, validationPassed, err)
			}
		})
		if err != nil {
			var retryErr *ModelRetryError
			if errors.As(err, &retryErr) {
				if incErr := incrementRetries(&state.retries, a.maxRetries, state.messages); incErr != nil {
					return nil, nil, nil, incErr
				}
				a.recordRetryScheduled(state, retryErr.Message, tc.ToolName, tc.ToolCallID)
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
			// Answer the output tool call in both end strategies so its
			// tool_use never dangles in history.
			resultParts = append(resultParts, ToolReturnPart{
				ToolName:   tc.ToolName,
				Content:    "output accepted",
				ToolCallID: tc.ToolCallID,
				Timestamp:  time.Now(),
			})
			if a.endStrategy == EndStrategyEarly {
				// Skip remaining function calls in early mode.
				return result, resultParts, nil, nil
			}
			continue
		}
		// A result was already accepted; answer the duplicate call so it
		// isn't left dangling in history.
		resultParts = append(resultParts, ToolReturnPart{
			ToolName:   tc.ToolName,
			Content:    "output already accepted; duplicate ignored",
			ToolCallID: tc.ToolCallID,
			Timestamp:  time.Now(),
		})
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
		// Reset global result-retry counter after successful tool execution.
		// The model is making progress, so give it a fresh retry allowance
		// for future result validation attempts. Without this, scattered
		// model errors (empty responses, unknown tools) across a long run
		// accumulate and eventually hit the maxRetries limit even though
		// the model self-corrects between each failure.
		state.retries = 0
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

func (a *Agent[T]) recordRetryScheduled(state *agentRunState, reason, toolName, toolCallID string) {
	if state == nil {
		return
	}
	now := time.Now()
	retry := state.retries
	maxRetries := a.maxRetries
	if a.tracingEnabled {
		state.mu.Lock()
		state.traceSteps = append(state.traceSteps, TraceStep{
			Kind:      TraceRetryScheduled,
			Timestamp: now,
			Data: map[string]any{
				"turn_number":  state.runStep,
				"tool_name":    toolName,
				"tool_call_id": toolCallID,
				"reason":       reason,
				"retry":        retry,
				"max_retries":  maxRetries,
			},
		})
		state.mu.Unlock()
	}
	if a.eventBus != nil {
		Publish(a.eventBus, RetryScheduledEvent{
			RunID:       state.runID,
			ParentRunID: state.parentRunID,
			TurnNumber:  state.runStep,
			ToolName:    toolName,
			ToolCallID:  toolCallID,
			Reason:      reason,
			Retry:       retry,
			MaxRetries:  maxRetries,
			ScheduledAt: now,
		})
	}
}

// executeFunctionTools executes function tool calls using consecutive batches of
// concurrency-safe tools. Exclusive tools act as barriers and always execute
// alone in their original position.
func (a *Agent[T]) executeFunctionTools(
	ctx context.Context,
	state *agentRunState,
	calls []ToolCallPart,
	toolMap map[string]*Tool,
	deps any,
	prompt string,
) ([]ModelRequestPart, error) {
	type indexedCall struct {
		idx  int
		call ToolCallPart
		tool *Tool
	}
	type toolBatch struct {
		concurrencySafe bool
		calls           []indexedCall
	}

	var batches []toolBatch
	for i, tc := range calls {
		tool := toolMap[tc.ToolName]
		concurrencySafe := tool.Definition.ConcurrencySafe && !tool.Definition.Sequential
		ic := indexedCall{idx: i, call: tc, tool: tool}
		if concurrencySafe && len(batches) > 0 && batches[len(batches)-1].concurrencySafe {
			batches[len(batches)-1].calls = append(batches[len(batches)-1].calls, ic)
		} else {
			batches = append(batches, toolBatch{
				concurrencySafe: concurrencySafe,
				calls:           []indexedCall{ic},
			})
		}
	}

	results := make([]ModelRequestPart, len(calls))

	pendingCallsFrom := func(batchIdx, callIdx int) []indexedCall {
		var pending []indexedCall
		for i := batchIdx; i < len(batches); i++ {
			start := 0
			if i == batchIdx {
				start = callIdx
			}
			pending = append(pending, batches[i].calls[start:]...)
		}
		return pending
	}

	publishPendingFailures := func(pending []indexedCall, msg string) {
		if a.eventBus == nil {
			return
		}
		for _, ic := range pending {
			if results[ic.idx] != nil {
				continue
			}
			Publish(a.eventBus, NewToolCalledEvent(
				state.runID, state.parentRunID,
				ic.call.ToolCallID, ic.call.ToolName,
				ic.call.ArgsJSON, time.Now(),
			))
			Publish(a.eventBus, ToolFailedEvent{
				RunID: state.runID, ParentRunID: state.parentRunID,
				ToolCallID: ic.call.ToolCallID, ToolName: ic.call.ToolName,
				Error: msg, FailedAt: time.Now(),
			})
		}
	}

	for batchIdx, batch := range batches {
		if err := ctx.Err(); err != nil {
			publishPendingFailures(pendingCallsFrom(batchIdx, 0), err.Error())
			return nil, err
		}

		if batch.concurrencySafe {
			var wg sync.WaitGroup
			var mu sync.Mutex
			var sem chan struct{}
			if a.maxConcurrency > 0 {
				sem = make(chan struct{}, a.maxConcurrency)
			}

			for _, ic := range batch.calls {
				wg.Add(1)
				go func(ic indexedCall) {
					defer wg.Done()
					if sem != nil {
						select {
						case sem <- struct{}{}:
							defer func() { <-sem }()
						case <-ctx.Done():
							if a.eventBus != nil {
								Publish(a.eventBus, ToolCalledEvent{
									RunID: state.runID, ParentRunID: state.parentRunID,
									ToolCallID: ic.call.ToolCallID, ToolName: ic.call.ToolName,
									ArgsJSON: ic.call.ArgsJSON, CalledAt: time.Now(),
								})
								Publish(a.eventBus, ToolFailedEvent{
									RunID: state.runID, ParentRunID: state.parentRunID,
									ToolCallID: ic.call.ToolCallID, ToolName: ic.call.ToolName,
									Error: "context cancelled waiting for semaphore", FailedAt: time.Now(),
								})
							}
							mu.Lock()
							results[ic.idx] = ToolReturnPart{
								ToolName:   ic.call.ToolName,
								Content:    "error: " + ctx.Err().Error(),
								ToolCallID: ic.call.ToolCallID,
								Timestamp:  time.Now(),
							}
							mu.Unlock()
							return
						}
					}
					part := a.executeSingleTool(ctx, state, ic.call, ic.tool, deps, prompt)
					mu.Lock()
					results[ic.idx] = part
					mu.Unlock()
				}(ic)
			}
			wg.Wait()
			continue
		}

		for callIdx, ic := range batch.calls {
			if err := ctx.Err(); err != nil {
				publishPendingFailures(pendingCallsFrom(batchIdx, callIdx), err.Error())
				return nil, err
			}
			results[ic.idx] = a.executeSingleTool(ctx, state, ic.call, ic.tool, deps, prompt)
		}
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

	rc := a.buildRunContext(state, deps, prompt)
	rc.Retry = state.toolRetries[call.ToolName]
	rc.MaxRetries = maxRetries
	rc.ToolName = call.ToolName
	rc.ToolCallID = call.ToolCallID
	state.mu.Unlock()

	// Publish ToolCalledEvent.
	if a.eventBus != nil {
		Publish(a.eventBus, NewToolCalledEvent(
			state.runID,
			state.parentRunID,
			call.ToolCallID,
			call.ToolName,
			call.ArgsJSON,
			time.Now(),
		))
	}

	// Enrich context with tool call ID early so approval funcs can access it.
	approvalCtx := ContextWithToolCallID(ctx, call.ToolCallID)

	// Check tool approval if required.
	if tool.RequiresApproval {
		if a.eventBus != nil {
			Publish(a.eventBus, ApprovalRequestedEvent{
				RunID:       state.runID,
				ParentRunID: state.parentRunID,
				ToolCallID:  call.ToolCallID,
				ToolName:    call.ToolName,
				ArgsJSON:    call.ArgsJSON,
				RequestedAt: time.Now(),
			})
		}
		if a.toolApprovalFunc == nil {
			if a.eventBus != nil {
				Publish(a.eventBus, ApprovalResolvedEvent{
					RunID:       state.runID,
					ParentRunID: state.parentRunID,
					ToolCallID:  call.ToolCallID,
					ToolName:    call.ToolName,
					Approved:    false,
					ResolvedAt:  time.Now(),
				})
				Publish(a.eventBus, ToolFailedEvent{
					RunID:       state.runID,
					ParentRunID: state.parentRunID,
					ToolCallID:  call.ToolCallID,
					ToolName:    call.ToolName,
					Error:       "no approval function configured",
					FailedAt:    time.Now(),
				})
			}
			return RetryPromptPart{
				Content:    fmt.Sprintf("tool %q requires approval but no approval function is configured", call.ToolName),
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				Timestamp:  time.Now(),
			}
		}
		firstApprovalWait := state.beginApprovalWait()
		if a.eventBus != nil && firstApprovalWait {
			Publish(a.eventBus, RunWaitingEvent{
				RunID:       state.runID,
				ParentRunID: state.parentRunID,
				Reason:      "approval",
				WaitingAt:   time.Now(),
			})
		}
		approved, approvalErr := a.toolApprovalFunc(approvalCtx, call.ToolName, call.ArgsJSON)
		lastApprovalResolved := state.endApprovalWait()
		if a.eventBus != nil && lastApprovalResolved {
			Publish(a.eventBus, RunResumedEvent{
				RunID:       state.runID,
				ParentRunID: state.parentRunID,
				ResumedAt:   time.Now(),
			})
		}
		if a.eventBus != nil {
			Publish(a.eventBus, ApprovalResolvedEvent{
				RunID:       state.runID,
				ParentRunID: state.parentRunID,
				ToolCallID:  call.ToolCallID,
				ToolName:    call.ToolName,
				Approved:    approvalErr == nil && approved,
				ResolvedAt:  time.Now(),
			})
		}
		if approvalErr != nil {
			if a.eventBus != nil {
				Publish(a.eventBus, ToolFailedEvent{
					RunID:       state.runID,
					ParentRunID: state.parentRunID,
					ToolCallID:  call.ToolCallID,
					ToolName:    call.ToolName,
					Error:       "approval error: " + approvalErr.Error(),
					FailedAt:    time.Now(),
				})
			}
			return ToolReturnPart{
				ToolName:   call.ToolName,
				Content:    "error checking tool approval: " + approvalErr.Error(),
				ToolCallID: call.ToolCallID,
				Timestamp:  time.Now(),
			}
		}
		if !approved {
			if a.eventBus != nil {
				Publish(a.eventBus, ToolFailedEvent{
					RunID:       state.runID,
					ParentRunID: state.parentRunID,
					ToolCallID:  call.ToolCallID,
					ToolName:    call.ToolName,
					Error:       "denied by user",
					FailedAt:    time.Now(),
				})
			}
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
			h.OnToolStart(ctx, rc, call.ToolCallID, call.ToolName, call.ArgsJSON)
		}
	})

	// Add trace step for tool call.
	toolStart := time.Now()
	if a.tracingEnabled {
		state.mu.Lock()
		state.traceSteps = append(state.traceSteps, TraceStep{
			Kind:      TraceToolCall,
			Timestamp: toolStart,
			Data:      map[string]any{"tool_call_id": call.ToolCallID, "tool_name": call.ToolName, "args": call.ArgsJSON, "turn_number": state.runStep},
		})
		state.mu.Unlock()
	}

	// Apply tool timeout.
	toolCtx := approvalCtx
	timeout := tool.Timeout
	if timeout == 0 && a.defaultToolTimeout > 0 {
		timeout = a.defaultToolTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		toolCtx, cancel = context.WithTimeout(toolCtx, timeout)
		defer cancel()
	}

	result, err := safeCallHandler(tool.Handler, toolCtx, rc, call.ArgsJSON)

	// Fire OnToolEnd hooks with the result or error.
	{
		var resultStr string
		if err == nil {
			var serErr error
			resultStr, serErr = serializeToolResult(result)
			if serErr != nil {
				resultStr = fmt.Sprintf("(serialization error: %v)", serErr)
			}
		}
		a.fireHook(func(h Hook) {
			if h.OnToolEnd != nil {
				h.OnToolEnd(ctx, rc, call.ToolCallID, call.ToolName, resultStr, err)
			}
		})

		// Publish ToolCompleted or ToolFailed runtime event.
		// CallDeferred is not a failure — it's expected control flow.
		if a.eventBus != nil {
			var isDeferred bool
			if err != nil {
				var deferredCheck *CallDeferred
				isDeferred = errors.As(err, &deferredCheck)
			}
			if !isDeferred {
				dur := time.Since(toolStart).Milliseconds()
				if err != nil {
					Publish(a.eventBus, ToolFailedEvent{
						RunID:       state.runID,
						ParentRunID: state.parentRunID,
						ToolCallID:  call.ToolCallID,
						ToolName:    call.ToolName,
						Error:       err.Error(),
						DurationMs:  dur,
						FailedAt:    time.Now(),
					})
				} else {
					Publish(a.eventBus, ToolCompletedEvent{
						RunID:       state.runID,
						ParentRunID: state.parentRunID,
						ToolCallID:  call.ToolCallID,
						ToolName:    call.ToolName,
						Result:      resultStr,
						DurationMs:  dur,
						CompletedAt: time.Now(),
					})
				}
			}
		}

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
				Data:      map[string]any{"tool_call_id": call.ToolCallID, "tool_name": call.ToolName, "result": resultStr, "error": errStr, "turn_number": state.runStep},
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
			retryCount := state.toolRetries[call.ToolName]
			state.toolRetries[call.ToolName] = retryCount + 1
			state.mu.Unlock()
			if retryCount >= maxRetries {
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

	// Check for multimodal result before serializing.
	var images []ImagePart
	var content string
	if mResult, ok := result.(ToolResultWithImages); ok {
		content = mResult.Text
		images = mResult.Images
	} else {
		var serErr error
		content, serErr = serializeToolResult(result)
		if serErr != nil {
			return ToolReturnPart{
				ToolName:   call.ToolName,
				Content:    "error serializing result: " + serErr.Error(),
				ToolCallID: call.ToolCallID,
				Timestamp:  time.Now(),
			}
		}
	}

	// Run per-tool result validator.
	if tool.ResultValidator != nil {
		if valErr := tool.ResultValidator(ctx, rc, call.ToolName, content); valErr != nil {
			state.mu.Lock()
			retryCount := state.toolRetries[call.ToolName]
			state.toolRetries[call.ToolName] = retryCount + 1
			state.mu.Unlock()
			msg := "tool result validation failed: " + valErr.Error()
			if retryCount >= maxRetries {
				msg = fmt.Sprintf("tool %q exceeded maximum retries (%d): %s", call.ToolName, maxRetries, msg)
			}
			return RetryPromptPart{
				Content:    msg,
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				Timestamp:  time.Now(),
			}
		}
	}

	// Run global result validators.
	for _, validator := range a.globalToolResultValidators {
		if valErr := validator(ctx, rc, call.ToolName, content); valErr != nil {
			state.mu.Lock()
			retryCount := state.toolRetries[call.ToolName]
			state.toolRetries[call.ToolName] = retryCount + 1
			state.mu.Unlock()
			msg := "tool result validation failed: " + valErr.Error()
			if retryCount >= maxRetries {
				msg = fmt.Sprintf("tool %q exceeded maximum retries (%d): %s", call.ToolName, maxRetries, msg)
			}
			return RetryPromptPart{
				Content:    msg,
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				Timestamp:  time.Now(),
			}
		}
	}

	// Reset the per-tool retry counter on success. Without this,
	// cumulative ModelRetryError failures across separate (unrelated)
	// invocations would accumulate and eventually trigger "exceeded
	// maximum retries" even when the tool recovered each time.
	state.mu.Lock()
	delete(state.toolRetries, call.ToolName)
	state.mu.Unlock()

	// Apply truncation if configured to prevent large tool outputs from
	// bloating conversation history on every subsequent model request.
	if a.truncationConfig != nil {
		content = TruncateToolOutput(content, *a.truncationConfig)
	}

	return ToolReturnPart{
		ToolName:   call.ToolName,
		Content:    content,
		ToolCallID: call.ToolCallID,
		Timestamp:  time.Now(),
		Images:     images,
	}
}

// safeCallHandler executes a tool handler with panic recovery, converting any
// panic into an error so a misbehaving tool doesn't crash the entire process.
func safeCallHandler(handler ToolHandler, ctx context.Context, rc *RunContext, argsJSON string) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool handler panicked: %v", r)
		}
	}()
	return handler(ctx, rc, argsJSON)
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

// buildInitialRequestWithDynamic constructs the initial request including dynamic system prompts.
func (a *Agent[T]) buildInitialRequestWithDynamic(ctx context.Context, prompt string, state *agentRunState, deps any, initialRequestParts []ModelRequestPart) (ModelRequest, error) {
	var parts []ModelRequestPart

	// Add static system prompts.
	for _, sp := range a.systemPrompts {
		parts = append(parts, SystemPromptPart{
			Content:   sp,
			Timestamp: time.Now(),
		})
	}

	// Add dynamic system prompts with caching. When a dynamic prompt
	// returns the same content as the previous invocation, reuse the
	// cached string. This ensures the serialized bytes are identical
	// across turns, improving provider-level prompt cache hit rates.
	//
	// We call user-provided callbacks outside the mutex to avoid holding
	// the lock during potentially slow I/O or deadlocking if the callback
	// tries to access the agent.
	type dynResult struct {
		index   int
		content string
	}
	var dynResults []dynResult
	for i, fn := range a.dynamicSystemPrompts {
		rc := a.buildRunContext(state, deps, prompt)
		content, err := fn(ctx, rc)
		if err != nil {
			return ModelRequest{}, fmt.Errorf("dynamic system prompt failed: %w", err)
		}
		if content != "" {
			dynResults = append(dynResults, dynResult{index: i, content: content})
		}
	}
	a.dynamicPromptCacheMu.Lock()
	if a.dynamicPromptCache == nil {
		a.dynamicPromptCache = make(map[int]string)
	}
	for _, dr := range dynResults {
		content := dr.content
		if cached, ok := a.dynamicPromptCache[dr.index]; ok && cached == content {
			content = cached
		} else {
			a.dynamicPromptCache[dr.index] = content
		}
		parts = append(parts, SystemPromptPart{
			Content:   content,
			Timestamp: time.Now(),
		})
	}
	a.dynamicPromptCacheMu.Unlock()

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
	parts = append(parts, initialRequestParts...)

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
	for _, ts := range a.toolsets {
		for _, t := range ts.Tools {
			names = append(names, t.Definition.Name)
		}
	}
	for _, ot := range a.outputSchema.OutputTools {
		names = append(names, ot.Name)
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

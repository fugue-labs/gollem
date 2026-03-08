package otel

import (
	"context"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/core"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// runSpanState holds mutable span state across hooks within a single agent run.
// It is stored in a global map keyed by RunID so that hooks (which cannot return
// modified contexts) can share span state across the lifecycle of a single run.
type runSpanState struct {
	mu            sync.Mutex
	rootSpan      trace.Span
	turnSpan      trace.Span
	modelSpan     trace.Span
	toolSpans     map[string]toolSpanInfo
	turnNumber    int
	modelReqStart time.Time
	cfg           *tracingConfig
	tracer        trace.Tracer

	// childSpanCtxs retains tool span references for child agent lookup.
	// Unlike toolSpans (which is cleaned up on OnToolEnd), this map
	// persists until OnRunEnd so that async child agents (e.g., teammates
	// spawned via spawn_teammate) can discover their parent's tool span
	// even after the tool call has returned.
	childSpanCtxs map[string]trace.Span
}

// TracingHooks returns a core.Hook that creates OTEL spans for the full
// agent execution lifecycle. Attach it to any agent via core.WithHooks.
//
// The returned hook creates hierarchical spans:
//
//	agent.run (root)
//	├── agent.turn[N]
//	│   ├── context.compaction.{strategy}
//	│   ├── model.request
//	│   └── tool.execute.{name}
//	│       └── agent.run (child — delegate, teammate, AgentTool)
//	├── guardrail.{name}
//	└── output.validation
//
// Child agents spawned via tool calls (delegate, spawn_teammate, AgentTool,
// Handoff) automatically nest under the parent's tool span. The core agent
// framework injects RunID and ToolCallID into the context, allowing
// onRunStart to discover the parent's active tool span and establish the
// parent-child relationship — even for async children like teammates.
func TracingHooks(opts ...TracingOption) core.Hook {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	var tp trace.TracerProvider
	if cfg.tracerProvider != nil {
		tp = cfg.tracerProvider
	} else {
		tp = otel.GetTracerProvider()
	}
	tracer := tp.Tracer(tracingInstrumentationName)

	return core.Hook{
		OnRunStart:            onRunStart(tracer, cfg),
		OnRunEnd:              onRunEnd(cfg),
		OnTurnStart:           onTurnStart(cfg),
		OnTurnEnd:             onTurnEnd(cfg),
		OnModelRequest:        onModelRequest(cfg),
		OnModelResponse:       onModelResponse(cfg),
		OnToolStart:           onToolStart(tracer, cfg),
		OnToolEnd:             onToolEnd(cfg),
		OnGuardrailEvaluated:  onGuardrailEvaluated(tracer, cfg),
		OnOutputValidation:    onOutputValidation(tracer, cfg),
		OnOutputRepair:        onOutputRepair(tracer, cfg),
		OnRunConditionChecked: onRunConditionChecked(tracer, cfg),
		OnContextCompaction:   onContextCompaction(tracer, cfg),
	}
}

func (c *tracingConfig) spanName(name string) string {
	if c.spanNamePrefix != "" {
		return c.spanNamePrefix + "." + name
	}
	return name
}

func (c *tracingConfig) truncate(s string) string {
	if c.maxAttributeLength > 0 && len(s) > c.maxAttributeLength {
		return s[:c.maxAttributeLength]
	}
	return s
}

// onRunStart creates the root agent.run span. If this agent was spawned by
// another agent (via a tool like spawn_teammate, delegate, or AgentTool),
// the span is created as a child of the parent agent's tool span, producing
// a connected trace tree across the full multi-agent hierarchy.
func onRunStart(tracer trace.Tracer, cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, prompt string) {
	return func(ctx context.Context, rc *core.RunContext, prompt string) {
		// Check if this agent was spawned from within another agent's tool
		// execution. The core agent framework injects RunID and ToolCallID
		// into the context, so we can look up the parent's active tool span.
		parentCtx := ctx
		if parentRunID := core.RunIDFromContext(ctx); parentRunID != "" && parentRunID != rc.RunID {
			if toolCallID := core.ToolCallIDFromContext(ctx); toolCallID != "" {
				if parentState := loadRunState(parentRunID); parentState != nil {
					parentState.mu.Lock()
					parentSpan := parentState.childSpanCtxs[toolCallID]
					parentState.mu.Unlock()
					if parentSpan != nil {
						parentCtx = trace.ContextWithSpan(ctx, parentSpan)
					}
				}
			}
		}

		_, span := tracer.Start(parentCtx, cfg.spanName(SpanAgentRun),
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(
				attribute.String(AttrAgentRunID, rc.RunID),
				attribute.String(AttrAgentPrompt, cfg.truncate(prompt)),
			),
		)

		state := &runSpanState{
			rootSpan:      span,
			cfg:           cfg,
			tracer:        tracer,
			toolSpans:     make(map[string]toolSpanInfo),
			childSpanCtxs: make(map[string]trace.Span),
		}

		// We can't modify the context from hooks (hooks don't return context),
		// so we store span state using the RunContext's RunID as a lookup key.
		// The state is stored in a global map keyed by RunID.
		storeRunState(rc.RunID, state)
	}
}

// onRunEnd ends the root span with final attributes.
func onRunEnd(cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, messages []core.ModelMessage, err error) {
	return func(ctx context.Context, rc *core.RunContext, messages []core.ModelMessage, err error) {
		state := loadRunState(rc.RunID)
		if state == nil {
			return
		}
		defer deleteRunState(rc.RunID)

		state.mu.Lock()
		defer state.mu.Unlock()

		span := state.rootSpan
		if span == nil {
			return
		}

		// Set final attributes.
		duration := time.Since(rc.RunStartTime)
		span.SetAttributes(
			attribute.Int(AttrAgentTurns, state.turnNumber),
			attribute.Int64(AttrAgentRunDuration, duration.Milliseconds()),
			attribute.Int(AttrTotalInputTokens, rc.Usage.InputTokens),
			attribute.Int(AttrTotalOutputTokens, rc.Usage.OutputTokens),
			attribute.Int(AttrTotalRequests, rc.Usage.Requests),
			attribute.Int(AttrTotalToolCalls, rc.Usage.ToolCalls),
		)

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	}
}

// onTurnStart creates a child span for each turn.
func onTurnStart(cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, turnNumber int) {
	return func(ctx context.Context, rc *core.RunContext, turnNumber int) {
		state := loadRunState(rc.RunID)
		if state == nil {
			return
		}

		state.mu.Lock()
		defer state.mu.Unlock()

		state.turnNumber = turnNumber

		// Create turn span as child of root span.
		if state.rootSpan != nil {
			turnCtx := trace.ContextWithSpan(ctx, state.rootSpan)
			_, turnSpan := state.tracer.Start(turnCtx, cfg.spanName(SpanAgentTurn),
				trace.WithAttributes(
					attribute.Int(AttrTurnNumber, turnNumber),
				),
			)
			state.turnSpan = turnSpan
		}
	}
}

// onTurnEnd ends the turn span.
func onTurnEnd(cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, turnNumber int, resp *core.ModelResponse) {
	return func(ctx context.Context, rc *core.RunContext, turnNumber int, resp *core.ModelResponse) {
		state := loadRunState(rc.RunID)
		if state == nil {
			return
		}

		state.mu.Lock()
		defer state.mu.Unlock()

		if state.turnSpan != nil {
			state.turnSpan.End()
			state.turnSpan = nil
		}
	}
}

// onModelRequest starts a model.request span.
func onModelRequest(cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, messages []core.ModelMessage) {
	return func(ctx context.Context, rc *core.RunContext, messages []core.ModelMessage) {
		state := loadRunState(rc.RunID)
		if state == nil {
			return
		}

		state.mu.Lock()
		defer state.mu.Unlock()

		state.modelReqStart = time.Now()

		// Create model span as child of turn span (or root if no turn).
		parentSpan := state.turnSpan
		if parentSpan == nil {
			parentSpan = state.rootSpan
		}
		if parentSpan == nil {
			return
		}

		parentCtx := trace.ContextWithSpan(ctx, parentSpan)
		attrs := []attribute.KeyValue{
			attribute.Int(AttrMessageCount, len(messages)),
		}

		_, modelSpan := state.tracer.Start(parentCtx, cfg.spanName(SpanModelRequest),
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(attrs...),
		)
		state.modelSpan = modelSpan
	}
}

// onModelResponse ends the model.request span with response attributes.
func onModelResponse(cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, resp *core.ModelResponse) {
	return func(ctx context.Context, rc *core.RunContext, resp *core.ModelResponse) {
		state := loadRunState(rc.RunID)
		if state == nil {
			return
		}

		state.mu.Lock()
		defer state.mu.Unlock()

		if state.modelSpan == nil {
			return
		}

		duration := time.Since(state.modelReqStart)
		state.modelSpan.SetAttributes(
			attribute.String(AttrModelName, resp.ModelName),
			attribute.Int(AttrInputTokens, resp.Usage.InputTokens),
			attribute.Int(AttrOutputTokens, resp.Usage.OutputTokens),
			attribute.String(AttrFinishReason, string(resp.FinishReason)),
			attribute.Int64(AttrToolDuration, duration.Milliseconds()),
		)

		// Record tool call names as an event if present.
		toolCalls := resp.ToolCalls()
		if len(toolCalls) > 0 {
			names := make([]string, len(toolCalls))
			for i, tc := range toolCalls {
				names[i] = tc.ToolName
			}
			state.modelSpan.SetAttributes(
				attribute.StringSlice("gen_ai.response.tool_calls", names),
			)
		}

		state.modelSpan.SetStatus(codes.Ok, "")
		state.modelSpan.End()
		state.modelSpan = nil
	}
}

// onToolStart creates a tool.execute span.
func onToolStart(tracer trace.Tracer, cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, toolCallID string, toolName string, argsJSON string) {
	return func(ctx context.Context, rc *core.RunContext, toolCallID string, toolName string, argsJSON string) {
		state := loadRunState(rc.RunID)
		if state == nil {
			return
		}

		state.mu.Lock()
		defer state.mu.Unlock()

		// Create tool span as child of turn span.
		parentSpan := state.turnSpan
		if parentSpan == nil {
			parentSpan = state.rootSpan
		}
		if parentSpan == nil {
			return
		}

		parentCtx := trace.ContextWithSpan(ctx, parentSpan)
		attrs := []attribute.KeyValue{
			attribute.String(AttrToolName, toolName),
		}
		if cfg.captureToolArgs {
			attrs = append(attrs, attribute.String(AttrToolArgs, cfg.truncate(argsJSON)))
		}

		_, toolSpan := tracer.Start(parentCtx, cfg.spanName(SpanToolExecute+"."+toolName),
			trace.WithAttributes(attrs...),
		)

		// Store tool spans keyed by toolCallID in a map on the state.
		state.toolSpans[toolCallID] = toolSpanInfo{
			span:  toolSpan,
			start: time.Now(),
		}

		// Also store in childSpanCtxs for child agent lookup. This map
		// persists until OnRunEnd, unlike toolSpans which is cleaned up
		// in OnToolEnd — necessary for async children (teammates).
		state.childSpanCtxs[toolCallID] = toolSpan
	}
}

// onToolEnd ends the tool.execute span.
func onToolEnd(cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, toolCallID string, toolName string, result string, err error) {
	return func(ctx context.Context, rc *core.RunContext, toolCallID string, toolName string, result string, err error) {
		state := loadRunState(rc.RunID)
		if state == nil {
			return
		}

		state.mu.Lock()
		defer state.mu.Unlock()

		info, ok := state.toolSpans[toolCallID]
		if !ok {
			return
		}
		delete(state.toolSpans, rc.ToolCallID)

		duration := time.Since(info.start)
		info.span.SetAttributes(
			attribute.Int64(AttrToolDuration, duration.Milliseconds()),
		)

		if cfg.captureToolResults && result != "" {
			info.span.SetAttributes(
				attribute.String(AttrToolResult, cfg.truncate(result)),
			)
		}

		if err != nil {
			info.span.RecordError(err)
			info.span.SetStatus(codes.Error, err.Error())
			info.span.SetAttributes(attribute.String(AttrToolError, err.Error()))
		} else {
			info.span.SetStatus(codes.Ok, "")
		}

		info.span.End()
	}
}

// onGuardrailEvaluated creates a span for guardrail evaluation.
func onGuardrailEvaluated(tracer trace.Tracer, cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, name string, passed bool, err error) {
	return func(ctx context.Context, rc *core.RunContext, name string, passed bool, err error) {
		state := loadRunState(rc.RunID)

		// For guardrails that fire before OnRunStart (input guardrails),
		// state may be nil. Create a standalone span in that case.
		var parentCtx context.Context
		if state != nil {
			state.mu.Lock()
			parent := state.rootSpan
			state.mu.Unlock()
			if parent != nil {
				parentCtx = trace.ContextWithSpan(ctx, parent)
			} else {
				parentCtx = ctx
			}
		} else {
			parentCtx = ctx
		}

		_, span := tracer.Start(parentCtx, cfg.spanName(SpanGuardrail+"."+name),
			trace.WithAttributes(
				attribute.String(AttrGuardrailName, name),
				attribute.Bool(AttrGuardrailPassed, passed),
			),
		)
		defer span.End()
		if err != nil {
			span.SetAttributes(attribute.String(AttrGuardrailError, err.Error()))
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
		}
	}
}

// onOutputValidation creates a span for output validation.
func onOutputValidation(tracer trace.Tracer, cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, passed bool, err error) {
	return func(ctx context.Context, rc *core.RunContext, passed bool, err error) {
		state := loadRunState(rc.RunID)
		if state == nil {
			return
		}

		state.mu.Lock()
		parent := state.turnSpan
		if parent == nil {
			parent = state.rootSpan
		}
		state.mu.Unlock()

		if parent == nil {
			return
		}

		parentCtx := trace.ContextWithSpan(ctx, parent)
		_, span := tracer.Start(parentCtx, cfg.spanName(SpanOutputValidation),
			trace.WithAttributes(
				attribute.Bool(AttrOutputValid, passed),
			),
		)
		defer span.End()
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
		}
	}
}

// onOutputRepair creates a span for output repair attempts.
func onOutputRepair(tracer trace.Tracer, cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, succeeded bool, err error) {
	return func(ctx context.Context, rc *core.RunContext, succeeded bool, err error) {
		state := loadRunState(rc.RunID)
		if state == nil {
			return
		}

		state.mu.Lock()
		parent := state.turnSpan
		if parent == nil {
			parent = state.rootSpan
		}
		state.mu.Unlock()

		if parent == nil {
			return
		}

		parentCtx := trace.ContextWithSpan(ctx, parent)
		_, span := tracer.Start(parentCtx, cfg.spanName(SpanOutputRepair),
			trace.WithAttributes(
				attribute.Bool(AttrOutputRepaired, succeeded),
			),
		)
		defer span.End()
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
		}
	}
}

// onRunConditionChecked creates a span when a run condition stops the run.
func onRunConditionChecked(tracer trace.Tracer, cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, stopped bool, reason string) {
	return func(ctx context.Context, rc *core.RunContext, stopped bool, reason string) {
		if !stopped {
			return // Only create spans for conditions that actually stopped the run.
		}

		state := loadRunState(rc.RunID)
		if state == nil {
			return
		}

		state.mu.Lock()
		parent := state.turnSpan
		if parent == nil {
			parent = state.rootSpan
		}
		state.mu.Unlock()

		if parent == nil {
			return
		}

		parentCtx := trace.ContextWithSpan(ctx, parent)
		_, span := tracer.Start(parentCtx, cfg.spanName(SpanRunCondition),
			trace.WithAttributes(
				attribute.Bool(AttrRunConditionStopped, stopped),
				attribute.String(AttrRunConditionReason, reason),
			),
		)
		defer span.End()
		span.SetStatus(codes.Ok, "")
	}
}

// onContextCompaction creates a span when the agent's context window is compressed.
func onContextCompaction(tracer trace.Tracer, cfg *tracingConfig) func(ctx context.Context, rc *core.RunContext, stats core.ContextCompactionStats) {
	return func(ctx context.Context, rc *core.RunContext, stats core.ContextCompactionStats) {
		state := loadRunState(rc.RunID)
		if state == nil {
			return
		}

		state.mu.Lock()
		parent := state.turnSpan
		if parent == nil {
			parent = state.rootSpan
		}
		state.mu.Unlock()

		if parent == nil {
			return
		}

		parentCtx := trace.ContextWithSpan(ctx, parent)
		_, span := tracer.Start(parentCtx, cfg.spanName(SpanContextCompaction+"."+stats.Strategy),
			trace.WithAttributes(
				attribute.String(AttrCompactionStrategy, stats.Strategy),
				attribute.Int(AttrCompactionMsgsBefore, stats.MessagesBefore),
				attribute.Int(AttrCompactionMsgsAfter, stats.MessagesAfter),
				attribute.Int(AttrCompactionTokensBefore, stats.EstimatedTokensBefore),
				attribute.Int(AttrCompactionTokensAfter, stats.EstimatedTokensAfter),
			),
		)
		defer span.End()
		span.SetStatus(codes.Ok, "")
	}
}

// toolSpanInfo tracks an in-flight tool span.
type toolSpanInfo struct {
	span  trace.Span
	start time.Time
}

// Global run state map — allows hooks (which can't return modified contexts)
// to share span state across the lifecycle of a single run.
//
// Entries are cleaned up in OnRunEnd, which the core agent framework always
// invokes via defer in Agent.Run/RunStream. If a panic bypasses the defer,
// the entry will leak — but that is an exceptional case.
var (
	runStateMu sync.RWMutex
	runStates  = make(map[string]*runSpanState)
)

func storeRunState(runID string, state *runSpanState) {
	runStateMu.Lock()
	runStates[runID] = state
	runStateMu.Unlock()
}

func loadRunState(runID string) *runSpanState {
	runStateMu.RLock()
	state := runStates[runID]
	runStateMu.RUnlock()
	return state
}

func deleteRunState(runID string) {
	runStateMu.Lock()
	delete(runStates, runID)
	runStateMu.Unlock()
}

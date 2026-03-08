package otel

import (
	"context"
	"errors"
	"testing"

	"github.com/fugue-labs/gollem/core"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func setupTracing(t *testing.T, opts ...TracingOption) (core.Hook, *tracetest.InMemoryExporter) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)

	allOpts := append([]TracingOption{WithTracerProvider(tp)}, opts...)
	hook := TracingHooks(allOpts...)
	return hook, exporter
}

func TestTracingHooksRunLifecycle(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-1",
	}

	// Simulate a complete run lifecycle.
	hook.OnRunStart(ctx, rc, "hello world")
	hook.OnTurnStart(ctx, rc, 1)
	hook.OnModelRequest(ctx, rc, nil)
	hook.OnModelResponse(ctx, rc, &core.ModelResponse{
		ModelName:    "claude-sonnet",
		Usage:        core.Usage{InputTokens: 100, OutputTokens: 50},
		FinishReason: core.FinishReasonStop,
		Parts:        []core.ModelResponsePart{core.TextPart{Content: "hello"}},
	})
	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected spans to be created")
	}

	// Check that we have the expected span types.
	spanNames := make(map[string]bool)
	for _, s := range spans {
		spanNames[s.Name] = true
	}

	expectedSpans := []string{
		SpanAgentRun,
		SpanAgentTurn,
		SpanModelRequest,
	}
	for _, name := range expectedSpans {
		if !spanNames[name] {
			t.Errorf("expected span %q to exist, got spans: %v", name, spanNames)
		}
	}
}

func TestTracingHooksToolExecution(t *testing.T) {
	hook, exporter := setupTracing(t, WithCaptureToolArgs(true), WithCaptureToolResults(true))

	ctx := context.Background()
	rc := &core.RunContext{
		RunID:      "test-run-2",
		ToolCallID: "call_1",
		ToolName:   "search",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnTurnStart(ctx, rc, 1)
	hook.OnToolStart(ctx, rc, "call_1", "search", `{"query": "test"}`)
	hook.OnToolEnd(ctx, rc, "call_1", "search", `{"results": []}`, nil)
	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()

	// Find the tool span.
	var toolSpanFound bool
	for _, s := range spans {
		if s.Name == SpanToolExecute+".search" {
			toolSpanFound = true
			// Check attributes.
			attrMap := spanAttrs(s)
			if v, ok := attrMap[AttrToolName]; !ok || v != "search" {
				t.Errorf("expected tool name 'search', got %v", v)
			}
			if v, ok := attrMap[AttrToolArgs]; !ok || v != `{"query": "test"}` {
				t.Errorf("expected tool args, got %v", v)
			}
			if v, ok := attrMap[AttrToolResult]; !ok || v != `{"results": []}` {
				t.Errorf("expected tool result, got %v", v)
			}
		}
	}
	if !toolSpanFound {
		t.Error("expected tool.execute.search span")
	}
}

func TestTracingHooksToolArgsCapturedByDefault(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID:      "test-run-3",
		ToolCallID: "call_1",
		ToolName:   "search",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnTurnStart(ctx, rc, 1)
	hook.OnToolStart(ctx, rc, "call_1", "search", `{"query": "test"}`)
	hook.OnToolEnd(ctx, rc, "call_1", "search", `result`, nil)
	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	for _, s := range spans {
		if s.Name == SpanToolExecute+".search" {
			attrMap := spanAttrs(s)
			if _, ok := attrMap[AttrToolArgs]; !ok {
				t.Error("tool args should be captured by default")
			}
			// Results are still off by default.
			if _, ok := attrMap[AttrToolResult]; ok {
				t.Error("tool results should not be captured by default")
			}
		}
	}
}

func TestTracingHooksToolError(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID:      "test-run-4",
		ToolCallID: "call_1",
		ToolName:   "fetch",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnTurnStart(ctx, rc, 1)
	hook.OnToolStart(ctx, rc, "call_1", "fetch", `{}`)
	hook.OnToolEnd(ctx, rc, "call_1", "fetch", "", errors.New("timeout"))
	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	for _, s := range spans {
		if s.Name == SpanToolExecute+".fetch" {
			attrMap := spanAttrs(s)
			if v, ok := attrMap[AttrToolError]; !ok || v != "timeout" {
				t.Errorf("expected tool error 'timeout', got %v", v)
			}
		}
	}
}

func TestTracingHooksRunError(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-5",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnRunEnd(ctx, rc, nil, errors.New("model failed"))

	spans := exporter.GetSpans()
	var rootFound bool
	for _, s := range spans {
		if s.Name == SpanAgentRun {
			rootFound = true
			if len(s.Events) == 0 {
				t.Error("expected error event on root span")
			}
		}
	}
	if !rootFound {
		t.Error("expected agent.run span")
	}
}

func TestTracingHooksGuardrail(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-6",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnGuardrailEvaluated(ctx, rc, "max_length", true, nil)
	hook.OnGuardrailEvaluated(ctx, rc, "content_filter", false, errors.New("blocked content"))
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	guardrailSpans := 0
	for _, s := range spans {
		if s.Name == SpanGuardrail+".max_length" || s.Name == SpanGuardrail+".content_filter" {
			guardrailSpans++
		}
	}
	if guardrailSpans != 2 {
		t.Errorf("expected 2 guardrail spans, got %d", guardrailSpans)
	}
}

func TestTracingHooksOutputValidation(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-7",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnTurnStart(ctx, rc, 1)
	hook.OnOutputValidation(ctx, rc, true, nil)
	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	var validationFound bool
	for _, s := range spans {
		if s.Name == SpanOutputValidation {
			validationFound = true
			attrMap := spanAttrs(s)
			if v, ok := attrMap[AttrOutputValid]; !ok || v != true {
				t.Errorf("expected output.valid=true, got %v", v)
			}
		}
	}
	if !validationFound {
		t.Error("expected output.validation span")
	}
}

func TestTracingHooksOutputRepair(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-8",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnTurnStart(ctx, rc, 1)
	hook.OnOutputRepair(ctx, rc, true, nil)
	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	var repairFound bool
	for _, s := range spans {
		if s.Name == SpanOutputRepair {
			repairFound = true
			attrMap := spanAttrs(s)
			if v, ok := attrMap[AttrOutputRepaired]; !ok || v != true {
				t.Errorf("expected output.repaired=true, got %v", v)
			}
		}
	}
	if !repairFound {
		t.Error("expected output.repair span")
	}
}

func TestTracingHooksRunCondition(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-9",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnTurnStart(ctx, rc, 1)
	hook.OnRunConditionChecked(ctx, rc, true, "max duration exceeded")
	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	var condFound bool
	for _, s := range spans {
		if s.Name == SpanRunCondition {
			condFound = true
			attrMap := spanAttrs(s)
			if v, ok := attrMap[AttrRunConditionReason]; !ok || v != "max duration exceeded" {
				t.Errorf("expected reason 'max duration exceeded', got %v", v)
			}
		}
	}
	if !condFound {
		t.Error("expected run_condition span")
	}
}

func TestTracingHooksRunConditionNotFiredForContinue(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-10",
	}

	hook.OnRunStart(ctx, rc, "test")
	// stopped=false should not create a span
	hook.OnRunConditionChecked(ctx, rc, false, "")
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	for _, s := range spans {
		if s.Name == SpanRunCondition {
			t.Error("run_condition span should not be created when stopped=false")
		}
	}
}

func TestTracingHooksSpanNamePrefix(t *testing.T) {
	hook, exporter := setupTracing(t, WithSpanNamePrefix("myapp"))

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-11",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected spans")
	}
	if spans[0].Name != "myapp."+SpanAgentRun {
		t.Errorf("expected prefixed span name, got %q", spans[0].Name)
	}
}

func TestTracingHooksMultipleTurns(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-12",
		Usage: core.RunUsage{
			Usage:    core.Usage{InputTokens: 200, OutputTokens: 100},
			Requests: 2,
		},
	}

	hook.OnRunStart(ctx, rc, "multi-turn test")

	// Turn 1
	hook.OnTurnStart(ctx, rc, 1)
	hook.OnModelRequest(ctx, rc, nil)
	hook.OnModelResponse(ctx, rc, &core.ModelResponse{
		ModelName:    "claude-sonnet",
		Usage:        core.Usage{InputTokens: 100, OutputTokens: 50},
		FinishReason: core.FinishReasonToolCall,
		Parts:        []core.ModelResponsePart{core.ToolCallPart{ToolName: "search", ToolCallID: "call_1"}},
	})
	rc.ToolCallID = "call_1"
	hook.OnToolStart(ctx, rc, "call_1", "search", `{"q":"test"}`)
	hook.OnToolEnd(ctx, rc, "call_1", "search", "result", nil)
	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})

	// Turn 2
	hook.OnTurnStart(ctx, rc, 2)
	hook.OnModelRequest(ctx, rc, nil)
	hook.OnModelResponse(ctx, rc, &core.ModelResponse{
		ModelName:    "claude-sonnet",
		Usage:        core.Usage{InputTokens: 100, OutputTokens: 50},
		FinishReason: core.FinishReasonStop,
		Parts:        []core.ModelResponsePart{core.TextPart{Content: "done"}},
	})
	hook.OnTurnEnd(ctx, rc, 2, &core.ModelResponse{})

	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	spanNames := make(map[string]int)
	for _, s := range spans {
		spanNames[s.Name]++
	}

	if spanNames[SpanAgentRun] != 1 {
		t.Errorf("expected 1 agent.run span, got %d", spanNames[SpanAgentRun])
	}
	if spanNames[SpanAgentTurn] != 2 {
		t.Errorf("expected 2 agent.turn spans, got %d", spanNames[SpanAgentTurn])
	}
	if spanNames[SpanModelRequest] != 2 {
		t.Errorf("expected 2 model.request spans, got %d", spanNames[SpanModelRequest])
	}
	if spanNames[SpanToolExecute+".search"] != 1 {
		t.Errorf("expected 1 tool.execute.search span, got %d", spanNames[SpanToolExecute+".search"])
	}
}

func TestTracingHooksSpanHierarchy(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID:      "test-run-13",
		ToolCallID: "call_1",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnTurnStart(ctx, rc, 1)
	hook.OnModelRequest(ctx, rc, nil)
	hook.OnModelResponse(ctx, rc, &core.ModelResponse{
		ModelName: "test",
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "ok"}},
	})
	hook.OnToolStart(ctx, rc, "call_1", "search", `{}`)
	hook.OnToolEnd(ctx, rc, "call_1", "search", "result", nil)
	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()

	// Build a map of span ID -> parent span ID.
	spanByID := make(map[string]tracetest.SpanStub)
	for _, s := range spans {
		spanByID[s.SpanContext.SpanID().String()] = s
	}

	// Find root span (agent.run).
	var rootSpanID string
	for _, s := range spans {
		if s.Name == SpanAgentRun {
			rootSpanID = s.SpanContext.SpanID().String()
			break
		}
	}

	// Turn span should be child of root.
	for _, s := range spans {
		if s.Name == SpanAgentTurn {
			if s.Parent.SpanID().String() != rootSpanID {
				t.Errorf("turn span should be child of root span")
			}
		}
	}

	// Model and tool spans should be children of turn span.
	var turnSpanID string
	for _, s := range spans {
		if s.Name == SpanAgentTurn {
			turnSpanID = s.SpanContext.SpanID().String()
			break
		}
	}

	for _, s := range spans {
		if s.Name == SpanModelRequest || s.Name == SpanToolExecute+".search" {
			if s.Parent.SpanID().String() != turnSpanID {
				t.Errorf("span %q should be child of turn span", s.Name)
			}
		}
	}
}

func TestTracingHooksTruncation(t *testing.T) {
	hook, exporter := setupTracing(t, WithMaxAttributeLength(10))

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-14",
	}

	longPrompt := "this is a very long prompt that should be truncated"
	hook.OnRunStart(ctx, rc, longPrompt)
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	for _, s := range spans {
		if s.Name == SpanAgentRun {
			attrMap := spanAttrs(s)
			if v, ok := attrMap[AttrAgentPrompt]; ok {
				if str, ok := v.(string); ok && len(str) > 10 {
					t.Errorf("prompt should be truncated to 10 chars, got %d", len(str))
				}
			}
		}
	}
}

func TestTracingHooksRunStateCleanup(t *testing.T) {
	hook, _ := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-cleanup",
	}

	hook.OnRunStart(ctx, rc, "test")
	if loadRunState(rc.RunID) == nil {
		t.Fatal("expected run state to exist")
	}

	hook.OnRunEnd(ctx, rc, nil, nil)
	if loadRunState(rc.RunID) != nil {
		t.Fatal("expected run state to be cleaned up")
	}
}

func TestTracingHooksModelResponseAttributes(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "test-run-15",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnTurnStart(ctx, rc, 1)
	hook.OnModelRequest(ctx, rc, nil)
	hook.OnModelResponse(ctx, rc, &core.ModelResponse{
		ModelName:    "claude-opus",
		Usage:        core.Usage{InputTokens: 500, OutputTokens: 200},
		FinishReason: core.FinishReasonStop,
		Parts:        []core.ModelResponsePart{core.TextPart{Content: "response"}},
	})
	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	for _, s := range spans {
		if s.Name == SpanModelRequest {
			attrMap := spanAttrs(s)
			if v, ok := attrMap[AttrModelName]; !ok || v != "claude-opus" {
				t.Errorf("expected model name 'claude-opus', got %v", v)
			}
			if v, ok := attrMap[AttrInputTokens]; !ok || v != int64(500) {
				t.Errorf("expected input tokens 500, got %v", v)
			}
			if v, ok := attrMap[AttrOutputTokens]; !ok || v != int64(200) {
				t.Errorf("expected output tokens 200, got %v", v)
			}
			if v, ok := attrMap[AttrFinishReason]; !ok || v != "stop" {
				t.Errorf("expected finish reason 'stop', got %v", v)
			}
		}
	}
}

// TestTracingHooksCrossAgentHierarchy verifies that a child agent's root span
// is automatically nested under the parent agent's tool span when context
// carries RunID and ToolCallID (simulating delegate, AgentTool, or teammate).
func TestTracingHooksCrossAgentHierarchy(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()

	// --- Parent agent run ---
	parentRC := &core.RunContext{
		RunID:      "parent-run",
		ToolCallID: "tool-call-delegate",
	}

	hook.OnRunStart(ctx, parentRC, "orchestrate tasks")
	hook.OnTurnStart(ctx, parentRC, 1)
	hook.OnToolStart(ctx, parentRC, "tool-call-delegate", "delegate", `{"task":"sub work"}`)

	// Simulate the context that the core agent framework injects before
	// executing the tool handler: RunID from Run(), ToolCallID before handler.
	childCtx := core.ContextWithRunID(ctx, "parent-run")
	childCtx = core.ContextWithToolCallID(childCtx, "tool-call-delegate")

	// --- Child agent run (synchronous, like delegate/AgentTool) ---
	childRC := &core.RunContext{
		RunID: "child-run",
	}
	hook.OnRunStart(childCtx, childRC, "sub work")
	hook.OnTurnStart(childCtx, childRC, 1)
	hook.OnModelRequest(childCtx, childRC, nil)
	hook.OnModelResponse(childCtx, childRC, &core.ModelResponse{
		ModelName: "test",
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "done"}},
	})
	hook.OnTurnEnd(childCtx, childRC, 1, &core.ModelResponse{})
	hook.OnRunEnd(childCtx, childRC, nil, nil)

	// --- Parent continues ---
	hook.OnToolEnd(ctx, parentRC, "tool-call-delegate", "delegate", "done", nil)
	hook.OnTurnEnd(ctx, parentRC, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, parentRC, nil, nil)

	spans := exporter.GetSpans()

	// Find the parent's tool span and the child's root span.
	var parentToolSpanID, childRootParentID string
	for _, s := range spans {
		if s.Name == SpanToolExecute+".delegate" {
			parentToolSpanID = s.SpanContext.SpanID().String()
		}
		if s.Name == SpanAgentRun {
			attrs := spanAttrs(s)
			if v, ok := attrs[AttrAgentRunID]; ok && v == "child-run" {
				childRootParentID = s.Parent.SpanID().String()
			}
		}
	}

	if parentToolSpanID == "" {
		t.Fatal("tool.execute.delegate span not found")
	}
	if childRootParentID == "" {
		t.Fatal("child agent.run span not found")
	}
	if childRootParentID != parentToolSpanID {
		t.Errorf("child agent.run should be child of parent's tool span: got parent=%s, want=%s",
			childRootParentID, parentToolSpanID)
	}

	// Verify all spans share the same trace ID.
	traceIDs := make(map[string]bool)
	for _, s := range spans {
		traceIDs[s.SpanContext.TraceID().String()] = true
	}
	if len(traceIDs) != 1 {
		t.Errorf("expected all spans in same trace, got %d distinct trace IDs", len(traceIDs))
	}
}

// TestTracingHooksCrossAgentHierarchyAsync verifies that an async child agent
// (like a teammate) nests under the parent's tool span even after OnToolEnd
// has fired for the spawning tool call.
func TestTracingHooksCrossAgentHierarchyAsync(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()

	// --- Parent agent run ---
	parentRC := &core.RunContext{
		RunID:      "parent-run-async",
		ToolCallID: "tool-call-spawn",
	}

	hook.OnRunStart(ctx, parentRC, "lead team")
	hook.OnTurnStart(ctx, parentRC, 1)
	hook.OnToolStart(ctx, parentRC, "tool-call-spawn", "spawn_teammate", `{"name":"worker"}`)

	// Build the context the child will inherit (injected by core framework).
	childCtx := core.ContextWithRunID(ctx, "parent-run-async")
	childCtx = core.ContextWithToolCallID(childCtx, "tool-call-spawn")

	// spawn_teammate returns immediately — OnToolEnd fires before child runs.
	hook.OnToolEnd(ctx, parentRC, "tool-call-spawn", "spawn_teammate", `{"status":"spawned"}`, nil)
	hook.OnTurnEnd(ctx, parentRC, 1, &core.ModelResponse{})

	// --- Child agent runs later (async goroutine) ---
	childRC := &core.RunContext{
		RunID: "teammate-run",
	}
	hook.OnRunStart(childCtx, childRC, "do subtask")
	hook.OnTurnStart(childCtx, childRC, 1)
	hook.OnModelRequest(childCtx, childRC, nil)
	hook.OnModelResponse(childCtx, childRC, &core.ModelResponse{
		ModelName: "test",
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "done"}},
	})
	hook.OnTurnEnd(childCtx, childRC, 1, &core.ModelResponse{})
	hook.OnRunEnd(childCtx, childRC, nil, nil)

	// Parent finishes after child.
	hook.OnRunEnd(ctx, parentRC, nil, nil)

	spans := exporter.GetSpans()

	// Find the parent's spawn_teammate tool span and the child's root span.
	var parentToolSpanID, childRootParentID string
	for _, s := range spans {
		if s.Name == SpanToolExecute+".spawn_teammate" {
			parentToolSpanID = s.SpanContext.SpanID().String()
		}
		if s.Name == SpanAgentRun {
			attrs := spanAttrs(s)
			if v, ok := attrs[AttrAgentRunID]; ok && v == "teammate-run" {
				childRootParentID = s.Parent.SpanID().String()
			}
		}
	}

	if parentToolSpanID == "" {
		t.Fatal("tool.execute.spawn_teammate span not found")
	}
	if childRootParentID == "" {
		t.Fatal("teammate agent.run span not found")
	}
	if childRootParentID != parentToolSpanID {
		t.Errorf("teammate agent.run should be child of spawn_teammate tool span: got parent=%s, want=%s",
			childRootParentID, parentToolSpanID)
	}

	// All spans should share the same trace ID.
	traceIDs := make(map[string]bool)
	for _, s := range spans {
		traceIDs[s.SpanContext.TraceID().String()] = true
	}
	if len(traceIDs) != 1 {
		t.Errorf("expected all spans in same trace, got %d distinct trace IDs", len(traceIDs))
	}
}

// TestTracingHooksNoParentForTopLevelRun verifies that a top-level agent run
// (not spawned by another agent) creates a root span with no parent.
func TestTracingHooksNoParentForTopLevelRun(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "top-level-run",
	}

	hook.OnRunStart(ctx, rc, "hello")
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()
	for _, s := range spans {
		if s.Name == SpanAgentRun {
			if s.Parent.SpanID().IsValid() {
				t.Errorf("top-level agent.run should have no parent, got %s", s.Parent.SpanID())
			}
			return
		}
	}
	t.Fatal("agent.run span not found")
}

// TestTracingHooksTeammateRerunNoStaleParent verifies that a teammate's
// second run (after wake) does NOT nest under the original spawn_teammate
// tool span when the ToolCallID has been cleared.
func TestTracingHooksTeammateRerunNoStaleParent(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()

	// --- Parent (leader) ---
	parentRC := &core.RunContext{
		RunID:      "leader-run",
		ToolCallID: "spawn-call-1",
	}
	hook.OnRunStart(ctx, parentRC, "lead")
	hook.OnTurnStart(ctx, parentRC, 1)
	hook.OnToolStart(ctx, parentRC, "spawn-call-1", "spawn_teammate", `{"name":"w"}`)

	// First run context (has both RunID and ToolCallID).
	firstCtx := core.ContextWithRunID(ctx, "leader-run")
	firstCtx = core.ContextWithToolCallID(firstCtx, "spawn-call-1")

	hook.OnToolEnd(ctx, parentRC, "spawn-call-1", "spawn_teammate", `{"status":"spawned"}`, nil)

	// --- Teammate first run (should nest) ---
	tmRC1 := &core.RunContext{RunID: "tm-run-1"}
	hook.OnRunStart(firstCtx, tmRC1, "first task")
	hook.OnRunEnd(firstCtx, tmRC1, nil, nil)

	// --- Teammate second run (ToolCallID cleared — should NOT nest) ---
	secondCtx := core.ContextWithToolCallID(firstCtx, "")
	tmRC2 := &core.RunContext{RunID: "tm-run-2"}
	hook.OnRunStart(secondCtx, tmRC2, "second task")
	hook.OnRunEnd(secondCtx, tmRC2, nil, nil)

	hook.OnTurnEnd(ctx, parentRC, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, parentRC, nil, nil)

	spans := exporter.GetSpans()

	var spawnToolSpanID string
	var firstRunParentID, secondRunParentID string
	var secondRunHasParent bool

	for _, s := range spans {
		if s.Name == SpanToolExecute+".spawn_teammate" {
			spawnToolSpanID = s.SpanContext.SpanID().String()
		}
		if s.Name == SpanAgentRun {
			attrs := spanAttrs(s)
			switch attrs[AttrAgentRunID] {
			case "tm-run-1":
				firstRunParentID = s.Parent.SpanID().String()
			case "tm-run-2":
				secondRunParentID = s.Parent.SpanID().String()
				secondRunHasParent = s.Parent.SpanID().IsValid()
			}
		}
	}

	// First run should nest under spawn_teammate.
	if firstRunParentID != spawnToolSpanID {
		t.Errorf("first run should be child of spawn_teammate: got %s, want %s",
			firstRunParentID, spawnToolSpanID)
	}

	// Second run should NOT have a valid parent (no stale nesting).
	if secondRunHasParent {
		t.Errorf("second run should be root span, got parent=%s", secondRunParentID)
	}
}

// TestTracingHooksParentDeletedBeforeChild verifies graceful degradation when
// the parent's run state is deleted before the child's onRunStart fires.
func TestTracingHooksParentDeletedBeforeChild(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()

	// Parent starts and ends immediately.
	parentRC := &core.RunContext{
		RunID:      "fast-parent",
		ToolCallID: "tool-1",
	}
	hook.OnRunStart(ctx, parentRC, "fast")
	hook.OnTurnStart(ctx, parentRC, 1)
	hook.OnToolStart(ctx, parentRC, "tool-1", "spawn", `{}`)
	hook.OnToolEnd(ctx, parentRC, "tool-1", "spawn", `{}`, nil)
	hook.OnTurnEnd(ctx, parentRC, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, parentRC, nil, nil) // Deletes parent state

	// Child starts AFTER parent state is gone.
	childCtx := core.ContextWithRunID(ctx, "fast-parent")
	childCtx = core.ContextWithToolCallID(childCtx, "tool-1")
	childRC := &core.RunContext{RunID: "orphan-child"}
	hook.OnRunStart(childCtx, childRC, "orphaned task")
	hook.OnRunEnd(childCtx, childRC, nil, nil)

	spans := exporter.GetSpans()
	for _, s := range spans {
		if s.Name == SpanAgentRun {
			attrs := spanAttrs(s)
			if attrs[AttrAgentRunID] == "orphan-child" {
				// Should be a root span (no parent), not crash.
				if s.Parent.SpanID().IsValid() {
					t.Errorf("orphaned child should be root span, got parent=%s",
						s.Parent.SpanID())
				}
				return
			}
		}
	}
	t.Fatal("orphan-child agent.run span not found")
}

// TestTracingHooksNestedDelegation verifies a 3-level trace hierarchy:
// leader → teammate → delegate (grandchild).
func TestTracingHooksNestedDelegation(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()

	// Level 1: Leader
	leaderRC := &core.RunContext{RunID: "leader", ToolCallID: "spawn-1"}
	hook.OnRunStart(ctx, leaderRC, "lead")
	hook.OnTurnStart(ctx, leaderRC, 1)
	hook.OnToolStart(ctx, leaderRC, "spawn-1", "spawn_teammate", `{}`)

	leaderCtx := core.ContextWithRunID(ctx, "leader")
	leaderCtx = core.ContextWithToolCallID(leaderCtx, "spawn-1")

	hook.OnToolEnd(ctx, leaderRC, "spawn-1", "spawn_teammate", `{}`, nil)

	// Level 2: Teammate (child of leader's spawn)
	tmRC := &core.RunContext{RunID: "teammate", ToolCallID: "delegate-1"}
	hook.OnRunStart(leaderCtx, tmRC, "subtask")
	// After onRunStart, agent.Run sets RunID = "teammate" in ctx for tool handlers
	tmCtx := core.ContextWithRunID(leaderCtx, "teammate")
	hook.OnTurnStart(tmCtx, tmRC, 1)
	hook.OnToolStart(tmCtx, tmRC, "delegate-1", "delegate", `{"task":"deep"}`)

	delegateCtx := core.ContextWithToolCallID(tmCtx, "delegate-1")

	// Level 3: Delegate (grandchild of leader, child of teammate)
	delegateRC := &core.RunContext{RunID: "grandchild"}
	hook.OnRunStart(delegateCtx, delegateRC, "deep work")
	hook.OnRunEnd(delegateCtx, delegateRC, nil, nil)

	hook.OnToolEnd(tmCtx, tmRC, "delegate-1", "delegate", "result", nil)
	hook.OnTurnEnd(tmCtx, tmRC, 1, &core.ModelResponse{})
	hook.OnRunEnd(tmCtx, tmRC, nil, nil)
	hook.OnTurnEnd(ctx, leaderRC, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, leaderRC, nil, nil)

	spans := exporter.GetSpans()

	spanMap := make(map[string]tracetest.SpanStub)
	for _, s := range spans {
		if s.Name == SpanAgentRun {
			attrs := spanAttrs(s)
			if runID, ok := attrs[AttrAgentRunID].(string); ok {
				spanMap[runID] = s
			}
		}
		if s.Name == SpanToolExecute+".spawn_teammate" {
			spanMap["tool:spawn"] = s
		}
		if s.Name == SpanToolExecute+".delegate" {
			spanMap["tool:delegate"] = s
		}
	}

	// Teammate's root should be child of leader's spawn_teammate tool span.
	tmSpan := spanMap["teammate"]
	spawnToolSpan := spanMap["tool:spawn"]
	if tmSpan.Parent.SpanID() != spawnToolSpan.SpanContext.SpanID() {
		t.Errorf("teammate should be child of spawn_teammate tool span")
	}

	// Grandchild's root should be child of teammate's delegate tool span.
	gcSpan := spanMap["grandchild"]
	delegateToolSpan := spanMap["tool:delegate"]
	if gcSpan.Parent.SpanID() != delegateToolSpan.SpanContext.SpanID() {
		t.Errorf("grandchild should be child of delegate tool span")
	}

	// All spans should share the same trace ID.
	traceIDs := make(map[string]bool)
	for _, s := range spans {
		traceIDs[s.SpanContext.TraceID().String()] = true
	}
	if len(traceIDs) != 1 {
		t.Errorf("expected all spans in same trace, got %d distinct trace IDs", len(traceIDs))
	}
}

// TestTracingHooksContextCompaction verifies that context compaction events
// produce spans with before/after token and message count attributes.
func TestTracingHooksContextCompaction(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "compaction-run",
	}

	hook.OnRunStart(ctx, rc, "long task")
	hook.OnTurnStart(ctx, rc, 5)

	// Simulate auto-summary compaction.
	hook.OnContextCompaction(ctx, rc, core.ContextCompactionStats{
		Strategy:              "auto_summary",
		MessagesBefore:        42,
		MessagesAfter:         6,
		EstimatedTokensBefore: 150000,
		EstimatedTokensAfter:  12000,
	})

	hook.OnTurnEnd(ctx, rc, 5, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()

	var compactionFound bool
	for _, s := range spans {
		if s.Name == SpanContextCompaction+".auto_summary" {
			compactionFound = true
			attrs := spanAttrs(s)
			if v := attrs[AttrCompactionStrategy]; v != "auto_summary" {
				t.Errorf("expected strategy 'auto_summary', got %v", v)
			}
			if v := attrs[AttrCompactionMsgsBefore]; v != int64(42) {
				t.Errorf("expected messages_before=42, got %v", v)
			}
			if v := attrs[AttrCompactionMsgsAfter]; v != int64(6) {
				t.Errorf("expected messages_after=6, got %v", v)
			}
			if v := attrs[AttrCompactionTokensBefore]; v != int64(150000) {
				t.Errorf("expected tokens_before=150000, got %v", v)
			}
			if v := attrs[AttrCompactionTokensAfter]; v != int64(12000) {
				t.Errorf("expected tokens_after=12000, got %v", v)
			}
		}
	}
	if !compactionFound {
		t.Error("expected context.compaction.auto_summary span")
	}
}

// TestTracingHooksContextCompactionParentage verifies compaction spans
// are children of the current turn span.
func TestTracingHooksContextCompactionParentage(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "compaction-parent-run",
	}

	hook.OnRunStart(ctx, rc, "test")
	hook.OnTurnStart(ctx, rc, 3)
	hook.OnContextCompaction(ctx, rc, core.ContextCompactionStats{
		Strategy:              "history_processor",
		MessagesBefore:        20,
		MessagesAfter:         20,
		EstimatedTokensBefore: 80000,
		EstimatedTokensAfter:  50000,
	})
	hook.OnTurnEnd(ctx, rc, 3, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()

	var turnSpanID, compactionParentID string
	for _, s := range spans {
		if s.Name == SpanAgentTurn {
			turnSpanID = s.SpanContext.SpanID().String()
		}
		if s.Name == SpanContextCompaction+".history_processor" {
			compactionParentID = s.Parent.SpanID().String()
		}
	}

	if turnSpanID == "" {
		t.Fatal("turn span not found")
	}
	if compactionParentID == "" {
		t.Fatal("compaction span not found")
	}
	if compactionParentID != turnSpanID {
		t.Errorf("compaction span should be child of turn span: got %s, want %s",
			compactionParentID, turnSpanID)
	}
}

// TestTracingHooksEmergencyTruncation verifies that emergency_truncation
// compaction events (from ContextOverflowMiddleware) produce proper spans.
func TestTracingHooksEmergencyTruncation(t *testing.T) {
	hook, exporter := setupTracing(t)

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "emergency-run",
	}

	hook.OnRunStart(ctx, rc, "big task")
	hook.OnTurnStart(ctx, rc, 1)

	// Simulate emergency truncation (what ContextOverflowMiddleware reports).
	hook.OnContextCompaction(ctx, rc, core.ContextCompactionStats{
		Strategy:              "emergency_truncation",
		MessagesBefore:        100,
		MessagesAfter:         8,
		EstimatedTokensBefore: 250000,
		EstimatedTokensAfter:  15000,
	})

	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()

	var found bool
	for _, s := range spans {
		if s.Name == SpanContextCompaction+".emergency_truncation" {
			found = true
			attrs := spanAttrs(s)
			if v := attrs[AttrCompactionStrategy]; v != "emergency_truncation" {
				t.Errorf("expected strategy 'emergency_truncation', got %v", v)
			}
			if v := attrs[AttrCompactionMsgsBefore]; v != int64(100) {
				t.Errorf("expected messages_before=100, got %v", v)
			}
			if v := attrs[AttrCompactionMsgsAfter]; v != int64(8) {
				t.Errorf("expected messages_after=8, got %v", v)
			}
			if v := attrs[AttrCompactionTokensBefore]; v != int64(250000) {
				t.Errorf("expected tokens_before=250000, got %v", v)
			}
			if v := attrs[AttrCompactionTokensAfter]; v != int64(15000) {
				t.Errorf("expected tokens_after=15000, got %v", v)
			}
		}
	}
	if !found {
		t.Error("expected context.compaction.emergency_truncation span")
	}
}

// TestCompactionCallbackContext verifies that CompactionCallbackFromContext
// round-trips correctly and can be used by middleware.
func TestCompactionCallbackContext(t *testing.T) {
	var received core.ContextCompactionStats
	cb := func(stats core.ContextCompactionStats) {
		received = stats
	}

	ctx := core.ContextWithCompactionCallback(context.Background(), cb)
	extracted := core.CompactionCallbackFromContext(ctx)
	if extracted == nil {
		t.Fatal("expected callback from context")
	}

	extracted(core.ContextCompactionStats{
		Strategy:       "emergency_truncation",
		MessagesBefore: 50,
		MessagesAfter:  10,
	})

	if received.Strategy != "emergency_truncation" {
		t.Errorf("expected 'emergency_truncation', got %q", received.Strategy)
	}
	if received.MessagesBefore != 50 || received.MessagesAfter != 10 {
		t.Errorf("unexpected stats: %+v", received)
	}
}

// TestCompactionCallbackContextNil verifies nil return when no callback is set.
func TestCompactionCallbackContextNil(t *testing.T) {
	cb := core.CompactionCallbackFromContext(context.Background())
	if cb != nil {
		t.Error("expected nil callback from empty context")
	}
}

// TestTracingHooksConcurrentToolExecution verifies that concurrent tool
// executions produce distinct spans with correct parent relationships.
func TestTracingHooksConcurrentToolExecution(t *testing.T) {
	hook, exporter := setupTracing(t, WithCaptureToolArgs(true))

	ctx := context.Background()
	rc := &core.RunContext{
		RunID: "concurrent-run",
	}

	hook.OnRunStart(ctx, rc, "parallel tools")
	hook.OnTurnStart(ctx, rc, 1)
	hook.OnModelRequest(ctx, rc, nil)
	hook.OnModelResponse(ctx, rc, &core.ModelResponse{
		ModelName: "test",
		Parts: []core.ModelResponsePart{
			core.ToolCallPart{ToolName: "fetch", ToolCallID: "call-a"},
			core.ToolCallPart{ToolName: "search", ToolCallID: "call-b"},
			core.ToolCallPart{ToolName: "compute", ToolCallID: "call-c"},
		},
	})

	// Start all three tools concurrently.
	rcA := &core.RunContext{RunID: "concurrent-run", ToolCallID: "call-a"}
	rcB := &core.RunContext{RunID: "concurrent-run", ToolCallID: "call-b"}
	rcC := &core.RunContext{RunID: "concurrent-run", ToolCallID: "call-c"}

	hook.OnToolStart(ctx, rcA, "call-a", "fetch", `{"url":"x"}`)
	hook.OnToolStart(ctx, rcB, "call-b", "search", `{"q":"y"}`)
	hook.OnToolStart(ctx, rcC, "call-c", "compute", `{"n":42}`)

	// End in different order than started.
	hook.OnToolEnd(ctx, rcB, "call-b", "search", "results", nil)
	hook.OnToolEnd(ctx, rcC, "call-c", "compute", "84", nil)
	hook.OnToolEnd(ctx, rcA, "call-a", "fetch", "html", nil)

	hook.OnTurnEnd(ctx, rc, 1, &core.ModelResponse{})
	hook.OnRunEnd(ctx, rc, nil, nil)

	spans := exporter.GetSpans()

	// Find the turn span ID.
	var turnSpanID string
	for _, s := range spans {
		if s.Name == SpanAgentTurn {
			turnSpanID = s.SpanContext.SpanID().String()
		}
	}
	if turnSpanID == "" {
		t.Fatal("turn span not found")
	}

	// Verify all three tool spans exist, have distinct IDs, and parent to turn.
	toolSpanNames := make(map[string]bool)
	for _, s := range spans {
		if s.Name == SpanToolExecute+".fetch" ||
			s.Name == SpanToolExecute+".search" ||
			s.Name == SpanToolExecute+".compute" {
			toolSpanNames[s.Name] = true
			if s.Parent.SpanID().String() != turnSpanID {
				t.Errorf("tool span %q should be child of turn span", s.Name)
			}
		}
	}
	if len(toolSpanNames) != 3 {
		t.Errorf("expected 3 distinct tool spans, got %d: %v", len(toolSpanNames), toolSpanNames)
	}
}

// spanAttrs converts a span's attributes to a map for easy lookup.
func spanAttrs(s tracetest.SpanStub) map[string]any {
	m := make(map[string]any)
	for _, attr := range s.Attributes {
		m[string(attr.Key)] = attr.Value.AsInterface()
	}
	return m
}

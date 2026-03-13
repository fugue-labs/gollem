package temporal

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

type depsFixture struct {
	Name string `json:"name"`
}

func TestDeferredResultSignal_DeferredToolResult(t *testing.T) {
	signal := DeferredResultSignal{
		ToolName:   "lookup",
		ToolCallID: "call_1",
		Content:    "done",
		IsError:    true,
	}

	result := signal.DeferredToolResult()
	if result.ToolName != "lookup" || result.ToolCallID != "call_1" || result.Content != "done" || !result.IsError {
		t.Fatalf("unexpected deferred tool result: %+v", result)
	}
}

func TestDecodeWorkflowStatusMessages(t *testing.T) {
	if _, err := DecodeWorkflowStatusMessages(nil); err == nil {
		t.Fatal("expected error for nil workflow status")
	}

	now := time.Unix(30, 0).UTC()
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hello", Timestamp: now}}},
	}

	structured, err := core.EncodeMessages(messages)
	if err != nil {
		t.Fatalf("encode messages: %v", err)
	}
	decoded, err := DecodeWorkflowStatusMessages(&WorkflowStatus{Messages: structured})
	if err != nil {
		t.Fatalf("decode structured messages: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("expected 1 structured message, got %d", len(decoded))
	}

	raw, err := core.MarshalMessages(messages)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	decoded, err = DecodeWorkflowStatusMessages(&WorkflowStatus{MessagesJSON: raw})
	if err != nil {
		t.Fatalf("decode legacy messages_json: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("expected 1 legacy message, got %d", len(decoded))
	}
}

func TestDecodeWorkflowStatusTrace(t *testing.T) {
	if _, err := DecodeWorkflowStatusTrace(nil); err == nil {
		t.Fatal("expected error for nil workflow status")
	}

	trace := &core.RunTrace{RunID: "trace-1"}
	decoded, err := DecodeWorkflowStatusTrace(&WorkflowStatus{Trace: trace})
	if err != nil {
		t.Fatalf("decode structured trace: %v", err)
	}
	if decoded != trace {
		t.Fatal("expected structured trace pointer to be preserved")
	}

	raw, err := json.Marshal(core.RunTrace{RunID: "trace-2"})
	if err != nil {
		t.Fatalf("marshal legacy trace: %v", err)
	}
	decoded, err = DecodeWorkflowStatusTrace(&WorkflowStatus{TraceJSON: raw})
	if err != nil {
		t.Fatalf("decode legacy trace_json: %v", err)
	}
	if decoded == nil || decoded.RunID != "trace-2" {
		t.Fatalf("unexpected decoded legacy trace: %+v", decoded)
	}
}

func TestDecodeTemporalDeps(t *testing.T) {
	defaultDeps := depsFixture{Name: "default"}
	got, err := decodeTemporalDeps(nil, nil, defaultDeps, nil)
	if err != nil {
		t.Fatalf("decode default deps: %v", err)
	}
	if !reflect.DeepEqual(got, defaultDeps) {
		t.Fatalf("expected default deps %+v, got %+v", defaultDeps, got)
	}

	data, err := json.Marshal(depsFixture{Name: "workflow"})
	if err != nil {
		t.Fatalf("marshal deps: %v", err)
	}
	if _, err := decodeTemporalDeps(nil, nil, nil, data); err == nil {
		t.Fatal("expected error when deps type is unavailable")
	}

	got, err = decodeTemporalDeps(nil, reflect.TypeOf(&depsFixture{}), nil, data)
	if err != nil {
		t.Fatalf("decode pointer deps: %v", err)
	}
	ptr, ok := got.(*depsFixture)
	if !ok || ptr.Name != "workflow" {
		t.Fatalf("unexpected pointer deps: %#v", got)
	}

	got, err = decodeTemporalDeps(nil, reflect.TypeOf(depsFixture{}), nil, data)
	if err != nil {
		t.Fatalf("decode value deps: %v", err)
	}
	value, ok := got.(depsFixture)
	if !ok || value.Name != "workflow" {
		t.Fatalf("unexpected value deps: %#v", got)
	}
}

func TestTemporalAgentMarshalDeps(t *testing.T) {
	var nilAgent *TemporalAgent[string]
	if _, err := nilAgent.MarshalDeps(depsFixture{}); err == nil {
		t.Fatal("expected error when TemporalAgent is nil")
	}

	agent := core.NewAgent[string](core.NewTestModel(core.TextResponse("ok")))
	ta := NewTemporalAgent(agent, WithName("deps-marshal"))

	data, err := ta.MarshalDeps(depsFixture{Name: "tenant"})
	if err != nil {
		t.Fatalf("marshal deps: %v", err)
	}
	if string(data) != `{"name":"tenant"}` {
		t.Fatalf("unexpected marshaled deps %q", string(data))
	}
	if data, err := ta.MarshalDeps(nil); err != nil || data != nil {
		t.Fatalf("expected nil deps to marshal to nil,nil, got %q %v", string(data), err)
	}
}

func TestReadablePayloadDecodeHelpers(t *testing.T) {
	traceRaw, err := json.Marshal(core.RunTrace{RunID: "trace-raw"})
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	trace, err := decodeTrace(nil, traceRaw)
	if err != nil {
		t.Fatalf("decode raw trace: %v", err)
	}
	if trace == nil || trace.RunID != "trace-raw" {
		t.Fatalf("unexpected decoded raw trace: %+v", trace)
	}

	traceStepsRaw, err := json.Marshal([]core.TraceStep{{Kind: core.TraceModelResponse}})
	if err != nil {
		t.Fatalf("marshal trace steps: %v", err)
	}
	steps, err := decodeTraceSteps(nil, traceStepsRaw)
	if err != nil {
		t.Fatalf("decode raw trace steps: %v", err)
	}
	if len(steps) != 1 || steps[0].Kind != core.TraceModelResponse {
		t.Fatalf("unexpected decoded trace steps: %+v", steps)
	}

	snapshot, err := core.EncodeRunSnapshot(&core.RunSnapshot{RunID: "run-1"})
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	decodedSnapshot, err := decodeSerializedSnapshot(snapshot, nil)
	if err != nil {
		t.Fatalf("decode structured snapshot: %v", err)
	}
	if decodedSnapshot == nil || decodedSnapshot.RunID != "run-1" {
		t.Fatalf("unexpected decoded snapshot: %+v", decodedSnapshot)
	}
}

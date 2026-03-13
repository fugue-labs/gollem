package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/fugue-labs/gollem/core"
)

type traceCaptureExporter struct {
	mu     sync.Mutex
	traces []*core.RunTrace
}

func (e *traceCaptureExporter) Export(_ context.Context, trace *core.RunTrace) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	cloned := *trace
	cloned.Steps = append([]core.TraceStep(nil), trace.Steps...)
	e.traces = append(e.traces, &cloned)
	return nil
}

func (e *traceCaptureExporter) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.traces)
}

func (e *traceCaptureExporter) Last() *core.RunTrace {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.traces) == 0 {
		return nil
	}
	return e.traces[len(e.traces)-1]
}

type temporalFailingExporter struct{}

func (e *temporalFailingExporter) Export(_ context.Context, _ *core.RunTrace) error {
	return errors.New("export failed")
}

type workflowCounterTool struct {
	count int
}

func (w *workflowCounterTool) ExportState() (any, error) {
	return map[string]any{"count": w.count}, nil
}

func (w *workflowCounterTool) RestoreState(state any) error {
	if m, ok := state.(map[string]any); ok {
		switch count := m["count"].(type) {
		case int:
			w.count = count
		case float64:
			w.count = int(count)
		}
	}
	return nil
}

func registerTemporalWorkflow[T any](env *testsuite.TestWorkflowEnvironment, ta *TemporalAgent[T]) {
	env.RegisterWorkflowWithOptions(ta.RunWorkflow, workflow.RegisterOptions{Name: ta.WorkflowName()})
	for name, fn := range ta.Activities() {
		env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
}

func TestTemporalAgent_RunWorkflow_Text(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("hello from workflow"))
	agent := core.NewAgent[string](model, core.WithSystemPrompt[string]("Be concise."))
	ta := NewTemporalAgent(agent, WithName("workflow-text"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "say hello"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var output WorkflowOutput
	if err := env.GetWorkflowResult(&output); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if !output.Completed {
		t.Fatal("expected workflow to complete")
	}

	result, err := ta.DecodeWorkflowOutput(&output)
	if err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	if result.Output != "hello from workflow" {
		t.Errorf("expected workflow output %q, got %q", "hello from workflow", result.Output)
	}

	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(calls))
	}
	req, ok := calls[0].Messages[0].(core.ModelRequest)
	if !ok {
		t.Fatalf("expected first message to be ModelRequest, got %T", calls[0].Messages[0])
	}
	if len(req.Parts) != 2 {
		t.Fatalf("expected system+user prompt parts, got %d", len(req.Parts))
	}
}

func TestTemporalAgent_DecodeWorkflowOutput_MissingSnapshotReturnsError(t *testing.T) {
	ta := NewTemporalAgent(core.NewAgent[string](core.NewTestModel(core.TextResponse("ok"))), WithName("decode-missing-snapshot"))

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DecodeWorkflowOutput panicked: %v", r)
		}
	}()

	_, err := ta.DecodeWorkflowOutput(&WorkflowOutput{
		Completed:  true,
		OutputJSON: json.RawMessage(`"ok"`),
	})
	if err == nil {
		t.Fatal("expected missing snapshot error")
	}
	if err.Error() != "workflow output missing snapshot" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTemporalAgent_RunWorkflow_ToolAndStatefulResult(t *testing.T) {
	type Params struct{}

	counterState := &workflowCounterTool{}
	counter := core.FuncTool[Params]("counter", "counter", func(_ context.Context, _ Params) (string, error) {
		counterState.count++
		return "counted", nil
	})
	counter.Stateful = counterState

	model := core.NewTestModel(
		core.ToolCallResponse("counter", `{}`),
		core.TextResponse("done"),
	)
	agent := core.NewAgent[string](model, core.WithTools[string](counter))
	ta := NewTemporalAgent(agent, WithName("workflow-tool"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "run counter"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var output WorkflowOutput
	if err := env.GetWorkflowResult(&output); err != nil {
		t.Fatalf("workflow result: %v", err)
	}

	result, err := ta.DecodeWorkflowOutput(&output)
	if err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	if result.Output != "done" {
		t.Errorf("expected final output %q, got %q", "done", result.Output)
	}
	state, ok := result.ToolState["counter"].(map[string]any)
	if !ok {
		t.Fatalf("expected counter tool state, got %T", result.ToolState["counter"])
	}
	if got := int(state["count"].(float64)); got != 1 {
		t.Errorf("expected counter state 1, got %d", got)
	}
}

func TestTemporalAgent_RunWorkflow_StructuredOutput(t *testing.T) {
	type Answer struct {
		Answer string `json:"answer"`
	}

	model := core.NewTestModel(
		core.ToolCallResponse("final_result", `{"answer":"42"}`),
	)
	agent := core.NewAgent[Answer](model)
	ta := NewTemporalAgent(agent, WithName("workflow-structured"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "answer"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var output WorkflowOutput
	if err := env.GetWorkflowResult(&output); err != nil {
		t.Fatalf("workflow result: %v", err)
	}

	result, err := ta.DecodeWorkflowOutput(&output)
	if err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	if result.Output.Answer != "42" {
		t.Errorf("expected answer 42, got %q", result.Output.Answer)
	}
}

func TestTemporalAgent_RunWorkflow_SnapshotResumeAppendsNewInitialRequest(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("resumed"))
	agent := core.NewAgent[string](model, core.WithSystemPrompt[string]("Be concise."))
	ta := NewTemporalAgent(agent, WithName("workflow-snapshot-resume"))

	snapshot, err := core.EncodeRunSnapshot(&core.RunSnapshot{
		Messages: []core.ModelMessage{
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.SystemPromptPart{Content: "Older system prompt"},
					core.UserPromptPart{Content: "older prompt"},
				},
				Timestamp: time.Now().Add(-2 * time.Minute),
			},
			core.ModelResponse{
				Parts:     []core.ModelResponsePart{core.TextPart{Content: "older response"}},
				Timestamp: time.Now().Add(-time.Minute),
			},
		},
		Prompt: "older prompt",
	})
	if err != nil {
		t.Fatalf("encode run snapshot: %v", err)
	}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{
		Prompt:   "branch prompt",
		Snapshot: snapshot,
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var output WorkflowOutput
	if err := env.GetWorkflowResult(&output); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	result, err := ta.DecodeWorkflowOutput(&output)
	if err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	if result.Output != "resumed" {
		t.Fatalf("expected final output %q, got %q", "resumed", result.Output)
	}

	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(calls))
	}
	if got := len(calls[0].Messages); got != 3 {
		t.Fatalf("expected snapshot resume to append a new request, got %d messages", got)
	}
	req, ok := calls[0].Messages[2].(core.ModelRequest)
	if !ok {
		t.Fatalf("expected appended message to be ModelRequest, got %T", calls[0].Messages[2])
	}
	foundPrompt := false
	for _, part := range req.Parts {
		user, ok := part.(core.UserPromptPart)
		if ok && user.Content == "branch prompt" {
			foundPrompt = true
			break
		}
	}
	if !foundPrompt {
		t.Fatal("expected resumed workflow to append the new prompt to the next model request")
	}
}

func TestTemporalAgent_RunWorkflow_TraceCostAndExporterParity(t *testing.T) {
	resp := core.TextResponse("traced result")
	resp.Usage = core.Usage{InputTokens: 100, OutputTokens: 50}

	model := core.NewTestModel(resp)
	exporter := &traceCaptureExporter{}
	agent := core.NewAgent[string](model,
		core.WithTraceExporter[string](core.NewMultiExporter(exporter, &temporalFailingExporter{})),
		core.WithCostTracker[string](core.NewCostTracker(map[string]core.ModelPricing{
			model.ModelName(): {
				InputTokenCost:  0.000003,
				OutputTokenCost: 0.000015,
			},
		})),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-trace-cost"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "trace this"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var output WorkflowOutput
	if err := env.GetWorkflowResult(&output); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if output.Trace == nil {
		t.Fatal("expected workflow output to include a decoded trace")
	}
	if output.Cost == nil {
		t.Fatal("expected workflow output to include cost")
	}

	result, err := ta.DecodeWorkflowOutput(&output)
	if err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	if result.Trace == nil {
		t.Fatal("expected decoded result trace")
	}
	if result.Cost == nil {
		t.Fatal("expected decoded result cost")
	}
	if result.Cost.TotalCost <= 0 {
		t.Fatalf("expected positive run cost, got %f", result.Cost.TotalCost)
	}
	if result.Trace.Prompt != "trace this" {
		t.Fatalf("expected trace prompt %q, got %q", "trace this", result.Trace.Prompt)
	}
	if !result.Trace.Success {
		t.Fatal("expected successful trace")
	}

	var hasReq, hasResp bool
	for _, step := range result.Trace.Steps {
		switch step.Kind {
		case core.TraceModelRequest:
			hasReq = true
		case core.TraceModelResponse:
			hasResp = true
		}
	}
	if !hasReq || !hasResp {
		t.Fatalf("expected trace to include model request/response steps, got %+v", result.Trace.Steps)
	}

	if exporter.Count() != 1 {
		t.Fatalf("expected exporter to receive 1 trace, got %d", exporter.Count())
	}
	exported := exporter.Last()
	if exported == nil {
		t.Fatal("expected exported trace")
	}
	if exported.RunID != result.RunID {
		t.Fatalf("expected exported trace run ID %q, got %q", result.RunID, exported.RunID)
	}
	if exported.Prompt != "trace this" {
		t.Fatalf("expected exported trace prompt %q, got %q", "trace this", exported.Prompt)
	}
}

func TestTemporalAgent_RunWorkflow_DeferredSignal(t *testing.T) {
	deferTool := core.Tool{
		Definition: core.ToolDefinition{
			Name:        "async_task",
			Description: "async task",
			Kind:        core.ToolKindFunction,
		},
		Handler: func(_ context.Context, _ *core.RunContext, _ string) (any, error) {
			return nil, &core.CallDeferred{Message: "waiting"}
		},
	}

	model := core.NewTestModel(
		core.ToolCallResponseWithID("async_task", `{}`, "call_1"),
		core.TextResponse("resumed"),
	)
	agent := core.NewAgent[string](model, core.WithTools[string](deferTool))
	ta := NewTemporalAgent(agent, WithName("workflow-deferred"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ta.DeferredResultSignalName(), DeferredResultSignal{
			ToolName:   "async_task",
			ToolCallID: "call_1",
			Content:    "done async",
		})
	}, time.Second)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "do async"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var output WorkflowOutput
	if err := env.GetWorkflowResult(&output); err != nil {
		t.Fatalf("workflow result: %v", err)
	}

	result, err := ta.DecodeWorkflowOutput(&output)
	if err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	if result.Output != "resumed" {
		t.Errorf("expected resumed output %q, got %q", "resumed", result.Output)
	}

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(calls))
	}
	foundDeferredResult := false
	for _, msg := range calls[1].Messages {
		req, ok := msg.(core.ModelRequest)
		if !ok {
			continue
		}
		for _, part := range req.Parts {
			if trp, ok := part.(core.ToolReturnPart); ok && trp.ToolCallID == "call_1" {
				foundDeferredResult = true
			}
		}
	}
	if !foundDeferredResult {
		t.Error("expected follow-up model call to include deferred tool result")
	}
}

func TestTemporalAgent_RunWorkflow_ApprovalDeniedSignal(t *testing.T) {
	type Params struct{}

	executed := false
	tool := core.FuncTool[Params]("dangerous_action", "Dangerous action", func(_ context.Context, _ Params) (string, error) {
		executed = true
		return "done", nil
	}, core.WithRequiresApproval())

	model := core.NewTestModel(
		core.ToolCallResponseWithID("dangerous_action", `{}`, "call_denied"),
		core.TextResponse("fallback after denial"),
	)
	agent := core.NewAgent[string](model, core.WithTools[string](tool))
	ta := NewTemporalAgent(agent, WithName("workflow-approval-denied"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ta.ApprovalSignalName(), ApprovalSignal{
			ToolName:   "dangerous_action",
			ToolCallID: "call_denied",
			Approved:   false,
			Message:    "needs review",
		})
	}, time.Second)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "do dangerous thing"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var output WorkflowOutput
	if err := env.GetWorkflowResult(&output); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	result, err := ta.DecodeWorkflowOutput(&output)
	if err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	if result.Output != "fallback after denial" {
		t.Fatalf("expected fallback output after denial, got %q", result.Output)
	}
	if executed {
		t.Fatal("expected tool execution to be skipped after denial")
	}

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 model calls after denial, got %d", len(calls))
	}
	foundRetry := false
	for _, msg := range calls[1].Messages {
		req, ok := msg.(core.ModelRequest)
		if !ok {
			continue
		}
		for _, part := range req.Parts {
			retry, ok := part.(core.RetryPromptPart)
			if ok && retry.ToolCallID == "call_denied" &&
				retry.Content == `tool call "dangerous_action" was denied by the user: needs review` {
				foundRetry = true
			}
		}
	}
	if !foundRetry {
		t.Fatal("expected denied approval to be sent back as a retry prompt")
	}
}

func TestTemporalWorkflowHelpers(t *testing.T) {
	if got := waitingReason(nil, nil); got != "" {
		t.Fatalf("expected empty waiting reason, got %q", got)
	}
	if got := waitingReason([]ToolApprovalRequest{{ToolCallID: "a"}}, nil); got != "approval" {
		t.Fatalf("expected approval waiting reason, got %q", got)
	}
	if got := waitingReason(nil, []core.DeferredToolRequest{{ToolCallID: "b"}}); got != "deferred" {
		t.Fatalf("expected deferred waiting reason, got %q", got)
	}
	if got := waitingReason(
		[]ToolApprovalRequest{{ToolCallID: "a"}},
		[]core.DeferredToolRequest{{ToolCallID: "b"}},
	); got != "approval_and_deferred" {
		t.Fatalf("expected combined waiting reason, got %q", got)
	}

	if err := workflowAbortError("   "); err == nil || err.Error() != "workflow aborted" {
		t.Fatalf("expected default abort error, got %v", err)
	}
	if err := workflowAbortError(" user stopped "); err == nil || err.Error() != "workflow aborted: user stopped" {
		t.Fatalf("expected trimmed abort error, got %v", err)
	}

	if got := approvalDeniedMessage("dangerous_action", ""); got != `tool call "dangerous_action" was denied by the user` {
		t.Fatalf("unexpected approval denied message %q", got)
	}
	if got := approvalDeniedMessage("dangerous_action", " not now "); got != `tool call "dangerous_action" was denied by the user: not now` {
		t.Fatalf("unexpected approval denied message with detail %q", got)
	}

	limit := 2
	if err := limitsToolCalls(core.UsageLimits{}, 2, 2); err != nil {
		t.Fatalf("expected no error without tool call limit, got %v", err)
	}
	if err := limitsToolCalls(core.UsageLimits{ToolCallsLimit: &limit}, 1, 1); err != nil {
		t.Fatalf("expected no error within tool call limit, got %v", err)
	}
	err := limitsToolCalls(core.UsageLimits{ToolCallsLimit: &limit}, 1, 2)
	var usageErr *core.UsageLimitExceeded
	if !errors.As(err, &usageErr) {
		t.Fatalf("expected usage limit exceeded, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "tool call limit of 2 exceeded") {
		t.Fatalf("unexpected tool call limit error %v", err)
	}
}

func TestTemporalAgent_DecodeWorkflowOutput_LegacyAndPausedBranches(t *testing.T) {
	ta := NewTemporalAgent(core.NewAgent[string](core.NewTestModel(core.TextResponse("ok"))), WithName("decode-legacy"))

	if _, err := ta.DecodeWorkflowOutput(nil); err == nil {
		t.Fatal("expected nil workflow output error")
	}
	if _, err := ta.DecodeWorkflowOutput(&WorkflowOutput{
		Completed:        false,
		DeferredRequests: []core.DeferredToolRequest{{ToolCallID: "call_1"}},
	}); err == nil || !strings.Contains(err.Error(), "workflow paused with 1 deferred tool request") {
		t.Fatalf("expected paused workflow error, got %v", err)
	}

	now := time.Unix(40, 0).UTC()
	snapshotJSON, err := core.MarshalSnapshot(&core.RunSnapshot{
		Messages: []core.ModelMessage{
			core.ModelRequest{
				Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "legacy", Timestamp: now}},
				Timestamp: now,
			},
		},
		Usage:     core.RunUsage{Requests: 1},
		RunID:     "legacy-run",
		ToolState: map[string]any{"tool": map[string]any{"count": 1}},
		Timestamp: now,
	})
	if err != nil {
		t.Fatalf("marshal legacy snapshot: %v", err)
	}
	traceJSON, err := json.Marshal(core.RunTrace{RunID: "legacy-run"})
	if err != nil {
		t.Fatalf("marshal legacy trace: %v", err)
	}

	result, err := ta.DecodeWorkflowOutput(&WorkflowOutput{
		Completed:    true,
		OutputJSON:   json.RawMessage(`"legacy-output"`),
		SnapshotJSON: snapshotJSON,
		TraceJSON:    traceJSON,
		Cost:         &core.RunCost{TotalCost: 1.23},
	})
	if err != nil {
		t.Fatalf("decode legacy workflow output: %v", err)
	}
	if result.Output != "legacy-output" {
		t.Fatalf("expected legacy output, got %q", result.Output)
	}
	if result.RunID != "legacy-run" {
		t.Fatalf("expected legacy run id, got %q", result.RunID)
	}
	if result.Trace == nil || result.Trace.RunID != "legacy-run" {
		t.Fatalf("expected decoded legacy trace, got %+v", result.Trace)
	}
	if result.Cost == nil || result.Cost.TotalCost != 1.23 {
		t.Fatalf("expected cost to be preserved, got %+v", result.Cost)
	}
}

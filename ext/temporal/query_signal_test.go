package temporal

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/fugue-labs/gollem/core"
)

func TestTemporalAgent_RunWorkflow_QueryStatusWhileWaitingOnDeferred(t *testing.T) {
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

	firstResp := core.ToolCallResponseWithID("async_task", `{}`, "call_1")
	firstResp.Usage = core.Usage{InputTokens: 100, OutputTokens: 25}
	model := core.NewTestModel(
		firstResp,
		core.TextResponse("done"),
	)
	agent := core.NewAgent[string](model,
		core.WithTools[string](deferTool),
		core.WithTracing[string](),
		core.WithCostTracker[string](core.NewCostTracker(map[string]core.ModelPricing{
			model.ModelName(): {
				InputTokenCost:  0.000003,
				OutputTokenCost: 0.000015,
			},
		})),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-query-deferred"), WithVersion("2026_03"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)

	var (
		status   WorkflowStatus
		queryErr error
	)
	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(ta.StatusQueryName())
		if err != nil {
			queryErr = err
			return
		}
		queryErr = value.Get(&status)
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ta.DeferredResultSignalName(), DeferredResultSignal{
			ToolName:   "async_task",
			ToolCallID: "call_1",
			Content:    "resolved",
		})
	}, 2*time.Second)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "do async"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	if queryErr != nil {
		t.Fatalf("query workflow: %v", queryErr)
	}
	if !status.Waiting {
		t.Fatal("expected workflow to report waiting status")
	}
	if status.WaitingReason != "deferred" {
		t.Fatalf("expected waiting reason %q, got %q", "deferred", status.WaitingReason)
	}
	if len(status.DeferredRequests) != 1 {
		t.Fatalf("expected 1 deferred request, got %d", len(status.DeferredRequests))
	}
	if len(status.PendingApprovals) != 0 {
		t.Fatalf("expected 0 pending approvals, got %d", len(status.PendingApprovals))
	}
	if status.Cost == nil || status.Cost.TotalCost <= 0 {
		t.Fatalf("expected query status to include positive run cost, got %+v", status.Cost)
	}
	if status.RunStep != 1 {
		t.Fatalf("expected run step 1 while waiting, got %d", status.RunStep)
	}
	if status.WorkflowName != ta.WorkflowName() {
		t.Fatalf("expected workflow name %q, got %q", ta.WorkflowName(), status.WorkflowName)
	}
	if status.RegistrationName != ta.RegistrationName() {
		t.Fatalf("expected registration name %q, got %q", ta.RegistrationName(), status.RegistrationName)
	}
	if status.Version != ta.Version() {
		t.Fatalf("expected version %q, got %q", ta.Version(), status.Version)
	}
	if status.ContinueAsNewCount != 0 {
		t.Fatalf("expected continue-as-new count 0 while waiting, got %d", status.ContinueAsNewCount)
	}
	messages, err := DecodeWorkflowStatusMessages(&status)
	if err != nil {
		t.Fatalf("decode workflow status messages: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("expected at least 2 messages in workflow status, got %d", len(messages))
	}
	trace, err := DecodeWorkflowStatusTrace(&status)
	if err != nil {
		t.Fatalf("decode workflow status trace: %v", err)
	}
	if trace == nil {
		t.Fatal("expected workflow status trace")
	}
	var hasReq, hasResp, hasToolCall, hasToolResult bool
	for _, step := range trace.Steps {
		switch step.Kind {
		case core.TraceModelRequest:
			hasReq = true
		case core.TraceModelResponse:
			hasResp = true
		case core.TraceToolCall:
			hasToolCall = true
		case core.TraceToolResult:
			hasToolResult = true
		}
	}
	if !hasReq || !hasResp || !hasToolCall || !hasToolResult {
		t.Fatalf("expected status trace to include model/tool steps, got %+v", trace.Steps)
	}
}

func TestTemporalAgent_RunWorkflow_ApprovalSignal(t *testing.T) {
	type Params struct{}

	var executed bool
	tool := core.FuncTool[Params]("dangerous_action", "Dangerous action", func(_ context.Context, _ Params) (string, error) {
		executed = true
		return "done", nil
	}, core.WithRequiresApproval())

	model := core.NewTestModel(
		core.ToolCallResponseWithID("dangerous_action", `{}`, "call_approval"),
		core.TextResponse("approved"),
	)
	agent := core.NewAgent[string](model, core.WithTools[string](tool))
	ta := NewTemporalAgent(agent, WithName("workflow-approval"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)

	var (
		status   WorkflowStatus
		queryErr error
	)
	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(ta.StatusQueryName())
		if err != nil {
			queryErr = err
			return
		}
		queryErr = value.Get(&status)
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ta.ApprovalSignalName(), ApprovalSignal{
			ToolName:   "dangerous_action",
			ToolCallID: "call_approval",
			Approved:   true,
		})
	}, 2*time.Second)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "do dangerous thing"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	if queryErr != nil {
		t.Fatalf("query workflow: %v", queryErr)
	}
	if !status.Waiting {
		t.Fatal("expected workflow to report waiting status")
	}
	if status.WaitingReason != "approval" {
		t.Fatalf("expected waiting reason %q, got %q", "approval", status.WaitingReason)
	}
	if len(status.PendingApprovals) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(status.PendingApprovals))
	}
	if status.PendingApprovals[0].ToolCallID != "call_approval" {
		t.Fatalf("expected approval request for call_approval, got %q", status.PendingApprovals[0].ToolCallID)
	}
	if !executed {
		t.Fatal("expected tool to execute after approval signal")
	}
}

func TestTemporalAgent_RunWorkflow_AbortSignal(t *testing.T) {
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

	model := core.NewTestModel(core.ToolCallResponseWithID("async_task", `{}`, "call_abort"))
	agent := core.NewAgent[string](model, core.WithTools[string](deferTool))
	ta := NewTemporalAgent(agent, WithName("workflow-abort"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ta.AbortSignalName(), AbortSignal{Reason: "user requested stop"})
	}, time.Second)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "abort me"})
	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("expected workflow error after abort signal")
	}
	if !strings.Contains(err.Error(), "workflow aborted: user requested stop") {
		t.Fatalf("expected abort error, got %v", err)
	}
}

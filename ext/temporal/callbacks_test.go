package temporal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/fugue-labs/gollem/core"
)

func TestTemporalAgent_BuildCallbackRunContext_PreservesRunStateSnapshot(t *testing.T) {
	start := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Second)
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "inspect snapshot", Timestamp: start}},
			Timestamp: start,
		},
	}
	serialized, err := core.EncodeMessages(messages)
	if err != nil {
		t.Fatalf("encode messages: %v", err)
	}

	ta := NewTemporalAgent(core.NewAgent[string](core.NewTestModel(core.TextResponse("ok"))), WithName("callback-snapshot"))
	rc, err := ta.buildCallbackRunContext(callbackRunInput{
		Prompt:          "inspect snapshot",
		Messages:        serialized,
		Usage:           core.RunUsage{Requests: 2, ToolCalls: 1},
		LastInputTokens: 17,
		Retries:         3,
		ToolRetries:     map[string]int{"validator": 2},
		RunStep:         8,
		RunID:           "run-callback",
		RunStartTime:    start,
		ToolState:       map[string]any{"validator": map[string]any{"count": 4}},
		ToolName:        "validator",
		ToolCallID:      "call-1",
		Retry:           2,
		MaxRetries:      5,
	})
	if err != nil {
		t.Fatalf("build callback run context: %v", err)
	}

	snap := rc.RunStateSnapshot()
	if snap == nil {
		t.Fatal("expected callback run context snapshot")
	}
	if snap.Prompt != "inspect snapshot" {
		t.Fatalf("unexpected prompt %q", snap.Prompt)
	}
	if snap.RunID != "run-callback" {
		t.Fatalf("unexpected run id %q", snap.RunID)
	}
	if snap.RunStep != 8 {
		t.Fatalf("unexpected run step %d", snap.RunStep)
	}
	if snap.LastInputTokens != 17 {
		t.Fatalf("unexpected last input tokens %d", snap.LastInputTokens)
	}
	if snap.Retries != 3 {
		t.Fatalf("unexpected retries %d", snap.Retries)
	}
	if snap.ToolRetries["validator"] != 2 {
		t.Fatalf("unexpected tool retries %+v", snap.ToolRetries)
	}
	if got := snap.ToolState["validator"].(map[string]any)["count"].(int); got != 4 {
		t.Fatalf("unexpected tool state %+v", snap.ToolState)
	}
	if len(snap.Messages) != 1 {
		t.Fatalf("unexpected snapshot messages %+v", snap.Messages)
	}
	if snap.Timestamp.IsZero() {
		t.Fatal("expected snapshot timestamp to be set")
	}
}

func TestTemporalAgent_RunWorkflow_CallbackActivities_RequestPipeline(t *testing.T) {
	type Params struct{}

	processorCalls := 0
	model := core.NewTestModel(
		core.ToolCallResponse("echo", `{}`),
		core.TextResponse("done"),
	)
	tool := core.FuncTool[Params]("echo", "Echo", func(_ context.Context, _ Params) (string, error) {
		return "tool-result", nil
	})
	agent := core.NewAgent[string](model,
		core.WithSystemPrompt[string]("static prompt"),
		core.WithDynamicSystemPrompt[string](func(_ context.Context, _ *core.RunContext) (string, error) {
			return "dynamic prompt", nil
		}),
		core.WithHistoryProcessor[string](func(_ context.Context, messages []core.ModelMessage) ([]core.ModelMessage, error) {
			processorCalls++
			return messages, nil
		}),
		core.WithMessageInterceptor[string](func(_ context.Context, messages []core.ModelMessage) core.InterceptResult {
			modified := make([]core.ModelMessage, len(messages))
			for i, msg := range messages {
				req, ok := msg.(core.ModelRequest)
				if !ok {
					modified[i] = msg
					continue
				}
				parts := make([]core.ModelRequestPart, len(req.Parts))
				copy(parts, req.Parts)
				for j, part := range parts {
					if user, ok := part.(core.UserPromptPart); ok {
						parts[j] = core.UserPromptPart{Content: user.Content + " [intercepted]", Timestamp: user.Timestamp}
					}
				}
				modified[i] = core.ModelRequest{Parts: parts, Timestamp: req.Timestamp}
			}
			return core.InterceptResult{Action: core.MessageModify, Messages: modified}
		}),
		core.WithTools[string](tool),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-callback-request"))

	runWorkflowTest(t, ta, WorkflowInput{Prompt: "say hi"}, func(output WorkflowOutput) {
		result, err := ta.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "done" {
			t.Fatalf("expected final output %q, got %q", "done", result.Output)
		}
		if processorCalls != 2 {
			t.Fatalf("expected 2 history processor calls, got %d", processorCalls)
		}

		calls := model.Calls()
		if len(calls) != 2 {
			t.Fatalf("expected 2 model calls, got %d", len(calls))
		}
		req, ok := calls[0].Messages[0].(core.ModelRequest)
		if !ok {
			t.Fatalf("expected first request, got %T", calls[0].Messages[0])
		}
		var foundDynamic, foundIntercepted bool
		for _, part := range req.Parts {
			if system, ok := part.(core.SystemPromptPart); ok && system.Content == "dynamic prompt" {
				foundDynamic = true
			}
			if user, ok := part.(core.UserPromptPart); ok && user.Content == "say hi [intercepted]" {
				foundIntercepted = true
			}
		}
		if !foundDynamic {
			t.Fatal("expected dynamic system prompt in first request")
		}
		if !foundIntercepted {
			t.Fatal("expected message interceptor to modify the user prompt")
		}
	})
}

func TestTemporalAgent_RunWorkflow_ResponseInterceptorDrop(t *testing.T) {
	model := core.NewTestModel(
		core.TextResponse("drop me"),
		core.TextResponse("keep me"),
	)
	agent := core.NewAgent[string](model,
		core.WithResponseInterceptor[string](func(_ context.Context, resp *core.ModelResponse) core.InterceptResult {
			if resp.TextContent() == "drop me" {
				return core.InterceptResult{Action: core.MessageDrop}
			}
			return core.InterceptResult{Action: core.MessageAllow}
		}),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-response-interceptor"))

	runWorkflowTest(t, ta, WorkflowInput{Prompt: "respond"}, func(output WorkflowOutput) {
		result, err := ta.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "keep me" {
			t.Fatalf("expected final output %q, got %q", "keep me", result.Output)
		}
		if got := len(model.Calls()); got != 2 {
			t.Fatalf("expected 2 model calls after dropped response, got %d", got)
		}
	})
}

func TestTemporalAgent_RunWorkflow_OutputRepairAndValidator(t *testing.T) {
	type Answer struct {
		Value int `json:"value"`
	}

	repairCalls := 0
	validatorCalls := 0
	model := core.NewTestModel(
		core.ToolCallResponse("final_result", `{broken}`),
		core.ToolCallResponse("final_result", `{"value":1}`),
	)
	agent := core.NewAgent[Answer](model,
		core.WithOutputRepair(func(_ context.Context, _ string, _ error) (Answer, error) {
			repairCalls++
			return Answer{Value: -1}, nil
		}),
		core.WithOutputValidator(func(_ context.Context, _ *core.RunContext, output Answer) (Answer, error) {
			validatorCalls++
			if output.Value < 0 {
				return output, core.NewModelRetryError("value must be positive")
			}
			return output, nil
		}),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-output-callbacks"))

	runWorkflowTest(t, ta, WorkflowInput{Prompt: "answer"}, func(output WorkflowOutput) {
		result, err := ta.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output.Value != 1 {
			t.Fatalf("expected repaired-and-retried value 1, got %d", result.Output.Value)
		}
		if repairCalls != 1 {
			t.Fatalf("expected repair to run once, got %d", repairCalls)
		}
		if validatorCalls != 2 {
			t.Fatalf("expected validator to run twice, got %d", validatorCalls)
		}
	})
}

func TestTemporalAgent_RunWorkflow_ToolApprovalCallback(t *testing.T) {
	type Params struct{}

	approvalCalls := 0
	executed := false
	model := core.NewTestModel(
		core.ToolCallResponseWithID("dangerous_action", `{}`, "call_approval"),
		core.TextResponse("approved"),
	)
	tool := core.FuncTool[Params]("dangerous_action", "Dangerous action", func(_ context.Context, _ Params) (string, error) {
		executed = true
		return "done", nil
	}, core.WithRequiresApproval())
	agent := core.NewAgent[string](model,
		core.WithTools[string](tool),
		core.WithToolApproval[string](func(_ context.Context, toolName string, argsJSON string) (bool, error) {
			approvalCalls++
			if toolName != "dangerous_action" || argsJSON != `{}` {
				return false, errors.New("unexpected approval input")
			}
			return true, nil
		}),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-tool-approval-callback"))

	runWorkflowTest(t, ta, WorkflowInput{Prompt: "do dangerous thing"}, func(output WorkflowOutput) {
		result, err := ta.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "approved" {
			t.Fatalf("expected final output %q, got %q", "approved", result.Output)
		}
		if !executed {
			t.Fatal("expected tool to execute after approval callback")
		}
		if approvalCalls != 1 {
			t.Fatalf("expected 1 approval callback call, got %d", approvalCalls)
		}
	})
}

func TestTemporalAgent_RunWorkflow_ToolResultValidator(t *testing.T) {
	type Params struct{}

	validatorCalls := 0
	model := core.NewTestModel(
		core.ToolCallResponse("echo", `{}`),
		core.TextResponse("done"),
	)
	tool := core.FuncTool[Params]("echo", "Echo", func(_ context.Context, _ Params) (string, error) {
		return "invalid", nil
	}, core.WithToolResultValidator(func(_ context.Context, _ *core.RunContext, _ string, result string) error {
		validatorCalls++
		if result == "invalid" {
			return errors.New("bad result")
		}
		return nil
	}))
	agent := core.NewAgent[string](model, core.WithTools[string](tool), core.WithMaxRetries[string](1))
	ta := NewTemporalAgent(agent, WithName("workflow-tool-result-validator"))

	runWorkflowTest(t, ta, WorkflowInput{Prompt: "validate tool"}, func(output WorkflowOutput) {
		result, err := ta.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "done" {
			t.Fatalf("expected final output %q, got %q", "done", result.Output)
		}
		if validatorCalls != 1 {
			t.Fatalf("expected tool result validator to run once, got %d", validatorCalls)
		}

		calls := model.Calls()
		if len(calls) != 2 {
			t.Fatalf("expected 2 model calls, got %d", len(calls))
		}
		foundRetry := false
		for _, msg := range calls[1].Messages {
			req, ok := msg.(core.ModelRequest)
			if !ok {
				continue
			}
			for _, part := range req.Parts {
				if retry, ok := part.(core.RetryPromptPart); ok && retry.Content == "tool result validation failed: bad result" {
					foundRetry = true
				}
			}
		}
		if !foundRetry {
			t.Fatal("expected retry prompt from tool result validator failure")
		}
	})
}

func TestTemporalAgent_RunWorkflow_ToolsetGuardrailsAndHooks(t *testing.T) {
	type Params struct{}

	var (
		runStarts          int
		runEnds            int
		turnStarts         int
		turnEnds           int
		modelRequests      int
		modelResponses     int
		toolStarts         int
		toolEnds           int
		guardrailEvaluated int
	)

	model := core.NewTestModel(
		core.ToolCallResponse("echo_toolset", `{}`),
		core.TextResponse("done"),
	)
	tool := core.FuncTool[Params]("echo_toolset", "Echo toolset", func(_ context.Context, _ Params) (string, error) {
		return "toolset-result", nil
	})
	toolset := core.NewToolset("extra", tool)

	agent := core.NewAgent[string](model,
		core.WithToolsets[string](toolset),
		core.WithInputGuardrail[string]("decorate", func(_ context.Context, prompt string) (string, error) {
			return strings.TrimSpace(prompt) + " [guarded]", nil
		}),
		core.WithTurnGuardrail[string]("allow", func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage) error {
			return nil
		}),
		core.WithHooks[string](core.Hook{
			OnRunStart: func(_ context.Context, _ *core.RunContext, _ string) { runStarts++ },
			OnRunEnd: func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage, _ error) {
				runEnds++
			},
			OnTurnStart: func(_ context.Context, _ *core.RunContext, _ int) { turnStarts++ },
			OnTurnEnd: func(_ context.Context, _ *core.RunContext, _ int, _ *core.ModelResponse) {
				turnEnds++
			},
			OnModelRequest: func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage) {
				modelRequests++
			},
			OnModelResponse: func(_ context.Context, _ *core.RunContext, _ *core.ModelResponse) {
				modelResponses++
			},
			OnToolStart: func(_ context.Context, _ *core.RunContext, _, _, _ string) { toolStarts++ },
			OnToolEnd: func(_ context.Context, _ *core.RunContext, _, _, _ string, _ error) {
				toolEnds++
			},
			OnGuardrailEvaluated: func(_ context.Context, _ *core.RunContext, _ string, _ bool, _ error) {
				guardrailEvaluated++
			},
		}),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-toolset-hooks"))

	runWorkflowTest(t, ta, WorkflowInput{Prompt: "  hello  "}, func(output WorkflowOutput) {
		result, err := ta.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "done" {
			t.Fatalf("expected final output %q, got %q", "done", result.Output)
		}
		if runStarts != 1 || runEnds != 1 {
			t.Fatalf("expected 1 run start/end, got starts=%d ends=%d", runStarts, runEnds)
		}
		if turnStarts != 2 || turnEnds != 2 {
			t.Fatalf("expected 2 turn start/end hooks, got starts=%d ends=%d", turnStarts, turnEnds)
		}
		if modelRequests != 2 || modelResponses != 2 {
			t.Fatalf("expected 2 model request/response hooks, got requests=%d responses=%d", modelRequests, modelResponses)
		}
		if toolStarts != 1 || toolEnds != 1 {
			t.Fatalf("expected 1 tool start/end hook, got starts=%d ends=%d", toolStarts, toolEnds)
		}
		if guardrailEvaluated != 3 {
			t.Fatalf("expected 3 guardrail evaluations (1 input + 2 turn), got %d", guardrailEvaluated)
		}

		calls := model.Calls()
		if len(calls) != 2 {
			t.Fatalf("expected 2 model calls, got %d", len(calls))
		}
		req, ok := calls[0].Messages[0].(core.ModelRequest)
		if !ok {
			t.Fatalf("expected first request, got %T", calls[0].Messages[0])
		}
		foundPrompt := false
		for _, part := range req.Parts {
			if user, ok := part.(core.UserPromptPart); ok && user.Content == "hello [guarded]" {
				foundPrompt = true
			}
		}
		if !foundPrompt {
			t.Fatal("expected transformed prompt from input guardrail in first model request")
		}
	})
}

func TestTemporalAgent_RunWorkflow_InputGuardrailRejects(t *testing.T) {
	evaluated := 0
	model := core.NewTestModel(core.TextResponse("should not run"))
	agent := core.NewAgent[string](model,
		core.WithInputGuardrail[string]("block", func(_ context.Context, _ string) (string, error) {
			return "", errors.New("blocked")
		}),
		core.WithHooks[string](core.Hook{
			OnGuardrailEvaluated: func(_ context.Context, _ *core.RunContext, name string, passed bool, err error) {
				if name == "block" && !passed && err != nil {
					evaluated++
				}
			},
		}),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-input-guardrail-reject"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "reject me"})
	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("expected workflow error from input guardrail rejection")
	}
	if !strings.Contains(err.Error(), `guardrail "block": blocked`) {
		t.Fatalf("unexpected input guardrail error: %v", err)
	}
	if evaluated != 1 {
		t.Fatalf("expected one failed guardrail evaluation hook, got %d", evaluated)
	}
	if got := len(model.Calls()); got != 0 {
		t.Fatalf("expected model not to run after input guardrail rejection, got %d calls", got)
	}
}

func TestTemporalAgent_RunWorkflow_TurnGuardrailRejects(t *testing.T) {
	evaluated := 0
	model := core.NewTestModel(core.TextResponse("should not run"))
	agent := core.NewAgent[string](model,
		core.WithTurnGuardrail[string]("halt", func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage) error {
			return errors.New("turn blocked")
		}),
		core.WithHooks[string](core.Hook{
			OnGuardrailEvaluated: func(_ context.Context, _ *core.RunContext, name string, passed bool, err error) {
				if name == "halt" && !passed && err != nil {
					evaluated++
				}
			},
		}),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-turn-guardrail-reject"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "reject turn"})
	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("expected workflow error from turn guardrail rejection")
	}
	if !strings.Contains(err.Error(), `guardrail "halt": turn blocked`) {
		t.Fatalf("unexpected turn guardrail error: %v", err)
	}
	if evaluated != 1 {
		t.Fatalf("expected one failed turn guardrail evaluation hook, got %d", evaluated)
	}
	if got := len(model.Calls()); got != 0 {
		t.Fatalf("expected model not to run after turn guardrail rejection, got %d calls", got)
	}
}

func TestTemporalAgent_RunWorkflow_RunConditionTextOutput(t *testing.T) {
	runConditionChecks := 0
	model := core.NewTestModel(core.TextResponse("stop here"))
	agent := core.NewAgent[string](model,
		core.WithRunCondition[string](core.TextContains("stop")),
		core.WithHooks[string](core.Hook{
			OnRunConditionChecked: func(_ context.Context, _ *core.RunContext, stopped bool, reason string) {
				if stopped && reason == "text contains stop" {
					runConditionChecks++
				}
			},
		}),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-run-condition"))

	runWorkflowTest(t, ta, WorkflowInput{Prompt: "respond"}, func(output WorkflowOutput) {
		result, err := ta.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "stop here" {
			t.Fatalf("expected final output %q, got %q", "stop here", result.Output)
		}
		if runConditionChecks != 1 {
			t.Fatalf("expected run condition hook once, got %d", runConditionChecks)
		}
	})
}

func TestTemporalAgent_RunWorkflow_KnowledgeBaseRetrieveAndStore(t *testing.T) {
	kb := core.NewStaticKnowledgeBase("Temporal remembers state.")
	model := core.NewTestModel(core.TextResponse("remember this"))
	agent := core.NewAgent[string](model,
		core.WithKnowledgeBase[string](kb),
		core.WithKnowledgeBaseAutoStore[string](),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-knowledge"))

	runWorkflowTest(t, ta, WorkflowInput{Prompt: "hello"}, func(output WorkflowOutput) {
		result, err := ta.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "remember this" {
			t.Fatalf("expected final output %q, got %q", "remember this", result.Output)
		}

		calls := model.Calls()
		if len(calls) != 1 {
			t.Fatalf("expected 1 model call, got %d", len(calls))
		}
		req, ok := calls[0].Messages[0].(core.ModelRequest)
		if !ok {
			t.Fatalf("expected initial request, got %T", calls[0].Messages[0])
		}
		foundKnowledge := false
		for _, part := range req.Parts {
			if system, ok := part.(core.SystemPromptPart); ok && system.Content == "[Knowledge Context] Temporal remembers state." {
				foundKnowledge = true
			}
		}
		if !foundKnowledge {
			t.Fatal("expected knowledge context in first model request")
		}
		stored := kb.Stored()
		if len(stored) != 1 || stored[0] != "remember this" {
			t.Fatalf("expected stored knowledge %q, got %v", "remember this", stored)
		}
	})
}

func TestTemporalAgent_RunWorkflow_UsageQuota(t *testing.T) {
	type Params struct{}

	model := core.NewTestModel(
		core.ToolCallResponse("echo", `{}`),
		core.TextResponse("done"),
	)
	tool := core.FuncTool[Params]("echo", "Echo", func(_ context.Context, _ Params) (string, error) {
		return "ok", nil
	})
	agent := core.NewAgent[string](model,
		core.WithTools[string](tool),
		core.WithUsageQuota[string](core.UsageQuota{MaxRequests: 1}),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-usage-quota"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)

	env.ExecuteWorkflow(ta.RunWorkflow, WorkflowInput{Prompt: "quota"})
	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("expected usage quota error")
	}
	if !strings.Contains(err.Error(), "usage quota exceeded") {
		t.Fatalf("expected usage quota error, got %v", err)
	}
}

func TestTemporalAgent_RunWorkflow_EventBus(t *testing.T) {
	type Params struct{}
	type customEvent struct {
		Value string
	}

	bus := core.NewEventBus()
	var (
		startEvent    core.RunStartedEvent
		toolEvent     core.ToolCalledEvent
		completeEvent core.RunCompletedEvent
		custom        customEvent
	)
	core.Subscribe(bus, func(e core.RunStartedEvent) { startEvent = e })
	core.Subscribe(bus, func(e core.ToolCalledEvent) { toolEvent = e })
	core.Subscribe(bus, func(e core.RunCompletedEvent) { completeEvent = e })
	core.Subscribe(bus, func(e customEvent) { custom = e })

	tool := core.FuncTool[Params]("echo", "Echo", func(_ context.Context, rc *core.RunContext, _ Params) (string, error) {
		if rc.EventBus == nil {
			return "", errors.New("missing event bus")
		}
		core.Publish(rc.EventBus, customEvent{Value: "from-tool"})
		return "echoed", nil
	})
	model := core.NewTestModel(
		core.ToolCallResponse("echo", `{}`),
		core.TextResponse("done"),
	)
	agent := core.NewAgent[string](model,
		core.WithTools[string](tool),
		core.WithEventBus[string](bus),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-event-bus"))

	runWorkflowTest(t, ta, WorkflowInput{Prompt: "test event bus"}, func(output WorkflowOutput) {
		result, err := ta.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "done" {
			t.Fatalf("expected final output %q, got %q", "done", result.Output)
		}
		if startEvent.Prompt != "test event bus" {
			t.Fatalf("expected run start event prompt %q, got %q", "test event bus", startEvent.Prompt)
		}
		if toolEvent.ToolName != "echo" {
			t.Fatalf("expected tool event for %q, got %q", "echo", toolEvent.ToolName)
		}
		if !completeEvent.Success {
			t.Fatal("expected successful run completed event")
		}
		if custom.Value != "from-tool" {
			t.Fatalf("expected custom tool-published event, got %+v", custom)
		}
	})
}

func TestTemporalAgent_RunWorkflow_AgentDepsAndOverride(t *testing.T) {
	type Params struct{}
	type deps struct {
		APIKey string `json:"api_key"`
	}

	t.Run("default deps", func(t *testing.T) {
		var captured string
		model := core.NewTestModel(
			core.ToolCallResponse("show_deps", `{}`),
			core.TextResponse("done"),
		)
		tool := core.FuncTool[Params]("show_deps", "Show deps", func(_ context.Context, rc *core.RunContext, _ Params) (string, error) {
			captured = core.GetDeps[*deps](rc).APIKey
			return captured, nil
		})
		agent := core.NewAgent[string](model,
			core.WithTools[string](tool),
			core.WithDeps[string](&deps{APIKey: "agent-default"}),
			core.WithDynamicSystemPrompt[string](func(_ context.Context, rc *core.RunContext) (string, error) {
				return "dep=" + core.GetDeps[*deps](rc).APIKey, nil
			}),
		)
		ta := NewTemporalAgent(agent, WithName("workflow-deps-default"))

		runWorkflowTest(t, ta, WorkflowInput{Prompt: "show deps"}, func(output WorkflowOutput) {
			if captured != "agent-default" {
				t.Fatalf("expected tool to see default deps, got %q", captured)
			}
			calls := model.Calls()
			if len(calls) != 2 {
				t.Fatalf("expected 2 model calls, got %d", len(calls))
			}
			req, ok := calls[0].Messages[0].(core.ModelRequest)
			if !ok {
				t.Fatalf("expected first request, got %T", calls[0].Messages[0])
			}
			foundDynamic := false
			for _, part := range req.Parts {
				if system, ok := part.(core.SystemPromptPart); ok && system.Content == "dep=agent-default" {
					foundDynamic = true
				}
			}
			if !foundDynamic {
				t.Fatal("expected dynamic prompt to use default deps")
			}
		})
	})

	t.Run("override deps", func(t *testing.T) {
		var captured string
		model := core.NewTestModel(
			core.ToolCallResponse("show_deps", `{}`),
			core.TextResponse("done"),
		)
		tool := core.FuncTool[Params]("show_deps", "Show deps", func(_ context.Context, rc *core.RunContext, _ Params) (string, error) {
			captured = core.GetDeps[*deps](rc).APIKey
			return captured, nil
		})
		agent := core.NewAgent[string](model,
			core.WithTools[string](tool),
			core.WithDeps[string](&deps{APIKey: "agent-default"}),
			core.WithDynamicSystemPrompt[string](func(_ context.Context, rc *core.RunContext) (string, error) {
				return "dep=" + core.GetDeps[*deps](rc).APIKey, nil
			}),
		)
		ta := NewTemporalAgent(agent, WithName("workflow-deps-override"))
		overrideJSON, err := ta.MarshalDeps(&deps{APIKey: "workflow-override"})
		if err != nil {
			t.Fatalf("marshal deps: %v", err)
		}

		runWorkflowTest(t, ta, WorkflowInput{
			Prompt:   "show deps",
			DepsJSON: overrideJSON,
		}, func(output WorkflowOutput) {
			if captured != "workflow-override" {
				t.Fatalf("expected tool to see override deps, got %q", captured)
			}
			calls := model.Calls()
			if len(calls) != 2 {
				t.Fatalf("expected 2 model calls, got %d", len(calls))
			}
			req, ok := calls[0].Messages[0].(core.ModelRequest)
			if !ok {
				t.Fatalf("expected first request, got %T", calls[0].Messages[0])
			}
			foundDynamic := false
			for _, part := range req.Parts {
				if system, ok := part.(core.SystemPromptPart); ok && system.Content == "dep=workflow-override" {
					foundDynamic = true
				}
			}
			if !foundDynamic {
				t.Fatal("expected dynamic prompt to use override deps")
			}
		})
	})
}

func TestTemporalAgent_RunWorkflow_AutoContext(t *testing.T) {
	type Params struct{}

	longText := strings.Repeat("very long tool output that inflates token counts ", 40)
	summaryModel := core.NewTestModel(core.TextResponse("compressed summary"))
	model := core.NewTestModel(
		core.ToolCallResponse("echo", `{}`),
		core.ToolCallResponse("echo", `{}`),
		core.TextResponse("done"),
	)
	tool := core.FuncTool[Params]("echo", "Echo", func(_ context.Context, _ Params) (string, error) {
		return longText, nil
	})
	agent := core.NewAgent[string](model,
		core.WithTools[string](tool),
		core.WithAutoContext[string](core.AutoContextConfig{
			MaxTokens:    20,
			KeepLastN:    1,
			SummaryModel: summaryModel,
		}),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-auto-context"))

	runWorkflowTest(t, ta, WorkflowInput{Prompt: strings.Repeat("prompt words ", 30)}, func(output WorkflowOutput) {
		result, err := ta.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "done" {
			t.Fatalf("expected final output %q, got %q", "done", result.Output)
		}
		calls := model.Calls()
		if len(calls) != 3 {
			t.Fatalf("expected 3 model calls, got %d", len(calls))
		}
		if got := len(summaryModel.Calls()); got == 0 {
			t.Fatal("expected summary model to run once history exceeded the budget")
		} else if got >= len(calls) {
			t.Fatalf("expected summary model calls to stay below main model calls, got %d summaries for %d main calls", got, len(calls))
		}

		if got := len(result.Messages); got >= 6 {
			t.Fatalf("expected auto-context to reduce persisted message history below 6 messages, got %d", got)
		}
	})
}

func TestTemporalAgent_RunWorkflow_ToolPreparation(t *testing.T) {
	type Params struct{}

	step2Tool := core.FuncTool[Params]("step2_tool", "Step 2 tool", func(_ context.Context, _ Params) (string, error) {
		return "step2", nil
	})
	step2Tool.PrepareFunc = func(_ context.Context, rc *core.RunContext, def core.ToolDefinition) *core.ToolDefinition {
		if rc.RunStep < 2 {
			return nil
		}
		def.Description = "prepared on step 2"
		return &def
	}
	alwaysTool := core.FuncTool[Params]("always_tool", "Always tool", func(_ context.Context, _ Params) (string, error) {
		return "always", nil
	})

	model := core.NewTestModel(
		core.ToolCallResponse("always_tool", `{}`),
		core.TextResponse("done"),
	)
	agent := core.NewAgent[string](model,
		core.WithTools[string](step2Tool, alwaysTool),
		core.WithToolsPrepare[string](func(_ context.Context, _ *core.RunContext, defs []core.ToolDefinition) []core.ToolDefinition {
			filtered := make([]core.ToolDefinition, 0, len(defs))
			for _, def := range defs {
				if def.Name == "always_tool" || def.Name == "step2_tool" {
					filtered = append(filtered, def)
				}
			}
			return filtered
		}),
	)
	ta := NewTemporalAgent(agent, WithName("workflow-tool-prepare"))

	runWorkflowTest(t, ta, WorkflowInput{Prompt: "prepare tools"}, func(output WorkflowOutput) {
		result, err := ta.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "done" {
			t.Fatalf("expected final output %q, got %q", "done", result.Output)
		}

		calls := model.Calls()
		if len(calls) != 2 {
			t.Fatalf("expected 2 model calls, got %d", len(calls))
		}

		firstTools := calls[0].Parameters.FunctionTools
		if len(firstTools) != 1 || firstTools[0].Name != "always_tool" {
			t.Fatalf("expected only always_tool on first request, got %+v", firstTools)
		}

		secondTools := calls[1].Parameters.FunctionTools
		foundPrepared := false
		for _, def := range secondTools {
			if def.Name == "step2_tool" {
				foundPrepared = true
				if def.Description != "prepared on step 2" {
					t.Fatalf("expected prepared description, got %q", def.Description)
				}
			}
		}
		if !foundPrepared {
			t.Fatal("expected step2_tool to be included on the second request")
		}
	})
}

func TestTemporalAgent_RunWorkflow_RequestMiddleware(t *testing.T) {
	modifiedModel := core.NewTestModel(core.TextResponse("done"))
	modifyAgent := core.NewAgent[string](modifiedModel,
		core.WithAgentMiddleware[string](core.RequestOnlyMiddleware(
			func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters, next func(context.Context, []core.ModelMessage, *core.ModelSettings, *core.ModelRequestParameters) (*core.ModelResponse, error)) (*core.ModelResponse, error) {
				modified := make([]core.ModelMessage, 0, len(messages)+1)
				modified = append(modified, core.ModelRequest{
					Parts:     []core.ModelRequestPart{core.SystemPromptPart{Content: "injected by middleware"}},
					Timestamp: time.Now(),
				})
				modified = append(modified, messages...)
				return next(ctx, modified, settings, params)
			},
		)),
	)
	modifyTA := NewTemporalAgent(modifyAgent, WithName("workflow-request-middleware-modify"))

	runWorkflowTest(t, modifyTA, WorkflowInput{Prompt: "middleware"}, func(output WorkflowOutput) {
		result, err := modifyTA.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "done" {
			t.Fatalf("expected final output %q, got %q", "done", result.Output)
		}

		calls := modifiedModel.Calls()
		if len(calls) != 1 {
			t.Fatalf("expected 1 model call, got %d", len(calls))
		}
		if len(calls[0].Messages) < 2 {
			t.Fatalf("expected middleware to inject a leading message, got %d", len(calls[0].Messages))
		}
		req, ok := calls[0].Messages[0].(core.ModelRequest)
		if !ok {
			t.Fatalf("expected injected model request, got %T", calls[0].Messages[0])
		}
		if len(req.Parts) != 1 {
			t.Fatalf("expected 1 injected part, got %d", len(req.Parts))
		}
		system, ok := req.Parts[0].(core.SystemPromptPart)
		if !ok || system.Content != "injected by middleware" {
			t.Fatalf("expected injected system prompt, got %#v", req.Parts[0])
		}
	})

	skippedModel := core.NewTestModel(core.TextResponse("should not be called"))
	skipAgent := core.NewAgent[string](skippedModel,
		core.WithAgentMiddleware[string](core.RequestOnlyMiddleware(
			func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters, _ func(context.Context, []core.ModelMessage, *core.ModelSettings, *core.ModelRequestParameters) (*core.ModelResponse, error)) (*core.ModelResponse, error) {
				return core.TextResponse("intercepted"), nil
			},
		)),
	)
	skipTA := NewTemporalAgent(skipAgent, WithName("workflow-request-middleware-skip"))

	runWorkflowTest(t, skipTA, WorkflowInput{Prompt: "skip"}, func(output WorkflowOutput) {
		result, err := skipTA.DecodeWorkflowOutput(&output)
		if err != nil {
			t.Fatalf("decode workflow output: %v", err)
		}
		if result.Output != "intercepted" {
			t.Fatalf("expected middleware output %q, got %q", "intercepted", result.Output)
		}
		if got := len(skippedModel.Calls()); got != 0 {
			t.Fatalf("expected skipped model to have 0 calls, got %d", got)
		}
	})
}

func runWorkflowTest[T any](t *testing.T, ta *TemporalAgent[T], input WorkflowInput, check func(WorkflowOutput)) {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerTemporalWorkflow(env, ta)

	env.ExecuteWorkflow(ta.RunWorkflow, input)
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
	check(output)
}

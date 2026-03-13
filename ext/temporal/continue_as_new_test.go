package temporal

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/fugue-labs/gollem/core"
)

func executeWorkflowChain[T any](t *testing.T, ta *TemporalAgent[T], input WorkflowInput) WorkflowOutput {
	t.Helper()

	dc := converter.GetDefaultDataConverter()
	for range 8 {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		registerTemporalWorkflow(env, ta)

		env.ExecuteWorkflow(ta.RunWorkflow, input)

		err := env.GetWorkflowError()
		if err == nil {
			var output WorkflowOutput
			if err := env.GetWorkflowResult(&output); err != nil {
				t.Fatalf("workflow result: %v", err)
			}
			return output
		}

		var continueErr *workflow.ContinueAsNewError
		if !errors.As(err, &continueErr) {
			t.Fatalf("workflow error: %v", err)
		}

		var nextInput WorkflowInput
		if err := dc.FromPayloads(continueErr.Input, &nextInput); err != nil {
			t.Fatalf("decode continue-as-new input: %v", err)
		}
		input = nextInput
	}

	t.Fatal("workflow exceeded continue-as-new iteration limit")
	return WorkflowOutput{}
}

func TestTemporalAgent_RunWorkflow_ContinueAsNewPreservesState(t *testing.T) {
	type Params struct{}

	var (
		runStarts      int
		runEnds        int
		inputGuardrail int
	)

	counterState := &workflowCounterTool{}
	counter := core.FuncTool[Params]("counter", "counter", func(_ context.Context, _ Params) (string, error) {
		counterState.count++
		return "counted", nil
	})
	counter.Stateful = counterState

	model := core.NewTestModel(
		core.ToolCallResponseWithID("counter", `{}`, "call_counter"),
		core.TextResponse("done after continue-as-new"),
	)
	agent := core.NewAgent[string](model,
		core.WithTools[string](counter),
		core.WithTracing[string](),
		core.WithInputGuardrail[string]("track", func(_ context.Context, prompt string) (string, error) {
			inputGuardrail++
			return prompt, nil
		}),
		core.WithHooks[string](core.Hook{
			OnRunStart: func(_ context.Context, _ *core.RunContext, _ string) { runStarts++ },
			OnRunEnd: func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage, _ error) {
				runEnds++
			},
		}),
	)
	ta := NewTemporalAgent(agent,
		WithName("workflow-continue"),
		WithContinueAsNew(ContinueAsNewConfig{MaxTurns: 1}),
	)

	output := executeWorkflowChain(t, ta, WorkflowInput{Prompt: "run counter"})
	if !output.Completed {
		t.Fatal("expected workflow to complete after continue-as-new")
	}
	if output.ContinueAsNewCount != 1 {
		t.Fatalf("expected continue-as-new count 1, got %d", output.ContinueAsNewCount)
	}

	result, err := ta.DecodeWorkflowOutput(&output)
	if err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	if result.Output != "done after continue-as-new" {
		t.Fatalf("expected final output %q, got %q", "done after continue-as-new", result.Output)
	}
	if result.Trace == nil {
		t.Fatal("expected trace after continue-as-new")
	}

	state, ok := result.ToolState["counter"].(map[string]any)
	if !ok {
		t.Fatalf("expected counter tool state, got %T", result.ToolState["counter"])
	}
	if got := int(state["count"].(float64)); got != 1 {
		t.Fatalf("expected counter state 1 after continue-as-new, got %d", got)
	}

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 model calls across workflow chain, got %d", len(calls))
	}
	if got := len(calls[1].Messages); got != 3 {
		t.Fatalf("expected 3 messages in second model call after resume, got %d", got)
	}

	var (
		foundToolReturn bool
		userPromptCount int
	)
	for _, msg := range calls[1].Messages {
		req, ok := msg.(core.ModelRequest)
		if !ok {
			continue
		}
		for _, part := range req.Parts {
			switch p := part.(type) {
			case core.ToolReturnPart:
				if p.ToolCallID == "call_counter" {
					foundToolReturn = true
				}
			case core.UserPromptPart:
				userPromptCount++
			}
		}
	}
	if !foundToolReturn {
		t.Fatal("expected continued run to include the prior tool return in the next model request")
	}
	if userPromptCount != 1 {
		t.Fatalf("expected exactly 1 user prompt after continue-as-new resume, got %d", userPromptCount)
	}
	if runStarts != 1 || runEnds != 1 {
		t.Fatalf("expected run lifecycle hooks once across continue-as-new, got starts=%d ends=%d", runStarts, runEnds)
	}
	if inputGuardrail != 1 {
		t.Fatalf("expected input guardrail once across continue-as-new, got %d", inputGuardrail)
	}

	var (
		hasToolCall   bool
		hasToolResult bool
		modelReqs     int
		modelResps    int
	)
	for _, step := range result.Trace.Steps {
		switch step.Kind {
		case core.TraceModelRequest:
			modelReqs++
		case core.TraceModelResponse:
			modelResps++
		case core.TraceToolCall:
			hasToolCall = true
		case core.TraceToolResult:
			hasToolResult = true
		}
	}
	if modelReqs != 2 || modelResps != 2 {
		t.Fatalf("expected 2 model request/response trace steps across continue-as-new, got requests=%d responses=%d", modelReqs, modelResps)
	}
	if !hasToolCall || !hasToolResult {
		t.Fatalf("expected tool trace steps across continue-as-new, got %+v", result.Trace.Steps)
	}
}

func TestNewWorkflowRunState_SnapshotToolRetriesMapRemainsWritable(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		start := workflow.Now(ctx).Add(-time.Minute)
		snap := &core.RunSnapshot{
			RunID:        "resume-run",
			RunStartTime: start,
			Prompt:       "resume",
		}
		encoded, err := core.EncodeRunSnapshot(snap)
		if err != nil {
			return err
		}
		state, err := newWorkflowRunState(ctx, WorkflowInput{
			Prompt:   "resume",
			Snapshot: encoded,
		})
		if err != nil {
			return err
		}
		state.ToolRetries["retry_tool"] = 1
		if got := state.ToolRetries["retry_tool"]; got != 1 {
			return errors.New("tool retry map write failed")
		}
		return nil
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
}

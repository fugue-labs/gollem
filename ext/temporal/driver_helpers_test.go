package temporal

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/fugue-labs/gollem/core"
)

type helperOutput struct {
	Answer string `json:"answer"`
}

func TestInitialRequestPartsHelpers(t *testing.T) {
	now := time.Unix(1, 0).UTC()
	parts := []core.ModelRequestPart{
		core.SystemPromptPart{Content: "system", Timestamp: now},
		core.UserPromptPart{Content: "hello", Timestamp: now},
	}

	data, err := MarshalInitialRequestParts(parts)
	if err != nil {
		t.Fatalf("marshal initial request parts: %v", err)
	}

	decoded, err := UnmarshalInitialRequestParts(data)
	if err != nil {
		t.Fatalf("unmarshal initial request parts: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 decoded parts, got %d", len(decoded))
	}

	encodedParts, err := core.EncodeRequestParts(parts)
	if err != nil {
		t.Fatalf("encode request parts: %v", err)
	}
	decodedFromStructured, err := unmarshalInitialRequestParts(encodedParts, nil)
	if err != nil {
		t.Fatalf("unmarshal structured initial request parts: %v", err)
	}
	if len(decodedFromStructured) != 2 {
		t.Fatalf("expected 2 structured decoded parts, got %d", len(decodedFromStructured))
	}

	twoRequests, err := core.MarshalMessages([]core.ModelMessage{
		core.ModelRequest{Parts: parts},
		core.ModelRequest{Parts: parts},
	})
	if err != nil {
		t.Fatalf("marshal two requests: %v", err)
	}
	if _, err := UnmarshalInitialRequestParts(twoRequests); err == nil {
		t.Fatal("expected error for multiple wrapper messages")
	}

	responseWrapper, err := core.MarshalMessages([]core.ModelMessage{
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "nope"}}},
	})
	if err != nil {
		t.Fatalf("marshal response wrapper: %v", err)
	}
	if _, err := UnmarshalInitialRequestParts(responseWrapper); err == nil {
		t.Fatal("expected error for non-request wrapper")
	}
}

func TestInjectDeferredResults(t *testing.T) {
	now := time.Unix(5, 0).UTC()

	mergedState := &workflowRunState{
		Messages: []core.ModelMessage{
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.UserPromptPart{Content: "start", Timestamp: now},
				},
				Timestamp: now,
			},
		},
	}
	injectDeferredResults(mergedState, []core.DeferredToolResult{
		{ToolName: "search", ToolCallID: "call_1", Content: "done"},
		{ToolName: "search", ToolCallID: "call_2", Content: "retry me", IsError: true},
	}, now)

	req, ok := mergedState.Messages[0].(core.ModelRequest)
	if !ok {
		t.Fatalf("expected merged request, got %T", mergedState.Messages[0])
	}
	if len(req.Parts) != 3 {
		t.Fatalf("expected merged request to have 3 parts, got %d", len(req.Parts))
	}
	if _, ok := req.Parts[1].(core.ToolReturnPart); !ok {
		t.Fatalf("expected tool return part, got %T", req.Parts[1])
	}
	if _, ok := req.Parts[2].(core.RetryPromptPart); !ok {
		t.Fatalf("expected retry prompt part, got %T", req.Parts[2])
	}

	appendedState := &workflowRunState{
		Messages: []core.ModelMessage{
			core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "assistant"}}},
		},
	}
	injectDeferredResults(appendedState, []core.DeferredToolResult{
		{ToolName: "search", ToolCallID: "call_3", Content: "later"},
	}, now)

	if len(appendedState.Messages) != 2 {
		t.Fatalf("expected appended request message, got %d messages", len(appendedState.Messages))
	}
	if _, ok := appendedState.Messages[1].(core.ModelRequest); !ok {
		t.Fatalf("expected appended request, got %T", appendedState.Messages[1])
	}
}

func TestDriverDecodeAndRetryHelpers(t *testing.T) {
	if _, err := decodeTemporalOutput[helperOutput](nil); err == nil {
		t.Fatal("expected error for empty workflow output")
	}
	if _, err := decodeTemporalOutput[helperOutput]([]byte(`{`)); err == nil {
		t.Fatal("expected error for invalid workflow output JSON")
	}
	decoded, err := decodeTemporalOutput[helperOutput]([]byte(`{"answer":"ok"}`))
	if err != nil {
		t.Fatalf("decode temporal output: %v", err)
	}
	if decoded.Answer != "ok" {
		t.Fatalf("unexpected decoded output: %+v", decoded)
	}

	structured, err := decodeStructuredOutput[helperOutput](`{"result":{"answer":"wrapped"}}`, "result")
	if err != nil {
		t.Fatalf("decode structured output: %v", err)
	}
	if structured.Answer != "wrapped" {
		t.Fatalf("unexpected structured output: %+v", structured)
	}
	if _, err := decodeStructuredOutput[helperOutput](`{"other":{"answer":"wrapped"}}`, "result"); err == nil {
		t.Fatal("expected error for missing wrapper key")
	}
	if _, err := decodeStructuredOutput[helperOutput](`{`, "result"); err == nil {
		t.Fatal("expected error for invalid structured output JSON")
	}

	start := time.Unix(10, 0).UTC()
	if got := deterministicWorkflowDuration(start, start); got != time.Nanosecond {
		t.Fatalf("expected 1ns duration for equal timestamps, got %v", got)
	}
	if got := deterministicWorkflowDuration(start, start.Add(-time.Second)); got != 0 {
		t.Fatalf("expected zero duration for reversed timestamps, got %v", got)
	}

	if got := availableToolNames(core.AgentRuntimeConfig[string]{}); got != "(none)" {
		t.Fatalf("expected no tool names, got %q", got)
	}
	names := availableToolNames(core.AgentRuntimeConfig[string]{
		Tools: []core.Tool{
			{Definition: core.ToolDefinition{Name: "lookup"}},
		},
		OutputSchema: &core.OutputSchema{
			OutputTools: []core.ToolDefinition{{Name: "final_result"}},
		},
	})
	if names != "lookup, final_result" {
		t.Fatalf("unexpected tool names %q", names)
	}

	if last := lastWorkflowModelResponse(nil); last != nil {
		t.Fatalf("expected nil last response, got %+v", last)
	}
	last := lastWorkflowModelResponse([]core.ModelMessage{
		core.ModelRequest{},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "first"}}},
		core.ModelRequest{},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "second"}}},
	})
	if last == nil || last.TextContent() != "second" {
		t.Fatalf("unexpected last response: %+v", last)
	}

	state := &workflowRunState{}
	if err := incrementWorkflowRetries(state, 2); err != nil {
		t.Fatalf("unexpected retry increment error: %v", err)
	}
	if state.Retries != 1 {
		t.Fatalf("expected retries to increment to 1, got %d", state.Retries)
	}

	state = &workflowRunState{}
	err = incrementWorkflowRetries(state, 0)
	var unexpected *core.UnexpectedModelBehavior
	if !errors.As(err, &unexpected) {
		t.Fatalf("expected unexpected model behavior, got %T: %v", err, err)
	}

	state = &workflowRunState{
		Messages: []core.ModelMessage{
			core.ModelResponse{
				FinishReason: core.FinishReasonLength,
				Parts: []core.ModelResponsePart{
					core.ToolCallPart{ToolName: "lookup", ToolCallID: "call_1", ArgsJSON: `{"bad"`},
				},
			},
		},
	}
	err = incrementWorkflowRetries(state, 0)
	var incomplete *core.IncompleteToolCall
	if !errors.As(err, &incomplete) {
		t.Fatalf("expected incomplete tool call, got %T: %v", err, err)
	}
}

func TestBuildActivityOptionsAndWorkflowHelpers(t *testing.T) {
	opts := buildActivityOptions(ActivityConfig{
		MaxRetries: maxTemporalActivityAttempts + 25,
	})
	if opts.RetryPolicy == nil {
		t.Fatal("expected retry policy")
	}
	if opts.RetryPolicy.MaximumAttempts != int32(maxTemporalActivityAttempts) {
		t.Fatalf("expected attempts to clamp at %d, got %d", maxTemporalActivityAttempts, opts.RetryPolicy.MaximumAttempts)
	}
	if opts.StartToCloseTimeout != DefaultActivityConfig().StartToCloseTimeout {
		t.Fatalf("expected default timeout, got %v", opts.StartToCloseTimeout)
	}
	if opts.RetryPolicy.InitialInterval != DefaultActivityConfig().InitialInterval {
		t.Fatalf("expected default retry interval, got %v", opts.RetryPolicy.InitialInterval)
	}

	now := time.Unix(15, 0).UTC()
	req := buildInitialWorkflowRequest(
		[]string{"system-1", "system-2"},
		"hello",
		[]core.ModelRequestPart{core.RetryPromptPart{Content: "retry", Timestamp: now}},
		now,
	)
	if len(req.Parts) != 4 {
		t.Fatalf("expected 4 initial request parts, got %d", len(req.Parts))
	}
	if got := req.Parts[2].(core.UserPromptPart).Content; got != "hello" {
		t.Fatalf("unexpected user prompt content %q", got)
	}

	cost := buildWorkflowCostSnapshot(true, "test-model", map[string]core.ModelPricing{
		"test-model": {
			InputTokenCost:  0.001,
			OutputTokenCost: 0.002,
		},
	}, "EUR", core.RunUsage{Usage: core.Usage{InputTokens: 2, OutputTokens: 3}})
	if cost == nil || cost.Currency != "EUR" || cost.TotalCost <= 0 {
		t.Fatalf("unexpected workflow cost snapshot: %+v", cost)
	}
	if got := buildWorkflowCostSnapshot(false, "test-model", nil, "", core.RunUsage{}); got != nil {
		t.Fatalf("expected nil workflow cost without tracker, got %+v", got)
	}
}

func TestBuildWorkflowRequestParamsFromDefinitions(t *testing.T) {
	params := buildWorkflowRequestParamsFromDefinitions(core.AgentRuntimeConfig[string]{
		OutputSchema: &core.OutputSchema{
			Mode:       core.OutputModeTool,
			AllowsText: true,
			OutputTools: []core.ToolDefinition{
				{Name: "final_result", Kind: core.ToolKindOutput},
			},
		},
	}, []core.ToolDefinition{{Name: "lookup", Kind: core.ToolKindFunction}})

	if params.OutputMode != core.OutputModeTool || !params.AllowTextOutput {
		t.Fatalf("unexpected output params: %+v", params)
	}
	if len(params.FunctionTools) != 1 || params.FunctionTools[0].Name != "lookup" {
		t.Fatalf("unexpected function tools: %+v", params.FunctionTools)
	}
	if len(params.OutputTools) != 1 || params.OutputTools[0].Name != "final_result" {
		t.Fatalf("unexpected output tools: %+v", params.OutputTools)
	}
}

func TestNewWorkflowRunStateDecodesSnapshotAndTraceSteps(t *testing.T) {
	now := time.Unix(20, 0).UTC()
	traceRaw, err := json.Marshal([]core.TraceStep{{Kind: core.TraceToolCall, Timestamp: now}})
	if err != nil {
		t.Fatalf("marshal trace steps: %v", err)
	}
	snapshot, err := core.EncodeRunSnapshot(&core.RunSnapshot{
		Messages: []core.ModelMessage{
			core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "prompt", Timestamp: now}}},
		},
		Usage:           core.RunUsage{Requests: 2},
		LastInputTokens: 11,
		Retries:         1,
		ToolRetries:     map[string]int{"lookup": 2},
		RunID:           "snap-run",
		RunStep:         3,
		RunStartTime:    now.Add(-time.Minute),
		Prompt:          "prompt",
		ToolState:       map[string]any{"lookup": map[string]any{"count": 1}},
		Timestamp:       now,
	})
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}

	input := WorkflowInput{
		Snapshot:       snapshot,
		TraceStepsJSON: traceRaw,
		DepsJSON:       []byte(`{"tenant":"acme"}`),
	}

	type summary struct {
		RunID       string
		RunStep     int
		TraceKinds  []string
		ToolRetries map[string]int
		DepsJSON    string
	}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartTime(now)
	env.ExecuteWorkflow(func(ctx workflow.Context) (summary, error) {
		state, err := newWorkflowRunState(ctx, input)
		if err != nil {
			return summary{}, err
		}
		result := summary{
			RunID:       state.RunID,
			RunStep:     state.RunStep,
			ToolRetries: state.ToolRetries,
			DepsJSON:    string(state.DepsJSON),
		}
		for _, step := range state.TraceSteps {
			result.TraceKinds = append(result.TraceKinds, string(step.Kind))
		}
		return result, nil
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var got summary
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("get workflow result: %v", err)
	}
	if got.RunID != "snap-run" || got.RunStep != 3 {
		t.Fatalf("unexpected restored run state summary: %+v", got)
	}
	if len(got.TraceKinds) != 1 || got.TraceKinds[0] != string(core.TraceToolCall) {
		t.Fatalf("unexpected restored trace kinds: %+v", got.TraceKinds)
	}
	if got.ToolRetries["lookup"] != 2 {
		t.Fatalf("unexpected restored tool retries: %+v", got.ToolRetries)
	}
	if got.DepsJSON != `{"tenant":"acme"}` {
		t.Fatalf("unexpected deps json %q", got.DepsJSON)
	}
}

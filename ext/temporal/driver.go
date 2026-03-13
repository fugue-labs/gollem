package temporal

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/fugue-labs/gollem/core"
)

const maxTemporalActivityAttempts = 1<<31 - 1

type workflowRunState struct {
	Messages                      []core.ModelMessage
	Usage                         core.RunUsage
	DepsJSON                      []byte
	LastInputTokens               int
	TraceSteps                    []core.TraceStep
	Retries                       int
	ToolRetries                   map[string]int
	RunStep                       int
	RunID                         string
	RunStartTime                  time.Time
	ToolState                     map[string]any
	ContinueAsNewCount            int
	LastContinueAsNewReason       string
	ContinueAsNewBaseRunStep      int
	ContinueAsNewBaseMessageCount int
}

func newWorkflowRunState(ctx workflow.Context, input WorkflowInput) (*workflowRunState, error) {
	state := &workflowRunState{
		ToolRetries:                   make(map[string]int),
		RunID:                         workflow.GetInfo(ctx).WorkflowExecution.ID,
		RunStartTime:                  workflow.Now(ctx),
		DepsJSON:                      append([]byte(nil), input.DepsJSON...),
		ContinueAsNewCount:            input.ContinueAsNewCount,
		ContinueAsNewBaseRunStep:      input.ContinueAsNewBaseRunStep,
		ContinueAsNewBaseMessageCount: input.ContinueAsNewBaseMessageCount,
	}

	traceSteps, err := decodeTraceSteps(input.TraceSteps, input.TraceStepsJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal workflow trace steps: %w", err)
	}
	state.TraceSteps = traceSteps

	if input.Snapshot == nil && len(input.SnapshotJSON) == 0 {
		return state, nil
	}

	snap, err := decodeSerializedSnapshot(input.Snapshot, input.SnapshotJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal workflow snapshot: %w", err)
	}
	state.Messages = cloneMessages(snap.Messages)
	state.Usage = snap.Usage
	state.LastInputTokens = snap.LastInputTokens
	state.Retries = snap.Retries
	state.ToolRetries = cloneIntMap(snap.ToolRetries)
	if state.ToolRetries == nil {
		state.ToolRetries = make(map[string]int)
	}
	if snap.RunID != "" {
		state.RunID = snap.RunID
	}
	if !snap.RunStartTime.IsZero() {
		state.RunStartTime = snap.RunStartTime
	}
	state.RunStep = snap.RunStep
	state.ToolState = cloneAnyMap(snap.ToolState)
	return state, nil
}

func (s *workflowRunState) snapshotJSON(prompt string, now time.Time) ([]byte, error) {
	snap := &core.RunSnapshot{
		Messages:        cloneMessages(s.Messages),
		Usage:           s.Usage,
		LastInputTokens: s.LastInputTokens,
		Retries:         s.Retries,
		ToolRetries:     cloneIntMap(s.ToolRetries),
		RunID:           s.RunID,
		RunStep:         s.RunStep,
		RunStartTime:    s.RunStartTime,
		Prompt:          prompt,
		ToolState:       cloneAnyMap(s.ToolState),
		Timestamp:       now,
	}
	return core.MarshalSnapshot(snap)
}

func buildActivityOptions(cfg ActivityConfig) workflow.ActivityOptions {
	normalized := cfg
	if normalized.StartToCloseTimeout <= 0 {
		normalized.StartToCloseTimeout = DefaultActivityConfig().StartToCloseTimeout
	}
	if normalized.InitialInterval <= 0 {
		normalized.InitialInterval = DefaultActivityConfig().InitialInterval
	}
	maximumAttempts := normalized.MaxRetries + 1
	if maximumAttempts < 1 {
		maximumAttempts = 1
	}
	if maximumAttempts > maxTemporalActivityAttempts {
		maximumAttempts = maxTemporalActivityAttempts
	}
	return workflow.ActivityOptions{
		StartToCloseTimeout: normalized.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: int32(maximumAttempts),
			InitialInterval: normalized.InitialInterval,
		},
	}
}

func buildWorkflowRequestParamsFromDefinitions[T any](config core.AgentRuntimeConfig[T], defs []core.ToolDefinition) *core.ModelRequestParameters {
	params := &core.ModelRequestParameters{
		OutputMode:      config.OutputSchema.Mode,
		AllowTextOutput: config.OutputSchema.AllowsText,
		OutputObject:    config.OutputSchema.OutputObject,
	}
	params.FunctionTools = append(params.FunctionTools, defs...)
	params.OutputTools = append(params.OutputTools, config.OutputSchema.OutputTools...)
	return params
}

func buildWorkflowCost[T any](config core.AgentRuntimeConfig[T], usage core.RunUsage) *core.RunCost {
	return buildWorkflowCostSnapshot(config.HasCostTracker, config.ModelName, config.CostPricing, config.CostCurrency, usage)
}

func buildInitialWorkflowRequest(systemPrompts []string, prompt string, initialParts []core.ModelRequestPart, now time.Time) core.ModelRequest {
	parts := make([]core.ModelRequestPart, 0, len(systemPrompts)+1+len(initialParts))
	for _, promptText := range systemPrompts {
		parts = append(parts, core.SystemPromptPart{
			Content:   promptText,
			Timestamp: now,
		})
	}
	parts = append(parts, core.UserPromptPart{
		Content:   prompt,
		Timestamp: now,
	})
	parts = append(parts, initialParts...)
	return core.ModelRequest{
		Parts:     parts,
		Timestamp: now,
	}
}

// MarshalInitialRequestParts serializes initial request parts for WorkflowInput.
func MarshalInitialRequestParts(parts []core.ModelRequestPart) ([]byte, error) {
	if len(parts) == 0 {
		return nil, nil
	}
	messagesJSON, err := core.MarshalMessages([]core.ModelMessage{
		core.ModelRequest{Parts: parts},
	})
	if err != nil {
		return nil, err
	}
	return messagesJSON, nil
}

// UnmarshalInitialRequestParts deserializes initial request parts from WorkflowInput.
func UnmarshalInitialRequestParts(data []byte) ([]core.ModelRequestPart, error) {
	if len(data) == 0 {
		return nil, nil
	}
	messages, err := core.UnmarshalMessages(data)
	if err != nil {
		return nil, err
	}
	if len(messages) != 1 {
		return nil, fmt.Errorf("expected 1 initial request wrapper message, got %d", len(messages))
	}
	req, ok := messages[0].(core.ModelRequest)
	if !ok {
		return nil, fmt.Errorf("expected initial request wrapper, got %T", messages[0])
	}
	return req.Parts, nil
}

func unmarshalInitialRequestParts(parts []core.SerializedPart, data []byte) ([]core.ModelRequestPart, error) {
	if len(parts) > 0 {
		return core.DecodeRequestParts(parts)
	}
	return UnmarshalInitialRequestParts(data)
}

func injectDeferredResults(state *workflowRunState, results []core.DeferredToolResult, now time.Time) {
	var deferredParts []core.ModelRequestPart
	for _, dr := range results {
		if dr.IsError {
			deferredParts = append(deferredParts, core.RetryPromptPart{
				Content:    dr.Content,
				ToolName:   dr.ToolName,
				ToolCallID: dr.ToolCallID,
				Timestamp:  now,
			})
		} else {
			deferredParts = append(deferredParts, core.ToolReturnPart{
				ToolName:   dr.ToolName,
				Content:    dr.Content,
				ToolCallID: dr.ToolCallID,
				Timestamp:  now,
			})
		}
	}

	merged := false
	if lastIdx := len(state.Messages) - 1; lastIdx >= 0 {
		if lastReq, ok := state.Messages[lastIdx].(core.ModelRequest); ok {
			lastReq.Parts = append(lastReq.Parts, deferredParts...)
			state.Messages[lastIdx] = lastReq
			merged = true
		}
	}
	if !merged {
		state.Messages = append(state.Messages, core.ModelRequest{
			Parts:     deferredParts,
			Timestamp: now,
		})
	}
}

func deterministicWorkflowDuration(start, end time.Time) time.Duration {
	if end.After(start) {
		return end.Sub(start)
	}
	if end.Equal(start) && !start.IsZero() {
		return time.Nanosecond
	}
	return 0
}

func cloneTraceSteps(src []core.TraceStep) []core.TraceStep {
	if len(src) == 0 {
		return nil
	}
	cloned := make([]core.TraceStep, len(src))
	copy(cloned, src)
	return cloned
}

func buildWorkflowCostSnapshot(hasCostTracker bool, modelName string, pricing map[string]core.ModelPricing, currency string, usage core.RunUsage) *core.RunCost {
	if !hasCostTracker {
		return nil
	}
	cost := core.EstimateRunCost(modelName, usage, pricing)
	if cost != nil && currency != "" {
		cost.Currency = currency
	}
	return cost
}

func cloneMessages(messages []core.ModelMessage) []core.ModelMessage {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]core.ModelMessage, len(messages))
	copy(cloned, messages)
	return cloned
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(src))
	for k, v := range src {
		cloned[k] = v
	}
	return cloned
}

func cloneIntMap(src map[string]int) map[string]int {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]int, len(src))
	for k, v := range src {
		cloned[k] = v
	}
	return cloned
}

func cloneModelPricingMap(src map[string]core.ModelPricing) map[string]core.ModelPricing {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]core.ModelPricing, len(src))
	for modelName, pricing := range src {
		cloned[modelName] = pricing
	}
	return cloned
}

func decodeTemporalOutput[T any](data []byte) (T, error) {
	var result T
	if len(data) == 0 {
		var zero T
		return zero, errors.New("empty workflow output")
	}
	if err := json.Unmarshal(data, &result); err != nil {
		var zero T
		return zero, fmt.Errorf("unmarshal workflow output: %w", err)
	}
	return result, nil
}

func decodeStructuredOutput[T any](raw string, outerKey string) (T, error) {
	var zero T

	data := []byte(raw)
	if outerKey != "" {
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return zero, fmt.Errorf("failed to unmarshal output wrapper: %w", err)
		}
		inner, ok := wrapper[outerKey]
		if !ok {
			return zero, fmt.Errorf("output wrapper missing key %q", outerKey)
		}
		data = inner
	}

	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		return zero, fmt.Errorf("failed to unmarshal output: %w", err)
	}
	return result, nil
}

func availableToolNames[T any](config core.AgentRuntimeConfig[T]) string {
	var names []string
	for _, tool := range config.Tools {
		names = append(names, tool.Definition.Name)
	}
	if config.OutputSchema != nil {
		for _, tool := range config.OutputSchema.OutputTools {
			names = append(names, tool.Name)
		}
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

func incrementWorkflowRetries(state *workflowRunState, maxRetries int) error {
	state.Retries++
	if state.Retries > maxRetries {
		if lastResp := lastWorkflowModelResponse(state.Messages); lastResp != nil {
			if lastResp.FinishReason == core.FinishReasonLength {
				toolCalls := lastResp.ToolCalls()
				if len(toolCalls) > 0 {
					lastCall := toolCalls[len(toolCalls)-1]
					if _, err := lastCall.ArgsAsMap(); err != nil {
						return &core.IncompleteToolCall{
							UnexpectedModelBehavior: core.UnexpectedModelBehavior{
								Message: "model hit token limit while generating tool call arguments, consider increasing max_tokens",
							},
						}
					}
				}
			}
		}

		return &core.UnexpectedModelBehavior{
			Message: fmt.Sprintf("exceeded maximum retries (%d) for result validation", maxRetries),
		}
	}
	return nil
}

func lastWorkflowModelResponse(messages []core.ModelMessage) *core.ModelResponse {
	for i := len(messages) - 1; i >= 0; i-- {
		if resp, ok := messages[i].(core.ModelResponse); ok {
			return &resp
		}
	}
	return nil
}

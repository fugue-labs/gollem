package temporal

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/fugue-labs/gollem/core"
)

// WorkflowInput is the serializable input to a durable Temporal agent run.
type WorkflowInput struct {
	Prompt                        string                      `json:"prompt"`
	Snapshot                      *core.SerializedRunSnapshot `json:"snapshot,omitempty"`
	SnapshotJSON                  json.RawMessage             `json:"snapshot_json,omitempty"` // Deprecated: prefer Snapshot.
	TraceSteps                    []core.TraceStep            `json:"trace_steps,omitempty"`
	TraceStepsJSON                json.RawMessage             `json:"trace_steps_json,omitempty"` // Deprecated: prefer TraceSteps.
	InitialRequestParts           []core.SerializedPart       `json:"initial_request_parts,omitempty"`
	InitialRequestPartsJSON       json.RawMessage             `json:"initial_request_parts_json,omitempty"` // Deprecated: prefer InitialRequestParts.
	DepsJSON                      []byte                      `json:"deps_json,omitempty"`
	DeferredResults               []core.DeferredToolResult   `json:"deferred_results,omitempty"`
	ModelSettings                 *core.ModelSettings         `json:"model_settings,omitempty"`
	UsageLimits                   *core.UsageLimits           `json:"usage_limits,omitempty"`
	ContinueAsNewCount            int                         `json:"continue_as_new_count,omitempty"`
	ContinueAsNewBaseRunStep      int                         `json:"continue_as_new_base_run_step,omitempty"`
	ContinueAsNewBaseMessageCount int                         `json:"continue_as_new_base_message_count,omitempty"`
}

// WorkflowOutput is the serializable result of a durable Temporal agent run.
type WorkflowOutput struct {
	Completed          bool                        `json:"completed"`
	OutputJSON         json.RawMessage             `json:"output_json,omitempty"`
	Snapshot           *core.SerializedRunSnapshot `json:"snapshot,omitempty"`
	SnapshotJSON       json.RawMessage             `json:"snapshot_json,omitempty"` // Deprecated: prefer Snapshot.
	Trace              *core.RunTrace              `json:"trace,omitempty"`
	TraceJSON          json.RawMessage             `json:"trace_json,omitempty"` // Deprecated: prefer Trace.
	Cost               *core.RunCost               `json:"cost,omitempty"`
	DeferredRequests   []core.DeferredToolRequest  `json:"deferred_requests,omitempty"`
	ContinueAsNewCount int                         `json:"continue_as_new_count,omitempty"`
}

type workflowSignalState struct {
	status           WorkflowStatus
	workflowName     string
	registrationName string
	version          string
	approvalCh       workflow.ReceiveChannel
	deferredCh       workflow.ReceiveChannel
	abortCh          workflow.ReceiveChannel
	approvals        map[string]ApprovalSignal
	deferredResults  map[string]DeferredResultSignal
	abortReason      string
	tracingEnabled   bool
	hasCostTracker   bool
	modelName        string
	costPricing      map[string]core.ModelPricing
	costCurrency     string
}

type workflowToolCall struct {
	idx             int
	call            core.ToolCallPart
	tool            TemporalTool
	usageCounted    bool
	eventPublished  bool
	waitingDeferred bool
	traceStart      time.Time
}

func isContinueAsNewResume(input WorkflowInput) bool {
	return input.ContinueAsNewCount > 0 ||
		input.ContinueAsNewBaseRunStep > 0 ||
		input.ContinueAsNewBaseMessageCount > 0
}

func newWorkflowSignalState[T any](ctx workflow.Context, ta *TemporalAgent[T]) *workflowSignalState {
	return &workflowSignalState{
		workflowName:     ta.WorkflowName(),
		registrationName: ta.RegistrationName(),
		version:          ta.Version(),
		approvalCh:       workflow.GetSignalChannel(ctx, workflowApprovalSignalName),
		deferredCh:       workflow.GetSignalChannel(ctx, workflowDeferredSignalName),
		abortCh:          workflow.GetSignalChannel(ctx, workflowAbortSignalName),
		approvals:        make(map[string]ApprovalSignal),
		deferredResults:  make(map[string]DeferredResultSignal),
		tracingEnabled:   ta.runtime.TracingEnabled,
		hasCostTracker:   ta.runtime.HasCostTracker,
		modelName:        ta.runtime.ModelName,
		costPricing:      cloneModelPricingMap(ta.runtime.CostPricing),
		costCurrency:     ta.runtime.CostCurrency,
	}
}

func (s *workflowSignalState) registerQuery(ctx workflow.Context) error {
	return workflow.SetQueryHandler(ctx, workflowStatusQueryName, func() (WorkflowStatus, error) {
		return s.status, nil
	})
}

func (s *workflowSignalState) drainSignals() {
	for {
		received := false

		var approval ApprovalSignal
		if s.approvalCh.ReceiveAsync(&approval) {
			s.approvals[approval.ToolCallID] = approval
			received = true
		}

		var deferred DeferredResultSignal
		if s.deferredCh.ReceiveAsync(&deferred) {
			s.deferredResults[deferred.ToolCallID] = deferred
			received = true
		}

		var abort AbortSignal
		if s.abortCh.ReceiveAsync(&abort) {
			s.abortReason = abort.Reason
			received = true
		}

		if !received {
			return
		}
	}
}

func (s *workflowSignalState) refreshStatus(
	ctx workflow.Context,
	state *workflowRunState,
	prompt string,
	waitingReason string,
	pendingApprovals []ToolApprovalRequest,
	pendingDeferred []core.DeferredToolRequest,
	completed bool,
	lastErr error,
	aborted bool,
) error {
	messages, err := core.EncodeMessages(state.Messages)
	if err != nil {
		return fmt.Errorf("marshal workflow status messages: %w", err)
	}
	now := workflow.Now(ctx)
	snapshot, err := core.EncodeRunSnapshot(&core.RunSnapshot{
		Messages:        cloneMessages(state.Messages),
		Usage:           state.Usage,
		LastInputTokens: state.LastInputTokens,
		Retries:         state.Retries,
		ToolRetries:     cloneIntMap(state.ToolRetries),
		RunID:           state.RunID,
		RunStep:         state.RunStep,
		RunStartTime:    state.RunStartTime,
		Prompt:          prompt,
		ToolState:       cloneAnyMap(state.ToolState),
		Timestamp:       now,
	})
	if err != nil {
		return fmt.Errorf("encode workflow status snapshot: %w", err)
	}
	var trace *core.RunTrace
	if s.tracingEnabled {
		trace = &core.RunTrace{
			RunID:     state.RunID,
			Prompt:    prompt,
			StartTime: state.RunStartTime,
			EndTime:   now,
			Duration:  deterministicWorkflowDuration(state.RunStartTime, now),
			Steps:     cloneTraceSteps(state.TraceSteps),
			Usage:     state.Usage,
			Success:   completed && lastErr == nil && !aborted,
		}
		if lastErr != nil {
			trace.Error = lastErr.Error()
		}
	}
	info := workflow.GetInfo(ctx)

	status := WorkflowStatus{
		RunID:                   state.RunID,
		RunStep:                 state.RunStep,
		Usage:                   state.Usage,
		WorkflowName:            s.workflowName,
		RegistrationName:        s.registrationName,
		Version:                 s.version,
		Messages:                messages,
		Snapshot:                snapshot,
		Trace:                   trace,
		Cost:                    buildWorkflowCostSnapshot(s.hasCostTracker, s.modelName, s.costPricing, s.costCurrency, state.Usage),
		PendingApprovals:        append([]ToolApprovalRequest(nil), pendingApprovals...),
		DeferredRequests:        append([]core.DeferredToolRequest(nil), pendingDeferred...),
		Waiting:                 waitingReason != "",
		WaitingReason:           waitingReason,
		Completed:               completed,
		Aborted:                 aborted,
		ContinueAsNewCount:      state.ContinueAsNewCount,
		CurrentHistoryLength:    info.GetCurrentHistoryLength(),
		CurrentHistorySize:      info.GetCurrentHistorySize(),
		ContinueAsNewSuggested:  info.GetContinueAsNewSuggested(),
		LastContinueAsNewReason: state.LastContinueAsNewReason,
	}
	if lastErr != nil {
		status.LastError = lastErr.Error()
	}

	s.status = status
	return nil
}

func (s *workflowSignalState) waitForExternalInput(
	ctx workflow.Context,
	state *workflowRunState,
	prompt string,
	pendingApprovals []ToolApprovalRequest,
	pendingDeferred []core.DeferredToolRequest,
) error {
	for {
		s.drainSignals()

		if s.abortReason != "" {
			err := workflowAbortError(s.abortReason)
			if statusErr := s.refreshStatus(ctx, state, prompt, waitingReason(pendingApprovals, pendingDeferred), pendingApprovals, pendingDeferred, false, err, true); statusErr != nil {
				return statusErr
			}
			return err
		}

		pendingApprovals = s.filterPendingApprovals(pendingApprovals)
		pendingDeferred = s.filterPendingDeferred(pendingDeferred)
		if len(pendingApprovals) == 0 && len(pendingDeferred) == 0 {
			return s.refreshStatus(ctx, state, prompt, "", nil, nil, false, nil, false)
		}

		if err := s.refreshStatus(ctx, state, prompt, waitingReason(pendingApprovals, pendingDeferred), pendingApprovals, pendingDeferred, false, nil, false); err != nil {
			return err
		}

		selector := workflow.NewSelector(ctx)
		selector.AddReceive(s.approvalCh, func(ch workflow.ReceiveChannel, more bool) {
			var approval ApprovalSignal
			ch.Receive(ctx, &approval)
			s.approvals[approval.ToolCallID] = approval
		})
		selector.AddReceive(s.deferredCh, func(ch workflow.ReceiveChannel, more bool) {
			var deferred DeferredResultSignal
			ch.Receive(ctx, &deferred)
			s.deferredResults[deferred.ToolCallID] = deferred
		})
		selector.AddReceive(s.abortCh, func(ch workflow.ReceiveChannel, more bool) {
			var abort AbortSignal
			ch.Receive(ctx, &abort)
			s.abortReason = abort.Reason
		})
		selector.Select(ctx)
	}
}

func (s *workflowSignalState) filterPendingApprovals(requests []ToolApprovalRequest) []ToolApprovalRequest {
	if len(requests) == 0 {
		return nil
	}
	filtered := make([]ToolApprovalRequest, 0, len(requests))
	for _, request := range requests {
		if _, ok := s.approvals[request.ToolCallID]; ok {
			continue
		}
		filtered = append(filtered, request)
	}
	return filtered
}

func (s *workflowSignalState) filterPendingDeferred(requests []core.DeferredToolRequest) []core.DeferredToolRequest {
	if len(requests) == 0 {
		return nil
	}
	filtered := make([]core.DeferredToolRequest, 0, len(requests))
	for _, request := range requests {
		if _, ok := s.deferredResults[request.ToolCallID]; ok {
			continue
		}
		filtered = append(filtered, request)
	}
	return filtered
}

func (s *workflowSignalState) takeApproval(toolCallID string) (ApprovalSignal, bool) {
	approval, ok := s.approvals[toolCallID]
	if ok {
		delete(s.approvals, toolCallID)
	}
	return approval, ok
}

func (s *workflowSignalState) takeDeferredResult(toolCallID string) (DeferredResultSignal, bool) {
	result, ok := s.deferredResults[toolCallID]
	if ok {
		delete(s.deferredResults, toolCallID)
	}
	return result, ok
}

func waitingReason(pendingApprovals []ToolApprovalRequest, pendingDeferred []core.DeferredToolRequest) string {
	switch {
	case len(pendingApprovals) > 0 && len(pendingDeferred) > 0:
		return "approval_and_deferred"
	case len(pendingApprovals) > 0:
		return "approval"
	case len(pendingDeferred) > 0:
		return "deferred"
	default:
		return ""
	}
}

func workflowAbortError(reason string) error {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return errors.New("workflow aborted")
	}
	return fmt.Errorf("workflow aborted: %s", trimmed)
}

func approvalDeniedMessage(toolName, message string) string {
	if strings.TrimSpace(message) == "" {
		return fmt.Sprintf("tool call %q was denied by the user", toolName)
	}
	return fmt.Sprintf("tool call %q was denied by the user: %s", toolName, strings.TrimSpace(message))
}

func (ta *TemporalAgent[T]) appendModelRequestTrace(ctx workflow.Context, state *workflowRunState, messageCount int) time.Time {
	if !ta.runtime.TracingEnabled {
		return time.Time{}
	}
	now := workflow.Now(ctx)
	state.TraceSteps = append(state.TraceSteps, core.TraceStep{
		Kind:      core.TraceModelRequest,
		Timestamp: now,
		Data:      map[string]any{"message_count": messageCount},
	})
	return now
}

func (ta *TemporalAgent[T]) appendModelResponseTrace(ctx workflow.Context, state *workflowRunState, start time.Time, resp *core.ModelResponse) {
	if !ta.runtime.TracingEnabled {
		return
	}
	now := workflow.Now(ctx)
	state.TraceSteps = append(state.TraceSteps, core.TraceStep{
		Kind:      core.TraceModelResponse,
		Timestamp: now,
		Duration:  deterministicWorkflowDuration(start, now),
		Data: map[string]any{
			"text":       resp.TextContent(),
			"tool_calls": len(resp.ToolCalls()),
		},
	})
}

func (ta *TemporalAgent[T]) appendToolCallTrace(ctx workflow.Context, state *workflowRunState, call core.ToolCallPart) time.Time {
	if !ta.runtime.TracingEnabled {
		return time.Time{}
	}
	now := workflow.Now(ctx)
	state.TraceSteps = append(state.TraceSteps, core.TraceStep{
		Kind:      core.TraceToolCall,
		Timestamp: now,
		Data: map[string]any{
			"tool_name": call.ToolName,
			"args":      call.ArgsJSON,
		},
	})
	return now
}

func (ta *TemporalAgent[T]) appendToolResultTrace(ctx workflow.Context, state *workflowRunState, start time.Time, call core.ToolCallPart, result, errText string) {
	if !ta.runtime.TracingEnabled {
		return
	}
	now := workflow.Now(ctx)
	state.TraceSteps = append(state.TraceSteps, core.TraceStep{
		Kind:      core.TraceToolResult,
		Timestamp: now,
		Duration:  deterministicWorkflowDuration(start, now),
		Data: map[string]any{
			"tool_name": call.ToolName,
			"result":    result,
			"error":     errText,
		},
	})
}

func buildCallbackInput(state *workflowRunState, prompt, toolName, toolCallID string, retry, maxRetries int, messages []core.ModelMessage) (callbackRunInput, error) {
	serialized, err := core.EncodeMessages(messages)
	if err != nil {
		return callbackRunInput{}, fmt.Errorf("marshal callback messages: %w", err)
	}
	return callbackRunInput{
		Prompt:          prompt,
		Messages:        serialized,
		DepsJSON:        append([]byte(nil), state.DepsJSON...),
		Usage:           state.Usage,
		LastInputTokens: state.LastInputTokens,
		Retries:         state.Retries,
		ToolRetries:     cloneIntMap(state.ToolRetries),
		RunStep:         state.RunStep,
		RunID:           state.RunID,
		RunStartTime:    state.RunStartTime,
		ToolState:       cloneAnyMap(state.ToolState),
		ToolName:        toolName,
		ToolCallID:      toolCallID,
		Retry:           retry,
		MaxRetries:      maxRetries,
	}, nil
}

func (ta *TemporalAgent[T]) executeDynamicSystemPrompts(ctx workflow.Context, state *workflowRunState, prompt string) ([]string, error) {
	if len(ta.runtime.DynamicSystemPrompts) == 0 {
		return nil, nil
	}
	input, err := buildCallbackInput(state, prompt, "", "", 0, 0, state.Messages)
	if err != nil {
		return nil, err
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output dynamicPromptActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.dynamicPromptActivityName(), input).Get(callbackCtx, &output); err != nil {
		return nil, fmt.Errorf("dynamic system prompt failed: %w", err)
	}
	return output.Prompts, nil
}

func (ta *TemporalAgent[T]) applyInputGuardrails(ctx workflow.Context, state *workflowRunState, prompt string) (string, error) {
	if len(ta.runtime.InputGuardrails) == 0 {
		return prompt, nil
	}
	input, err := buildCallbackInput(state, prompt, "", "", 0, 0, state.Messages)
	if err != nil {
		return prompt, err
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output inputGuardrailActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.inputGuardrailActivityName(), inputGuardrailActivityInput{
		Run: input,
	}).Get(callbackCtx, &output); err != nil {
		return prompt, fmt.Errorf("input guardrail failed: %w", err)
	}
	if output.Rejected {
		for _, eval := range output.Evaluations {
			if !eval.Passed {
				return output.Prompt, &core.GuardrailError{
					GuardrailName: eval.Name,
					Message:       eval.Message,
				}
			}
		}
		return output.Prompt, &core.GuardrailError{Message: "input guardrail rejected the prompt"}
	}
	return output.Prompt, nil
}

func (ta *TemporalAgent[T]) applyHistoryProcessors(ctx workflow.Context, state *workflowRunState, prompt string, messages []core.ModelMessage) ([]core.ModelMessage, error) {
	if len(ta.runtime.HistoryProcessors) == 0 {
		return messages, nil
	}
	beforeCount := len(messages)
	input, err := buildCallbackInput(state, prompt, "", "", 0, 0, messages)
	if err != nil {
		return nil, err
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output historyProcessorActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.historyProcessorActivityName(), input).Get(callbackCtx, &output); err != nil {
		return nil, fmt.Errorf("history processor failed: %w", err)
	}
	processed, err := decodeSerializedMessages(output.Messages, output.MessagesJSON)
	if err != nil {
		return nil, err
	}
	if len(ta.runtime.Hooks) > 0 && len(processed) < beforeCount {
		hookInput, err := buildCallbackInput(state, prompt, "", "", 0, 0, processed)
		if err != nil {
			return nil, err
		}
		if err := ta.fireHook(ctx, hookActivityInput{
			Run:   hookInput,
			Event: hookEventContextCompaction,
			Stats: &core.ContextCompactionStats{
				Strategy:       core.CompactionStrategyHistoryProcessor,
				MessagesBefore: beforeCount,
				MessagesAfter:  len(processed),
			},
		}); err != nil {
			return nil, err
		}
	}
	return processed, nil
}

func (ta *TemporalAgent[T]) applyAutoContext(ctx workflow.Context, state *workflowRunState, prompt string) error {
	if ta.runtime.AutoContext == nil {
		return nil
	}
	input, err := buildCallbackInput(state, prompt, "", "", 0, 0, state.Messages)
	if err != nil {
		return err
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output autoContextActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.autoContextActivityName(), autoContextActivityInput{
		Run: input,
	}).Get(callbackCtx, &output); err != nil {
		return fmt.Errorf("auto-context compaction failed: %w", err)
	}
	if !output.Changed {
		return nil
	}
	compressed, err := decodeSerializedMessages(output.Messages, output.MessagesJSON)
	if err != nil {
		return err
	}
	state.Messages = compressed
	if len(ta.runtime.Hooks) > 0 && output.Stats != nil {
		hookInput, err := buildCallbackInput(state, prompt, "", "", 0, 0, compressed)
		if err != nil {
			return err
		}
		if err := ta.fireHook(ctx, hookActivityInput{
			Run:   hookInput,
			Event: hookEventContextCompaction,
			Stats: output.Stats,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (ta *TemporalAgent[T]) applyToolPreparation(ctx workflow.Context, state *workflowRunState, prompt string) ([]core.ToolDefinition, error) {
	if ta.runtime.AgentToolsPrepare == nil && !runtimeHasToolPrepareFuncs(ta.runtime.Tools) {
		defs := make([]core.ToolDefinition, 0, len(ta.runtime.Tools))
		for _, tool := range ta.runtime.Tools {
			defs = append(defs, tool.Definition)
		}
		return defs, nil
	}
	input, err := buildCallbackInput(state, prompt, "", "", 0, 0, state.Messages)
	if err != nil {
		return nil, err
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output toolPrepareActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.toolPrepareActivityName(), toolPrepareActivityInput{
		Run: input,
	}).Get(callbackCtx, &output); err != nil {
		return nil, fmt.Errorf("tool preparation failed: %w", err)
	}
	return output.Definitions, nil
}

func (ta *TemporalAgent[T]) applyTurnGuardrails(ctx workflow.Context, state *workflowRunState, prompt string, messages []core.ModelMessage) error {
	if len(ta.runtime.TurnGuardrails) == 0 {
		return nil
	}
	input, err := buildCallbackInput(state, prompt, "", "", 0, 0, messages)
	if err != nil {
		return err
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output turnGuardrailActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.turnGuardrailActivityName(), turnGuardrailActivityInput{
		Run: input,
	}).Get(callbackCtx, &output); err != nil {
		return fmt.Errorf("turn guardrail failed: %w", err)
	}
	if output.Rejected {
		for _, eval := range output.Evaluations {
			if !eval.Passed {
				return &core.GuardrailError{
					GuardrailName: eval.Name,
					Message:       eval.Message,
				}
			}
		}
		return &core.GuardrailError{Message: "turn guardrail rejected the run"}
	}
	return nil
}

func (ta *TemporalAgent[T]) applyMessageInterceptors(ctx workflow.Context, state *workflowRunState, prompt string, messages []core.ModelMessage) ([]core.ModelMessage, bool, error) {
	if len(ta.runtime.MessageInterceptors) == 0 {
		return messages, false, nil
	}
	input, err := buildCallbackInput(state, prompt, "", "", 0, 0, messages)
	if err != nil {
		return nil, false, err
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output messageInterceptorActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.messageInterceptorActivityName(), input).Get(callbackCtx, &output); err != nil {
		return nil, false, fmt.Errorf("message interceptor failed: %w", err)
	}
	if output.Dropped {
		return nil, true, nil
	}
	processed, err := decodeSerializedMessages(output.Messages, output.MessagesJSON)
	if err != nil {
		return nil, false, err
	}
	return processed, false, nil
}

func (ta *TemporalAgent[T]) applyResponseInterceptors(ctx workflow.Context, resp *core.ModelResponse) (*core.ModelResponse, bool, error) {
	if len(ta.runtime.ResponseInterceptors) == 0 {
		return resp, false, nil
	}
	response, err := core.EncodeModelResponse(resp)
	if err != nil {
		return nil, false, err
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output responseInterceptorActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.responseInterceptorActivityName(), responseInterceptorActivityInput{
		Response: response,
	}).Get(callbackCtx, &output); err != nil {
		return nil, false, fmt.Errorf("response interceptor failed: %w", err)
	}
	if output.Dropped {
		return nil, true, nil
	}
	processed, err := decodeResponse(output.Response, output.ResponseJSON)
	if err != nil {
		return nil, false, err
	}
	return processed, false, nil
}

func (ta *TemporalAgent[T]) evaluateRunConditions(ctx workflow.Context, state *workflowRunState, prompt string, resp *core.ModelResponse) (bool, string, error) {
	if len(ta.runtime.RunConditions) == 0 {
		return false, "", nil
	}
	input, err := buildCallbackInput(state, prompt, "", "", 0, 0, state.Messages)
	if err != nil {
		return false, "", err
	}
	response, err := core.EncodeModelResponse(resp)
	if err != nil {
		return false, "", err
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output runConditionActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.runConditionActivityName(), runConditionActivityInput{
		Run:      input,
		Response: response,
	}).Get(callbackCtx, &output); err != nil {
		return false, "", fmt.Errorf("run condition failed: %w", err)
	}
	if !output.Stopped {
		return false, "", nil
	}
	if err := ta.fireHook(ctx, hookActivityInput{
		Run:    input,
		Event:  hookEventRunCondition,
		Passed: true,
		Reason: output.Reason,
	}); err != nil {
		return false, "", err
	}
	return true, output.Reason, nil
}

func (ta *TemporalAgent[T]) retrieveKnowledgeContext(ctx workflow.Context, prompt string) (string, error) {
	if ta.runtime.KnowledgeBase == nil {
		return "", nil
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output knowledgeRetrieveActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.knowledgeRetrieveActivityName(), knowledgeRetrieveActivityInput{
		Prompt: prompt,
	}).Get(callbackCtx, &output); err != nil {
		return "", fmt.Errorf("knowledge base retrieve failed: %w", err)
	}
	return output.Content, nil
}

func (ta *TemporalAgent[T]) storeKnowledgeResult(ctx workflow.Context, content string) error {
	if ta.runtime.KnowledgeBase == nil || !ta.runtime.KnowledgeAutoStore || strings.TrimSpace(content) == "" {
		return nil
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	if err := workflow.ExecuteActivity(callbackCtx, ta.knowledgeStoreActivityName(), knowledgeStoreActivityInput{
		Content: content,
	}).Get(callbackCtx, nil); err != nil {
		return fmt.Errorf("knowledge base store failed: %w", err)
	}
	return nil
}

func (ta *TemporalAgent[T]) repairOutput(ctx workflow.Context, state *workflowRunState, prompt, toolName, toolCallID, raw string, parseErr error) (T, error) {
	var zero T
	if ta.runtime.OutputRepair == nil {
		return zero, parseErr
	}
	input, err := buildCallbackInput(state, prompt, toolName, toolCallID, 0, 0, state.Messages)
	if err != nil {
		return zero, err
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output outputRepairActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.outputRepairActivityName(), outputRepairActivityInput{
		Run:        input,
		Raw:        raw,
		ParseError: parseErr.Error(),
	}).Get(callbackCtx, &output); err != nil {
		_ = ta.fireHook(ctx, hookActivityInput{
			Run:   input,
			Event: hookEventOutputRepair,
			Error: err.Error(),
		})
		return zero, parseErr
	}
	if err := ta.fireHook(ctx, hookActivityInput{
		Run:    input,
		Event:  hookEventOutputRepair,
		Passed: true,
	}); err != nil {
		return zero, err
	}
	if err := json.Unmarshal(output.OutputJSON, &zero); err != nil {
		var out T
		return out, fmt.Errorf("unmarshal repaired output: %w", err)
	}
	return zero, nil
}

func (ta *TemporalAgent[T]) validateWorkflowOutput(ctx workflow.Context, state *workflowRunState, prompt, toolName, toolCallID string, output T) (T, string, error) {
	if len(ta.runtime.OutputValidators) == 0 {
		return output, "", nil
	}
	input, err := buildCallbackInput(state, prompt, toolName, toolCallID, 0, 0, state.Messages)
	if err != nil {
		var zero T
		return zero, "", err
	}
	outputJSON, err := json.Marshal(output)
	if err != nil {
		var zero T
		return zero, "", fmt.Errorf("marshal output for validation: %w", err)
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var validated outputValidateActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.outputValidateActivityName(), outputValidateActivityInput{
		Run:        input,
		OutputJSON: outputJSON,
	}).Get(callbackCtx, &validated); err != nil {
		var zero T
		return zero, "", fmt.Errorf("output validation failed: %w", err)
	}
	if validated.RetryMessage != "" {
		if err := ta.fireHook(ctx, hookActivityInput{
			Run:   input,
			Event: hookEventOutputValidation,
			Error: validated.ValidationErr,
		}); err != nil {
			var zero T
			return zero, "", err
		}
		return output, validated.RetryMessage, nil
	}
	if validated.FatalError != "" {
		if err := ta.fireHook(ctx, hookActivityInput{
			Run:   input,
			Event: hookEventOutputValidation,
			Error: validated.ValidationErr,
		}); err != nil {
			var zero T
			return zero, "", err
		}
		var zero T
		return zero, "", errors.New(validated.FatalError)
	}
	if err := ta.fireHook(ctx, hookActivityInput{
		Run:    input,
		Event:  hookEventOutputValidation,
		Passed: true,
	}); err != nil {
		var zero T
		return zero, "", err
	}
	var finalOutput T
	if err := json.Unmarshal(validated.OutputJSON, &finalOutput); err != nil {
		var zero T
		return zero, "", fmt.Errorf("unmarshal validated output: %w", err)
	}
	return finalOutput, "", nil
}

func (ta *TemporalAgent[T]) checkToolApproval(ctx workflow.Context, callState *workflowToolCall) (*toolApprovalActivityOutput, error) {
	if ta.runtime.ToolApprovalFunc == nil {
		return nil, nil
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	var output toolApprovalActivityOutput
	if err := workflow.ExecuteActivity(callbackCtx, ta.toolApprovalActivityName(), toolApprovalActivityInput{
		ToolName: callState.call.ToolName,
		ArgsJSON: callState.call.ArgsJSON,
	}).Get(callbackCtx, &output); err != nil {
		return nil, fmt.Errorf("tool approval callback failed: %w", err)
	}
	return &output, nil
}

func (ta *TemporalAgent[T]) shouldContinueAsNew(ctx workflow.Context, state *workflowRunState) string {
	return ta.config.continueAsNew.reason(ctx, state)
}

func (ta *TemporalAgent[T]) continueAsNew(ctx workflow.Context, state *workflowRunState, prompt string, settings *core.ModelSettings, limits core.UsageLimits) error {
	snapshotJSON, err := state.snapshotJSON(prompt, workflow.Now(ctx))
	if err != nil {
		return err
	}
	snapshot, err := core.UnmarshalSnapshot(snapshotJSON)
	if err != nil {
		return fmt.Errorf("unmarshal workflow snapshot for continue-as-new: %w", err)
	}
	encodedSnapshot, err := core.EncodeRunSnapshot(snapshot)
	if err != nil {
		return fmt.Errorf("encode workflow snapshot for continue-as-new: %w", err)
	}
	state.ContinueAsNewCount++
	nextInput := WorkflowInput{
		Prompt:                        prompt,
		Snapshot:                      encodedSnapshot,
		TraceSteps:                    cloneTraceSteps(state.TraceSteps),
		DepsJSON:                      append([]byte(nil), state.DepsJSON...),
		ModelSettings:                 settings,
		UsageLimits:                   &limits,
		ContinueAsNewCount:            state.ContinueAsNewCount,
		ContinueAsNewBaseRunStep:      state.RunStep,
		ContinueAsNewBaseMessageCount: len(state.Messages),
	}
	return workflow.NewContinueAsNewError(ctx, ta.RunWorkflow, nextInput)
}

func (ta *TemporalAgent[T]) exportTrace(ctx workflow.Context, trace *core.RunTrace) {
	if trace == nil || len(ta.runtime.TraceExporters) == 0 {
		return
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	_ = workflow.ExecuteActivity(callbackCtx, ta.traceExportActivityName(), traceExportActivityInput{
		Trace: trace,
	}).Get(callbackCtx, nil)
}

func (ta *TemporalAgent[T]) publishEventBus(ctx workflow.Context, input eventBusActivityInput) error {
	if ta.runtime.EventBus == nil {
		return nil
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	return workflow.ExecuteActivity(callbackCtx, ta.eventBusActivityName(), input).Get(callbackCtx, nil)
}

func checkWorkflowQuota(quota *core.UsageQuota, usage core.RunUsage) error {
	if quota == nil {
		return nil
	}
	if quota.MaxRequests > 0 && usage.Requests >= quota.MaxRequests {
		return &core.QuotaExceededError{
			Quota:   *quota,
			Usage:   usage,
			Message: fmt.Sprintf("request limit %d reached (used %d)", quota.MaxRequests, usage.Requests),
		}
	}
	if quota.MaxTotalTokens > 0 && usage.TotalTokens() >= quota.MaxTotalTokens {
		return &core.QuotaExceededError{
			Quota:   *quota,
			Usage:   usage,
			Message: fmt.Sprintf("total token limit %d reached (used %d)", quota.MaxTotalTokens, usage.TotalTokens()),
		}
	}
	if quota.MaxInputTokens > 0 && usage.InputTokens >= quota.MaxInputTokens {
		return &core.QuotaExceededError{
			Quota:   *quota,
			Usage:   usage,
			Message: fmt.Sprintf("input token limit %d reached (used %d)", quota.MaxInputTokens, usage.InputTokens),
		}
	}
	if quota.MaxOutputTokens > 0 && usage.OutputTokens >= quota.MaxOutputTokens {
		return &core.QuotaExceededError{
			Quota:   *quota,
			Usage:   usage,
			Message: fmt.Sprintf("output token limit %d reached (used %d)", quota.MaxOutputTokens, usage.OutputTokens),
		}
	}
	return nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// DecodeWorkflowOutput converts a serializable workflow result back into a RunResult.
func (ta *TemporalAgent[T]) DecodeWorkflowOutput(output *WorkflowOutput) (*core.RunResult[T], error) {
	if output == nil {
		return nil, errors.New("nil workflow output")
	}
	if !output.Completed {
		return nil, fmt.Errorf("workflow paused with %d deferred tool request(s)", len(output.DeferredRequests))
	}

	snapshot, err := decodeSerializedSnapshot(output.Snapshot, output.SnapshotJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal workflow snapshot: %w", err)
	}
	if snapshot == nil {
		return nil, errors.New("workflow output missing snapshot")
	}
	result, err := decodeTemporalOutput[T](output.OutputJSON)
	if err != nil {
		return nil, err
	}
	trace, err := decodeTrace(output.Trace, output.TraceJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal workflow trace: %w", err)
	}
	return &core.RunResult[T]{
		Output:    result,
		Messages:  snapshot.Messages,
		Usage:     snapshot.Usage,
		RunID:     snapshot.RunID,
		ToolState: snapshot.ToolState,
		Trace:     trace,
		Cost:      output.Cost,
	}, nil
}

// RunWorkflow executes the supported subset of an agent through Temporal activities.
func (ta *TemporalAgent[T]) RunWorkflow(ctx workflow.Context, input WorkflowInput) (result *WorkflowOutput, runErr error) {
	if len(ta.config.passthroughTools) > 0 {
		return nil, errors.New("gollem/temporal: passthrough tools are not supported by RunWorkflow")
	}

	signals := newWorkflowSignalState(ctx, ta)
	if err := signals.registerQuery(ctx); err != nil {
		return nil, fmt.Errorf("register workflow status query: %w", err)
	}

	state, err := newWorkflowRunState(ctx, input)
	if err != nil {
		return nil, err
	}
	prompt := input.Prompt
	if prompt == "" && (input.Snapshot != nil || len(input.SnapshotJSON) > 0) {
		if snapshot, snapErr := decodeSerializedSnapshot(input.Snapshot, input.SnapshotJSON); snapErr == nil {
			prompt = snapshot.Prompt
		}
	}
	continueAsNewResume := isContinueAsNewResume(input)
	runStarted := continueAsNewResume
	defer func() {
		if !runStarted {
			return
		}
		var continueErr *workflow.ContinueAsNewError
		if errors.As(runErr, &continueErr) {
			return
		}
		if busErr := ta.publishEventBus(ctx, eventBusActivityInput{
			EventType: hookEventRunEnd,
			RunID:     state.RunID,
			Success:   runErr == nil,
			Error:     errorString(runErr),
		}); busErr != nil && runErr == nil {
			runErr = fmt.Errorf("run end event publish failed: %w", busErr)
			return
		}
		input, err := buildCallbackInput(state, prompt, "", "", 0, 0, state.Messages)
		if err != nil {
			if runErr == nil {
				runErr = err
			}
			return
		}
		hookErr := ta.fireHook(ctx, hookActivityInput{
			Run:   input,
			Event: hookEventRunEnd,
			Error: errorString(runErr),
		})
		if hookErr != nil && runErr == nil {
			runErr = fmt.Errorf("run end hook failed: %w", hookErr)
			return
		}

		if result != nil && result.Completed && ta.runtime.HasCostTracker {
			result.Cost = buildWorkflowCost(ta.runtime, state.Usage)
		}

		if !ta.runtime.TracingEnabled {
			return
		}

		trace := &core.RunTrace{
			RunID:     state.RunID,
			Prompt:    prompt,
			StartTime: state.RunStartTime,
			EndTime:   workflow.Now(ctx),
			Duration:  deterministicWorkflowDuration(state.RunStartTime, workflow.Now(ctx)),
			Steps:     cloneTraceSteps(state.TraceSteps),
			Usage:     state.Usage,
			Success:   runErr == nil && result != nil && result.Completed,
		}
		if runErr != nil {
			trace.Error = runErr.Error()
		}
		if result != nil && result.Completed {
			result.Trace = trace
		}
		ta.exportTrace(ctx, trace)
	}()

	initialParts, err := unmarshalInitialRequestParts(input.InitialRequestParts, input.InitialRequestPartsJSON)
	if err != nil {
		return nil, fmt.Errorf("decode initial request parts: %w", err)
	}

	if !continueAsNewResume {
		prompt, err = ta.applyInputGuardrails(ctx, state, prompt)
		if err != nil {
			return nil, err
		}
	}

	if len(input.DeferredResults) > 0 && len(state.Messages) > 0 {
		injectDeferredResults(state, input.DeferredResults, workflow.Now(ctx))
	} else if !continueAsNewResume {
		dynamicPrompts, err := ta.executeDynamicSystemPrompts(ctx, state, prompt)
		if err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}
		systemPrompts := append(append([]string(nil), ta.runtime.SystemPrompts...), dynamicPrompts...)
		knowledgeContext, err := ta.retrieveKnowledgeContext(ctx, prompt)
		if err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, fmt.Errorf("failed to build initial request: %w", err)
		}
		if knowledgeContext != "" {
			systemPrompts = append(systemPrompts, "[Knowledge Context] "+knowledgeContext)
		}
		state.Messages = append(state.Messages, buildInitialWorkflowRequest(
			systemPrompts,
			prompt,
			initialParts,
			workflow.Now(ctx),
		))
	}
	if !continueAsNewResume {
		if err := ta.publishEventBus(ctx, eventBusActivityInput{
			EventType: hookEventRunStart,
			RunID:     state.RunID,
			Prompt:    prompt,
		}); err != nil {
			return nil, fmt.Errorf("run start event publish failed: %w", err)
		}
		if hookInput, hookErr := buildCallbackInput(state, prompt, "", "", 0, 0, state.Messages); hookErr != nil {
			return nil, hookErr
		} else if hookErr := ta.fireHook(ctx, hookActivityInput{
			Run:   hookInput,
			Event: hookEventRunStart,
		}); hookErr != nil {
			return nil, fmt.Errorf("run start hook failed: %w", hookErr)
		}
		runStarted = true
	}
	if err := signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, nil, false); err != nil {
		return nil, err
	}

	settings := ta.runtime.ModelSettings
	if input.ModelSettings != nil {
		settings = input.ModelSettings
	}
	limits := ta.runtime.UsageLimits
	if input.UsageLimits != nil {
		limits = *input.UsageLimits
	}

	for {
		if reason := ta.shouldContinueAsNew(ctx, state); reason != "" {
			state.LastContinueAsNewReason = reason
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, nil, false)
			return nil, ta.continueAsNew(ctx, state, prompt, settings, limits)
		}
		if err := limits.CheckBeforeRequest(state.Usage); err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}
		if limits.ToolCallsLimit != nil {
			if err := limits.CheckToolCalls(state.Usage); err != nil {
				_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
				return nil, err
			}
		}
		if err := checkWorkflowQuota(ta.runtime.UsageQuota, state.Usage); err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}

		state.RunStep++
		if hookInput, hookErr := buildCallbackInput(state, prompt, "", "", 0, 0, state.Messages); hookErr != nil {
			return nil, hookErr
		} else if hookErr := ta.fireHook(ctx, hookActivityInput{
			Run:        hookInput,
			Event:      hookEventTurnStart,
			TurnNumber: state.RunStep,
		}); hookErr != nil {
			return nil, hookErr
		}
		if err := signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, nil, false); err != nil {
			return nil, err
		}

		preparedDefs, err := ta.applyToolPreparation(ctx, state, prompt)
		if err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}
		requestParams := buildWorkflowRequestParamsFromDefinitions(ta.runtime, preparedDefs)
		currentSettings := settings
		if ta.runtime.ToolChoice != nil {
			if currentSettings == nil {
				currentSettings = &core.ModelSettings{}
			}
			if currentSettings.ToolChoice == nil {
				choice := *ta.runtime.ToolChoice
				currentSettings.ToolChoice = &choice
			}
		}
		if err := ta.applyAutoContext(ctx, state, prompt); err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}

		messages := cloneMessages(state.Messages)
		messages, err = ta.applyHistoryProcessors(ctx, state, prompt, messages)
		if err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}
		messages, dropped, err := ta.applyMessageInterceptors(ctx, state, prompt, messages)
		if err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}
		if dropped {
			err := errors.New("message interceptor dropped the request")
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}
		if err := ta.applyTurnGuardrails(ctx, state, prompt, messages); err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}

		serializedMessages, err := core.EncodeMessages(messages)
		if err != nil {
			return nil, fmt.Errorf("marshal workflow messages: %w", err)
		}
		if hookInput, hookErr := buildCallbackInput(state, prompt, "", "", 0, 0, messages); hookErr != nil {
			return nil, hookErr
		} else if hookErr := ta.fireHook(ctx, hookActivityInput{
			Run:   hookInput,
			Event: hookEventModelRequest,
		}); hookErr != nil {
			return nil, hookErr
		}

		var modelOutput ModelActivityOutput
		modelCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.model.config))
		modelReqStart := ta.appendModelRequestTrace(ctx, state, len(messages))
		if err := workflow.ExecuteActivity(modelCtx, ta.model.ModelRequestActivityName(), ModelActivityInput{
			Messages:   serializedMessages,
			Settings:   currentSettings,
			Parameters: requestParams,
		}).Get(modelCtx, &modelOutput); err != nil {
			wrappedErr := fmt.Errorf("model activity failed: %w", err)
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, wrappedErr, false)
			return nil, wrappedErr
		}

		resp, err := decodeModelActivityOutput(&modelOutput)
		if err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}

		state.Usage.IncrRequest(resp.Usage)
		if resp.Usage.InputTokens > 0 {
			state.LastInputTokens = resp.Usage.InputTokens
		}
		resp, dropped, err = ta.applyResponseInterceptors(ctx, resp)
		if err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}
		if dropped {
			continue
		}
		if ta.runtime.ToolChoiceAutoReset && len(resp.ToolCalls()) > 0 && currentSettings != nil && currentSettings.ToolChoice != nil {
			nextSettings := *currentSettings
			nextSettings.ToolChoice = core.ToolChoiceAuto()
			settings = &nextSettings
		} else {
			settings = currentSettings
		}

		state.Messages = append(state.Messages, *resp)
		if hookInput, hookErr := buildCallbackInput(state, prompt, "", "", 0, 0, state.Messages); hookErr != nil {
			return nil, hookErr
		} else {
			response, hookErr := core.EncodeModelResponse(resp)
			if hookErr != nil {
				return nil, hookErr
			}
			if hookErr := ta.fireHook(ctx, hookActivityInput{
				Run:      hookInput,
				Event:    hookEventModelResponse,
				Response: response,
			}); hookErr != nil {
				return nil, hookErr
			}
		}
		ta.appendModelResponseTrace(ctx, state, modelReqStart, resp)
		if err := signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, nil, false); err != nil {
			return nil, err
		}

		if limits.HasTokenLimits() {
			if err := limits.CheckTokens(state.Usage); err != nil {
				_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
				return nil, err
			}
		}
		if stop, reason, err := ta.evaluateRunConditions(ctx, state, prompt, resp); err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		} else if stop {
			if hasText := resp.TextContent() != ""; hasText && ta.runtime.OutputSchema.AllowsText {
				text := resp.TextContent()
				finalOutput, decodeErr := ta.decodeTextOutput(text)
				if decodeErr == nil {
					if err := ta.storeKnowledgeResult(ctx, text); err != nil {
						_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
						return nil, err
					}
					outputJSON, err := json.Marshal(finalOutput)
					if err != nil {
						marshalErr := fmt.Errorf("marshal workflow output: %w", err)
						_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, marshalErr, false)
						return nil, marshalErr
					}
					snapshotJSON, err := state.snapshotJSON(prompt, workflow.Now(ctx))
					if err != nil {
						_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
						return nil, err
					}
					if err := signals.refreshStatus(ctx, state, prompt, "", nil, nil, true, nil, false); err != nil {
						return nil, err
					}
					snapshot, err := core.UnmarshalSnapshot(snapshotJSON)
					if err != nil {
						return nil, err
					}
					encodedSnapshot, err := core.EncodeRunSnapshot(snapshot)
					if err != nil {
						return nil, err
					}
					return &WorkflowOutput{
						Completed:          true,
						OutputJSON:         outputJSON,
						Snapshot:           encodedSnapshot,
						ContinueAsNewCount: state.ContinueAsNewCount,
					}, nil
				}
			}
			stopErr := &core.RunConditionError{Reason: reason}
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, stopErr, false)
			return nil, stopErr
		}

		finalOutput, nextParts, err := ta.processWorkflowResponse(ctx, state, prompt, resp, signals, settings, limits)
		if response, hookErr := core.EncodeModelResponse(resp); hookErr == nil {
			if hookInput, hookErr := buildCallbackInput(state, prompt, "", "", 0, 0, state.Messages); hookErr == nil {
				if hookErr := ta.fireHook(ctx, hookActivityInput{
					Run:        hookInput,
					Event:      hookEventTurnEnd,
					TurnNumber: state.RunStep,
					Response:   response,
				}); hookErr != nil {
					return nil, hookErr
				}
			} else {
				return nil, hookErr
			}
		} else {
			return nil, hookErr
		}
		if err != nil {
			_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
			return nil, err
		}
		if finalOutput != nil {
			if err := ta.storeKnowledgeResult(ctx, resp.TextContent()); err != nil {
				_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
				return nil, err
			}
			outputJSON, err := json.Marshal(finalOutput)
			if err != nil {
				marshalErr := fmt.Errorf("marshal workflow output: %w", err)
				_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, marshalErr, false)
				return nil, marshalErr
			}
			snapshotJSON, err := state.snapshotJSON(prompt, workflow.Now(ctx))
			if err != nil {
				_ = signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, err, false)
				return nil, err
			}
			if err := signals.refreshStatus(ctx, state, prompt, "", nil, nil, true, nil, false); err != nil {
				return nil, err
			}
			snapshot, err := core.UnmarshalSnapshot(snapshotJSON)
			if err != nil {
				return nil, err
			}
			encodedSnapshot, err := core.EncodeRunSnapshot(snapshot)
			if err != nil {
				return nil, err
			}
			return &WorkflowOutput{
				Completed:          true,
				OutputJSON:         outputJSON,
				Snapshot:           encodedSnapshot,
				ContinueAsNewCount: state.ContinueAsNewCount,
			}, nil
		}
		if len(nextParts) > 0 {
			state.Messages = append(state.Messages, core.ModelRequest{
				Parts:     nextParts,
				Timestamp: workflow.Now(ctx),
			})
			if err := signals.refreshStatus(ctx, state, prompt, "", nil, nil, false, nil, false); err != nil {
				return nil, err
			}
		}
	}
}

func (ta *TemporalAgent[T]) processWorkflowResponse(
	ctx workflow.Context,
	state *workflowRunState,
	prompt string,
	resp *core.ModelResponse,
	signals *workflowSignalState,
	settings *core.ModelSettings,
	limits core.UsageLimits,
) (*T, []core.ModelRequestPart, error) {
	toolCalls := resp.ToolCalls()
	hasText := resp.TextContent() != ""

	if len(toolCalls) == 0 && hasText && ta.runtime.OutputSchema.AllowsText {
		text := resp.TextContent()
		output, err := ta.decodeTextOutput(text)
		if err != nil {
			if ta.runtime.OutputSchema.Mode == core.OutputModeText {
				return nil, nil, fmt.Errorf("failed to parse text output: %w", err)
			}
			output, err = ta.repairOutput(ctx, state, prompt, "", "", text, err)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to parse text output: %w", err)
			}
		}
		output, retryMessage, err := ta.validateWorkflowOutput(ctx, state, prompt, "", "", output)
		if err != nil {
			return nil, nil, err
		}
		if retryMessage != "" {
			if incErr := incrementWorkflowRetries(state, ta.runtime.MaxRetries); incErr != nil {
				return nil, nil, incErr
			}
			return nil, []core.ModelRequestPart{core.RetryPromptPart{
				Content:   retryMessage,
				Timestamp: workflow.Now(ctx),
			}}, nil
		}
		return &output, nil, nil
	}

	if len(toolCalls) == 0 {
		if resp.FinishReason == core.FinishReasonLength {
			return nil, nil, &core.UnexpectedModelBehavior{
				Message: "model response ended due to token limit without producing a result",
			}
		}
		if resp.FinishReason == core.FinishReasonContentFilter {
			return nil, nil, &core.ContentFilterError{
				UnexpectedModelBehavior: core.UnexpectedModelBehavior{
					Message: "content filter triggered",
				},
			}
		}
		if err := incrementWorkflowRetries(state, ta.runtime.MaxRetries); err != nil {
			return nil, nil, err
		}
		return nil, []core.ModelRequestPart{core.RetryPromptPart{
			Content:   "empty response, please provide a result",
			Timestamp: workflow.Now(ctx),
		}}, nil
	}

	var (
		outputCalls   []core.ToolCallPart
		functionCalls []core.ToolCallPart
		unknownCalls  []core.ToolCallPart
		resultParts   []core.ModelRequestPart
		finalOutput   *T
	)

	for _, call := range toolCalls {
		if ta.isOutputTool(call.ToolName) {
			outputCalls = append(outputCalls, call)
			continue
		}
		if _, ok := ta.toolsByName[call.ToolName]; ok {
			functionCalls = append(functionCalls, call)
			continue
		}
		unknownCalls = append(unknownCalls, call)
	}

	if len(unknownCalls) > 0 {
		if err := incrementWorkflowRetries(state, ta.runtime.MaxRetries); err != nil {
			return nil, nil, err
		}
		now := workflow.Now(ctx)
		available := availableToolNames(ta.runtime)
		for _, call := range unknownCalls {
			resultParts = append(resultParts, core.RetryPromptPart{
				Content:    fmt.Sprintf("unknown tool %q, available tools: %s", call.ToolName, available),
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				Timestamp:  now,
			})
		}
	}

	for _, call := range outputCalls {
		output, err := decodeStructuredOutput[T](call.ArgsJSON, ta.runtime.OutputSchema.OuterTypedDictKey)
		if err != nil {
			output, err = ta.repairOutput(ctx, state, prompt, call.ToolName, call.ToolCallID, call.ArgsJSON, err)
		}
		if err != nil {
			if retryErr := incrementWorkflowRetries(state, ta.runtime.MaxRetries); retryErr != nil {
				return nil, nil, retryErr
			}
			resultParts = append(resultParts, core.RetryPromptPart{
				Content:    "failed to parse output: " + err.Error(),
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				Timestamp:  workflow.Now(ctx),
			})
			continue
		}
		output, retryMessage, err := ta.validateWorkflowOutput(ctx, state, prompt, call.ToolName, call.ToolCallID, output)
		if err != nil {
			return nil, nil, err
		}
		if retryMessage != "" {
			if retryErr := incrementWorkflowRetries(state, ta.runtime.MaxRetries); retryErr != nil {
				return nil, nil, retryErr
			}
			resultParts = append(resultParts, core.RetryPromptPart{
				Content:    retryMessage,
				ToolName:   call.ToolName,
				ToolCallID: call.ToolCallID,
				Timestamp:  workflow.Now(ctx),
			})
			continue
		}
		if finalOutput == nil {
			candidate := output
			finalOutput = &candidate
			if ta.runtime.EndStrategy == core.EndStrategyEarly {
				return finalOutput, nil, nil
			}
		}
	}

	if len(functionCalls) > 0 {
		if limit := limitsToolCalls(ta.runtime.UsageLimits, state.Usage.ToolCalls, len(functionCalls)); limit != nil {
			return nil, nil, limit
		}
		funcParts, err := ta.executeFunctionTools(ctx, state, prompt, functionCalls, signals, settings, limits)
		if err != nil {
			return nil, nil, err
		}
		resultParts = append(resultParts, funcParts...)
		state.Retries = 0
	}

	if finalOutput != nil {
		return finalOutput, resultParts, nil
	}
	return nil, resultParts, nil
}

func (ta *TemporalAgent[T]) executeFunctionTools(
	ctx workflow.Context,
	state *workflowRunState,
	prompt string,
	calls []core.ToolCallPart,
	signals *workflowSignalState,
	settings *core.ModelSettings,
	limits core.UsageLimits,
) ([]core.ModelRequestPart, error) {
	callStates := make([]*workflowToolCall, 0, len(calls))
	for i, call := range calls {
		tool := ta.toolsByName[call.ToolName]
		callStates = append(callStates, &workflowToolCall{
			idx:  i,
			call: call,
			tool: tool,
		})
	}

	results := make([]core.ModelRequestPart, len(calls))
	remaining := len(callStates)

	fireToolStart := func(callState *workflowToolCall) error {
		maxRetries := ta.runtime.MaxRetries
		if callState.tool.Tool.MaxRetries != nil {
			maxRetries = *callState.tool.Tool.MaxRetries
		}
		input, err := buildCallbackInput(state, prompt, callState.call.ToolName, callState.call.ToolCallID, state.ToolRetries[callState.call.ToolName], maxRetries, state.Messages)
		if err != nil {
			return err
		}
		if err := ta.fireHook(ctx, hookActivityInput{
			Run:      input,
			Event:    hookEventToolStart,
			ArgsJSON: callState.call.ArgsJSON,
		}); err != nil {
			return err
		}
		callState.traceStart = ta.appendToolCallTrace(ctx, state, callState.call)
		return nil
	}

	fireToolEnd := func(callState *workflowToolCall, output *ToolActivityOutput, activityErr error) error {
		maxRetries := ta.runtime.MaxRetries
		if callState.tool.Tool.MaxRetries != nil {
			maxRetries = *callState.tool.Tool.MaxRetries
		}
		input, err := buildCallbackInput(state, prompt, callState.call.ToolName, callState.call.ToolCallID, state.ToolRetries[callState.call.ToolName], maxRetries, state.Messages)
		if err != nil {
			return err
		}
		result := ""
		errText := ""
		switch {
		case activityErr != nil:
			errText = activityErr.Error()
		case output != nil:
			switch output.Kind {
			case "return":
				result = output.Content
			case "retry", "error", "deferred":
				errText = output.Message
			}
		}
		if err := ta.fireHook(ctx, hookActivityInput{
			Run:    input,
			Event:  hookEventToolEnd,
			Result: result,
			Error:  errText,
		}); err != nil {
			return err
		}
		ta.appendToolResultTrace(ctx, state, callState.traceStart, callState.call, result, errText)
		return nil
	}

	for remaining > 0 {
		signals.drainSignals()

		var (
			readyConcurrent  []*workflowToolCall
			readySequential  []*workflowToolCall
			pendingApprovals []ToolApprovalRequest
			pendingDeferred  []core.DeferredToolRequest
		)

		for i, callState := range callStates {
			if callState == nil {
				continue
			}
			if !callState.usageCounted {
				state.Usage.IncrToolCall()
				callState.usageCounted = true
			}
			if !callState.eventPublished {
				if err := ta.publishEventBus(ctx, eventBusActivityInput{
					EventType: hookEventToolStart,
					RunID:     state.RunID,
					ToolName:  callState.call.ToolName,
					ArgsJSON:  callState.call.ArgsJSON,
				}); err != nil {
					return nil, err
				}
				callState.eventPublished = true
			}

			if callState.waitingDeferred {
				if deferred, ok := signals.takeDeferredResult(callState.call.ToolCallID); ok {
					if deferred.IsError {
						results[callState.idx] = core.RetryPromptPart{
							Content:    deferred.Content,
							ToolName:   callState.call.ToolName,
							ToolCallID: callState.call.ToolCallID,
							Timestamp:  workflow.Now(ctx),
						}
					} else {
						results[callState.idx] = core.ToolReturnPart{
							ToolName:   callState.call.ToolName,
							Content:    deferred.Content,
							ToolCallID: callState.call.ToolCallID,
							Timestamp:  workflow.Now(ctx),
						}
					}
					callStates[i] = nil
					remaining--
					delete(state.ToolRetries, callState.call.ToolName)
					continue
				}
				pendingDeferred = append(pendingDeferred, core.DeferredToolRequest{
					ToolName:   callState.call.ToolName,
					ToolCallID: callState.call.ToolCallID,
					ArgsJSON:   callState.call.ArgsJSON,
				})
				continue
			}

			if callState.tool.Tool.RequiresApproval {
				if ta.runtime.ToolApprovalFunc != nil {
					approvalOutput, err := ta.checkToolApproval(ctx, callState)
					if err != nil {
						return nil, err
					}
					if approvalOutput != nil {
						if approvalOutput.Error != "" {
							results[callState.idx] = core.ToolReturnPart{
								ToolName:   callState.call.ToolName,
								Content:    "error checking tool approval: " + approvalOutput.Error,
								ToolCallID: callState.call.ToolCallID,
								Timestamp:  workflow.Now(ctx),
							}
							callStates[i] = nil
							remaining--
							continue
						}
						if !approvalOutput.Approved {
							results[callState.idx] = core.RetryPromptPart{
								Content:    approvalDeniedMessage(callState.call.ToolName, ""),
								ToolName:   callState.call.ToolName,
								ToolCallID: callState.call.ToolCallID,
								Timestamp:  workflow.Now(ctx),
							}
							callStates[i] = nil
							remaining--
							delete(state.ToolRetries, callState.call.ToolName)
							continue
						}
					}
				} else {
					approval, ok := signals.takeApproval(callState.call.ToolCallID)
					if !ok {
						pendingApprovals = append(pendingApprovals, ToolApprovalRequest{
							ToolName:   callState.call.ToolName,
							ToolCallID: callState.call.ToolCallID,
							ArgsJSON:   callState.call.ArgsJSON,
						})
						continue
					}
					if !approval.Approved {
						results[callState.idx] = core.RetryPromptPart{
							Content:    approvalDeniedMessage(callState.call.ToolName, approval.Message),
							ToolName:   callState.call.ToolName,
							ToolCallID: callState.call.ToolCallID,
							Timestamp:  workflow.Now(ctx),
						}
						callStates[i] = nil
						remaining--
						delete(state.ToolRetries, callState.call.ToolName)
						continue
					}
				}
			}

			if callState.tool.Tool.Definition.Sequential {
				readySequential = append(readySequential, callState)
			} else {
				readyConcurrent = append(readyConcurrent, callState)
			}
		}

		if len(readyConcurrent) == 0 && len(readySequential) == 0 {
			if reason := ta.shouldContinueAsNew(ctx, state); reason != "" {
				state.LastContinueAsNewReason = reason
				return nil, ta.continueAsNew(ctx, state, prompt, settings, limits)
			}
			if err := signals.waitForExternalInput(ctx, state, prompt, pendingApprovals, pendingDeferred); err != nil {
				return nil, err
			}
			continue
		}

		serializedMessages, err := core.EncodeMessages(state.Messages)
		if err != nil {
			return nil, fmt.Errorf("marshal workflow messages for tool activity: %w", err)
		}

		executeOne := func(callState *workflowToolCall) (*ToolActivityOutput, error) {
			maxRetries := ta.runtime.MaxRetries
			if callState.tool.Tool.MaxRetries != nil {
				maxRetries = *callState.tool.Tool.MaxRetries
			}
			if err := fireToolStart(callState); err != nil {
				return nil, err
			}
			toolCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(callState.tool.Config))
			var output ToolActivityOutput
			err := workflow.ExecuteActivity(toolCtx, callState.tool.ActivityName, ToolActivityInput{
				ArgsJSON:        callState.call.ArgsJSON,
				ToolCallID:      callState.call.ToolCallID,
				Prompt:          prompt,
				DepsJSON:        append([]byte(nil), state.DepsJSON...),
				RunStep:         state.RunStep,
				RunID:           state.RunID,
				RunStartTime:    state.RunStartTime,
				Usage:           state.Usage,
				LastInputTokens: state.LastInputTokens,
				Retries:         state.Retries,
				ToolRetries:     cloneIntMap(state.ToolRetries),
				Retry:           state.ToolRetries[callState.call.ToolName],
				MaxRetries:      maxRetries,
				Messages:        serializedMessages,
				ToolState:       cloneAnyMap(state.ToolState),
			}).Get(toolCtx, &output)
			if err != nil {
				if hookErr := fireToolEnd(callState, nil, err); hookErr != nil {
					return nil, hookErr
				}
				return nil, fmt.Errorf("tool activity %q failed: %w", callState.call.ToolName, err)
			}
			if hookErr := fireToolEnd(callState, &output, nil); hookErr != nil {
				return nil, hookErr
			}
			return &output, nil
		}

		applyOutput := func(callState *workflowToolCall, output *ToolActivityOutput) error {
			if output.HasUpdatedState {
				if state.ToolState == nil {
					state.ToolState = make(map[string]any)
				}
				state.ToolState[callState.call.ToolName] = output.UpdatedToolState
			}

			switch output.Kind {
			case "return":
				delete(state.ToolRetries, callState.call.ToolName)
				results[callState.idx] = core.ToolReturnPart{
					ToolName:   callState.call.ToolName,
					Content:    output.Content,
					ToolCallID: callState.call.ToolCallID,
					Timestamp:  workflow.Now(ctx),
					Images:     output.Images,
				}
				callState.waitingDeferred = false
				callStates[callState.idx] = nil
				remaining--
			case "retry":
				state.ToolRetries[callState.call.ToolName] = state.ToolRetries[callState.call.ToolName] + 1
				results[callState.idx] = core.RetryPromptPart{
					Content:    output.Message,
					ToolName:   callState.call.ToolName,
					ToolCallID: callState.call.ToolCallID,
					Timestamp:  workflow.Now(ctx),
				}
				callState.waitingDeferred = false
				callStates[callState.idx] = nil
				remaining--
			case "error":
				results[callState.idx] = core.ToolReturnPart{
					ToolName:   callState.call.ToolName,
					Content:    "error: " + output.Message,
					ToolCallID: callState.call.ToolCallID,
					Timestamp:  workflow.Now(ctx),
				}
				callState.waitingDeferred = false
				callStates[callState.idx] = nil
				remaining--
			case "deferred":
				callState.waitingDeferred = true
			default:
				return fmt.Errorf("unknown tool activity output kind %q", output.Kind)
			}
			return nil
		}

		if len(readyConcurrent) > 0 {
			limit := ta.runtime.MaxConcurrency
			if limit <= 0 || limit > len(readyConcurrent) {
				limit = len(readyConcurrent)
			}
			for start := 0; start < len(readyConcurrent); start += limit {
				end := start + limit
				if end > len(readyConcurrent) {
					end = len(readyConcurrent)
				}

				type indexedFuture struct {
					callState *workflowToolCall
					future    workflow.Future
				}
				futures := make([]indexedFuture, 0, end-start)
				for _, callState := range readyConcurrent[start:end] {
					if err := fireToolStart(callState); err != nil {
						return nil, err
					}
					maxRetries := ta.runtime.MaxRetries
					if callState.tool.Tool.MaxRetries != nil {
						maxRetries = *callState.tool.Tool.MaxRetries
					}
					toolCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(callState.tool.Config))
					futures = append(futures, indexedFuture{
						callState: callState,
						future: workflow.ExecuteActivity(toolCtx, callState.tool.ActivityName, ToolActivityInput{
							ArgsJSON:        callState.call.ArgsJSON,
							ToolCallID:      callState.call.ToolCallID,
							Prompt:          prompt,
							DepsJSON:        append([]byte(nil), state.DepsJSON...),
							RunStep:         state.RunStep,
							RunID:           state.RunID,
							RunStartTime:    state.RunStartTime,
							Usage:           state.Usage,
							LastInputTokens: state.LastInputTokens,
							Retries:         state.Retries,
							ToolRetries:     cloneIntMap(state.ToolRetries),
							Retry:           state.ToolRetries[callState.call.ToolName],
							MaxRetries:      maxRetries,
							Messages:        serializedMessages,
							ToolState:       cloneAnyMap(state.ToolState),
						}),
					})
				}
				for _, future := range futures {
					var output ToolActivityOutput
					if err := future.future.Get(ctx, &output); err != nil {
						if hookErr := fireToolEnd(future.callState, nil, err); hookErr != nil {
							return nil, hookErr
						}
						return nil, fmt.Errorf("tool activity %q failed: %w", future.callState.call.ToolName, err)
					}
					if hookErr := fireToolEnd(future.callState, &output, nil); hookErr != nil {
						return nil, hookErr
					}
					if err := applyOutput(future.callState, &output); err != nil {
						return nil, err
					}
				}
			}
		}

		for _, callState := range readySequential {
			output, err := executeOne(callState)
			if err != nil {
				return nil, err
			}
			if err := applyOutput(callState, output); err != nil {
				return nil, err
			}
		}
	}

	var parts []core.ModelRequestPart
	for _, part := range results {
		if part != nil {
			parts = append(parts, part)
		}
	}
	return parts, nil
}

func (ta *TemporalAgent[T]) decodeTextOutput(raw string) (T, error) {
	if ta.runtime.OutputSchema.Mode == core.OutputModeText {
		if textOutput, ok := any(raw).(T); ok {
			return textOutput, nil
		}
	}
	return decodeStructuredOutput[T](raw, ta.runtime.OutputSchema.OuterTypedDictKey)
}

func (ta *TemporalAgent[T]) isOutputTool(name string) bool {
	for _, tool := range ta.runtime.OutputSchema.OutputTools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func limitsToolCalls(limits core.UsageLimits, used, pending int) error {
	if limits.ToolCallsLimit == nil {
		return nil
	}
	projected := used + pending
	if projected > *limits.ToolCallsLimit {
		return &core.UsageLimitExceeded{
			Message: fmt.Sprintf("tool call limit of %d exceeded (used %d, pending %d)", *limits.ToolCallsLimit, used, pending),
		}
	}
	return nil
}

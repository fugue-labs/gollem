package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/fugue-labs/gollem/core"
)

type callbackRunInput struct {
	Prompt          string                   `json:"prompt"`
	Messages        []core.SerializedMessage `json:"messages,omitempty"`
	MessagesJSON    json.RawMessage          `json:"messages_json,omitempty"` // Deprecated: prefer Messages.
	DepsJSON        []byte                   `json:"deps_json,omitempty"`
	Usage           core.RunUsage            `json:"usage"`
	LastInputTokens int                      `json:"last_input_tokens,omitempty"`
	Retries         int                      `json:"retries,omitempty"`
	ToolRetries     map[string]int           `json:"tool_retries,omitempty"`
	RunStep         int                      `json:"run_step"`
	RunID           string                   `json:"run_id"`
	RunStartTime    time.Time                `json:"run_start_time"`
	ToolState       map[string]any           `json:"tool_state,omitempty"`
	ToolName        string                   `json:"tool_name,omitempty"`
	ToolCallID      string                   `json:"tool_call_id,omitempty"`
	Retry           int                      `json:"retry,omitempty"`
	MaxRetries      int                      `json:"max_retries,omitempty"`
}

type dynamicPromptActivityOutput struct {
	Prompts []string `json:"prompts,omitempty"`
}

type historyProcessorActivityOutput struct {
	Messages     []core.SerializedMessage `json:"messages,omitempty"`
	MessagesJSON json.RawMessage          `json:"messages_json,omitempty"` // Deprecated: prefer Messages.
}

type autoContextActivityInput struct {
	Run callbackRunInput `json:"run"`
}

type autoContextActivityOutput struct {
	Messages     []core.SerializedMessage     `json:"messages,omitempty"`
	MessagesJSON json.RawMessage              `json:"messages_json,omitempty"` // Deprecated: prefer Messages.
	Changed      bool                         `json:"changed,omitempty"`
	Stats        *core.ContextCompactionStats `json:"stats,omitempty"`
}

type toolPrepareActivityInput struct {
	Run callbackRunInput `json:"run"`
}

type toolPrepareActivityOutput struct {
	Definitions []core.ToolDefinition `json:"definitions,omitempty"`
}

type messageInterceptorActivityOutput struct {
	Messages     []core.SerializedMessage `json:"messages,omitempty"`
	MessagesJSON json.RawMessage          `json:"messages_json,omitempty"` // Deprecated: prefer Messages.
	Dropped      bool                     `json:"dropped,omitempty"`
}

type responseInterceptorActivityInput struct {
	Response     *core.SerializedMessage `json:"response,omitempty"`
	ResponseJSON json.RawMessage         `json:"response_json,omitempty"` // Deprecated: prefer Response.
}

type responseInterceptorActivityOutput struct {
	Response     *core.SerializedMessage `json:"response,omitempty"`
	ResponseJSON json.RawMessage         `json:"response_json,omitempty"` // Deprecated: prefer Response.
	Dropped      bool                    `json:"dropped,omitempty"`
}

type outputRepairActivityInput struct {
	Run        callbackRunInput `json:"run"`
	Raw        string           `json:"raw"`
	ParseError string           `json:"parse_error"`
}

type outputRepairActivityOutput struct {
	OutputJSON json.RawMessage `json:"output_json"`
}

type outputValidateActivityInput struct {
	Run        callbackRunInput `json:"run"`
	OutputJSON json.RawMessage  `json:"output_json"`
}

type outputValidateActivityOutput struct {
	OutputJSON    json.RawMessage `json:"output_json,omitempty"`
	RetryMessage  string          `json:"retry_message,omitempty"`
	FatalError    string          `json:"fatal_error,omitempty"`
	ValidationErr string          `json:"validation_error,omitempty"`
}

type toolApprovalActivityInput struct {
	ToolName string `json:"tool_name"`
	ArgsJSON string `json:"args_json"`
}

type toolApprovalActivityOutput struct {
	Approved bool   `json:"approved"`
	Error    string `json:"error,omitempty"`
}

type guardrailEvaluation struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

type inputGuardrailActivityInput struct {
	Run callbackRunInput `json:"run"`
}

type inputGuardrailActivityOutput struct {
	Prompt      string                `json:"prompt"`
	Evaluations []guardrailEvaluation `json:"evaluations,omitempty"`
	Rejected    bool                  `json:"rejected,omitempty"`
}

type turnGuardrailActivityInput struct {
	Run callbackRunInput `json:"run"`
}

type turnGuardrailActivityOutput struct {
	Evaluations []guardrailEvaluation `json:"evaluations,omitempty"`
	Rejected    bool                  `json:"rejected,omitempty"`
}

type runConditionActivityInput struct {
	Run          callbackRunInput        `json:"run"`
	Response     *core.SerializedMessage `json:"response,omitempty"`
	ResponseJSON json.RawMessage         `json:"response_json,omitempty"` // Deprecated: prefer Response.
}

type runConditionActivityOutput struct {
	Stopped bool   `json:"stopped"`
	Reason  string `json:"reason,omitempty"`
}

type knowledgeRetrieveActivityInput struct {
	Prompt string `json:"prompt"`
}

type knowledgeRetrieveActivityOutput struct {
	Content string `json:"content,omitempty"`
}

type knowledgeStoreActivityInput struct {
	Content string `json:"content"`
}

type eventBusActivityInput struct {
	EventType string `json:"event_type"`
	RunID     string `json:"run_id"`
	Prompt    string `json:"prompt,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	ArgsJSON  string `json:"args_json,omitempty"`
	Success   bool   `json:"success,omitempty"`
	Error     string `json:"error,omitempty"`
}

type traceExportActivityInput struct {
	Trace     *core.RunTrace  `json:"trace,omitempty"`
	TraceJSON json.RawMessage `json:"trace_json,omitempty"` // Deprecated: prefer Trace.
}

type hookActivityInput struct {
	Run          callbackRunInput             `json:"run"`
	Event        string                       `json:"event"`
	Response     *core.SerializedMessage      `json:"response,omitempty"`
	ResponseJSON json.RawMessage              `json:"response_json,omitempty"` // Deprecated: prefer Response.
	ArgsJSON     string                       `json:"args_json,omitempty"`
	Result       string                       `json:"result,omitempty"`
	Error        string                       `json:"error,omitempty"`
	TurnNumber   int                          `json:"turn_number,omitempty"`
	Passed       bool                         `json:"passed,omitempty"`
	Reason       string                       `json:"reason,omitempty"`
	Stats        *core.ContextCompactionStats `json:"stats,omitempty"`
}

const (
	hookEventRunStart          = "run_start"
	hookEventRunEnd            = "run_end"
	hookEventModelRequest      = "model_request"
	hookEventModelResponse     = "model_response"
	hookEventToolStart         = "tool_start"
	hookEventToolEnd           = "tool_end"
	hookEventTurnStart         = "turn_start"
	hookEventTurnEnd           = "turn_end"
	hookEventOutputValidation  = "output_validation"
	hookEventOutputRepair      = "output_repair"
	hookEventRunCondition      = "run_condition"
	hookEventContextCompaction = "context_compaction"
)

func (ta *TemporalAgent[T]) inputGuardrailActivityName() string {
	return "agent__" + ta.regName + "__input_guardrails"
}

func (ta *TemporalAgent[T]) turnGuardrailActivityName() string {
	return "agent__" + ta.regName + "__turn_guardrails"
}

func (ta *TemporalAgent[T]) runConditionActivityName() string {
	return "agent__" + ta.regName + "__run_conditions"
}

func (ta *TemporalAgent[T]) knowledgeRetrieveActivityName() string {
	return "agent__" + ta.regName + "__knowledge_retrieve"
}

func (ta *TemporalAgent[T]) knowledgeStoreActivityName() string {
	return "agent__" + ta.regName + "__knowledge_store"
}

func (ta *TemporalAgent[T]) hookActivityName() string {
	return "agent__" + ta.regName + "__hooks"
}

func (ta *TemporalAgent[T]) eventBusActivityName() string {
	return "agent__" + ta.regName + "__event_bus"
}

func (ta *TemporalAgent[T]) traceExportActivityName() string {
	return "agent__" + ta.regName + "__trace_export"
}

func (ta *TemporalAgent[T]) dynamicPromptActivityName() string {
	return "agent__" + ta.regName + "__dynamic_system_prompts"
}

func (ta *TemporalAgent[T]) historyProcessorActivityName() string {
	return "agent__" + ta.regName + "__history_processors"
}

func (ta *TemporalAgent[T]) autoContextActivityName() string {
	return "agent__" + ta.regName + "__auto_context"
}

func (ta *TemporalAgent[T]) toolPrepareActivityName() string {
	return "agent__" + ta.regName + "__tool_prepare"
}

func (ta *TemporalAgent[T]) messageInterceptorActivityName() string {
	return "agent__" + ta.regName + "__message_interceptors"
}

func (ta *TemporalAgent[T]) responseInterceptorActivityName() string {
	return "agent__" + ta.regName + "__response_interceptors"
}

func (ta *TemporalAgent[T]) outputRepairActivityName() string {
	return "agent__" + ta.regName + "__output_repair"
}

func (ta *TemporalAgent[T]) outputValidateActivityName() string {
	return "agent__" + ta.regName + "__output_validate"
}

func (ta *TemporalAgent[T]) toolApprovalActivityName() string {
	return "agent__" + ta.regName + "__tool_approval"
}

func (ta *TemporalAgent[T]) callbackActivities() map[string]any {
	activities := make(map[string]any)
	if len(ta.runtime.Hooks) > 0 {
		activities[ta.hookActivityName()] = ta.hookActivity
	}
	if ta.runtime.EventBus != nil {
		activities[ta.eventBusActivityName()] = ta.eventBusActivity
	}
	if len(ta.runtime.TraceExporters) > 0 {
		activities[ta.traceExportActivityName()] = ta.traceExportActivity
	}
	if len(ta.runtime.InputGuardrails) > 0 {
		activities[ta.inputGuardrailActivityName()] = ta.inputGuardrailActivity
	}
	if len(ta.runtime.TurnGuardrails) > 0 {
		activities[ta.turnGuardrailActivityName()] = ta.turnGuardrailActivity
	}
	if len(ta.runtime.DynamicSystemPrompts) > 0 {
		activities[ta.dynamicPromptActivityName()] = ta.dynamicPromptActivity
	}
	if len(ta.runtime.HistoryProcessors) > 0 {
		activities[ta.historyProcessorActivityName()] = ta.historyProcessorActivity
	}
	if ta.runtime.AutoContext != nil {
		activities[ta.autoContextActivityName()] = ta.autoContextActivity
	}
	if ta.runtime.AgentToolsPrepare != nil || runtimeHasToolPrepareFuncs(ta.runtime.Tools) {
		activities[ta.toolPrepareActivityName()] = ta.toolPrepareActivity
	}
	if len(ta.runtime.MessageInterceptors) > 0 {
		activities[ta.messageInterceptorActivityName()] = ta.messageInterceptorActivity
	}
	if len(ta.runtime.ResponseInterceptors) > 0 {
		activities[ta.responseInterceptorActivityName()] = ta.responseInterceptorActivity
	}
	if ta.runtime.OutputRepair != nil {
		activities[ta.outputRepairActivityName()] = ta.outputRepairActivity
	}
	if len(ta.runtime.OutputValidators) > 0 {
		activities[ta.outputValidateActivityName()] = ta.outputValidateActivity
	}
	if ta.runtime.ToolApprovalFunc != nil {
		activities[ta.toolApprovalActivityName()] = ta.toolApprovalActivity
	}
	if len(ta.runtime.RunConditions) > 0 {
		activities[ta.runConditionActivityName()] = ta.runConditionActivity
	}
	if ta.runtime.KnowledgeBase != nil {
		activities[ta.knowledgeRetrieveActivityName()] = ta.knowledgeRetrieveActivity
		activities[ta.knowledgeStoreActivityName()] = ta.knowledgeStoreActivity
	}
	return activities
}

func (ta *TemporalAgent[T]) buildCallbackRunContext(input callbackRunInput) (*core.RunContext, error) {
	messages, err := decodeSerializedMessages(input.Messages, input.MessagesJSON)
	if err != nil {
		return nil, err
	}
	deps, err := ta.resolveDeps(input.DepsJSON)
	if err != nil {
		return nil, err
	}
	return core.NewRunContext(core.RunContext{
		Deps:         deps,
		Usage:        input.Usage,
		Prompt:       input.Prompt,
		Messages:     messages,
		RunStep:      input.RunStep,
		RunID:        input.RunID,
		RunStartTime: input.RunStartTime,
		EventBus:     ta.runtime.EventBus,
		ToolName:     input.ToolName,
		ToolCallID:   input.ToolCallID,
		Retry:        input.Retry,
		MaxRetries:   input.MaxRetries,
	}, func() map[string]any {
		return cloneAnyMap(input.ToolState)
	}, func() *core.RunStateSnapshot {
		return buildTemporalRunStateSnapshot(
			input.Prompt,
			messages,
			input.Usage,
			input.LastInputTokens,
			input.Retries,
			input.ToolRetries,
			input.RunID,
			input.RunStep,
			input.RunStartTime,
			input.ToolState,
		)
	}), nil
}

func (ta *TemporalAgent[T]) fireHook(ctx workflow.Context, input hookActivityInput) error {
	if len(ta.runtime.Hooks) == 0 {
		return nil
	}
	callbackCtx := workflow.WithActivityOptions(ctx, buildActivityOptions(ta.config.defaultConfig))
	return workflow.ExecuteActivity(callbackCtx, ta.hookActivityName(), input).Get(callbackCtx, nil)
}

func (ta *TemporalAgent[T]) hookActivity(ctx context.Context, input hookActivityInput) error {
	if len(ta.runtime.Hooks) == 0 {
		return nil
	}
	rc, err := ta.buildCallbackRunContext(input.Run)
	if err != nil {
		return err
	}

	var resp *core.ModelResponse
	if input.Response != nil || len(input.ResponseJSON) > 0 {
		resp, err = decodeResponse(input.Response, input.ResponseJSON)
		if err != nil {
			return err
		}
	}

	var hookErr error
	if input.Error != "" {
		hookErr = errors.New(input.Error)
	}

	for _, hook := range ta.runtime.Hooks {
		switch input.Event {
		case hookEventRunStart:
			if hook.OnRunStart != nil {
				hook.OnRunStart(ctx, rc, input.Run.Prompt)
			}
		case hookEventRunEnd:
			if hook.OnRunEnd != nil {
				hook.OnRunEnd(ctx, rc, rc.Messages, hookErr)
			}
		case hookEventModelRequest:
			if hook.OnModelRequest != nil {
				hook.OnModelRequest(ctx, rc, rc.Messages)
			}
		case hookEventModelResponse:
			if hook.OnModelResponse != nil && resp != nil {
				hook.OnModelResponse(ctx, rc, resp)
			}
		case hookEventToolStart:
			if hook.OnToolStart != nil {
				hook.OnToolStart(ctx, rc, input.Run.ToolCallID, input.Run.ToolName, input.ArgsJSON)
			}
		case hookEventToolEnd:
			if hook.OnToolEnd != nil {
				hook.OnToolEnd(ctx, rc, input.Run.ToolCallID, input.Run.ToolName, input.Result, hookErr)
			}
		case hookEventTurnStart:
			if hook.OnTurnStart != nil {
				hook.OnTurnStart(ctx, rc, input.TurnNumber)
			}
		case hookEventTurnEnd:
			if hook.OnTurnEnd != nil && resp != nil {
				hook.OnTurnEnd(ctx, rc, input.TurnNumber, resp)
			}
		case hookEventOutputValidation:
			if hook.OnOutputValidation != nil {
				hook.OnOutputValidation(ctx, rc, input.Passed, hookErr)
			}
		case hookEventOutputRepair:
			if hook.OnOutputRepair != nil {
				hook.OnOutputRepair(ctx, rc, input.Passed, hookErr)
			}
		case hookEventRunCondition:
			if hook.OnRunConditionChecked != nil {
				hook.OnRunConditionChecked(ctx, rc, input.Passed, input.Reason)
			}
		case hookEventContextCompaction:
			if hook.OnContextCompaction != nil && input.Stats != nil {
				hook.OnContextCompaction(ctx, rc, *input.Stats)
			}
		default:
			return fmt.Errorf("unknown hook event %q", input.Event)
		}
	}
	return nil
}

func (ta *TemporalAgent[T]) eventBusActivity(ctx context.Context, input eventBusActivityInput) error {
	if ta.runtime.EventBus == nil {
		return nil
	}
	switch input.EventType {
	case hookEventRunStart:
		core.Publish(ta.runtime.EventBus, core.RunStartedEvent{
			RunID:  input.RunID,
			Prompt: input.Prompt,
		})
	case hookEventRunEnd:
		core.Publish(ta.runtime.EventBus, core.RunCompletedEvent{
			RunID:   input.RunID,
			Success: input.Success,
			Error:   input.Error,
		})
	case hookEventToolStart:
		core.Publish(ta.runtime.EventBus, core.ToolCalledEvent{
			RunID:    input.RunID,
			ToolName: input.ToolName,
			ArgsJSON: input.ArgsJSON,
		})
	default:
		return fmt.Errorf("unknown event bus activity type %q", input.EventType)
	}
	return nil
}

func (ta *TemporalAgent[T]) traceExportActivity(ctx context.Context, input traceExportActivityInput) error {
	if (input.Trace == nil && len(input.TraceJSON) == 0) || len(ta.runtime.TraceExporters) == 0 {
		return nil
	}
	trace, err := decodeTrace(input.Trace, input.TraceJSON)
	if err != nil {
		return fmt.Errorf("unmarshal trace for export: %w", err)
	}
	for _, exporter := range ta.runtime.TraceExporters {
		// Exporter errors remain non-fatal to match local agent behavior.
		_ = exporter.Export(ctx, trace)
	}
	return nil
}

func (ta *TemporalAgent[T]) inputGuardrailActivity(ctx context.Context, input inputGuardrailActivityInput) (*inputGuardrailActivityOutput, error) {
	prompt := input.Run.Prompt
	rc, err := ta.buildCallbackRunContext(input.Run)
	if err != nil {
		return nil, err
	}
	output := &inputGuardrailActivityOutput{Prompt: prompt}
	for _, guardrail := range ta.runtime.InputGuardrails {
		nextPrompt, guardrailErr := guardrail.Func(ctx, prompt)
		eval := guardrailEvaluation{
			Name:   guardrail.Name,
			Passed: guardrailErr == nil,
		}
		if guardrailErr == nil {
			ta.runGuardrailHooks(ctx, rc, eval)
			output.Evaluations = append(output.Evaluations, eval)
			prompt = nextPrompt
			output.Prompt = prompt
			continue
		}
		eval.Message = guardrailErr.Error()
		ta.runGuardrailHooks(ctx, rc, eval)
		output.Evaluations = append(output.Evaluations, eval)
		output.Rejected = true
		break
	}
	return output, nil
}

func (ta *TemporalAgent[T]) turnGuardrailActivity(ctx context.Context, input turnGuardrailActivityInput) (*turnGuardrailActivityOutput, error) {
	rc, err := ta.buildCallbackRunContext(input.Run)
	if err != nil {
		return nil, err
	}
	output := &turnGuardrailActivityOutput{}
	for _, guardrail := range ta.runtime.TurnGuardrails {
		guardrailErr := guardrail.Func(ctx, rc, rc.Messages)
		eval := guardrailEvaluation{
			Name:   guardrail.Name,
			Passed: guardrailErr == nil,
		}
		if guardrailErr == nil {
			ta.runGuardrailHooks(ctx, rc, eval)
			output.Evaluations = append(output.Evaluations, eval)
			continue
		}
		eval.Message = guardrailErr.Error()
		ta.runGuardrailHooks(ctx, rc, eval)
		output.Evaluations = append(output.Evaluations, eval)
		output.Rejected = true
		break
	}
	return output, nil
}

func (ta *TemporalAgent[T]) dynamicPromptActivity(ctx context.Context, input callbackRunInput) (*dynamicPromptActivityOutput, error) {
	rc, err := ta.buildCallbackRunContext(input)
	if err != nil {
		return nil, err
	}
	prompts := make([]string, 0, len(ta.runtime.DynamicSystemPrompts))
	for _, fn := range ta.runtime.DynamicSystemPrompts {
		prompt, err := fn(ctx, rc)
		if err != nil {
			return nil, err
		}
		if prompt != "" {
			prompts = append(prompts, prompt)
		}
	}
	return &dynamicPromptActivityOutput{Prompts: prompts}, nil
}

func (ta *TemporalAgent[T]) historyProcessorActivity(ctx context.Context, input callbackRunInput) (*historyProcessorActivityOutput, error) {
	messages, err := decodeSerializedMessages(input.Messages, input.MessagesJSON)
	if err != nil {
		return nil, err
	}
	for _, proc := range ta.runtime.HistoryProcessors {
		messages, err = proc(ctx, messages)
		if err != nil {
			return nil, err
		}
	}
	serialized, err := core.EncodeMessages(messages)
	if err != nil {
		return nil, fmt.Errorf("marshal processed messages: %w", err)
	}
	return &historyProcessorActivityOutput{Messages: serialized}, nil
}

func (ta *TemporalAgent[T]) autoContextActivity(ctx context.Context, input autoContextActivityInput) (*autoContextActivityOutput, error) {
	if ta.runtime.AutoContext == nil {
		return &autoContextActivityOutput{}, nil
	}
	messages, err := decodeSerializedMessages(input.Run.Messages, input.Run.MessagesJSON)
	if err != nil {
		return nil, err
	}
	beforeCount := len(messages)
	beforeTokens := core.EstimateTokens(messages)
	if input.Run.LastInputTokens > beforeTokens {
		beforeTokens = input.Run.LastInputTokens
	}
	compressed, err := core.AutoCompressMessages(ctx, messages, ta.runtime.AutoContext, ta.model.wrapped, beforeTokens)
	if err != nil {
		return nil, err
	}
	if len(compressed) >= beforeCount {
		return &autoContextActivityOutput{Changed: false}, nil
	}
	serialized, err := core.EncodeMessages(compressed)
	if err != nil {
		return nil, fmt.Errorf("marshal auto-context messages: %w", err)
	}
	return &autoContextActivityOutput{
		Messages: serialized,
		Changed:  true,
		Stats: &core.ContextCompactionStats{
			Strategy:              core.CompactionStrategyAutoSummary,
			MessagesBefore:        beforeCount,
			MessagesAfter:         len(compressed),
			EstimatedTokensBefore: beforeTokens,
			EstimatedTokensAfter:  core.EstimateTokens(compressed),
		},
	}, nil
}

func (ta *TemporalAgent[T]) toolPrepareActivity(ctx context.Context, input toolPrepareActivityInput) (*toolPrepareActivityOutput, error) {
	rc, err := ta.buildCallbackRunContext(input.Run)
	if err != nil {
		return nil, err
	}

	prepared := make([]core.Tool, 0, len(ta.runtime.Tools))
	for _, tool := range ta.runtime.Tools {
		if tool.PrepareFunc != nil {
			def := tool.PrepareFunc(ctx, rc, tool.Definition)
			if def == nil {
				continue
			}
			modified := tool
			modified.Definition = *def
			prepared = append(prepared, modified)
			continue
		}
		prepared = append(prepared, tool)
	}

	if ta.runtime.AgentToolsPrepare != nil {
		defs := make([]core.ToolDefinition, len(prepared))
		for i, tool := range prepared {
			defs[i] = tool.Definition
		}
		filtered := ta.runtime.AgentToolsPrepare(ctx, rc, defs)
		retained := make(map[string]core.ToolDefinition, len(filtered))
		for _, def := range filtered {
			retained[def.Name] = def
		}
		result := make([]core.ToolDefinition, 0, len(prepared))
		for _, tool := range prepared {
			if def, ok := retained[tool.Definition.Name]; ok {
				result = append(result, def)
			}
		}
		return &toolPrepareActivityOutput{Definitions: result}, nil
	}

	defs := make([]core.ToolDefinition, 0, len(prepared))
	for _, tool := range prepared {
		defs = append(defs, tool.Definition)
	}
	return &toolPrepareActivityOutput{Definitions: defs}, nil
}

func (ta *TemporalAgent[T]) messageInterceptorActivity(ctx context.Context, input callbackRunInput) (*messageInterceptorActivityOutput, error) {
	messages, err := decodeSerializedMessages(input.Messages, input.MessagesJSON)
	if err != nil {
		return nil, err
	}
	messages, dropped := runTemporalMessageInterceptors(ctx, ta.runtime.MessageInterceptors, messages)
	if dropped {
		return &messageInterceptorActivityOutput{Dropped: true}, nil
	}
	serialized, err := core.EncodeMessages(messages)
	if err != nil {
		return nil, fmt.Errorf("marshal intercepted messages: %w", err)
	}
	return &messageInterceptorActivityOutput{Messages: serialized}, nil
}

func (ta *TemporalAgent[T]) responseInterceptorActivity(ctx context.Context, input responseInterceptorActivityInput) (*responseInterceptorActivityOutput, error) {
	resp, err := decodeResponse(input.Response, input.ResponseJSON)
	if err != nil {
		return nil, err
	}
	dropped := runTemporalResponseInterceptors(ctx, ta.runtime.ResponseInterceptors, resp)
	if dropped {
		return &responseInterceptorActivityOutput{Dropped: true}, nil
	}
	response, err := core.EncodeModelResponse(resp)
	if err != nil {
		return nil, err
	}
	return &responseInterceptorActivityOutput{Response: response}, nil
}

func (ta *TemporalAgent[T]) outputRepairActivity(ctx context.Context, input outputRepairActivityInput) (*outputRepairActivityOutput, error) {
	if ta.runtime.OutputRepair == nil {
		return nil, errors.New("output repair is not configured")
	}
	repaired, err := ta.runtime.OutputRepair(ctx, input.Raw, errors.New(input.ParseError))
	if err != nil {
		return nil, err
	}
	outputJSON, err := json.Marshal(repaired)
	if err != nil {
		return nil, fmt.Errorf("marshal repaired output: %w", err)
	}
	return &outputRepairActivityOutput{OutputJSON: outputJSON}, nil
}

func (ta *TemporalAgent[T]) outputValidateActivity(ctx context.Context, input outputValidateActivityInput) (*outputValidateActivityOutput, error) {
	rc, err := ta.buildCallbackRunContext(input.Run)
	if err != nil {
		return nil, err
	}
	var output T
	if err := json.Unmarshal(input.OutputJSON, &output); err != nil {
		return nil, fmt.Errorf("unmarshal output for validation: %w", err)
	}
	for _, validator := range ta.runtime.OutputValidators {
		output, err = validator(ctx, rc, output)
		if err != nil {
			var retryErr *core.ModelRetryError
			if errors.As(err, &retryErr) {
				return &outputValidateActivityOutput{
					RetryMessage:  retryErr.Message,
					ValidationErr: err.Error(),
				}, nil
			}
			return &outputValidateActivityOutput{
				FatalError:    err.Error(),
				ValidationErr: err.Error(),
			}, nil
		}
	}
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("marshal validated output: %w", err)
	}
	return &outputValidateActivityOutput{OutputJSON: outputJSON}, nil
}

func (ta *TemporalAgent[T]) toolApprovalActivity(ctx context.Context, input toolApprovalActivityInput) (*toolApprovalActivityOutput, error) {
	if ta.runtime.ToolApprovalFunc == nil {
		return nil, errors.New("tool approval callback is not configured")
	}
	output := &toolApprovalActivityOutput{}
	approved, approvalErr := ta.runtime.ToolApprovalFunc(ctx, input.ToolName, input.ArgsJSON)
	if approvalErr != nil {
		output.Error = approvalErr.Error()
	} else {
		output.Approved = approved
	}
	return output, nil
}

func (ta *TemporalAgent[T]) runConditionActivity(ctx context.Context, input runConditionActivityInput) (*runConditionActivityOutput, error) {
	rc, err := ta.buildCallbackRunContext(input.Run)
	if err != nil {
		return nil, err
	}
	resp, err := decodeResponse(input.Response, input.ResponseJSON)
	if err != nil {
		return nil, err
	}
	for _, condition := range ta.runtime.RunConditions {
		if stop, reason := condition(ctx, rc, resp); stop {
			return &runConditionActivityOutput{
				Stopped: true,
				Reason:  reason,
			}, nil
		}
	}
	return &runConditionActivityOutput{}, nil
}

func (ta *TemporalAgent[T]) knowledgeRetrieveActivity(ctx context.Context, input knowledgeRetrieveActivityInput) (*knowledgeRetrieveActivityOutput, error) {
	if ta.runtime.KnowledgeBase == nil {
		return nil, errors.New("knowledge base is not configured")
	}
	content, err := ta.runtime.KnowledgeBase.Retrieve(ctx, input.Prompt)
	if err != nil {
		return nil, err
	}
	return &knowledgeRetrieveActivityOutput{Content: content}, nil
}

func (ta *TemporalAgent[T]) knowledgeStoreActivity(ctx context.Context, input knowledgeStoreActivityInput) error {
	if ta.runtime.KnowledgeBase == nil {
		return errors.New("knowledge base is not configured")
	}
	return ta.runtime.KnowledgeBase.Store(ctx, input.Content)
}

func runTemporalMessageInterceptors(ctx context.Context, interceptors []core.MessageInterceptor, messages []core.ModelMessage) ([]core.ModelMessage, bool) {
	for _, interceptor := range interceptors {
		result := interceptor(ctx, messages)
		switch result.Action {
		case core.MessageDrop:
			return nil, true
		case core.MessageModify:
			messages = result.Messages
		case core.MessageAllow:
		}
	}
	return messages, false
}

func runTemporalResponseInterceptors(ctx context.Context, interceptors []core.ResponseInterceptor, resp *core.ModelResponse) bool {
	for _, interceptor := range interceptors {
		result := interceptor(ctx, resp)
		if result.Action == core.MessageDrop {
			return true
		}
		if result.Action == core.MessageModify && len(result.Messages) > 0 {
			if modified, ok := result.Messages[0].(core.ModelResponse); ok {
				resp.Parts = modified.Parts
			}
		}
	}
	return false
}

func runtimeHasToolPrepareFuncs(tools []core.Tool) bool {
	for _, tool := range tools {
		if tool.PrepareFunc != nil {
			return true
		}
	}
	return false
}

func (ta *TemporalAgent[T]) runGuardrailHooks(ctx context.Context, rc *core.RunContext, eval guardrailEvaluation) {
	for _, hook := range ta.runtime.Hooks {
		if hook.OnGuardrailEvaluated != nil {
			var hookErr error
			if eval.Message != "" {
				hookErr = errors.New(eval.Message)
			}
			hook.OnGuardrailEvaluated(ctx, rc, eval.Name, eval.Passed, hookErr)
		}
	}
}

func decodeResponse(response *core.SerializedMessage, data []byte) (*core.ModelResponse, error) {
	if response != nil {
		return core.DecodeModelResponse(response)
	}
	return decodeResponseJSON(data)
}

func decodeResponseJSON(data []byte) (*core.ModelResponse, error) {
	messages, err := core.UnmarshalMessages(data)
	if err != nil {
		return nil, fmt.Errorf("unmarshal model response: %w", err)
	}
	if len(messages) != 1 {
		return nil, fmt.Errorf("expected 1 model response message, got %d", len(messages))
	}
	resp, ok := messages[0].(core.ModelResponse)
	if !ok {
		return nil, fmt.Errorf("expected model response, got %T", messages[0])
	}
	return &resp, nil
}

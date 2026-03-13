package core

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type runtimeConfigOutput struct {
	Answer string `json:"answer"`
}

type runtimeConfigDeps struct {
	Token string `json:"token"`
}

type runtimeConfigExporter struct{}

func (runtimeConfigExporter) Export(context.Context, *RunTrace) error { return nil }

func TestAgentRuntimeConfig_ClonesAndExports(t *testing.T) {
	model := NewTestModel(TextResponse("ok"))

	directTool := FuncTool[struct {
		Value int `json:"value"`
	}]("direct_tool", "Direct tool", func(_ context.Context, _ struct {
		Value int `json:"value"`
	}) (string, error) {
		return "direct", nil
	})
	preparedTool := FuncTool[struct{}]("prepared_tool", "Prepared tool", func(_ context.Context, _ struct{}) (string, error) {
		return "prepared", nil
	})
	preparedTool.PrepareFunc = func(_ context.Context, _ *RunContext, def ToolDefinition) *ToolDefinition {
		def.Description = "prepared copy"
		return &def
	}
	toolsetTool := FuncTool[struct{}]("toolset_tool", "Toolset tool", func(_ context.Context, _ struct{}) (string, error) {
		return "toolset", nil
	})

	reqLimit := 4
	toolCallLimit := 7
	temperature := 0.7
	maxTokens := 128
	topP := 0.9
	thinking := 256
	effort := "high"
	quota := &UsageQuota{
		MaxRequests:    5,
		MaxTotalTokens: 1000,
	}
	tracker := NewCostTracker(map[string]ModelPricing{
		model.ModelName(): {
			InputTokenCost:  0.001,
			OutputTokenCost: 0.002,
		},
	})
	tracker.currency = "EUR"
	bus := NewEventBus()
	kb := NewStaticKnowledgeBase("facts")
	deps := runtimeConfigDeps{Token: "abc123"}

	agent := NewAgent[runtimeConfigOutput](model)
	agent.systemPrompts = []string{"system prompt"}
	agent.dynamicSystemPrompts = []SystemPromptFunc{
		func(_ context.Context, _ *RunContext) (string, error) { return "dynamic prompt", nil },
	}
	agent.historyProcessors = []HistoryProcessor{
		func(_ context.Context, messages []ModelMessage) ([]ModelMessage, error) { return messages, nil },
	}
	agent.tools = []Tool{directTool, preparedTool}
	agent.toolsets = []*Toolset{NewToolset("shared", toolsetTool)}
	agent.hooks = []Hook{{
		OnRunStart: func(_ context.Context, _ *RunContext, _ string) {},
	}}
	agent.inputGuardrails = []namedInputGuardrail{{
		name: "input_guardrail",
		fn: func(_ context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	}}
	agent.turnGuardrails = []namedTurnGuardrail{{
		name: "turn_guardrail",
		fn:   func(_ context.Context, _ *RunContext, _ []ModelMessage) error { return nil },
	}}
	agent.toolsPrepareFunc = func(_ context.Context, _ *RunContext, tools []ToolDefinition) []ToolDefinition {
		return tools
	}
	agent.outputSchema = buildOutputSchema[runtimeConfigOutput](
		WithOutputMode(OutputModeNative),
		WithOutputToolName("structured_output"),
		WithOutputToolDescription("Structured output"),
	)
	agent.outputValidators = []OutputValidatorFunc[runtimeConfigOutput]{
		func(_ context.Context, _ *RunContext, output runtimeConfigOutput) (runtimeConfigOutput, error) {
			return output, nil
		},
	}
	agent.repairFunc = RepairFunc[runtimeConfigOutput](func(_ context.Context, _ string, _ error) (runtimeConfigOutput, error) {
		return runtimeConfigOutput{Answer: "fixed"}, nil
	})
	agent.modelSettings = &ModelSettings{
		MaxTokens:       &maxTokens,
		Temperature:     &temperature,
		TopP:            &topP,
		ToolChoice:      ToolChoiceForce("direct_tool"),
		ThinkingBudget:  &thinking,
		ReasoningEffort: &effort,
	}
	agent.usageLimits = UsageLimits{
		RequestLimit:   &reqLimit,
		ToolCallsLimit: &toolCallLimit,
	}
	agent.usageQuota = quota
	agent.maxRetries = 6
	agent.endStrategy = EndStrategyExhaustive
	agent.maxConcurrency = 3
	agent.defaultToolTimeout = 2 * time.Second
	agent.toolChoice = ToolChoiceRequired()
	agent.toolChoiceAutoReset = true
	agent.toolApprovalFunc = func(_ context.Context, _ string, _ string) (bool, error) { return true, nil }
	agent.globalToolResultValidators = []ToolResultValidatorFunc{
		func(_ context.Context, _ *RunContext, _ string, _ string) error { return nil },
	}
	agent.middleware = []RequestMiddlewareFunc{
		func(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters, next func(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error)) (*ModelResponse, error) {
			return next(ctx, messages, settings, params)
		},
	}
	agent.streamMiddleware = []AgentStreamMiddleware{
		func(_ context.Context, _ []ModelMessage, _ *ModelSettings, _ *ModelRequestParameters, _ AgentStreamFunc) (StreamedResponse, error) {
			return nil, nil
		},
	}
	agent.messageInterceptors = []MessageInterceptor{
		func(_ context.Context, messages []ModelMessage) InterceptResult {
			return InterceptResult{Action: MessageAllow, Messages: messages}
		},
	}
	agent.responseInterceptors = []ResponseInterceptor{
		func(_ context.Context, _ *ModelResponse) InterceptResult {
			return InterceptResult{Action: MessageAllow}
		},
	}
	agent.runConditions = []RunCondition{
		func(_ context.Context, _ *RunContext, _ *ModelResponse) (bool, string) { return false, "" },
	}
	agent.tracingEnabled = true
	agent.traceExporters = []TraceExporter{runtimeConfigExporter{}}
	agent.eventBus = bus
	agent.deps = deps
	agent.autoContext = &AutoContextConfig{
		MaxTokens:    200,
		KeepLastN:    3,
		SummaryModel: model,
	}
	agent.knowledgeBase = kb
	agent.kbAutoStore = true
	agent.costTracker = tracker

	cfg := agent.RuntimeConfig()

	if len(cfg.Tools) != 3 {
		t.Fatalf("expected 3 tools including toolset tools, got %d", len(cfg.Tools))
	}
	if cfg.Tools[0].Definition.Name != "direct_tool" || cfg.Tools[2].Definition.Name != "toolset_tool" {
		t.Fatalf("unexpected tool names: %+v", []string{
			cfg.Tools[0].Definition.Name,
			cfg.Tools[1].Definition.Name,
			cfg.Tools[2].Definition.Name,
		})
	}
	if len(cfg.SystemPrompts) != 1 || cfg.SystemPrompts[0] != "system prompt" {
		t.Fatalf("unexpected system prompts: %+v", cfg.SystemPrompts)
	}
	if len(cfg.DynamicSystemPrompts) != 1 || cfg.DynamicSystemPrompts[0] == nil {
		t.Fatalf("expected cloned dynamic system prompts, got %+v", cfg.DynamicSystemPrompts)
	}
	if len(cfg.HistoryProcessors) != 1 || cfg.HistoryProcessors[0] == nil {
		t.Fatalf("expected cloned history processors, got %+v", cfg.HistoryProcessors)
	}
	if len(cfg.Hooks) != 1 || cfg.Hooks[0].OnRunStart == nil {
		t.Fatalf("expected cloned hooks, got %+v", cfg.Hooks)
	}
	if len(cfg.InputGuardrails) != 1 || cfg.InputGuardrails[0].Name != "input_guardrail" {
		t.Fatalf("unexpected input guardrails: %+v", cfg.InputGuardrails)
	}
	if len(cfg.TurnGuardrails) != 1 || cfg.TurnGuardrails[0].Name != "turn_guardrail" {
		t.Fatalf("unexpected turn guardrails: %+v", cfg.TurnGuardrails)
	}
	if cfg.AgentToolsPrepare == nil {
		t.Fatal("expected agent tools prepare func")
	}
	if cfg.OutputSchema == nil || cfg.OutputSchema.OutputObject == nil {
		t.Fatalf("expected native output schema clone, got %+v", cfg.OutputSchema)
	}
	if cfg.OutputSchema.OutputTools[0].Name != "structured_output" {
		t.Fatalf("unexpected output tool name %q", cfg.OutputSchema.OutputTools[0].Name)
	}
	if cfg.OutputRepair == nil {
		t.Fatal("expected output repair func")
	}
	if len(cfg.OutputValidators) != 1 || cfg.OutputValidators[0] == nil {
		t.Fatalf("expected output validators, got %+v", cfg.OutputValidators)
	}
	if cfg.ModelSettings == nil || cfg.ModelSettings.ToolChoice == nil {
		t.Fatalf("expected model settings clone, got %+v", cfg.ModelSettings)
	}
	if cfg.ModelSettings == agent.modelSettings || cfg.ModelSettings.ToolChoice == agent.modelSettings.ToolChoice {
		t.Fatal("expected model settings and tool choice to be deep-cloned")
	}
	if cfg.UsageQuota == nil || cfg.UsageQuota == agent.usageQuota {
		t.Fatal("expected usage quota clone")
	}
	if cfg.MaxRetries != 6 || cfg.EndStrategy != EndStrategyExhaustive || cfg.MaxConcurrency != 3 {
		t.Fatalf("unexpected runtime execution settings: %+v", cfg)
	}
	if cfg.DefaultToolTimeout != 2*time.Second {
		t.Fatalf("unexpected default tool timeout %v", cfg.DefaultToolTimeout)
	}
	if cfg.ToolChoice == nil || cfg.ToolChoice == agent.toolChoice || cfg.ToolChoice.Mode != "required" {
		t.Fatalf("unexpected tool choice clone: %+v", cfg.ToolChoice)
	}
	if !cfg.ToolChoiceAutoReset {
		t.Fatal("expected tool choice auto reset to be preserved")
	}
	if cfg.ToolApprovalFunc == nil {
		t.Fatal("expected tool approval func")
	}
	if len(cfg.GlobalToolResultValidators) != 1 || cfg.GlobalToolResultValidators[0] == nil {
		t.Fatalf("expected global tool validators, got %+v", cfg.GlobalToolResultValidators)
	}
	if len(cfg.RequestMiddleware) != 1 || cfg.RequestMiddleware[0] == nil {
		t.Fatalf("expected request middleware, got %+v", cfg.RequestMiddleware)
	}
	if len(cfg.StreamMiddleware) != 1 || cfg.StreamMiddleware[0] == nil {
		t.Fatalf("expected stream middleware, got %+v", cfg.StreamMiddleware)
	}
	if len(cfg.MessageInterceptors) != 1 || cfg.MessageInterceptors[0] == nil {
		t.Fatalf("expected message interceptors, got %+v", cfg.MessageInterceptors)
	}
	if len(cfg.ResponseInterceptors) != 1 || cfg.ResponseInterceptors[0] == nil {
		t.Fatalf("expected response interceptors, got %+v", cfg.ResponseInterceptors)
	}
	if len(cfg.RunConditions) != 1 || cfg.RunConditions[0] == nil {
		t.Fatalf("expected run conditions, got %+v", cfg.RunConditions)
	}
	if !cfg.TracingEnabled || len(cfg.TraceExporters) != 1 || cfg.TraceExporters[0] == nil {
		t.Fatalf("expected tracing config, got %+v", cfg.TraceExporters)
	}
	if cfg.ModelName != model.ModelName() {
		t.Fatalf("expected model name %q, got %q", model.ModelName(), cfg.ModelName)
	}
	if !cfg.HasCostTracker || cfg.CostCurrency != "EUR" {
		t.Fatalf("unexpected cost tracker metadata: has=%v currency=%q", cfg.HasCostTracker, cfg.CostCurrency)
	}
	if cfg.EventBus != bus {
		t.Fatal("expected event bus pointer to be preserved")
	}
	if !reflect.DeepEqual(cfg.AgentDeps, deps) {
		t.Fatalf("unexpected agent deps: %+v", cfg.AgentDeps)
	}
	if cfg.AutoContext == nil || cfg.AutoContext == agent.autoContext || cfg.AutoContext.MaxTokens != 200 {
		t.Fatalf("unexpected auto-context clone: %+v", cfg.AutoContext)
	}
	if cfg.KnowledgeBase != kb || !cfg.KnowledgeAutoStore {
		t.Fatalf("unexpected knowledge base config: kb=%v auto=%v", cfg.KnowledgeBase, cfg.KnowledgeAutoStore)
	}
	if got := cfg.CostPricing[model.ModelName()].InputTokenCost; got != 0.001 {
		t.Fatalf("unexpected cloned cost pricing: %+v", cfg.CostPricing)
	}

	agent.systemPrompts[0] = "changed"
	agent.dynamicSystemPrompts[0] = nil
	agent.historyProcessors[0] = nil
	agent.tools[0].Definition.Name = "changed_tool"
	agent.hooks[0] = Hook{}
	agent.inputGuardrails[0].name = "changed_input"
	agent.turnGuardrails[0].name = "changed_turn"
	agent.outputSchema.OutputTools[0].Name = "changed_output"
	agent.outputSchema.OutputObject.Name = "changed_object"
	agent.modelSettings.ToolChoice.ToolName = "changed_choice"
	agent.usageQuota.MaxRequests = 99
	agent.autoContext.MaxTokens = 999
	agent.costTracker.pricing[model.ModelName()] = ModelPricing{InputTokenCost: 9.9}

	if cfg.SystemPrompts[0] != "system prompt" {
		t.Fatalf("expected cloned system prompts to remain stable, got %+v", cfg.SystemPrompts)
	}
	if cfg.DynamicSystemPrompts[0] == nil || cfg.HistoryProcessors[0] == nil {
		t.Fatal("expected cloned function slices to remain populated")
	}
	if cfg.Tools[0].Definition.Name != "direct_tool" {
		t.Fatalf("expected cloned tools to remain stable, got %+v", cfg.Tools[0].Definition)
	}
	if cfg.Hooks[0].OnRunStart == nil {
		t.Fatal("expected cloned hooks to remain populated")
	}
	if cfg.InputGuardrails[0].Name != "input_guardrail" || cfg.TurnGuardrails[0].Name != "turn_guardrail" {
		t.Fatalf("expected cloned guardrail names to remain stable, got %+v / %+v", cfg.InputGuardrails, cfg.TurnGuardrails)
	}
	if cfg.OutputSchema.OutputTools[0].Name != "structured_output" || cfg.OutputSchema.OutputObject.Name != "structured_output" {
		t.Fatalf("expected cloned output schema to remain stable, got %+v", cfg.OutputSchema)
	}
	if cfg.ModelSettings.ToolChoice.ToolName != "direct_tool" {
		t.Fatalf("expected cloned model settings to remain stable, got %+v", cfg.ModelSettings)
	}
	if cfg.UsageQuota.MaxRequests != 5 {
		t.Fatalf("expected cloned quota to remain stable, got %+v", cfg.UsageQuota)
	}
	if cfg.AutoContext.MaxTokens != 200 {
		t.Fatalf("expected cloned auto-context to remain stable, got %+v", cfg.AutoContext)
	}
	if got := cfg.CostPricing[model.ModelName()].InputTokenCost; got != 0.001 {
		t.Fatalf("expected cloned cost pricing to remain stable, got %+v", cfg.CostPricing)
	}
}

func TestAgentRuntimeCloneHelpers_NilAndEmpty(t *testing.T) {
	if cloneOutputSchema(nil) != nil {
		t.Fatal("expected nil output schema clone")
	}
	if cloneModelSettings(nil) != nil {
		t.Fatal("expected nil model settings clone")
	}
	if cloneToolChoice(nil) != nil {
		t.Fatal("expected nil tool choice clone")
	}
	if cloneUsageQuota(nil) != nil {
		t.Fatal("expected nil usage quota clone")
	}
	if cloneModelPricingMap(nil) != nil {
		t.Fatal("expected nil pricing clone")
	}
	if cloneAutoContextConfig(nil) != nil {
		t.Fatal("expected nil auto-context clone")
	}
	if cloneInputGuardrails(nil) != nil {
		t.Fatal("expected nil input guardrails clone")
	}
	if cloneTurnGuardrails(nil) != nil {
		t.Fatal("expected nil turn guardrails clone")
	}

	agent := NewAgent[string](NewTestModel(TextResponse("ok")))
	if got := agent.costPricing(); got != nil {
		t.Fatalf("expected nil pricing with no cost tracker, got %+v", got)
	}
	if got := agent.costCurrency(); got != "" {
		t.Fatalf("expected empty currency with no cost tracker, got %q", got)
	}
}

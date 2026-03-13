package core

import "time"

// InputGuardrailRuntime captures a named input guardrail for alternative
// execution backends.
type InputGuardrailRuntime struct {
	Name string
	Func InputGuardrailFunc
}

// TurnGuardrailRuntime captures a named turn guardrail for alternative
// execution backends.
type TurnGuardrailRuntime struct {
	Name string
	Func TurnGuardrailFunc
}

// AgentRuntimeConfig captures the configuration needed by alternative
// execution backends such as ext/temporal. It includes callback hooks so
// backends can execute them behind their own side-effect boundaries.
type AgentRuntimeConfig[T any] struct {
	SystemPrompts              []string
	DynamicSystemPrompts       []SystemPromptFunc
	HistoryProcessors          []HistoryProcessor
	Tools                      []Tool
	Hooks                      []Hook
	InputGuardrails            []InputGuardrailRuntime
	TurnGuardrails             []TurnGuardrailRuntime
	AgentToolsPrepare          AgentToolsPrepareFunc
	OutputSchema               *OutputSchema
	OutputValidators           []OutputValidatorFunc[T]
	OutputRepair               RepairFunc[T]
	ModelSettings              *ModelSettings
	UsageLimits                UsageLimits
	UsageQuota                 *UsageQuota
	MaxRetries                 int
	EndStrategy                EndStrategy
	MaxConcurrency             int
	DefaultToolTimeout         time.Duration
	ToolChoice                 *ToolChoice
	ToolChoiceAutoReset        bool
	ToolApprovalFunc           ToolApprovalFunc
	GlobalToolResultValidators []ToolResultValidatorFunc
	RequestMiddleware          []RequestMiddlewareFunc
	StreamMiddleware           []AgentStreamMiddleware
	MessageInterceptors        []MessageInterceptor
	ResponseInterceptors       []ResponseInterceptor
	RunConditions              []RunCondition
	TracingEnabled             bool
	TraceExporters             []TraceExporter
	ModelName                  string
	HasCostTracker             bool
	CostPricing                map[string]ModelPricing
	CostCurrency               string
	EventBus                   *EventBus
	AgentDeps                  any
	AutoContext                *AutoContextConfig
	KnowledgeBase              KnowledgeBase
	KnowledgeAutoStore         bool
}

// RuntimeConfig returns a copy of the agent configuration needed to reproduce
// the supported execution subset outside core.Agent.Run.
func (a *Agent[T]) RuntimeConfig() AgentRuntimeConfig[T] {
	var repair RepairFunc[T]
	if a.repairFunc != nil {
		if repairFn, ok := a.repairFunc.(RepairFunc[T]); ok {
			repair = repairFn
		}
	}

	return AgentRuntimeConfig[T]{
		SystemPrompts:              append([]string(nil), a.systemPrompts...),
		DynamicSystemPrompts:       append([]SystemPromptFunc(nil), a.dynamicSystemPrompts...),
		HistoryProcessors:          append([]HistoryProcessor(nil), a.historyProcessors...),
		Tools:                      append([]Tool(nil), a.allTools()...),
		Hooks:                      append([]Hook(nil), a.hooks...),
		InputGuardrails:            cloneInputGuardrails(a.inputGuardrails),
		TurnGuardrails:             cloneTurnGuardrails(a.turnGuardrails),
		AgentToolsPrepare:          a.toolsPrepareFunc,
		OutputSchema:               cloneOutputSchema(a.ensureOutputSchema()),
		OutputValidators:           append([]OutputValidatorFunc[T](nil), a.outputValidators...),
		OutputRepair:               repair,
		ModelSettings:              cloneModelSettings(a.modelSettings),
		UsageLimits:                a.usageLimits,
		UsageQuota:                 cloneUsageQuota(a.usageQuota),
		MaxRetries:                 a.maxRetries,
		EndStrategy:                a.endStrategy,
		MaxConcurrency:             a.maxConcurrency,
		DefaultToolTimeout:         a.defaultToolTimeout,
		ToolChoice:                 cloneToolChoice(a.toolChoice),
		ToolChoiceAutoReset:        a.toolChoiceAutoReset,
		ToolApprovalFunc:           a.toolApprovalFunc,
		GlobalToolResultValidators: append([]ToolResultValidatorFunc(nil), a.globalToolResultValidators...),
		RequestMiddleware:          append([]RequestMiddlewareFunc(nil), a.middleware...),
		StreamMiddleware:           append([]AgentStreamMiddleware(nil), a.streamMiddleware...),
		MessageInterceptors:        append([]MessageInterceptor(nil), a.messageInterceptors...),
		ResponseInterceptors:       append([]ResponseInterceptor(nil), a.responseInterceptors...),
		RunConditions:              append([]RunCondition(nil), a.runConditions...),
		TracingEnabled:             a.tracingEnabled,
		TraceExporters:             append([]TraceExporter(nil), a.traceExporters...),
		ModelName:                  a.model.ModelName(),
		HasCostTracker:             a.costTracker != nil,
		CostPricing:                cloneModelPricingMap(a.costPricing()),
		CostCurrency:               a.costCurrency(),
		EventBus:                   a.eventBus,
		AgentDeps:                  a.deps,
		AutoContext:                cloneAutoContextConfig(a.autoContext),
		KnowledgeBase:              a.knowledgeBase,
		KnowledgeAutoStore:         a.kbAutoStore,
	}
}

func cloneOutputSchema(schema *OutputSchema) *OutputSchema {
	if schema == nil {
		return nil
	}

	cloned := *schema
	if len(schema.OutputTools) > 0 {
		cloned.OutputTools = append([]ToolDefinition(nil), schema.OutputTools...)
	}
	if schema.OutputObject != nil {
		outputObject := *schema.OutputObject
		cloned.OutputObject = &outputObject
	}
	return &cloned
}

func cloneModelSettings(settings *ModelSettings) *ModelSettings {
	if settings == nil {
		return nil
	}
	cloned := *settings
	cloned.ToolChoice = cloneToolChoice(settings.ToolChoice)
	return &cloned
}

func cloneToolChoice(choice *ToolChoice) *ToolChoice {
	if choice == nil {
		return nil
	}
	cloned := *choice
	return &cloned
}

func cloneUsageQuota(quota *UsageQuota) *UsageQuota {
	if quota == nil {
		return nil
	}
	cloned := *quota
	return &cloned
}

func cloneModelPricingMap(src map[string]ModelPricing) map[string]ModelPricing {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]ModelPricing, len(src))
	for modelName, pricing := range src {
		cloned[modelName] = pricing
	}
	return cloned
}

func cloneAutoContextConfig(config *AutoContextConfig) *AutoContextConfig {
	if config == nil {
		return nil
	}
	cloned := *config
	return &cloned
}

func (a *Agent[T]) costPricing() map[string]ModelPricing {
	if a.costTracker == nil {
		return nil
	}
	return a.costTracker.Pricing()
}

func (a *Agent[T]) costCurrency() string {
	if a.costTracker == nil {
		return ""
	}
	return a.costTracker.currency
}

func cloneInputGuardrails(src []namedInputGuardrail) []InputGuardrailRuntime {
	if len(src) == 0 {
		return nil
	}
	cloned := make([]InputGuardrailRuntime, 0, len(src))
	for _, guardrail := range src {
		cloned = append(cloned, InputGuardrailRuntime{
			Name: guardrail.name,
			Func: guardrail.fn,
		})
	}
	return cloned
}

func cloneTurnGuardrails(src []namedTurnGuardrail) []TurnGuardrailRuntime {
	if len(src) == 0 {
		return nil
	}
	cloned := make([]TurnGuardrailRuntime, 0, len(src))
	for _, guardrail := range src {
		cloned = append(cloned, TurnGuardrailRuntime{
			Name: guardrail.name,
			Func: guardrail.fn,
		})
	}
	return cloned
}

package core

// Clone creates a copy of the agent with additional options applied.
// The original agent is not modified.
func (a *Agent[T]) Clone(opts ...AgentOption[T]) *Agent[T] {
	clone := &Agent[T]{
		model:       a.model,
		maxRetries:  a.maxRetries,
		endStrategy: a.endStrategy,
		usageLimits: a.usageLimits,
	}

	// Copy slices.
	if len(a.systemPrompts) > 0 {
		clone.systemPrompts = make([]string, len(a.systemPrompts))
		copy(clone.systemPrompts, a.systemPrompts)
	}
	if len(a.dynamicSystemPrompts) > 0 {
		clone.dynamicSystemPrompts = make([]SystemPromptFunc, len(a.dynamicSystemPrompts))
		copy(clone.dynamicSystemPrompts, a.dynamicSystemPrompts)
	}
	if len(a.historyProcessors) > 0 {
		clone.historyProcessors = make([]HistoryProcessor, len(a.historyProcessors))
		copy(clone.historyProcessors, a.historyProcessors)
	}
	if len(a.tools) > 0 {
		clone.tools = make([]Tool, len(a.tools))
		copy(clone.tools, a.tools)
	}
	if len(a.toolsets) > 0 {
		clone.toolsets = make([]*Toolset, len(a.toolsets))
		copy(clone.toolsets, a.toolsets)
	}
	if len(a.outputValidators) > 0 {
		clone.outputValidators = make([]OutputValidatorFunc[T], len(a.outputValidators))
		copy(clone.outputValidators, a.outputValidators)
	}
	if len(a.outputOpts) > 0 {
		clone.outputOpts = make([]OutputOption, len(a.outputOpts))
		copy(clone.outputOpts, a.outputOpts)
	}
	if len(a.hooks) > 0 {
		clone.hooks = make([]Hook, len(a.hooks))
		copy(clone.hooks, a.hooks)
	}
	if len(a.inputGuardrails) > 0 {
		clone.inputGuardrails = make([]namedInputGuardrail, len(a.inputGuardrails))
		copy(clone.inputGuardrails, a.inputGuardrails)
	}
	if len(a.turnGuardrails) > 0 {
		clone.turnGuardrails = make([]namedTurnGuardrail, len(a.turnGuardrails))
		copy(clone.turnGuardrails, a.turnGuardrails)
	}

	// Copy pointer/interface fields.
	if a.modelSettings != nil {
		s := *a.modelSettings
		clone.modelSettings = &s
	}
	clone.outputSchema = a.outputSchema
	clone.toolApprovalFunc = a.toolApprovalFunc
	clone.maxConcurrency = a.maxConcurrency
	clone.knowledgeBase = a.knowledgeBase
	clone.kbAutoStore = a.kbAutoStore
	clone.toolsPrepareFunc = a.toolsPrepareFunc
	clone.repairFunc = a.repairFunc
	clone.tracingEnabled = a.tracingEnabled
	clone.defaultToolTimeout = a.defaultToolTimeout
	clone.eventBus = a.eventBus
	clone.deps = a.deps
	clone.usageQuota = a.usageQuota
	clone.costTracker = a.costTracker
	clone.autoContext = a.autoContext
	clone.toolChoice = a.toolChoice
	clone.toolChoiceAutoReset = a.toolChoiceAutoReset
	if len(a.globalToolResultValidators) > 0 {
		clone.globalToolResultValidators = make([]ToolResultValidatorFunc, len(a.globalToolResultValidators))
		copy(clone.globalToolResultValidators, a.globalToolResultValidators)
	}
	if len(a.runConditions) > 0 {
		clone.runConditions = make([]RunCondition, len(a.runConditions))
		copy(clone.runConditions, a.runConditions)
	}
	if len(a.traceExporters) > 0 {
		clone.traceExporters = make([]TraceExporter, len(a.traceExporters))
		copy(clone.traceExporters, a.traceExporters)
	}
	if len(a.messageInterceptors) > 0 {
		clone.messageInterceptors = make([]MessageInterceptor, len(a.messageInterceptors))
		copy(clone.messageInterceptors, a.messageInterceptors)
	}
	if len(a.responseInterceptors) > 0 {
		clone.responseInterceptors = make([]ResponseInterceptor, len(a.responseInterceptors))
		copy(clone.responseInterceptors, a.responseInterceptors)
	}
	if len(a.middleware) > 0 {
		clone.middleware = make([]AgentMiddleware, len(a.middleware))
		copy(clone.middleware, a.middleware)
	}

	// Apply new options.
	for _, opt := range opts {
		opt(clone)
	}

	return clone
}


package core

// AgentExecutionFeatures summarizes the optional execution features configured
// on an agent. Extensions can use this to conservatively reject configurations
// they cannot faithfully support.
type AgentExecutionFeatures struct {
	DynamicSystemPrompts       int
	HistoryProcessors          int
	Toolsets                   int
	HasAgentToolsPrepare       bool
	ToolsWithPrepareFunc       int
	ToolsWithResultValidator   int
	ToolsRequiringApproval     int
	HasToolApprovalFunc        bool
	Hooks                      int
	InputGuardrails            int
	TurnGuardrails             int
	OutputValidators           int
	HasOutputRepair            bool
	GlobalToolResultValidators int
	RunConditions              int
	TraceExporters             int
	HasEventBus                bool
	HasAgentDeps               bool
	MessageInterceptors        int
	ResponseInterceptors       int
	HasKnowledgeBase           bool
	HasKnowledgeAutoStore      bool
	HasCostTracker             bool
	HasAutoContext             bool
	HasUsageQuota              bool
	RequestMiddleware          int
	StreamMiddleware           int
}

// ExecutionFeatures returns a summary of optional execution features configured
// on the agent and its toolsets.
func (a *Agent[T]) ExecutionFeatures() AgentExecutionFeatures {
	features := AgentExecutionFeatures{
		DynamicSystemPrompts:       len(a.dynamicSystemPrompts),
		HistoryProcessors:          len(a.historyProcessors),
		Toolsets:                   len(a.toolsets),
		HasAgentToolsPrepare:       a.toolsPrepareFunc != nil,
		HasToolApprovalFunc:        a.toolApprovalFunc != nil,
		Hooks:                      len(a.hooks),
		InputGuardrails:            len(a.inputGuardrails),
		TurnGuardrails:             len(a.turnGuardrails),
		OutputValidators:           len(a.outputValidators),
		HasOutputRepair:            a.repairFunc != nil,
		GlobalToolResultValidators: len(a.globalToolResultValidators),
		RunConditions:              len(a.runConditions),
		TraceExporters:             len(a.traceExporters),
		HasEventBus:                a.eventBus != nil,
		HasAgentDeps:               a.deps != nil,
		MessageInterceptors:        len(a.messageInterceptors),
		ResponseInterceptors:       len(a.responseInterceptors),
		HasKnowledgeBase:           a.knowledgeBase != nil,
		HasKnowledgeAutoStore:      a.kbAutoStore,
		HasCostTracker:             a.costTracker != nil,
		HasAutoContext:             a.autoContext != nil,
		HasUsageQuota:              a.usageQuota != nil,
		RequestMiddleware:          len(a.middleware),
		StreamMiddleware:           len(a.streamMiddleware),
	}

	for _, tool := range a.tools {
		accumulateToolExecutionFeatures(&features, tool)
	}
	for _, ts := range a.toolsets {
		if ts == nil {
			continue
		}
		for _, tool := range ts.Tools {
			accumulateToolExecutionFeatures(&features, tool)
		}
	}

	return features
}

func accumulateToolExecutionFeatures(features *AgentExecutionFeatures, tool Tool) {
	if tool.PrepareFunc != nil {
		features.ToolsWithPrepareFunc++
	}
	if tool.ResultValidator != nil {
		features.ToolsWithResultValidator++
	}
	if tool.RequiresApproval {
		features.ToolsRequiringApproval++
	}
}

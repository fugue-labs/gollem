package core

// ToolChoice controls how the model selects tools.
type ToolChoice struct {
	// Mode is "auto" (default), "required" (must use any tool), or "none" (no tools).
	Mode string `json:"mode,omitempty"`
	// ToolName forces use of a specific tool (when Mode is empty or "auto").
	ToolName string `json:"tool_name,omitempty"`
}

// ToolChoiceAuto lets the model decide whether to use tools.
func ToolChoiceAuto() *ToolChoice {
	return &ToolChoice{Mode: "auto"}
}

// ToolChoiceRequired forces the model to use a tool.
func ToolChoiceRequired() *ToolChoice {
	return &ToolChoice{Mode: "required"}
}

// ToolChoiceNone prevents tool use.
func ToolChoiceNone() *ToolChoice {
	return &ToolChoice{Mode: "none"}
}

// ToolChoiceForce forces use of a specific tool by name.
func ToolChoiceForce(toolName string) *ToolChoice {
	return &ToolChoice{ToolName: toolName}
}

// WithToolChoice sets the initial tool choice for model requests.
func WithToolChoice[T any](choice *ToolChoice) AgentOption[T] {
	return func(a *Agent[T]) {
		a.toolChoice = choice
	}
}

// WithToolChoiceAutoReset resets tool choice to "auto" after the first tool call.
// This prevents infinite loops when tool_choice is "required".
func WithToolChoiceAutoReset[T any]() AgentOption[T] {
	return func(a *Agent[T]) {
		a.toolChoiceAutoReset = true
	}
}

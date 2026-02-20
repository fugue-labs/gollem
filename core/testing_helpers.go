package core

// Override creates a test-scoped override of the agent.
// The original agent is not modified. The returned override has its own
// model and options but shares the same configuration.
func (a *Agent[T]) Override(model Model, opts ...AgentOption[T]) *Agent[T] {
	clone := a.Clone()
	clone.model = model
	// Reset output schema so it's rebuilt for the new model.
	clone.outputSchema = nil
	for _, opt := range opts {
		opt(clone)
	}
	return clone
}

// WithTestModel is a convenience that creates an agent override with a TestModel
// pre-configured with the given responses.
func (a *Agent[T]) WithTestModel(responses ...*ModelResponse) (*Agent[T], *TestModel) {
	tm := NewTestModel(responses...)
	return a.Override(tm), tm
}

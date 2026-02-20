package core

import "fmt"

// GetDeps extracts typed dependencies from RunContext.
// Panics if deps are nil or the wrong type.
func GetDeps[D any](rc *RunContext) D {
	if rc.Deps == nil {
		panic("gollem.GetDeps: RunContext.Deps is nil")
	}
	d, ok := rc.Deps.(D)
	if !ok {
		panic(fmt.Sprintf("gollem.GetDeps: RunContext.Deps type mismatch: got %T", rc.Deps))
	}
	return d
}

// TryGetDeps extracts typed dependencies, returning ok=false if not available or wrong type.
func TryGetDeps[D any](rc *RunContext) (D, bool) {
	if rc == nil || rc.Deps == nil {
		var zero D
		return zero, false
	}
	d, ok := rc.Deps.(D)
	return d, ok
}

// WithDeps sets the dependency value for the agent run.
// The dependency is accessible via GetDeps[D](rc) in tools.
func WithDeps[T any](deps any) AgentOption[T] {
	return func(a *Agent[T]) {
		a.deps = deps
	}
}

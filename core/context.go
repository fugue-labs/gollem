package core

import "context"

// Context keys for propagating agent identity through tool execution.
// These allow tracing systems to establish parent-child relationships
// between agents (e.g., a parent agent's tool span as the parent of
// a subagent's root span).

type runIDContextKey struct{}
type toolCallIDContextKey struct{}

// ContextWithRunID returns a context carrying the agent's RunID.
// The agent framework injects this automatically at the start of Run.
func ContextWithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDContextKey{}, runID)
}

// RunIDFromContext extracts the current agent's RunID from the context.
// Returns "" if no RunID is set.
func RunIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(runIDContextKey{}).(string); ok {
		return v
	}
	return ""
}

// ContextWithToolCallID returns a context carrying the active tool call ID.
// The agent framework injects this before each tool handler execution.
func ContextWithToolCallID(ctx context.Context, toolCallID string) context.Context {
	return context.WithValue(ctx, toolCallIDContextKey{}, toolCallID)
}

// ToolCallIDFromContext extracts the active tool call ID from the context.
// Returns "" if no tool call ID is set.
func ToolCallIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(toolCallIDContextKey{}).(string); ok {
		return v
	}
	return ""
}

// CompactionCallback is a function that middleware can call to notify the
// agent's hook system about context compaction events.
type CompactionCallback func(stats ContextCompactionStats)

type compactionCallbackKey struct{}

// ContextWithCompactionCallback returns a context carrying a compaction callback.
// The agent framework injects this so middleware (e.g., ContextOverflowMiddleware)
// can report emergency compression events to hooks.
func ContextWithCompactionCallback(ctx context.Context, cb CompactionCallback) context.Context {
	return context.WithValue(ctx, compactionCallbackKey{}, cb)
}

// CompactionCallbackFromContext extracts the compaction callback from context.
// Returns nil if no callback is set.
func CompactionCallbackFromContext(ctx context.Context) CompactionCallback {
	if cb, ok := ctx.Value(compactionCallbackKey{}).(CompactionCallback); ok {
		return cb
	}
	return nil
}

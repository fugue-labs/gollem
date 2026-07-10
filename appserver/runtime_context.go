package appserver

import "context"

type runtimeTurnContextKey struct{}
type runtimeApprovalItemIDContextKey struct{}

type runtimeTurnContext struct {
	ThreadID string
	TurnID   string
}

func withRuntimeTurnContext(ctx context.Context, threadID, turnID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runtimeTurnContextKey{}, runtimeTurnContext{
		ThreadID: threadID,
		TurnID:   turnID,
	})
}

func runtimeTurnContextFrom(ctx context.Context) runtimeTurnContext {
	if ctx == nil {
		return runtimeTurnContext{}
	}
	value, _ := ctx.Value(runtimeTurnContextKey{}).(runtimeTurnContext)
	return value
}

func withRuntimeApprovalItemID(ctx context.Context, itemID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if itemID == "" {
		return ctx
	}
	return context.WithValue(ctx, runtimeApprovalItemIDContextKey{}, itemID)
}

func runtimeApprovalItemIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(runtimeApprovalItemIDContextKey{}).(string)
	return value
}

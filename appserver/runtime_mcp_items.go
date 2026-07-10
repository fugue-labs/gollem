package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
)

const (
	runtimeMCPToolCallItemKind = "mcpToolCall"
	runtimeMCPStatusInProgress = "inProgress"
	runtimeMCPStatusCompleted  = "completed"
	runtimeMCPStatusFailed     = "failed"
)

type runtimeMCPToolCallPayload = protocol.MCPToolCallItem
type runtimeMCPToolCallErrorPayload = protocol.MCPToolCallError
type runtimeMCPItemStartedNotificationParams = protocol.MCPToolCallItemStartedNotificationParams
type runtimeMCPItemCompletedNotificationParams = protocol.MCPToolCallItemCompletedNotificationParams
type runtimeMCPToolProgressNotificationParams = protocol.MCPToolCallProgressNotificationParams

type runtimeMCPItemState struct {
	item      *store.Item
	payload   runtimeMCPToolCallPayload
	startedAt time.Time
}

type runtimeMCPItemTracker struct {
	mu        sync.Mutex
	store     store.Store
	notifier  runtimeNotifier
	turn      *store.Turn
	toolItems *runtimeToolItemTracker
	items     map[string]*runtimeMCPItemState
	err       error
}

func newRuntimeMCPItemTracker(st store.Store, notifier runtimeNotifier, turn *store.Turn, toolItems *runtimeToolItemTracker) *runtimeMCPItemTracker {
	return &runtimeMCPItemTracker{
		store:     st,
		notifier:  notifier,
		turn:      turn,
		toolItems: toolItems,
		items:     make(map[string]*runtimeMCPItemState),
	}
}

func (t *runtimeMCPItemTracker) toolStarted(event runtimeMCPToolStartedEvent) {
	if t == nil || t.store == nil || t.turn == nil {
		return
	}
	startedAt := event.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	argumentsRaw, _ := json.Marshal(event.Arguments)
	payload := runtimeMCPToolCallPayload{
		Type:      runtimeMCPToolCallItemKind,
		Server:    event.Server,
		Tool:      event.MCPTool,
		Status:    runtimeMCPStatusInProgress,
		Arguments: runtimeToolArguments(string(argumentsRaw)),
	}
	parentItemID := ""
	if t.toolItems != nil {
		parentItemID = t.toolItems.itemID(event.RunID, event.ToolCallID, event.ToolName)
	}
	key := runtimeToolItemKey(event.RunID, event.ToolCallID, event.ToolName)

	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.items[key]; exists {
		return
	}
	item, err := t.store.AppendItem(context.Background(), store.AppendItemRequest{
		ThreadID:     t.turn.ThreadID,
		TurnID:       t.turn.ID,
		ParentItemID: parentItemID,
		Kind:         runtimeMCPToolCallItemKind,
		Status:       runtimeMCPStatusInProgress,
		Payload:      mustRuntimeJSON(payload),
	})
	if err != nil {
		t.recordErrorLocked("append", err)
		return
	}
	payload.ID = item.ID
	item, err = t.store.UpdateItem(context.Background(), store.UpdateItemRequest{
		ID:      item.ID,
		Status:  runtimeMCPStatusInProgress,
		Payload: mustRuntimeJSON(payload),
	})
	if err != nil {
		t.recordErrorLocked("set id", err)
		return
	}
	t.items[key] = &runtimeMCPItemState{item: item, payload: payload, startedAt: startedAt}
	if event.ItemID != nil {
		*event.ItemID = item.ID
	}
	if t.notifier != nil {
		t.notifier.PublishNotification("item/started", runtimeMCPItemStartedNotificationParams{
			Item:        payload,
			ThreadID:    t.turn.ThreadID,
			TurnID:      t.turn.ID,
			StartedAtMS: startedAt.UnixMilli(),
		})
	}
}

func (t *runtimeMCPItemTracker) toolProgress(event runtimeMCPToolProgressEvent) {
	if t == nil || t.notifier == nil {
		return
	}
	key := runtimeToolItemKey(event.RunID, event.ToolCallID, event.ToolName)
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.items[key]
	if state == nil || state.item == nil {
		return
	}
	message, _ := boundedRuntimeMCPText(event.Message, runtimeMCPMetadataMaxBytes)
	if message == "" {
		return
	}
	t.notifier.PublishNotification("item/mcpToolCall/progress", runtimeMCPToolProgressNotificationParams{
		ThreadID: t.turn.ThreadID,
		TurnID:   t.turn.ID,
		ItemID:   state.item.ID,
		Message:  message,
	})
}

func (t *runtimeMCPItemTracker) toolCompleted(event runtimeMCPToolCompletedEvent) {
	if t == nil {
		return
	}
	key := runtimeToolItemKey(event.RunID, event.ToolCallID, event.ToolName)
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.items[key]
	if state == nil || state.item == nil {
		return
	}
	delete(t.items, key)
	completedAt := event.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	duration := completedAt.Sub(state.startedAt).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	state.payload.DurationMS = runtimeInt64Pointer(duration)
	state.payload.Status = runtimeMCPStatusCompleted
	if event.Error != "" {
		message, _ := boundedRuntimeMCPText(event.Error, runtimeMCPMetadataMaxBytes)
		state.payload.Status = runtimeMCPStatusFailed
		state.payload.Error = &runtimeMCPToolCallErrorPayload{Message: message}
		state.payload.Result = nil
	} else if event.Result != nil {
		state.payload.Result = event.Result.ItemResult
	}
	item, err := t.store.UpdateItem(context.Background(), store.UpdateItemRequest{
		ID:      state.item.ID,
		Status:  state.payload.Status,
		Payload: mustRuntimeJSON(state.payload),
	})
	if err != nil {
		t.recordErrorLocked("complete", err)
		return
	}
	state.item = item
	if t.notifier != nil {
		t.notifier.PublishNotification("item/completed", runtimeMCPItemCompletedNotificationParams{
			Item:          state.payload,
			ThreadID:      t.turn.ThreadID,
			TurnID:        t.turn.ID,
			CompletedAtMS: completedAt.UnixMilli(),
		})
	}
}

func (t *runtimeMCPItemTracker) Err() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

func (t *runtimeMCPItemTracker) recordErrorLocked(operation string, err error) {
	if err == nil || t.err != nil {
		return
	}
	t.err = fmt.Errorf("persist runtime MCP item (%s): %w", operation, err)
	publishRuntimeError(t.notifier, t.turn, t.err.Error())
}

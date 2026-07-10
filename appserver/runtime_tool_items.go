package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

const (
	runtimeDynamicToolCallItemKind = "dynamicToolCall"
	runtimeToolStatusInProgress    = "inProgress"
	runtimeToolStatusCompleted     = "completed"
	runtimeToolStatusFailed        = "failed"
	runtimeToolPayloadMaxBytes     = 64 * 1024
)

type runtimeDynamicToolCallPayload = protocol.DynamicToolCallItem
type runtimeDynamicToolCallContentItem = protocol.DynamicToolCallContentItem
type runtimeToolPayloadSummary = protocol.ToolPayloadSummary
type runtimeToolItemStartedNotificationParams = protocol.DynamicToolCallItemStartedNotificationParams
type runtimeToolItemCompletedNotificationParams = protocol.DynamicToolCallItemCompletedNotificationParams

type runtimeToolItemState struct {
	item    *store.Item
	payload runtimeDynamicToolCallPayload
}

type runtimeToolItemIDRequestEvent struct {
	RunID      string
	ToolCallID string
	ToolName   string
	ItemID     *string
}

type runtimeToolItemTracker struct {
	mu         sync.Mutex
	store      store.Store
	notifier   runtimeNotifier
	turn       *store.Turn
	namespaces map[string]string
	items      map[string]runtimeToolItemState
	err        error
}

func newRuntimeToolItemTracker(st store.Store, notifier runtimeNotifier, turn *store.Turn, tools []core.Tool) *runtimeToolItemTracker {
	namespaces := make(map[string]string)
	for _, tool := range tools {
		if tool.Definition.Name == "" || tool.Definition.Namespace == "" {
			continue
		}
		namespaces[tool.Definition.Name] = tool.Definition.Namespace
	}
	return &runtimeToolItemTracker{
		store:      st,
		notifier:   notifier,
		turn:       turn,
		namespaces: namespaces,
		items:      make(map[string]runtimeToolItemState),
	}
}

func (t *runtimeToolItemTracker) toolCalled(event core.ToolCalledEvent) {
	if t == nil || t.store == nil || t.turn == nil || event.ToolName == "" {
		return
	}
	key := runtimeToolItemKey(event.RunID, event.ToolCallID, event.ToolName)
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.items[key]; exists {
		return
	}

	payload := runtimeDynamicToolCallPayload{
		Type:      runtimeDynamicToolCallItemKind,
		Namespace: runtimeToolNamespace(t.namespaces[event.ToolName]),
		Tool:      event.ToolName,
		Arguments: runtimeToolArguments(event.ArgsJSON),
		Status:    runtimeToolStatusInProgress,
	}
	item, err := t.store.AppendItem(context.Background(), store.AppendItemRequest{
		ThreadID: t.turn.ThreadID,
		TurnID:   t.turn.ID,
		Kind:     runtimeDynamicToolCallItemKind,
		Status:   runtimeToolStatusInProgress,
		Payload:  mustRuntimeJSON(payload),
	})
	if err != nil {
		t.recordErrorLocked("append", err)
		return
	}
	payload.ID = item.ID
	item, err = t.store.UpdateItem(context.Background(), store.UpdateItemRequest{
		ID:      item.ID,
		Status:  runtimeToolStatusInProgress,
		Payload: mustRuntimeJSON(payload),
	})
	if err != nil {
		t.recordErrorLocked("set id", err)
		return
	}
	t.items[key] = runtimeToolItemState{item: item, payload: payload}
	if t.notifier != nil {
		startedAt := event.CalledAt
		if startedAt.IsZero() {
			startedAt = item.CreatedAt
		}
		t.notifier.PublishNotification("item/started", runtimeToolItemStartedNotificationParams{
			Item:        payload,
			ThreadID:    t.turn.ThreadID,
			TurnID:      t.turn.ID,
			StartedAtMS: startedAt.UnixMilli(),
		})
	}
}

func (t *runtimeToolItemTracker) toolCompleted(event core.ToolCompletedEvent) {
	t.complete(
		runtimeToolItemKey(event.RunID, event.ToolCallID, event.ToolName),
		runtimeToolStatusCompleted,
		event.Result,
		true,
		event.DurationMs,
		event.CompletedAt,
	)
}

func (t *runtimeToolItemTracker) toolFailed(event core.ToolFailedEvent) {
	t.complete(
		runtimeToolItemKey(event.RunID, event.ToolCallID, event.ToolName),
		runtimeToolStatusFailed,
		event.Error,
		false,
		event.DurationMs,
		event.FailedAt,
	)
}

func (t *runtimeToolItemTracker) complete(key, status, output string, success bool, durationMS int64, completedAt time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state, ok := t.items[key]
	if !ok || state.item == nil {
		return
	}
	delete(t.items, key)
	state.payload.Status = status
	state.payload.ContentItems = []runtimeDynamicToolCallContentItem{{Type: "inputText", Text: boundedRuntimeToolOutput(output)}}
	state.payload.Success = runtimeBoolPointer(success)
	state.payload.DurationMS = runtimeInt64Pointer(durationMS)
	item, err := t.store.UpdateItem(context.Background(), store.UpdateItemRequest{
		ID:      state.item.ID,
		Status:  status,
		Payload: mustRuntimeJSON(state.payload),
	})
	if err != nil {
		t.recordErrorLocked("complete", err)
		return
	}
	if t.notifier != nil && t.turn != nil {
		if completedAt.IsZero() {
			completedAt = item.UpdatedAt
		}
		t.notifier.PublishNotification("item/completed", runtimeToolItemCompletedNotificationParams{
			Item:          state.payload,
			ThreadID:      t.turn.ThreadID,
			TurnID:        t.turn.ID,
			CompletedAtMS: completedAt.UnixMilli(),
		})
	}
}

func (t *runtimeToolItemTracker) Err() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

func (t *runtimeToolItemTracker) itemID(runID, toolCallID, toolName string) string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state, ok := t.items[runtimeToolItemKey(runID, toolCallID, toolName)]
	if !ok || state.item == nil {
		return ""
	}
	return state.item.ID
}

func (t *runtimeToolItemTracker) resolveItemID(event runtimeToolItemIDRequestEvent) {
	if event.ItemID == nil {
		return
	}
	*event.ItemID = t.itemID(event.RunID, event.ToolCallID, event.ToolName)
}

func runtimeDurableToolItemID(ctx context.Context, rc *core.RunContext) string {
	if rc == nil || rc.EventBus == nil {
		return ""
	}
	itemID := ""
	core.Publish(rc.EventBus, runtimeToolItemIDRequestEvent{
		RunID:      runtimeRunID(rc),
		ToolCallID: runtimeToolCallID(ctx, rc),
		ToolName:   runtimeToolName(rc, ""),
		ItemID:     &itemID,
	})
	return itemID
}

func (t *runtimeToolItemTracker) recordErrorLocked(operation string, err error) {
	if err == nil || t.err != nil {
		return
	}
	t.err = fmt.Errorf("persist runtime tool item (%s): %w", operation, err)
	publishRuntimeError(t.notifier, t.turn, t.err.Error())
}

func runtimeToolItemKey(runID, toolCallID, toolName string) string {
	if toolCallID == "" {
		toolCallID = toolName
	}
	return runID + "\x00" + toolCallID
}

func runtimeToolNamespace(namespace string) *string {
	if namespace == "" {
		return nil
	}
	value := namespace
	return &value
}

func runtimeToolArguments(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}
	}
	if len(raw) > runtimeToolPayloadMaxBytes {
		return runtimeToolPayloadSummary{
			Omitted: true,
			Reason:  "tool arguments exceed persisted payload limit",
			Bytes:   len(raw),
			SHA256:  runtimeSHA256([]byte(raw)),
		}
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	return value
}

func boundedRuntimeToolOutput(output string) string {
	output = strings.ToValidUTF8(output, "\uFFFD")
	if len(output) <= runtimeToolPayloadMaxBytes {
		return output
	}
	marker := "\n... tool output truncated ...\n"
	remaining := runtimeToolPayloadMaxBytes - len(marker)
	headBytes := remaining / 2
	tailBytes := remaining - headBytes
	head := boundedRuntimeToolOutputPrefix(output, headBytes)
	tail := boundedRuntimeToolOutputSuffix(output, tailBytes)
	return head + marker + tail
}

func boundedRuntimeToolOutputPrefix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func boundedRuntimeToolOutputSuffix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	start := len(value) - limit
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}

func runtimeBoolPointer(value bool) *bool {
	return &value
}

func runtimeInt64Pointer(value int64) *int64 {
	return &value
}

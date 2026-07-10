package appserver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	toolprocess "github.com/fugue-labs/gollem/appserver/tools/process"
)

const runtimeCommandOutputTruncatedMarker = "\n[output truncated]\n"

type runtimeCommandItemStartedNotificationParams = protocol.CommandExecutionItemStartedNotificationParams
type runtimeCommandItemCompletedNotificationParams = protocol.CommandExecutionItemCompletedNotificationParams

type runtimeCommandItemState struct {
	item         *store.Item
	payload      threadShellCommandPayload
	output       string
	outputCapped bool
}

type runtimeCommandItemTracker struct {
	mu        sync.Mutex
	store     store.Store
	notifier  runtimeNotifier
	turn      *store.Turn
	toolItems *runtimeToolItemTracker
	items     map[string]*runtimeCommandItemState
	err       error
}

func newRuntimeCommandItemTracker(st store.Store, notifier runtimeNotifier, turn *store.Turn, toolItems *runtimeToolItemTracker) *runtimeCommandItemTracker {
	return &runtimeCommandItemTracker{
		store:     st,
		notifier:  notifier,
		turn:      turn,
		toolItems: toolItems,
		items:     make(map[string]*runtimeCommandItemState),
	}
}

func (t *runtimeCommandItemTracker) commandStarted(event runtimeCommandStartedEvent) {
	if t == nil || t.store == nil || t.turn == nil {
		return
	}
	startedAt := event.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	payload := newCommandExecutionPayload(
		event.Command,
		event.WorkDir,
		"",
		commandExecutionSourceAgent,
		commandExecutionStatusInProgress,
		"",
		nil,
		startedAt,
		nil,
	)
	parentItemID := ""
	if t.toolItems != nil {
		parentItemID = t.toolItems.itemID(event.RunID, event.ToolCallID, event.ToolName)
	}
	key := runtimeToolItemKey(event.RunID, event.ToolCallID, event.ToolName)

	t.mu.Lock()
	defer t.mu.Unlock()
	item, err := t.store.AppendItem(context.Background(), store.AppendItemRequest{
		ThreadID:     t.turn.ThreadID,
		TurnID:       t.turn.ID,
		ParentItemID: parentItemID,
		Kind:         threadShellCommandItemKind,
		Status:       commandExecutionStatusInProgress,
		Payload:      mustRuntimeJSON(payload),
	})
	if err != nil {
		t.recordErrorLocked("append", err)
		return
	}
	payload.ID = item.ID
	item, err = t.store.UpdateItem(context.Background(), store.UpdateItemRequest{
		ID:      item.ID,
		Status:  commandExecutionStatusInProgress,
		Payload: mustRuntimeJSON(payload),
	})
	if err != nil {
		t.recordErrorLocked("set id", err)
		return
	}
	t.items[key] = &runtimeCommandItemState{item: item, payload: payload}
	if event.ItemID != nil {
		*event.ItemID = item.ID
	}
	if t.notifier != nil {
		t.notifier.PublishNotification("item/started", runtimeCommandItemStartedNotificationParams{
			Item:        payload,
			ThreadID:    t.turn.ThreadID,
			TurnID:      t.turn.ID,
			StartedAtMS: startedAt.UnixMilli(),
		})
	}
}

func (t *runtimeCommandItemTracker) commandOutput(event runtimeCommandOutputEvent) {
	if t == nil || len(event.Data) == 0 {
		return
	}
	key := runtimeToolItemKey(event.RunID, event.ToolCallID, event.ToolName)
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.items[key]
	if state == nil || state.item == nil || state.outputCapped {
		return
	}
	if event.ProcessID != "" {
		state.payload.ProcessID = runtimeStringPointer(event.ProcessID)
	}
	delta, capped := appendRuntimeCommandOutput(state.output, event.Data)
	if delta == "" {
		return
	}
	state.output += delta
	state.outputCapped = capped
	state.payload.AggregatedOutput = runtimeStringPointer(state.output)
	item, err := t.store.UpdateItem(context.Background(), store.UpdateItemRequest{
		ID:      state.item.ID,
		Status:  commandExecutionStatusInProgress,
		Payload: mustRuntimeJSON(state.payload),
	})
	if err != nil {
		t.recordErrorLocked("output", err)
		return
	}
	state.item = item
	if t.notifier != nil {
		t.notifier.PublishNotification("item/commandExecution/outputDelta", commandExecutionOutputDeltaNotificationParams{
			ThreadID: t.turn.ThreadID,
			TurnID:   t.turn.ID,
			ItemID:   state.item.ID,
			Delta:    delta,
		})
	}
}

func (t *runtimeCommandItemTracker) commandCompleted(event runtimeCommandCompletedEvent) {
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
	status := commandExecutionStatusCompleted
	if event.Declined {
		status = commandExecutionStatusDeclined
	} else if event.Error != "" || event.Snapshot == nil || event.Snapshot.Status != toolprocess.StatusCompleted {
		status = commandExecutionStatusFailed
	}
	var exitCode *int
	processID := ""
	workDir := state.payload.CWD
	if event.Snapshot != nil {
		processID = event.Snapshot.ID
		workDir = event.Snapshot.WorkDir
		code := event.Snapshot.ExitCode
		exitCode = &code
		if !event.Snapshot.EndedAt.IsZero() {
			completedAt = event.Snapshot.EndedAt
		}
		if state.output == "" && (len(event.Snapshot.Stdout) > 0 || len(event.Snapshot.Stderr) > 0) {
			delta, capped := appendRuntimeCommandOutput("", append(append([]byte(nil), event.Snapshot.Stdout...), event.Snapshot.Stderr...))
			state.output = delta
			state.outputCapped = capped
		}
	}
	state.payload = newCommandExecutionPayload(
		state.payload.Command,
		workDir,
		processID,
		commandExecutionSourceAgent,
		status,
		state.output,
		exitCode,
		state.payload.StartedAt,
		&completedAt,
	)
	state.payload.ID = state.item.ID
	item, err := t.store.UpdateItem(context.Background(), store.UpdateItemRequest{
		ID:      state.item.ID,
		Status:  status,
		Payload: mustRuntimeJSON(state.payload),
	})
	if err != nil {
		t.recordErrorLocked("complete", err)
		return
	}
	state.item = item
	if t.notifier != nil {
		t.notifier.PublishNotification("item/completed", runtimeCommandItemCompletedNotificationParams{
			Item:          state.payload,
			ThreadID:      t.turn.ThreadID,
			TurnID:        t.turn.ID,
			CompletedAtMS: completedAt.UnixMilli(),
		})
	}
}

func appendRuntimeCommandOutput(current string, data []byte) (string, bool) {
	valid := strings.ToValidUTF8(string(data), "\uFFFD")
	dataLimit := runtimeProcessOutputMaxBytes - len(runtimeCommandOutputTruncatedMarker)
	remaining := dataLimit - len(current)
	if remaining <= 0 {
		return runtimeCommandOutputTruncatedMarker, true
	}
	if len(valid) <= remaining {
		return valid, false
	}
	return validRuntimeUTF8Prefix(valid, remaining) + runtimeCommandOutputTruncatedMarker, true
}

func (t *runtimeCommandItemTracker) Err() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

func (t *runtimeCommandItemTracker) recordErrorLocked(operation string, err error) {
	if err == nil || t.err != nil {
		return
	}
	t.err = fmt.Errorf("persist runtime command item (%s): %w", operation, err)
	publishRuntimeError(t.notifier, t.turn, t.err.Error())
}

func runtimeStringPointer(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

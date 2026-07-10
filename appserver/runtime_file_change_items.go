package appserver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

const (
	runtimeFileChangeItemKind         = "fileChange"
	runtimeFileChangeStatusInProgress = "inProgress"
	runtimeFileChangeStatusCompleted  = "completed"

	runtimePatchChangeAdd    = "add"
	runtimePatchChangeDelete = "delete"
	runtimePatchChangeUpdate = "update"
)

type runtimeFileChangePayload = protocol.FileChangeItem
type runtimeFileUpdateChange = protocol.FileUpdateChange
type runtimePatchChangeKind = protocol.PatchChangeKind
type runtimeFileChangeArtifactEvidence = protocol.FileChangeArtifactEvidence
type runtimeFileChangeItemStartedNotificationParams = protocol.FileChangeItemStartedNotificationParams
type runtimeFileChangeItemCompletedNotificationParams = protocol.FileChangeItemCompletedNotificationParams
type runtimeFileChangePatchUpdatedNotificationParams = protocol.FileChangePatchUpdatedNotificationParams

type runtimeFileChangeTracker struct {
	mu        sync.Mutex
	store     store.Store
	notifier  runtimeNotifier
	turn      *store.Turn
	toolItems *runtimeToolItemTracker
	turnDiffs []string
	err       error
}

func newRuntimeFileChangeTracker(st store.Store, notifier runtimeNotifier, turn *store.Turn, toolItems *runtimeToolItemTracker) *runtimeFileChangeTracker {
	return &runtimeFileChangeTracker{
		store:     st,
		notifier:  notifier,
		turn:      turn,
		toolItems: toolItems,
	}
}

func (t *runtimeFileChangeTracker) artifactChanged(event core.ArtifactChangedEvent) {
	if t == nil || t.store == nil || t.turn == nil || strings.TrimSpace(event.Path) == "" {
		return
	}
	changedAt := event.ChangedAt
	if changedAt.IsZero() {
		changedAt = time.Now().UTC()
	}
	change := runtimeFileUpdateChange{
		Path: event.Path,
		Kind: runtimePatchChangeKind{Type: runtimeFileChangeKind(event.Operation)},
		Diff: event.Diff,
	}
	payload := runtimeFileChangePayload{
		Type:    runtimeFileChangeItemKind,
		Changes: []runtimeFileUpdateChange{change},
		Status:  runtimeFileChangeStatusInProgress,
		Evidence: []runtimeFileChangeArtifactEvidence{{
			Path:                 event.Path,
			Operation:            event.Operation,
			Bytes:                event.Bytes,
			BeforeSHA256:         event.BeforeSHA256,
			AfterSHA256:          event.AfterSHA256,
			DiffTruncated:        event.DiffTruncated,
			DiffOmittedReason:    event.DiffOmittedReason,
			ContentEncoding:      event.ContentEncoding,
			ContentTruncated:     event.ContentTruncated,
			ContentOmittedReason: event.ContentOmittedReason,
		}},
	}
	parentItemID := ""
	if t.toolItems != nil {
		parentItemID = t.toolItems.itemID(event.RunID, event.ToolCallID, event.ToolName)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	item, err := t.store.AppendItem(context.Background(), store.AppendItemRequest{
		ThreadID:     t.turn.ThreadID,
		TurnID:       t.turn.ID,
		ParentItemID: parentItemID,
		Kind:         runtimeFileChangeItemKind,
		Status:       runtimeFileChangeStatusInProgress,
		Payload:      mustRuntimeJSON(payload),
	})
	if err != nil {
		t.recordErrorLocked("append", err)
		return
	}
	payload.ID = item.ID
	item, err = t.store.UpdateItem(context.Background(), store.UpdateItemRequest{
		ID:      item.ID,
		Status:  runtimeFileChangeStatusInProgress,
		Payload: mustRuntimeJSON(payload),
	})
	if err != nil {
		t.recordErrorLocked("set id", err)
		return
	}
	if t.notifier != nil {
		t.notifier.PublishNotification("item/started", runtimeFileChangeItemStartedNotificationParams{
			Item:        payload,
			ThreadID:    t.turn.ThreadID,
			TurnID:      t.turn.ID,
			StartedAtMS: changedAt.UnixMilli(),
		})
	}

	payload.Status = runtimeFileChangeStatusCompleted
	item, err = t.store.UpdateItem(context.Background(), store.UpdateItemRequest{
		ID:      item.ID,
		Status:  runtimeFileChangeStatusCompleted,
		Payload: mustRuntimeJSON(payload),
	})
	if err != nil {
		t.recordErrorLocked("complete", err)
		return
	}
	if event.Diff != "" {
		t.turnDiffs = append(t.turnDiffs, event.Diff)
	}
	if t.notifier == nil {
		return
	}
	t.notifier.PublishNotification("item/fileChange/patchUpdated", runtimeFileChangePatchUpdatedNotificationParams{
		ThreadID: t.turn.ThreadID,
		TurnID:   t.turn.ID,
		ItemID:   item.ID,
		Changes:  append([]runtimeFileUpdateChange(nil), payload.Changes...),
	})
	if len(t.turnDiffs) > 0 {
		t.notifier.PublishNotification("turn/diff/updated", turnDiffUpdatedNotificationParams{
			ThreadID: t.turn.ThreadID,
			TurnID:   t.turn.ID,
			Diff:     strings.Join(t.turnDiffs, "\n"),
		})
	}
	t.notifier.PublishNotification("item/completed", runtimeFileChangeItemCompletedNotificationParams{
		Item:          payload,
		ThreadID:      t.turn.ThreadID,
		TurnID:        t.turn.ID,
		CompletedAtMS: changedAt.UnixMilli(),
	})
}

func (t *runtimeFileChangeTracker) Err() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

func (t *runtimeFileChangeTracker) recordErrorLocked(operation string, err error) {
	if err == nil || t.err != nil {
		return
	}
	t.err = fmt.Errorf("persist runtime file change item (%s): %w", operation, err)
	publishRuntimeError(t.notifier, t.turn, t.err.Error())
}

func runtimeFileChangeKind(operation string) string {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "create", "add", "createdirectory":
		return runtimePatchChangeAdd
	case "delete", "remove":
		return runtimePatchChangeDelete
	default:
		return runtimePatchChangeUpdate
	}
}

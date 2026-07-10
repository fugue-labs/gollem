package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestSQLiteStoreThreadLifecyclePersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "appserver.db")

	s := newTestSQLiteStore(t, path)
	thread, err := s.CreateThread(ctx, CreateThreadRequest{
		Title:     "Build protocol",
		Workspace: "/work",
		Settings:  map[string]any{"model": "claude"},
		Metadata:  map[string]any{"source": "test"},
	})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	if thread.ID == "" || thread.Status != ThreadActive {
		t.Fatalf("thread = %+v", thread)
	}

	archived, err := s.ArchiveThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ArchiveThread: %v", err)
	}
	if archived.Status != ThreadArchived || archived.ArchivedAt.IsZero() {
		t.Fatalf("archived = %+v", archived)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s = newTestSQLiteStore(t, path)

	loaded, err := s.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread after reopen: %v", err)
	}
	if loaded.Status != ThreadArchived || loaded.Settings["model"] != "claude" {
		t.Fatalf("loaded = %+v", loaded)
	}

	activeOnly, err := s.ListThreads(ctx, ThreadFilter{Statuses: []ThreadStatus{ThreadActive}})
	if err != nil {
		t.Fatalf("ListThreads active: %v", err)
	}
	if len(activeOnly) != 0 {
		t.Fatalf("activeOnly = %+v", activeOnly)
	}

	unarchived, err := s.UnarchiveThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("UnarchiveThread: %v", err)
	}
	if unarchived.Status != ThreadActive || !unarchived.ArchivedAt.IsZero() {
		t.Fatalf("unarchived = %+v", unarchived)
	}

	deleted, err := s.DeleteThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}
	if deleted.Status != ThreadDeleted || deleted.DeletedAt.IsZero() {
		t.Fatalf("deleted = %+v", deleted)
	}

	visible, err := s.ListThreads(ctx, ThreadFilter{})
	if err != nil {
		t.Fatalf("ListThreads visible: %v", err)
	}
	if len(visible) != 0 {
		t.Fatalf("deleted thread should be hidden by default: %+v", visible)
	}

	all, err := s.ListThreads(ctx, ThreadFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("ListThreads all: %v", err)
	}
	if len(all) != 1 || all[0].Status != ThreadDeleted {
		t.Fatalf("all = %+v", all)
	}
}

func TestSQLiteStoreClosedOperationsReturnErrStoreClosed(t *testing.T) {
	ctx := context.Background()
	s := newTestSQLiteStore(t, filepath.Join(t.TempDir(), "appserver.db"))
	thread, err := s.CreateThread(ctx, CreateThreadRequest{Title: "closed"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := s.CreateThread(ctx, CreateThreadRequest{Title: "after close"}); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("CreateThread after close error = %v, want ErrStoreClosed", err)
	}
	if _, err := s.GetThread(ctx, thread.ID); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("GetThread after close error = %v, want ErrStoreClosed", err)
	}
	if _, err := s.ListThreads(ctx, ThreadFilter{}); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("ListThreads after close error = %v, want ErrStoreClosed", err)
	}
}

func TestSQLiteStoreTurnsAndItemsPersistAndPaginate(t *testing.T) {
	ctx := context.Background()
	s := newTestSQLiteStore(t, filepath.Join(t.TempDir(), "appserver.db"))

	thread, err := s.CreateThread(ctx, CreateThreadRequest{Title: "Timeline"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	turn, err := s.CreateTurn(ctx, CreateTurnRequest{
		ThreadID: thread.ID,
		Input:    json.RawMessage(`{"prompt":"hello"}`),
		Metadata: map[string]any{"kind": "user"},
	})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if turn.Status != TurnQueued {
		t.Fatalf("turn status = %s", turn.Status)
	}
	started, err := s.StartTurn(ctx, turn.ID)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if started.Status != TurnRunning || started.StartedAt.IsZero() {
		t.Fatalf("started = %+v", started)
	}
	completed, err := s.CompleteTurn(ctx, CompleteTurnRequest{
		ID:     turn.ID,
		Result: json.RawMessage(`{"answer":"ok"}`),
		Usage:  map[string]any{"tokens": float64(12)},
	})
	if err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if completed.Status != TurnCompleted || completed.CompletedAt.IsZero() {
		t.Fatalf("completed = %+v", completed)
	}

	first, err := s.AppendItem(ctx, AppendItemRequest{
		ThreadID: thread.ID,
		TurnID:   turn.ID,
		Kind:     "message",
		Status:   "completed",
		Payload:  json.RawMessage(`{"role":"user","text":"hello"}`),
	})
	if err != nil {
		t.Fatalf("AppendItem first: %v", err)
	}
	second, err := s.AppendItem(ctx, AppendItemRequest{
		ThreadID: thread.ID,
		TurnID:   turn.ID,
		Kind:     "message",
		Status:   "completed",
		Payload:  json.RawMessage(`{"role":"assistant","text":"ok"}`),
	})
	if err != nil {
		t.Fatalf("AppendItem second: %v", err)
	}
	if first.Seq == 0 || second.Seq <= first.Seq {
		t.Fatalf("item seqs not increasing: first=%d second=%d", first.Seq, second.Seq)
	}
	updatedFirst, err := s.UpdateItem(ctx, UpdateItemRequest{
		ID:      first.ID,
		Status:  "failed",
		Payload: json.RawMessage(`{"role":"user","text":"updated"}`),
	})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	if updatedFirst.ID != first.ID || updatedFirst.Seq != first.Seq || updatedFirst.Status != "failed" || string(updatedFirst.Payload) != `{"role":"user","text":"updated"}` {
		t.Fatalf("updated item = %#v, want same id/seq with updated status and payload", updatedFirst)
	}

	items, err := s.ListItems(ctx, ItemFilter{ThreadID: thread.ID, Limit: 1})
	if err != nil {
		t.Fatalf("ListItems first page: %v", err)
	}
	if len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("first page = %+v", items)
	}
	next, err := s.ListItems(ctx, ItemFilter{ThreadID: thread.ID, AfterSeq: items[0].Seq})
	if err != nil {
		t.Fatalf("ListItems next page: %v", err)
	}
	if len(next) != 1 || next[0].ID != second.ID {
		t.Fatalf("next page = %+v", next)
	}

	turns, err := s.ListTurns(ctx, TurnFilter{ThreadID: thread.ID, Statuses: []TurnStatus{TurnCompleted}})
	if err != nil {
		t.Fatalf("ListTurns: %v", err)
	}
	if len(turns) != 1 || turns[0].ID != turn.ID {
		t.Fatalf("turns = %+v", turns)
	}
}

func TestSQLiteStoreRollbackThreadPrunesTurnsAndItems(t *testing.T) {
	ctx := context.Background()
	s := newTestSQLiteStore(t, filepath.Join(t.TempDir(), "appserver.db"))

	thread, err := s.CreateThread(ctx, CreateThreadRequest{Title: "Rollback"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	var turns []*Turn
	for _, prompt := range []string{"one", "two", "three"} {
		turn, err := s.CreateTurn(ctx, CreateTurnRequest{ThreadID: thread.ID, Input: json.RawMessage(`{"prompt":"` + prompt + `"}`)})
		if err != nil {
			t.Fatalf("CreateTurn %s: %v", prompt, err)
		}
		if _, err := s.AppendItem(ctx, AppendItemRequest{ThreadID: thread.ID, TurnID: turn.ID, Kind: "message", Payload: json.RawMessage(`{"role":"user","text":"` + prompt + `"}`)}); err != nil {
			t.Fatalf("AppendItem %s: %v", prompt, err)
		}
		turns = append(turns, turn)
	}
	if _, err := s.AppendItem(ctx, AppendItemRequest{ThreadID: thread.ID, Kind: "response_item", Payload: json.RawMessage(`{"text":"later injected context"}`)}); err != nil {
		t.Fatalf("AppendItem trailing: %v", err)
	}

	rolled, err := s.RollbackThread(ctx, RollbackThreadRequest{ID: thread.ID, NumTurns: 2})
	if err != nil {
		t.Fatalf("RollbackThread: %v", err)
	}
	if rolled.Thread.ID != thread.ID || len(rolled.Turns) != 1 || rolled.Turns[0].ID != turns[0].ID {
		t.Fatalf("rollback result = %+v", rolled)
	}
	if len(rolled.RemovedTurns) != 2 || rolled.Marker == nil || rolled.Marker.Kind != "thread_rollback" {
		t.Fatalf("rollback removed/marker = %+v marker=%+v", rolled.RemovedTurns, rolled.Marker)
	}
	remainingTurns, err := s.ListTurns(ctx, TurnFilter{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("ListTurns: %v", err)
	}
	if len(remainingTurns) != 1 || remainingTurns[0].ID != turns[0].ID {
		t.Fatalf("remaining turns = %+v", remainingTurns)
	}
	remainingItems, err := s.ListItems(ctx, ItemFilter{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(remainingItems) != 2 || remainingItems[0].TurnID != turns[0].ID || remainingItems[1].Kind != "thread_rollback" {
		t.Fatalf("remaining items = %+v", remainingItems)
	}
	if _, err := s.GetTurn(ctx, turns[1].ID); !errors.Is(err, ErrTurnNotFound) {
		t.Fatalf("rolled back turn lookup error = %v, want ErrTurnNotFound", err)
	}

	rolled, err = s.RollbackThread(ctx, RollbackThreadRequest{ID: thread.ID, NumTurns: 10})
	if err != nil {
		t.Fatalf("RollbackThread all: %v", err)
	}
	if len(rolled.Turns) != 0 || len(rolled.RemovedTurns) != 1 {
		t.Fatalf("rollback all = %+v", rolled)
	}
	remainingItems, err = s.ListItems(ctx, ItemFilter{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("ListItems after all: %v", err)
	}
	if len(remainingItems) != 1 || remainingItems[0].Kind != "thread_rollback" {
		t.Fatalf("remaining items after all = %+v", remainingItems)
	}

	if _, err := s.RollbackThread(ctx, RollbackThreadRequest{ID: thread.ID}); err == nil {
		t.Fatal("RollbackThread with zero num turns succeeded")
	}
}

func TestSQLiteStoreRollbackThreadPrunesTrailingItemsWhenTurnHasNoItems(t *testing.T) {
	ctx := context.Background()
	s := newTestSQLiteStore(t, filepath.Join(t.TempDir(), "appserver.db"))

	thread, err := s.CreateThread(ctx, CreateThreadRequest{Title: "Empty Turn Rollback"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	firstTurn, err := s.CreateTurn(ctx, CreateTurnRequest{ThreadID: thread.ID, Input: json.RawMessage(`{"prompt":"keep"}`)})
	if err != nil {
		t.Fatalf("CreateTurn first: %v", err)
	}
	if _, err := s.AppendItem(ctx, AppendItemRequest{ThreadID: thread.ID, TurnID: firstTurn.ID, Kind: "message", Payload: json.RawMessage(`{"text":"keep"}`)}); err != nil {
		t.Fatalf("AppendItem first: %v", err)
	}
	emptyTurn, err := s.CreateTurn(ctx, CreateTurnRequest{ThreadID: thread.ID, Input: json.RawMessage(`{"prompt":"remove"}`)})
	if err != nil {
		t.Fatalf("CreateTurn empty: %v", err)
	}
	if _, err := s.AppendItem(ctx, AppendItemRequest{ThreadID: thread.ID, Kind: "response_item", Payload: json.RawMessage(`{"text":"trailing context"}`)}); err != nil {
		t.Fatalf("AppendItem trailing: %v", err)
	}

	rolled, err := s.RollbackThread(ctx, RollbackThreadRequest{ID: thread.ID, NumTurns: 1})
	if err != nil {
		t.Fatalf("RollbackThread: %v", err)
	}
	if len(rolled.RemovedTurns) != 1 || rolled.RemovedTurns[0].ID != emptyTurn.ID {
		t.Fatalf("removed turns = %+v", rolled.RemovedTurns)
	}
	remainingItems, err := s.ListItems(ctx, ItemFilter{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(remainingItems) != 2 || remainingItems[0].TurnID != firstTurn.ID || remainingItems[1].Kind != "thread_rollback" {
		t.Fatalf("remaining items = %+v", remainingItems)
	}
}

func TestSQLiteStoreForkCopiesThreadHistory(t *testing.T) {
	ctx := context.Background()
	s := newTestSQLiteStore(t, filepath.Join(t.TempDir(), "appserver.db"))

	source, err := s.CreateThread(ctx, CreateThreadRequest{
		Title:    "Source",
		Settings: map[string]any{"model": "sonnet"},
		Metadata: map[string]any{"root": "yes"},
	})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	turn, err := s.CreateTurn(ctx, CreateTurnRequest{ThreadID: source.ID, Input: json.RawMessage(`{"prompt":"fork me"}`)})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	parent, err := s.AppendItem(ctx, AppendItemRequest{ThreadID: source.ID, TurnID: turn.ID, Kind: "dynamicToolCall", Payload: json.RawMessage(`{"tool":"echo"}`)})
	if err != nil {
		t.Fatalf("AppendItem: %v", err)
	}
	parent, err = s.UpdateItem(ctx, UpdateItemRequest{ID: parent.ID, Payload: json.RawMessage(`{"id":"` + parent.ID + `","tool":"echo"}`)})
	if err != nil {
		t.Fatalf("UpdateItem parent payload ID: %v", err)
	}
	child, err := s.AppendItem(ctx, AppendItemRequest{
		ThreadID:     source.ID,
		TurnID:       turn.ID,
		ParentItemID: parent.ID,
		Kind:         "commandExecution",
		Payload:      json.RawMessage(`{"command":"echo fork"}`),
	})
	if err != nil {
		t.Fatalf("AppendItem child: %v", err)
	}

	fork, err := s.ForkThread(ctx, ForkThreadRequest{
		SourceThreadID: source.ID,
		Title:          "Fork",
		Metadata:       map[string]any{"fork": "yes"},
		IncludeItems:   true,
	})
	if err != nil {
		t.Fatalf("ForkThread: %v", err)
	}
	if fork.ID == source.ID || fork.ForkedFromThreadID != source.ID || fork.Title != "Fork" {
		t.Fatalf("fork = %+v source = %+v", fork, source)
	}
	if fork.Settings["model"] != "sonnet" || fork.Metadata["root"] != "yes" || fork.Metadata["fork"] != "yes" {
		t.Fatalf("fork metadata/settings not copied: %+v", fork)
	}

	forkTurns, err := s.ListTurns(ctx, TurnFilter{ThreadID: fork.ID})
	if err != nil {
		t.Fatalf("ListTurns fork: %v", err)
	}
	if len(forkTurns) != 1 || forkTurns[0].ID == turn.ID || forkTurns[0].ThreadID != fork.ID {
		t.Fatalf("fork turns = %+v", forkTurns)
	}
	forkItems, err := s.ListItems(ctx, ItemFilter{ThreadID: fork.ID})
	if err != nil {
		t.Fatalf("ListItems fork: %v", err)
	}
	if len(forkItems) != 2 || forkItems[0].ThreadID != fork.ID || forkItems[0].TurnID != forkTurns[0].ID {
		t.Fatalf("fork items = %+v turns = %+v", forkItems, forkTurns)
	}
	if forkItems[0].ID == parent.ID || forkItems[1].ID == child.ID {
		t.Fatalf("fork item IDs were not regenerated: source=%q/%q fork=%q/%q", parent.ID, child.ID, forkItems[0].ID, forkItems[1].ID)
	}
	if forkItems[1].ParentItemID != forkItems[0].ID {
		t.Fatalf("fork child parent = %q, want remapped parent %q", forkItems[1].ParentItemID, forkItems[0].ID)
	}
	var forkParentPayload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(forkItems[0].Payload, &forkParentPayload); err != nil {
		t.Fatalf("decode fork parent payload: %v", err)
	}
	if forkParentPayload.ID != forkItems[0].ID {
		t.Fatalf("fork parent payload id = %q, want %q", forkParentPayload.ID, forkItems[0].ID)
	}
}

func TestSQLiteStoreUpdateThreadSettingsMergesAndReplaces(t *testing.T) {
	ctx := context.Background()
	s := newTestSQLiteStore(t, filepath.Join(t.TempDir(), "appserver.db"))

	thread, err := s.CreateThread(ctx, CreateThreadRequest{
		Settings: map[string]any{"provider": "openai", "model": "gpt"},
		Metadata: map[string]any{"source": "initial"},
	})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	merged, err := s.UpdateThreadSettings(ctx, UpdateThreadSettingsRequest{
		ID:       thread.ID,
		Settings: map[string]any{"model": "claude"},
		Metadata: map[string]any{"updated": true},
	})
	if err != nil {
		t.Fatalf("UpdateThreadSettings merge: %v", err)
	}
	if merged.Settings["provider"] != "openai" || merged.Settings["model"] != "claude" {
		t.Fatalf("merged settings = %#v", merged.Settings)
	}
	if merged.Metadata["source"] != "initial" || merged.Metadata["updated"] != true {
		t.Fatalf("merged metadata = %#v", merged.Metadata)
	}

	replaced, err := s.UpdateThreadSettings(ctx, UpdateThreadSettingsRequest{
		ID:       thread.ID,
		Settings: map[string]any{"provider": "anthropic"},
		Replace:  true,
	})
	if err != nil {
		t.Fatalf("UpdateThreadSettings replace: %v", err)
	}
	if replaced.Settings["provider"] != "anthropic" || replaced.Settings["model"] != nil {
		t.Fatalf("replaced settings = %#v", replaced.Settings)
	}
	if len(replaced.Metadata) != 0 {
		t.Fatalf("replaced metadata = %#v", replaced.Metadata)
	}
}

func TestSQLiteStoreUpdateThreadTitle(t *testing.T) {
	ctx := context.Background()
	s := newTestSQLiteStore(t, filepath.Join(t.TempDir(), "appserver.db"))

	thread, err := s.CreateThread(ctx, CreateThreadRequest{
		Title:    "Original",
		Settings: map[string]any{"goal": "keep"},
		Metadata: map[string]any{"source": "test"},
	})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	renamed, err := s.UpdateThreadTitle(ctx, thread.ID, "Renamed")
	if err != nil {
		t.Fatalf("UpdateThreadTitle: %v", err)
	}
	if renamed.Title != "Renamed" || renamed.Settings["goal"] != "keep" || renamed.Metadata["source"] != "test" {
		t.Fatalf("renamed thread = %#v", renamed)
	}
	if !renamed.UpdatedAt.After(thread.UpdatedAt) && !renamed.UpdatedAt.Equal(thread.UpdatedAt) {
		t.Fatalf("renamed UpdatedAt moved backward: before=%s after=%s", thread.UpdatedAt, renamed.UpdatedAt)
	}

	if _, err := s.DeleteThread(ctx, thread.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}
	if _, err := s.UpdateThreadTitle(ctx, thread.ID, "Deleted"); !errors.Is(err, ErrThreadDeleted) {
		t.Fatalf("UpdateThreadTitle deleted err = %v, want ErrThreadDeleted", err)
	}
}

func TestSQLiteStoreReturnsCopies(t *testing.T) {
	ctx := context.Background()
	s := newTestSQLiteStore(t, filepath.Join(t.TempDir(), "appserver.db"))

	thread, err := s.CreateThread(ctx, CreateThreadRequest{Settings: map[string]any{"model": "a"}})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	thread.Settings["model"] = "mutated"
	loaded, err := s.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if loaded.Settings["model"] != "a" {
		t.Fatalf("store leaked thread settings map: %+v", loaded.Settings)
	}

	turn, err := s.CreateTurn(ctx, CreateTurnRequest{ThreadID: loaded.ID, Input: json.RawMessage(`{"prompt":"original"}`)})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	turn.Input[11] = 'X'
	loadedTurn, err := s.GetTurn(ctx, turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if string(loadedTurn.Input) != `{"prompt":"original"}` {
		t.Fatalf("store leaked turn raw message: %s", loadedTurn.Input)
	}
}

func TestSQLiteStoreRejectsDeletedThreadMutations(t *testing.T) {
	ctx := context.Background()
	s := newTestSQLiteStore(t, filepath.Join(t.TempDir(), "appserver.db"))

	thread, err := s.CreateThread(ctx, CreateThreadRequest{})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	if _, err := s.DeleteThread(ctx, thread.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}
	if _, err := s.ArchiveThread(ctx, thread.ID); !errors.Is(err, ErrThreadDeleted) {
		t.Fatalf("ArchiveThread on deleted thread error = %v, want ErrThreadDeleted", err)
	}
	if _, err := s.UnarchiveThread(ctx, thread.ID); !errors.Is(err, ErrThreadDeleted) {
		t.Fatalf("UnarchiveThread on deleted thread error = %v, want ErrThreadDeleted", err)
	}
	if _, err := s.CreateTurn(ctx, CreateTurnRequest{ThreadID: thread.ID}); !errors.Is(err, ErrThreadDeleted) {
		t.Fatalf("CreateTurn on deleted thread error = %v, want ErrThreadDeleted", err)
	}
	if _, err := s.ForkThread(ctx, ForkThreadRequest{SourceThreadID: thread.ID}); !errors.Is(err, ErrThreadDeleted) {
		t.Fatalf("ForkThread on deleted thread error = %v, want ErrThreadDeleted", err)
	}
}

func TestSQLiteStoreRejectsCrossThreadItemTurn(t *testing.T) {
	ctx := context.Background()
	s := newTestSQLiteStore(t, filepath.Join(t.TempDir(), "appserver.db"))

	threadA, err := s.CreateThread(ctx, CreateThreadRequest{})
	if err != nil {
		t.Fatalf("CreateThread A: %v", err)
	}
	threadB, err := s.CreateThread(ctx, CreateThreadRequest{})
	if err != nil {
		t.Fatalf("CreateThread B: %v", err)
	}
	turnA, err := s.CreateTurn(ctx, CreateTurnRequest{ThreadID: threadA.ID})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}

	_, err = s.AppendItem(ctx, AppendItemRequest{ThreadID: threadB.ID, TurnID: turnA.ID, Kind: "message"})
	if !errors.Is(err, ErrTurnNotFound) {
		t.Fatalf("AppendItem cross-thread error = %v, want ErrTurnNotFound", err)
	}
}

func TestSQLiteStoreRejectsStartingExistingTurnAfterThreadDeleted(t *testing.T) {
	ctx := context.Background()
	s := newTestSQLiteStore(t, filepath.Join(t.TempDir(), "appserver.db"))

	thread, err := s.CreateThread(ctx, CreateThreadRequest{})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	turn, err := s.CreateTurn(ctx, CreateTurnRequest{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if _, err := s.DeleteThread(ctx, thread.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}
	if _, err := s.StartTurn(ctx, turn.ID); !errors.Is(err, ErrThreadDeleted) {
		t.Fatalf("StartTurn after delete error = %v, want ErrThreadDeleted", err)
	}
	if _, err := s.CompleteTurn(ctx, CompleteTurnRequest{ID: turn.ID}); !errors.Is(err, ErrThreadDeleted) {
		t.Fatalf("CompleteTurn after delete error = %v, want ErrThreadDeleted", err)
	}
}

func newTestSQLiteStore(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	return s
}

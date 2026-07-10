package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	toolfs "github.com/fugue-labs/gollem/appserver/tools/fs"
	"github.com/fugue-labs/gollem/core"
)

func TestFilesystemRuntimeToolsExposeScopedOperations(t *testing.T) {
	fsSvc, err := toolfs.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer fsSvc.Close()

	tools := FilesystemRuntimeTools(fsSvc)
	want := map[string]struct {
		sequential      bool
		concurrencySafe bool
	}{
		"workspace_read_file":        {concurrencySafe: true},
		"workspace_list_directory":   {concurrencySafe: true},
		"workspace_file_metadata":    {concurrencySafe: true},
		"workspace_write_file":       {sequential: true},
		"workspace_create_directory": {sequential: true},
		"workspace_remove_path":      {sequential: true},
		"workspace_copy_path":        {sequential: true},
	}
	if len(tools) != len(want) {
		t.Fatalf("runtime filesystem tools = %d, want %d", len(tools), len(want))
	}
	for _, tool := range tools {
		expected, ok := want[tool.Definition.Name]
		if !ok {
			t.Fatalf("unexpected runtime tool %q", tool.Definition.Name)
		}
		if tool.Definition.Namespace != runtimeFilesystemToolNamespace {
			t.Fatalf("tool %q namespace = %q", tool.Definition.Name, tool.Definition.Namespace)
		}
		if tool.Definition.Sequential != expected.sequential || tool.Definition.ConcurrencySafe != expected.concurrencySafe {
			t.Fatalf("tool %q scheduling = sequential %v concurrency-safe %v", tool.Definition.Name, tool.Definition.Sequential, tool.Definition.ConcurrencySafe)
		}
		delete(want, tool.Definition.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing runtime tools: %v", want)
	}
}

func TestFilesystemRuntimeReadToolRejectsWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	fsSvc, err := toolfs.NewService(root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer fsSvc.Close()
	tool := findRuntimeToolByName(t, FilesystemRuntimeTools(fsSvc), "workspace_read_file")

	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })
	_, err = tool.Handler(context.Background(), &core.RunContext{}, `{"path":"../outside.txt"}`)
	if !errors.Is(err, toolfs.ErrPathOutsideRoot) {
		t.Fatalf("read escape error = %v, want %v", err, toolfs.ErrPathOutsideRoot)
	}
}

func TestFilesystemRuntimeToolsBoundEvidenceAndSuppressNoOpChanges(t *testing.T) {
	binary := newRuntimeFilesystemReadResult(&toolfs.FileContent{
		Path:    "image.bin",
		Content: []byte{0, 1, 2, 3},
		Size:    4,
	})
	if binary.Content != "" || binary.ContentEncoding != "" || binary.OmittedReason != "binary content omitted" || binary.SHA256 == "" {
		t.Fatalf("binary read result = %#v", binary)
	}
	largeContent := strings.Repeat("x", runtimeFilesystemContentMaxBytes+100)
	large := newRuntimeFilesystemReadResult(&toolfs.FileContent{
		Path:    "large.txt",
		Content: []byte(largeContent),
		Size:    int64(len(largeContent)),
	})
	if !large.ContentTruncated || len(large.Content) > runtimeFilesystemContentMaxBytes || large.ContentEncoding != "utf-8" {
		t.Fatalf("large read result = content bytes %d, truncated %v, encoding %q", len(large.Content), large.ContentTruncated, large.ContentEncoding)
	}
	_, _, omitted := runtimeArtifactDiff("large.txt",
		runtimeArtifactCapture{Exists: true, Content: []byte("before\n")},
		runtimeArtifactCapture{Exists: true, Content: []byte(largeContent)},
	)
	if !strings.Contains(omitted, "diff limit") {
		t.Fatalf("large diff omitted reason = %q", omitted)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "same.txt"), []byte("unchanged\n"), 0o644); err != nil {
		t.Fatalf("write no-op fixture: %v", err)
	}
	fsSvc, err := toolfs.NewService(root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer fsSvc.Close()
	bus := core.NewEventBus()
	defer bus.Close()
	eventCount := 0
	unsubscribe := core.Subscribe(bus, func(core.ArtifactChangedEvent) { eventCount++ })
	defer unsubscribe()
	tool := findRuntimeToolByName(t, FilesystemRuntimeTools(fsSvc), "workspace_write_file")
	result, err := tool.Handler(
		core.ContextWithToolCallID(context.Background(), "call-no-op"),
		&core.RunContext{EventBus: bus, RunID: "run-no-op", ToolCallID: "call-no-op", ToolName: "workspace_write_file"},
		`{"path":"same.txt","content":"unchanged\n"}`,
	)
	if err != nil {
		t.Fatalf("no-op write: %v", err)
	}
	mutation, ok := result.(runtimeFilesystemMutationResult)
	if !ok || mutation.Changed || eventCount != 0 {
		t.Fatalf("no-op write result = %#v, artifact events = %d", result, eventCount)
	}
}

func TestRuntimeToolTimelinePayloadsAreBounded(t *testing.T) {
	large := strings.Repeat("payload", runtimeToolPayloadMaxBytes)
	arguments := runtimeToolArguments(`{"content":"` + large + `"}`)
	summary, ok := arguments.(runtimeToolPayloadSummary)
	if !ok || !summary.Omitted || summary.Bytes <= runtimeToolPayloadMaxBytes || summary.SHA256 == "" {
		t.Fatalf("large argument summary = %#v", arguments)
	}
	output := boundedRuntimeToolOutput("prefix-" + large + "-suffix")
	if len(output) > runtimeToolPayloadMaxBytes || !strings.HasPrefix(output, "prefix-") || !strings.HasSuffix(output, "-suffix") || !strings.Contains(output, "output truncated") {
		t.Fatalf("bounded output bytes = %d, prefix/suffix/marker missing", len(output))
	}
}

func TestServerRuntimeApprovedFilesystemWritePersistsFileChange(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "preexisting.txt"), []byte("dirty before turn\n"), 0o644); err != nil {
		t.Fatalf("write pre-existing fixture: %v", err)
	}
	st := newRuntimeTestStore(t)
	approvals := NewApprovalService()
	fsSvc, err := toolfs.NewService(root, toolfs.WithApproval(approvals.FilesystemApproval))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer fsSvc.Close()
	model := core.NewTestModel(
		core.ToolCallResponseWithID(
			"workspace_write_file",
			`{"path":"notes/result.txt","content":"runtime write\n"}`,
			"call-write",
		),
		core.TextResponse("write complete"),
	)
	server := readyServer(
		WithStore(st),
		WithFilesystem(fsSvc),
		WithApprovalService(approvals),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(FilesystemRuntimeTools(fsSvc)...),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{
		"title":  "Runtime filesystem",
		"prompt": "write the result",
	}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)

	approvalRequest := waitForServerRequest(t, server)
	if approvalRequest.Method != "item/fileChange/requestApproval" {
		t.Fatalf("approval method = %q", approvalRequest.Method)
	}
	requestID, _ := approvalRequest.ID.Value().(string)
	if requestID == "" {
		t.Fatalf("approval request id = %#v", approvalRequest.ID.Value())
	}
	var approvalParams fileChangeApprovalParams
	if err := json.Unmarshal(approvalRequest.Params, &approvalParams); err != nil {
		t.Fatalf("decode approval params: %v", err)
	}
	if approvalParams.ThreadID != started.Thread.ID || approvalParams.TurnID != started.Turn.ID {
		t.Fatalf("approval turn context = %q/%q, want %q/%q", approvalParams.ThreadID, approvalParams.TurnID, started.Thread.ID, started.Turn.ID)
	}
	if approvalParams.ItemID != "call-write" {
		t.Fatalf("approval item id = %q, want tool call id", approvalParams.ItemID)
	}
	approvalResponse := server.HandleRequest(ctx, request("approval/respond", map[string]any{
		"requestId": requestID,
		"approved":  true,
	}))
	if approvalResponse.Error != nil {
		t.Fatalf("approval/respond error: %v", approvalResponse.Error)
	}

	events := waitForNotificationSet(t, server,
		"item/fileChange/patchUpdated",
		"turn/diff/updated",
		"turn/completed",
	)
	data, err := os.ReadFile(filepath.Join(root, "notes", "result.txt"))
	if err != nil {
		t.Fatalf("read runtime output: %v", err)
	}
	if string(data) != "runtime write\n" {
		t.Fatalf("runtime output = %q", data)
	}

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	fileItem := findRuntimeFileChangeItem(t, items, "notes/result.txt")
	toolItem := findRuntimeToolItem(t, items, "workspace_write_file", "path", "notes/result.txt")
	if fileItem.Item.ParentItemID != toolItem.Item.ID {
		t.Fatalf("file item parent = %q, want dynamic tool item %q", fileItem.Item.ParentItemID, toolItem.Item.ID)
	}
	if fileItem.Item.Status != runtimeFileChangeStatusCompleted || fileItem.Payload.Status != runtimeFileChangeStatusCompleted {
		t.Fatalf("file item status = %q/%q", fileItem.Item.Status, fileItem.Payload.Status)
	}
	if len(fileItem.Payload.Changes) != 1 || fileItem.Payload.Changes[0].Kind.Type != runtimePatchChangeAdd {
		t.Fatalf("file item changes = %#v", fileItem.Payload.Changes)
	}
	if !strings.Contains(fileItem.Payload.Changes[0].Diff, "+runtime write") {
		t.Fatalf("file item diff = %q", fileItem.Payload.Changes[0].Diff)
	}
	if len(fileItem.Payload.Evidence) != 1 || fileItem.Payload.Evidence[0].AfterSHA256 == "" {
		t.Fatalf("file item evidence = %#v", fileItem.Payload.Evidence)
	}

	patch := decodeRuntimeFileChangePatch(t, events)
	if patch.ThreadID != started.Thread.ID || patch.TurnID != started.Turn.ID || patch.ItemID != fileItem.Item.ID {
		t.Fatalf("patch ids = %#v", patch)
	}
	if len(patch.Changes) != 1 || patch.Changes[0].Path != "notes/result.txt" {
		t.Fatalf("patch changes = %#v", patch.Changes)
	}
	diff := decodeRuntimeTurnDiff(t, events)
	if !strings.Contains(diff.Diff, "notes/result.txt") || strings.Contains(diff.Diff, "preexisting.txt") {
		t.Fatalf("turn-local diff = %q", diff.Diff)
	}
}

func TestServerRuntimePersistsUpdateAndDeleteFileChanges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "update.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write update fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "delete.txt"), []byte("remove me\n"), 0o644); err != nil {
		t.Fatalf("write delete fixture: %v", err)
	}
	st := newRuntimeTestStore(t)
	fsSvc, err := toolfs.NewService(root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer fsSvc.Close()
	model := core.NewTestModel(
		core.ToolCallResponseWithID(
			"workspace_write_file",
			`{"path":"update.txt","content":"after\n"}`,
			"call-update",
		),
		core.ToolCallResponseWithID(
			"workspace_remove_path",
			`{"path":"delete.txt"}`,
			"call-delete",
		),
		core.TextResponse("changes complete"),
	)
	server := readyServer(
		WithStore(st),
		WithFilesystem(fsSvc),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(FilesystemRuntimeTools(fsSvc)...),
		)),
	)
	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "update and delete"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	events := waitForNotificationSet(t, server,
		"item/fileChange/patchUpdated",
		"item/fileChange/patchUpdated",
		"turn/completed",
	)

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	updated := findRuntimeFileChangeItem(t, items, "update.txt")
	if updated.Payload.Changes[0].Kind.Type != runtimePatchChangeUpdate ||
		!strings.Contains(updated.Payload.Changes[0].Diff, "-before") ||
		!strings.Contains(updated.Payload.Changes[0].Diff, "+after") {
		t.Fatalf("updated file change = %#v", updated.Payload.Changes[0])
	}
	deleted := findRuntimeFileChangeItem(t, items, "delete.txt")
	if deleted.Payload.Changes[0].Kind.Type != runtimePatchChangeDelete || !strings.Contains(deleted.Payload.Changes[0].Diff, "-remove me") {
		t.Fatalf("deleted file change = %#v", deleted.Payload.Changes[0])
	}
	if _, err := os.Stat(filepath.Join(root, "delete.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted file stat error = %v, want not-exist", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "update.txt"))
	if err != nil || string(data) != "after\n" {
		t.Fatalf("updated file = %q, error %v", data, err)
	}
	var finalDiff string
	for _, event := range events {
		if event.Method != "turn/diff/updated" {
			continue
		}
		var params turnDiffUpdatedNotificationParams
		if err := json.Unmarshal(event.Params, &params); err != nil {
			t.Fatalf("decode turn diff: %v", err)
		}
		finalDiff = params.Diff
	}
	if !strings.Contains(finalDiff, "update.txt") || !strings.Contains(finalDiff, "delete.txt") {
		t.Fatalf("cumulative turn diff = %q", finalDiff)
	}
}

func TestServerRuntimeDeniedFilesystemWriteDoesNotPersistFileChange(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := newRuntimeTestStore(t)
	approvals := NewApprovalService()
	fsSvc, err := toolfs.NewService(root, toolfs.WithApproval(approvals.FilesystemApproval))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer fsSvc.Close()
	model := core.NewTestModel(
		core.ToolCallResponseWithID(
			"workspace_write_file",
			`{"path":"denied.txt","content":"must not exist\n"}`,
			"call-denied",
		),
		core.TextResponse("write was denied"),
	)
	server := readyServer(
		WithStore(st),
		WithFilesystem(fsSvc),
		WithApprovalService(approvals),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(FilesystemRuntimeTools(fsSvc)...),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "try a denied write"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	approvalRequest := waitForServerRequest(t, server)
	requestID, _ := approvalRequest.ID.Value().(string)
	approvalResponse := server.HandleRequest(ctx, request("approval/respond", map[string]any{
		"requestId": requestID,
		"approved":  false,
		"message":   "denied by test",
	}))
	if approvalResponse.Error != nil {
		t.Fatalf("approval/respond error: %v", approvalResponse.Error)
	}
	events := waitForNotificationSet(t, server, "turn/completed")
	if _, err := os.Stat(filepath.Join(root, "denied.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("denied file stat error = %v, want not-exist", err)
	}
	for _, event := range events {
		if event.Method == "item/fileChange/patchUpdated" || event.Method == "turn/diff/updated" {
			t.Fatalf("denied write emitted %s: %#v", event.Method, event)
		}
	}
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, item := range items {
		if item.Kind == runtimeFileChangeItemKind {
			t.Fatalf("denied write persisted file change item: %#v", item)
		}
	}
	toolItem := findRuntimeToolItem(t, items, "workspace_write_file", "path", "denied.txt")
	if toolItem.Status != runtimeToolStatusFailed {
		t.Fatalf("denied dynamic tool status = %q, want failed", toolItem.Status)
	}
}

func TestServerRuntimeInterruptCancelsPendingFilesystemApproval(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := newRuntimeTestStore(t)
	approvals := NewApprovalService()
	fsSvc, err := toolfs.NewService(root, toolfs.WithApproval(approvals.FilesystemApproval))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer fsSvc.Close()
	model := core.NewTestModel(
		core.ToolCallResponseWithID(
			"workspace_write_file",
			`{"path":"interrupted.txt","content":"must not exist\n"}`,
			"call-interrupted",
		),
		core.TextResponse("should not run"),
	)
	server := readyServer(
		WithStore(st),
		WithFilesystem(fsSvc),
		WithApprovalService(approvals),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(FilesystemRuntimeTools(fsSvc)...),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "interrupt this write"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	approvalRequest := waitForServerRequest(t, server)
	requestID, _ := approvalRequest.ID.Value().(string)
	interrupt := server.HandleRequest(ctx, request("turn/interrupt", map[string]any{"turnId": started.Turn.ID}))
	if interrupt.Error != nil {
		t.Fatalf("turn/interrupt error: %v", interrupt.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	completed, err := st.GetTurn(ctx, started.Turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if completed.Status != store.TurnInterrupted {
		t.Fatalf("interrupted turn status = %q", completed.Status)
	}
	if _, err := os.Stat(filepath.Join(root, "interrupted.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted file stat error = %v, want not-exist", err)
	}
	lateApproval := server.HandleRequest(ctx, request("approval/respond", map[string]any{
		"requestId": requestID,
		"approved":  true,
	}))
	if lateApproval.Error == nil || lateApproval.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("late approval error = %#v, want invalid params", lateApproval.Error)
	}
}

func findRuntimeToolByName(t *testing.T, tools []core.Tool, name string) core.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Definition.Name == name {
			return tool
		}
	}
	t.Fatalf("runtime tool %q not found", name)
	return core.Tool{}
}

type storedRuntimeFileChangeItem struct {
	Item    *store.Item
	Payload runtimeFileChangePayload
}

func findRuntimeFileChangeItem(t *testing.T, items []*store.Item, path string) storedRuntimeFileChangeItem {
	t.Helper()
	for _, item := range items {
		if item == nil || item.Kind != runtimeFileChangeItemKind {
			continue
		}
		var payload runtimeFileChangePayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatalf("decode stored file change item: %v", err)
		}
		if len(payload.Changes) == 1 && payload.Changes[0].Path == path {
			return storedRuntimeFileChangeItem{Item: item, Payload: payload}
		}
	}
	t.Fatalf("file change item for %q not found in %#v", path, items)
	return storedRuntimeFileChangeItem{}
}

func decodeRuntimeFileChangePatch(t *testing.T, notifications []protocol.Notification) runtimeFileChangePatchUpdatedNotificationParams {
	t.Helper()
	for _, notification := range notifications {
		if notification.Method != "item/fileChange/patchUpdated" {
			continue
		}
		var params runtimeFileChangePatchUpdatedNotificationParams
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			t.Fatalf("decode item/fileChange/patchUpdated: %v", err)
		}
		return params
	}
	t.Fatalf("file change patch notification missing from %v", notificationMethods(notifications))
	return runtimeFileChangePatchUpdatedNotificationParams{}
}

func decodeRuntimeTurnDiff(t *testing.T, notifications []protocol.Notification) turnDiffUpdatedNotificationParams {
	t.Helper()
	for _, notification := range notifications {
		if notification.Method != "turn/diff/updated" {
			continue
		}
		var params turnDiffUpdatedNotificationParams
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			t.Fatalf("decode turn/diff/updated: %v", err)
		}
		return params
	}
	t.Fatalf("turn diff notification missing from %v", notificationMethods(notifications))
	return turnDiffUpdatedNotificationParams{}
}

package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	appcache "github.com/fugue-labs/gollem/appserver/cache"
	"github.com/fugue-labs/gollem/appserver/catalog"
	appconfig "github.com/fugue-labs/gollem/appserver/config"
	appmcp "github.com/fugue-labs/gollem/appserver/mcp"
	"github.com/fugue-labs/gollem/appserver/protocol"
	appskills "github.com/fugue-labs/gollem/appserver/skills"
	"github.com/fugue-labs/gollem/appserver/store"
	toolfs "github.com/fugue-labs/gollem/appserver/tools/fs"
	toolgit "github.com/fugue-labs/gollem/appserver/tools/git"
	toolprocess "github.com/fugue-labs/gollem/appserver/tools/process"
	extmcp "github.com/fugue-labs/gollem/ext/mcp"
)

func TestServerHandshakeAndUnavailable(t *testing.T) {
	ctx := context.Background()
	server := NewServer(
		WithImplementationInfo(protocol.ImplementationInfo{Name: "test-server", Version: "v1"}),
		WithInitializeHome("/tmp/test-gollem-home"),
	)

	preInit := server.HandleRequest(ctx, request("thread/list", nil))
	if preInit.Error == nil || preInit.Error.Code != protocol.CodeInvalidRequest {
		t.Fatalf("pre-init error = %#v, want invalid request", preInit.Error)
	}
	missingVersion := server.HandleRequest(ctx, request("initialize", protocol.InitializeParams{
		ClientInfo: protocol.ClientInfo{Name: "test-client"},
	}))
	if missingVersion.Error == nil || missingVersion.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("missing client version error = %#v, want invalid params", missingVersion.Error)
	}

	initResp := server.HandleRequest(ctx, request("initialize", protocol.InitializeParams{
		ClientInfo: protocol.ClientInfo{Name: "test-client", Version: "1.0.0"},
		Capabilities: &protocol.InitializeCapabilities{
			ExperimentalAPI:                true,
			RequestAttestation:             true,
			MCPServerOpenAIFormElicitation: true,
			OptOutNotificationMethods:      []string{"thread/status/changed"},
			Experimental:                   map[string]bool{"gollemFeature": true},
		},
	}))
	if initResp.Error != nil {
		t.Fatalf("initialize error: %v", initResp.Error)
	}
	var initResult protocol.InitializeResponse
	decodeResult(t, initResp, &initResult)
	if initResult.ProtocolVersion != protocol.ProtocolVersion || initResult.ServerInfo.Name != "test-server" ||
		initResult.UserAgent != "test-server/v1" || initResult.CodexHome != "/tmp/test-gollem-home" ||
		initResult.PlatformFamily == "" || initResult.PlatformOS == "" {
		t.Fatalf("initialize result = %#v", initResult)
	}
	if server.NotificationEnabled("thread/status/changed") {
		t.Fatal("NotificationEnabled returned true for opted-out method")
	}
	clientCapabilities := server.ClientCapabilities()
	if !clientCapabilities.ExperimentalAPI || !clientCapabilities.RequestAttestation ||
		!clientCapabilities.MCPServerOpenAIFormElicitation || !clientCapabilities.Experimental["gollemFeature"] {
		t.Fatalf("client capabilities = %+v", clientCapabilities)
	}
	clientCapabilities.Experimental["gollemFeature"] = false
	clientCapabilities.OptOutNotificationMethods[0] = "changed"
	freshCapabilities := server.ClientCapabilities()
	if !freshCapabilities.Experimental["gollemFeature"] || freshCapabilities.OptOutNotificationMethods[0] != "thread/status/changed" {
		t.Fatal("ClientCapabilities returned mutable server storage")
	}

	beforeReady := server.HandleRequest(ctx, request("thread/list", nil))
	if beforeReady.Error == nil || beforeReady.Error.Code != protocol.CodeInvalidRequest {
		t.Fatalf("before-ready error = %#v, want invalid request", beforeReady.Error)
	}
	if err := server.HandleNotification(ctx, protocol.Notification{Method: "initialized"}); err != nil {
		t.Fatalf("initialized notification: %v", err)
	}

	unknown := server.HandleRequest(ctx, request("not/a/method", nil))
	if unknown.Error == nil || unknown.Error.Code != protocol.CodeMethodNotFound {
		t.Fatalf("unknown method error = %#v, want method not found", unknown.Error)
	}
	knownMissingDependency := server.HandleRequest(ctx, request("thread/list", nil))
	if knownMissingDependency.Error == nil || knownMissingDependency.Error.Code != protocol.CodeMethodUnavailable {
		t.Fatalf("known missing dependency error = %#v, want unavailable", knownMissingDependency.Error)
	}
	missingProcess := server.HandleRequest(ctx, request("command/exec", map[string]any{"command": "echo hi"}))
	if missingProcess.Error == nil || missingProcess.Error.Code != protocol.CodeMethodUnavailable {
		t.Fatalf("missing process service error = %#v, want unavailable", missingProcess.Error)
	}

	repeatedInit := server.HandleRequest(ctx, request("initialize", nil))
	if repeatedInit.Error == nil || repeatedInit.Error.Code != protocol.CodeInvalidRequest {
		t.Fatalf("repeated initialize error = %#v, want invalid request", repeatedInit.Error)
	}
}

func TestServerThreadStoreHandlers(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Root", Workspace: "/tmp/work"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	turn, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: thread.ID, Input: json.RawMessage(`{"text":"hi"}`)})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if _, err := st.AppendItem(ctx, store.AppendItemRequest{ThreadID: thread.ID, TurnID: turn.ID, Kind: "message", Payload: json.RawMessage(`{"text":"hi"}`)}); err != nil {
		t.Fatalf("AppendItem: %v", err)
	}
	if _, err := st.CompleteTurn(ctx, store.CompleteTurnRequest{
		ID:     turn.ID,
		Status: store.TurnCompleted,
	}); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	server := readyServer(WithStore(st))
	listResp := server.HandleRequest(ctx, request("thread/list", nil))
	if listResp.Error != nil {
		t.Fatalf("thread/list error: %v", listResp.Error)
	}
	var list struct {
		Threads []*store.Thread `json:"threads"`
	}
	decodeResult(t, listResp, &list)
	if len(list.Threads) != 1 || list.Threads[0].ID != thread.ID {
		t.Fatalf("thread/list = %#v", list.Threads)
	}

	readResp := server.HandleRequest(ctx, request("thread/read", map[string]any{"threadId": thread.ID}))
	if readResp.Error != nil {
		t.Fatalf("thread/read error: %v", readResp.Error)
	}
	var read legacyThreadReadResult
	decodeResult(t, readResp, &read)
	if read.Thread.ID != thread.ID || len(read.Turns) != 1 || len(read.Items) != 1 {
		t.Fatalf("thread/read = %#v", read)
	}

	forkResp := server.HandleRequest(ctx, request("thread/fork", map[string]any{
		"threadId":     thread.ID,
		"title":        "Fork",
		"includeItems": true,
	}))
	if forkResp.Error != nil {
		t.Fatalf("thread/fork error: %v", forkResp.Error)
	}
	var forked struct {
		Thread *store.Thread `json:"thread"`
	}
	decodeResult(t, forkResp, &forked)
	if forked.Thread.ForkedFromThreadID != thread.ID {
		t.Fatalf("forked thread = %#v", forked.Thread)
	}
	threadEvents := server.DrainNotifications()
	assertNotificationMethods(t, threadEvents, "thread/started")
	var startedNotice threadNotificationParams
	if err := json.Unmarshal(threadEvents[0].Params, &startedNotice); err != nil {
		t.Fatalf("decode thread started notice: %v", err)
	}
	if startedNotice.ThreadID != forked.Thread.ID || startedNotice.Thread == nil {
		t.Fatalf("thread started notice = %#v", startedNotice)
	}

	archiveResp := server.HandleRequest(ctx, request("thread/archive", map[string]any{"threadId": thread.ID}))
	if archiveResp.Error != nil {
		t.Fatalf("thread/archive error: %v", archiveResp.Error)
	}
	var archived struct {
		Thread *store.Thread `json:"thread"`
	}
	decodeResult(t, archiveResp, &archived)
	if archived.Thread.Status != store.ThreadArchived {
		t.Fatalf("archived status = %s", archived.Thread.Status)
	}
	threadEvents = server.DrainNotifications()
	assertNotificationMethods(t, threadEvents, "thread/status/changed", "thread/archived")
}

func TestServerThreadForkTypedRequestIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	source, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Source"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	turn, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: source.ID})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if _, err := st.CompleteTurn(ctx, store.CompleteTurnRequest{
		ID:     turn.ID,
		Status: store.TurnCompleted,
	}); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if _, err := st.AppendItem(ctx, store.AppendItemRequest{
		ThreadID: source.ID,
		TurnID:   turn.ID,
		Kind:     "message",
		Status:   "completed",
		Payload:  json.RawMessage(`{"text":"fork me"}`),
	}); err != nil {
		t.Fatalf("AppendItem: %v", err)
	}

	server := readyServer(WithStore(st))
	params := map[string]any{
		"threadId":       " \t" + source.ID + "\n",
		"idempotencyKey": " fork-key ",
	}
	firstResponse := server.HandleRequest(ctx, request("thread/fork", params))
	if firstResponse.Error != nil {
		t.Fatalf("thread/fork first error: %v", firstResponse.Error)
	}
	var first protocol.ThreadHistoryForkResult
	decodeResult(t, firstResponse, &first)
	if first.Thread.ID == source.ID ||
		first.Thread.ForkedFromThreadID != source.ID ||
		first.SourceThreadID != source.ID ||
		first.IdempotencyKey != "fork-key" ||
		first.Reused ||
		!first.HistoryCopied ||
		first.FileChangeRecoveryTransferred {
		t.Fatalf("first fork = %+v", first)
	}
	assertNotificationMethods(t, server.DrainNotifications(), "thread/started")

	forkTurns, err := st.ListTurns(ctx, store.TurnFilter{ThreadID: first.Thread.ID})
	if err != nil {
		t.Fatalf("ListTurns: %v", err)
	}
	forkItems, err := st.ListItems(ctx, store.ItemFilter{ThreadID: first.Thread.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(forkTurns) != 1 || len(forkItems) != 1 ||
		forkTurns[0].ID == turn.ID || forkItems[0].TurnID != forkTurns[0].ID {
		t.Fatalf("fork history turns=%+v items=%+v", forkTurns, forkItems)
	}

	secondResponse := server.HandleRequest(ctx, request("thread/fork", params))
	if secondResponse.Error != nil {
		t.Fatalf("thread/fork second error: %v", secondResponse.Error)
	}
	var second protocol.ThreadHistoryForkResult
	decodeResult(t, secondResponse, &second)
	if !second.Reused || second.Thread.ID != first.Thread.ID {
		t.Fatalf("second fork = %+v, want reuse of %q", second, first.Thread.ID)
	}
	if notifications := server.DrainNotifications(); len(notifications) != 0 {
		t.Fatalf("reused fork notifications = %+v, want none", notifications)
	}

	other, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Other"})
	if err != nil {
		t.Fatalf("CreateThread other: %v", err)
	}
	conflict := server.HandleRequest(ctx, request("thread/fork", map[string]any{
		"threadId":       other.ID,
		"idempotencyKey": "fork-key",
	}))
	if conflict.Error == nil || conflict.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("conflicting fork response = %+v, want invalid params", conflict)
	}

	activeSource, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Active"})
	if err != nil {
		t.Fatalf("CreateThread active: %v", err)
	}
	if _, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: activeSource.ID}); err != nil {
		t.Fatalf("CreateTurn active: %v", err)
	}
	activeResponse := server.HandleRequest(ctx, request("thread/fork", map[string]any{
		"threadId":       activeSource.ID,
		"idempotencyKey": "active-key",
	}))
	if activeResponse.Error == nil || activeResponse.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("active fork response = %+v, want invalid params", activeResponse)
	}

	mixed := server.HandleRequest(ctx, request("thread/fork", map[string]any{
		"threadId":       source.ID,
		"idempotencyKey": "other-key",
		"includeItems":   true,
	}))
	if mixed.Error == nil || mixed.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("mixed typed/legacy response = %+v, want invalid params", mixed)
	}
	for _, key := range []string{"", " \t\n"} {
		invalid := server.HandleRequest(ctx, request("thread/fork", map[string]any{
			"threadId":       source.ID,
			"idempotencyKey": key,
		}))
		if invalid.Error == nil || invalid.Error.Code != protocol.CodeInvalidParams {
			t.Fatalf("thread/fork key %q response = %+v, want invalid params", key, invalid)
		}
	}
	blankSource := server.HandleRequest(ctx, request("thread/fork", map[string]any{
		"threadId":       " \t\n",
		"idempotencyKey": "blank-source",
	}))
	if blankSource.Error == nil || blankSource.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("blank source response = %+v, want invalid params", blankSource)
	}
}

func TestServerThreadApproveGuardianDeniedActionDeferredStub(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Guardian"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	server := readyServer(WithStore(st))

	resp := server.HandleRequest(ctx, request("thread/approveGuardianDeniedAction", map[string]any{
		"threadId": thread.ID,
		"event": map[string]any{
			"type": "guardian_assessment",
			"id":   "guardian-1",
		},
	}))
	if resp.Error == nil || resp.Error.Code != protocol.CodeMethodUnavailable {
		t.Fatalf("guardian approval error = %#v, want method unavailable", resp.Error)
	}
	var data protocol.UnavailableData
	if err := json.Unmarshal(resp.Error.Data, &data); err != nil {
		t.Fatalf("decode unavailable data: %v", err)
	}
	if data.Method != "thread/approveGuardianDeniedAction" || data.Surface != protocol.SurfaceClientRequest || data.Status != protocol.MethodDeferredStub {
		t.Fatalf("unavailable data = %+v", data)
	}
	if !strings.Contains(data.Reason, "Guardian denied-action replay is not implemented") {
		t.Fatalf("unavailable reason = %q", data.Reason)
	}

	missingEvent := server.HandleRequest(ctx, request("thread/approveGuardianDeniedAction", map[string]any{"threadId": thread.ID}))
	if missingEvent.Error == nil || missingEvent.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("missing event error = %#v, want invalid params", missingEvent.Error)
	}
}

func TestServerThreadInjectItemsHandler(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Inject"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	server := readyServer(WithStore(st))

	injectResp := server.HandleRequest(ctx, request("thread/inject_items", map[string]any{
		"threadId": thread.ID,
		"items": []any{
			map[string]any{
				"type": "message",
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "output_text", "text": "injected assistant history"},
				},
			},
		},
	}))
	if injectResp.Error != nil {
		t.Fatalf("thread/inject_items error: %v", injectResp.Error)
	}
	var injectedResult threadInjectItemsResponse
	decodeResult(t, injectResp, &injectedResult)

	events := server.DrainNotifications()
	assertNotificationMethods(t, events, "item/completed")
	var itemNotice runtimeItemNotificationParams
	if err := json.Unmarshal(events[0].Params, &itemNotice); err != nil {
		t.Fatalf("decode item notice: %v", err)
	}
	if itemNotice.ThreadID != thread.ID || itemNotice.Item == nil || itemNotice.Item.Kind != threadInjectedResponseItemKind {
		t.Fatalf("item notice = %#v", itemNotice)
	}

	itemsResp := server.HandleRequest(ctx, request("thread/items/list", map[string]any{"threadId": thread.ID}))
	if itemsResp.Error != nil {
		t.Fatalf("thread/items/list error: %v", itemsResp.Error)
	}
	var listed struct {
		Items []*store.Item `json:"items"`
	}
	decodeResult(t, itemsResp, &listed)
	if len(listed.Items) != 1 || listed.Items[0].Kind != threadInjectedResponseItemKind || !strings.Contains(string(listed.Items[0].Payload), "injected assistant history") {
		t.Fatalf("listed injected items = %#v", listed.Items)
	}

	loadedResp := server.HandleRequest(ctx, request("thread/loaded/list", nil))
	if loadedResp.Error != nil {
		t.Fatalf("thread/loaded/list error: %v", loadedResp.Error)
	}
	var loaded protocol.ThreadLoadedListResponse
	decodeResult(t, loadedResp, &loaded)
	if !sameStringSet(loaded.Data, []string{thread.ID}) {
		t.Fatalf("loaded after inject = %#v", loaded.Data)
	}

	deleted, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Deleted"})
	if err != nil {
		t.Fatalf("CreateThread deleted: %v", err)
	}
	if _, err := st.DeleteThread(ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}
	deletedResp := server.HandleRequest(ctx, request("thread/inject_items", map[string]any{
		"threadId": deleted.ID,
		"items":    []any{map[string]any{"role": "user", "content": "nope"}},
	}))
	if deletedResp.Error == nil || deletedResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("deleted inject error = %#v", deletedResp.Error)
	}
}

func TestServerThreadDiscoveryHandlers(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	alpha, err := st.CreateThread(ctx, store.CreateThreadRequest{
		Title:     "Alpha migration",
		Workspace: "/tmp/alpha",
		Metadata:  map[string]any{"sourceKind": "appServer"},
	})
	if err != nil {
		t.Fatalf("CreateThread alpha: %v", err)
	}
	beta, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Beta notes", Workspace: "/tmp/beta"})
	if err != nil {
		t.Fatalf("CreateThread beta: %v", err)
	}
	if _, err := st.AppendItem(ctx, store.AppendItemRequest{
		ThreadID: beta.ID,
		Kind:     "message",
		Payload:  json.RawMessage(`{"text":"find the hidden needle in this payload"}`),
	}); err != nil {
		t.Fatalf("AppendItem beta: %v", err)
	}
	archived, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Archived needle"})
	if err != nil {
		t.Fatalf("CreateThread archived: %v", err)
	}
	if _, err := st.ArchiveThread(ctx, archived.ID); err != nil {
		t.Fatalf("ArchiveThread: %v", err)
	}
	deleted, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Deleted needle"})
	if err != nil {
		t.Fatalf("CreateThread deleted: %v", err)
	}
	if _, err := st.DeleteThread(ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}

	server := readyServer(WithStore(st))
	for _, id := range []string{alpha.ID, beta.ID} {
		readResp := server.HandleRequest(ctx, request("thread/read", map[string]any{"threadId": id, "includeItems": false}))
		if readResp.Error != nil {
			t.Fatalf("thread/read %s error: %v", id, readResp.Error)
		}
	}

	loadedResp := server.HandleRequest(ctx, request("thread/loaded/list", map[string]any{"limit": 1}))
	if loadedResp.Error != nil {
		t.Fatalf("thread/loaded/list error: %v", loadedResp.Error)
	}
	var loadedPage protocol.ThreadLoadedListResponse
	decodeResult(t, loadedResp, &loadedPage)
	if len(loadedPage.Data) != 1 || loadedPage.NextCursor == nil {
		t.Fatalf("loaded first page = %#v, want one result and next cursor", loadedPage)
	}
	nextLoadedResp := server.HandleRequest(ctx, request("thread/loaded/list", map[string]any{"cursor": *loadedPage.NextCursor}))
	if nextLoadedResp.Error != nil {
		t.Fatalf("thread/loaded/list next error: %v", nextLoadedResp.Error)
	}
	var loadedNext protocol.ThreadLoadedListResponse
	decodeResult(t, nextLoadedResp, &loadedNext)
	loadedIDs := append(loadedPage.Data, loadedNext.Data...)
	if !sameStringSet(loadedIDs, []string{alpha.ID, beta.ID}) || loadedNext.NextCursor != nil {
		t.Fatalf("loaded ids = %#v next = %v", loadedIDs, loadedNext.NextCursor)
	}

	searchResp := server.HandleRequest(ctx, request("thread/search", map[string]any{
		"searchTerm": "needle",
		"limit":      10,
	}))
	if searchResp.Error != nil {
		t.Fatalf("thread/search error: %v", searchResp.Error)
	}
	var search threadSearchResponse
	decodeResult(t, searchResp, &search)
	if len(search.Data) != 1 || search.Data[0].Thread.ID != beta.ID || !strings.Contains(strings.ToLower(search.Data[0].Snippet), "needle") {
		t.Fatalf("search active result = %#v", search.Data)
	}

	archivedOnly := true
	archivedResp := server.HandleRequest(ctx, request("thread/search", map[string]any{
		"searchTerm": "needle",
		"archived":   archivedOnly,
	}))
	if archivedResp.Error != nil {
		t.Fatalf("thread/search archived error: %v", archivedResp.Error)
	}
	var archivedSearch threadSearchResponse
	decodeResult(t, archivedResp, &archivedSearch)
	if len(archivedSearch.Data) != 1 || archivedSearch.Data[0].Thread.ID != archived.ID {
		t.Fatalf("archived search = %#v", archivedSearch.Data)
	}

	sourceFilteredResp := server.HandleRequest(ctx, request("thread/search", map[string]any{
		"searchTerm":  "alpha",
		"sourceKinds": []string{"cli"},
	}))
	if sourceFilteredResp.Error != nil {
		t.Fatalf("thread/search source filtered error: %v", sourceFilteredResp.Error)
	}
	var sourceFiltered threadSearchResponse
	decodeResult(t, sourceFilteredResp, &sourceFiltered)
	if len(sourceFiltered.Data) != 0 {
		t.Fatalf("source filtered search = %#v, want none", sourceFiltered.Data)
	}

	emptySearchResp := server.HandleRequest(ctx, request("thread/search", map[string]any{"searchTerm": "  "}))
	if emptySearchResp.Error == nil || emptySearchResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("empty search error = %#v", emptySearchResp.Error)
	}
}

func TestServerThreadUnsubscribeHandler(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Subscribed"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	neverLoaded, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Never loaded"})
	if err != nil {
		t.Fatalf("CreateThread never loaded: %v", err)
	}
	deleted, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Deleted"})
	if err != nil {
		t.Fatalf("CreateThread deleted: %v", err)
	}
	if _, err := st.DeleteThread(ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}

	server := readyServer(WithStore(st), WithThreadIdleUnloadAfter(500*time.Millisecond))
	readResp := server.HandleRequest(ctx, request("thread/read", map[string]any{"threadId": thread.ID, "includeTurns": false, "includeItems": false}))
	if readResp.Error != nil {
		t.Fatalf("thread/read error: %v", readResp.Error)
	}

	firstResp := server.HandleRequest(ctx, request("thread/unsubscribe", map[string]any{"threadId": thread.ID}))
	if firstResp.Error != nil {
		t.Fatalf("thread/unsubscribe error: %v", firstResp.Error)
	}
	var first protocol.ThreadUnsubscribeResponse
	decodeResult(t, firstResp, &first)
	if first.Status != "unsubscribed" {
		t.Fatalf("first unsubscribe = %#v", first)
	}
	loadedResp := server.HandleRequest(ctx, request("thread/loaded/list", nil))
	if loadedResp.Error != nil {
		t.Fatalf("thread/loaded/list error: %v", loadedResp.Error)
	}
	var loaded protocol.ThreadLoadedListResponse
	decodeResult(t, loadedResp, &loaded)
	if !sameStringSet(loaded.Data, []string{thread.ID}) {
		t.Fatalf("loaded after unsubscribe = %#v", loaded.Data)
	}
	if events := server.DrainNotifications(); len(events) != 0 {
		t.Fatalf("unsubscribe emitted immediate notifications: %#v", events)
	}

	secondResp := server.HandleRequest(ctx, request("thread/unsubscribe", map[string]any{"threadId": thread.ID}))
	if secondResp.Error != nil {
		t.Fatalf("second thread/unsubscribe error: %v", secondResp.Error)
	}
	var second protocol.ThreadUnsubscribeResponse
	decodeResult(t, secondResp, &second)
	if second.Status != "notSubscribed" {
		t.Fatalf("second unsubscribe = %#v", second)
	}

	readResp = server.HandleRequest(ctx, request("thread/read", map[string]any{"threadId": thread.ID, "includeTurns": false, "includeItems": false}))
	if readResp.Error != nil {
		t.Fatalf("thread/read resubscribe error: %v", readResp.Error)
	}
	time.Sleep(600 * time.Millisecond)
	if events := server.DrainNotifications(); len(events) != 0 {
		t.Fatalf("resubscribed thread emitted idle close: %#v", events)
	}

	finalResp := server.HandleRequest(ctx, request("thread/unsubscribe", map[string]any{"threadId": thread.ID}))
	if finalResp.Error != nil {
		t.Fatalf("final thread/unsubscribe error: %v", finalResp.Error)
	}
	var final protocol.ThreadUnsubscribeResponse
	decodeResult(t, finalResp, &final)
	if final.Status != "unsubscribed" {
		t.Fatalf("final unsubscribe = %#v", final)
	}
	events := waitForNotificationMethods(t, server, "thread/closed", "thread/status/changed")
	var closed protocol.ThreadClosedNotification
	if err := json.Unmarshal(events[0].Params, &closed); err != nil {
		t.Fatalf("decode closed notice: %v", err)
	}
	if closed.ThreadID != thread.ID {
		t.Fatalf("closed notice = %#v", closed)
	}
	var notLoaded threadNotLoadedStatusNotificationParams
	if err := json.Unmarshal(events[1].Params, &notLoaded); err != nil {
		t.Fatalf("decode status notice: %v", err)
	}
	if notLoaded.ThreadID != thread.ID || notLoaded.Status["type"] != "notLoaded" {
		t.Fatalf("notLoaded status notice = %#v", notLoaded)
	}

	loadedResp = server.HandleRequest(ctx, request("thread/loaded/list", nil))
	if loadedResp.Error != nil {
		t.Fatalf("thread/loaded/list after close error: %v", loadedResp.Error)
	}
	loaded = protocol.ThreadLoadedListResponse{}
	decodeResult(t, loadedResp, &loaded)
	if len(loaded.Data) != 0 {
		t.Fatalf("loaded after idle close = %#v", loaded.Data)
	}

	for _, tc := range []struct {
		name     string
		threadID string
	}{
		{name: "closed", threadID: thread.ID},
		{name: "never-loaded", threadID: neverLoaded.ID},
		{name: "deleted", threadID: deleted.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := server.HandleRequest(ctx, request("thread/unsubscribe", map[string]any{"threadId": tc.threadID}))
			if resp.Error != nil {
				t.Fatalf("thread/unsubscribe error: %v", resp.Error)
			}
			var result protocol.ThreadUnsubscribeResponse
			decodeResult(t, resp, &result)
			if result.Status != "notLoaded" {
				t.Fatalf("unsubscribe %s = %#v, want notLoaded", tc.name, result)
			}
		})
	}

	missingIDResp := server.HandleRequest(ctx, request("thread/unsubscribe", map[string]any{"threadId": ""}))
	if missingIDResp.Error == nil || missingIDResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("missing threadId error = %#v", missingIDResp.Error)
	}
}

func TestServerThreadRollbackHandler(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Rollback Title"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	firstTurn, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: thread.ID, Input: json.RawMessage(`{"prompt":"first"}`)})
	if err != nil {
		t.Fatalf("CreateTurn first: %v", err)
	}
	if _, err := st.AppendItem(ctx, store.AppendItemRequest{ThreadID: thread.ID, TurnID: firstTurn.ID, Kind: "message", Payload: json.RawMessage(`{"role":"user","text":"first"}`)}); err != nil {
		t.Fatalf("AppendItem first: %v", err)
	}
	if _, err := st.StartTurn(ctx, firstTurn.ID); err != nil {
		t.Fatalf("StartTurn first: %v", err)
	}
	if _, err := st.CompleteTurn(ctx, store.CompleteTurnRequest{ID: firstTurn.ID, Status: store.TurnCompleted}); err != nil {
		t.Fatalf("CompleteTurn first: %v", err)
	}
	secondTurn, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: thread.ID, Input: json.RawMessage(`{"prompt":"second"}`)})
	if err != nil {
		t.Fatalf("CreateTurn second: %v", err)
	}
	if _, err := st.AppendItem(ctx, store.AppendItemRequest{ThreadID: thread.ID, TurnID: secondTurn.ID, Kind: "message", Payload: json.RawMessage(`{"role":"user","text":"second"}`)}); err != nil {
		t.Fatalf("AppendItem second: %v", err)
	}
	if _, err := st.StartTurn(ctx, secondTurn.ID); err != nil {
		t.Fatalf("StartTurn second: %v", err)
	}
	if _, err := st.CompleteTurn(ctx, store.CompleteTurnRequest{ID: secondTurn.ID, Status: store.TurnCompleted}); err != nil {
		t.Fatalf("CompleteTurn second: %v", err)
	}

	server := readyServer(WithStore(st))
	rollbackResp := server.HandleRequest(ctx, request("thread/rollback", map[string]any{
		"threadId": thread.ID,
		"numTurns": 1,
	}))
	if rollbackResp.Error != nil {
		t.Fatalf("thread/rollback error: %v", rollbackResp.Error)
	}
	events := server.DrainNotifications()
	assertNotificationMethods(t, events, "deprecationNotice")
	var notice deprecationNoticeNotificationParams
	if err := json.Unmarshal(events[0].Params, &notice); err != nil {
		t.Fatalf("decode deprecation notice: %v", err)
	}
	if notice.Summary != threadRollbackDeprecationSummary || notice.Details != nil {
		t.Fatalf("deprecation notice = %#v", notice)
	}
	var rollback protocol.ThreadHistoryRollbackResult
	decodeResult(t, rollbackResp, &rollback)
	if rollback.Thread.ID != thread.ID || rollback.Thread.Name == nil || *rollback.Thread.Name != "Rollback Title" {
		t.Fatalf("rollback thread identity = %#v", rollback.Thread)
	}
	if len(rollback.Thread.Turns) != 1 || rollback.Thread.Turns[0].ID != firstTurn.ID {
		t.Fatalf("rollback turns = %#v", rollback.Thread.Turns)
	}
	if !reflect.DeepEqual(rollback.RemovedTurnIDs, []string{secondTurn.ID}) {
		t.Fatalf("removed turn ids = %#v", rollback.RemovedTurnIDs)
	}
	if rollback.Marker.Kind != "thread_rollback" || rollback.Marker.ThreadID != thread.ID {
		t.Fatalf("rollback marker = %#v", rollback.Marker)
	}
	if rollback.WorkspaceEffectsReverted {
		t.Fatal("history rollback claimed to revert workspace effects")
	}
	remainingTurns, err := st.ListTurns(ctx, store.TurnFilter{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("ListTurns: %v", err)
	}
	if len(remainingTurns) != 1 || remainingTurns[0].ID != firstTurn.ID {
		t.Fatalf("remaining turns = %#v", remainingTurns)
	}
	remainingItems, err := st.ListItems(ctx, store.ItemFilter{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(remainingItems) != 2 || remainingItems[0].TurnID != firstTurn.ID || remainingItems[1].Kind != "thread_rollback" {
		t.Fatalf("remaining items = %#v", remainingItems)
	}
	loadedResp := server.HandleRequest(ctx, request("thread/loaded/list", nil))
	if loadedResp.Error != nil {
		t.Fatalf("thread/loaded/list error: %v", loadedResp.Error)
	}
	var loaded protocol.ThreadLoadedListResponse
	decodeResult(t, loadedResp, &loaded)
	if !sameStringSet(loaded.Data, []string{thread.ID}) {
		t.Fatalf("loaded after rollback = %#v", loaded.Data)
	}

	invalidResp := server.HandleRequest(ctx, request("thread/rollback", map[string]any{
		"threadId": thread.ID,
		"numTurns": 0,
	}))
	if invalidResp.Error == nil || invalidResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("invalid rollback error = %#v", invalidResp.Error)
	}

	activeThread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Active"})
	if err != nil {
		t.Fatalf("CreateThread active: %v", err)
	}
	activeTurn, err := st.CreateTurn(ctx, store.CreateTurnRequest{
		ThreadID: activeThread.ID,
		Input:    json.RawMessage(`{"prompt":"active"}`),
	})
	if err != nil {
		t.Fatalf("CreateTurn active: %v", err)
	}
	activeResp := server.HandleRequest(ctx, request("thread/rollback", map[string]any{
		"threadId": activeThread.ID,
		"numTurns": 1,
	}))
	if activeResp.Error == nil ||
		activeResp.Error.Code != protocol.CodeInvalidRequest ||
		activeResp.Error.Message != "cannot roll back thread history while a turn is active" {
		t.Fatalf("active rollback error = %#v", activeResp.Error)
	}
	if events := server.DrainNotifications(); len(events) != 0 {
		t.Fatalf("rejected active rollback emitted notifications: %#v", events)
	}
	if loaded, err := st.GetTurn(ctx, activeTurn.ID); err != nil || loaded.ID != activeTurn.ID {
		t.Fatalf("active turn was pruned: turn=%#v err=%v", loaded, err)
	}

	tuiThread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "TUI"})
	if err != nil {
		t.Fatalf("CreateThread tui: %v", err)
	}
	tuiTurn, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: tuiThread.ID, Input: json.RawMessage(`{"prompt":"tui"}`)})
	if err != nil {
		t.Fatalf("CreateTurn tui: %v", err)
	}
	if _, err := st.StartTurn(ctx, tuiTurn.ID); err != nil {
		t.Fatalf("StartTurn tui: %v", err)
	}
	if _, err := st.CompleteTurn(ctx, store.CompleteTurnRequest{ID: tuiTurn.ID, Status: store.TurnCompleted}); err != nil {
		t.Fatalf("CompleteTurn tui: %v", err)
	}
	tuiServer := NewServer(WithStore(st))
	_ = tuiServer.HandleRequest(ctx, request("initialize", protocol.InitializeParams{ClientInfo: protocol.ClientInfo{Name: "codex-tui", Version: "1.0.0"}}))
	if err := tuiServer.HandleNotification(ctx, protocol.Notification{Method: "initialized"}); err != nil {
		t.Fatalf("initialized tui: %v", err)
	}
	tuiResp := tuiServer.HandleRequest(ctx, request("thread/rollback", map[string]any{
		"threadId": tuiThread.ID,
		"numTurns": 1,
	}))
	if tuiResp.Error != nil {
		t.Fatalf("thread/rollback tui error: %v", tuiResp.Error)
	}
	if events := tuiServer.DrainNotifications(); len(events) != 0 {
		t.Fatalf("codex-tui rollback emitted deprecation notice: %#v", events)
	}
}

func TestServerThreadCompactStartHandler(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Compact"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	turn, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: thread.ID, Input: json.RawMessage(`{"prompt":"old"}`)})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if _, err := st.AppendItem(ctx, store.AppendItemRequest{ThreadID: thread.ID, TurnID: turn.ID, Kind: "message", Payload: json.RawMessage(`{"role":"user","text":"old prompt"}`)}); err != nil {
		t.Fatalf("AppendItem user: %v", err)
	}
	if _, err := st.AppendItem(ctx, store.AppendItemRequest{ThreadID: thread.ID, TurnID: turn.ID, Kind: "message", Payload: json.RawMessage(`{"role":"assistant","text":"old answer"}`)}); err != nil {
		t.Fatalf("AppendItem assistant: %v", err)
	}

	server := readyServer(WithStore(st))
	resp := server.HandleRequest(ctx, request("thread/compact/start", map[string]any{"threadId": thread.ID}))
	if resp.Error != nil {
		t.Fatalf("thread/compact/start error: %v", resp.Error)
	}
	var result map[string]any
	decodeResult(t, resp, &result)
	if len(result) != 0 {
		t.Fatalf("compact result = %#v, want empty object", result)
	}
	events := server.DrainNotifications()
	assertNotificationMethods(t, events, "turn/started", "item/started", "item/completed", "turn/completed", "thread/compacted")
	var compacted contextCompactedNotificationParams
	if err := json.Unmarshal(events[4].Params, &compacted); err != nil {
		t.Fatalf("decode compacted notification: %v", err)
	}
	if compacted.ThreadID != thread.ID || compacted.TurnID == "" {
		t.Fatalf("compacted notification = %#v", compacted)
	}
	var startedItem runtimeItemNotificationParams
	if err := json.Unmarshal(events[1].Params, &startedItem); err != nil {
		t.Fatalf("decode item/started: %v", err)
	}
	if startedItem.ItemID == "" || startedItem.Item == nil || startedItem.Item.Kind != threadCompactionItemKind || startedItem.Item.Status != "running" {
		t.Fatalf("item/started params = %#v", startedItem)
	}
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 3 || items[2].Kind != threadCompactionItemKind || items[2].Status != "completed" {
		t.Fatalf("items after compact = %#v", items)
	}
	var payload threadCompactionPayload
	if err := json.Unmarshal(items[2].Payload, &payload); err != nil {
		t.Fatalf("decode compact payload: %v", err)
	}
	if payload.Type != threadCompactionItemKind || !strings.Contains(payload.Summary, "old prompt") || !strings.Contains(payload.Summary, "old answer") {
		t.Fatalf("compact payload = %#v", payload)
	}
	turns, err := st.ListTurns(ctx, store.TurnFilter{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("ListTurns: %v", err)
	}
	if len(turns) != 2 || turns[1].ID != compacted.TurnID || turns[1].Status != store.TurnCompleted {
		t.Fatalf("turns after compact = %#v", turns)
	}
	loadedResp := server.HandleRequest(ctx, request("thread/loaded/list", nil))
	if loadedResp.Error != nil {
		t.Fatalf("thread/loaded/list error: %v", loadedResp.Error)
	}
	var loaded protocol.ThreadLoadedListResponse
	decodeResult(t, loadedResp, &loaded)
	if !sameStringSet(loaded.Data, []string{thread.ID}) {
		t.Fatalf("loaded after compact = %#v", loaded.Data)
	}

	invalidResp := server.HandleRequest(ctx, request("thread/compact/start", map[string]any{"threadId": ""}))
	if invalidResp.Error == nil || invalidResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("invalid compact error = %#v", invalidResp.Error)
	}
}

func TestServerThreadShellCommandHandler(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.NewSQLiteStore(filepath.Join(root, "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Shell", Workspace: "."})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	var server *Server
	processSvc, err := toolprocess.NewService(root,
		toolprocess.WithOutputSink(func(ev toolprocess.OutputEvent) {
			server.PublishProcessOutput(ev)
		}),
		toolprocess.WithExitSink(func(ev toolprocess.ExitEvent) {
			server.PublishProcessExited(ev)
		}),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server = readyServer(WithStore(st), WithProcess(processSvc))

	resp := server.HandleRequest(ctx, request("thread/shellCommand", map[string]any{
		"threadId": thread.ID,
		"command":  "sleep 0.05; printf shell-output",
	}))
	if resp.Error != nil {
		t.Fatalf("thread/shellCommand error: %v", resp.Error)
	}
	var result map[string]any
	decodeResult(t, resp, &result)
	if len(result) != 0 {
		t.Fatalf("shell command result = %#v, want empty object", result)
	}

	events := waitForNotificationSet(t, server,
		"turn/started",
		"item/started",
		"process/outputDelta",
		"item/commandExecution/outputDelta",
		"process/exited",
		"item/completed",
		"turn/completed",
	)
	var delta commandExecutionOutputDeltaNotificationParams
	for _, event := range events {
		if event.Method != "item/commandExecution/outputDelta" {
			continue
		}
		if err := json.Unmarshal(event.Params, &delta); err != nil {
			t.Fatalf("decode command delta: %v", err)
		}
	}
	if delta.ThreadID != thread.ID || delta.TurnID == "" || delta.ItemID == "" || delta.Delta != "shell-output" {
		t.Fatalf("command delta = %#v", delta)
	}
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].Kind != threadShellCommandItemKind || items[0].Status != commandExecutionStatusCompleted {
		t.Fatalf("shell command items = %#v", items)
	}
	var payload threadShellCommandPayload
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatalf("decode shell payload: %v", err)
	}
	if payload.Command != "sleep 0.05; printf shell-output" || payload.Source != commandExecutionSourceUserShell || payload.AggregatedOutput == nil || *payload.AggregatedOutput != "shell-output" || payload.ExitCode == nil || *payload.ExitCode != 0 || payload.ProcessID == nil {
		t.Fatalf("shell payload = %#v", payload)
	}
	turns, err := st.ListTurns(ctx, store.TurnFilter{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("ListTurns: %v", err)
	}
	if len(turns) != 1 || turns[0].Status != store.TurnCompleted {
		t.Fatalf("shell command turns = %#v", turns)
	}

	invalidResp := server.HandleRequest(ctx, request("thread/shellCommand", map[string]any{"threadId": thread.ID, "command": "   "}))
	if invalidResp.Error == nil || invalidResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("invalid shell command error = %#v", invalidResp.Error)
	}
}

func TestServerThreadShellCommandPublishesTurnDiffUpdated(t *testing.T) {
	ctx := context.Background()
	repo := initRepo(t)
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Shell diff", Workspace: repo})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	gitSvc, err := toolgit.NewService(repo, toolgit.WithWorktreeRoot(filepath.Join(t.TempDir(), "worktrees")))
	if err != nil {
		t.Fatalf("NewService git: %v", err)
	}
	var server *Server
	processSvc, err := toolprocess.NewService(repo,
		toolprocess.WithOutputSink(func(ev toolprocess.OutputEvent) {
			server.PublishProcessOutput(ev)
		}),
		toolprocess.WithExitSink(func(ev toolprocess.ExitEvent) {
			server.PublishProcessExited(ev)
		}),
	)
	if err != nil {
		t.Fatalf("NewService process: %v", err)
	}
	server = readyServer(WithStore(st), WithProcess(processSvc), WithGit(gitSvc))

	resp := server.HandleRequest(ctx, request("thread/shellCommand", map[string]any{
		"threadId": thread.ID,
		"command":  "printf 'changed\\n' > README.md",
	}))
	if resp.Error != nil {
		t.Fatalf("thread/shellCommand error: %v", resp.Error)
	}
	events := waitForNotificationSet(t, server,
		"process/exited",
		"turn/diff/updated",
		"turn/completed",
	)
	var diffNotice turnDiffUpdatedNotificationParams
	for _, event := range events {
		if event.Method != "turn/diff/updated" {
			continue
		}
		if err := json.Unmarshal(event.Params, &diffNotice); err != nil {
			t.Fatalf("decode turn/diff/updated: %v", err)
		}
	}
	if diffNotice.ThreadID != thread.ID || diffNotice.TurnID == "" {
		t.Fatalf("turn diff ids = %#v", diffNotice)
	}
	if !strings.Contains(diffNotice.Diff, "README.md") || !strings.Contains(diffNotice.Diff, "-hello") || !strings.Contains(diffNotice.Diff, "+changed") {
		t.Fatalf("turn diff = %q, want README.md hello->changed patch", diffNotice.Diff)
	}

	resp = server.HandleRequest(ctx, request("thread/shellCommand", map[string]any{
		"threadId": thread.ID,
		"command":  "true",
	}))
	if resp.Error != nil {
		t.Fatalf("thread/shellCommand no-op error: %v", resp.Error)
	}
	events = waitForNotificationSet(t, server, "process/exited", "turn/completed")
	for _, event := range events {
		if event.Method == "turn/diff/updated" {
			t.Fatalf("unexpected turn/diff/updated for unchanged git diff: %#v", event)
		}
	}
}

func TestServerThreadControlHandlers(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{
		Title:    "Controls",
		Settings: map[string]any{"provider": "openai"},
		Metadata: map[string]any{"source": "initial"},
	})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	server := readyServer(WithStore(st))

	getResp := server.HandleRequest(ctx, request("thread/goal/get", map[string]any{"threadId": thread.ID}))
	if getResp.Error != nil {
		t.Fatalf("thread/goal/get error: %v", getResp.Error)
	}
	var initialGoal protocol.ThreadGoalGetResponse
	decodeResult(t, getResp, &initialGoal)
	if initialGoal.Set == nil || *initialGoal.Set || initialGoal.Goal != nil {
		t.Fatalf("initial goal = %#v", initialGoal)
	}

	goalPayload := map[string]any{
		"objective":     "finish PR",
		"status":        "active",
		"budgetLimited": false,
	}
	setResp := server.HandleRequest(ctx, request("thread/goal/set", map[string]any{
		"threadId": thread.ID,
		"goal":     goalPayload,
	}))
	if setResp.Error != nil {
		t.Fatalf("thread/goal/set error: %v", setResp.Error)
	}
	var setGoal protocol.ThreadGoalSetResponse
	decodeResult(t, setResp, &setGoal)
	if setGoal.Set == nil || !*setGoal.Set || setGoal.Goal.Objective != "finish PR" || setGoal.Thread == nil || setGoal.Thread.Settings[threadGoalSettingKey] == nil {
		t.Fatalf("set goal = %#v", setGoal)
	}
	events := server.DrainNotifications()
	assertNotificationMethods(t, events, "thread/settings/updated", "thread/goal/updated")
	var goalNotice protocol.ThreadGoalUpdatedNotification
	if err := json.Unmarshal(events[1].Params, &goalNotice); err != nil {
		t.Fatalf("decode goal notice: %v", err)
	}
	if goalNotice.ThreadID != thread.ID || goalNotice.Goal.Status != protocol.ThreadGoalActive {
		t.Fatalf("goal notice = %#v", goalNotice)
	}

	metadataResp := server.HandleRequest(ctx, request("thread/metadata/update", map[string]any{
		"threadId": thread.ID,
		"metadata": map[string]any{"reviewed": true},
	}))
	if metadataResp.Error != nil {
		t.Fatalf("thread/metadata/update error: %v", metadataResp.Error)
	}
	var metadataUpdated struct {
		Thread   *store.Thread  `json:"thread"`
		Metadata map[string]any `json:"metadata"`
	}
	decodeResult(t, metadataResp, &metadataUpdated)
	if metadataUpdated.Metadata["source"] != "initial" || metadataUpdated.Metadata["reviewed"] != true {
		t.Fatalf("merged metadata = %#v", metadataUpdated.Metadata)
	}
	events = server.DrainNotifications()
	assertNotificationMethods(t, events, "thread/settings/updated")

	replaceMetadataResp := server.HandleRequest(ctx, request("thread/metadata/update", map[string]any{
		"threadId": thread.ID,
		"metadata": map[string]any{"source": "replacement"},
		"replace":  true,
	}))
	if replaceMetadataResp.Error != nil {
		t.Fatalf("thread/metadata/update replace error: %v", replaceMetadataResp.Error)
	}
	metadataUpdated = struct {
		Thread   *store.Thread  `json:"thread"`
		Metadata map[string]any `json:"metadata"`
	}{}
	decodeResult(t, replaceMetadataResp, &metadataUpdated)
	if metadataUpdated.Metadata["source"] != "replacement" || metadataUpdated.Metadata["reviewed"] != nil || metadataUpdated.Thread.Settings[threadGoalSettingKey] == nil {
		t.Fatalf("replaced metadata = %#v settings = %#v", metadataUpdated.Metadata, metadataUpdated.Thread.Settings)
	}
	_ = server.DrainNotifications()

	memoryResp := server.HandleRequest(ctx, request("thread/memoryMode/set", map[string]any{
		"threadId": thread.ID,
		"mode":     "disabled",
	}))
	if memoryResp.Error != nil {
		t.Fatalf("thread/memoryMode/set error: %v", memoryResp.Error)
	}
	var memoryResult protocol.ThreadMemoryModeSetResponse
	decodeResult(t, memoryResp, &memoryResult)
	if memoryResult.MemoryMode != "disabled" || memoryResult.Thread.Settings[threadMemoryModeSettingKey] != "disabled" {
		t.Fatalf("memory result = %#v", memoryResult)
	}
	events = server.DrainNotifications()
	assertNotificationMethods(t, events, "thread/settings/updated")

	nameResp := server.HandleRequest(ctx, request("thread/name/set", map[string]any{
		"threadId": thread.ID,
		"name":     "Renamed Controls",
	}))
	if nameResp.Error != nil {
		t.Fatalf("thread/name/set error: %v", nameResp.Error)
	}
	var nameResult protocol.ThreadSetNameResponse
	decodeResult(t, nameResp, &nameResult)
	if nameResult.Name != "Renamed Controls" || nameResult.Thread.Title != "Renamed Controls" || nameResult.Thread.Settings[threadMemoryModeSettingKey] != "disabled" {
		t.Fatalf("name result = %#v", nameResult)
	}
	events = server.DrainNotifications()
	assertNotificationMethods(t, events, "thread/name/updated")
	var nameNotice protocol.ThreadNameUpdatedNotification
	if err := json.Unmarshal(events[0].Params, &nameNotice); err != nil {
		t.Fatalf("decode name notice: %v", err)
	}
	if nameNotice.ThreadID != thread.ID || nameNotice.Name != "Renamed Controls" {
		t.Fatalf("name notice = %#v", nameNotice)
	}

	emptyNameResp := server.HandleRequest(ctx, request("thread/name/set", map[string]any{
		"threadId": thread.ID,
		"name":     "  ",
	}))
	if emptyNameResp.Error == nil || emptyNameResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("empty name error = %#v", emptyNameResp.Error)
	}

	badModeResp := server.HandleRequest(ctx, request("thread/memoryMode/set", map[string]any{
		"threadId": thread.ID,
		"mode":     "sometimes",
	}))
	if badModeResp.Error == nil || badModeResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("bad memory mode error = %#v", badModeResp.Error)
	}

	clearResp := server.HandleRequest(ctx, request("thread/goal/clear", map[string]any{"threadId": thread.ID}))
	if clearResp.Error != nil {
		t.Fatalf("thread/goal/clear error: %v", clearResp.Error)
	}
	var cleared protocol.ThreadGoalClearResponse
	decodeResult(t, clearResp, &cleared)
	if !cleared.Cleared || cleared.Thread == nil || cleared.Thread.Settings[threadGoalSettingKey] != nil || cleared.Thread.Settings[threadMemoryModeSettingKey] != "disabled" {
		t.Fatalf("cleared goal = %#v", cleared)
	}
	events = server.DrainNotifications()
	assertNotificationMethods(t, events, "thread/settings/updated", "thread/goal/cleared")

	getResp = server.HandleRequest(ctx, request("thread/goal/get", map[string]any{"threadId": thread.ID}))
	if getResp.Error != nil {
		t.Fatalf("thread/goal/get after clear error: %v", getResp.Error)
	}
	var finalGoal protocol.ThreadGoalGetResponse
	decodeResult(t, getResp, &finalGoal)
	if finalGoal.Set == nil || *finalGoal.Set || finalGoal.Goal != nil {
		t.Fatalf("final goal = %#v", finalGoal)
	}

	clearAgainResp := server.HandleRequest(ctx, request("thread/goal/clear", map[string]any{"threadId": thread.ID}))
	if clearAgainResp.Error != nil {
		t.Fatalf("thread/goal/clear no-op error: %v", clearAgainResp.Error)
	}
	var clearAgain protocol.ThreadGoalClearResponse
	decodeResult(t, clearAgainResp, &clearAgain)
	if clearAgain.Cleared {
		t.Fatalf("clear again = %#v, want no-op", clearAgain)
	}
	if events := server.DrainNotifications(); len(events) != 0 {
		t.Fatalf("clear no-op emitted notifications: %#v", events)
	}
}

func TestServerMemoryResetHandler(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{
		Title:    "Memory",
		Settings: map[string]any{threadMemoryModeSettingKey: "enabled"},
	})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	memoryRoot := filepath.Join(t.TempDir(), "memories")
	if err := os.MkdirAll(filepath.Join(memoryRoot, "rollout_summaries"), 0o700); err != nil {
		t.Fatalf("MkdirAll memory root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "MEMORY.md"), []byte("stale memory"), 0o600); err != nil {
		t.Fatalf("WriteFile memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "rollout_summaries", "old.md"), []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile rollout: %v", err)
	}
	memorySvc, err := NewMemoryService(memoryRoot)
	if err != nil {
		t.Fatalf("NewMemoryService: %v", err)
	}
	server := readyServer(WithStore(st), WithMemoryService(memorySvc))

	resetResp := server.HandleRequest(ctx, request("memory/reset", nil))
	if resetResp.Error != nil {
		t.Fatalf("memory/reset error: %v", resetResp.Error)
	}
	var reset MemoryResetResponse
	decodeResult(t, resetResp, &reset)
	entries, err := os.ReadDir(memoryRoot)
	if err != nil {
		t.Fatalf("ReadDir memory root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("memory root entries after reset = %#v", entries)
	}
	loaded, err := st.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if loaded.Settings[threadMemoryModeSettingKey] != "enabled" {
		t.Fatalf("memory mode after reset = %#v", loaded.Settings)
	}
}

func TestServerCatalogHandlers(t *testing.T) {
	ctx := context.Background()
	catalogSvc := catalog.NewDefault(catalog.WithEnvLookup(func(key string) (string, bool) {
		if key == "OPENAI_API_KEY" {
			return "set", true
		}
		return "", false
	}))
	fsSvc, err := toolfs.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	memorySvc, err := NewMemoryService(filepath.Join(t.TempDir(), "memories"))
	if err != nil {
		t.Fatalf("NewMemoryService: %v", err)
	}
	server := readyServer(WithCatalog(catalogSvc), WithFilesystem(fsSvc), WithMemoryService(memorySvc))

	providersResp := server.HandleRequest(ctx, request("provider/list", nil))
	if providersResp.Error != nil {
		t.Fatalf("provider/list error: %v", providersResp.Error)
	}
	var providers catalog.ProviderListResponse
	decodeResult(t, providersResp, &providers)
	if len(providers.Data) == 0 || !providerConfigured(providers.Data, catalog.ProviderOpenAI) {
		t.Fatalf("provider/list = %#v", providers.Data)
	}

	modelsResp := server.HandleRequest(ctx, request("model/list", map[string]any{
		"providerId": catalog.ProviderOpenAI,
		"limit":      2,
	}))
	if modelsResp.Error != nil {
		t.Fatalf("model/list error: %v", modelsResp.Error)
	}
	var models catalog.ModelListResponse
	decodeResult(t, modelsResp, &models)
	if len(models.Data) != 2 || models.NextCursor == nil {
		t.Fatalf("model/list = %#v", models)
	}
	if models.Data[0].ProviderID != catalog.ProviderOpenAI || !models.Data[0].IsDefault {
		t.Fatalf("model/list first model = %#v", models.Data[0])
	}

	codexCapsResp := server.HandleRequest(ctx, request("modelProvider/capabilities/read", map[string]any{
		"providerId": catalog.ProviderOpenAI,
	}))
	if codexCapsResp.Error != nil {
		t.Fatalf("modelProvider/capabilities/read error: %v", codexCapsResp.Error)
	}
	var codexCaps catalog.ProviderCapabilities
	decodeResult(t, codexCapsResp, &codexCaps)
	if !codexCaps.NamespaceTools || !codexCaps.ToolCalls || !codexCaps.Configured {
		t.Fatalf("codex capabilities = %#v", codexCaps)
	}

	aggregateCapsResp := server.HandleRequest(ctx, request("modelProvider/capabilities/read", map[string]any{}))
	if aggregateCapsResp.Error != nil {
		t.Fatalf("aggregate modelProvider/capabilities/read error: %v", aggregateCapsResp.Error)
	}
	var aggregateCaps catalog.ProviderCapabilities
	decodeResult(t, aggregateCapsResp, &aggregateCaps)
	if aggregateCaps.ProviderID != "" || !aggregateCaps.NamespaceTools ||
		!aggregateCaps.ToolCalls || !aggregateCaps.Configured || len(aggregateCaps.ReasoningEfforts) == 0 {
		t.Fatalf("aggregate capabilities = %#v", aggregateCaps)
	}

	aliasResp := server.HandleRequest(ctx, request("provider/capabilities/read", map[string]any{
		"provider": catalog.ProviderAnthropic,
	}))
	if aliasResp.Error != nil {
		t.Fatalf("provider/capabilities/read error: %v", aliasResp.Error)
	}
	var aliasCaps catalog.ProviderCapabilities
	decodeResult(t, aliasResp, &aliasCaps)
	if !aliasCaps.AdaptiveThinking || !aliasCaps.ManualThinking {
		t.Fatalf("anthropic capabilities = %#v", aliasCaps)
	}

	toolsResp := server.HandleRequest(ctx, request("tool/list", map[string]any{"includeUnavailable": true}))
	if toolsResp.Error != nil {
		t.Fatalf("tool/list error: %v", toolsResp.Error)
	}
	var tools catalog.ToolListResponse
	decodeResult(t, toolsResp, &tools)
	if !toolAvailable(tools.Data, "fs") {
		t.Fatalf("tool/list did not report filesystem available: %#v", tools.Data)
	}
	if toolAvailable(tools.Data, "git") {
		t.Fatalf("tool/list reported git available without git service: %#v", tools.Data)
	}
	if !toolAvailable(tools.Data, "cache") {
		t.Fatalf("tool/list did not report cache available: %#v", tools.Data)
	}
	if !toolAvailable(tools.Data, "config") {
		t.Fatalf("tool/list did not report config available: %#v", tools.Data)
	}
	if !toolAvailable(tools.Data, "memory") {
		t.Fatalf("tool/list did not report memory available: %#v", tools.Data)
	}
}

func TestServerProviderHealthProbeIsTypedAndCredentialFree(t *testing.T) {
	const token = "server-local-probe-secret"
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("probe request = %s %s, want GET /v1/models", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q, want local token", got)
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer endpoint.Close()
	env := map[string]string{
		"GOLLEM_LOCAL_OPENAI_BASE_URL": endpoint.URL,
		"GOLLEM_LOCAL_OPENAI_MODEL":    "local-tool-model",
		"GOLLEM_LOCAL_OPENAI_API_KEY":  token,
	}
	catalogSvc := catalog.NewDefault(catalog.WithEnvLookup(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}))
	server := readyServer(WithCatalog(catalogSvc))

	resp := server.HandleRequest(context.Background(), request("provider/health/probe", map[string]any{
		"providerId": catalog.ProviderOpenAICompatibleLocal,
	}))
	if resp.Error != nil {
		t.Fatalf("provider/health/probe error: %v", resp.Error)
	}
	var probe catalog.ProviderHealthProbeResponse
	decodeResult(t, resp, &probe)
	if probe.ProviderID != catalog.ProviderOpenAICompatibleLocal || probe.Status != catalog.ProviderHealthAvailable {
		t.Fatalf("provider/health/probe = %#v", probe)
	}
	for _, secret := range []string{endpoint.URL, token} {
		if strings.Contains(string(resp.Result), secret) {
			t.Fatalf("provider/health/probe leaked %q: %s", secret, resp.Result)
		}
	}

	unknown := server.HandleRequest(context.Background(), request("provider/health/probe", map[string]any{"providerId": "unknown"}))
	if unknown.Error == nil || unknown.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("unknown provider probe error = %#v, want invalid params", unknown.Error)
	}
}

func TestServerConfigHandlers(t *testing.T) {
	ctx := context.Background()
	configSvc := appconfig.NewService(
		appconfig.WithWorkDir("/tmp/work"),
		appconfig.WithEnvLookup(func(key string) (string, bool) {
			switch key {
			case "ANTHROPIC_API_KEY":
				return "secret", true
			case "SHELL":
				return "/bin/zsh", true
			case "HOME":
				return "/Users/example", true
			default:
				return "", false
			}
		}),
	)
	server := readyServer(WithConfig(configSvc))

	writeResp := server.HandleRequest(ctx, request("config/value/write", map[string]any{
		"key":   "api.token",
		"value": "secret-value",
	}))
	if writeResp.Error != nil {
		t.Fatalf("config/value/write error: %v", writeResp.Error)
	}
	readResp := server.HandleRequest(ctx, request("config/read", nil))
	if readResp.Error != nil {
		t.Fatalf("config/read error: %v", readResp.Error)
	}
	var read appconfig.ReadResponse
	decodeResult(t, readResp, &read)
	token := configEntry(t, read.Entries, "api.token")
	if !token.Redacted || string(token.Value) != "null" || string(read.Values["api.token"]) != "null" {
		t.Fatalf("secret config leaked: entry=%#v value=%s", token, read.Values["api.token"])
	}

	batchResp := server.HandleRequest(ctx, request("config/batchWrite", map[string]any{
		"values": map[string]any{"provider.default": "anthropic"},
		"entries": []map[string]any{{
			"key":   "custom.flag",
			"value": true,
		}},
	}))
	if batchResp.Error != nil {
		t.Fatalf("config/batchWrite error: %v", batchResp.Error)
	}
	var batch appconfig.WriteResponse
	decodeResult(t, batchResp, &batch)
	if len(batch.Entries) != 2 || string(batch.Values["custom.flag"]) != "true" {
		t.Fatalf("batch write = %#v", batch)
	}

	requirementsResp := server.HandleRequest(ctx, request("configRequirements/read", nil))
	if requirementsResp.Error != nil {
		t.Fatalf("configRequirements/read error: %v", requirementsResp.Error)
	}
	var requirements appconfig.RequirementsResponse
	decodeResult(t, requirementsResp, &requirements)
	if !configRequirementSatisfied(requirements.Requirements, "anthropic.apiKey") {
		t.Fatalf("requirements did not reflect env status: %#v", requirements.Requirements)
	}

	addResp := server.HandleRequest(ctx, request("environment/add", map[string]any{
		"id":      "staging",
		"name":    "Staging",
		"workDir": "/tmp/staging",
		"variables": map[string]any{
			"OPENAI_API_KEY": "not-returned",
		},
	}))
	if addResp.Error != nil {
		t.Fatalf("environment/add error: %v", addResp.Error)
	}
	var added appconfig.EnvironmentResponse
	decodeResult(t, addResp, &added)
	if added.Environment.ID != "staging" || len(added.Environment.Variables) != 1 || !added.Environment.Variables[0].Redacted {
		t.Fatalf("added environment = %#v", added.Environment)
	}

	envResp := server.HandleRequest(ctx, request("environment/info", nil))
	if envResp.Error != nil {
		t.Fatalf("environment/info error: %v", envResp.Error)
	}
	var envInfo appconfig.EnvironmentInfoResponse
	decodeResult(t, envResp, &envInfo)
	if envInfo.CurrentID != "current" || len(envInfo.Environments) != 2 {
		t.Fatalf("environment/info = %#v", envInfo)
	}

	for _, method := range []string{"collaborationMode/list", "permissionProfile/list", "experimentalFeature/list"} {
		resp := server.HandleRequest(ctx, request(method, nil))
		if resp.Error != nil {
			t.Fatalf("%s error: %v", method, resp.Error)
		}
	}
	setResp := server.HandleRequest(ctx, request("experimentalFeature/enablement/set", map[string]any{
		"id":      "websocket-transport",
		"enabled": false,
	}))
	if setResp.Error != nil {
		t.Fatalf("experimentalFeature/enablement/set error: %v", setResp.Error)
	}
	var set appconfig.ExperimentalFeatureSetResponse
	decodeResult(t, setResp, &set)
	if set.Feature.Enabled {
		t.Fatalf("feature was not disabled: %#v", set.Feature)
	}

	reloadResp := server.HandleRequest(ctx, request("config/mcpServer/reload", nil))
	if reloadResp.Error != nil {
		t.Fatalf("config/mcpServer/reload error: %v", reloadResp.Error)
	}
	var reload appconfig.MCPReloadResponse
	decodeResult(t, reloadResp, &reload)
	if reload.Reloaded || reload.Status != "no-op" {
		t.Fatalf("reload = %#v", reload)
	}
}

func TestServerMCPHandlers(t *testing.T) {
	ctx := context.Background()
	mcpSvc := appmcp.NewService()
	src := &serverTestMCPSource{
		tools: []extmcp.Tool{{
			Name:        "echo",
			Description: "Echo text",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		toolResults: map[string]*extmcp.ToolResult{
			"echo": {
				Content: []extmcp.Content{{Type: "text", Text: "pong"}},
			},
		},
		resources: []extmcp.Resource{{
			URI:  "file:///workspace/README.md",
			Name: "README",
		}},
		resourceTemplates: []extmcp.ResourceTemplate{{
			URITemplate: "file:///workspace/{path}",
			Name:        "workspace_file",
		}},
		resourceResults: map[string]*extmcp.ReadResourceResult{
			"file:///workspace/README.md": {
				Contents: []extmcp.ResourceContents{{
					URI:  "file:///workspace/README.md",
					Text: "# Gollem\n",
				}},
			},
		},
	}
	if err := mcpSvc.AddServer("repo", src); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	server := readyServer(WithMCP(mcpSvc))

	statusResp := server.HandleRequest(ctx, request("mcpServerStatus/list", nil))
	if statusResp.Error != nil {
		t.Fatalf("mcpServerStatus/list error: %v", statusResp.Error)
	}
	var statuses appmcp.StatusListResponse
	decodeResult(t, statusResp, &statuses)
	if len(statuses.Servers) != 1 || statuses.Servers[0].ToolCount != 1 || !statuses.Servers[0].Capabilities.Resources {
		t.Fatalf("mcp statuses = %#v", statuses)
	}

	readResp := server.HandleRequest(ctx, request("mcpServer/resource/read", map[string]any{
		"serverName": "repo",
		"uri":        "file:///workspace/README.md",
	}))
	if readResp.Error != nil {
		t.Fatalf("mcpServer/resource/read error: %v", readResp.Error)
	}
	var resource appmcp.ResourceReadResponse
	decodeResult(t, readResp, &resource)
	if resource.Text != "# Gollem\n" {
		t.Fatalf("resource = %#v", resource)
	}

	callRespCh := make(chan protocol.Response, 1)
	go func() {
		callRespCh <- server.HandleRequest(ctx, request("mcpServer/tool/call", map[string]any{
			"name":      "repo__echo",
			"arguments": map[string]any{"text": "ping"},
		}))
	}()
	select {
	case <-server.RequestSignal():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for MCP tool approval request")
	}
	approvalRequests := server.DrainRequests()
	if len(approvalRequests) != 1 || approvalRequests[0].Method != "item/permissions/requestApproval" {
		t.Fatalf("approval requests = %#v", approvalRequests)
	}
	var approval permissionsApprovalParams
	if err := json.Unmarshal(approvalRequests[0].Params, &approval); err != nil {
		t.Fatalf("decode approval request: %v", err)
	}
	if approval.Permissions["kind"] != "mcp" || approval.Permissions["server"] != "repo" || approval.Permissions["tool"] != "echo" {
		t.Fatalf("approval params = %#v", approval)
	}
	if _, ok := approval.Permissions["argumentKeys"]; !ok {
		t.Fatalf("approval params missing redacted argument keys: %#v", approval.Permissions)
	}
	requestID, _ := approvalRequests[0].ID.Value().(string)
	approveResp := server.HandleRequest(ctx, request("approval/respond", map[string]any{
		"requestId": requestID,
		"approved":  true,
	}))
	if approveResp.Error != nil {
		t.Fatalf("approval/respond error: %v", approveResp.Error)
	}
	var callResp protocol.Response
	select {
	case callResp = <-callRespCh:
	case <-time.After(2 * time.Second):
		t.Fatal("MCP tool call did not finish after approval")
	}
	if callResp.Error != nil {
		t.Fatalf("mcpServer/tool/call error: %v", callResp.Error)
	}
	var tool appmcp.ToolCallResponse
	decodeResult(t, callResp, &tool)
	if tool.ServerName != "repo" || tool.ToolName != "echo" || tool.Text != "pong" {
		t.Fatalf("tool call = %#v", tool)
	}
	if src.callCount != 1 {
		t.Fatalf("source call count = %d, want 1", src.callCount)
	}

	deniedRespCh := make(chan protocol.Response, 1)
	go func() {
		deniedRespCh <- server.HandleRequest(ctx, request("mcpServer/tool/call", map[string]any{
			"serverName": "repo",
			"toolName":   "echo",
		}))
	}()
	select {
	case <-server.RequestSignal():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for denied MCP tool approval request")
	}
	approvalRequests = server.DrainRequests()
	if len(approvalRequests) != 1 || approvalRequests[0].Method != "item/permissions/requestApproval" {
		t.Fatalf("denied approval requests = %#v", approvalRequests)
	}
	requestID, _ = approvalRequests[0].ID.Value().(string)
	denyResp := server.HandleRequest(ctx, request("approval/respond", map[string]any{
		"requestId": requestID,
		"approved":  false,
		"message":   "not allowed",
	}))
	if denyResp.Error != nil {
		t.Fatalf("approval/respond deny error: %v", denyResp.Error)
	}
	var deniedResp protocol.Response
	select {
	case deniedResp = <-deniedRespCh:
	case <-time.After(2 * time.Second):
		t.Fatal("denied MCP tool call did not finish")
	}
	if deniedResp.Error == nil || deniedResp.Error.Code != protocol.CodeInvalidRequest {
		t.Fatalf("denied response = %#v, want invalid request", deniedResp)
	}
	if src.callCount != 1 {
		t.Fatalf("denied call reached source, call count = %d", src.callCount)
	}

	reloadResp := server.HandleRequest(ctx, request("config/mcpServer/reload", nil))
	if reloadResp.Error != nil {
		t.Fatalf("config/mcpServer/reload error: %v", reloadResp.Error)
	}
	var reload appconfig.MCPReloadResponse
	decodeResult(t, reloadResp, &reload)
	if !reload.Reloaded || reload.Status != "reloaded" {
		t.Fatalf("reload = %#v", reload)
	}

	oauthResp := server.HandleRequest(ctx, request("mcpServer/oauth/login", map[string]any{"serverName": "repo"}))
	if oauthResp.Error == nil || oauthResp.Error.Code != protocol.CodeMethodUnavailable {
		t.Fatalf("oauth response = %#v, want method unavailable", oauthResp)
	}
}

func TestServerSkillsPluginHandlers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeAppServerTestFile(t, root, "standalone/SKILL.md", "# Standalone\n\nStandalone skill description.\n")
	writeAppServerTestFile(t, root, "plugins/example/.codex-plugin/plugin.json", `{"id":"example","name":"Example Plugin","description":"Example plugin","version":"1.2.3"}`)
	writeAppServerTestFile(t, root, "plugins/example/skills/review/SKILL.md", "---\nname: Review Skill\ndescription: Review code carefully.\n---\n")
	server := readyServer(WithSkills(appskills.NewService(appskills.WithRoot(root))))

	skillsResp := server.HandleRequest(ctx, request("skills/list", nil))
	if skillsResp.Error != nil {
		t.Fatalf("skills/list error: %v", skillsResp.Error)
	}
	var skills appskills.ListResponse
	decodeResult(t, skillsResp, &skills)
	if len(skills.Skills) != 2 {
		t.Fatalf("skills/list = %#v", skills)
	}
	review := appServerTestSkillByName(t, skills.Skills, "Review Skill")
	if review.PluginID != "example" || review.Description != "Review code carefully." {
		t.Fatalf("review skill = %#v", review)
	}

	pluginsResp := server.HandleRequest(ctx, request("plugin/list", map[string]any{"includeSkills": true}))
	if pluginsResp.Error != nil {
		t.Fatalf("plugin/list error: %v", pluginsResp.Error)
	}
	var plugins appskills.PluginListResponse
	decodeResult(t, pluginsResp, &plugins)
	if len(plugins.Plugins) != 1 || plugins.Plugins[0].ID != "example" || plugins.Plugins[0].SkillCount != 1 {
		t.Fatalf("plugin/list = %#v", plugins)
	}
	installedResp := server.HandleRequest(ctx, request("plugin/installed", map[string]any{"includeSkills": true}))
	if installedResp.Error != nil {
		t.Fatalf("plugin/installed error: %v", installedResp.Error)
	}

	pluginReadResp := server.HandleRequest(ctx, request("plugin/read", map[string]any{"pluginId": "example"}))
	if pluginReadResp.Error != nil {
		t.Fatalf("plugin/read error: %v", pluginReadResp.Error)
	}
	var plugin appskills.PluginReadResponse
	decodeResult(t, pluginReadResp, &plugin)
	if plugin.Plugin.Version != "1.2.3" || plugin.Manifest["name"] != "Example Plugin" {
		t.Fatalf("plugin/read = %#v", plugin)
	}

	skillReadResp := server.HandleRequest(ctx, request("plugin/skill/read", map[string]any{"pluginId": "example", "skillId": review.ID}))
	if skillReadResp.Error != nil {
		t.Fatalf("plugin/skill/read error: %v", skillReadResp.Error)
	}
	var skill appskills.PluginSkillReadResponse
	decodeResult(t, skillReadResp, &skill)
	if skill.Skill.ID != review.ID || !strings.Contains(skill.Content, "Review code carefully") {
		t.Fatalf("plugin/skill/read = %#v", skill)
	}

	missingResp := server.HandleRequest(ctx, request("plugin/read", map[string]any{"pluginId": "missing"}))
	if missingResp.Error == nil || missingResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("missing plugin response = %#v, want invalid params", missingResp)
	}
	installResp := server.HandleRequest(ctx, request("plugin/install", map[string]any{"id": "example"}))
	if installResp.Error == nil || installResp.Error.Code != protocol.CodeMethodUnavailable {
		t.Fatalf("plugin/install response = %#v, want unavailable", installResp)
	}
}

func TestServerCacheHandlers(t *testing.T) {
	ctx := context.Background()
	server := readyServer()

	statsResp := server.HandleRequest(ctx, request("cache/stats", nil))
	if statsResp.Error != nil {
		t.Fatalf("cache/stats error: %v", statsResp.Error)
	}
	var initial protocol.CacheStatsResponse
	decodeResult(t, statsResp, &initial)
	if initial.TotalRequests != 0 || initial.Providers == nil {
		t.Fatalf("initial cache stats = %#v", initial)
	}

	benchmarkResp := server.HandleRequest(ctx, request("cache/benchmark", map[string]any{
		"includeEvents": true,
	}))
	if benchmarkResp.Error != nil {
		t.Fatalf("cache/benchmark error: %v", benchmarkResp.Error)
	}
	var benchmark appcache.BenchmarkResponse
	decodeResult(t, benchmarkResp, &benchmark)
	if !benchmark.Passed {
		t.Fatalf("cache benchmark failed: %#v", benchmark)
	}
	if len(benchmark.Providers) != 2 {
		t.Fatalf("cache benchmark providers = %#v", benchmark.Providers)
	}
	for _, provider := range benchmark.Providers {
		if provider.HitRate < 0.90 {
			t.Fatalf("%s hit rate = %f, want >= .90", provider.Provider, provider.HitRate)
		}
	}
	if len(benchmark.Events) == 0 {
		t.Fatal("cache benchmark did not return typed events")
	}
	cacheEvents := server.DrainNotifications()
	assertNotificationMethods(t, cacheEvents, "cache/benchmark/completed")
	var completedNotice cacheBenchmarkNotificationParams
	if err := json.Unmarshal(cacheEvents[0].Params, &completedNotice); err != nil {
		t.Fatalf("decode cache benchmark notice: %v", err)
	}
	if !completedNotice.Passed || completedNotice.Totals.TotalRequests != benchmark.Totals.TotalRequests {
		t.Fatalf("cache benchmark notice = %#v, benchmark = %#v", completedNotice, benchmark.Totals)
	}

	afterResp := server.HandleRequest(ctx, request("cache/stats", nil))
	if afterResp.Error != nil {
		t.Fatalf("cache/stats after benchmark error: %v", afterResp.Error)
	}
	var after protocol.CacheStatsResponse
	decodeResult(t, afterResp, &after)
	if after.TotalRequests != benchmark.Totals.TotalRequests || after.HitRate < 0.90 {
		t.Fatalf("cache stats after benchmark = %#v, benchmark = %#v", after, benchmark.Totals)
	}
	if len(after.RecentEvents) != len(benchmark.Events) {
		t.Fatalf("cache stats events = %#v, benchmark events = %#v", after.RecentEvents, benchmark.Events)
	}
	if got, want := after.RecentEvents[0].Key, benchmark.Events[0].Key; got != want {
		t.Fatalf("cache stats event key = %q, want %q", got, want)
	}
	if got, want := after.RecentEvents[0].Type, protocol.CacheEventType(benchmark.Events[0].Type); got != want {
		t.Fatalf("cache stats event type = %q, want %q", got, want)
	}
}

func TestServerDaemonLifecycleHandlers(t *testing.T) {
	ctx := context.Background()
	daemon := NewDaemonService(
		WithDaemonVersion("test-version"),
		WithDaemonTransport("stdio"),
		WithDaemonWorkDir("/tmp/work"),
		WithDaemonStorePath(":memory:"),
	)
	server := readyServer(WithDaemonService(daemon))

	statusResp := server.HandleRequest(ctx, request("daemon/status", nil))
	if statusResp.Error != nil {
		t.Fatalf("daemon/status error: %v", statusResp.Error)
	}
	var status DaemonStatus
	decodeResult(t, statusResp, &status)
	if status.Status != daemonStatusRunning || status.Version != "test-version" || status.ProtocolVersion != protocol.ProtocolVersion || status.WorkDir != "/tmp/work" {
		t.Fatalf("daemon/status = %#v", status)
	}

	versionResp := server.HandleRequest(ctx, request("daemon/version", nil))
	if versionResp.Error != nil {
		t.Fatalf("daemon/version error: %v", versionResp.Error)
	}
	var version DaemonVersion
	decodeResult(t, versionResp, &version)
	if version.Version != "test-version" || version.ProtocolVersion != protocol.ProtocolVersion || version.GoVersion == "" {
		t.Fatalf("daemon/version = %#v", version)
	}

	startResp := server.HandleRequest(ctx, request("daemon/start", nil))
	if startResp.Error != nil {
		t.Fatalf("daemon/start error: %v", startResp.Error)
	}
	var start DaemonStartResult
	decodeResult(t, startResp, &start)
	if !start.OK || !start.AlreadyRunning || start.Status.Status != daemonStatusRunning {
		t.Fatalf("daemon/start = %#v", start)
	}

	stopResp := server.HandleRequest(ctx, request("daemon/stop", map[string]any{"reason": "test stop"}))
	if stopResp.Error != nil {
		t.Fatalf("daemon/stop error: %v", stopResp.Error)
	}
	var stop DaemonStopResult
	decodeResult(t, stopResp, &stop)
	if !stop.OK || !stop.Stopping || stop.Restart || !daemon.ShutdownRequested() || stop.Status.Status != "stopping" {
		t.Fatalf("daemon/stop = %#v", stop)
	}

	restartDaemon := NewDaemonService()
	restartServer := readyServer(WithDaemonService(restartDaemon))
	restartResp := restartServer.HandleRequest(ctx, request("daemon/restart", map[string]any{"reason": "test restart"}))
	if restartResp.Error != nil {
		t.Fatalf("daemon/restart error: %v", restartResp.Error)
	}
	var restart DaemonStopResult
	decodeResult(t, restartResp, &restart)
	if !restart.OK || !restart.Stopping || !restart.Restart || !restartDaemon.ShutdownRequested() || !restart.Status.RestartRequested {
		t.Fatalf("daemon/restart = %#v", restart)
	}
}

func TestServerFilesystemHandlers(t *testing.T) {
	ctx := context.Background()
	fsSvc, err := toolfs.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithFilesystem(fsSvc))

	writeResp := server.HandleRequest(ctx, request("fs/writeFile", map[string]any{
		"path":    "nested/hello.txt",
		"content": "hello",
	}))
	if writeResp.Error != nil {
		t.Fatalf("fs/writeFile error: %v", writeResp.Error)
	}
	fsEvents := server.DrainNotifications()
	assertNotificationMethods(t, fsEvents, "fs/changed")
	var changedNotice fileChangedParams
	if err := json.Unmarshal(fsEvents[0].Params, &changedNotice); err != nil {
		t.Fatalf("decode fs changed notice: %v", err)
	}
	if changedNotice.Path != "nested/hello.txt" || changedNotice.Operation != "writeFile" {
		t.Fatalf("fs changed notice = %#v", changedNotice)
	}

	readResp := server.HandleRequest(ctx, request("fs/readFile", map[string]any{"path": "nested/hello.txt"}))
	if readResp.Error != nil {
		t.Fatalf("fs/readFile error: %v", readResp.Error)
	}
	var read fileContentResult
	decodeResult(t, readResp, &read)
	if read.Content != "hello" || read.Encoding != "utf-8" || read.Path != "nested/hello.txt" {
		t.Fatalf("fs/readFile = %#v", read)
	}

	copyResp := server.HandleRequest(ctx, request("fs/copy", map[string]any{
		"source":      "nested/hello.txt",
		"destination": "copy.txt",
	}))
	if copyResp.Error != nil {
		t.Fatalf("fs/copy error: %v", copyResp.Error)
	}
	fsEvents = server.DrainNotifications()
	assertNotificationMethods(t, fsEvents, "fs/changed")
	if err := json.Unmarshal(fsEvents[0].Params, &changedNotice); err != nil {
		t.Fatalf("decode fs copy changed notice: %v", err)
	}
	if changedNotice.Path != "nested/hello.txt" || changedNotice.Destination != "copy.txt" || changedNotice.Operation != "copy" {
		t.Fatalf("fs copy changed notice = %#v", changedNotice)
	}
	listResp := server.HandleRequest(ctx, request("fs/readDirectory", map[string]any{"path": "."}))
	if listResp.Error != nil {
		t.Fatalf("fs/readDirectory error: %v", listResp.Error)
	}
}

func TestServerFilesystemWatchHandlers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fsSvc, err := toolfs.NewService(root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer fsSvc.Close()
	server := readyServer(WithFilesystem(fsSvc))
	watchPath := filepath.Join(root, "watched.txt")

	watchResp := server.HandleRequest(ctx, request("fs/watch", map[string]any{
		"watchId":            "watch-file",
		"path":               watchPath,
		"pollIntervalMillis": 20,
	}))
	var watch fsWatchResult
	decodeResult(t, watchResp, &watch)
	if watch.Path != watchPath {
		t.Fatalf("fs/watch path = %q, want %q", watch.Path, watchPath)
	}

	relativeResp := server.HandleRequest(ctx, request("fs/watch", map[string]any{
		"watchId": "relative",
		"path":    "relative.txt",
	}))
	if relativeResp.Error == nil || relativeResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("relative fs/watch response = %#v", relativeResp)
	}

	if err := os.WriteFile(watchPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write watched file: %v", err)
	}
	notification := waitForNotification(t, server, "fs/changed")
	var changed fsWatchChangedParams
	if err := json.Unmarshal(notification.Params, &changed); err != nil {
		t.Fatalf("decode watch changed notification: %v", err)
	}
	if changed.WatchID != "watch-file" || !containsString(changed.ChangedPaths, watchPath) {
		t.Fatalf("watch changed notification = %#v", changed)
	}

	unwatchResp := server.HandleRequest(ctx, request("fs/unwatch", map[string]any{"watchId": "watch-file"}))
	if unwatchResp.Error != nil {
		t.Fatalf("fs/unwatch error: %v", unwatchResp.Error)
	}
	if err := os.WriteFile(watchPath, []byte("again"), 0o644); err != nil {
		t.Fatalf("write unwatched file: %v", err)
	}
	assertNoNotification(t, server, "fs/changed")
}

func TestServerFilesystemApprovalRespondFlow(t *testing.T) {
	ctx := context.Background()
	approvals := NewApprovalService()
	fsSvc, err := toolfs.NewService(t.TempDir(), toolfs.WithApproval(approvals.FilesystemApproval))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithFilesystem(fsSvc), WithApprovalService(approvals))

	respCh := make(chan protocol.Response, 1)
	go func() {
		respCh <- server.HandleRequest(ctx, request("fs/writeFile", map[string]any{
			"path":    "approved.txt",
			"content": "ok",
		}))
	}()

	approvalReq := waitForServerRequest(t, server)
	if approvalReq.Method != "item/fileChange/requestApproval" {
		t.Fatalf("approval method = %q", approvalReq.Method)
	}
	requestID, _ := approvalReq.ID.Value().(string)
	if requestID == "" {
		t.Fatalf("approval request id = %#v", approvalReq.ID.Value())
	}
	approvalResp := server.HandleRequest(ctx, request("approval/respond", map[string]any{
		"requestId": requestID,
		"approved":  true,
	}))
	if approvalResp.Error != nil {
		t.Fatalf("approval/respond error: %v", approvalResp.Error)
	}

	select {
	case writeResp := <-respCh:
		if writeResp.Error != nil {
			t.Fatalf("fs/writeFile after approval error: %v", writeResp.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fs/writeFile did not finish after approval")
	}

	waitForNotificationSet(t, server, "serverRequest/resolved", "fs/changed")
}

func TestServerProcessHandlers(t *testing.T) {
	ctx := context.Background()
	processSvc, err := toolprocess.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithProcess(processSvc))

	startResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{
		"command": "cat",
	}))
	if startResp.Error != nil {
		t.Fatalf("process/spawn error: %v", startResp.Error)
	}
	var started struct {
		Process processSnapshotResult `json:"process"`
	}
	decodeResult(t, startResp, &started)
	if started.Process.ID == "" {
		t.Fatalf("process/spawn result = %#v", started.Process)
	}

	writeResp := server.HandleRequest(ctx, request("process/writeStdin", map[string]any{
		"id":    started.Process.ID,
		"data":  "hello\n",
		"close": true,
	}))
	if writeResp.Error != nil {
		t.Fatalf("process/writeStdin error: %v", writeResp.Error)
	}
	snapshot, err := processSvc.Wait(ctx, started.Process.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if string(snapshot.Stdout) != "hello\n" {
		t.Fatalf("stdout = %q", snapshot.Stdout)
	}

	resizeResp := server.HandleRequest(ctx, request("process/resizePty", map[string]any{
		"id":   started.Process.ID,
		"cols": 80,
		"rows": 24,
	}))
	if resizeResp.Error == nil || resizeResp.Error.Code != protocol.CodeMethodUnavailable {
		t.Fatalf("resize error = %#v, want unavailable", resizeResp.Error)
	}
}

func TestServerProcessPTYHandlers(t *testing.T) {
	ctx := context.Background()
	processSvc, err := toolprocess.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithProcess(processSvc))
	probe, err := processSvc.Start(ctx, toolprocess.StartRequest{Command: "true", Shell: true, PTY: true})
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.ENOSYS) {
		t.Skipf("PTY allocation is unavailable in this environment: %v", err)
	}
	if err != nil {
		t.Fatalf("start PTY probe: %v", err)
	}
	if _, err := waitProcessSnapshot(t, processSvc, probe.ID); err != nil {
		t.Fatalf("wait PTY probe: %v", err)
	}

	startResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{
		"command": "cat",
		"pty":     true,
		"ptySize": map[string]any{"rows": 24, "cols": 80},
	}))
	if startResp.Error != nil {
		t.Fatalf("process/spawn PTY error: %v", startResp.Error)
	}
	var started struct {
		Process processSnapshotResult `json:"process"`
	}
	decodeResult(t, startResp, &started)
	if !started.Process.PTY || started.Process.PTYSize == nil ||
		*started.Process.PTYSize != (toolprocess.TerminalSize{Rows: 24, Cols: 80}) {
		t.Fatalf("process/spawn PTY result = %#v", started.Process)
	}

	resizeResp := server.HandleRequest(ctx, request("process/resizePty", map[string]any{
		"id": started.Process.ID, "cols": 120, "rows": 40,
	}))
	if resizeResp.Error != nil {
		t.Fatalf("process/resizePty error: %v", resizeResp.Error)
	}
	writeResp := server.HandleRequest(ctx, request("process/writeStdin", map[string]any{
		"id": started.Process.ID, "data": "hello\n",
	}))
	if writeResp.Error != nil {
		t.Fatalf("process/writeStdin PTY error: %v", writeResp.Error)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot, err := processSvc.Snapshot(ctx, started.Process.ID)
		if err != nil {
			t.Fatalf("snapshot after PTY write: %v", err)
		}
		if strings.Contains(string(snapshot.Stdout), "hello") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("PTY process did not echo input before kill; stdout = %q", snapshot.Stdout)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := processSvc.Kill(ctx, started.Process.ID); err != nil {
		t.Fatalf("Kill PTY process: %v", err)
	}
	completed, err := waitProcessSnapshot(t, processSvc, started.Process.ID)
	if err != nil {
		t.Fatalf("wait PTY process: %v", err)
	}
	if !completed.PTY || completed.PTYSize != (toolprocess.TerminalSize{Rows: 40, Cols: 120}) ||
		!strings.Contains(string(completed.Stdout), "hello") || len(completed.Stderr) != 0 {
		t.Fatalf("completed PTY process = %#v", completed)
	}

	nonPTYResizeResp := server.HandleRequest(ctx, request("process/resizePty", map[string]any{
		"id": "missing", "cols": 0, "rows": 24,
	}))
	if nonPTYResizeResp.Error == nil || nonPTYResizeResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("invalid PTY resize response = %#v", nonPTYResizeResp)
	}
}

func TestProcessSnapshotResultPTYWireShape(t *testing.T) {
	nonPTY, err := json.Marshal(processSnapshotResultFrom(&toolprocess.Snapshot{ID: "pipe"}))
	if err != nil {
		t.Fatalf("marshal non-PTY snapshot: %v", err)
	}
	var nonPTYFields map[string]json.RawMessage
	if err := json.Unmarshal(nonPTY, &nonPTYFields); err != nil {
		t.Fatalf("unmarshal non-PTY snapshot: %v", err)
	}
	if _, ok := nonPTYFields["ptySize"]; ok {
		t.Fatalf("non-PTY snapshot leaked ptySize: %s", nonPTY)
	}

	ptySnapshot, err := json.Marshal(processSnapshotResultFrom(&toolprocess.Snapshot{
		ID: "terminal", PTY: true, PTYSize: toolprocess.TerminalSize{Rows: 24, Cols: 80},
	}))
	if err != nil {
		t.Fatalf("marshal PTY snapshot: %v", err)
	}
	var ptyFields struct {
		PTY     bool                     `json:"pty"`
		PTYSize toolprocess.TerminalSize `json:"ptySize"`
	}
	if err := json.Unmarshal(ptySnapshot, &ptyFields); err != nil {
		t.Fatalf("unmarshal PTY snapshot: %v", err)
	}
	if !ptyFields.PTY || ptyFields.PTYSize != (toolprocess.TerminalSize{Rows: 24, Cols: 80}) {
		t.Fatalf("PTY snapshot wire result = %s", ptySnapshot)
	}
}

func TestServerCommandExecPublishesCommandOutputDelta(t *testing.T) {
	ctx := context.Background()
	var server *Server
	processSvc, err := toolprocess.NewService(t.TempDir(),
		toolprocess.WithOutputSink(func(ev toolprocess.OutputEvent) {
			server.PublishProcessOutput(ev)
		}),
		toolprocess.WithExitSink(func(ev toolprocess.ExitEvent) {
			server.PublishProcessExited(ev)
		}),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server = readyServer(WithProcess(processSvc))

	startResp := server.HandleRequest(ctx, request("command/exec", map[string]any{
		"processId": "client-command-1",
		"command":   "cat",
	}))
	if startResp.Error != nil {
		t.Fatalf("command/exec start error: %v", startResp.Error)
	}
	var started struct {
		Process processSnapshotResult `json:"process"`
	}
	decodeResult(t, startResp, &started)
	if started.Process.ID != "client-command-1" {
		t.Fatalf("command/exec process id = %q, want client-command-1", started.Process.ID)
	}

	duplicateResp := server.HandleRequest(ctx, request("command/exec", map[string]any{
		"processId": "client-command-1",
		"command":   "printf should-not-run",
	}))
	if duplicateResp.Error == nil || duplicateResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("duplicate command/exec error = %#v, want invalid params", duplicateResp.Error)
	}

	resizeResp := server.HandleRequest(ctx, request("command/exec/resize", map[string]any{
		"processId": "client-command-1",
		"size": map[string]any{
			"cols": 80,
			"rows": 24,
		},
	}))
	if resizeResp.Error == nil || resizeResp.Error.Code != protocol.CodeMethodUnavailable {
		t.Fatalf("command/exec/resize error = %#v, want unavailable", resizeResp.Error)
	}

	writeResp := server.HandleRequest(ctx, request("command/exec/write", map[string]any{
		"processId":   "client-command-1",
		"deltaBase64": base64.StdEncoding.EncodeToString([]byte("cmd-output\n")),
		"closeStdin":  true,
	}))
	if writeResp.Error != nil {
		t.Fatalf("command/exec/write error: %v", writeResp.Error)
	}
	var writeResult protocol.CommandExecWriteResponse
	decodeResult(t, writeResp, &writeResult)
	if !writeResult.OK || writeResult.Path != "client-command-1" {
		t.Fatalf("command/exec/write result = %#v", writeResult)
	}

	events := waitForNotificationSet(t, server, "process/outputDelta", "command/exec/outputDelta", "process/exited")
	var delta protocol.CommandExecOutputDeltaNotification
	for _, event := range events {
		if event.Method != "command/exec/outputDelta" {
			continue
		}
		if err := json.Unmarshal(event.Params, &delta); err != nil {
			t.Fatalf("decode command exec delta: %v", err)
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(delta.DeltaBase64)
	if err != nil {
		t.Fatalf("decode delta base64: %v", err)
	}
	if delta.ProcessID != "client-command-1" || delta.Stream != protocol.CommandExecOutputStdout || string(decoded) != "cmd-output\n" || delta.CapReached {
		t.Fatalf("command exec delta = %#v decoded=%q", delta, decoded)
	}
}

func TestServerCommandExecTerminateUsesExportedContract(t *testing.T) {
	ctx := context.Background()
	processSvc, err := toolprocess.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithProcess(processSvc))

	startResp := server.HandleRequest(ctx, request("command/exec", map[string]any{
		"processId": "terminate-command",
		"command":   "cat",
	}))
	if startResp.Error != nil {
		t.Fatalf("command/exec start error: %v", startResp.Error)
	}
	terminateResp := server.HandleRequest(ctx, request("command/exec/terminate", map[string]any{
		"processId": "terminate-command",
	}))
	if terminateResp.Error != nil {
		t.Fatalf("command/exec/terminate error: %v", terminateResp.Error)
	}
	var result protocol.CommandExecTerminateResponse
	decodeResult(t, terminateResp, &result)
	if !result.OK || result.Path != "terminate-command" {
		t.Fatalf("command/exec/terminate result = %#v", result)
	}
	if _, err := processSvc.Wait(ctx, "terminate-command"); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestServerCommandExecControlsRejectGenericProcessIDs(t *testing.T) {
	ctx := context.Background()
	processSvc, err := toolprocess.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithProcess(processSvc))

	spawnResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{
		"processId": "generic-process",
		"command":   "cat",
	}))
	if spawnResp.Error != nil {
		t.Fatalf("process/spawn error: %v", spawnResp.Error)
	}
	for _, test := range []struct {
		method string
		params map[string]any
	}{
		{method: "command/exec/write", params: map[string]any{"processId": "generic-process", "closeStdin": true}},
		{method: "command/exec/resize", params: map[string]any{"processId": "generic-process", "size": map[string]any{"cols": 80, "rows": 24}}},
		{method: "command/exec/terminate", params: map[string]any{"processId": "generic-process"}},
	} {
		resp := server.HandleRequest(ctx, request(test.method, test.params))
		if resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
			t.Errorf("%s error = %#v, want invalid params", test.method, resp.Error)
		}
	}

	killResp := server.HandleRequest(ctx, request("process/kill", map[string]any{"processId": "generic-process"}))
	if killResp.Error != nil {
		t.Fatalf("process/kill error: %v", killResp.Error)
	}
	if _, err := processSvc.Wait(ctx, "generic-process"); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestServerProcessSpawnDoesNotPublishCommandExecDelta(t *testing.T) {
	ctx := context.Background()
	var server *Server
	processSvc, err := toolprocess.NewService(t.TempDir(),
		toolprocess.WithOutputSink(func(ev toolprocess.OutputEvent) {
			server.PublishProcessOutput(ev)
		}),
		toolprocess.WithExitSink(func(ev toolprocess.ExitEvent) {
			server.PublishProcessExited(ev)
		}),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server = readyServer(WithProcess(processSvc))

	startResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{
		"id":      "spawn-client-1",
		"command": "printf",
		"args":    []string{"spawn-output"},
	}))
	if startResp.Error != nil {
		t.Fatalf("process/spawn error: %v", startResp.Error)
	}
	events := waitForNotificationSet(t, server, "process/outputDelta", "process/exited")
	events = append(events, server.DrainNotifications()...)
	for _, event := range events {
		if event.Method == "command/exec/outputDelta" {
			t.Fatalf("unexpected command exec delta for process/spawn: %#v", event)
		}
	}
	assertNoNotification(t, server, "command/exec/outputDelta")
}

func TestServerBackgroundTerminalHandlers(t *testing.T) {
	ctx := context.Background()
	processSvc, err := toolprocess.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithProcess(processSvc))

	runningResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{
		"command": "cat",
	}))
	if runningResp.Error != nil {
		t.Fatalf("process/spawn running error: %v", runningResp.Error)
	}
	var runningStarted struct {
		Process processSnapshotResult `json:"process"`
	}
	decodeResult(t, runningResp, &runningStarted)

	writeResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/write", map[string]any{
		"id":    runningStarted.Process.ID,
		"input": "hello\n",
	}))
	if writeResp.Error != nil {
		t.Fatalf("thread/backgroundTerminals/write error: %v", writeResp.Error)
	}
	var wrote struct {
		OK           bool                        `json:"ok"`
		Terminal     protocol.BackgroundTerminal `json:"terminal"`
		WrittenBytes int                         `json:"writtenBytes"`
		ObservedAt   time.Time                   `json:"observedAt"`
	}
	decodeResult(t, writeResp, &wrote)
	if !wrote.OK || wrote.Terminal.ID != runningStarted.Process.ID ||
		wrote.Terminal.Status != protocol.BackgroundTerminalStatusRunning ||
		wrote.WrittenBytes != len("hello\n") || wrote.ObservedAt.IsZero() {
		t.Fatalf("background terminal write = %#v", wrote)
	}
	if strings.Contains(string(writeResp.Result), "hello") {
		t.Fatalf("background terminal write leaked input: %s", writeResp.Result)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot, err := processSvc.Snapshot(ctx, runningStarted.Process.ID)
		if err != nil {
			t.Fatalf("snapshot after background terminal write: %v", err)
		}
		if string(snapshot.Stdout) == "hello\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background terminal did not receive input; stdout = %q", snapshot.Stdout)
		}
		time.Sleep(10 * time.Millisecond)
	}

	doneResp := server.HandleRequest(ctx, request("command/exec", map[string]any{
		"command": "printf done",
	}))
	if doneResp.Error != nil {
		t.Fatalf("command/exec done error: %v", doneResp.Error)
	}
	var doneStarted struct {
		Process processSnapshotResult `json:"process"`
	}
	decodeResult(t, doneResp, &doneStarted)
	if _, err := waitProcessSnapshot(t, processSvc, doneStarted.Process.ID); err != nil {
		t.Fatalf("wait completed process: %v", err)
	}
	completedWriteResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/write", map[string]any{
		"id":    doneStarted.Process.ID,
		"input": "hello\n",
	}))
	if completedWriteResp.Error == nil || completedWriteResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("completed terminal write response = %#v, want invalid params", completedWriteResp)
	}
	readResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/read", map[string]any{
		"id": doneStarted.Process.ID,
	}))
	if readResp.Error != nil {
		t.Fatalf("thread/backgroundTerminals/read error: %v", readResp.Error)
	}
	var transcript protocol.BackgroundTerminalReadResponse
	decodeResult(t, readResp, &transcript)
	if transcript.Terminal.ID != doneStarted.Process.ID || transcript.StdoutBase64 != "ZG9uZQ==" ||
		transcript.StderrBase64 != "" || transcript.StdoutTruncated || transcript.StderrTruncated || transcript.ObservedAt.IsZero() {
		t.Fatalf("background terminal transcript = %#v", transcript)
	}

	listResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/list", map[string]any{"limit": 1}))
	if listResp.Error != nil {
		t.Fatalf("thread/backgroundTerminals/list error: %v", listResp.Error)
	}
	var list protocol.BackgroundTerminalListResponse
	decodeResult(t, listResp, &list)
	if len(list.Terminals) != 1 || len(list.BackgroundTerminals) != 1 || len(list.Data) != 1 ||
		list.Total != 2 || !list.Truncated || list.NextCursor == "" || list.SnapshotID == "" || list.ObservedAt.IsZero() {
		t.Fatalf("background terminal list = %#v", list)
	}
	if list.Terminals[0].ArgumentCount != 0 || list.Terminals[0].WorkDir != "." {
		t.Fatalf("background terminal projection = %#v", list.Terminals[0])
	}
	pageTwoResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/list", map[string]any{
		"limit":  1,
		"cursor": list.NextCursor,
	}))
	if pageTwoResp.Error != nil {
		t.Fatalf("thread/backgroundTerminals/list page two error: %v", pageTwoResp.Error)
	}
	var pageTwo protocol.BackgroundTerminalListResponse
	decodeResult(t, pageTwoResp, &pageTwo)
	if len(pageTwo.Terminals) != 1 || pageTwo.Total != 2 || !pageTwo.Truncated || pageTwo.NextCursor != "" ||
		pageTwo.SnapshotID != list.SnapshotID || pageTwo.Terminals[0].ID == list.Terminals[0].ID {
		t.Fatalf("background terminal page two = %#v", pageTwo)
	}

	terminateResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/terminate", map[string]any{
		"terminalId": runningStarted.Process.ID,
	}))
	if terminateResp.Error != nil {
		t.Fatalf("thread/backgroundTerminals/terminate error: %v", terminateResp.Error)
	}
	var terminated protocol.BackgroundTerminalTerminateResponse
	decodeResult(t, terminateResp, &terminated)
	if !terminated.OK || terminated.ID != runningStarted.Process.ID ||
		terminated.Terminal.ID != runningStarted.Process.ID ||
		terminated.Terminal.Status != protocol.BackgroundTerminalStatusKilled {
		t.Fatalf("terminate result = %#v", terminated)
	}

	cleanResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/clean", nil))
	if cleanResp.Error != nil {
		t.Fatalf("thread/backgroundTerminals/clean error: %v", cleanResp.Error)
	}
	var cleaned protocol.BackgroundTerminalCleanResponse
	decodeResult(t, cleanResp, &cleaned)
	if cleaned.RemovedCount != 2 || len(cleaned.Removed) != 2 || cleaned.Truncated || cleaned.ObservedAt.IsZero() {
		t.Fatalf("cleaned terminals = %#v", cleaned)
	}
	listAfterResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/list", nil))
	if listAfterResp.Error != nil {
		t.Fatalf("thread/backgroundTerminals/list after clean error: %v", listAfterResp.Error)
	}
	var listAfter protocol.BackgroundTerminalListResponse
	decodeResult(t, listAfterResp, &listAfter)
	if len(listAfter.Terminals) != 0 || listAfter.Total != 0 || listAfter.SnapshotID == "" {
		t.Fatalf("terminals after clean = %#v", listAfter)
	}
	if strings.Contains(string(listAfterResp.Result), `"terminals":null`) ||
		strings.Contains(string(listAfterResp.Result), `"backgroundTerminals":null`) ||
		strings.Contains(string(listAfterResp.Result), `"data":null`) {
		t.Fatalf("empty terminal inventory emitted null arrays: %s", listAfterResp.Result)
	}

	missingIDResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/terminate", nil))
	if missingIDResp.Error == nil || missingIDResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("missing id terminate response = %#v, want invalid params", missingIDResp)
	}
	missingReadIDResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/read", nil))
	if missingReadIDResp.Error == nil || missingReadIDResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("missing id read response = %#v, want invalid params", missingReadIDResp)
	}
	missingWriteIDResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/write", map[string]any{
		"input": "hello\n",
	}))
	if missingWriteIDResp.Error == nil || missingWriteIDResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("missing id write response = %#v, want invalid params", missingWriteIDResp)
	}
	emptyWriteResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/write", map[string]any{
		"id":    runningStarted.Process.ID,
		"input": "",
	}))
	if emptyWriteResp.Error == nil || emptyWriteResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("empty write response = %#v, want invalid params", emptyWriteResp)
	}
	oversizedWriteResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/write", map[string]any{
		"id":    runningStarted.Process.ID,
		"input": strings.Repeat("x", 8<<10+1),
	}))
	if oversizedWriteResp.Error == nil || oversizedWriteResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("oversized write response = %#v, want invalid params", oversizedWriteResp)
	}
	unknownWriteIDResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/write", map[string]any{
		"id":    "missing",
		"input": "hello\n",
	}))
	if unknownWriteIDResp.Error == nil || unknownWriteIDResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("unknown id write response = %#v, want invalid params", unknownWriteIDResp)
	}
	unknownReadIDResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/read", map[string]any{
		"id": "missing",
	}))
	if unknownReadIDResp.Error == nil || unknownReadIDResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("unknown id read response = %#v, want invalid params", unknownReadIDResp)
	}
	unknownReadParamsResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/read", map[string]any{
		"id":      doneStarted.Process.ID,
		"unknown": true,
	}))
	if unknownReadParamsResp.Error == nil || unknownReadParamsResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("unknown read params response = %#v, want invalid params", unknownReadParamsResp)
	}
	unknownWriteParamsResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/write", map[string]any{
		"id":      runningStarted.Process.ID,
		"input":   "hello\n",
		"unknown": true,
	}))
	if unknownWriteParamsResp.Error == nil || unknownWriteParamsResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("unknown write params response = %#v, want invalid params", unknownWriteParamsResp)
	}
	unknownListResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/list", map[string]any{"unknown": true}))
	if unknownListResp.Error == nil || unknownListResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("unknown list params response = %#v, want invalid params", unknownListResp)
	}
	unknownTerminateResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/terminate", map[string]any{
		"id":      "missing",
		"unknown": true,
	}))
	if unknownTerminateResp.Error == nil || unknownTerminateResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("unknown terminate params response = %#v, want invalid params", unknownTerminateResp)
	}
	unexpectedCleanResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/clean", map[string]any{"all": true}))
	if unexpectedCleanResp.Error == nil || unexpectedCleanResp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("unexpected clean params response = %#v, want invalid params", unexpectedCleanResp)
	}
}

func TestServerBackgroundTerminalResize(t *testing.T) {
	ctx := context.Background()
	processSvc, err := toolprocess.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithProcess(processSvc))

	ptyResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{
		"command": "cat",
		"pty":     true,
		"ptySize": map[string]any{"rows": 24, "cols": 80},
	}))
	if ptyResp.Error != nil {
		t.Fatalf("process/spawn PTY error: %v", ptyResp.Error)
	}
	var ptyStarted struct {
		Process processSnapshotResult `json:"process"`
	}
	decodeResult(t, ptyResp, &ptyStarted)
	defer func() {
		_ = processSvc.Kill(context.Background(), ptyStarted.Process.ID)
		_, _ = waitProcessSnapshot(t, processSvc, ptyStarted.Process.ID)
	}()

	resizeResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/resize", map[string]any{
		"id":   ptyStarted.Process.ID,
		"size": map[string]any{"rows": 40, "cols": 120},
	}))
	if resizeResp.Error != nil {
		t.Fatalf("thread/backgroundTerminals/resize error: %v", resizeResp.Error)
	}
	var resized protocol.BackgroundTerminalResizeResponse
	decodeResult(t, resizeResp, &resized)
	if !resized.OK || resized.Terminal.ID != ptyStarted.Process.ID || !resized.Terminal.PTY ||
		resized.Terminal.PTYSize == nil || resized.Terminal.PTYSize.Rows != 40 ||
		resized.Terminal.PTYSize.Cols != 120 || resized.ObservedAt.IsZero() {
		t.Fatalf("background terminal resize = %#v", resized)
	}
	snapshot, err := processSvc.Snapshot(ctx, ptyStarted.Process.ID)
	if err != nil {
		t.Fatalf("snapshot after background terminal resize: %v", err)
	}
	if !snapshot.PTY || snapshot.PTYSize != (toolprocess.TerminalSize{Rows: 40, Cols: 120}) {
		t.Fatalf("PTY snapshot after resize = %#v", snapshot)
	}

	nonPTYResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{"command": "cat"}))
	if nonPTYResp.Error != nil {
		t.Fatalf("process/spawn non-PTY error: %v", nonPTYResp.Error)
	}
	var nonPTYStarted struct {
		Process processSnapshotResult `json:"process"`
	}
	decodeResult(t, nonPTYResp, &nonPTYStarted)
	defer func() {
		_ = processSvc.Kill(context.Background(), nonPTYStarted.Process.ID)
		_, _ = waitProcessSnapshot(t, processSvc, nonPTYStarted.Process.ID)
	}()
	nonPTYResize := server.HandleRequest(ctx, request("thread/backgroundTerminals/resize", map[string]any{
		"id":   nonPTYStarted.Process.ID,
		"size": map[string]any{"rows": 40, "cols": 120},
	}))
	if nonPTYResize.Error == nil || nonPTYResize.Error.Code != protocol.CodeMethodUnavailable {
		t.Fatalf("non-PTY resize response = %#v (code %d), want method unavailable", nonPTYResize, nonPTYResize.Error.Code)
	}

	for _, test := range []struct {
		name   string
		params map[string]any
	}{
		{name: "missing id", params: map[string]any{"size": map[string]any{"rows": 40, "cols": 120}}},
		{name: "missing size", params: map[string]any{"id": ptyStarted.Process.ID}},
		{name: "zero rows", params: map[string]any{"id": ptyStarted.Process.ID, "size": map[string]any{"rows": 0, "cols": 120}}},
		{name: "unknown terminal", params: map[string]any{"id": "missing", "size": map[string]any{"rows": 40, "cols": 120}}},
		{name: "unknown field", params: map[string]any{"id": ptyStarted.Process.ID, "size": map[string]any{"rows": 40, "cols": 120}, "unknown": true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := server.HandleRequest(ctx, request("thread/backgroundTerminals/resize", test.params))
			if response.Error == nil || response.Error.Code != protocol.CodeInvalidParams {
				t.Fatalf("resize response = %#v, want invalid params", response)
			}
		})
	}

	completedResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{
		"command": "printf",
		"args":    []string{"done"},
		"pty":     true,
	}))
	if completedResp.Error != nil {
		t.Fatalf("process/spawn completed PTY error: %v", completedResp.Error)
	}
	var completedStarted struct {
		Process processSnapshotResult `json:"process"`
	}
	decodeResult(t, completedResp, &completedStarted)
	if _, err := waitProcessSnapshot(t, processSvc, completedStarted.Process.ID); err != nil {
		t.Fatalf("wait completed PTY: %v", err)
	}
	completedResize := server.HandleRequest(ctx, request("thread/backgroundTerminals/resize", map[string]any{
		"id":   completedStarted.Process.ID,
		"size": map[string]any{"rows": 40, "cols": 120},
	}))
	if completedResize.Error == nil || completedResize.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("completed resize response = %#v, want invalid params", completedResize)
	}
}

func TestServerBackgroundTerminalWriteUsesStableApprovalIdentity(t *testing.T) {
	ctx := context.Background()
	var (
		operation      toolprocess.Operation
		approvalItemID string
	)
	processSvc, err := toolprocess.NewService(t.TempDir(), toolprocess.WithApproval(func(ctx context.Context, got toolprocess.Operation) error {
		operation = got
		approvalItemID = runtimeApprovalItemIDFrom(ctx)
		return nil
	}))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithProcess(processSvc))

	startResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{"command": "cat"}))
	if startResp.Error != nil {
		t.Fatalf("process/spawn error: %v", startResp.Error)
	}
	var started struct {
		Process processSnapshotResult `json:"process"`
	}
	decodeResult(t, startResp, &started)
	operation = toolprocess.Operation{}
	approvalItemID = ""

	writeResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/write", map[string]any{
		"id":    started.Process.ID,
		"input": "private input\n",
	}))
	if writeResp.Error != nil {
		t.Fatalf("thread/backgroundTerminals/write error: %v", writeResp.Error)
	}
	if operation.Kind != toolprocess.OperationWriteStdin || operation.ID != started.Process.ID {
		t.Fatalf("write operation = %#v", operation)
	}
	if approvalItemID != operationalTerminalWriteApprovalItemID(started.Process.ID) || strings.Contains(approvalItemID, "private") {
		t.Fatalf("write approval item id = %q", approvalItemID)
	}
	if err := processSvc.Kill(ctx, started.Process.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, err := waitProcessSnapshot(t, processSvc, started.Process.ID); err != nil {
		t.Fatalf("wait killed process: %v", err)
	}
}

func TestServerBackgroundTerminalTerminateDoesNotClaimSuccessBeforeExit(t *testing.T) {
	ctx := context.Background()
	processSvc, err := toolprocess.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithProcess(processSvc))

	runningResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{
		"command": "sh",
		"args":    []string{"-c", "trap '' TERM; printf ready; while :; do :; done"},
	}))
	if runningResp.Error != nil {
		t.Fatalf("process/spawn running error: %v", runningResp.Error)
	}
	var running struct {
		Process processSnapshotResult `json:"process"`
	}
	decodeResult(t, runningResp, &running)
	ready := false
	for range 100 {
		snapshot, snapshotErr := processSvc.Snapshot(ctx, running.Process.ID)
		if snapshotErr != nil {
			t.Fatalf("Snapshot before terminate: %v", snapshotErr)
		}
		if strings.Contains(string(snapshot.Stdout), "ready") {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("signal-ignore fixture did not become ready")
	}
	defer func() {
		_ = processSvc.Kill(context.Background(), running.Process.ID)
		_, _ = processSvc.Wait(context.Background(), running.Process.ID)
	}()

	terminateCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	terminateResp := server.HandleRequest(
		terminateCtx,
		request("thread/backgroundTerminals/terminate", map[string]any{
			"id": running.Process.ID,
		}),
	)
	if terminateResp.Error == nil {
		t.Fatalf("terminate response claimed success before process exit: %#v", terminateResp)
	}
	snapshot, err := processSvc.Snapshot(context.Background(), running.Process.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.Status != toolprocess.StatusRunning {
		t.Fatalf("process status = %s, want running after ignored SIGTERM", snapshot.Status)
	}
}

func TestServerBackgroundTerminalTerminateApprovalBindsTarget(t *testing.T) {
	ctx := context.Background()
	approvals := NewApprovalService()
	processSvc, err := toolprocess.NewService(
		t.TempDir(),
		toolprocess.WithApproval(func(ctx context.Context, op toolprocess.Operation) error {
			if op.Kind == toolprocess.OperationTerminate {
				return approvals.ProcessApproval(ctx, op)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(
		WithProcess(processSvc),
		WithApprovalService(approvals),
	)

	spawnResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{
		"command": "cat",
	}))
	if spawnResp.Error != nil {
		t.Fatalf("process/spawn error: %v", spawnResp.Error)
	}
	var running struct {
		Process processSnapshotResult `json:"process"`
	}
	decodeResult(t, spawnResp, &running)
	t.Cleanup(func() {
		_ = processSvc.Kill(context.Background(), running.Process.ID)
		_, _ = processSvc.Wait(context.Background(), running.Process.ID)
	})

	terminateResult := make(chan protocol.Response, 1)
	go func() {
		terminateResult <- server.HandleRequest(
			ctx,
			request("thread/backgroundTerminals/terminate", map[string]any{
				"id": running.Process.ID,
			}),
		)
	}()

	approvalRequest := waitForServerRequest(t, server)
	if approvalRequest.Method != "item/commandExecution/requestApproval" {
		t.Fatalf("approval method = %q", approvalRequest.Method)
	}
	var approvalParams protocol.CommandExecutionApprovalRequestParams
	if err := json.Unmarshal(approvalRequest.Params, &approvalParams); err != nil {
		t.Fatalf("decode command approval params: %v", err)
	}
	wantItemID := operationalTerminalTerminateApprovalItemID(running.Process.ID)
	if approvalParams.ItemID != wantItemID {
		t.Fatalf("approval item id = %q, want %q", approvalParams.ItemID, wantItemID)
	}
	if strings.Contains(approvalParams.ItemID, running.Process.ID) {
		t.Fatalf("approval item id leaked native process id: %q", approvalParams.ItemID)
	}
	if approvalParams.Operation != string(toolprocess.OperationTerminate) ||
		approvalParams.Signal != "SIGTERM" ||
		!approvalParams.Destructive {
		t.Fatalf("terminate approval params = %#v", approvalParams)
	}

	requestID, _ := approvalRequest.ID.Value().(string)
	approvalResp := server.HandleRequest(ctx, request("approval/respond", map[string]any{
		"requestId": requestID,
		"approved":  true,
	}))
	if approvalResp.Error != nil {
		t.Fatalf("approval/respond error: %v", approvalResp.Error)
	}

	select {
	case terminateResp := <-terminateResult:
		if terminateResp.Error != nil {
			t.Fatalf("thread/backgroundTerminals/terminate error: %v", terminateResp.Error)
		}
		var terminated protocol.BackgroundTerminalTerminateResponse
		decodeResult(t, terminateResp, &terminated)
		if !terminated.OK ||
			terminated.ID != running.Process.ID ||
			terminated.Terminal.Status != protocol.BackgroundTerminalStatusKilled {
			t.Fatalf("terminate result = %#v", terminated)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for approved terminate response")
	}
}

func TestServerBackgroundTerminalInventoryRejectsStaleCursorAndRedactsArguments(t *testing.T) {
	ctx := context.Background()
	processSvc, err := toolprocess.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithProcess(processSvc))

	for _, id := range []string{"one", "two"} {
		resp := server.HandleRequest(ctx, request("process/spawn", map[string]any{
			"id":      id,
			"command": "sh",
			"args":    []string{"-c", "printf super-secret-" + id},
		}))
		if resp.Error != nil {
			t.Fatalf("process/spawn %s error: %v", id, resp.Error)
		}
		if _, err := waitProcessSnapshot(t, processSvc, id); err != nil {
			t.Fatalf("wait %s: %v", id, err)
		}
	}

	firstResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/list", map[string]any{"limit": 1}))
	if firstResp.Error != nil {
		t.Fatalf("first page error: %v", firstResp.Error)
	}
	if strings.Contains(string(firstResp.Result), "super-secret") || strings.Contains(string(firstResp.Result), `"args"`) ||
		strings.Contains(string(firstResp.Result), `"stdout"`) || strings.Contains(string(firstResp.Result), `"stderr"`) {
		t.Fatalf("terminal inventory leaked process metadata: %s", firstResp.Result)
	}
	var first protocol.BackgroundTerminalListResponse
	decodeResult(t, firstResp, &first)
	if first.NextCursor == "" || len(first.Terminals) != 1 || first.Terminals[0].Command != "sh" ||
		!first.Terminals[0].CommandRedacted || first.Terminals[0].ArgumentCount != 2 {
		t.Fatalf("first terminal page = %#v", first)
	}

	thirdResp := server.HandleRequest(ctx, request("process/spawn", map[string]any{
		"id":      "three",
		"command": "printf",
		"args":    []string{"three"},
	}))
	if thirdResp.Error != nil {
		t.Fatalf("process/spawn three error: %v", thirdResp.Error)
	}
	if _, err := waitProcessSnapshot(t, processSvc, "three"); err != nil {
		t.Fatalf("wait three: %v", err)
	}
	staleResp := server.HandleRequest(ctx, request("thread/backgroundTerminals/list", map[string]any{
		"limit":  1,
		"cursor": first.NextCursor,
	}))
	if staleResp.Error == nil || staleResp.Error.Code != protocol.CodeInvalidParams ||
		!strings.Contains(staleResp.Error.Message, "stale") {
		t.Fatalf("stale cursor response = %#v", staleResp)
	}
}

func TestServerGitHandlers(t *testing.T) {
	ctx := context.Background()
	repo := initRepo(t)
	gitSvc, err := toolgit.NewService(repo, toolgit.WithWorktreeRoot(filepath.Join(t.TempDir(), "worktrees")))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	server := readyServer(WithGit(gitSvc))

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "SECOND.md"), []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write second file: %v", err)
	}
	statusResp := server.HandleRequest(ctx, request("git/status", map[string]any{"limit": 1}))
	if statusResp.Error != nil {
		t.Fatalf("git/status error: %v", statusResp.Error)
	}
	var status protocol.GitStatusResponse
	decodeResult(t, statusResp, &status)
	if status.Status.Clean || len(status.Status.Entries) != 1 || status.Status.EntryCount != 2 ||
		!status.Status.EntriesTruncated || status.NextCursor == "" || status.SnapshotID == "" || status.ObservedAt.IsZero() {
		t.Fatalf("git/status = %#v", status.Status)
	}
	if strings.Contains(string(statusResp.Result), `"raw"`) {
		t.Fatalf("git/status leaked raw porcelain output: %s", statusResp.Result)
	}
	statusPageTwoResp := server.HandleRequest(ctx, request("git/status", map[string]any{
		"limit":  1,
		"cursor": status.NextCursor,
	}))
	if statusPageTwoResp.Error != nil {
		t.Fatalf("git/status page two error: %v", statusPageTwoResp.Error)
	}
	var statusPageTwo protocol.GitStatusResponse
	decodeResult(t, statusPageTwoResp, &statusPageTwo)
	if len(statusPageTwo.Status.Entries) != 1 || statusPageTwo.Status.EntryCount != 2 ||
		!statusPageTwo.Status.EntriesTruncated || statusPageTwo.NextCursor != "" ||
		statusPageTwo.SnapshotID != status.SnapshotID {
		t.Fatalf("git/status page two = %#v", statusPageTwo)
	}

	diffResp := server.HandleRequest(ctx, request("git/diff", nil))
	if diffResp.Error != nil {
		t.Fatalf("git/diff error: %v", diffResp.Error)
	}
	var diff struct {
		Diff *toolgit.Diff `json:"diff"`
	}
	decodeResult(t, diffResp, &diff)
	if diff.Diff.Patch == "" {
		t.Fatal("git/diff returned empty patch")
	}

	commitResp := server.HandleRequest(ctx, request("git/commit", map[string]any{
		"message": "commit changed file",
		"all":     true,
	}))
	if commitResp.Error != nil {
		t.Fatalf("git/commit error: %v", commitResp.Error)
	}
	var commit struct {
		Commit *toolgit.CommitResult `json:"commit"`
	}
	decodeResult(t, commitResp, &commit)
	if commit.Commit.Hash == "" {
		t.Fatalf("git/commit = %#v", commit.Commit)
	}
	cleanStatusResp := server.HandleRequest(ctx, request("git/status", nil))
	if cleanStatusResp.Error != nil {
		t.Fatalf("clean git/status error: %v", cleanStatusResp.Error)
	}
	if strings.Contains(string(cleanStatusResp.Result), `"entries":null`) {
		t.Fatalf("clean git/status emitted null entries: %s", cleanStatusResp.Result)
	}
	var cleanStatus protocol.GitStatusResponse
	decodeResult(t, cleanStatusResp, &cleanStatus)
	if !cleanStatus.Status.Clean || len(cleanStatus.Status.Entries) != 0 || cleanStatus.Status.EntryCount != 0 {
		t.Fatalf("clean git/status = %#v", cleanStatus)
	}

	listResp := server.HandleRequest(ctx, request("git/worktree/list", nil))
	if listResp.Error != nil {
		t.Fatalf("git/worktree/list error: %v", listResp.Error)
	}
	var worktrees protocol.GitWorktreeListResponse
	decodeResult(t, listResp, &worktrees)
	if len(worktrees.Worktrees) != 1 || worktrees.Total != 1 || worktrees.Truncated ||
		worktrees.SnapshotID == "" || worktrees.ObservedAt.IsZero() || worktrees.Worktrees[0].Path == "" {
		t.Fatalf("git/worktree/list = %#v", worktrees)
	}
}

func readyServer(opts ...Option) *Server {
	server := NewServer(opts...)
	_ = server.HandleRequest(context.Background(), request("initialize", protocol.InitializeParams{
		ClientInfo: protocol.ClientInfo{Name: "test-client", Version: "1.0.0"},
	}))
	if err := server.HandleNotification(context.Background(), protocol.Notification{Method: "initialized"}); err != nil {
		panic(err)
	}
	return server
}

func request(method string, params any) protocol.Request {
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			panic(err)
		}
		raw = data
	}
	return protocol.Request{
		ID:     protocol.NewStringID("req-" + method),
		Method: method,
		Params: raw,
	}
}

func decodeResult(t *testing.T, resp protocol.Response, out any) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("response error: %v", resp.Error)
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		t.Fatalf("unmarshal result %s: %v", string(resp.Result), err)
	}
}

func assertNotificationMethods(t *testing.T, notifications []protocol.Notification, want ...string) {
	t.Helper()
	if len(notifications) != len(want) {
		t.Fatalf("notifications = %#v, want methods %v", notifications, want)
	}
	for i, method := range want {
		if notifications[i].Method != method {
			t.Fatalf("notification[%d] method = %q, want %q", i, notifications[i].Method, method)
		}
	}
}

func waitForNotificationMethods(t *testing.T, server *Server, want ...string) []protocol.Notification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var notifications []protocol.Notification
	for time.Now().Before(deadline) {
		notifications = append(notifications, server.DrainNotifications()...)
		if len(notifications) >= len(want) {
			assertNotificationMethods(t, notifications, want...)
			return notifications
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for notifications %v, got %#v", want, notifications)
	return nil
}

func sameStringSet(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[string]int, len(got))
	for _, value := range got {
		counts[value]++
	}
	for _, value := range want {
		if counts[value] == 0 {
			return false
		}
		counts[value]--
	}
	return true
}

func waitForNotification(t *testing.T, server *Server, method string) protocol.Notification {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case <-server.NotificationSignal():
			for _, notification := range server.DrainNotifications() {
				if notification.Method == method {
					return notification
				}
			}
		case <-timeout:
			t.Fatalf("timed out waiting for notification %q", method)
		}
	}
}

func assertNoNotification(t *testing.T, server *Server, method string) {
	t.Helper()
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case <-server.NotificationSignal():
			for _, notification := range server.DrainNotifications() {
				if notification.Method == method {
					t.Fatalf("unexpected notification %q: %#v", method, notification)
				}
			}
		case <-timeout:
			return
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func waitForServerRequest(t *testing.T, server *Server) protocol.Request {
	t.Helper()
	select {
	case <-server.RequestSignal():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server request")
	}
	requests := server.DrainRequests()
	if len(requests) != 1 {
		t.Fatalf("server requests = %#v", requests)
	}
	return requests[0]
}

func waitProcessSnapshot(t *testing.T, svc *toolprocess.Service, id string) (*toolprocess.Snapshot, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return svc.Wait(ctx, id)
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Test User")
	runGit(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = cleanTestGitEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func cleanTestGitEnv(env []string) []string {
	blocked := map[string]struct{}{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
		"GIT_COMMON_DIR":                   {},
		"GIT_DIR":                          {},
		"GIT_INDEX_FILE":                   {},
		"GIT_NAMESPACE":                    {},
		"GIT_OBJECT_DIRECTORY":             {},
		"GIT_PREFIX":                       {},
		"GIT_QUARANTINE_PATH":              {},
		"GIT_WORK_TREE":                    {},
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if _, ok := blocked[key]; ok {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func providerConfigured(providers []catalog.Provider, id string) bool {
	for _, provider := range providers {
		if provider.ID == id {
			return provider.Configured
		}
	}
	return false
}

func configEntry(t *testing.T, entries []appconfig.Entry, key string) appconfig.Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.Key == key {
			return entry
		}
	}
	t.Fatalf("config entry %q not found in %#v", key, entries)
	return appconfig.Entry{}
}

func configRequirementSatisfied(requirements []appconfig.Requirement, id string) bool {
	for _, requirement := range requirements {
		if requirement.ID == id {
			return requirement.Satisfied
		}
	}
	return false
}

func writeAppServerTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func appServerTestSkillByName(t *testing.T, skills []appskills.Skill, name string) appskills.Skill {
	t.Helper()
	for _, skill := range skills {
		if skill.Name == name {
			return skill
		}
	}
	t.Fatalf("skill %q not found in %#v", name, skills)
	return appskills.Skill{}
}

func toolAvailable(tools []catalog.Tool, id string) bool {
	for _, tool := range tools {
		if tool.ID == id {
			return tool.Available
		}
	}
	return false
}

type serverTestMCPSource struct {
	tools             []extmcp.Tool
	toolResults       map[string]*extmcp.ToolResult
	resources         []extmcp.Resource
	resourceTemplates []extmcp.ResourceTemplate
	resourceResults   map[string]*extmcp.ReadResourceResult
	callCount         int
}

func (s *serverTestMCPSource) ListTools(context.Context) ([]extmcp.Tool, error) {
	return append([]extmcp.Tool(nil), s.tools...), nil
}

func (s *serverTestMCPSource) CallTool(_ context.Context, name string, _ map[string]any) (*extmcp.ToolResult, error) {
	s.callCount++
	result, ok := s.toolResults[name]
	if !ok {
		return nil, errors.New("tool not found")
	}
	return result, nil
}

func (s *serverTestMCPSource) ListResources(context.Context) ([]extmcp.Resource, error) {
	return append([]extmcp.Resource(nil), s.resources...), nil
}

func (s *serverTestMCPSource) ReadResource(_ context.Context, uri string) (*extmcp.ReadResourceResult, error) {
	result, ok := s.resourceResults[uri]
	if !ok {
		return nil, errors.New("resource not found")
	}
	return result, nil
}

func (s *serverTestMCPSource) ListResourceTemplates(context.Context) ([]extmcp.ResourceTemplate, error) {
	return append([]extmcp.ResourceTemplate(nil), s.resourceTemplates...), nil
}

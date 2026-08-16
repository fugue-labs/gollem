package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

func TestServerRuntimeToolFailureRedactsPersistedAndNotifiedItem(t *testing.T) {
	const secret = "https://tools.invalid/run?token=super-secret"
	type args struct{}
	tool := core.FuncTool[args]("secret_tool", "returns a secret-bearing error", func(context.Context, args) (string, error) {
		return "", errors.New("tool request to " + secret + " failed")
	})
	st := newRuntimeTestStore(t)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(
				core.NewTestModel(
					core.ToolCallResponseWithID("secret_tool", `{}`, "call-secret"),
					core.TextResponse("recovered"),
				),
				RuntimeModelInfo{ProviderID: "test", Model: "tool-failure"},
			),
			WithRuntimeTools(tool),
		)),
	)

	response := server.HandleRequest(context.Background(), request("thread/start", map[string]any{"prompt": "run tool"}))
	if response.Error != nil {
		t.Fatalf("thread/start error: %v", response.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, response, &started)
	notifications := waitForNotificationSet(t, server, "item/completed", "turn/completed")

	items, err := st.ListItems(context.Background(), store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	item, payload := runtimeFailedToolItem(t, items)
	if item.Status != runtimeToolStatusFailed || payload.Status != runtimeToolStatusFailed || len(payload.ContentItems) != 1 || payload.ContentItems[0].Text != runtimePublicToolFailure {
		t.Fatalf("persisted failed tool item = %#v, payload = %#v", item, payload)
	}
	if strings.Contains(payload.ContentItems[0].Text, "super-secret") || strings.Contains(payload.ContentItems[0].Text, "tools.invalid") {
		t.Fatalf("persisted failed tool output leaked secret detail: %#v", payload.ContentItems)
	}

	for _, notification := range notifications {
		if notification.Method != "item/completed" {
			continue
		}
		var params runtimeToolItemCompletedNotificationParams
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			t.Fatalf("decode tool completion notification: %v", err)
		}
		if params.Item.ID != item.ID {
			continue
		}
		if len(params.Item.ContentItems) != 1 || params.Item.ContentItems[0].Text != runtimePublicToolFailure {
			t.Fatalf("tool completion notification = %#v", params)
		}
		return
	}
	t.Fatal("failed tool completion notification missing")
}

func TestRuntimeMCPFailureRedactsPersistedAndNotifiedItem(t *testing.T) {
	const secret = "Authorization: Bearer super-secret"
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "MCP item redaction"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	turn, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	notifier := &runtimeErrorCaptureNotifier{}
	tracker := newRuntimeMCPItemTracker(st, notifier, turn, nil)
	tracker.toolStarted(runtimeMCPToolStartedEvent{
		RunID: "run", ToolCallID: "call", ToolName: "mcp_call_tool", Server: "repo", MCPTool: "status", StartedAt: time.Now().UTC(),
	})
	tracker.toolCompleted(runtimeMCPToolCompletedEvent{
		RunID: "run", ToolCallID: "call", ToolName: "mcp_call_tool", Error: secret, CompletedAt: time.Now().UTC(),
	})

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: thread.ID, TurnID: turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %#v, want one MCP item", items)
	}
	var payload runtimeMCPToolCallPayload
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatalf("decode MCP item: %v", err)
	}
	if items[0].Status != runtimeMCPStatusFailed || payload.Status != runtimeMCPStatusFailed || payload.Error == nil || payload.Error.Message != runtimePublicMCPToolFailure {
		t.Fatalf("persisted MCP item = %#v, payload = %#v", items[0], payload)
	}
	if strings.Contains(payload.Error.Message, "super-secret") {
		t.Fatalf("persisted MCP item leaked secret detail: %#v", payload.Error)
	}

	params, ok := notifier.params.(runtimeMCPItemCompletedNotificationParams)
	if !ok || params.Item.Error == nil || params.Item.Error.Message != runtimePublicMCPToolFailure {
		t.Fatalf("MCP completion notification = %#v (%T)", notifier.params, notifier.params)
	}
}

func runtimeFailedToolItem(t *testing.T, items []*store.Item) (*store.Item, runtimeDynamicToolCallPayload) {
	t.Helper()
	for _, item := range items {
		if item.Kind != runtimeDynamicToolCallItemKind {
			continue
		}
		var payload runtimeDynamicToolCallPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatalf("decode dynamic tool item: %v", err)
		}
		if payload.Tool == "secret_tool" {
			return item, payload
		}
	}
	t.Fatal("failed dynamic tool item missing")
	return nil, runtimeDynamicToolCallPayload{}
}

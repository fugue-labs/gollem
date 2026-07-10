package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

func TestInteractionRuntimeToolsExposeStructuredRequests(t *testing.T) {
	tools := InteractionRuntimeTools(NewInteractionService())
	want := map[string]struct{}{
		"request_user_input":      {},
		"call_client_tool":        {},
		"request_mcp_elicitation": {},
	}
	if len(tools) != len(want) {
		t.Fatalf("runtime interaction tools = %d, want %d", len(tools), len(want))
	}
	for _, tool := range tools {
		if _, ok := want[tool.Definition.Name]; !ok {
			t.Fatalf("unexpected runtime interaction tool %q", tool.Definition.Name)
		}
		if tool.Definition.Namespace != runtimeInteractionToolNamespace || !tool.Definition.Sequential || tool.Definition.ConcurrencySafe {
			t.Fatalf("tool %q namespace/scheduling = %q sequential=%v concurrencySafe=%v", tool.Definition.Name, tool.Definition.Namespace, tool.Definition.Sequential, tool.Definition.ConcurrencySafe)
		}
	}
	if InteractionRuntimeTools(nil) != nil {
		t.Fatal("nil interaction service should expose no tools")
	}
}

func TestServerRuntimeUserInputUsesDurableToolItemCorrelation(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	interactions := NewInteractionService()
	model := core.NewTestModel(
		core.ToolCallResponseWithID(
			"request_user_input",
			`{"prompt":"Choose a mode","options":["safe","fast"],"required":true}`,
			"call-user-input",
		),
		core.TextResponse("input received"),
	)
	server := readyServer(
		WithStore(st),
		WithInteractionService(interactions),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(InteractionRuntimeTools(interactions)...),
		)),
	)
	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "ask me"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)

	serverRequest := waitForServerRequest(t, server)
	if serverRequest.Method != InteractionRequestUserInput {
		t.Fatalf("interaction method = %q", serverRequest.Method)
	}
	var params map[string]any
	if err := json.Unmarshal(serverRequest.Params, &params); err != nil {
		t.Fatalf("decode interaction request: %v", err)
	}
	itemID, _ := params["itemId"].(string)
	if params["threadId"] != started.Thread.ID || params["turnId"] != started.Turn.ID || itemID == "" || itemID == "call-user-input" {
		t.Fatalf("interaction correlation = %#v", params)
	}
	if err := server.HandleResponse(ctx, protocol.Response{ID: serverRequest.ID, Result: json.RawMessage(`{"text":"safe"}`)}); err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}
	waitForNotificationSet(t, server, "serverRequest/resolved", "turn/completed")

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	toolItem := findRuntimeToolItem(t, items, "request_user_input", "prompt", "Choose a mode")
	if itemID != toolItem.Item.ID {
		t.Fatalf("request item = %q, want durable dynamic item %q", itemID, toolItem.Item.ID)
	}
	if toolItem.Status != runtimeToolStatusCompleted || len(toolItem.Payload.ContentItems) != 1 || !containsJSONText(toolItem.Payload.ContentItems[0].Text, "safe") {
		t.Fatalf("completed interaction item = %#v", toolItem)
	}
}

func TestInteractionRuntimeClientToolAndMCPElicitationUseTypedRequests(t *testing.T) {
	interactions := NewInteractionService()
	server := readyServer(WithInteractionService(interactions))
	tools := InteractionRuntimeTools(interactions)
	tests := []struct {
		name       string
		toolName   string
		args       string
		wantMethod string
	}{
		{
			name:       "client tool",
			toolName:   "call_client_tool",
			args:       `{"toolName":"client.search","arguments":{"query":"gollem"},"timeoutSeconds":30}`,
			wantMethod: InteractionToolCall,
		},
		{
			name:       "MCP elicitation",
			toolName:   "request_mcp_elicitation",
			args:       `{"server":"repo","message":"Choose access","schema":{"type":"object"},"timeoutSeconds":30}`,
			wantMethod: InteractionMCPElicitation,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultCh := make(chan struct {
				result any
				err    error
			}, 1)
			go func() {
				result, err := findRuntimeToolByName(t, tools, tt.toolName).Handler(
					withRuntimeTurnContext(context.Background(), "thread-runtime", "turn-runtime"),
					&core.RunContext{ToolCallID: "call-runtime", ToolName: tt.toolName},
					tt.args,
				)
				resultCh <- struct {
					result any
					err    error
				}{result: result, err: err}
			}()
			serverRequest := waitForServerRequest(t, server)
			if serverRequest.Method != tt.wantMethod {
				t.Fatalf("request method = %q, want %q", serverRequest.Method, tt.wantMethod)
			}
			if err := server.HandleResponse(context.Background(), protocol.Response{ID: serverRequest.ID, Result: json.RawMessage(`{"ok":true}`)}); err != nil {
				t.Fatalf("HandleResponse: %v", err)
			}
			got := <-resultCh
			if got.err != nil {
				t.Fatalf("runtime interaction: %v", got.err)
			}
			result := got.result.(runtimeInteractionResult)
			if result.Method != tt.wantMethod || result.Result == nil {
				t.Fatalf("runtime interaction result = %#v", result)
			}
		})
	}
}

func TestServerRuntimeClientToolAndMCPElicitationUseDurableCorrelation(t *testing.T) {
	tests := []struct {
		name          string
		toolName      string
		args          string
		wantMethod    string
		argumentKey   string
		argumentValue any
	}{
		{
			name:          "client tool",
			toolName:      "call_client_tool",
			args:          `{"toolName":"client.search","arguments":{"query":"gollem"}}`,
			wantMethod:    InteractionToolCall,
			argumentKey:   "toolName",
			argumentValue: "client.search",
		},
		{
			name:          "MCP elicitation",
			toolName:      "request_mcp_elicitation",
			args:          `{"server":"repo","message":"Choose access","schema":{"type":"object"}}`,
			wantMethod:    InteractionMCPElicitation,
			argumentKey:   "server",
			argumentValue: "repo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := newRuntimeTestStore(t)
			interactions := NewInteractionService()
			model := core.NewTestModel(
				core.ToolCallResponseWithID(tt.toolName, tt.args, "call-runtime-interaction"),
				core.TextResponse("interaction complete"),
			)
			server := readyServer(
				WithStore(st),
				WithInteractionService(interactions),
				WithRuntimeService(NewRuntimeService(
					WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
					WithRuntimeTools(InteractionRuntimeTools(interactions)...),
				)),
			)
			resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "request interaction"}))
			if resp.Error != nil {
				t.Fatalf("thread/start error: %v", resp.Error)
			}
			var started struct {
				Thread *store.Thread `json:"thread"`
				Turn   *store.Turn   `json:"turn"`
			}
			decodeResult(t, resp, &started)
			serverRequest := waitForServerRequest(t, server)
			if serverRequest.Method != tt.wantMethod {
				t.Fatalf("request method = %q, want %q", serverRequest.Method, tt.wantMethod)
			}
			var params map[string]any
			if err := json.Unmarshal(serverRequest.Params, &params); err != nil {
				t.Fatalf("decode server request: %v", err)
			}
			itemID, _ := params["itemId"].(string)
			if itemID == "" || itemID == "call-runtime-interaction" || params["threadId"] != started.Thread.ID || params["turnId"] != started.Turn.ID {
				t.Fatalf("server request correlation = %#v", params)
			}
			if err := server.HandleResponse(ctx, protocol.Response{ID: serverRequest.ID, Result: json.RawMessage(`{"ok":true}`)}); err != nil {
				t.Fatalf("HandleResponse: %v", err)
			}
			waitForNotificationSet(t, server, "serverRequest/resolved", "turn/completed")
			items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
			if err != nil {
				t.Fatalf("ListItems: %v", err)
			}
			toolItem := findRuntimeToolItem(t, items, tt.toolName, tt.argumentKey, tt.argumentValue)
			if toolItem.Item.ID != itemID || toolItem.Status != runtimeToolStatusCompleted {
				t.Fatalf("durable interaction item = %#v, request item %q", toolItem, itemID)
			}
		})
	}
}

func TestInteractionRuntimeCancellationRemovesPendingRequest(t *testing.T) {
	interactions := NewInteractionService()
	tool := findRuntimeToolByName(t, InteractionRuntimeTools(interactions), "request_user_input")
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	go func() {
		_, err := tool.Handler(ctx, &core.RunContext{ToolCallID: "call-cancel", ToolName: "request_user_input"}, `{"prompt":"Wait","timeoutSeconds":30}`)
		resultCh <- err
	}()

	select {
	case <-interactions.requests.Signal():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for interaction request")
	}
	requests := interactions.requests.Drain()
	if len(requests) != 1 {
		t.Fatalf("interaction requests = %#v", requests)
	}
	cancel()
	if err := <-resultCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled interaction error = %v", err)
	}
	if _, ok, err := interactions.Respond(protocol.Response{ID: requests[0].ID, Result: json.RawMessage(`{"late":true}`)}); err != nil || ok {
		t.Fatalf("late response ok=%v err=%v, want removed pending request", ok, err)
	}
}

func TestServerRuntimeInterruptCancelsPendingInteraction(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	interactions := NewInteractionService()
	model := core.NewTestModel(
		core.ToolCallResponseWithID("request_user_input", `{"prompt":"Wait for input"}`, "call-cancel-input"),
		core.TextResponse("should not run"),
	)
	server := readyServer(
		WithStore(st),
		WithInteractionService(interactions),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(InteractionRuntimeTools(interactions)...),
		)),
	)
	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "ask and interrupt"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	serverRequest := waitForServerRequest(t, server)
	if interrupt := server.HandleRequest(ctx, request("turn/interrupt", map[string]any{"turnId": started.Turn.ID})); interrupt.Error != nil {
		t.Fatalf("turn/interrupt error: %v", interrupt.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")
	turn, err := st.GetTurn(ctx, started.Turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if turn.Status != store.TurnInterrupted {
		t.Fatalf("turn status = %q, want interrupted", turn.Status)
	}
	if err := server.HandleResponse(ctx, protocol.Response{ID: serverRequest.ID, Result: json.RawMessage(`{"late":true}`)}); err == nil {
		t.Fatal("late interaction response should be rejected after interruption")
	}
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	toolItem := findRuntimeToolItem(t, items, "request_user_input", "prompt", "Wait for input")
	if toolItem.Status != runtimeToolStatusFailed || len(toolItem.Payload.ContentItems) != 1 || !strings.Contains(toolItem.Payload.ContentItems[0].Text, "canceled") {
		t.Fatalf("interrupted interaction item = %#v", toolItem)
	}
}

func TestInteractionRuntimeRejectsOversizedPayload(t *testing.T) {
	tool := findRuntimeToolByName(t, InteractionRuntimeTools(NewInteractionService()), "call_client_tool")
	_, err := tool.Handler(context.Background(), &core.RunContext{}, `{"toolName":"client.search","arguments":{"query":"`+strings.Repeat("x", runtimeInteractionPayloadMaxBytes)+`"}}`)
	if err == nil {
		t.Fatal("oversized interaction payload should fail")
	}
}

func TestRuntimeInteractionContextAppliesAndValidatesTimeout(t *testing.T) {
	started := time.Now()
	ctx, cancel, err := runtimeInteractionContext(context.Background(), 1)
	if err != nil {
		t.Fatalf("runtimeInteractionContext: %v", err)
	}
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Before(started.Add(900*time.Millisecond)) || deadline.After(started.Add(2*time.Second)) {
		t.Fatalf("interaction deadline = %v, ok=%v", deadline, ok)
	}
	if _, _, err := runtimeInteractionContext(context.Background(), int(runtimeInteractionMaxTimeout/time.Second)+1); err == nil {
		t.Fatal("timeout above maximum should fail")
	}
}

func containsJSONText(value, needle string) bool {
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) == nil {
		raw, _ := json.Marshal(decoded)
		return string(raw) != "" && json.Valid(raw) && strings.Contains(string(raw), needle)
	}
	return strings.Contains(value, needle)
}

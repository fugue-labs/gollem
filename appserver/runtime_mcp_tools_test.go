package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	appmcp "github.com/fugue-labs/gollem/appserver/mcp"
	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
	extmcp "github.com/fugue-labs/gollem/ext/mcp"
)

func TestMCPRuntimeToolsExposeBoundedOperations(t *testing.T) {
	svc := appmcp.NewService()
	tools := MCPRuntimeTools(svc, NewApprovalService())
	want := map[string]bool{
		"mcp_list_servers":  true,
		"mcp_list_tools":    true,
		"mcp_read_resource": true,
		"mcp_call_tool":     false,
	}
	if len(tools) != len(want) {
		t.Fatalf("runtime MCP tools = %d, want %d", len(tools), len(want))
	}
	for _, tool := range tools {
		concurrencySafe, ok := want[tool.Definition.Name]
		if !ok {
			t.Fatalf("unexpected runtime MCP tool %q", tool.Definition.Name)
		}
		if tool.Definition.Namespace != runtimeMCPToolNamespace {
			t.Fatalf("tool %q namespace = %q", tool.Definition.Name, tool.Definition.Namespace)
		}
		if tool.Definition.ConcurrencySafe != concurrencySafe || tool.Definition.Sequential == concurrencySafe {
			t.Fatalf("tool %q scheduling sequential=%v concurrencySafe=%v", tool.Definition.Name, tool.Definition.Sequential, tool.Definition.ConcurrencySafe)
		}
	}
	if MCPRuntimeTools(nil, NewApprovalService()) != nil {
		t.Fatal("nil MCP service should expose no tools")
	}
}

func TestMCPRuntimeDiscoveryAndResourceReadAreBounded(t *testing.T) {
	large := strings.Repeat("resource", runtimeMCPResultMaxBytes)
	source := &runtimeMCPSource{
		tools: []extmcp.Tool{{
			Name:        "echo",
			Description: strings.Repeat("description", runtimeMCPMetadataMaxBytes),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
		}},
		resources: map[string]*extmcp.ReadResourceResult{
			"file:///large.txt": {Contents: []extmcp.ResourceContents{{URI: "file:///large.txt", Text: large}}},
		},
	}
	svc := appmcp.NewService()
	if err := svc.AddServer("repo", source); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	tools := MCPRuntimeTools(svc, NewApprovalService())

	listResult, err := findRuntimeToolByName(t, tools, "mcp_list_tools").Handler(context.Background(), &core.RunContext{}, `{}`)
	if err != nil {
		t.Fatalf("mcp_list_tools: %v", err)
	}
	listed := listResult.(runtimeMCPToolListResult)
	if len(listed.Tools) != 1 || listed.Tools[0].Server != "repo" || listed.Tools[0].Name != "echo" || !listed.Tools[0].DescriptionTruncated {
		t.Fatalf("listed tools = %#v", listed)
	}

	readResult, err := findRuntimeToolByName(t, tools, "mcp_read_resource").Handler(
		context.Background(),
		&core.RunContext{},
		`{"server":"repo","uri":"file:///large.txt"}`,
	)
	if err != nil {
		t.Fatalf("mcp_read_resource: %v", err)
	}
	resource := readResult.(runtimeMCPResourceResult)
	if !resource.Truncated || len(resource.Text) > runtimeMCPResultMaxBytes || resource.Bytes != len(large) || resource.SHA256 == "" {
		t.Fatalf("bounded resource = %#v", resource)
	}
}

func TestServerRuntimeApprovedMCPCallPersistsMCPItemAndProgress(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	approvals := NewApprovalService()
	source := &runtimeMCPSource{
		tools: []extmcp.Tool{{Name: "echo", Description: "Echo text", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		results: map[string]*extmcp.ToolResult{
			"echo": {Content: []extmcp.Content{{Type: "text", Text: "pong"}}},
		},
	}
	svc := appmcp.NewService()
	if err := svc.AddServer("repo", source); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	model := core.NewTestModel(
		core.ToolCallResponseWithID("mcp_call_tool", `{"server":"repo","tool":"echo","arguments":{"text":"ping"}}`, "call-mcp"),
		core.TextResponse("MCP complete"),
	)
	server := readyServer(
		WithStore(st),
		WithMCP(svc),
		WithApprovalService(approvals),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(MCPRuntimeTools(svc, approvals)...),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "call MCP"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)

	approvalRequest := waitForServerRequest(t, server)
	if approvalRequest.Method != "item/permissions/requestApproval" {
		t.Fatalf("approval method = %q", approvalRequest.Method)
	}
	var approvalParams permissionsApprovalParams
	if err := json.Unmarshal(approvalRequest.Params, &approvalParams); err != nil {
		t.Fatalf("decode MCP approval: %v", err)
	}
	if approvalParams.ThreadID != started.Thread.ID || approvalParams.TurnID != started.Turn.ID || approvalParams.ItemID == "" || approvalParams.ItemID == "call-mcp" {
		t.Fatalf("approval correlation = %#v", approvalParams)
	}
	if approvalParams.Permissions["server"] != "repo" || approvalParams.Permissions["tool"] != "echo" {
		t.Fatalf("approval permissions = %#v", approvalParams.Permissions)
	}
	requestID, _ := approvalRequest.ID.Value().(string)
	if approval := server.HandleRequest(ctx, request("approval/respond", map[string]any{"requestId": requestID, "approved": true})); approval.Error != nil {
		t.Fatalf("approval/respond error: %v", approval.Error)
	}
	notifications := waitForNotificationSet(t, server, "item/mcpToolCall/progress", "turn/completed")
	resolved := findServerRequestResolved(t, notifications, requestID)
	if resolved.ThreadID != started.Thread.ID {
		t.Fatalf("resolved approval thread = %q, want %q", resolved.ThreadID, started.Thread.ID)
	}

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	mcpItem := findRuntimeMCPItem(t, items, "repo", "echo")
	toolItem := findRuntimeToolItem(t, items, "mcp_call_tool", "server", "repo")
	if mcpItem.Item.ParentItemID != toolItem.Item.ID {
		t.Fatalf("MCP item parent = %q, want %q", mcpItem.Item.ParentItemID, toolItem.Item.ID)
	}
	if approvalParams.ItemID != mcpItem.Item.ID {
		t.Fatalf("approval item = %q, want durable MCP item %q", approvalParams.ItemID, mcpItem.Item.ID)
	}
	if mcpItem.Item.Status != runtimeMCPStatusCompleted || mcpItem.Payload.Result == nil || len(mcpItem.Payload.Result.Content) != 1 || mcpItem.Payload.Result.Content[0].Text != "pong" {
		t.Fatalf("MCP item = %#v", mcpItem)
	}
	if !hasRuntimeMCPProgress(t, notifications, mcpItem.Item.ID, "Calling MCP tool") {
		t.Fatalf("MCP progress missing from %v", notificationMethods(notifications))
	}
	if source.callCount() != 1 {
		t.Fatalf("MCP call count = %d", source.callCount())
	}
}

func TestServerRuntimeDeniedMCPCallPersistsFailedItemWithoutCallingSource(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	approvals := NewApprovalService()
	source := &runtimeMCPSource{
		tools:   []extmcp.Tool{{Name: "mutate", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		results: map[string]*extmcp.ToolResult{"mutate": {Content: []extmcp.Content{{Type: "text", Text: "changed"}}}},
	}
	svc := appmcp.NewService()
	if err := svc.AddServer("repo", source); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	model := core.NewTestModel(
		core.ToolCallResponseWithID("mcp_call_tool", `{"server":"repo","tool":"mutate"}`, "call-denied-mcp"),
		core.TextResponse("denied"),
	)
	server := readyServer(
		WithStore(st),
		WithMCP(svc),
		WithApprovalService(approvals),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(MCPRuntimeTools(svc, approvals)...),
		)),
	)
	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "deny MCP"}))
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
	if approval := server.HandleRequest(ctx, request("approval/respond", map[string]any{"requestId": requestID, "approved": false, "message": "denied by test"})); approval.Error != nil {
		t.Fatalf("approval/respond error: %v", approval.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	mcpItem := findRuntimeMCPItem(t, items, "repo", "mutate")
	if mcpItem.Item.Status != runtimeMCPStatusFailed || mcpItem.Payload.Error == nil || !strings.Contains(mcpItem.Payload.Error.Message, "denied") {
		t.Fatalf("denied MCP item = %#v", mcpItem)
	}
	if source.callCount() != 0 {
		t.Fatalf("denied source call count = %d", source.callCount())
	}
}

func TestServerRuntimeInterruptCancelsPendingMCPApproval(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	approvals := NewApprovalService()
	source := &runtimeMCPSource{
		tools:   []extmcp.Tool{{Name: "wait", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		results: map[string]*extmcp.ToolResult{"wait": {Content: []extmcp.Content{{Type: "text", Text: "done"}}}},
	}
	svc := appmcp.NewService()
	if err := svc.AddServer("repo", source); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	model := core.NewTestModel(
		core.ToolCallResponseWithID("mcp_call_tool", `{"server":"repo","tool":"wait"}`, "call-cancel-mcp"),
		core.TextResponse("should not run"),
	)
	server := readyServer(
		WithStore(st),
		WithMCP(svc),
		WithApprovalService(approvals),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(MCPRuntimeTools(svc, approvals)...),
		)),
	)
	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "wait for approval"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	approvalRequest := waitForServerRequest(t, server)
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
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	mcpItem := findRuntimeMCPItem(t, items, "repo", "wait")
	if mcpItem.Item.Status != runtimeMCPStatusFailed || mcpItem.Payload.Error == nil || !strings.Contains(mcpItem.Payload.Error.Message, "canceled") {
		t.Fatalf("interrupted MCP item = %#v", mcpItem)
	}
	requestID, _ := approvalRequest.ID.Value().(string)
	late := server.HandleRequest(ctx, request("approval/respond", map[string]any{"requestId": requestID, "approved": true}))
	if late.Error == nil {
		t.Fatal("late MCP approval should be rejected after interruption")
	}
	if source.callCount() != 0 {
		t.Fatalf("interrupted source call count = %d", source.callCount())
	}
}

func TestRuntimeMCPResultIsBounded(t *testing.T) {
	large := strings.Repeat("result", runtimeMCPResultMaxBytes)
	result := newRuntimeMCPCallResult(appmcp.ToolCallResponse{
		ServerName: "repo",
		ToolName:   "large",
		Result: &extmcp.ToolResult{
			Content: []extmcp.Content{{Type: "text", Text: large}},
		},
		Content: []extmcp.Content{{Type: "text", Text: large}},
		Text:    large,
	})
	if !result.Truncated || len(result.Text) > runtimeMCPResultMaxBytes || result.Bytes <= runtimeMCPResultMaxBytes || result.SHA256 == "" {
		t.Fatalf("bounded MCP result = %#v", result)
	}
	if result.ItemResult == nil || len(result.ItemResult.Content) != 1 || len(result.ItemResult.Content[0].Text) > runtimeMCPResultMaxBytes {
		t.Fatalf("bounded MCP item result = %#v", result.ItemResult)
	}
}

type runtimeMCPSource struct {
	mu        sync.Mutex
	tools     []extmcp.Tool
	results   map[string]*extmcp.ToolResult
	resources map[string]*extmcp.ReadResourceResult
	calls     int
}

func (s *runtimeMCPSource) ListTools(context.Context) ([]extmcp.Tool, error) {
	return append([]extmcp.Tool(nil), s.tools...), nil
}

func (s *runtimeMCPSource) CallTool(_ context.Context, name string, _ map[string]any) (*extmcp.ToolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	result := s.results[name]
	if result == nil {
		return nil, errors.New("tool not found")
	}
	return result, nil
}

func (s *runtimeMCPSource) ListResources(context.Context) ([]extmcp.Resource, error) {
	resources := make([]extmcp.Resource, 0, len(s.resources))
	for uri := range s.resources {
		resources = append(resources, extmcp.Resource{URI: uri})
	}
	return resources, nil
}

func (s *runtimeMCPSource) ReadResource(_ context.Context, uri string) (*extmcp.ReadResourceResult, error) {
	result := s.resources[uri]
	if result == nil {
		return nil, errors.New("resource not found")
	}
	return result, nil
}

func (s *runtimeMCPSource) ListResourceTemplates(context.Context) ([]extmcp.ResourceTemplate, error) {
	return nil, nil
}

func (s *runtimeMCPSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type storedRuntimeMCPItem struct {
	Item    *store.Item
	Payload runtimeMCPToolCallPayload
}

func findRuntimeMCPItem(t *testing.T, items []*store.Item, serverName, toolName string) storedRuntimeMCPItem {
	t.Helper()
	for _, item := range items {
		if item == nil || item.Kind != runtimeMCPToolCallItemKind {
			continue
		}
		var payload runtimeMCPToolCallPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatalf("decode MCP item: %v", err)
		}
		if payload.Server == serverName && payload.Tool == toolName {
			return storedRuntimeMCPItem{Item: item, Payload: payload}
		}
	}
	t.Fatalf("MCP item %s/%s not found in %#v", serverName, toolName, items)
	return storedRuntimeMCPItem{}
}

func hasRuntimeMCPProgress(t *testing.T, notifications []protocol.Notification, itemID, message string) bool {
	t.Helper()
	for _, notification := range notifications {
		if notification.Method != "item/mcpToolCall/progress" {
			continue
		}
		var params runtimeMCPToolProgressNotificationParams
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			t.Fatalf("decode MCP progress: %v", err)
		}
		if params.ItemID == itemID && strings.Contains(params.Message, message) {
			return true
		}
	}
	return false
}

func findServerRequestResolved(t *testing.T, notifications []protocol.Notification, requestID string) serverRequestResolvedParams {
	t.Helper()
	for _, notification := range notifications {
		if notification.Method != "serverRequest/resolved" {
			continue
		}
		var params serverRequestResolvedParams
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			t.Fatalf("decode serverRequest/resolved: %v", err)
		}
		if params.RequestID == requestID {
			return params
		}
	}
	t.Fatalf("serverRequest/resolved %q missing from %v", requestID, notificationMethods(notifications))
	return serverRequestResolvedParams{}
}

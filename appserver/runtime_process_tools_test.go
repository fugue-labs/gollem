package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	toolprocess "github.com/fugue-labs/gollem/appserver/tools/process"
	"github.com/fugue-labs/gollem/core"
)

func TestProcessRuntimeToolsExposeScopedOperations(t *testing.T) {
	processSvc, err := toolprocess.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	tools := ProcessRuntimeTools(processSvc)
	want := map[string]struct {
		sequential      bool
		concurrencySafe bool
	}{
		"workspace_run_command":    {sequential: true},
		"workspace_list_processes": {concurrencySafe: true},
		"workspace_process_status": {concurrencySafe: true},
	}
	if len(tools) != len(want) {
		t.Fatalf("runtime process tools = %d, want %d", len(tools), len(want))
	}
	for _, tool := range tools {
		expected, ok := want[tool.Definition.Name]
		if !ok {
			t.Fatalf("unexpected runtime process tool %q", tool.Definition.Name)
		}
		if tool.Definition.Namespace != runtimeProcessToolNamespace {
			t.Fatalf("tool %q namespace = %q", tool.Definition.Name, tool.Definition.Namespace)
		}
		if tool.Definition.Sequential != expected.sequential || tool.Definition.ConcurrencySafe != expected.concurrencySafe {
			t.Fatalf("tool %q flags sequential=%v concurrencySafe=%v", tool.Definition.Name, tool.Definition.Sequential, tool.Definition.ConcurrencySafe)
		}
	}
	if ProcessRuntimeTools(nil) != nil {
		t.Fatal("nil process service should expose no tools")
	}
}

func TestServerRuntimeApprovedCommandPersistsCommandExecution(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	approvals := NewApprovalService()
	processSvc, err := toolprocess.NewService(t.TempDir(), toolprocess.WithApproval(approvals.ProcessApproval))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	model := core.NewTestModel(
		core.ToolCallResponseWithID(
			"workspace_run_command",
			`{"command":"printf","args":["runtime stdout"]}`,
			"call-command",
		),
		core.TextResponse("command complete"),
	)
	server := readyServer(
		WithStore(st),
		WithProcess(processSvc),
		WithApprovalService(approvals),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(ProcessRuntimeTools(processSvc)...),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "run a command"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)

	approvalRequest := waitForServerRequest(t, server)
	if approvalRequest.Method != "item/commandExecution/requestApproval" {
		t.Fatalf("approval method = %q", approvalRequest.Method)
	}
	var approvalParams commandApprovalParams
	if err := json.Unmarshal(approvalRequest.Params, &approvalParams); err != nil {
		t.Fatalf("decode command approval: %v", err)
	}
	if approvalParams.ThreadID != started.Thread.ID || approvalParams.TurnID != started.Turn.ID || approvalParams.ItemID == "" || approvalParams.ItemID == "call-command" {
		t.Fatalf("approval correlation = %#v", approvalParams)
	}
	if approvalParams.Command != "printf runtime stdout" || approvalParams.Operation != string(toolprocess.OperationStart) {
		t.Fatalf("approval command = %#v", approvalParams)
	}
	requestID, _ := approvalRequest.ID.Value().(string)
	approval := server.HandleRequest(ctx, request("approval/respond", map[string]any{
		"requestId": requestID,
		"approved":  true,
	}))
	if approval.Error != nil {
		t.Fatalf("approval/respond error: %v", approval.Error)
	}
	notifications := waitForNotificationSet(t, server, "item/commandExecution/outputDelta", "turn/completed")

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	commandItem := findRuntimeCommandItem(t, items, "printf")
	toolItem := findRuntimeToolItem(t, items, "workspace_run_command", "command", "printf")
	if commandItem.Item.ParentItemID != toolItem.Item.ID {
		t.Fatalf("command item parent = %q, want %q", commandItem.Item.ParentItemID, toolItem.Item.ID)
	}
	if approvalParams.ItemID != commandItem.Item.ID {
		t.Fatalf("approval item = %q, want durable command item %q", approvalParams.ItemID, commandItem.Item.ID)
	}
	if commandItem.Item.Status != commandExecutionStatusCompleted || commandItem.Payload.Status != commandExecutionStatusCompleted {
		t.Fatalf("command status = %q/%q", commandItem.Item.Status, commandItem.Payload.Status)
	}
	if commandItem.Payload.Source != commandExecutionSourceAgent || commandItem.Payload.ProcessID == nil || commandItem.Payload.ExitCode == nil || *commandItem.Payload.ExitCode != 0 {
		t.Fatalf("command payload = %#v", commandItem.Payload)
	}
	if commandItem.Payload.AggregatedOutput == nil || *commandItem.Payload.AggregatedOutput != "runtime stdout" {
		t.Fatalf("command output = %#v", commandItem.Payload.AggregatedOutput)
	}
	if !hasRuntimeCommandDelta(t, notifications, commandItem.Item.ID, "runtime stdout") {
		t.Fatalf("command delta missing from %v", notificationMethods(notifications))
	}
}

func TestServerRuntimeInterruptKillsOwnedCommand(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	approvals := NewApprovalService()
	var audits []toolprocess.AuditEvent
	var auditMu sync.Mutex
	processSvc, err := toolprocess.NewService(t.TempDir(),
		toolprocess.WithApproval(approvals.ProcessApproval),
		toolprocess.WithAuditSink(func(event toolprocess.AuditEvent) {
			auditMu.Lock()
			defer auditMu.Unlock()
			audits = append(audits, event)
		}),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	model := core.NewTestModel(
		core.ToolCallResponseWithID(
			"workspace_run_command",
			`{"command":"sh","args":["-c","printf ready; sleep 30"]}`,
			"call-interrupt-command",
		),
		core.TextResponse("should not run"),
	)
	server := readyServer(
		WithStore(st),
		WithProcess(processSvc),
		WithApprovalService(approvals),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(ProcessRuntimeTools(processSvc)...),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "run until interrupted"}))
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
	if approval := server.HandleRequest(ctx, request("approval/respond", map[string]any{"requestId": requestID, "approved": true})); approval.Error != nil {
		t.Fatalf("approval/respond error: %v", approval.Error)
	}
	waitForNotificationSet(t, server, "item/commandExecution/outputDelta")
	interrupt := server.HandleRequest(ctx, request("turn/interrupt", map[string]any{"turnId": started.Turn.ID}))
	if interrupt.Error != nil {
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
	processes, err := processSvc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(processes) != 1 || processes[0].Status == toolprocess.StatusRunning {
		t.Fatalf("processes after interrupt = %+v", processes)
	}
	if pending := server.DrainRequests(); len(pending) != 0 {
		t.Fatalf("unexpected second approval request: %#v", pending)
	}
	auditMu.Lock()
	defer auditMu.Unlock()
	if len(audits) < 2 || audits[len(audits)-1].Operation.Kind != toolprocess.OperationCancel || !audits[len(audits)-1].Allowed {
		t.Fatalf("process audits = %+v", audits)
	}
}

func TestServerRuntimeDeniedCommandPersistsDeclinedItem(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	approvals := NewApprovalService()
	processSvc, err := toolprocess.NewService(t.TempDir(), toolprocess.WithApproval(approvals.ProcessApproval))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	model := core.NewTestModel(
		core.ToolCallResponseWithID(
			"workspace_run_command",
			`{"command":"printf","args":["must not run"]}`,
			"call-denied-command",
		),
		core.TextResponse("command was denied"),
	)
	server := readyServer(
		WithStore(st),
		WithProcess(processSvc),
		WithApprovalService(approvals),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(ProcessRuntimeTools(processSvc)...),
		)),
	)
	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "try a denied command"}))
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
	if approval := server.HandleRequest(ctx, request("approval/respond", map[string]any{
		"requestId": requestID,
		"approved":  false,
		"message":   "denied by test",
	})); approval.Error != nil {
		t.Fatalf("approval/respond error: %v", approval.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")
	processes, err := processSvc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(processes) != 0 {
		t.Fatalf("denied command processes = %+v", processes)
	}
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	commandItem := findRuntimeCommandItem(t, items, "printf")
	if commandItem.Item.Status != commandExecutionStatusDeclined || commandItem.Payload.Status != commandExecutionStatusDeclined || commandItem.Payload.ProcessID != nil {
		t.Fatalf("declined command item = %#v", commandItem)
	}
	toolItem := findRuntimeToolItem(t, items, "workspace_run_command", "command", "printf")
	if toolItem.Status != runtimeToolStatusFailed {
		t.Fatalf("denied dynamic tool status = %q", toolItem.Status)
	}
}

func TestServerRuntimeCommandOutputIsBounded(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	processSvc, err := toolprocess.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	model := core.NewTestModel(
		core.ToolCallResponseWithID(
			"workspace_run_command",
			`{"command":"sh","args":["-c","yes output | head -c 131072"]}`,
			"call-large-command",
		),
		core.TextResponse("large command complete"),
	)
	server := readyServer(
		WithStore(st),
		WithProcess(processSvc),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(ProcessRuntimeTools(processSvc)...),
		)),
	)
	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "run a noisy command"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	notifications := waitForNotificationSet(t, server, "turn/completed")
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	commandItem := findRuntimeCommandItem(t, items, "sh")
	if commandItem.Payload.AggregatedOutput == nil || len(*commandItem.Payload.AggregatedOutput) > runtimeProcessOutputMaxBytes || !strings.Contains(*commandItem.Payload.AggregatedOutput, runtimeCommandOutputTruncatedMarker) {
		t.Fatalf("bounded command output bytes=%d payload=%#v", runtimeStringLength(commandItem.Payload.AggregatedOutput), commandItem.Payload.AggregatedOutput)
	}
	var streamed strings.Builder
	for _, notification := range notifications {
		if notification.Method != "item/commandExecution/outputDelta" {
			continue
		}
		var params commandExecutionOutputDeltaNotificationParams
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			t.Fatalf("decode command output delta: %v", err)
		}
		if params.ItemID == commandItem.Item.ID {
			streamed.WriteString(params.Delta)
		}
	}
	if streamed.Len() > runtimeProcessOutputMaxBytes || !strings.Contains(streamed.String(), runtimeCommandOutputTruncatedMarker) {
		t.Fatalf("streamed command output bytes=%d marker=%v", streamed.Len(), strings.Contains(streamed.String(), runtimeCommandOutputTruncatedMarker))
	}
}

type storedRuntimeCommandItem struct {
	Item    *store.Item
	Payload threadShellCommandPayload
}

func findRuntimeCommandItem(t *testing.T, items []*store.Item, command string) storedRuntimeCommandItem {
	t.Helper()
	for _, item := range items {
		if item == nil || item.Kind != threadShellCommandItemKind {
			continue
		}
		var payload threadShellCommandPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatalf("decode command item: %v", err)
		}
		if strings.HasPrefix(payload.Command, command) {
			return storedRuntimeCommandItem{Item: item, Payload: payload}
		}
	}
	t.Fatalf("command item %q not found in %#v", command, items)
	return storedRuntimeCommandItem{}
}

func hasRuntimeCommandDelta(t *testing.T, notifications []protocol.Notification, itemID, content string) bool {
	t.Helper()
	for _, notification := range notifications {
		if notification.Method != "item/commandExecution/outputDelta" {
			continue
		}
		var params commandExecutionOutputDeltaNotificationParams
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			t.Fatalf("decode command output delta: %v", err)
		}
		if params.ItemID == itemID && strings.Contains(params.Delta, content) {
			return true
		}
	}
	return false
}

func runtimeStringLength(value *string) int {
	if value == nil {
		return 0
	}
	return len(*value)
}

func TestRuntimeProcessTimeoutValidation(t *testing.T) {
	processSvc, err := toolprocess.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	tool := findRuntimeToolByName(t, ProcessRuntimeTools(processSvc), "workspace_run_command")
	_, err = tool.Handler(context.Background(), &core.RunContext{}, `{"command":"printf","timeoutSeconds":999999}`)
	if err == nil || !strings.Contains(err.Error(), "timeoutSeconds") {
		t.Fatalf("timeout validation error = %v", err)
	}
}

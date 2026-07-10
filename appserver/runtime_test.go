package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

func TestServerRuntimeThreadStartCompletesTurn(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(core.TextResponse("runtime answer"))
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{
		"title":    "Runtime",
		"prompt":   "hello runtime",
		"provider": "test",
		"model":    "test-model",
	}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	if started.Thread.ID == "" || started.Turn.ID == "" || started.Turn.Status != store.TurnRunning {
		t.Fatalf("thread/start result = %#v", started)
	}

	waitForNotificationSet(t, server, "thread/started", "turn/started", "item/agentMessage/delta", "item/completed", "turn/completed")
	readResp := server.HandleRequest(ctx, request("thread/read", map[string]any{"threadId": started.Thread.ID}))
	if readResp.Error != nil {
		t.Fatalf("thread/read error: %v", readResp.Error)
	}
	var read threadReadResult
	decodeResult(t, readResp, &read)
	if len(read.Turns) != 1 || read.Turns[0].Status != store.TurnCompleted {
		t.Fatalf("turns = %#v", read.Turns)
	}
	if len(read.Items) != 2 {
		t.Fatalf("items = %#v", read.Items)
	}
	if got := runtimePromptFromInput(read.Turns[0].Input); got != "hello runtime" {
		t.Fatalf("turn input prompt = %q", got)
	}
	if len(model.Calls()) != 1 {
		t.Fatalf("model calls = %d, want 1", len(model.Calls()))
	}
}

func TestServerRuntimeThreadResumeUsesPersistedHistory(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(core.TextResponse("first answer"), core.TextResponse("second answer"))
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "first prompt"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForNotificationSet(t, server, "turn/completed")

	resumeResp := server.HandleRequest(ctx, request("thread/resume", map[string]any{
		"threadId": started.Thread.ID,
		"prompt":   "second prompt",
	}))
	if resumeResp.Error != nil {
		t.Fatalf("thread/resume error: %v", resumeResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	secondMessages := calls[1].Messages
	if len(secondMessages) != 3 {
		t.Fatalf("second call messages = %#v", secondMessages)
	}
	assertRuntimeUserPrompt(t, secondMessages[0], "first prompt")
	assertRuntimeAssistantText(t, secondMessages[1], "first answer")
	assertRuntimeUserPrompt(t, secondMessages[2], "second prompt")
}

func TestServerRuntimePublishesThreadTokenUsageUpdated(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	first := core.TextResponse("first answer")
	first.Usage = core.Usage{
		InputTokens:     10,
		OutputTokens:    4,
		CacheReadTokens: 3,
		Details: map[string]int{
			"reasoning_tokens": 2,
		},
	}
	second := core.TextResponse("second answer")
	second.Usage = core.Usage{
		InputTokens:     5,
		OutputTokens:    6,
		CacheReadTokens: 1,
		Details: map[string]int{
			"reasoning_tokens": 1,
		},
	}
	model := core.NewTestModel(first, second)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "first prompt"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	firstEvents := waitForNotificationSet(t, server, "thread/tokenUsage/updated", "turn/completed")
	firstUsage := decodeThreadTokenUsageNotification(t, firstEvents)
	if firstUsage.ThreadID != started.Thread.ID || firstUsage.TurnID != started.Turn.ID {
		t.Fatalf("first usage ids = thread %q turn %q, want %q/%q", firstUsage.ThreadID, firstUsage.TurnID, started.Thread.ID, started.Turn.ID)
	}
	if firstUsage.TokenUsage.ModelContextWindow != nil {
		t.Fatalf("first modelContextWindow = %v, want nil", *firstUsage.TokenUsage.ModelContextWindow)
	}
	assertTokenUsageBreakdown(t, "first last", firstUsage.TokenUsage.Last, 14, 10, 3, 4, 2)
	assertTokenUsageBreakdown(t, "first total", firstUsage.TokenUsage.Total, 14, 10, 3, 4, 2)

	resumeResp := server.HandleRequest(ctx, request("thread/resume", map[string]any{
		"threadId": started.Thread.ID,
		"prompt":   "second prompt",
	}))
	if resumeResp.Error != nil {
		t.Fatalf("thread/resume error: %v", resumeResp.Error)
	}
	var resumed struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, resumeResp, &resumed)
	secondEvents := waitForNotificationSet(t, server, "thread/tokenUsage/updated", "turn/completed")
	secondUsage := decodeThreadTokenUsageNotification(t, secondEvents)
	if secondUsage.ThreadID != started.Thread.ID || secondUsage.TurnID != resumed.Turn.ID {
		t.Fatalf("second usage ids = thread %q turn %q, want %q/%q", secondUsage.ThreadID, secondUsage.TurnID, started.Thread.ID, resumed.Turn.ID)
	}
	if secondUsage.TokenUsage.ModelContextWindow != nil {
		t.Fatalf("second modelContextWindow = %v, want nil", *secondUsage.TokenUsage.ModelContextWindow)
	}
	assertTokenUsageBreakdown(t, "second last", secondUsage.TokenUsage.Last, 11, 5, 1, 6, 1)
	assertTokenUsageBreakdown(t, "second total", secondUsage.TokenUsage.Total, 25, 15, 4, 10, 3)
}

func TestServerRuntimePersistsDynamicToolCallLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	type echoParams struct {
		Text string `json:"text"`
	}
	echo := core.FuncTool[echoParams]("echo", "Echo text.", func(_ context.Context, params echoParams) (string, error) {
		return "echo: " + params.Text, nil
	})
	echo.Definition.Namespace = "utility"
	unused := core.FuncTool[struct{}]("unused", "Unused test tool.", func(context.Context, struct{}) (string, error) {
		return "unused", nil
	})
	model := core.NewTestModel(
		core.ToolCallResponseWithID("echo", `{"text":"hello"}`, "call-echo"),
		core.TextResponse("tool finished"),
	)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(echo),
			WithRuntimeTools(unused),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "use echo"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	events := waitForNotificationSet(t, server,
		"item/started",
		"item/completed",
		"item/completed",
		"turn/completed",
	)
	toolEvents := runtimeToolNotifications(t, events)
	if len(toolEvents) != 2 {
		t.Fatalf("dynamic tool notifications = %#v, want started and completed", toolEvents)
	}
	startedNotice := toolEvents[0]
	completedNotice := toolEvents[1]
	if startedNotice.Method != "item/started" || startedNotice.Params.StartedAtMS <= 0 {
		t.Fatalf("started tool notification = %#v", startedNotice)
	}
	if completedNotice.Method != "item/completed" || completedNotice.Params.CompletedAtMS <= 0 {
		t.Fatalf("completed tool notification = %#v", completedNotice)
	}
	if startedNotice.Params.ThreadID != started.Thread.ID || startedNotice.Params.TurnID != started.Turn.ID {
		t.Fatalf("started tool ids = %#v, want %q/%q", startedNotice.Params, started.Thread.ID, started.Turn.ID)
	}
	if startedNotice.Params.Item.ID == "" || completedNotice.Params.Item.ID != startedNotice.Params.Item.ID {
		t.Fatalf("tool item ids = started %q completed %q", startedNotice.Params.Item.ID, completedNotice.Params.Item.ID)
	}
	if startedNotice.Params.Item.Tool != "echo" || startedNotice.Params.Item.Status != "inProgress" || startedNotice.Params.Item.Success != nil {
		t.Fatalf("started tool item = %#v", startedNotice.Params.Item)
	}
	if startedNotice.Params.Item.Namespace == nil || *startedNotice.Params.Item.Namespace != "utility" {
		t.Fatalf("started tool namespace = %v", startedNotice.Params.Item.Namespace)
	}
	if got := startedNotice.Params.Item.Arguments["text"]; got != "hello" {
		t.Fatalf("started tool arguments = %#v", startedNotice.Params.Item.Arguments)
	}
	if completedNotice.Params.Item.Status != "completed" || completedNotice.Params.Item.Success == nil || !*completedNotice.Params.Item.Success {
		t.Fatalf("completed tool item = %#v", completedNotice.Params.Item)
	}
	if completedNotice.Params.Item.DurationMS == nil || *completedNotice.Params.Item.DurationMS < 0 {
		t.Fatalf("completed tool duration = %v", completedNotice.Params.Item.DurationMS)
	}
	if len(completedNotice.Params.Item.ContentItems) != 1 || !strings.Contains(completedNotice.Params.Item.ContentItems[0].Text, "echo: hello") {
		t.Fatalf("completed tool content = %#v", completedNotice.Params.Item.ContentItems)
	}

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	toolItem := findRuntimeToolItem(t, items, "echo", "text", "hello")
	if toolItem.Status != "completed" || toolItem.Payload.ID != toolItem.Item.ID || toolItem.Payload.Tool != "echo" {
		t.Fatalf("stored tool item = %#v", toolItem)
	}
}

func TestServerRuntimePersistsFailedDynamicToolCall(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	type failParams struct {
		Reason string `json:"reason"`
	}
	failing := core.FuncTool[failParams]("failing", "Fail with a reason.", func(_ context.Context, params failParams) (string, error) {
		return "", errors.New(params.Reason)
	})
	model := core.NewTestModel(
		core.ToolCallResponseWithID("failing", `{"reason":"boom"}`, "call-failing"),
		core.TextResponse("failure handled"),
	)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(failing),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "call failing"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	events := waitForNotificationSet(t, server,
		"item/started",
		"item/completed",
		"item/completed",
		"turn/completed",
	)
	toolEvents := runtimeToolNotifications(t, events)
	if len(toolEvents) != 2 {
		t.Fatalf("dynamic tool notifications = %#v, want started and failed completion", toolEvents)
	}
	failed := toolEvents[1].Params.Item
	if failed.Status != "failed" || failed.Success == nil || *failed.Success {
		t.Fatalf("failed tool item = %#v", failed)
	}
	if len(failed.ContentItems) != 1 || !strings.Contains(failed.ContentItems[0].Text, "boom") {
		t.Fatalf("failed tool content = %#v", failed.ContentItems)
	}

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	toolItem := findRuntimeToolItem(t, items, "failing", "reason", "boom")
	if toolItem.Status != "failed" || toolItem.Payload.Success == nil || *toolItem.Payload.Success {
		t.Fatalf("stored failed tool item = %#v", toolItem)
	}
}

func TestServerRuntimeTracksConcurrentDynamicToolCalls(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	type parallelParams struct {
		Value string `json:"value"`
	}
	entered := make(chan string, 2)
	release := make(chan struct{})
	parallel := core.FuncTool[parallelParams]("parallel", "Run concurrently.", func(ctx context.Context, params parallelParams) (string, error) {
		entered <- params.Value
		select {
		case <-release:
			return "done: " + params.Value, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}, core.WithToolConcurrencySafe(true))
	model := core.NewTestModel(
		core.MultiToolCallResponse(
			core.ToolCallPart{ToolName: "parallel", ToolCallID: "call-a", ArgsJSON: `{"value":"a"}`},
			core.ToolCallPart{ToolName: "parallel", ToolCallID: "call-b", ArgsJSON: `{"value":"b"}`},
		),
		core.TextResponse("parallel finished"),
	)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(parallel),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "run both"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	seenValues := map[string]bool{}
	for len(seenValues) < 2 {
		select {
		case value := <-entered:
			seenValues[value] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("tool calls did not run concurrently; entered=%v", seenValues)
		}
	}
	close(release)
	events := waitForNotificationSet(t, server,
		"item/started",
		"item/started",
		"item/completed",
		"item/completed",
		"item/completed",
		"turn/completed",
	)
	toolEvents := runtimeToolNotifications(t, events)
	if len(toolEvents) != 4 {
		t.Fatalf("dynamic tool notifications = %#v, want two starts and two completions", toolEvents)
	}
	completedIDs := map[string]bool{}
	for _, event := range toolEvents {
		if event.Method != "item/completed" {
			continue
		}
		if event.Params.Item.Status != "completed" || event.Params.Item.Success == nil || !*event.Params.Item.Success {
			t.Fatalf("concurrent completed tool item = %#v", event.Params.Item)
		}
		completedIDs[event.Params.Item.ID] = true
	}
	if len(completedIDs) != 2 {
		t.Fatalf("completed concurrent tool ids = %#v", completedIDs)
	}

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if findRuntimeToolItem(t, items, "parallel", "value", "a").Status != "completed" || findRuntimeToolItem(t, items, "parallel", "value", "b").Status != "completed" {
		t.Fatalf("concurrent tool items were not completed: %#v", items)
	}
}

func TestServerRuntimeThreadResumeUsesInjectedResponseItems(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(core.TextResponse("after injection"))
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Injected history"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	injectResp := server.HandleRequest(ctx, request("thread/inject_items", map[string]any{
		"threadId": thread.ID,
		"items": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "injected user"},
				},
			},
			map[string]any{
				"type":  "message",
				"role":  "assistant",
				"model": "prior-model",
				"content": []any{
					map[string]any{"type": "output_text", "text": "injected assistant"},
				},
			},
		},
	}))
	if injectResp.Error != nil {
		t.Fatalf("thread/inject_items error: %v", injectResp.Error)
	}
	server.DrainNotifications()

	resumeResp := server.HandleRequest(ctx, request("thread/resume", map[string]any{
		"threadId": thread.ID,
		"prompt":   "next prompt",
	}))
	if resumeResp.Error != nil {
		t.Fatalf("thread/resume error: %v", resumeResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(calls))
	}
	messages := calls[0].Messages
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	assertRuntimeUserPrompt(t, messages[0], "injected user")
	assertRuntimeAssistantText(t, messages[1], "injected assistant")
	assertRuntimeUserPrompt(t, messages[2], "next prompt")
}

func TestServerRuntimeThreadCompactBoundsResumeHistory(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(
		core.TextResponse("first answer"),
		core.TextResponse("second answer"),
		core.TextResponse("third answer"),
	)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "first prompt"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForNotificationSet(t, server, "turn/completed")

	resumeResp := server.HandleRequest(ctx, request("thread/resume", map[string]any{
		"threadId": started.Thread.ID,
		"prompt":   "second prompt",
	}))
	if resumeResp.Error != nil {
		t.Fatalf("thread/resume error: %v", resumeResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	compactResp := server.HandleRequest(ctx, request("thread/compact/start", map[string]any{"threadId": started.Thread.ID}))
	if compactResp.Error != nil {
		t.Fatalf("thread/compact/start error: %v", compactResp.Error)
	}
	waitForNotificationSet(t, server, "thread/compacted")

	resumeResp = server.HandleRequest(ctx, request("thread/resume", map[string]any{
		"threadId": started.Thread.ID,
		"prompt":   "third prompt",
	}))
	if resumeResp.Error != nil {
		t.Fatalf("thread/resume third error: %v", resumeResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	calls := model.Calls()
	if len(calls) != 3 {
		t.Fatalf("model calls = %d, want 3", len(calls))
	}
	messages := calls[2].Messages
	if len(messages) != 2 {
		t.Fatalf("third call messages = %#v", messages)
	}
	assertRuntimeSystemPromptContains(t, messages[0], "first prompt", "second answer")
	assertRuntimeUserPrompt(t, messages[1], "third prompt")
}

func TestServerRuntimeTurnRetryBranchesBeforeSourceTurn(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(core.TextResponse("first answer"), core.TextResponse("retry answer"))
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "original prompt"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForNotificationSet(t, server, "turn/completed")

	retryResp := server.HandleRequest(ctx, request("turn/retry", map[string]any{"turnId": started.Turn.ID}))
	if retryResp.Error != nil {
		t.Fatalf("turn/retry error: %v", retryResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	if len(calls[1].Messages) != 1 {
		t.Fatalf("retry messages = %#v", calls[1].Messages)
	}
	assertRuntimeUserPrompt(t, calls[1].Messages[0], "original prompt")
}

func TestServerRuntimeTurnInterruptCancelsActiveRun(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := &blockingRuntimeModel{started: make(chan struct{})}
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "blocking"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "block"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForBlockingModel(t, model)

	interruptResp := server.HandleRequest(ctx, request("turn/interrupt", map[string]any{"turnId": started.Turn.ID}))
	if interruptResp.Error != nil {
		t.Fatalf("turn/interrupt error: %v", interruptResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")
	turn, err := st.GetTurn(ctx, started.Turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if turn.Status != store.TurnInterrupted {
		t.Fatalf("turn status = %s, want interrupted; error=%q", turn.Status, turn.Error)
	}
}

func TestRuntimeShutdownCancelsActiveRunBeforeStoreClose(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := &blockingRuntimeModel{started: make(chan struct{})}
	runtimeSvc := NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "blocking"}))
	server := readyServer(
		WithStore(st),
		WithRuntimeService(runtimeSvc),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "block"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForBlockingModel(t, model)

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := runtimeSvc.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	turn, err := st.GetTurn(ctx, started.Turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if turn.Status != store.TurnInterrupted {
		t.Fatalf("turn status = %s, want interrupted; error=%q", turn.Status, turn.Error)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close store after runtime shutdown: %v", err)
	}
}

func TestRuntimeShutdownRejectsNewStarts(t *testing.T) {
	ctx := context.Background()
	runtimeSvc := NewRuntimeService(WithRuntimeModel(
		core.NewTestModel(core.TextResponse("late")),
		RuntimeModelInfo{ProviderID: "test", Model: "test-model"},
	))
	server := readyServer(
		WithStore(newRuntimeTestStore(t)),
		WithRuntimeService(runtimeSvc),
	)
	if err := runtimeSvc.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "after shutdown"}))
	if resp.Error == nil || resp.Error.Code != protocol.CodeMethodUnavailable {
		t.Fatalf("thread/start after shutdown error = %#v, want method unavailable", resp.Error)
	}
	var data protocol.UnavailableData
	if err := json.Unmarshal(resp.Error.Data, &data); err != nil {
		t.Fatalf("decode unavailable data: %v", err)
	}
	if data.Method != "thread/start" || data.Reason != "turn runtime is shutting down" {
		t.Fatalf("unavailable data = %+v", data)
	}
}

func TestServerRuntimeTurnSteerRecordsActiveMessage(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := &blockingRuntimeModel{started: make(chan struct{})}
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "blocking"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "block"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForBlockingModel(t, model)

	steerResp := server.HandleRequest(ctx, request("turn/steer", map[string]any{
		"turnId":  started.Turn.ID,
		"message": "adjust course",
	}))
	if steerResp.Error != nil {
		t.Fatalf("turn/steer error: %v", steerResp.Error)
	}
	var steer struct {
		Accepted bool        `json:"accepted"`
		Item     *store.Item `json:"item"`
	}
	decodeResult(t, steerResp, &steer)
	if !steer.Accepted || steer.Item == nil || steer.Item.Kind != "steer" || steer.Item.Status != "queued" {
		t.Fatalf("turn/steer result = %#v", steer)
	}

	interruptResp := server.HandleRequest(ctx, request("turn/interrupt", map[string]any{"turnId": started.Turn.ID}))
	if interruptResp.Error != nil {
		t.Fatalf("turn/interrupt error: %v", interruptResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")
}

func newRuntimeTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func waitForNotificationSet(t *testing.T, server *Server, want ...string) []protocol.Notification {
	t.Helper()
	remaining := make(map[string]int, len(want))
	for _, method := range want {
		remaining[method]++
	}
	var seen []protocol.Notification
	timeout := time.After(3 * time.Second)
	for len(remaining) > 0 {
		select {
		case <-server.NotificationSignal():
			for _, notification := range server.DrainNotifications() {
				seen = append(seen, notification)
				if remaining[notification.Method] > 1 {
					remaining[notification.Method]--
				} else if remaining[notification.Method] == 1 {
					delete(remaining, notification.Method)
				}
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %v; seen=%v", remaining, notificationMethods(seen))
		}
	}
	return seen
}

func notificationMethods(notifications []protocol.Notification) []string {
	methods := make([]string, len(notifications))
	for i, notification := range notifications {
		methods[i] = notification.Method
	}
	return methods
}

type runtimeToolNotification struct {
	Method string
	Params struct {
		ThreadID      string                         `json:"threadId"`
		TurnID        string                         `json:"turnId"`
		Item          runtimeDynamicToolCallTestItem `json:"item"`
		StartedAtMS   int64                          `json:"startedAtMs"`
		CompletedAtMS int64                          `json:"completedAtMs"`
	}
}

type runtimeDynamicToolCallTestItem struct {
	Type         string                                  `json:"type"`
	ID           string                                  `json:"id"`
	Namespace    *string                                 `json:"namespace"`
	Tool         string                                  `json:"tool"`
	Arguments    map[string]any                          `json:"arguments"`
	Status       string                                  `json:"status"`
	ContentItems []runtimeDynamicToolCallTestContentItem `json:"contentItems"`
	Success      *bool                                   `json:"success"`
	DurationMS   *int64                                  `json:"durationMs"`
}

type runtimeDynamicToolCallTestContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func runtimeToolNotifications(t *testing.T, notifications []protocol.Notification) []runtimeToolNotification {
	t.Helper()
	var out []runtimeToolNotification
	for _, notification := range notifications {
		if notification.Method != "item/started" && notification.Method != "item/completed" {
			continue
		}
		var decoded runtimeToolNotification
		decoded.Method = notification.Method
		if err := json.Unmarshal(notification.Params, &decoded.Params); err != nil {
			t.Fatalf("decode %s: %v", notification.Method, err)
		}
		if decoded.Params.Item.Type == "dynamicToolCall" {
			out = append(out, decoded)
		}
	}
	return out
}

type storedRuntimeToolItem struct {
	Item    *store.Item
	Status  string
	Payload runtimeDynamicToolCallTestItem
}

func findRuntimeToolItem(t *testing.T, items []*store.Item, tool, argumentKey string, argumentValue any) storedRuntimeToolItem {
	t.Helper()
	for _, item := range items {
		if item == nil || item.Kind != "dynamicToolCall" {
			continue
		}
		var payload runtimeDynamicToolCallTestItem
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatalf("decode stored dynamic tool item: %v", err)
		}
		if payload.Tool == tool && payload.Arguments[argumentKey] == argumentValue {
			return storedRuntimeToolItem{Item: item, Status: item.Status, Payload: payload}
		}
	}
	t.Fatalf("dynamic tool item %q with %s=%v not found in %#v", tool, argumentKey, argumentValue, items)
	return storedRuntimeToolItem{}
}

func decodeThreadTokenUsageNotification(t *testing.T, notifications []protocol.Notification) threadTokenUsageUpdatedNotificationParams {
	t.Helper()
	for _, notification := range notifications {
		if notification.Method != "thread/tokenUsage/updated" {
			continue
		}
		var params threadTokenUsageUpdatedNotificationParams
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			t.Fatalf("decode thread/tokenUsage/updated params: %v", err)
		}
		return params
	}
	t.Fatalf("thread/tokenUsage/updated notification missing from %v", notificationMethods(notifications))
	return threadTokenUsageUpdatedNotificationParams{}
}

func assertTokenUsageBreakdown(t *testing.T, label string, got tokenUsageBreakdown, total, input, cachedInput, output, reasoningOutput int64) {
	t.Helper()
	want := tokenUsageBreakdown{
		TotalTokens:           total,
		InputTokens:           input,
		CachedInputTokens:     cachedInput,
		OutputTokens:          output,
		ReasoningOutputTokens: reasoningOutput,
	}
	if got != want {
		t.Fatalf("%s usage = %+v, want %+v", label, got, want)
	}
}

func assertRuntimeUserPrompt(t *testing.T, message core.ModelMessage, want string) {
	t.Helper()
	req, ok := message.(core.ModelRequest)
	if !ok || len(req.Parts) != 1 {
		t.Fatalf("message = %#v, want one-part user request", message)
	}
	part, ok := req.Parts[0].(core.UserPromptPart)
	if !ok || part.Content != want {
		t.Fatalf("request part = %#v, want user prompt %q", req.Parts[0], want)
	}
}

func assertRuntimeSystemPromptContains(t *testing.T, message core.ModelMessage, want ...string) {
	t.Helper()
	req, ok := message.(core.ModelRequest)
	if !ok || len(req.Parts) != 1 {
		t.Fatalf("message = %#v, want one-part system request", message)
	}
	part, ok := req.Parts[0].(core.SystemPromptPart)
	if !ok {
		t.Fatalf("request part = %#v, want system prompt", req.Parts[0])
	}
	for _, text := range want {
		if !strings.Contains(part.Content, text) {
			t.Fatalf("system prompt = %q, want substring %q", part.Content, text)
		}
	}
}

func assertRuntimeAssistantText(t *testing.T, message core.ModelMessage, want string) {
	t.Helper()
	resp, ok := message.(core.ModelResponse)
	if !ok || resp.TextContent() != want {
		t.Fatalf("message = %#v, want assistant text %q", message, want)
	}
}

type blockingRuntimeModel struct {
	started chan struct{}
}

func (m *blockingRuntimeModel) Request(ctx context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *blockingRuntimeModel) RequestStream(ctx context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
	close(m.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *blockingRuntimeModel) ModelName() string {
	return "blocking"
}

func waitForBlockingModel(t *testing.T, model *blockingRuntimeModel) {
	t.Helper()
	select {
	case <-model.started:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking model did not start")
	}
}

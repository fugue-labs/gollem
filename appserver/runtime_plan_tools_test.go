package appserver

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

type runtimePlanCaptureNotifier struct {
	mu     sync.Mutex
	events []protocol.Notification
}

func (n *runtimePlanCaptureNotifier) PublishNotification(method string, params any) {
	encoded, err := json.Marshal(params)
	if err != nil {
		panic(err)
	}
	n.mu.Lock()
	n.events = append(n.events, protocol.Notification{Method: method, Params: encoded})
	n.mu.Unlock()
}

func (n *runtimePlanCaptureNotifier) snapshot() []protocol.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]protocol.Notification(nil), n.events...)
}

func TestPlanRuntimeToolsExposeExactSequentialUpdatePlan(t *testing.T) {
	tools := PlanRuntimeTools()
	if len(tools) != 1 {
		t.Fatalf("plan runtime tools = %d, want 1", len(tools))
	}
	tool := tools[0]
	if tool.Definition.Name != runtimeUpdatePlanToolName ||
		tool.Definition.Namespace != "" ||
		!tool.Definition.Sequential ||
		tool.Definition.ConcurrencySafe ||
		tool.Definition.Strict == nil || !*tool.Definition.Strict {
		t.Fatalf("update_plan definition = %#v", tool.Definition)
	}
	schema := tool.Definition.ParametersSchema
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("update_plan schema = %#v", schema)
	}
	properties, ok := schema["properties"].(core.Schema)
	if !ok {
		t.Fatalf("update_plan properties = %T", schema["properties"])
	}
	if _, ok := properties["explanation"]; !ok {
		t.Fatal("update_plan schema missing explanation")
	}
	plan, ok := properties["plan"].(core.Schema)
	if !ok || plan["type"] != "array" {
		t.Fatalf("update_plan plan schema = %#v", properties["plan"])
	}
	items, ok := plan["items"].(core.Schema)
	if !ok || items["type"] != "object" || items["additionalProperties"] != false {
		t.Fatalf("update_plan step schema = %#v", plan["items"])
	}
	stepProperties, ok := items["properties"].(core.Schema)
	status, statusOK := stepProperties["status"].(core.Schema)
	if !ok || !statusOK || !reflect.DeepEqual(status["enum"], []any{"pending", "inProgress", "completed"}) {
		t.Fatalf("update_plan status schema = %#v", stepProperties["status"])
	}
	if got := schema["required"]; !reflect.DeepEqual(got, []string{"plan"}) {
		t.Fatalf("update_plan required = %#v, want plan", got)
	}
}

func TestPlanRuntimeToolPersistsOnePlanSnapshotBeforePublishing(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Plan updates"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: thread.ID})
	if err != nil {
		t.Fatal(err)
	}
	notifier := &runtimePlanCaptureNotifier{}
	tool := findRuntimeToolByName(t, PlanRuntimeTools(), runtimeUpdatePlanToolName)
	runContext := &core.RunContext{
		Deps:       runtimeToolDependencies{store: st, notifier: notifier, turn: turn},
		ToolName:   runtimeUpdatePlanToolName,
		ToolCallID: "plan-call-1",
	}

	result, err := tool.Handler(ctx, runContext, `{"explanation":"Inspect before editing","plan":[{"step":"Inspect","status":"inProgress"},{"step":"Implement","status":"pending"}]}`)
	if err != nil {
		t.Fatalf("first update_plan: %v", err)
	}
	if result != runtimePlanUpdatedResult {
		t.Fatalf("first update_plan result = %#v", result)
	}
	firstItem, firstPlan := runtimeStoredPlan(t, st, thread.ID, turn.ID)
	if firstItem.Status != protocol.ItemStatusInProgress || firstPlan.Explanation == nil || *firstPlan.Explanation != "Inspect before editing" {
		t.Fatalf("first durable plan = item %#v payload %#v", firstItem, firstPlan)
	}
	firstEvents := notifier.snapshot()
	if len(firstEvents) != 1 || firstEvents[0].Method != "turn/plan/updated" {
		t.Fatalf("first plan notifications = %#v", firstEvents)
	}
	var firstNotification protocol.TurnPlanUpdatedNotification
	if err := json.Unmarshal(firstEvents[0].Params, &firstNotification); err != nil {
		t.Fatalf("decode first notification: %v", err)
	}
	if !reflect.DeepEqual(firstNotification, firstPlan) {
		t.Fatalf("first notification = %#v, durable %#v", firstNotification, firstPlan)
	}

	runContext.ToolCallID = "plan-call-2"
	result, err = tool.Handler(ctx, runContext, `{"plan":[{"step":"Inspect","status":"completed"},{"step":"Implement","status":"completed"}]}`)
	if err != nil {
		t.Fatalf("second update_plan: %v", err)
	}
	if result != runtimePlanUpdatedResult {
		t.Fatalf("second update_plan result = %#v", result)
	}
	secondItem, secondPlan := runtimeStoredPlan(t, st, thread.ID, turn.ID)
	if secondItem.ID != firstItem.ID || secondItem.Status != protocol.ItemStatusCompleted {
		t.Fatalf("second durable plan item = %#v, first id %q", secondItem, firstItem.ID)
	}
	if secondPlan.Explanation != nil || len(secondPlan.Plan) != 2 || secondPlan.Plan[1].Status != protocol.TurnPlanStepStatusCompleted {
		t.Fatalf("second durable plan payload = %#v", secondPlan)
	}
	events := notifier.snapshot()
	if len(events) != 2 || events[1].Method != "turn/plan/updated" {
		t.Fatalf("plan notifications = %#v", events)
	}
	var secondNotification protocol.TurnPlanUpdatedNotification
	if err := json.Unmarshal(events[1].Params, &secondNotification); err != nil {
		t.Fatalf("decode second notification: %v", err)
	}
	if !reflect.DeepEqual(secondNotification, secondPlan) {
		t.Fatalf("second notification = %#v, durable %#v", secondNotification, secondPlan)
	}
}

func TestPlanRuntimeToolRejectsMalformedOrUnownedUpdatesWithoutPersistence(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Invalid plans"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: thread.ID})
	if err != nil {
		t.Fatal(err)
	}
	notifier := &runtimePlanCaptureNotifier{}
	tool := findRuntimeToolByName(t, PlanRuntimeTools(), runtimeUpdatePlanToolName)
	runContext := &core.RunContext{Deps: runtimeToolDependencies{store: st, notifier: notifier, turn: turn}}

	invalid := []string{
		`null`,
		`[]`,
		`{}`,
		`{"plan":null}`,
		`{"plan":[{"step":"","status":"pending"}]}`,
		`{"plan":[{"step":"Inspect","status":"blocked"}]}`,
		`{"plan":[],"plan":[]}`,
		`{"plan":[{"step":"Inspect","step":"Replace","status":"pending"}]}`,
		`{"plan":[],"extra":true}`,
		`{"plan":[]} {}`,
	}
	tooManySteps := make([]runtimeUpdatePlanStep, runtimePlanMaxSteps+1)
	for i := range tooManySteps {
		tooManySteps[i] = runtimeUpdatePlanStep{Step: "step", Status: "pending"}
	}
	tooManyJSON, err := json.Marshal(runtimeUpdatePlanParams{Plan: tooManySteps})
	if err != nil {
		t.Fatal(err)
	}
	invalid = append(
		invalid,
		string(tooManyJSON),
		`{"plan":[{"step":"`+strings.Repeat("x", runtimePlanMaxStepBytes+1)+`","status":"pending"}]}`,
		`{"explanation":"`+strings.Repeat("x", runtimePlanMaxExplanationBytes+1)+`","plan":[]}`,
	)
	for _, input := range invalid {
		if _, err := tool.Handler(ctx, runContext, input); err == nil {
			t.Errorf("update_plan(%s) succeeded", input)
		}
	}
	if _, err := tool.Handler(ctx, &core.RunContext{}, `{"plan":[]}`); err == nil || !strings.Contains(err.Error(), "active runtime turn") {
		t.Fatalf("unowned update_plan error = %v", err)
	}
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: thread.ID, TurnID: turn.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 || len(notifier.snapshot()) != 0 {
		t.Fatalf("invalid plans persisted items=%#v notifications=%#v", items, notifier.snapshot())
	}
}

func TestPlanRuntimeToolDoesNotPublishWhenDurableWriteFails(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Failed plan"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: thread.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	notifier := &runtimePlanCaptureNotifier{}
	tool := findRuntimeToolByName(t, PlanRuntimeTools(), runtimeUpdatePlanToolName)
	_, err = tool.Handler(ctx, &core.RunContext{
		Deps: runtimeToolDependencies{store: st, notifier: notifier, turn: turn},
	}, `{"plan":[]}`)
	if err == nil {
		t.Fatal("update_plan succeeded against closed durable store")
	}
	if len(notifier.snapshot()) != 0 {
		t.Fatalf("failed durable write published %#v", notifier.snapshot())
	}
}

func TestServerRuntimeUpdatePlanIsDurableAndCorrelated(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(
		core.ToolCallResponseWithID(runtimeUpdatePlanToolName, `{"explanation":"Work in order","plan":[{"step":"Inspect","status":"completed"},{"step":"Verify","status":"inProgress"}]}`, "call-plan"),
		core.TextResponse("plan recorded"),
	)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(PlanRuntimeTools()...),
		)),
	)

	response := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "make a plan"}))
	if response.Error != nil {
		t.Fatalf("thread/start error: %v", response.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, response, &started)
	notifications := waitForNotificationSet(t, server, "turn/plan/updated", "turn/completed")
	var got protocol.TurnPlanUpdatedNotification
	for _, notification := range notifications {
		if notification.Method != "turn/plan/updated" {
			continue
		}
		if err := json.Unmarshal(notification.Params, &got); err != nil {
			t.Fatalf("decode plan notification: %v", err)
		}
	}
	if got.ThreadID != started.Thread.ID || got.TurnID != started.Turn.ID || got.Explanation == nil || *got.Explanation != "Work in order" || len(got.Plan) != 2 {
		t.Fatalf("runtime plan notification = %#v", got)
	}
	_, durable := runtimeStoredPlan(t, st, started.Thread.ID, started.Turn.ID)
	if !reflect.DeepEqual(durable, got) {
		t.Fatalf("runtime durable plan = %#v, notification %#v", durable, got)
	}
	if calls := len(model.Calls()); calls != 2 {
		t.Fatalf("model calls = %d, want tool call then final response", calls)
	}
}

func runtimeStoredPlan(t *testing.T, st store.Store, threadID, turnID string) (*store.Item, protocol.TurnPlanUpdatedNotification) {
	t.Helper()
	items, err := st.ListItems(context.Background(), store.ItemFilter{ThreadID: threadID, TurnID: turnID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	var found *store.Item
	for _, item := range items {
		if item != nil && item.Kind == runtimePlanItemKind {
			if found != nil {
				t.Fatalf("multiple durable plan items: %q and %q", found.ID, item.ID)
			}
			found = item
		}
	}
	if found == nil {
		t.Fatalf("durable plan item missing from %#v", items)
	}
	var plan protocol.TurnPlanUpdatedNotification
	if err := json.Unmarshal(found.Payload, &plan); err != nil {
		t.Fatalf("decode durable plan: %v", err)
	}
	return found, plan
}

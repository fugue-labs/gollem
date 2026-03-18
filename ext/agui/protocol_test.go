package agui

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/temporal"
)

func TestOpenSessionRequestJSONRoundTrip(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	messages, snapshot := testSerializedMessagesAndSnapshot(t, now)

	input := SessionInput{
		Prompt:   "hello from AGUI",
		Messages: messages,
		Metadata: map[string]any{"source": "ui", "attempt": float64(1)},
	}
	state := SessionState{
		Session: Session{
			SessionID:   "sess_123",
			RunID:       "run_123",
			ParentRunID: "parent_1",
			Mode:        SessionModeTemporal,
			CreatedAt:   now,
			Status:      SessionStatusRunning,
			Metadata:    map[string]any{"tenant": "acme"},
		},
		CurrentSequence: 42,
		Messages:        messages,
		Snapshot:        snapshot,
		Usage: core.RunUsage{
			Usage:     core.Usage{InputTokens: 11, OutputTokens: 7, Details: map[string]int{"reasoning": 3}},
			Requests:  2,
			ToolCalls: 1,
		},
		Metadata: map[string]any{"pane": "main"},
	}

	original := OpenSessionResponse{
		Session: state.Session,
		State:   &state,
	}
	request := OpenSessionRequest{
		SessionID: "sess_123",
		Mode:      SessionModeTemporal,
		Input:     input,
	}

	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal open request: %v", err)
	}
	var decodedReq OpenSessionRequest
	if err := json.Unmarshal(data, &decodedReq); err != nil {
		t.Fatalf("unmarshal open request: %v", err)
	}
	if !reflect.DeepEqual(decodedReq, request) {
		t.Fatalf("open request mismatch\nwant: %#v\n got: %#v", request, decodedReq)
	}

	respJSON, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal open response: %v", err)
	}
	var decodedResp OpenSessionResponse
	if err := json.Unmarshal(respJSON, &decodedResp); err != nil {
		t.Fatalf("unmarshal open response: %v", err)
	}
	if !reflect.DeepEqual(decodedResp, original) {
		t.Fatalf("open response mismatch\nwant: %#v\n got: %#v", original, decodedResp)
	}
}

func TestEventCodecRoundTrip(t *testing.T) {
	now := time.Unix(1700000010, 0).UTC()
	messages, snapshot := testSerializedMessagesAndSnapshot(t, now)
	responseEnv, err := core.EncodeModelResponse(&core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.TextPart{Content: "done"},
			core.ToolCallPart{ToolName: "lookup", ToolCallID: "call_1", ArgsJSON: `{"id":1}`, Metadata: map[string]string{"provider": "openai"}},
		},
		Usage:        core.Usage{InputTokens: 4, OutputTokens: 9},
		ModelName:    "gpt-test",
		FinishReason: core.FinishReasonToolCall,
		Timestamp:    now,
	})
	if err != nil {
		t.Fatalf("encode model response: %v", err)
	}

	state := SessionState{
		Session:         Session{SessionID: "sess_evt", RunID: "run_evt", Mode: SessionModeCoreStream, CreatedAt: now, Status: SessionStatusWaiting},
		CurrentSequence: 9,
		WaitingReason:   WaitingReasonApproval,
		Messages:        messages,
		Snapshot:        snapshot,
		Usage: core.RunUsage{
			Usage:     core.Usage{InputTokens: 20, OutputTokens: 10, Details: map[string]int{"audio": 2}},
			Requests:  3,
			ToolCalls: 1,
		},
		Cost: &core.RunCost{TotalCost: 0.42, Breakdown: map[string]float64{"gpt-test": 0.42}, Currency: "USD"},
		PendingApprovals: []ToolApprovalRequest{{
			ToolName:   "dangerous_tool",
			ToolCallID: "call_approve",
			ArgsJSON:   `{"target":"prod"}`,
			Summary:    "Delete production cache",
		}},
		DeferredRequests: []DeferredRequest{{ToolName: "fetch_url", ToolCallID: "call_deferred", ArgsJSON: `{"url":"https://example.com"}`}},
		DeferredResults:  []DeferredResult{{ToolName: "fetch_url", ToolCallID: "call_deferred", Content: "ok"}},
		LastError:        "waiting for human approval",
		Metadata:         map[string]any{"tab": "events"},
	}

	events := []Event{
		mustNewEvent(t, 1, EventSessionOpened, state.Session, 0, SessionOpenedPayload{Session: state.Session}, now),
		mustNewEvent(t, 2, EventSessionInputAccepted, state.Session, 0, SessionInputAcceptedPayload{Input: SessionInput{Prompt: "hello", Messages: messages}}, now),
		mustNewEvent(t, 3, EventRunStarted, state.Session, 0, RunStartedPayload{Mode: SessionModeCoreStream}, now),
		mustNewEvent(t, 4, EventModelOutputTextDelta, state.Session, 1, TextDeltaPayload{Index: 0, Delta: "hel", Content: "hel"}, now),
		mustNewEvent(t, 5, EventModelOutputToolCallDelta, state.Session, 1, ToolCallDeltaPayload{Index: 1, ToolName: "lookup", ToolCallID: "call_1", ArgsDelta: `{"id"`, ArgsJSON: `{"id":1}`}, now),
		mustNewEvent(t, 6, EventApprovalRequested, state.Session, 1, ApprovalRequestedPayload{Request: state.PendingApprovals[0]}, now),
		mustNewEvent(t, 7, EventExternalInputRequested, state.Session, 1, ExternalInputRequestedPayload{Request: state.DeferredRequests[0]}, now),
		mustNewEvent(t, 8, EventSessionWaiting, state.Session, 1, SessionWaitingPayload{Reason: WaitingReasonApproval, PendingApprovals: state.PendingApprovals, DeferredRequests: state.DeferredRequests}, now),
		mustNewEvent(t, 9, EventModelResponseCompleted, state.Session, 1, ModelResponseCompletedPayload{Response: *responseEnv}, now),
		mustNewEvent(t, 10, EventSessionSnapshot, state.Session, 1, SessionSnapshotPayload{State: state}, now),
		mustNewEvent(t, 11, EventSessionCompleted, state.Session, 1, SessionTerminalPayload{State: state}, now),
	}

	data, err := MarshalEvents(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	decoded, err := UnmarshalEvents(data)
	if err != nil {
		t.Fatalf("unmarshal events: %v", err)
	}
	if !reflect.DeepEqual(decoded, events) {
		t.Fatalf("event round-trip mismatch\nwant: %#v\n got: %#v", events, decoded)
	}

	payload, err := DecodeEventPayload[SessionSnapshotPayload](decoded[9])
	if err != nil {
		t.Fatalf("decode snapshot payload: %v", err)
	}
	if !reflect.DeepEqual(payload.State, state) {
		t.Fatalf("snapshot payload mismatch\nwant: %#v\n got: %#v", state, payload.State)
	}
}

func TestClientCommandCodecRoundTrip(t *testing.T) {
	now := time.Unix(1700000020, 0).UTC()
	_, snapshot := testSerializedMessagesAndSnapshot(t, now)

	resume := ResumePayload{
		Snapshot: snapshot,
		DeferredResults: []DeferredResult{{
			ToolName:   "review",
			ToolCallID: "call_resume",
			Content:    "approved externally",
		}},
	}

	commands := []ClientCommand{
		mustNewCommand(t, CommandApproveToolCall, "sess_cmd", ApprovalDecisionPayload{ToolName: "deploy", ToolCallID: "call_1", Approved: true, Message: "ship it"}),
		mustNewCommand(t, CommandDenyToolCall, "sess_cmd", ApprovalDecisionPayload{ToolName: "deploy", ToolCallID: "call_2", Approved: false, Message: "needs review"}),
		mustNewCommand(t, CommandSubmitDeferred, "sess_cmd", DeferredResult{ToolName: "fetch", ToolCallID: "call_3", Content: "body", IsError: true}),
		mustNewCommand(t, CommandAbortSession, "sess_cmd", AbortPayload{Reason: "user_cancelled"}),
		mustNewCommand(t, CommandResumeSession, "sess_cmd", resume),
		mustNewCommand(t, CommandReconnectStream, "sess_cmd", ReconnectPayload{LastSequence: 77}),
	}

	for _, command := range commands {
		data, err := MarshalCommand(command)
		if err != nil {
			t.Fatalf("marshal command %s: %v", command.Type, err)
		}
		decoded, err := UnmarshalCommand(data)
		if err != nil {
			t.Fatalf("unmarshal command %s: %v", command.Type, err)
		}
		if !reflect.DeepEqual(*decoded, command) {
			t.Fatalf("command mismatch for %s\nwant: %#v\n got: %#v", command.Type, command, *decoded)
		}
	}

	decodedResume, err := DecodeCommandPayload[ResumePayload](commands[4])
	if err != nil {
		t.Fatalf("decode resume payload: %v", err)
	}
	if !reflect.DeepEqual(*decodedResume, resume) {
		t.Fatalf("resume payload mismatch\nwant: %#v\n got: %#v", resume, *decodedResume)
	}
}

func TestSessionStateRoundTripAndWorkflowProjection(t *testing.T) {
	now := time.Unix(1700000030, 0).UTC()
	messages, snapshot := testSerializedMessagesAndSnapshot(t, now)
	trace := &core.RunTrace{
		RunID:     "run_state",
		Prompt:    "state prompt",
		StartTime: now,
		EndTime:   now.Add(2 * time.Second),
		Duration:  2 * time.Second,
		Steps: []core.TraceStep{{
			Kind:      core.TraceModelResponse,
			Timestamp: now.Add(time.Second),
			Duration:  time.Second,
			Data:      map[string]any{"summary": "ok"},
		}},
		Usage:   core.RunUsage{Usage: core.Usage{InputTokens: 5, OutputTokens: 6}, Requests: 1},
		Success: true,
	}
	status := &temporal.WorkflowStatus{
		RunID:            "run_state",
		ParentRunID:      "parent_state",
		RunStep:          3,
		Usage:            core.RunUsage{Usage: core.Usage{InputTokens: 55, OutputTokens: 44}, Requests: 4, ToolCalls: 2},
		Messages:         messages,
		Snapshot:         snapshot,
		Trace:            trace,
		Cost:             &core.RunCost{TotalCost: 1.25, Breakdown: map[string]float64{"model": 1.25}, Currency: "USD"},
		PendingApprovals: []temporal.ToolApprovalRequest{{ToolName: "danger", ToolCallID: "call_appr", ArgsJSON: `{"scope":"prod"}`}},
		DeferredRequests: []core.DeferredToolRequest{{ToolName: "ticket", ToolCallID: "call_def", ArgsJSON: `{"id":99}`}},
		Waiting:          true,
		WaitingReason:    string(WaitingReasonApprovalAndDeferred),
		LastError:        "still waiting",
	}
	state := StateFromWorkflowStatus(Session{
		SessionID: "sess_state",
		RunID:     status.RunID,
		Mode:      SessionModeTemporal,
		CreatedAt: now,
		Status:    SessionStatusWaiting,
	}, 88, status)

	if state.WaitingReason != WaitingReasonApprovalAndDeferred {
		t.Fatalf("unexpected waiting reason: %q", state.WaitingReason)
	}
	if len(state.PendingApprovals) != 1 || state.PendingApprovals[0].ToolCallID != "call_appr" {
		t.Fatalf("unexpected projected approvals: %#v", state.PendingApprovals)
	}
	if len(state.DeferredRequests) != 1 || state.DeferredRequests[0].ToolCallID != "call_def" {
		t.Fatalf("unexpected projected deferred requests: %#v", state.DeferredRequests)
	}

	data, err := MarshalSessionState(*state)
	if err != nil {
		t.Fatalf("marshal session state: %v", err)
	}
	decoded, err := UnmarshalSessionState(data)
	if err != nil {
		t.Fatalf("unmarshal session state: %v", err)
	}
	if !reflect.DeepEqual(decoded, state) {
		t.Fatalf("session state mismatch\nwant: %#v\n got: %#v", state, decoded)
	}
}

func TestDecodePayloadNilTarget(t *testing.T) {
	if err := DecodePayload(json.RawMessage(`{"ok":true}`), nil); err == nil {
		t.Fatal("expected error for nil target")
	}
}

func testSerializedMessagesAndSnapshot(t *testing.T, now time.Time) ([]core.SerializedMessage, *core.SerializedRunSnapshot) {
	t.Helper()
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "system", Timestamp: now},
				core.UserPromptPart{Content: "hello", Timestamp: now},
				core.ToolReturnPart{ToolName: "lookup", ToolCallID: "call_return", Content: map[string]any{"answer": "42", "ok": true}, Timestamp: now},
			},
			Timestamp: now,
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "world"},
				core.ThinkingPart{Content: "thinking", Signature: "sig_1"},
				core.ToolCallPart{ToolName: "lookup", ToolCallID: "call_tool", ArgsJSON: `{"query":"q"}`, Metadata: map[string]string{"signature": "opaque"}},
			},
			Usage:        core.Usage{InputTokens: 2, OutputTokens: 3, Details: map[string]int{"reasoning": 1}},
			ModelName:    "gpt-test",
			FinishReason: core.FinishReasonToolCall,
			Timestamp:    now,
		},
	}
	serialized, err := core.EncodeMessages(messages)
	if err != nil {
		t.Fatalf("encode messages: %v", err)
	}
	snapshot, err := core.EncodeRunSnapshot(&core.RunSnapshot{
		Messages:        messages,
		Usage:           core.RunUsage{Usage: core.Usage{InputTokens: 10, OutputTokens: 12, Details: map[string]int{"cache_read": 1}}, Requests: 1, ToolCalls: 1},
		LastInputTokens: 10,
		Retries:         1,
		ToolRetries:     map[string]int{"lookup": 2},
		RunID:           "run_snapshot",
		ParentRunID:     "parent_snapshot",
		RunStep:         2,
		RunStartTime:    now.Add(-time.Minute),
		Prompt:          "hello world",
		ToolState:       map[string]any{"lookup": map[string]any{"cache": true}},
		Timestamp:       now,
	})
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	return serialized, snapshot
}

func mustNewEvent(t *testing.T, seq int64, eventType EventType, session Session, turn int, payload any, ts time.Time) Event {
	t.Helper()
	event, err := NewEvent(seq, eventType, session, turn, payload)
	if err != nil {
		t.Fatalf("new event %s: %v", eventType, err)
	}
	event.Timestamp = ts
	return event
}

func mustNewCommand(t *testing.T, commandType ClientCommandType, sessionID string, payload any) ClientCommand {
	t.Helper()
	command, err := NewClientCommand(commandType, sessionID, payload)
	if err != nil {
		t.Fatalf("new command %s: %v", commandType, err)
	}
	return command
}

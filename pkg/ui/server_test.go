package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/agui"
)

func TestHandleStartRunCreatesRunAndRedirects(t *testing.T) {
	store := NewRunStateStore()
	started := make(chan string, 1)
	server := MustNewServer(
		WithRunStore(store),
		WithRunStarter(RunStarterFunc(func(_ context.Context, runtime *RunRuntime, req RunStartRequest) error {
			started <- runtime.RunID + ":" + req.Prompt
			core.Publish(runtime.EventBus, core.RunStartedEvent{
				RunID:     runtime.RunID,
				Prompt:    req.Prompt,
				StartedAt: time.Now().UTC(),
			})
			return nil
		})),
	)

	form := url.Values{
		"title":  {"Test run"},
		"prompt": {"hello world"},
	}
	req := httptest.NewRequest(http.MethodPost, "/runs/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "/runs/") {
		t.Fatalf("location = %q, want /runs/<id>", location)
	}
	runID := strings.TrimPrefix(location, "/runs/")
	if runID == "" {
		t.Fatal("expected non-empty run id")
	}
	if _, ok := store.get(runID); !ok {
		t.Fatalf("expected run %q in store", runID)
	}

	select {
	case got := <-started:
		if got != runID+":hello world" {
			t.Fatalf("starter payload = %q, want %q", got, runID+":hello world")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async starter")
	}
}

func TestServerServeRoutesAssetsSSEAndApproveFlow(t *testing.T) {
	store := NewRunStateStore()
	server := MustNewServer(
		WithRunStore(store),
		WithRunStarter(RunStarterFunc(func(ctx context.Context, runtime *RunRuntime, req RunStartRequest) error {
			now := time.Now().UTC()
			core.Publish(runtime.EventBus, core.RunStartedEvent{RunID: runtime.RunID, Prompt: req.Prompt, StartedAt: now})
			core.Publish(runtime.EventBus, core.TurnStartedEvent{RunID: runtime.RunID, TurnNumber: 1, StartedAt: now.Add(10 * time.Millisecond)})
			runtime.Adapter.EmitTextDelta("msg_before_approval", "stream before approval")
			core.Publish(runtime.EventBus, core.ModelResponseCompletedEvent{RunID: runtime.RunID, InputTokens: 11, OutputTokens: 5, CompletedAt: now.Add(20 * time.Millisecond)})
			core.Publish(runtime.EventBus, core.ToolCalledEvent{RunID: runtime.RunID, ToolCallID: "tool_approve", ToolName: "dangerous_write", ArgsJSON: `{"path":"/tmp/out.txt"}`, CalledAt: now.Add(30 * time.Millisecond)})
			core.Publish(runtime.EventBus, core.ApprovalRequestedEvent{RunID: runtime.RunID, ToolCallID: "tool_approve", ToolName: "dangerous_write", ArgsJSON: `{"path":"/tmp/out.txt"}`, RequestedAt: now.Add(40 * time.Millisecond)})
			core.Publish(runtime.EventBus, core.RunWaitingEvent{RunID: runtime.RunID, Reason: "approval", WaitingAt: now.Add(50 * time.Millisecond)})

			approved, err := runtime.ApprovalBridge.ToolApprovalFunc()(core.ContextWithToolCallID(ctx, "tool_approve"), "dangerous_write", `{"path":"/tmp/out.txt"}`)
			if err != nil {
				return err
			}

			resolvedAt := time.Now().UTC()
			core.Publish(runtime.EventBus, core.RunResumedEvent{RunID: runtime.RunID, ResumedAt: resolvedAt})
			core.Publish(runtime.EventBus, core.ApprovalResolvedEvent{RunID: runtime.RunID, ToolCallID: "tool_approve", ToolName: "dangerous_write", Approved: approved, ResolvedAt: resolvedAt})
			if !approved {
				core.Publish(runtime.EventBus, core.ToolFailedEvent{RunID: runtime.RunID, ToolCallID: "tool_approve", ToolName: "dangerous_write", Error: "denied by user", FailedAt: resolvedAt})
				core.Publish(runtime.EventBus, core.TurnCompletedEvent{RunID: runtime.RunID, TurnNumber: 1, CompletedAt: resolvedAt.Add(10 * time.Millisecond)})
				core.Publish(runtime.EventBus, core.RunCompletedEvent{RunID: runtime.RunID, Error: "denied by user", CompletedAt: resolvedAt.Add(20 * time.Millisecond)})
				return nil
			}

			core.Publish(runtime.EventBus, core.ToolCompletedEvent{RunID: runtime.RunID, ToolCallID: "tool_approve", ToolName: "dangerous_write", Result: "ok", CompletedAt: resolvedAt.Add(10 * time.Millisecond)})
			runtime.Adapter.EmitTextDelta("msg_after_approval", "stream after approval")
			core.Publish(runtime.EventBus, core.TurnCompletedEvent{RunID: runtime.RunID, TurnNumber: 1, HasText: true, HasToolCalls: true, CompletedAt: resolvedAt.Add(20 * time.Millisecond)})
			core.Publish(runtime.EventBus, core.RunCompletedEvent{RunID: runtime.RunID, Success: true, StartedAt: now, CompletedAt: resolvedAt.Add(30 * time.Millisecond)})
			return nil
		})),
	)

	ts := httptest.NewServer(server)
	defer ts.Close()
	client := ts.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	startForm := url.Values{
		"title":    {"Approve flow"},
		"summary":  {"exercise live dashboard flow"},
		"prompt":   {"please verify the UI"},
		"provider": {"test"},
		"model":    {"serve-test"},
	}
	startResp := mustPOSTForm(t, client, ts.URL+"/runs/start", startForm)
	defer startResp.Body.Close()
	if startResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("start status = %d, want %d", startResp.StatusCode, http.StatusSeeOther)
	}
	runID := strings.TrimPrefix(startResp.Header.Get("Location"), "/runs/")
	if runID == "" {
		t.Fatalf("redirect location = %q, want /runs/<id>", startResp.Header.Get("Location"))
	}

	run := waitForRun(t, store, runID)
	waitForRunStatus(t, store, runID, "waiting")
	waitForPendingApprovals(t, run, 1)

	assertHTMLContains(t, mustGETBody(t, client, ts.URL+"/"),
		"Dashboard",
		"Approve flow",
		"/runs/"+runID,
		"test / serve-test",
	)
	assertHTMLContains(t, mustGETBody(t, client, ts.URL+"/runs/"+runID),
		"Approve flow",
		"exercise live dashboard flow",
		"data-run-scene",
		"data-run-canvas",
		"data-run-event-log",
		"/runs/"+runID+"/events",
	)
	assertHTMLContains(t, mustGETBody(t, client, ts.URL+"/runs/"+runID+"/sidebar"),
		"waiting",
		"dangerous_write",
		"tool_approve",
		">11<",
		">5<",
		">1<",
	)

	assetReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/static/style.css", nil)
	if err != nil {
		t.Fatalf("new static asset request: %v", err)
	}
	assetResp, err := client.Do(assetReq)
	if err != nil {
		t.Fatalf("GET static asset: %v", err)
	}
	assetBody, err := io.ReadAll(assetResp.Body)
	assetResp.Body.Close()
	if err != nil {
		t.Fatalf("read static asset: %v", err)
	}
	if assetResp.StatusCode != http.StatusOK {
		t.Fatalf("asset status = %d, want %d", assetResp.StatusCode, http.StatusOK)
	}
	if got := assetResp.Header.Get("Content-Type"); !strings.Contains(got, "text/css") {
		t.Fatalf("asset content type = %q, want text/css", got)
	}
	if len(assetBody) == 0 {
		t.Fatal("expected embedded asset body")
	}

	eventResp := mustOpenUISSE(t, client, ts.URL+"/runs/"+runID+"/events")
	defer eventResp.Body.Close()
	reader := newUISSEStreamReader(t, eventResp.Body)
	preApprovalFrames := readUISSEFrames(t, reader, 10)
	assertUISSEFrameTypes(t, preApprovalFrames,
		agui.AGUIRunStarted,
		agui.AGUIStepStarted,
		agui.AGUITextMessageStart,
		agui.AGUITextMessageContent,
		agui.AGUITextMessageEnd,
		agui.AGUIToolCallStart,
		agui.AGUIToolCallArgs,
		agui.AGUIToolCallEnd,
		agui.AGUICustom,
	)
	assertUISSEFrameContains(t, preApprovalFrames, agui.AGUITextMessageContent, "delta", "stream before approval")
	assertUISSECustomEvent(t, preApprovalFrames, "gollem.approval.requested")
	assertUISSECustomEvent(t, preApprovalFrames, "gollem.run.waiting")

	actionResp := mustPOSTJSON(t, client, ts.URL+"/runs/"+runID+"/action", `{"type":"approve_tool_call","session_id":"wrong-session","tool_call_id":"tool_approve","message":"ship it"}`)
	defer actionResp.Body.Close()
	if actionResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(actionResp.Body)
		t.Fatalf("approve status = %d, body=%s", actionResp.StatusCode, string(body))
	}
	var actionBody struct {
		OK        bool   `json:"ok"`
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(actionResp.Body).Decode(&actionBody); err != nil {
		t.Fatalf("decode approve response: %v", err)
	}
	if !actionBody.OK || actionBody.Action != agui.ActionApproveToolCall {
		t.Fatalf("unexpected approve response: %+v", actionBody)
	}
	if actionBody.SessionID != run.Session().ID {
		t.Fatalf("approve session_id = %q, want %q", actionBody.SessionID, run.Session().ID)
	}
	if actionBody.Message != "ship it" {
		t.Fatalf("approve message = %q, want ship it", actionBody.Message)
	}

	waitForRunStatus(t, store, runID, "completed")
	postApprovalFrames := readUISSEFrames(t, reader, 8)
	assertUISSEFrameTypes(t, postApprovalFrames,
		agui.AGUICustom,
		agui.AGUIToolCallResult,
		agui.AGUITextMessageStart,
		agui.AGUITextMessageContent,
		agui.AGUITextMessageEnd,
		agui.AGUIStepFinished,
		agui.AGUIRunFinished,
	)
	assertUISSECustomEvent(t, postApprovalFrames, "gollem.run.resumed")
	assertUISSECustomEvent(t, postApprovalFrames, "gollem.approval.resolved")
	assertUISSEFrameContains(t, postApprovalFrames, agui.AGUITextMessageContent, "delta", "stream after approval")
	assertHTMLContains(t, mustGETBody(t, client, ts.URL+"/runs/"+runID+"/sidebar"),
		"completed",
		"No pending approvals.",
		">11<",
		">5<",
		">1<",
	)
}

func TestHandleActionDenyFlow(t *testing.T) {
	store := NewRunStateStore()
	server := MustNewServer(
		WithRunStore(store),
		WithRunStarter(RunStarterFunc(func(ctx context.Context, runtime *RunRuntime, req RunStartRequest) error {
			now := time.Now().UTC()
			core.Publish(runtime.EventBus, core.RunStartedEvent{RunID: runtime.RunID, Prompt: req.Prompt, StartedAt: now})
			core.Publish(runtime.EventBus, core.ToolCalledEvent{RunID: runtime.RunID, ToolCallID: "tool_deny", ToolName: "rm", ArgsJSON: `{"path":"/tmp/nope"}`, CalledAt: now.Add(10 * time.Millisecond)})
			core.Publish(runtime.EventBus, core.ApprovalRequestedEvent{RunID: runtime.RunID, ToolCallID: "tool_deny", ToolName: "rm", ArgsJSON: `{"path":"/tmp/nope"}`, RequestedAt: now.Add(20 * time.Millisecond)})
			core.Publish(runtime.EventBus, core.RunWaitingEvent{RunID: runtime.RunID, Reason: "approval", WaitingAt: now.Add(30 * time.Millisecond)})

			approved, err := runtime.ApprovalBridge.ToolApprovalFunc()(core.ContextWithToolCallID(ctx, "tool_deny"), "rm", `{"path":"/tmp/nope"}`)
			if err != nil {
				return err
			}

			resolvedAt := time.Now().UTC()
			core.Publish(runtime.EventBus, core.RunResumedEvent{RunID: runtime.RunID, ResumedAt: resolvedAt})
			core.Publish(runtime.EventBus, core.ApprovalResolvedEvent{RunID: runtime.RunID, ToolCallID: "tool_deny", ToolName: "rm", Approved: approved, ResolvedAt: resolvedAt})
			if approved {
				return fmt.Errorf("expected deny flow")
			}
			core.Publish(runtime.EventBus, core.ToolFailedEvent{RunID: runtime.RunID, ToolCallID: "tool_deny", ToolName: "rm", Error: "denied by user", FailedAt: resolvedAt.Add(10 * time.Millisecond)})
			core.Publish(runtime.EventBus, core.RunCompletedEvent{RunID: runtime.RunID, Error: "denied by user", CompletedAt: resolvedAt.Add(20 * time.Millisecond)})
			return nil
		})),
	)

	ts := httptest.NewServer(server)
	defer ts.Close()
	client := ts.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	startResp := mustPOSTJSON(t, client, ts.URL+"/runs/start", `{"title":"Deny flow","prompt":"deny the tool"}`)
	defer startResp.Body.Close()
	runID := strings.TrimPrefix(startResp.Header.Get("Location"), "/runs/")
	if runID == "" {
		t.Fatalf("redirect location = %q, want /runs/<id>", startResp.Header.Get("Location"))
	}

	run := waitForRun(t, store, runID)
	waitForRunStatus(t, store, runID, "waiting")
	waitForPendingApprovals(t, run, 1)

	actionResp := mustPOSTJSON(t, client, ts.URL+"/runs/"+runID+"/action", `{"type":"deny_tool_call","session_id":"wrong-session","tool_call_id":"tool_deny","message":"do not run it"}`)
	defer actionResp.Body.Close()
	if actionResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(actionResp.Body)
		t.Fatalf("deny status = %d, body=%s", actionResp.StatusCode, string(body))
	}
	var actionBody struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(actionResp.Body).Decode(&actionBody); err != nil {
		t.Fatalf("decode deny response: %v", err)
	}
	if actionBody.Action != agui.ActionDenyToolCall {
		t.Fatalf("deny action = %q, want %q", actionBody.Action, agui.ActionDenyToolCall)
	}
	if actionBody.SessionID != run.Session().ID {
		t.Fatalf("deny session_id = %q, want %q", actionBody.SessionID, run.Session().ID)
	}
	if actionBody.Message != "do not run it" {
		t.Fatalf("deny message = %q, want %q", actionBody.Message, "do not run it")
	}

	waitForRunStatus(t, store, runID, "failed")
	assertHTMLContains(t, mustGETBody(t, client, ts.URL+"/runs/"+runID+"/sidebar"),
		"failed",
		"No pending approvals.",
	)
}

func TestHandleActionAbortUsesRunSpecificSessionAndMarksRunAborted(t *testing.T) {
	store := NewRunStateStore()
	server := MustNewServer(
		WithRunStore(store),
		WithRunStarter(RunStarterFunc(func(ctx context.Context, runtime *RunRuntime, req RunStartRequest) error {
			core.Publish(runtime.EventBus, core.RunStartedEvent{RunID: runtime.RunID, Prompt: req.Prompt, StartedAt: time.Now().UTC()})
			<-ctx.Done()
			return ctx.Err()
		})),
	)

	ts := httptest.NewServer(server)
	defer ts.Close()
	client := ts.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	startResp := mustPOSTJSON(t, client, ts.URL+"/runs/start", `{"prompt":"abort me"}`)
	defer startResp.Body.Close()
	runID := strings.TrimPrefix(startResp.Header.Get("Location"), "/runs/")
	if runID == "" {
		t.Fatalf("redirect location = %q, want /runs/<id>", startResp.Header.Get("Location"))
	}

	run := waitForRun(t, store, runID)
	waitForRunStatus(t, store, runID, "running")
	actionResp := mustPOSTJSON(t, client, ts.URL+"/runs/"+runID+"/action", `{"type":"abort_session","session_id":"wrong-session"}`)
	defer actionResp.Body.Close()
	if actionResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(actionResp.Body)
		t.Fatalf("abort status = %d, body=%s", actionResp.StatusCode, string(body))
	}
	var actionBody struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(actionResp.Body).Decode(&actionBody); err != nil {
		t.Fatalf("decode abort response: %v", err)
	}
	if actionBody.Action != agui.ActionAbortSession {
		t.Fatalf("abort action = %q, want %q", actionBody.Action, agui.ActionAbortSession)
	}
	if actionBody.SessionID != run.Session().ID {
		t.Fatalf("abort session_id = %q, want %q", actionBody.SessionID, run.Session().ID)
	}
	if actionBody.Message != "session aborted" {
		t.Fatalf("abort message = %q, want session aborted", actionBody.Message)
	}

	waitForRunStatus(t, store, runID, "aborted")
	if got := run.Session().GetStatus(); got != agui.SessionStatusAborted {
		t.Fatalf("session status = %q, want %q", got, agui.SessionStatusAborted)
	}
}

func TestRunCompletedDeferredPreservesExistingWaitingReason(t *testing.T) {
	store := NewRunStateStore()
	run := store.create(RunStartRequest{Prompt: "wait"})
	now := time.Now()

	core.Publish(run.EventBus(), core.RunWaitingEvent{RunID: run.ID(), Reason: "approval_and_deferred", WaitingAt: now})
	core.Publish(run.EventBus(), core.RunCompletedEvent{RunID: run.ID(), Deferred: true, CompletedAt: now.Add(time.Second)})

	snap := run.Snapshot()
	if snap.Status != "waiting" {
		t.Fatalf("status = %q, want waiting", snap.Status)
	}
	if snap.WaitingReason != "approval_and_deferred" {
		t.Fatalf("waiting reason = %q, want approval_and_deferred", snap.WaitingReason)
	}
}

func TestRunStateStoreCreateRejectsDuplicateIDs(t *testing.T) {
	store := NewRunStateStore()
	calls := 0
	store.nextID = func() string {
		calls++
		switch calls {
		case 1, 2:
			return "dup"
		default:
			return fmt.Sprintf("dup-%d", calls)
		}
	}

	first := store.create(RunStartRequest{Prompt: "first"})
	second := store.create(RunStartRequest{Prompt: "second"})

	if first.ID() != "dup" {
		t.Fatalf("first id = %q, want dup", first.ID())
	}
	if second.ID() != "dup-3" {
		t.Fatalf("second id = %q, want dup-3", second.ID())
	}
	if first.ID() == second.ID() {
		t.Fatal("expected unique run ids")
	}
}

func mustPOSTForm(t *testing.T, client *http.Client, target string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new POST form request %s: %v", target, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST form %s: %v", target, err)
	}
	return resp
}

func mustPOSTJSON(t *testing.T, client *http.Client, target, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new POST json request %s: %v", target, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST json %s: %v", target, err)
	}
	return resp
}

func mustGETBody(t *testing.T, client *http.Client, target string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("new GET request %s: %v", target, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body %s: %v", target, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, body=%s", target, resp.StatusCode, string(body))
	}
	return string(body)
}

func assertHTMLContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func waitForRun(t *testing.T, store *RunStateStore, runID string) *RunRecord {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if run, ok := store.get(runID); ok {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %q not found", runID)
	return nil
}

func waitForRunStatus(t *testing.T, store *RunStateStore, runID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, ok := store.get(runID)
		if ok && run.Snapshot().Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, ok := store.get(runID)
	if !ok {
		t.Fatalf("run %q not found", runID)
	}
	t.Fatalf("run %q status = %q, want %q", runID, run.Snapshot().Status, want)
}

func waitForPendingApprovals(t *testing.T, run *RunRecord, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := run.ApprovalBridge().PendingCount(); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pending approvals = %d, want %d", run.ApprovalBridge().PendingCount(), want)
}

type uiSSEFrame struct {
	id   string
	data string
}

type uiSSEStreamReader struct {
	t       *testing.T
	scanner *bufio.Scanner
}

func newUISSEStreamReader(t *testing.T, body io.Reader) *uiSSEStreamReader {
	t.Helper()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 1024), 1024*1024)
	return &uiSSEStreamReader{t: t, scanner: scanner}
}

func (r *uiSSEStreamReader) Next() uiSSEFrame {
	r.t.Helper()
	var frame uiSSEFrame
	var dataLines []string
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if line == "" {
			if frame.id != "" || len(dataLines) > 0 {
				frame.data = strings.Join(dataLines, "\n")
				return frame
			}
			continue
		}
		if strings.HasPrefix(line, "id: ") {
			frame.id = strings.TrimPrefix(line, "id: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
	}
	if err := r.scanner.Err(); err != nil {
		r.t.Fatalf("read SSE frame: %v", err)
	}
	r.t.Fatal("no SSE frame received")
	return uiSSEFrame{}
}

func mustOpenUISSE(t *testing.T, client *http.Client, target string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("new SSE request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("SSE status = %d, body=%s", resp.StatusCode, string(body))
	}
	return resp
}

func readUISSEFrames(t *testing.T, reader *uiSSEStreamReader, count int) []map[string]any {
	t.Helper()
	frames := make([]map[string]any, 0, count)
	for range count {
		frame := reader.Next()
		var payload map[string]any
		if err := json.Unmarshal([]byte(frame.data), &payload); err != nil {
			t.Fatalf("unmarshal SSE frame %q: %v", frame.data, err)
		}
		frames = append(frames, payload)
	}
	return frames
}

func assertUISSEFrameTypes(t *testing.T, frames []map[string]any, wants ...string) {
	t.Helper()
	for _, want := range wants {
		found := false
		for _, frame := range frames {
			if got, _ := frame["type"].(string); got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing SSE frame type %q in %+v", want, frames)
		}
	}
}

func assertUISSECustomEvent(t *testing.T, frames []map[string]any, wantName string) {
	t.Helper()
	for _, frame := range frames {
		if gotType, _ := frame["type"].(string); gotType != agui.AGUICustom {
			continue
		}
		if gotName, _ := frame["name"].(string); gotName == wantName {
			return
		}
	}
	t.Fatalf("missing custom SSE event %q in %+v", wantName, frames)
}

func assertUISSEFrameContains(t *testing.T, frames []map[string]any, eventType, key, want string) {
	t.Helper()
	for _, frame := range frames {
		if gotType, _ := frame["type"].(string); gotType != eventType {
			continue
		}
		if got, _ := frame[key].(string); got == want {
			return
		}
	}
	t.Fatalf("missing %s frame with %s=%q in %+v", eventType, key, want, frames)
}

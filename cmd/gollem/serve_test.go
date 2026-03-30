package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/agui"
	"github.com/fugue-labs/gollem/modelutil"
	"github.com/fugue-labs/gollem/pkg/ui"
)

func TestParseServeFlags(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		got, err := parseServeFlags(nil)
		if err != nil {
			t.Fatalf("parseServeFlags() error = %v", err)
		}
		if got.port != defaultServePort {
			t.Fatalf("port = %d, want %d", got.port, defaultServePort)
		}
		if got.tools {
			t.Fatal("tools = true, want false")
		}
		if got.openBrowser {
			t.Fatal("openBrowser = true, want false")
		}
	})

	t.Run("explicit flags", func(t *testing.T) {
		got, err := parseServeFlags([]string{
			"--port", "9090",
			"--provider", "openai",
			"--model", "gpt-5.3",
			"--tools",
			"--open=false",
		})
		if err != nil {
			t.Fatalf("parseServeFlags() error = %v", err)
		}
		if got.port != 9090 {
			t.Fatalf("port = %d, want 9090", got.port)
		}
		if got.provider != "openai" {
			t.Fatalf("provider = %q, want openai", got.provider)
		}
		if got.modelName != "gpt-5.3" {
			t.Fatalf("modelName = %q, want gpt-5.3", got.modelName)
		}
		if !got.tools {
			t.Fatal("tools = false, want true")
		}
		if got.openBrowser {
			t.Fatal("openBrowser = true, want false")
		}
	})

	t.Run("invalid port", func(t *testing.T) {
		_, err := parseServeFlags([]string{"--port", "0"})
		if err == nil || !strings.Contains(err.Error(), "invalid --port") {
			t.Fatalf("expected invalid port error, got %v", err)
		}
	})
}

func TestApplyRunStartDefaultsJSONOverwritesProviderAndModel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/runs/start", strings.NewReader(`{"prompt":"hello","provider":"wrong","model":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")

	updated, err := applyRunStartDefaults(req, ui.RunStartRequest{Provider: "anthropic", Model: "claude-opus-4-6"})
	if err != nil {
		t.Fatalf("applyRunStartDefaults() error = %v", err)
	}

	var got ui.RunStartRequest
	if err := json.NewDecoder(updated.Body).Decode(&got); err != nil {
		t.Fatalf("decode updated body: %v", err)
	}
	if got.Provider != "anthropic" {
		t.Fatalf("provider = %q, want anthropic", got.Provider)
	}
	if got.Model != "claude-opus-4-6" {
		t.Fatalf("model = %q, want claude-opus-4-6", got.Model)
	}
	if got.Prompt != "hello" {
		t.Fatalf("prompt = %q, want hello", got.Prompt)
	}
}

func TestApplyRunStartDefaultsFormOverwritesProviderAndModel(t *testing.T) {
	form := url.Values{
		"prompt":   {"hello"},
		"provider": {"wrong"},
		"model":    {"wrong"},
	}
	req := httptest.NewRequest(http.MethodPost, "/runs/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	updated, err := applyRunStartDefaults(req, ui.RunStartRequest{Provider: "openai", Model: "gpt-5.3"})
	if err != nil {
		t.Fatalf("applyRunStartDefaults() error = %v", err)
	}
	if err := updated.ParseForm(); err != nil {
		t.Fatalf("updated.ParseForm() error = %v", err)
	}
	if got := updated.FormValue("provider"); got != "openai" {
		t.Fatalf("provider = %q, want openai", got)
	}
	if got := updated.FormValue("model"); got != "gpt-5.3" {
		t.Fatalf("model = %q, want gpt-5.3", got)
	}
	if got := updated.FormValue("prompt"); got != "hello" {
		t.Fatalf("prompt = %q, want hello", got)
	}
}

func TestServeHandlerWiringRunsModelAndStreamsToSSE(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("serve wiring response"))
	cfg := serveRunConfig{
		provider:  "openai",
		modelName: "serve-test",
		timeout:   time.Minute,
		model:     modelutil.NewRetryModel(model, modelutil.DefaultRetryConfig()),
	}

	server := ui.MustNewServer(ui.WithRunStarter(newServeRunStarter(cfg)))
	handler := withRunStartDefaults(server, ui.RunStartRequest{Provider: cfg.provider, Model: cfg.modelName})

	ts := httptest.NewServer(handler)
	defer ts.Close()
	client := ts.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	startResp := mustServePOSTJSON(t, client, ts.URL+"/runs/start", `{"title":"Serve wiring","prompt":"stream this","provider":"ignored","model":"ignored"}`)
	defer startResp.Body.Close()
	if startResp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(startResp.Body)
		t.Fatalf("start status = %d, want %d, body=%s", startResp.StatusCode, http.StatusSeeOther, string(body))
	}
	runID := strings.TrimPrefix(startResp.Header.Get("Location"), "/runs/")
	if runID == "" {
		t.Fatalf("redirect location = %q, want /runs/<id>", startResp.Header.Get("Location"))
	}

	waitForServeBodyContains(t, client, ts.URL+"/runs/"+runID+"/sidebar", "completed", "openai", "serve-test")
	assertServeBodyContains(t, mustServeGETBody(t, client, ts.URL+"/"), "Serve wiring", "openai / serve-test", "/runs/"+runID)
	assertServeBodyContains(t, mustServeGETBody(t, client, ts.URL+"/runs/"+runID), "Serve wiring", "stream this", "/runs/"+runID+"/events")

	eventResp := mustServeOpenSSE(t, client, ts.URL+"/runs/"+runID+"/events")
	defer eventResp.Body.Close()
	reader := newServeSSEStreamReader(t, eventResp.Body)
	frames := readServeSSEFrames(t, reader, 7)
	assertServeFrameTypes(t, frames, agui.AGUIRunStarted, agui.AGUIStepStarted, agui.AGUITextMessageContent, agui.AGUIStepFinished, agui.AGUIRunFinished)
	assertServeFrameContains(t, frames, agui.AGUITextMessageContent, "delta", "serve wiring response")

	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected model call")
	}
	request, ok := calls[0].Messages[len(calls[0].Messages)-1].(core.ModelRequest)
	if !ok {
		t.Fatalf("last message type = %T, want core.ModelRequest", calls[0].Messages[len(calls[0].Messages)-1])
	}
	if got := modelRequestText(request); got != "stream this" {
		t.Fatalf("prompt = %q, want %q", got, "stream this")
	}
}

func TestServeHandlerStartRouteInjectsDefaultsAndSupportsAbortAction(t *testing.T) {
	server := ui.MustNewServer(
		ui.WithRunStarter(ui.RunStarterFunc(func(ctx context.Context, runtime *ui.RunRuntime, req ui.RunStartRequest) error {
			core.Publish(runtime.EventBus, core.RunStartedEvent{RunID: runtime.RunID, Prompt: req.Prompt, StartedAt: time.Now().UTC()})
			<-ctx.Done()
			return ctx.Err()
		})),
	)
	handler := withRunStartDefaults(server, ui.RunStartRequest{Provider: "openai", Model: "serve-test"})

	ts := httptest.NewServer(handler)
	defer ts.Close()
	client := ts.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	startResp := mustServePOSTJSON(t, client, ts.URL+"/runs/start", `{"title":"Serve defaults","prompt":"wire defaults","provider":"wrong","model":"wrong"}`)
	defer startResp.Body.Close()
	if startResp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(startResp.Body)
		t.Fatalf("start status = %d, want %d, body=%s", startResp.StatusCode, http.StatusSeeOther, string(body))
	}
	runID := strings.TrimPrefix(startResp.Header.Get("Location"), "/runs/")
	if runID == "" {
		t.Fatalf("redirect location = %q, want /runs/<id>", startResp.Header.Get("Location"))
	}

	waitForServeBodyContains(t, client, ts.URL+"/", "Serve defaults", "openai / serve-test", "/runs/"+runID)
	waitForServeBodyContains(t, client, ts.URL+"/runs/"+runID, "Serve defaults", "wire defaults", "/runs/"+runID+"/events")
	waitForServeBodyContains(t, client, ts.URL+"/runs/"+runID+"/sidebar", "running", "openai", "serve-test")

	abortResp := mustServePOSTJSON(t, client, ts.URL+"/runs/"+runID+"/action", `{"type":"abort_session","session_id":"wrong-session"}`)
	defer abortResp.Body.Close()
	var abortBody struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(abortResp.Body).Decode(&abortBody); err != nil {
		t.Fatalf("decode abort response: %v", err)
	}
	if abortBody.Action != agui.ActionAbortSession {
		t.Fatalf("abort action = %q, want %q", abortBody.Action, agui.ActionAbortSession)
	}
	if abortBody.SessionID == "" || abortBody.SessionID == "wrong-session" {
		t.Fatalf("abort session_id = %q, want rewritten live session id", abortBody.SessionID)
	}
	waitForServeBodyContains(t, client, ts.URL+"/runs/"+runID+"/sidebar", "aborted")
}

func TestNewServeRunStarterClosesPerRunModels(t *testing.T) {
	base := &serveCountingModel{response: core.TextResponse("done")}
	cfg := serveRunConfig{
		provider:  "openai",
		modelName: "serve-test",
		timeout:   time.Minute,
		model:     modelutil.NewRetryModel(base, modelutil.DefaultRetryConfig()),
	}

	runtime := &ui.RunRuntime{
		RunID:          "run-1",
		EventBus:       core.NewEventBus(),
		Session:        agui.NewSession(agui.SessionModeCoreStream),
		ApprovalBridge: agui.NewApprovalBridge(),
		Adapter:        agui.NewAdapter("run-1"),
	}
	defer runtime.Adapter.Close()

	starter := newServeRunStarter(cfg)
	if err := starter.StartRun(context.Background(), runtime, ui.RunStartRequest{Prompt: "hello"}); err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}

	if got := base.newSessionCalls.Load(); got != 2 {
		t.Fatalf("newSessionCalls = %d, want 2", got)
	}
	if got := base.closeCalls.Load(); got != 2 {
		t.Fatalf("closeCalls = %d, want 2", got)
	}
}

type serveCountingModel struct {
	response        *core.ModelResponse
	newSessionCalls atomic.Int32
	closeCalls      atomic.Int32
}

func (m *serveCountingModel) Request(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
	return nil, errors.New("unexpected Request call")
}

func (m *serveCountingModel) RequestStream(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
	return nil, errors.New("unexpected RequestStream call")
}

func (m *serveCountingModel) ModelName() string { return "serve-counting-model" }

func (m *serveCountingModel) NewSession() core.Model {
	m.newSessionCalls.Add(1)
	return &serveCountingSession{
		response:        m.response,
		newSessionCalls: &m.newSessionCalls,
		closeCalls:      &m.closeCalls,
	}
}

type serveCountingSession struct {
	response        *core.ModelResponse
	newSessionCalls *atomic.Int32
	closeCalls      *atomic.Int32
}

func (m *serveCountingSession) Request(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
	return m.response, nil
}

func (m *serveCountingSession) RequestStream(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
	return &serveStream{response: m.response}, nil
}

func (m *serveCountingSession) ModelName() string { return "serve-counting-session" }

func (m *serveCountingSession) NewSession() core.Model {
	m.newSessionCalls.Add(1)
	return &serveCountingSession{
		response:        m.response,
		newSessionCalls: m.newSessionCalls,
		closeCalls:      m.closeCalls,
	}
}

func (m *serveCountingSession) Close() error {
	m.closeCalls.Add(1)
	return nil
}

type serveStream struct {
	response *core.ModelResponse
	stage    int
}

func (s *serveStream) Next() (core.ModelResponseStreamEvent, error) {
	switch s.stage {
	case 0:
		s.stage = 1
		return core.PartStartEvent{Index: 0, Part: core.TextPart{Content: s.response.TextContent()}}, nil
	case 1:
		s.stage = 2
		return core.PartEndEvent{Index: 0}, nil
	default:
		return nil, io.EOF
	}
}

func (s *serveStream) Response() *core.ModelResponse { return s.response }
func (s *serveStream) Usage() core.Usage             { return s.response.Usage }
func (s *serveStream) Close() error                  { return nil }

func mustServePOSTJSON(t *testing.T, client *http.Client, target, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new POST request %s: %v", target, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	return resp
}

func mustServeGETBody(t *testing.T, client *http.Client, target string) string {
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

func waitForServeBodyContains(t *testing.T, client *http.Client, target string, wants ...string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		body := mustServeGETBody(t, client, target)
		ok := true
		for _, want := range wants {
			if !strings.Contains(body, want) {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	assertServeBodyContains(t, mustServeGETBody(t, client, target), wants...)
}

func assertServeBodyContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func modelRequestText(req core.ModelRequest) string {
	var parts []string
	for _, part := range req.Parts {
		if p, ok := part.(core.UserPromptPart); ok {
			parts = append(parts, p.Content)
		}
	}
	return strings.Join(parts, "")
}

type serveSSEStreamReader struct {
	t       *testing.T
	scanner *bufio.Scanner
}

func newServeSSEStreamReader(t *testing.T, body io.Reader) *serveSSEStreamReader {
	t.Helper()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 1024), 1024*1024)
	return &serveSSEStreamReader{t: t, scanner: scanner}
}

func (r *serveSSEStreamReader) Next() map[string]any {
	r.t.Helper()
	var dataLines []string
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if line == "" {
			if len(dataLines) > 0 {
				var payload map[string]any
				if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &payload); err != nil {
					r.t.Fatalf("unmarshal SSE frame: %v", err)
				}
				return payload
			}
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
	return nil
}

func mustServeOpenSSE(t *testing.T, client *http.Client, target string) *http.Response {
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

func readServeSSEFrames(t *testing.T, reader *serveSSEStreamReader, count int) []map[string]any {
	t.Helper()
	frames := make([]map[string]any, 0, count)
	for range count {
		frames = append(frames, reader.Next())
	}
	return frames
}

func assertServeFrameTypes(t *testing.T, frames []map[string]any, wants ...string) {
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

func assertServeFrameContains(t *testing.T, frames []map[string]any, eventType, key, want string) {
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

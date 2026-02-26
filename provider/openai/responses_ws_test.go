package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/gorilla/websocket"
)

func TestResponsesWebSocketURL(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		{base: "https://api.openai.com", want: "wss://api.openai.com/v1/responses"},
		{base: "http://localhost:8080", want: "ws://localhost:8080/v1/responses"},
		{base: "https://proxy.example.com/root", want: "wss://proxy.example.com/root/v1/responses"},
	}
	for _, tt := range tests {
		got, err := responsesWebSocketURL(tt.base)
		if err != nil {
			t.Fatalf("responsesWebSocketURL(%q) failed: %v", tt.base, err)
		}
		if got != tt.want {
			t.Fatalf("responsesWebSocketURL(%q) = %q, want %q", tt.base, got, tt.want)
		}
	}
}

func TestResponsesIncrementalInputAndTrim(t *testing.T) {
	prevSigs := []string{"1", "2"}
	currSigs := []string{"1", "2", "3", "4"}
	currInput := []map[string]any{
		{"type": "message", "role": "user"},
		{"type": "message", "role": "assistant"},
		{"type": "message", "role": "assistant"},
		{"type": "function_call_output", "call_id": "call_1"},
	}

	delta, ok := responsesIncrementalInput(prevSigs, currSigs, currInput)
	if !ok {
		t.Fatal("expected incremental delta")
	}
	trimmed := trimContinuationDelta(delta)
	want := []map[string]any{
		{"type": "function_call_output", "call_id": "call_1"},
	}
	if !reflect.DeepEqual(trimmed, want) {
		t.Fatalf("trimmed delta mismatch:\n got: %#v\nwant: %#v", trimmed, want)
	}
}

func TestTrimContinuationDeltaRemovesAssistantFunctionCalls(t *testing.T) {
	delta := []map[string]any{
		{"type": "function_call", "call_id": "call_1", "name": "read_file", "arguments": `{"path":"main.go"}`},
		{"type": "message", "role": "assistant"},
		{"type": "function_call_output", "call_id": "call_1", "output": "ok"},
		{"type": "message", "role": "user"},
	}

	trimmed := trimContinuationDelta(delta)
	want := []map[string]any{
		{"type": "function_call_output", "call_id": "call_1", "output": "ok"},
		{"type": "message", "role": "user"},
	}
	if !reflect.DeepEqual(trimmed, want) {
		t.Fatalf("trimmed delta mismatch:\n got: %#v\nwant: %#v", trimmed, want)
	}
}

func TestRequestViaResponsesWebSocketContinuation(t *testing.T) {
	var (
		mu       sync.Mutex
		received []responsesWSCreateEvent
	)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("websocket upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var ev responsesWSCreateEvent
			if err := json.Unmarshal(payload, &ev); err != nil {
				t.Errorf("decode websocket event: %v", err)
				return
			}

			mu.Lock()
			received = append(received, ev)
			idx := len(received)
			mu.Unlock()

			done := responsesWSEvent{
				Type: "response.completed",
				Response: &responsesAPIResponse{
					ID:    fmt.Sprintf("resp_%d", idx),
					Model: "gpt-5.3-codex",
					Output: []responsesOutputItem{
						{
							Type: "message",
							Role: "assistant",
							Content: []responsesContentItem{
								{Type: "output_text", Text: "ok"},
							},
						},
					},
					Usage: responsesUsage{
						InputTokens:  10,
						OutputTokens: 3,
					},
				},
			}
			if err := conn.WriteJSON(done); err != nil {
				t.Errorf("write websocket response: %v", err)
				return
			}
		}
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL(server.URL),
		WithTransport("websocket"),
	)

	firstMessages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "initial request"},
			},
		},
	}
	if _, err := p.Request(context.Background(), firstMessages, nil, nil); err != nil {
		t.Fatalf("first websocket request failed: %v", err)
	}

	secondMessages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "initial request"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "ok"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolCallID: "call_1",
					ToolName:   "read_file",
					Content:    "tool output",
				},
				core.UserPromptPart{Content: "continue"},
			},
		},
	}
	if _, err := p.Request(context.Background(), secondMessages, nil, nil); err != nil {
		t.Fatalf("second websocket request failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("expected 2 websocket create events, got %d", len(received))
	}

	first := received[0]
	if first.PreviousResponseID != "" {
		t.Fatalf("first request should not include previous_response_id, got %q", first.PreviousResponseID)
	}
	if first.Store == nil || *first.Store {
		t.Fatalf("first request should set store=false in websocket mode, got %v", first.Store)
	}
	if len(first.Input) != 1 {
		t.Fatalf("first request expected 1 input item, got %d", len(first.Input))
	}

	second := received[1]
	if second.PreviousResponseID != "resp_1" {
		t.Fatalf("second request expected previous_response_id resp_1, got %q", second.PreviousResponseID)
	}
	if second.Store == nil || *second.Store {
		t.Fatalf("second request should set store=false in websocket mode, got %v", second.Store)
	}
	if len(second.Input) != 2 {
		t.Fatalf("second request expected 2 delta items, got %d", len(second.Input))
	}
	if got, _ := second.Input[0]["type"].(string); got != "function_call_output" {
		t.Fatalf("first delta item should be function_call_output, got %q", got)
	}
	if got, _ := second.Input[1]["type"].(string); got != "message" {
		t.Fatalf("second delta item should be message, got %q", got)
	}
	if got, _ := second.Input[1]["role"].(string); got != "user" {
		t.Fatalf("second delta message role should be user, got %q", got)
	}
}

func TestRequestViaResponsesWebSocketAcceptsResponseCompletedEvent(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("websocket upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		_, _, err = conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteJSON(responsesWSEvent{
			Type: "response.completed",
			Response: &responsesAPIResponse{
				ID:    "resp_completed_1",
				Model: "gpt-5.3-codex",
				Output: []responsesOutputItem{
					{
						Type: "message",
						Role: "assistant",
						Content: []responsesContentItem{
							{Type: "output_text", Text: "ok"},
						},
					},
				},
				Usage: responsesUsage{InputTokens: 4, OutputTokens: 2},
			},
		})
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL(server.URL),
		WithTransport("websocket"),
	)

	resp, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "hello"},
			},
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if got := resp.TextContent(); got != "ok" {
		t.Fatalf("response text = %q, want ok", got)
	}
}

func TestRequestViaResponsesWebSocketFallsBackToHTTP(t *testing.T) {
	var (
		getHits  int
		postHits int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			getHits++
			w.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		postHits++

		var req responsesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode HTTP fallback request: %v", err)
		}
		if req.Model != "gpt-5.3-codex" {
			t.Fatalf("expected codex model in fallback request, got %q", req.Model)
		}
		if req.Store != nil {
			t.Fatalf("expected HTTP fallback to preserve original unset store (nil), got %v", *req.Store)
		}

		resp := responsesAPIResponse{
			ID:    "resp_http_fallback",
			Model: "gpt-5.3-codex",
			Output: []responsesOutputItem{
				{
					Type: "message",
					Role: "assistant",
					Content: []responsesContentItem{
						{Type: "output_text", Text: "fallback-ok"},
					},
				},
			},
			Usage: responsesUsage{InputTokens: 5, OutputTokens: 2},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL(server.URL),
		WithTransport("websocket"),
		WithWebSocketHTTPFallback(true),
	)

	resp, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "hello"},
			},
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if got := resp.TextContent(); got != "fallback-ok" {
		t.Fatalf("fallback text = %q, want fallback-ok", got)
	}
	if getHits != 1 {
		t.Fatalf("expected one websocket attempt before fallback, got %d", getHits)
	}
	if postHits != 1 {
		t.Fatalf("expected one HTTP fallback call, got %d", postHits)
	}
}

func TestRequestViaResponsesWebSocketFallbackPreservesExplicitStoreTrue(t *testing.T) {
	var (
		getHits     int
		postHits    int
		lastHTTPReq responsesRequest
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			getHits++
			w.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		postHits++
		if err := json.NewDecoder(r.Body).Decode(&lastHTTPReq); err != nil {
			t.Fatalf("decode HTTP fallback request: %v", err)
		}

		resp := responsesAPIResponse{
			ID:    "resp_http_fallback",
			Model: "gpt-5.3-codex",
			Output: []responsesOutputItem{
				{
					Type: "message",
					Role: "assistant",
					Content: []responsesContentItem{
						{Type: "output_text", Text: "fallback-ok"},
					},
				},
			},
			Usage: responsesUsage{InputTokens: 5, OutputTokens: 2},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL(server.URL),
		WithTransport("websocket"),
		WithWebSocketHTTPFallback(true),
	)

	storeTrue := true
	req := &responsesRequest{
		Model: "gpt-5.3-codex",
		Store: &storeTrue,
		Input: []map[string]any{
			responsesMessage("user", "hello"),
		},
	}
	resp, err := p.requestViaResponsesWithReq(context.Background(), req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if got := resp.TextContent(); got != "fallback-ok" {
		t.Fatalf("fallback text = %q, want fallback-ok", got)
	}
	if getHits != 1 {
		t.Fatalf("expected one websocket attempt before fallback, got %d", getHits)
	}
	if postHits != 1 {
		t.Fatalf("expected one HTTP fallback call, got %d", postHits)
	}
	if lastHTTPReq.Store == nil || !*lastHTTPReq.Store {
		t.Fatalf("expected HTTP fallback to preserve explicit store=true, got %v", lastHTTPReq.Store)
	}
}

func TestRequestViaResponsesWebSocketNoFallbackByDefault(t *testing.T) {
	var (
		getHits  int
		postHits int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			getHits++
			w.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		if r.Method == http.MethodPost {
			postHits++
		}
		http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL(server.URL),
		WithTransport("websocket"),
	)

	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hello"}},
		},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected websocket connection error without HTTP fallback")
	}
	if getHits != 1 {
		t.Fatalf("expected one websocket attempt, got %d", getHits)
	}
	if postHits != 0 {
		t.Fatalf("expected no HTTP fallback attempt by default, got %d", postHits)
	}
}

func TestRequestViaResponsesWebSocketDisablesContinuationAfterHistoryRewrite(t *testing.T) {
	var (
		mu       sync.Mutex
		received []responsesWSCreateEvent
	)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("websocket upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var ev responsesWSCreateEvent
			if err := json.Unmarshal(payload, &ev); err != nil {
				t.Errorf("decode websocket event: %v", err)
				return
			}

			mu.Lock()
			received = append(received, ev)
			idx := len(received)
			mu.Unlock()

			done := responsesWSEvent{
				Type: "response.completed",
				Response: &responsesAPIResponse{
					ID:    fmt.Sprintf("resp_rewrite_%d", idx),
					Model: "gpt-5.3-codex",
					Output: []responsesOutputItem{
						{
							Type: "message",
							Role: "assistant",
							Content: []responsesContentItem{
								{Type: "output_text", Text: "ok"},
							},
						},
					},
					Usage: responsesUsage{InputTokens: 7, OutputTokens: 2},
				},
			}
			if err := conn.WriteJSON(done); err != nil {
				t.Errorf("write websocket response: %v", err)
				return
			}
		}
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL(server.URL),
		WithTransport("websocket"),
	)

	// Turn 1 (baseline).
	first := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "original prompt"},
			},
		},
	}
	if _, err := p.Request(context.Background(), first, nil, nil); err != nil {
		t.Fatalf("turn1 failed: %v", err)
	}

	// Turn 2 (append-only): should use continuation.
	second := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "original prompt"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "assistant result"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "next step"},
			},
		},
	}
	if _, err := p.Request(context.Background(), second, nil, nil); err != nil {
		t.Fatalf("turn2 failed: %v", err)
	}

	// Turn 3 simulates context compression/summarization rewrite:
	// history is rewritten instead of append-only. Continuation must be disabled.
	third := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "[Conversation Summary] original prompt + assistant result"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "next step after compression"},
			},
		},
	}
	if _, err := p.Request(context.Background(), third, nil, nil); err != nil {
		t.Fatalf("turn3 failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatalf("expected 3 websocket create events, got %d", len(received))
	}

	if received[1].PreviousResponseID == "" {
		t.Fatalf("turn2 expected continuation previous_response_id, got empty")
	}
	if received[2].PreviousResponseID != "" {
		t.Fatalf("turn3 should disable continuation after rewrite, got previous_response_id=%q", received[2].PreviousResponseID)
	}
	if len(received[2].Input) != 2 {
		t.Fatalf("turn3 should send full rebuilt input (2 items), got %d", len(received[2].Input))
	}
}

func TestRequestViaResponsesWebSocketReconnectsOnConnectionLimit(t *testing.T) {
	var (
		mu         sync.Mutex
		received   []responsesWSCreateEvent
		connNumber int
	)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("websocket upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		mu.Lock()
		connNumber++
		thisConn := connNumber
		mu.Unlock()

		msgIdx := 0
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			msgIdx++

			var ev responsesWSCreateEvent
			if err := json.Unmarshal(payload, &ev); err != nil {
				t.Errorf("decode websocket event: %v", err)
				return
			}
			mu.Lock()
			received = append(received, ev)
			mu.Unlock()

			// On first connection's second request, force a connection-limit error.
			if thisConn == 1 && msgIdx == 2 {
				_ = conn.WriteJSON(responsesWSEvent{
					Type:   "error",
					Status: 400,
					Error: &responsesWSError{
						Type:    "invalid_request_error",
						Code:    "websocket_connection_limit_reached",
						Message: "connection limit reached",
					},
				})
				return
			}

			done := responsesWSEvent{
				Type: "response.completed",
				Response: &responsesAPIResponse{
					ID:    fmt.Sprintf("resp_conn_%d_msg_%d", thisConn, msgIdx),
					Model: "gpt-5.3-codex",
					Output: []responsesOutputItem{
						{
							Type: "message",
							Role: "assistant",
							Content: []responsesContentItem{
								{Type: "output_text", Text: "ok"},
							},
						},
					},
					Usage: responsesUsage{InputTokens: 6, OutputTokens: 2},
				},
			}
			if err := conn.WriteJSON(done); err != nil {
				t.Errorf("write websocket response: %v", err)
				return
			}
		}
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL(server.URL),
		WithTransport("websocket"),
	)

	first := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "original prompt"},
			},
		},
	}
	if _, err := p.Request(context.Background(), first, nil, nil); err != nil {
		t.Fatalf("turn1 failed: %v", err)
	}

	second := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "original prompt"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "assistant result"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "next step"},
			},
		},
	}
	if _, err := p.Request(context.Background(), second, nil, nil); err != nil {
		t.Fatalf("turn2 failed after reconnect: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// Expected request events:
	// 1) first turn on conn1
	// 2) continuation attempt on conn1 (fails with limit)
	// 3) retried full-context request on conn2
	if len(received) != 3 {
		t.Fatalf("expected 3 websocket create events, got %d", len(received))
	}
	if received[1].PreviousResponseID == "" {
		t.Fatalf("second event should be continuation attempt with previous_response_id")
	}
	if received[2].PreviousResponseID != "" {
		t.Fatalf("third event should start new chain after reconnect, got previous_response_id=%q", received[2].PreviousResponseID)
	}
	if len(received[2].Input) != 3 {
		t.Fatalf("third event should resend full context (3 items), got %d", len(received[2].Input))
	}
}

func TestSendResponsesCreateLockedUsesFallbackReadTimeoutWithoutContextDeadline(t *testing.T) {
	oldRead := fallbackWebSocketReadTimeout
	oldWrite := fallbackWebSocketWriteTimeout
	fallbackWebSocketReadTimeout = 40 * time.Millisecond
	fallbackWebSocketWriteTimeout = 2 * time.Second
	defer func() {
		fallbackWebSocketReadTimeout = oldRead
		fallbackWebSocketWriteTimeout = oldWrite
	}()

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("websocket upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		// Consume one request message, then intentionally send no response event.
		_, _, _ = conn.ReadMessage()
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL(server.URL),
		WithTransport("websocket"),
	)
	conn, err := p.ensureResponsesWebSocketLocked(context.Background())
	if err != nil {
		t.Fatalf("ensure websocket failed: %v", err)
	}
	defer p.resetResponsesWebSocketLocked()

	req := &responsesRequest{
		Model: "gpt-5.3-codex",
		Input: []map[string]any{
			responsesMessage("user", "hello"),
		},
	}

	start := time.Now()
	_, err = p.sendResponsesCreateLocked(context.Background(), conn, req)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected read timeout error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "read failed") {
		t.Fatalf("expected read failure error, got: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("expected fallback timeout-bound return, took too long: %v", elapsed)
	}
}

func TestRequestViaResponsesWebSocketUsesHTTPClientTimeoutAsDeadline(t *testing.T) {
	oldRead := fallbackWebSocketReadTimeout
	oldWrite := fallbackWebSocketWriteTimeout
	fallbackWebSocketReadTimeout = 5 * time.Second
	fallbackWebSocketWriteTimeout = 5 * time.Second
	defer func() {
		fallbackWebSocketReadTimeout = oldRead
		fallbackWebSocketWriteTimeout = oldWrite
	}()

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("websocket upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		// Consume one request message, then intentionally never send terminal event.
		_, _, _ = conn.ReadMessage()
		time.Sleep(2 * time.Second)
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL(server.URL),
		WithTransport("websocket"),
		WithHTTPClient(&http.Client{Timeout: 120 * time.Millisecond}),
	)

	start := time.Now()
	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "hello"},
			},
		},
	}, nil, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout-bound websocket read failure")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "read failed") {
		t.Fatalf("expected websocket read failure, got: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("expected timeout around HTTP client timeout, took too long: %v", elapsed)
	}
}

func TestRequestViaResponsesWebSocketResponseFailed(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("websocket upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		_, _, err = conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteJSON(responsesWSEvent{
			Type: "response.failed",
			Response: &responsesAPIResponse{
				ID:    "resp_failed",
				Model: "gpt-5.3-codex",
				IncompleteDetails: &responsesIncompleteDetails{
					Reason: "max_output_tokens",
				},
			},
		})
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL(server.URL),
		WithTransport("websocket"),
	)

	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "hello"},
			},
		},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected response.failed to return error")
	}
	var httpErr *core.ModelHTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected ModelHTTPError, got %T", err)
	}
	if httpErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", httpErr.StatusCode)
	}
	if !strings.Contains(httpErr.Message, "response failed: max_output_tokens") {
		t.Fatalf("unexpected error message: %q", httpErr.Message)
	}
}

func TestRequestViaResponsesWebSocketReconnectsOnPreviousResponseNotFound(t *testing.T) {
	var (
		mu         sync.Mutex
		received   []responsesWSCreateEvent
		connNumber int
	)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("websocket upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		mu.Lock()
		connNumber++
		thisConn := connNumber
		mu.Unlock()

		msgIdx := 0
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			msgIdx++

			var ev responsesWSCreateEvent
			if err := json.Unmarshal(payload, &ev); err != nil {
				t.Errorf("decode websocket event: %v", err)
				return
			}
			mu.Lock()
			received = append(received, ev)
			mu.Unlock()

			// On first connection's second request, force previous_response_not_found.
			if thisConn == 1 && msgIdx == 2 {
				_ = conn.WriteJSON(responsesWSEvent{
					Type:   "error",
					Status: 400,
					Error: &responsesWSError{
						Type:    "invalid_request_error",
						Code:    "previous_response_not_found",
						Message: "Previous response with id 'resp_abc' not found.",
					},
				})
				return
			}

			done := responsesWSEvent{
				Type: "response.completed",
				Response: &responsesAPIResponse{
					ID:    fmt.Sprintf("resp_prev_%d_%d", thisConn, msgIdx),
					Model: "gpt-5.3-codex",
					Output: []responsesOutputItem{
						{
							Type: "message",
							Role: "assistant",
							Content: []responsesContentItem{
								{Type: "output_text", Text: "ok"},
							},
						},
					},
					Usage: responsesUsage{InputTokens: 6, OutputTokens: 2},
				},
			}
			if err := conn.WriteJSON(done); err != nil {
				t.Errorf("write websocket response: %v", err)
				return
			}
		}
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL(server.URL),
		WithTransport("websocket"),
	)

	first := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "original prompt"},
			},
		},
	}
	if _, err := p.Request(context.Background(), first, nil, nil); err != nil {
		t.Fatalf("turn1 failed: %v", err)
	}

	second := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "original prompt"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "assistant result"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "next step"},
			},
		},
	}
	if _, err := p.Request(context.Background(), second, nil, nil); err != nil {
		t.Fatalf("turn2 failed after reconnect: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatalf("expected 3 websocket create events, got %d", len(received))
	}
	if received[1].PreviousResponseID == "" {
		t.Fatalf("second event should be continuation attempt with previous_response_id")
	}
	if received[2].PreviousResponseID != "" {
		t.Fatalf("third event should start new chain after reconnect, got previous_response_id=%q", received[2].PreviousResponseID)
	}
	if len(received[2].Input) != 3 {
		t.Fatalf("third event should resend full context (3 items), got %d", len(received[2].Input))
	}
}

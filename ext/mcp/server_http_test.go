package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestHTTPServerTransportSamplingRoundTrip(t *testing.T) {
	server := NewServer(WithServerInfo(ServerInfo{Name: "sleepy-test", Version: "0.1.0"}))
	server.AddTool(Tool{
		Name:        "ask_client",
		Description: "Ask the connected client to sample a response",
		InputSchema: mustRawJSON([]byte(`{"type":"object","properties":{"prompt":{"type":"string"}},"required":["prompt"]}`)),
	}, func(ctx context.Context, rc *RequestContext, params map[string]any) (*ToolResult, error) {
		prompt, _ := params["prompt"].(string)
		resp, err := rc.CreateMessage(ctx, &CreateMessageParams{
			Messages: []SamplingMessage{{
				Role:    "user",
				Content: MarshalSamplingContent(Content{Type: "text", Text: prompt}),
			}},
			MaxTokens: 32,
		})
		if err != nil {
			return nil, err
		}
		blocks, err := ParseSamplingContent(resp.Content)
		if err != nil {
			return nil, err
		}
		if len(blocks) == 0 {
			return textToolResult(""), nil
		}
		return textToolResult(blocks[0].Text), nil
	})

	httpServer := httptest.NewServer(NewHTTPServerTransport(server))
	defer httpServer.Close()

	clientModel := &recordingModel{
		requestFn: func(_ context.Context, messages []core.ModelMessage, settings *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
			if len(messages) != 1 {
				t.Fatalf("unexpected nested sampling messages: %+v", messages)
			}
			req := messages[0].(core.ModelRequest)
			if got := req.Parts[0].(core.UserPromptPart).Content; got != "hello from transport" {
				t.Fatalf("unexpected prompt: %q", got)
			}
			if settings == nil || settings.MaxTokens == nil || *settings.MaxTokens != 32 {
				t.Fatalf("unexpected settings: %+v", settings)
			}
			return &core.ModelResponse{
				ModelName:    "client-model",
				FinishReason: core.FinishReasonStop,
				Parts: []core.ModelResponsePart{
					core.TextPart{Content: "client says hi"},
				},
			}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := NewHTTPClientWithConfig(ctx, httpServer.URL, ClientConfig{
		SamplingHandler: ModelSamplingHandler(clientModel),
	})
	if err != nil {
		t.Fatalf("failed to create HTTP client: %v", err)
	}
	defer client.Close()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ask_client" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	result, err := client.CallTool(ctx, "ask_client", map[string]any{"prompt": "hello from transport"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if got := result.TextContent(); got != "client says hi" {
		t.Fatalf("unexpected tool result: %q", got)
	}
}

func TestHTTPServerTransportRunClosesSessionsOnCancel(t *testing.T) {
	transport := NewHTTPServerTransport(NewServer())
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- transport.Run(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want %v", err, context.Canceled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transport.Run to return")
	}

	select {
	case <-session.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session context to close")
	}

	transport.mu.Lock()
	sessionCount := len(transport.sessions)
	transport.mu.Unlock()
	if sessionCount != 0 {
		t.Fatalf("expected all sessions to be closed, found %d", sessionCount)
	}

	session.mu.Lock()
	closed := session.closed
	session.mu.Unlock()
	if !closed {
		t.Fatal("expected session to be marked closed")
	}
}

func TestHTTPServerTransportSessionIDsAreOpaque(t *testing.T) {
	seen := make(map[string]struct{}, 256)
	for range 256 {
		id, err := generateHTTPSessionID()
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate HTTP session ID: %q", id)
		}
		seen[id] = struct{}{}

		encoded := strings.TrimPrefix(id, "mcp-")
		if encoded == id {
			t.Fatalf("session ID missing mcp- prefix: %q", id)
		}
		random, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("session ID is not raw URL-safe base64: %q: %v", id, err)
		}
		if len(random) != httpSessionIDBytes {
			t.Fatalf("session ID contains %d random bytes, want %d", len(random), httpSessionIDBytes)
		}
	}
}

func TestHTTPServerTransportAuthorizesEveryFollowUpRequest(t *testing.T) {
	type principalKey struct{}

	transport := NewHTTPServerTransport(NewServer())
	transport.SetSessionContextFunc(func(r *http.Request) context.Context {
		return context.WithValue(context.Background(), principalKey{}, r.Header.Get("X-Test-Principal"))
	})
	transport.SetSessionAuthorizer(func(sessionCtx context.Context, r *http.Request) (bool, error) {
		owner, _ := sessionCtx.Value(principalKey{}).(string)
		return owner != "" && owner == r.Header.Get("X-Test-Principal"), nil
	})
	srv := httptest.NewServer(transport)
	defer srv.Close()

	sessionID := initializeRawHTTPSession(t, srv.URL, "tenant-a")
	toolList := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)

	for _, tc := range []struct {
		name      string
		method    string
		principal string
		body      []byte
	}{
		{name: "tenant-b post", method: http.MethodPost, principal: "tenant-b", body: toolList},
		{name: "anonymous post", method: http.MethodPost, body: toolList},
		{name: "tenant-b get", method: http.MethodGet, principal: "tenant-b"},
		{name: "anonymous get", method: http.MethodGet},
		{name: "tenant-b delete", method: http.MethodDelete, principal: "tenant-b"},
		{name: "anonymous delete", method: http.MethodDelete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := rawHTTPSessionRequest(t, srv.URL, tc.method, sessionID, tc.principal, tc.body)
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
			}
		})
	}

	// Unauthorized DELETE requests did not remove the session, and the
	// rightful principal can still dispatch work through it.
	resp := rawHTTPSessionRequest(t, srv.URL, http.MethodPost, sessionID, "tenant-a", toolList)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("owner POST status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}
	if session, ok := transport.getSession(sessionID); ok {
		session.server.WaitIdle()
	} else {
		t.Fatal("session disappeared after unauthorized follow-up")
	}

	resp = rawHTTPSessionRequest(t, srv.URL, http.MethodDelete, sessionID, "tenant-a", nil)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("owner DELETE status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if _, ok := transport.getSession(sessionID); ok {
		t.Fatal("owner DELETE did not remove session")
	}
}

func TestHTTPServerTransportSessionAuthorizerErrorsFailClosed(t *testing.T) {
	transport := NewHTTPServerTransport(NewServer())
	transport.SetSessionAuthorizer(func(context.Context, *http.Request) (bool, error) {
		return false, errors.New("identity backend unavailable")
	})
	srv := httptest.NewServer(transport)
	defer srv.Close()

	sessionID := initializeRawHTTPSession(t, srv.URL, "tenant-a")
	resp := rawHTTPSessionRequest(t, srv.URL, http.MethodPost, sessionID, "tenant-a", []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
}

func initializeRawHTTPSession(t *testing.T, endpoint, principal string) string {
	t.Helper()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	resp := rawHTTPSessionRequest(t, endpoint, http.MethodPost, "", principal, body)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	id := resp.Header.Get("Mcp-Session-Id")
	if id == "" {
		t.Fatal("initialize response missing Mcp-Session-Id")
	}
	return id
}

func rawHTTPSessionRequest(t *testing.T, endpoint, method, sessionID, principal string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if principal != "" {
		req.Header.Set("X-Test-Principal", principal)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestHTTPServerTransportSessionContextFunc verifies the per-session
// context hook: identity derived from the initializing HTTP request
// (e.g. a workspace resolved from the Authorization header) reaches
// tool handlers through their ctx, for the whole life of the session.
func TestHTTPServerTransportSessionContextFunc(t *testing.T) {
	type wsKey struct{}

	server := NewServer(WithServerInfo(ServerInfo{Name: "ctx-test", Version: "0.1.0"}))
	server.AddTool(Tool{
		Name:        "whoami",
		Description: "Report the session identity",
		InputSchema: mustRawJSON([]byte(`{"type":"object"}`)),
	}, func(ctx context.Context, _ *RequestContext, _ map[string]any) (*ToolResult, error) {
		ws, _ := ctx.Value(wsKey{}).(string)
		return textToolResult(ws), nil
	})

	transport := NewHTTPServerTransport(server)
	transport.SetSessionContextFunc(func(r *http.Request) context.Context {
		return context.WithValue(context.Background(), wsKey{}, r.Header.Get("X-Test-Workspace"))
	})
	httpServer := httptest.NewServer(transport)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewHTTPClientWithConfig(ctx, httpServer.URL, ClientConfig{},
		WithHeaders(map[string]string{"X-Test-Workspace": "ws-42"}))
	if err != nil {
		t.Fatalf("failed to create HTTP client: %v", err)
	}
	defer client.Close()

	result, err := client.CallTool(ctx, "whoami", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if got := result.TextContent(); got != "ws-42" {
		t.Fatalf("session context value did not reach the tool handler: %q", got)
	}
}

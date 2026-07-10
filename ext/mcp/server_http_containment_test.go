package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func containmentHTTPConfig() HTTPServerTransportConfig {
	config := DefaultHTTPServerTransportConfig()
	config.MaxSessions = 8
	config.MaxSessionsPerPrincipal = 4
	config.IdleTimeout = time.Minute
	config.AbsoluteLifetime = time.Hour
	config.MaxRequestBodyBytes = 1024
	config.MaxConcurrentMessages = 8
	config.MaxConcurrentMessagesPerSession = 2
	config.OutboxMaxMessages = 8
	config.OutboxMaxBytes = 4096
	config.RetryAfter = 1500 * time.Millisecond
	return config
}

func newContainmentTransport(t *testing.T, server *Server, config HTTPServerTransportConfig) *HTTPServerTransport {
	t.Helper()
	transport, err := NewHTTPServerTransportWithConfig(server, config)
	if err != nil {
		t.Fatalf("NewHTTPServerTransportWithConfig: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	return transport
}

func TestDefaultHTTPServerTransportConfigIsBounded(t *testing.T) {
	config := DefaultHTTPServerTransportConfig()
	if config.MaxSessions != 1024 || config.MaxSessionsPerPrincipal != 64 {
		t.Fatalf("session defaults = %d/%d, want 1024/64", config.MaxSessions, config.MaxSessionsPerPrincipal)
	}
	if config.IdleTimeout != 30*time.Minute || config.AbsoluteLifetime != 24*time.Hour {
		t.Fatalf("lifetime defaults = %v/%v", config.IdleTimeout, config.AbsoluteLifetime)
	}
	if config.MaxRequestBodyBytes != 8<<20 {
		t.Fatalf("MaxRequestBodyBytes = %d, want %d", config.MaxRequestBodyBytes, 8<<20)
	}
	if config.RequestBodyTimeout != 30*time.Second {
		t.Fatalf("RequestBodyTimeout = %v, want 30s", config.RequestBodyTimeout)
	}
	if config.MaxConcurrentMessages != 256 || config.MaxConcurrentMessagesPerSession != 4 {
		t.Fatalf("message defaults = %d/%d, want 256/4", config.MaxConcurrentMessages, config.MaxConcurrentMessagesPerSession)
	}
	if config.OutboxMaxMessages != 256 || config.OutboxMaxBytes != 8<<20 {
		t.Fatalf("outbox defaults = %d/%d, want 256/%d", config.OutboxMaxMessages, config.OutboxMaxBytes, 8<<20)
	}
	if err := config.validate(); err != nil {
		t.Fatalf("default config: %v", err)
	}
}

func TestHTTPServerTransportConfigRejectsEveryUnboundedOrIncoherentLimit(t *testing.T) {
	valid := containmentHTTPConfig()
	tests := map[string]func(*HTTPServerTransportConfig){
		"sessions":              func(c *HTTPServerTransportConfig) { c.MaxSessions = 0 },
		"principal sessions":    func(c *HTTPServerTransportConfig) { c.MaxSessionsPerPrincipal = 0 },
		"principal over global": func(c *HTTPServerTransportConfig) { c.MaxSessionsPerPrincipal = c.MaxSessions + 1 },
		"idle":                  func(c *HTTPServerTransportConfig) { c.IdleTimeout = 0 },
		"absolute":              func(c *HTTPServerTransportConfig) { c.AbsoluteLifetime = 0 },
		"idle over absolute":    func(c *HTTPServerTransportConfig) { c.IdleTimeout = c.AbsoluteLifetime + 1 },
		"body":                  func(c *HTTPServerTransportConfig) { c.MaxRequestBodyBytes = 0 },
		"negative body timeout": func(c *HTTPServerTransportConfig) { c.RequestBodyTimeout = -1 },
		"messages":              func(c *HTTPServerTransportConfig) { c.MaxConcurrentMessages = 0 },
		"session messages":      func(c *HTTPServerTransportConfig) { c.MaxConcurrentMessagesPerSession = 0 },
		"session over global":   func(c *HTTPServerTransportConfig) { c.MaxConcurrentMessagesPerSession = c.MaxConcurrentMessages + 1 },
		"outbox messages":       func(c *HTTPServerTransportConfig) { c.OutboxMaxMessages = 0 },
		"outbox bytes":          func(c *HTTPServerTransportConfig) { c.OutboxMaxBytes = 0 },
		"retry":                 func(c *HTTPServerTransportConfig) { c.RetryAfter = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			config := valid
			mutate(&config)
			if transport, err := NewHTTPServerTransportWithConfig(NewServer(), config); err == nil {
				_ = transport.Close()
				t.Fatal("invalid config was accepted")
			}
		})
	}
}

func TestHTTPServerTransportZeroBodyTimeoutNormalizesToSafeDefault(t *testing.T) {
	config := containmentHTTPConfig()
	config.RequestBodyTimeout = 0
	transport := newContainmentTransport(t, NewServer(), config)
	if transport.config.RequestBodyTimeout != defaultHTTPRequestBodyTimeout {
		t.Fatalf("effective RequestBodyTimeout = %v, want %v", transport.config.RequestBodyTimeout, defaultHTTPRequestBodyTimeout)
	}
}

func TestHTTPServerTransportInitializationIsTransactional(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxRequestBodyBytes = 128
	transport := newContainmentTransport(t, NewServer(), config)

	for _, tc := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "oversize", body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"padding":"` + strings.Repeat("x", 256) + `"}}`, status: http.StatusRequestEntityTooLarge},
		{name: "two values", body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}} {}`, status: http.StatusBadRequest},
		{name: "invalid params", body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":"not-an-object"}`, status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			transport.ServeHTTP(recorder, request)
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.status, recorder.Body.String())
			}
			if got := recorder.Header().Get("Mcp-Session-Id"); got != "" {
				t.Fatalf("failed initialize exposed session ID %q", got)
			}
			stats := transport.Stats()
			if stats.ActiveSessions != 0 || stats.ProvisionalSessions != 0 || stats.InFlightMessages != 0 {
				t.Fatalf("failed initialize retained accounting: %+v", stats)
			}
		})
	}
}

type failingInitializeWriter struct {
	header http.Header
}

func (w *failingInitializeWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (*failingInitializeWriter) WriteHeader(int) {}
func (*failingInitializeWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func TestHTTPServerTransportInitializeWriteFailureRollsBack(t *testing.T) {
	transport := newContainmentTransport(t, NewServer(), containmentHTTPConfig())
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	writer := &failingInitializeWriter{}
	transport.ServeHTTP(writer, request)
	stats := transport.Stats()
	if stats.ActiveSessions != 0 || stats.ProvisionalSessions != 0 || len(transport.sessions) != 0 {
		t.Fatalf("write failure retained session: stats=%+v sessions=%d", stats, len(transport.sessions))
	}
}

func TestHTTPServerTransportIndexesProvisionalSessionsForPrincipalQuota(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxSessions = 2
	config.MaxSessionsPerPrincipal = 1
	transport := newContainmentTransport(t, NewServer(), config)
	transport.SetSessionPrincipalFunc(func(r *http.Request) (string, error) {
		return r.Header.Get("X-Principal"), nil
	})

	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("X-Principal", "principal-a")
	provisional, err := transport.newSession(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.newSession(request); !errors.Is(err, errHTTPPrincipalLimit) {
		t.Fatalf("second provisional error = %v, want principal limit", err)
	}
	stats := transport.Stats()
	if stats.ProvisionalSessions != 1 || stats.RejectedSessionCreations != 1 {
		t.Fatalf("quota accounting = %+v", stats)
	}
	if got := transport.ClosePrincipal("principal-a"); got != 1 {
		t.Fatalf("ClosePrincipal = %d, want 1", got)
	}
	select {
	case <-provisional.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("provisional session was not cancelled")
	}
}

func TestHTTPServerTransportGlobalSessionQuotaIncludesProvisionalSessions(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxSessions = 1
	config.MaxSessionsPerPrincipal = 1
	transport := newContainmentTransport(t, NewServer(), config)
	first, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil)); !errors.Is(err, errHTTPSessionLimit) {
		t.Fatalf("second session error = %v, want global limit", err)
	}
	if stats := transport.Stats(); stats.ProvisionalSessions != 1 || stats.RejectedSessionCreations != 1 {
		t.Fatalf("global quota accounting = %+v", stats)
	}
	first.close()
}

func TestHTTPServerTransportStatsCannotCarrySourceOrIdentity(t *testing.T) {
	typeOfStats := reflect.TypeOf(HTTPServerTransportStats{})
	for i := 0; i < typeOfStats.NumField(); i++ {
		field := typeOfStats.Field(i)
		switch field.Type.Kind() {
		case reflect.String, reflect.Slice, reflect.Map, reflect.Pointer, reflect.Interface:
			t.Fatalf("Stats field %s has source-bearing-capable type %s", field.Name, field.Type)
		}
	}
}

func TestHTTPServerTransportFreshContextPreservesImmutableSessionValues(t *testing.T) {
	type ownerKey struct{}
	type freshKey struct{}
	type observed struct{ owner, fresh string }

	observedCh := make(chan observed, 1)
	server := NewServer()
	server.AddTool(Tool{Name: "observe", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(ctx context.Context, _ *RequestContext, _ map[string]any) (*ToolResult, error) {
		observedCh <- observed{
			owner: ctx.Value(ownerKey{}).(string),
			fresh: ctx.Value(freshKey{}).(string),
		}
		return textToolResult("ok"), nil
	})
	transport := newContainmentTransport(t, server, containmentHTTPConfig())
	transport.SetSessionContextFunc(func(r *http.Request) context.Context {
		return context.WithValue(r.Context(), ownerKey{}, r.Header.Get("X-Owner"))
	})
	transport.SetSessionRequestContextFunc(func(_ context.Context, r *http.Request) (context.Context, error) {
		ctx := context.WithValue(r.Context(), ownerKey{}, "attempted-rewrite")
		return context.WithValue(ctx, freshKey{}, r.Header.Get("X-Fresh")), nil
	})

	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	initializeHeaders := map[string]string{"X-Owner": "immutable-owner"}
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, initializeHeaders)
	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID,
		map[string]string{"X-Owner": "different", "X-Fresh": "current-policy"},
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"observe","arguments":{}}}`)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("POST status = %d", response.StatusCode)
	}
	select {
	case got := <-observedCh:
		if got.owner != "immutable-owner" || got.fresh != "current-policy" {
			t.Fatalf("handler context = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("tool handler did not run")
	}
	eventuallyContainment(t, time.Second, func() bool { return transport.Stats().InFlightMessages == 0 })
}

func TestHTTPServerTransportRequestContextHookErrorsReleaseEveryLeaseKind(t *testing.T) {
	var hookCalls atomic.Int64
	var handlerCalls atomic.Int64
	server := NewServer()
	server.AddTool(Tool{Name: "never", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		handlerCalls.Add(1)
		return textToolResult("unexpected"), nil
	})
	transport := newContainmentTransport(t, server, containmentHTTPConfig())
	transport.SetSessionRequestContextFunc(func(context.Context, *http.Request) (context.Context, error) {
		hookCalls.Add(1)
		return nil, errors.New("current policy unavailable")
	})
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)
	session, ok := transport.getSession(sessionID)
	if !ok {
		t.Fatal("initialized session missing")
	}
	session.mu.Lock()
	lastActivity := session.lastActivity
	session.mu.Unlock()

	messageResponse := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"never","arguments":{}}}`)
	io.Copy(io.Discard, messageResponse.Body)
	messageResponse.Body.Close()
	if messageResponse.StatusCode != http.StatusServiceUnavailable || messageResponse.Header.Get("Retry-After") != "2" {
		t.Fatalf("message hook failure = %d retry=%q", messageResponse.StatusCode, messageResponse.Header.Get("Retry-After"))
	}

	pendingID, _, err := session.server.prepareCall()
	if err != nil {
		t.Fatal(err)
	}
	controlResponse := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil,
		`{"jsonrpc":"2.0","id":`+strconv.FormatInt(pendingID, 10)+`,"result":{}}`)
	io.Copy(io.Discard, controlResponse.Body)
	controlResponse.Body.Close()
	if controlResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("control hook failure status = %d", controlResponse.StatusCode)
	}
	if !session.server.hasPendingResponse(rawJSONID(pendingID)) {
		t.Fatal("hook failure consumed pending nested response")
	}
	session.server.removePending(pendingID)

	streamResponse := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodGet, sessionID, nil, "")
	io.Copy(io.Discard, streamResponse.Body)
	streamResponse.Body.Close()
	if streamResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("SSE hook failure status = %d", streamResponse.StatusCode)
	}

	if hookCalls.Load() != 3 || handlerCalls.Load() != 0 {
		t.Fatalf("hook/handler calls = %d/%d, want 3/0", hookCalls.Load(), handlerCalls.Load())
	}
	transport.mu.Lock()
	stats := transport.stats
	decodeInFlight := transport.decodeInFlight
	controlInFlight := transport.controlInFlight
	transport.mu.Unlock()
	session.mu.Lock()
	operations := session.operations
	messages := session.inFlightMessages
	controls := session.controlInFlight
	sseOpen := session.sseOpen
	activityAfter := session.lastActivity
	session.mu.Unlock()
	if stats.InFlightMessages != 0 || decodeInFlight != 0 || controlInFlight != 0 || operations != 0 || messages != 0 || controls != 0 || sseOpen {
		t.Fatalf("hook failures leaked counters: stats=%+v decode=%d control=%d ops=%d messages=%d controls=%d sse=%v",
			stats, decodeInFlight, controlInFlight, operations, messages, controls, sseOpen)
	}
	if !activityAfter.After(lastActivity) {
		t.Fatalf("admitted decode did not refresh idle activity: before=%v after=%v", lastActivity, activityAfter)
	}
}

func TestHTTPServerTransportSaturatedMessageDoesNotCallRequestContextHook(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := NewServer()
	server.AddTool(Tool{Name: "block", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(ctx context.Context, _ *RequestContext, _ map[string]any) (*ToolResult, error) {
		started <- struct{}{}
		select {
		case <-release:
			return textToolResult("done"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = 1
	config.MaxConcurrentMessagesPerSession = 1
	transport := newContainmentTransport(t, server, config)
	var hookCalls atomic.Int64
	transport.SetSessionRequestContextFunc(func(sessionCtx context.Context, _ *http.Request) (context.Context, error) {
		hookCalls.Add(1)
		return sessionCtx, nil
	})
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"block","arguments":{}}}`
	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, call)
	response.Body.Close()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first handler did not start")
	}
	if hookCalls.Load() != 1 {
		t.Fatalf("first hook calls = %d, want 1", hookCalls.Load())
	}

	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, call)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("saturated message status = %d, want 429", response.StatusCode)
	}
	if hookCalls.Load() != 1 {
		t.Fatalf("saturated message called policy hook; calls=%d", hookCalls.Load())
	}
	close(release)
	eventuallyContainment(t, time.Second, func() bool { return transport.Stats().InFlightMessages == 0 })
}

func TestHTTPServerTransportRevokedBetweenAuthorizationAndPolicyHookNeverDispatches(t *testing.T) {
	authorizerEntered := make(chan struct{}, 1)
	continueAuthorization := make(chan struct{})
	var revoked atomic.Bool
	var hookCalls atomic.Int64
	var handlerCalls atomic.Int64
	server := NewServer()
	server.AddTool(Tool{Name: "never", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		handlerCalls.Add(1)
		return textToolResult("unexpected"), nil
	})
	transport := newContainmentTransport(t, server, containmentHTTPConfig())
	transport.SetSessionAuthorizer(func(context.Context, *http.Request) (bool, error) {
		authorizerEntered <- struct{}{}
		<-continueAuthorization
		return true, nil
	})
	transport.SetSessionRequestContextFunc(func(sessionCtx context.Context, _ *http.Request) (context.Context, error) {
		hookCalls.Add(1)
		if revoked.Load() {
			return nil, errors.New("token revoked after authorization")
		}
		return sessionCtx, nil
	})
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)

	responseCh := make(chan *http.Response, 1)
	errorCh := make(chan error, 1)
	go func() {
		request, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"never","arguments":{}}}`))
		request.Header.Set("Mcp-Session-Id", sessionID)
		response, err := srv.Client().Do(request)
		if err != nil {
			errorCh <- err
			return
		}
		responseCh <- response
	}()
	select {
	case <-authorizerEntered:
	case <-time.After(time.Second):
		t.Fatal("authorizer did not start")
	}
	revoked.Store(true)
	close(continueAuthorization)
	select {
	case err := <-errorCh:
		t.Fatal(err)
	case response := <-responseCh:
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("revoked policy status = %d, want 503", response.StatusCode)
		}
	case <-time.After(time.Second):
		t.Fatal("revoked request did not finish")
	}
	if hookCalls.Load() != 1 || handlerCalls.Load() != 0 {
		t.Fatalf("hook/handler calls = %d/%d, want 1/0", hookCalls.Load(), handlerCalls.Load())
	}
	if stats := transport.Stats(); stats.InFlightMessages != 0 {
		t.Fatalf("revoked hook leaked handler lease: %+v", stats)
	}
}

func TestHTTPServerTransportClosePrincipalRevokesActiveAndProvisionalOperations(t *testing.T) {
	started := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	server := NewServer()
	server.AddTool(Tool{Name: "block", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(ctx context.Context, _ *RequestContext, _ map[string]any) (*ToolResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		cancelled <- struct{}{}
		return nil, ctx.Err()
	})
	transport := newContainmentTransport(t, server, containmentHTTPConfig())
	transport.SetSessionPrincipalFunc(func(r *http.Request) (string, error) { return r.Header.Get("X-Principal"), nil })
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)

	headers := map[string]string{"X-Principal": "token-hash-a"}
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, headers)
	provisionalReq := httptest.NewRequest(http.MethodPost, "/", nil)
	provisionalReq.Header.Set("X-Principal", "token-hash-a")
	provisional, err := transport.newSession(provisionalReq)
	if err != nil {
		t.Fatal(err)
	}

	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, headers,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"block","arguments":{}}}`)
	response.Body.Close()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocking handler did not start")
	}

	if got := transport.ClosePrincipal("token-hash-a"); got != 2 {
		t.Fatalf("ClosePrincipal = %d, want active + provisional = 2", got)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("active handler context was not cancelled")
	}
	select {
	case <-provisional.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("provisional context was not cancelled")
	}
	eventuallyContainment(t, time.Second, func() bool { return transport.Stats().InFlightMessages == 0 })
	stats := transport.Stats()
	if stats.ActiveSessions != 0 || stats.ProvisionalSessions != 0 {
		t.Fatalf("revocation retained sessions: %+v", stats)
	}
}

func TestHTTPServerTransportPrincipalValidatorFailsClosedWithoutMassRevokingOnError(t *testing.T) {
	transport := newContainmentTransport(t, NewServer(), containmentHTTPConfig())
	transport.SetSessionPrincipalFunc(func(r *http.Request) (string, error) { return r.Header.Get("X-Principal"), nil })
	transport.SetSessionPrincipalValidator(func(context.Context, string) (bool, error) {
		return false, errors.New("identity store unavailable")
	})
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	headers := map[string]string{"X-Principal": "token-hash-a"}
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, headers)

	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, headers,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("validator error status = %d, want 500", response.StatusCode)
	}
	if transport.Stats().ActiveSessions != 1 {
		t.Fatalf("validator outage revoked session: %+v", transport.Stats())
	}

	transport.SetSessionPrincipalValidator(func(context.Context, string) (bool, error) { return false, nil })
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, headers,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked validator status = %d, want 403", response.StatusCode)
	}
	if transport.Stats().ActiveSessions != 0 {
		t.Fatalf("validator revocation retained session: %+v", transport.Stats())
	}
}

func TestHTTPServerTransportSweeperRevokesPrincipalAndExpiresSessions(t *testing.T) {
	config := containmentHTTPConfig()
	config.IdleTimeout = 30 * time.Millisecond
	config.AbsoluteLifetime = 200 * time.Millisecond
	transport := newContainmentTransport(t, NewServer(), config)
	transport.SetSessionPrincipalFunc(func(r *http.Request) (string, error) { return r.Header.Get("X-Principal"), nil })
	var revoked atomic.Bool
	transport.SetSessionPrincipalValidator(func(_ context.Context, principal string) (bool, error) {
		return principal != "revoked" || !revoked.Load(), nil
	})
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)

	revokedID := initializeContainmentSession(t, srv.Client(), srv.URL, map[string]string{"X-Principal": "revoked"})
	_ = revokedID
	revoked.Store(true)
	transport.sweepOnce(time.Now())
	if transport.Stats().ActiveSessions != 0 {
		t.Fatalf("sweeper did not revoke indexed principal: %+v", transport.Stats())
	}

	initializeContainmentSession(t, srv.Client(), srv.URL, map[string]string{"X-Principal": "valid"})
	eventuallyContainment(t, time.Second, func() bool {
		stats := transport.Stats()
		return stats.ActiveSessions == 0 && stats.ExpiredSessions >= 1
	})
}

func TestHTTPServerTransportSweeperRetainsPrincipalOnValidatorErrorThenRetries(t *testing.T) {
	transport := newContainmentTransport(t, NewServer(), containmentHTTPConfig())
	transport.SetSessionPrincipalFunc(func(r *http.Request) (string, error) { return r.Header.Get("X-Principal"), nil })
	var outage atomic.Bool
	outage.Store(true)
	transport.SetSessionPrincipalValidator(func(context.Context, string) (bool, error) {
		if outage.Load() {
			return false, errors.New("identity store unavailable")
		}
		return false, nil
	})
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	initializeContainmentSession(t, srv.Client(), srv.URL, map[string]string{"X-Principal": "principal-a"})

	transport.sweepOnce(time.Now())
	if transport.Stats().ActiveSessions != 1 {
		t.Fatalf("validator error mass-closed session: %+v", transport.Stats())
	}
	outage.Store(false)
	transport.sweepOnce(time.Now())
	if transport.Stats().ActiveSessions != 0 {
		t.Fatalf("validator retry did not revoke session: %+v", transport.Stats())
	}
}

func TestHTTPServerTransportConcurrencyLimitsReleaseOnEveryPath(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	server := NewServer()
	server.AddTool(Tool{Name: "block", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(ctx context.Context, _ *RequestContext, _ map[string]any) (*ToolResult, error) {
		started <- struct{}{}
		select {
		case <-release:
			return textToolResult("done"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = 2
	config.MaxConcurrentMessagesPerSession = 1
	transport := newContainmentTransport(t, server, config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionA := initializeContainmentSession(t, srv.Client(), srv.URL, nil)
	sessionB := initializeContainmentSession(t, srv.Client(), srv.URL, nil)
	sessionC := initializeContainmentSession(t, srv.Client(), srv.URL, nil)
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"block","arguments":{}}}`

	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionA, nil, call)
	response.Body.Close()
	<-started
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionA, nil, call)
	response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") != "2" {
		t.Fatalf("per-session rejection = %d retry=%q", response.StatusCode, response.Header.Get("Retry-After"))
	}
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionB, nil, call)
	response.Body.Close()
	<-started

	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionC, nil, call)
	response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("global rejection status = %d, want 429", response.StatusCode)
	}
	close(release)
	eventuallyContainment(t, time.Second, func() bool { return transport.Stats().InFlightMessages == 0 })

	// Parse failures acquire and release the same per-session/global lease.
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionA, nil, `{`)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || transport.Stats().InFlightMessages != 0 {
		t.Fatalf("parse failure leaked lease: status=%d stats=%+v", response.StatusCode, transport.Stats())
	}
}

func TestHTTPServerTransportNestedResponsesUseBoundedControlLaneWithoutDeadlock(t *testing.T) {
	const concurrent = 4
	finished := make(chan error, concurrent)
	server := NewServer()
	server.AddTool(Tool{Name: "sample", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(ctx context.Context, rc *RequestContext, _ map[string]any) (*ToolResult, error) {
		_, err := rc.CreateMessage(ctx, &CreateMessageParams{
			Messages:  []SamplingMessage{{Role: "user", Content: MarshalSamplingContent(Content{Type: "text", Text: "hello"})}},
			MaxTokens: 1,
		})
		finished <- err
		if err != nil {
			return nil, err
		}
		return textToolResult("done"), nil
	})
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = concurrent
	config.MaxConcurrentMessagesPerSession = concurrent
	config.OutboxMaxMessages = 32
	transport := newContainmentTransport(t, server, config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)

	initializeBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"sampling":{}},"clientInfo":{"name":"test-client","version":"1"}}}`
	initializeResponse := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, "", nil, initializeBody)
	io.Copy(io.Discard, initializeResponse.Body)
	initializeResponse.Body.Close()
	if initializeResponse.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d", initializeResponse.StatusCode)
	}
	sessionID := initializeResponse.Header.Get("Mcp-Session-Id")
	session, ok := transport.getSession(sessionID)
	if !ok {
		t.Fatal("initialized session missing")
	}

	for i := 0; i < concurrent; i++ {
		body := `{"jsonrpc":"2.0","id":` + strconv.Itoa(10+i) + `,"method":"tools/call","params":{"name":"sample","arguments":{}}}`
		response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, body)
		response.Body.Close()
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("tool POST %d status = %d", i, response.StatusCode)
		}
	}
	eventuallyContainment(t, time.Second, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return len(session.outbox) >= concurrent && session.inFlightMessages == concurrent
	})

	responseIDs := make([]int64, 0, concurrent)
	for len(responseIDs) < concurrent {
		payload, ok := session.dequeue()
		if !ok {
			t.Fatal("sampling request disappeared from FIFO")
		}
		var nested jsonRPCMessage
		if err := json.Unmarshal(payload, &nested); err != nil {
			t.Fatalf("decode nested request: %v", err)
		}
		if nested.Method != "sampling/createMessage" {
			t.Fatalf("outbox method = %q, want sampling/createMessage", nested.Method)
		}
		id, err := parsePendingID(nested.ID)
		if err != nil {
			t.Fatal(err)
		}
		responseIDs = append(responseIDs, id)
	}

	for _, id := range responseIDs {
		body := `{"jsonrpc":"2.0","id":` + strconv.FormatInt(id, 10) + `,"result":{"role":"assistant","content":{"type":"text","text":"ok"},"model":"test","stopReason":"endTurn"}}`
		response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, body)
		response.Body.Close()
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("nested response %d status = %d", id, response.StatusCode)
		}
	}
	for range concurrent {
		select {
		case err := <-finished:
			if err != nil {
				t.Fatalf("nested sampling failed: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("four blocked sampling handlers deadlocked")
		}
	}
	eventuallyContainment(t, time.Second, func() bool {
		transport.mu.Lock()
		defer transport.mu.Unlock()
		return transport.stats.InFlightMessages == 0 && transport.decodeInFlight == 0 && transport.controlInFlight == 0
	})
}

func TestHTTPServerTransportHostileUnmatchedResponsesCannotEscapeDecodeBound(t *testing.T) {
	const limit = 4
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = limit
	config.MaxConcurrentMessagesPerSession = limit
	transport := newContainmentTransport(t, NewServer(), config)
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(session) {
		t.Fatalf("activate: %v", err)
	}

	type blockedRequest struct {
		writer   *io.PipeWriter
		recorder *httptest.ResponseRecorder
	}
	blocked := make([]blockedRequest, 0, limit)
	var wg sync.WaitGroup
	for i := 0; i < limit; i++ {
		reader, writer := io.Pipe()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/", reader)
		request.Header.Set("Mcp-Session-Id", session.id)
		blocked = append(blocked, blockedRequest{writer: writer, recorder: recorder})
		wg.Add(1)
		go func() {
			defer wg.Done()
			transport.ServeHTTP(recorder, request)
		}()
	}
	eventuallyContainment(t, time.Second, func() bool {
		transport.mu.Lock()
		defer transport.mu.Unlock()
		return transport.decodeInFlight == limit
	})

	overflowRecorder := httptest.NewRecorder()
	overflowRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":999,"result":{}}`))
	overflowRequest.Header.Set("Mcp-Session-Id", session.id)
	transport.ServeHTTP(overflowRecorder, overflowRequest)
	if overflowRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("decode saturation status = %d, want 429", overflowRecorder.Code)
	}

	for i, request := range blocked {
		_, _ = io.WriteString(request.writer, `{"jsonrpc":"2.0","id":`+strconv.Itoa(1000+i)+`,"result":{}}`)
		_ = request.writer.Close()
	}
	wg.Wait()
	for i, request := range blocked {
		if request.recorder.Code != http.StatusBadRequest {
			t.Fatalf("unmatched response %d status = %d, want 400", i, request.recorder.Code)
		}
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.decodeInFlight != 0 || transport.controlInFlight != 0 || transport.stats.InFlightMessages != 0 {
		t.Fatalf("hostile responses leaked lanes: decode=%d control=%d stats=%+v", transport.decodeInFlight, transport.controlInFlight, transport.stats)
	}
}

func TestHTTPServerTransportControlLaneHasGlobalAndPerSessionBounds(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = 3
	config.MaxConcurrentMessagesPerSession = 2
	transport := newContainmentTransport(t, NewServer(), config)
	sessionA, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(sessionA) {
		t.Fatalf("activate A: %v", err)
	}
	sessionB, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(sessionB) {
		t.Fatalf("activate B: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	leaseA1, err := transport.acquireSessionLease(sessionA, request, nil, leaseControl)
	if err != nil {
		t.Fatal(err)
	}
	leaseA2, err := transport.acquireSessionLease(sessionA, request, nil, leaseControl)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.acquireSessionLease(sessionA, request, nil, leaseControl); !errors.Is(err, errHTTPMessageLimit) {
		t.Fatalf("per-session control limit = %v", err)
	}
	leaseB, err := transport.acquireSessionLease(sessionB, request, nil, leaseControl)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.acquireSessionLease(sessionB, request, nil, leaseControl); !errors.Is(err, errHTTPMessageLimit) {
		t.Fatalf("global control limit = %v", err)
	}
	leaseA1.release()
	leaseA2.release()
	leaseB.release()
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.controlInFlight != 0 {
		t.Fatalf("control leases retained: %d", transport.controlInFlight)
	}
}

func TestHTTPServerTransportAllowsExactlyOneSSEStreamPerSession(t *testing.T) {
	transport := newContainmentTransport(t, NewServer(), containmentHTTPConfig())
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)

	firstRequest, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	firstRequest.Header.Set("Mcp-Session-Id", sessionID)
	first, err := srv.Client().Do(firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Body.Close() })
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first SSE status = %d", first.StatusCode)
	}

	second := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodGet, sessionID, nil, "")
	second.Body.Close()
	if second.StatusCode != http.StatusConflict || second.Header.Get("Retry-After") != "2" {
		t.Fatalf("second SSE = %d retry=%q, want 409/2", second.StatusCode, second.Header.Get("Retry-After"))
	}
	first.Body.Close()
	eventuallyContainment(t, time.Second, func() bool {
		session, ok := transport.getSession(sessionID)
		if !ok {
			return false
		}
		session.mu.Lock()
		defer session.mu.Unlock()
		return !session.sseOpen && session.operations == 0
	})
}

func TestHTTPServerTransportOutboxIsFIFOAndOverflowClosesSession(t *testing.T) {
	config := containmentHTTPConfig()
	config.OutboxMaxMessages = 3
	config.OutboxMaxBytes = 6
	transport := newContainmentTransport(t, NewServer(), config)
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(session) {
		t.Fatalf("activate session: %v", err)
	}
	for _, payload := range [][]byte{[]byte("a"), []byte("bb"), []byte("ccc")} {
		if err := transport.enqueue(session, payload); err != nil {
			t.Fatalf("enqueue %q: %v", payload, err)
		}
	}
	for _, want := range []string{"a", "bb", "ccc"} {
		got, ok := session.dequeue()
		if !ok || string(got) != want {
			t.Fatalf("dequeue = %q/%v, want %q", got, ok, want)
		}
	}
	if stats := transport.Stats(); stats.ActiveSessions != 1 || stats.OutboxOverflowClosures != 0 {
		t.Fatalf("FIFO accounting = %+v", stats)
	}

	if err := transport.enqueue(session, []byte("1234567")); !errors.Is(err, errHTTPOutboxOverflow) {
		t.Fatalf("oversize enqueue error = %v", err)
	}
	stats := transport.Stats()
	if stats.ActiveSessions != 0 || stats.OutboxOverflowClosures != 1 {
		t.Fatalf("overflow accounting = %+v", stats)
	}
	select {
	case <-session.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("overflow did not cancel session")
	}
}

func TestHTTPServerTransportOutboxMessageCountOverflowClosesSession(t *testing.T) {
	config := containmentHTTPConfig()
	config.OutboxMaxMessages = 1
	config.OutboxMaxBytes = 1024
	transport := newContainmentTransport(t, NewServer(), config)
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(session) {
		t.Fatalf("activate: %v", err)
	}
	if err := transport.enqueue(session, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := transport.enqueue(session, []byte("second")); !errors.Is(err, errHTTPOutboxOverflow) {
		t.Fatalf("second enqueue = %v, want overflow", err)
	}
	if stats := transport.Stats(); stats.ActiveSessions != 0 || stats.OutboxOverflowClosures != 1 {
		t.Fatalf("message overflow accounting = %+v", stats)
	}
}

func TestHTTPServerTransportDeleteIsLinearizableAgainstAuthorizedRequest(t *testing.T) {
	authorizerEntered := make(chan struct{}, 1)
	allowAuthorizer := make(chan struct{})
	var toolCalls atomic.Int64
	server := NewServer()
	server.AddTool(Tool{Name: "count", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		toolCalls.Add(1)
		return textToolResult("ok"), nil
	})
	transport := newContainmentTransport(t, server, containmentHTTPConfig())
	transport.SetSessionPrincipalFunc(func(r *http.Request) (string, error) { return r.Header.Get("X-Principal"), nil })
	transport.SetSessionAuthorizer(func(context.Context, *http.Request) (bool, error) {
		authorizerEntered <- struct{}{}
		<-allowAuthorizer
		return true, nil
	})
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	headers := map[string]string{"X-Principal": "principal-a"}
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, headers)

	responseCh := make(chan *http.Response, 1)
	errorCh := make(chan error, 1)
	go func() {
		request, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"count","arguments":{}}}`))
		request.Header.Set("Mcp-Session-Id", sessionID)
		request.Header.Set("X-Principal", "principal-a")
		response, err := srv.Client().Do(request)
		if err != nil {
			errorCh <- err
			return
		}
		responseCh <- response
	}()
	select {
	case <-authorizerEntered:
	case <-time.After(time.Second):
		t.Fatal("authorizer did not start")
	}
	if got := transport.ClosePrincipal("principal-a"); got != 1 {
		t.Fatalf("ClosePrincipal = %d, want 1", got)
	}
	close(allowAuthorizer)
	select {
	case err := <-errorCh:
		t.Fatal(err)
	case response := <-responseCh:
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("racing POST status = %d, want 404", response.StatusCode)
		}
	case <-time.After(time.Second):
		t.Fatal("racing POST did not finish")
	}
	if toolCalls.Load() != 0 {
		t.Fatalf("tool ran %d times after revocation linearized", toolCalls.Load())
	}
}

func TestHTTPServerTransportAbsoluteExpiryCancelsActiveLease(t *testing.T) {
	config := containmentHTTPConfig()
	config.IdleTimeout = time.Second
	config.AbsoluteLifetime = 2 * time.Second
	transport := newContainmentTransport(t, NewServer(), config)
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(session) {
		t.Fatalf("activate: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	lease, err := transport.acquireSessionLease(session, request, nil, leaseSSE)
	if err != nil {
		t.Fatal(err)
	}
	// Idle expiry skips a live operation.
	transport.sweepOnce(session.lastActivity.Add(config.IdleTimeout + time.Millisecond))
	if transport.Stats().ActiveSessions != 1 {
		t.Fatalf("idle sweep closed active operation: %+v", transport.Stats())
	}
	transport.sweepOnce(session.createdAt.Add(config.AbsoluteLifetime))
	select {
	case <-lease.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("absolute expiry did not cancel active lease")
	}
	lease.release()
	stats := transport.Stats()
	if stats.ActiveSessions != 0 || stats.ExpiredSessions != 1 {
		t.Fatalf("absolute expiry accounting = %+v", stats)
	}
}

func TestHTTPServerTransportPerSessionDecodeLimitContainsIncompleteBodies(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = 4
	config.MaxConcurrentMessagesPerSession = 2
	config.RequestBodyTimeout = 5 * time.Second
	transport := newContainmentTransport(t, NewServer(), config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionA := initializeContainmentSession(t, srv.Client(), srv.URL, nil)
	sessionB := initializeContainmentSession(t, srv.Client(), srv.URL, nil)

	bodyA1, resultA1 := startIncompleteHTTPPost(t, srv.Client(), srv.URL, sessionA)
	bodyA2, resultA2 := startIncompleteHTTPPost(t, srv.Client(), srv.URL, sessionA)
	eventuallyContainment(t, time.Second, func() bool {
		stats := transport.Stats()
		session, ok := transport.getSession(sessionA)
		if !ok {
			return false
		}
		session.mu.Lock()
		defer session.mu.Unlock()
		return stats.InFlightDecodes == 2 && session.decodeInFlight == 2
	})

	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionA, nil,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third same-session decode status = %d, want 429", response.StatusCode)
	}

	// The offending session cannot monopolize the remaining global lanes.
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionB, nil,
		`{"jsonrpc":"2.0","id":4,"method":"tools/list","params":{}}`)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("other-session POST status = %d, want 202", response.StatusCode)
	}

	bodyA1.Close()
	bodyA2.Close()
	waitIncompleteHTTPResult(t, resultA1)
	waitIncompleteHTTPResult(t, resultA2)
	eventuallyContainment(t, time.Second, func() bool { return transport.Stats().InFlightDecodes == 0 })
}

func TestHTTPServerTransportPrincipalBoundInitializeDecodeCannotMonopolizeGlobalCapacity(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = 3
	config.MaxConcurrentMessagesPerSession = 1
	config.RequestBodyTimeout = 5 * time.Second
	transport := newContainmentTransport(t, NewServer(), config)
	var derivations atomic.Int64
	transport.SetSessionPrincipalFunc(func(r *http.Request) (string, error) {
		derivations.Add(1)
		return r.Header.Get("X-Principal"), nil
	})
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)

	bodyA, resultA := startIncompleteHTTPPostWithHeaders(t, srv.Client(), srv.URL, "", map[string]string{"X-Principal": "principal-a"})
	eventuallyContainment(t, time.Second, func() bool {
		transport.mu.Lock()
		defer transport.mu.Unlock()
		return transport.initializingByPrincipal["principal-a"] == 1 && transport.decodeInFlight == 1
	})
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	samePrincipal := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, "",
		map[string]string{"X-Principal": "principal-a"}, initialize)
	samePrincipal.Body.Close()
	if samePrincipal.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("same-principal initialize saturation = %d, want 429", samePrincipal.StatusCode)
	}

	otherPrincipal := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, "",
		map[string]string{"X-Principal": "principal-b"}, initialize)
	io.Copy(io.Discard, otherPrincipal.Body)
	otherPrincipal.Body.Close()
	if otherPrincipal.StatusCode != http.StatusOK {
		t.Fatalf("other-principal initialize status = %d, want 200", otherPrincipal.StatusCode)
	}
	otherSessionID := otherPrincipal.Header.Get("Mcp-Session-Id")
	otherSession, ok := transport.getSession(otherSessionID)
	if !ok || otherSession.principal != "principal-b" {
		t.Fatalf("other-principal session = %#v", otherSession)
	}
	if got := derivations.Load(); got != 3 {
		t.Fatalf("principal derivations = %d, want exactly one per request (no create-time re-derivation)", got)
	}

	bodyA.Close()
	waitIncompleteHTTPResult(t, resultA)
	eventuallyContainment(t, time.Second, func() bool {
		transport.mu.Lock()
		defer transport.mu.Unlock()
		_, retained := transport.initializingByPrincipal["principal-a"]
		return !retained && transport.decodeInFlight == 0
	})
}

func TestHTTPServerTransportAnonymousInitializeDecodeUsesGlobalBoundOnly(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = 2
	config.MaxConcurrentMessagesPerSession = 1
	config.RequestBodyTimeout = 5 * time.Second
	transport := newContainmentTransport(t, NewServer(), config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	body1, result1 := startIncompleteHTTPPost(t, srv.Client(), srv.URL, "")
	body2, result2 := startIncompleteHTTPPost(t, srv.Client(), srv.URL, "")
	eventuallyContainment(t, time.Second, func() bool { return transport.Stats().InFlightDecodes == 2 })
	third := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, "", nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	third.Body.Close()
	if third.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("anonymous global decode saturation = %d, want 429", third.StatusCode)
	}
	body1.Close()
	body2.Close()
	waitIncompleteHTTPResult(t, result1)
	waitIncompleteHTTPResult(t, result2)
}

func TestHTTPServerTransportSlowValidDecodeRefreshesShortIdleSession(t *testing.T) {
	config := containmentHTTPConfig()
	config.IdleTimeout = 20 * time.Millisecond
	config.AbsoluteLifetime = time.Second
	config.RequestBodyTimeout = 250 * time.Millisecond
	transport := newContainmentTransport(t, NewServer(), config)
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(session) {
		t.Fatalf("activate: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Mcp-Session-Id", session.id)
	request.Body = &delayedReadCloser{
		data:  []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`),
		delay: 60 * time.Millisecond,
	}
	recorder := httptest.NewRecorder()
	transport.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("slow valid POST status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
	if _, ok := transport.getSession(session.id); !ok {
		t.Fatal("slow valid decode was expired between decode and dispatch")
	}
	session.server.WaitIdle()
}

func TestHTTPServerTransportReadDeadlineReleasesRealNetworkDecodeSlot(t *testing.T) {
	config := containmentHTTPConfig()
	config.RequestBodyTimeout = 40 * time.Millisecond
	transport := newContainmentTransport(t, NewServer(), config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)

	address := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	request := "POST / HTTP/1.1\r\nHost: " + address + "\r\nContent-Length: 100\r\nMcp-Session-Id: " + sessionID + "\r\nConnection: close\r\n\r\n{"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("read timeout response: %v", err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("slow body status = %d, want 408", response.StatusCode)
	}
	eventuallyContainment(t, time.Second, func() bool {
		stats := transport.Stats()
		return stats.InFlightDecodes == 0 && stats.InFlightProtectedDecodes == 0
	})
}

func TestHTTPServerTransportBodyCloseFallbackUnblocksCustomReader(t *testing.T) {
	config := containmentHTTPConfig()
	config.RequestBodyTimeout = 20 * time.Millisecond
	transport := newContainmentTransport(t, NewServer(), config)
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(session) {
		t.Fatalf("activate: %v", err)
	}
	body := newCloseUnblocksReader()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Mcp-Session-Id", session.id)
	request.Body = body
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		transport.ServeHTTP(recorder, request)
		close(done)
	}()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("custom body Read did not start")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("body timeout did not close-unblock custom reader")
	}
	if recorder.Code != http.StatusRequestTimeout {
		t.Fatalf("custom slow body status = %d, want 408", recorder.Code)
	}
	stats := transport.Stats()
	if stats.InFlightDecodes != 0 {
		t.Fatalf("custom reader retained decode slot: %+v", stats)
	}
}

func TestHTTPServerTransportProtectedDecodeDeliversNestedResponseWhenOrdinaryDecodesAreFull(t *testing.T) {
	finished := make(chan error, 1)
	server := NewServer()
	server.AddTool(Tool{Name: "sample", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(ctx context.Context, rc *RequestContext, _ map[string]any) (*ToolResult, error) {
		_, err := rc.CreateMessage(ctx, &CreateMessageParams{
			Messages:  []SamplingMessage{{Role: "user", Content: MarshalSamplingContent(Content{Type: "text", Text: "hello"})}},
			MaxTokens: 1,
		})
		finished <- err
		if err != nil {
			return nil, err
		}
		return textToolResult("done"), nil
	})
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = 2
	config.MaxConcurrentMessagesPerSession = 2
	config.RequestBodyTimeout = 5 * time.Second
	transport := newContainmentTransport(t, server, config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	initializeBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"sampling":{}},"clientInfo":{"name":"test-client","version":"1"}}}`
	initializeResponse := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, "", nil, initializeBody)
	initializeResponse.Body.Close()
	sessionID := initializeResponse.Header.Get("Mcp-Session-Id")
	session, ok := transport.getSession(sessionID)
	if !ok {
		t.Fatal("initialized session missing")
	}
	toolResponse := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sample","arguments":{}}}`)
	toolResponse.Body.Close()
	eventuallyContainment(t, time.Second, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return len(session.outbox) > 0
	})
	payload, ok := session.dequeue()
	if !ok {
		t.Fatal("nested sampling request missing")
	}
	var nested jsonRPCMessage
	if err := json.Unmarshal(payload, &nested); err != nil {
		t.Fatal(err)
	}
	pendingID, err := parsePendingID(nested.ID)
	if err != nil {
		t.Fatal(err)
	}

	body1, result1 := startIncompleteHTTPPost(t, srv.Client(), srv.URL, sessionID)
	body2, result2 := startIncompleteHTTPPost(t, srv.Client(), srv.URL, sessionID)
	eventuallyContainment(t, time.Second, func() bool { return transport.Stats().InFlightDecodes == 2 })

	// The reserve is not a bypass for arbitrary/unmatched traffic.
	unmatched := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil,
		`{"jsonrpc":"2.0","id":999999,"result":{}}`)
	unmatched.Body.Close()
	if unmatched.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("unmatched protected decode status = %d, want 429", unmatched.StatusCode)
	}

	matchedBody := `{"jsonrpc":"2.0","id":` + strconv.FormatInt(pendingID, 10) + `,"result":{"role":"assistant","content":{"type":"text","text":"ok"},"model":"test","stopReason":"endTurn"}}`
	matched := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, matchedBody)
	matched.Body.Close()
	if matched.StatusCode != http.StatusAccepted {
		t.Fatalf("matched protected response status = %d, want 202", matched.StatusCode)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("nested sampling failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("protected response did not release blocked sampling handler")
	}
	body1.Close()
	body2.Close()
	waitIncompleteHTTPResult(t, result1)
	waitIncompleteHTTPResult(t, result2)
	eventuallyContainment(t, time.Second, func() bool {
		stats := transport.Stats()
		return stats.InFlightDecodes == 0 && stats.InFlightProtectedDecodes == 0 && stats.InFlightControlResponses == 0
	})
}

func TestHTTPServerTransportProtectedDecodeReserveIsOnePerSession(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = 2
	config.MaxConcurrentMessagesPerSession = 2
	config.RequestBodyTimeout = time.Second
	transport := newContainmentTransport(t, NewServer(), config)
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(session) {
		t.Fatalf("activate: %v", err)
	}
	pendingID, _, err := session.server.prepareCall()
	if err != nil {
		t.Fatal(err)
	}
	defer session.server.removePending(pendingID)
	var leases []*httpDecodeLease
	for range config.MaxConcurrentMessagesPerSession {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		lease, err := transport.acquireDecode(session, "", httptest.NewRecorder(), request)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	protectedRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	protected, err := transport.acquireDecode(session, "", httptest.NewRecorder(), protectedRequest)
	if err != nil || !protected.protected {
		t.Fatalf("protected reserve = %#v, %v", protected, err)
	}
	secondProtectedRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	if _, err := transport.acquireDecode(session, "", httptest.NewRecorder(), secondProtectedRequest); !errors.Is(err, errHTTPMessageLimit) {
		t.Fatalf("second protected decode = %v, want bounded rejection", err)
	}
	if stats := transport.Stats(); stats.InFlightDecodes != 2 || stats.InFlightProtectedDecodes != 1 {
		t.Fatalf("decode reserve stats = %+v", stats)
	}
	protected.release()
	for _, lease := range leases {
		lease.release()
	}
}

func TestHTTPServerTransportGlobalSSELimitAndStats(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = 2
	config.MaxConcurrentMessagesPerSession = 2
	config.MaxSessions = 4
	config.MaxSessionsPerPrincipal = 2
	transport := newContainmentTransport(t, NewServer(), config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionIDs := []string{
		initializeContainmentSession(t, srv.Client(), srv.URL, nil),
		initializeContainmentSession(t, srv.Client(), srv.URL, nil),
		initializeContainmentSession(t, srv.Client(), srv.URL, nil),
	}
	open := func(sessionID string) *http.Response {
		request, err := http.NewRequest(http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Mcp-Session-Id", sessionID)
		response, err := srv.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := open(sessionIDs[0])
	second := open(sessionIDs[1])
	defer second.Body.Close()
	if first.StatusCode != http.StatusOK || second.StatusCode != http.StatusOK {
		t.Fatalf("initial SSE statuses = %d/%d", first.StatusCode, second.StatusCode)
	}
	if got := transport.Stats().InFlightSSEStreams; got != 2 {
		t.Fatalf("InFlightSSEStreams = %d, want 2", got)
	}
	third := open(sessionIDs[2])
	third.Body.Close()
	if third.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("global SSE saturation status = %d, want 429", third.StatusCode)
	}
	first.Body.Close()
	eventuallyContainment(t, time.Second, func() bool { return transport.Stats().InFlightSSEStreams == 1 })
	third = open(sessionIDs[2])
	if third.StatusCode != http.StatusOK {
		third.Body.Close()
		t.Fatalf("SSE after capacity release = %d, want 200", third.StatusCode)
	}
	third.Body.Close()
	eventuallyContainment(t, time.Second, func() bool { return transport.Stats().InFlightSSEStreams == 1 })
}

func TestHTTPServerTransportBlockedSSEWriteUnblocksOnRevocationExpiryAndDeadline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close func(*HTTPServerTransport, *httpServerSession)
	}{
		{name: "principal revocation", close: func(transport *HTTPServerTransport, session *httpServerSession) {
			if got := transport.ClosePrincipal(session.principal); got != 1 {
				t.Fatalf("ClosePrincipal = %d, want 1", got)
			}
		}},
		{name: "absolute expiry", close: func(transport *HTTPServerTransport, session *httpServerSession) {
			transport.sweepOnce(session.createdAt.Add(transport.config.AbsoluteLifetime))
		}},
		{name: "write deadline", close: func(*HTTPServerTransport, *httpServerSession) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := containmentHTTPConfig()
			transport := newContainmentTransport(t, NewServer(), config)
			transport.sseWriteTimeout = 30 * time.Millisecond
			transport.SetSessionPrincipalFunc(func(r *http.Request) (string, error) { return r.Header.Get("X-Principal"), nil })
			initializeRequest := httptest.NewRequest(http.MethodPost, "/", nil)
			initializeRequest.Header.Set("X-Principal", "principal-a")
			session, err := transport.newSession(initializeRequest)
			if err != nil || !transport.activateSession(session) {
				t.Fatalf("activate: %v", err)
			}
			if err := transport.enqueue(session, []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)); err != nil {
				t.Fatal(err)
			}
			writer := newDeadlineBlockingResponseWriter()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Mcp-Session-Id", session.id)
			request.Header.Set("X-Principal", "principal-a")
			done := make(chan struct{})
			go func() {
				transport.ServeHTTP(writer, request)
				close(done)
			}()
			select {
			case <-writer.started:
			case <-time.After(time.Second):
				t.Fatal("SSE payload write did not block")
			}
			if got := transport.Stats().InFlightSSEStreams; got != 1 {
				t.Fatalf("InFlightSSEStreams while blocked = %d, want 1", got)
			}
			tc.close(transport, session)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("blocked SSE writer did not return")
			}
			if got := transport.Stats().InFlightSSEStreams; got != 0 {
				t.Fatalf("InFlightSSEStreams after unblock = %d, want 0", got)
			}
			select {
			case <-session.ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("failed/closed SSE session was not canceled")
			}
		})
	}
}

func TestHTTPServerTransportUnsupportedSSEWriterNeverStartsUnboundedWrite(t *testing.T) {
	transport := newContainmentTransport(t, NewServer(), containmentHTTPConfig())
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(session) {
		t.Fatalf("activate: %v", err)
	}
	writer := newUnsupportedBlockingResponseWriter()
	defer writer.unblockWrite()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Mcp-Session-Id", session.id)
	done := make(chan struct{})
	go func() {
		transport.ServeHTTP(writer, request)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("unsupported SSE writer retained handler")
	}
	select {
	case <-writer.writeStarted:
		t.Fatal("transport attempted an unbounded fallback Write")
	default:
	}
	if got := transport.Stats().InFlightSSEStreams; got != 0 {
		t.Fatalf("unsupported writer retained SSE capacity: %d", got)
	}
}

func TestHTTPServerTransportReapsExpiredSessionsAtExactCapacity(t *testing.T) {
	t.Run("global", func(t *testing.T) {
		config := containmentHTTPConfig()
		config.MaxSessions = 1
		config.MaxSessionsPerPrincipal = 1
		transport := newContainmentTransport(t, NewServer(), config)
		old, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
		if err != nil || !transport.activateSession(old) {
			t.Fatalf("activate old: %v", err)
		}
		old.mu.Lock()
		old.lastActivity = time.Now().Add(-config.IdleTimeout)
		old.mu.Unlock()
		created, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
		if err != nil {
			t.Fatalf("create at expired capacity: %v", err)
		}
		if created == old || transport.Stats().ExpiredSessions != 1 {
			t.Fatalf("opportunistic reap stats=%+v", transport.Stats())
		}
		select {
		case <-old.ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("reaped global session was not canceled")
		}
	})

	t.Run("per principal", func(t *testing.T) {
		config := containmentHTTPConfig()
		config.MaxSessions = 3
		config.MaxSessionsPerPrincipal = 1
		transport := newContainmentTransport(t, NewServer(), config)
		transport.SetSessionPrincipalFunc(func(r *http.Request) (string, error) { return r.Header.Get("X-Principal"), nil })
		request := func(principal string) *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.Header.Set("X-Principal", principal)
			return r
		}
		old, err := transport.newSession(request("principal-a"))
		if err != nil || !transport.activateSession(old) {
			t.Fatalf("activate old principal session: %v", err)
		}
		other, err := transport.newSession(request("principal-b"))
		if err != nil || !transport.activateSession(other) {
			t.Fatalf("activate other principal: %v", err)
		}
		old.mu.Lock()
		old.lastActivity = time.Now().Add(-config.IdleTimeout)
		old.mu.Unlock()
		if _, err := transport.newSession(request("principal-a")); err != nil {
			t.Fatalf("create at expired principal capacity: %v", err)
		}
		if _, ok := transport.getSession(other.id); !ok {
			t.Fatal("opportunistic principal reap removed unexpired other principal")
		}
	})
}

func TestHTTPServerTransportCloseIsConcurrentAndIdempotent(t *testing.T) {
	transport, err := NewHTTPServerTransportWithConfig(NewServer(), containmentHTTPConfig())
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := transport.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	wg.Wait()
	stats := transport.Stats()
	if stats.ActiveSessions != 0 || stats.ProvisionalSessions != 0 || len(transport.sessions) != 0 {
		t.Fatalf("Close retained sessions: stats=%+v sessions=%d", stats, len(transport.sessions))
	}
}

func initializeContainmentSession(t *testing.T, client *http.Client, endpoint string, headers map[string]string) string {
	t.Helper()
	response := doContainmentRequest(t, client, endpoint, http.MethodPost, "", headers,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d", response.StatusCode)
	}
	sessionID := response.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("initialize missing session ID")
	}
	return sessionID
}

func doContainmentRequest(t *testing.T, client *http.Client, endpoint, method, sessionID string, headers map[string]string, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func eventuallyContainment(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %s", strconv.FormatInt(timeout.Milliseconds(), 10)+"ms")
}

type incompleteHTTPResult struct {
	response *http.Response
	err      error
}

type incompleteHTTPBody struct {
	started   chan struct{}
	unblock   chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
	first     atomic.Bool
}

func newIncompleteHTTPBody() *incompleteHTTPBody {
	body := &incompleteHTTPBody{
		started: make(chan struct{}),
		unblock: make(chan struct{}),
	}
	body.first.Store(true)
	return body
}

func (b *incompleteHTTPBody) Read(p []byte) (int, error) {
	if b.first.CompareAndSwap(true, false) {
		if len(p) == 0 {
			return 0, nil
		}
		p[0] = '{'
		return 1, nil
	}
	b.startOnce.Do(func() { close(b.started) })
	<-b.unblock
	return 0, io.EOF
}

func (b *incompleteHTTPBody) Close() error {
	b.closeOnce.Do(func() { close(b.unblock) })
	return nil
}

func startIncompleteHTTPPost(t *testing.T, client *http.Client, endpoint, sessionID string) (*incompleteHTTPBody, <-chan incompleteHTTPResult) {
	return startIncompleteHTTPPostWithHeaders(t, client, endpoint, sessionID, nil)
}

func startIncompleteHTTPPostWithHeaders(t *testing.T, client *http.Client, endpoint, sessionID string, headers map[string]string) (*incompleteHTTPBody, <-chan incompleteHTTPResult) {
	t.Helper()
	body := newIncompleteHTTPBody()
	request, err := http.NewRequest(http.MethodPost, endpoint, body)
	if err != nil {
		t.Fatal(err)
	}
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	result := make(chan incompleteHTTPResult, 1)
	go func() {
		response, err := client.Do(request)
		result <- incompleteHTTPResult{response: response, err: err}
	}()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("incomplete client body did not begin streaming")
	}
	return body, result
}

func waitIncompleteHTTPResult(t *testing.T, result <-chan incompleteHTTPResult) {
	t.Helper()
	select {
	case completed := <-result:
		if completed.response != nil {
			io.Copy(io.Discard, completed.response.Body)
			completed.response.Body.Close()
		}
		// Closing an in-progress client request body may surface either the
		// server's parse response or a client-side transport error. Both prove
		// the request returned; lane accounting is asserted separately.
	case <-time.After(time.Second):
		t.Fatal("incomplete HTTP request did not return")
	}
}

type closeUnblocksReader struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

type delayedReadCloser struct {
	data  []byte
	delay time.Duration
	once  sync.Once
}

func (r *delayedReadCloser) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	r.once.Do(func() { time.Sleep(r.delay) })
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func (*delayedReadCloser) Close() error { return nil }

func newCloseUnblocksReader() *closeUnblocksReader {
	return &closeUnblocksReader{started: make(chan struct{}), closed: make(chan struct{})}
}

func (r *closeUnblocksReader) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, errors.New("body closed")
}

func (r *closeUnblocksReader) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

type deadlineBlockingResponseWriter struct {
	header    http.Header
	started   chan struct{}
	startOnce sync.Once

	mu       sync.Mutex
	deadline time.Time
	wake     chan struct{}
}

type unsupportedBlockingResponseWriter struct {
	header       http.Header
	writeStarted chan struct{}
	unblock      chan struct{}
	startOnce    sync.Once
	unblockOnce  sync.Once
}

func newUnsupportedBlockingResponseWriter() *unsupportedBlockingResponseWriter {
	return &unsupportedBlockingResponseWriter{header: make(http.Header), writeStarted: make(chan struct{}), unblock: make(chan struct{})}
}

func (w *unsupportedBlockingResponseWriter) Header() http.Header { return w.header }
func (*unsupportedBlockingResponseWriter) WriteHeader(int)       {}
func (*unsupportedBlockingResponseWriter) Flush()                {}
func (w *unsupportedBlockingResponseWriter) Write([]byte) (int, error) {
	w.startOnce.Do(func() { close(w.writeStarted) })
	<-w.unblock
	return 0, context.Canceled
}

func (w *unsupportedBlockingResponseWriter) unblockWrite() {
	w.unblockOnce.Do(func() { close(w.unblock) })
}

func newDeadlineBlockingResponseWriter() *deadlineBlockingResponseWriter {
	return &deadlineBlockingResponseWriter{
		header:  make(http.Header),
		started: make(chan struct{}),
		wake:    make(chan struct{}),
	}
}

func (w *deadlineBlockingResponseWriter) Header() http.Header { return w.header }
func (*deadlineBlockingResponseWriter) WriteHeader(int)       {}
func (*deadlineBlockingResponseWriter) Flush()                {}

func (w *deadlineBlockingResponseWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	w.deadline = deadline
	close(w.wake)
	w.wake = make(chan struct{})
	w.mu.Unlock()
	return nil
}

func (w *deadlineBlockingResponseWriter) Write([]byte) (int, error) {
	w.startOnce.Do(func() { close(w.started) })
	for {
		w.mu.Lock()
		deadline := w.deadline
		wake := w.wake
		w.mu.Unlock()
		if deadline.IsZero() {
			<-wake
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, context.DeadlineExceeded
		}
		timer := time.NewTimer(remaining)
		select {
		case <-timer.C:
			return 0, context.DeadlineExceeded
		case <-wake:
			if !timer.Stop() {
				<-timer.C
			}
		}
	}
}

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	transport.SetSessionRequestContextFunc(func(_ context.Context, r *http.Request) context.Context {
		ctx := context.WithValue(r.Context(), ownerKey{}, "attempted-rewrite")
		return context.WithValue(ctx, freshKey{}, r.Header.Get("X-Fresh"))
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

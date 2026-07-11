package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"testing/iotest"
	"time"
)

func containmentHTTPConfig() HTTPServerTransportConfig {
	config := DefaultHTTPServerTransportConfig()
	config.MaxSessions = 8
	config.MaxSessionsPerPrincipal = 4
	config.MaxSessionsPerQuotaKey = 4
	config.IdleTimeout = time.Minute
	config.AbsoluteLifetime = time.Hour
	config.MaxRequestBodyBytes = 1024
	config.MaxInitializeRequestBodyBytes = 1024
	config.MaxNestedResponseBytes = 512
	config.MaxProtectedResponseBodyBytes = 768
	config.MaxRequestIDBytes = 32
	config.MaxConcurrentMessages = 8
	config.MaxConcurrentMessagesPerSession = 2
	config.OutboxMaxMessages = 8
	config.OutboxMaxBytes = 4096
	config.ResponseReservationBytes = 512
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
	if config.MaxSessions != 1024 || config.MaxSessionsPerPrincipal != 64 || config.MaxSessionsPerQuotaKey != 64 {
		t.Fatalf("session defaults = %d/%d/%d, want 1024/64/64", config.MaxSessions, config.MaxSessionsPerPrincipal, config.MaxSessionsPerQuotaKey)
	}
	if config.IdleTimeout != 30*time.Minute || config.AbsoluteLifetime != 24*time.Hour {
		t.Fatalf("lifetime defaults = %v/%v", config.IdleTimeout, config.AbsoluteLifetime)
	}
	if config.MaxRequestBodyBytes != 8<<20 {
		t.Fatalf("MaxRequestBodyBytes = %d, want %d", config.MaxRequestBodyBytes, 8<<20)
	}
	if config.MaxInitializeRequestBodyBytes != 64<<10 {
		t.Fatalf("MaxInitializeRequestBodyBytes = %d, want %d", config.MaxInitializeRequestBodyBytes, 64<<10)
	}
	if config.MaxJSONDepth != 64 || config.MaxJSONStructuralTokens != 65_536 {
		t.Fatalf("JSON complexity defaults = %d/%d, want 64/65536", config.MaxJSONDepth, config.MaxJSONStructuralTokens)
	}
	if config.MaxNestedResponseBytes != 4<<20 {
		t.Fatalf("MaxNestedResponseBytes = %d, want %d", config.MaxNestedResponseBytes, 4<<20)
	}
	if config.MaxProtectedResponseBodyBytes != (4<<20)+(64<<10) {
		t.Fatalf("MaxProtectedResponseBodyBytes = %d, want %d", config.MaxProtectedResponseBodyBytes, (4<<20)+(64<<10))
	}
	if config.MaxRequestIDBytes != 128 {
		t.Fatalf("MaxRequestIDBytes = %d, want 128", config.MaxRequestIDBytes)
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
	if config.ResponseReservationBytes != 1<<20 {
		t.Fatalf("ResponseReservationBytes = %d, want %d", config.ResponseReservationBytes, 1<<20)
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
		"quota sessions":        func(c *HTTPServerTransportConfig) { c.MaxSessionsPerQuotaKey = 0 },
		"quota over global":     func(c *HTTPServerTransportConfig) { c.MaxSessionsPerQuotaKey = c.MaxSessions + 1 },
		"idle":                  func(c *HTTPServerTransportConfig) { c.IdleTimeout = 0 },
		"absolute":              func(c *HTTPServerTransportConfig) { c.AbsoluteLifetime = 0 },
		"idle over absolute":    func(c *HTTPServerTransportConfig) { c.IdleTimeout = c.AbsoluteLifetime + 1 },
		"body":                  func(c *HTTPServerTransportConfig) { c.MaxRequestBodyBytes = 0 },
		"initialize body":       func(c *HTTPServerTransportConfig) { c.MaxInitializeRequestBodyBytes = 0 },
		"initialize over body": func(c *HTTPServerTransportConfig) {
			c.MaxInitializeRequestBodyBytes = c.MaxRequestBodyBytes + 1
		},
		"JSON depth": func(c *HTTPServerTransportConfig) { c.MaxJSONDepth = 0 },
		"JSON structural tokens": func(c *HTTPServerTransportConfig) {
			c.MaxJSONStructuralTokens = 0
		},
		"nested response": func(c *HTTPServerTransportConfig) { c.MaxNestedResponseBytes = 0 },
		"protected body": func(c *HTTPServerTransportConfig) {
			c.MaxProtectedResponseBodyBytes = c.MaxNestedResponseBytes
		},
		"protected over body": func(c *HTTPServerTransportConfig) {
			c.MaxProtectedResponseBodyBytes = c.MaxRequestBodyBytes + 1
		},
		"request ID":            func(c *HTTPServerTransportConfig) { c.MaxRequestIDBytes = 0 },
		"negative body timeout": func(c *HTTPServerTransportConfig) { c.RequestBodyTimeout = -1 },
		"messages":              func(c *HTTPServerTransportConfig) { c.MaxConcurrentMessages = 0 },
		"session messages":      func(c *HTTPServerTransportConfig) { c.MaxConcurrentMessagesPerSession = 0 },
		"session over global":   func(c *HTTPServerTransportConfig) { c.MaxConcurrentMessagesPerSession = c.MaxConcurrentMessages + 1 },
		"outbox messages":       func(c *HTTPServerTransportConfig) { c.OutboxMaxMessages = 0 },
		"outbox bytes":          func(c *HTTPServerTransportConfig) { c.OutboxMaxBytes = 0 },
		"response reservation":  func(c *HTTPServerTransportConfig) { c.ResponseReservationBytes = 0 },
		"response over outbox":  func(c *HTTPServerTransportConfig) { c.ResponseReservationBytes = c.OutboxMaxBytes + 1 },
		"ID over response":      func(c *HTTPServerTransportConfig) { c.MaxRequestIDBytes = int(c.ResponseReservationBytes) },
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

func TestJSONComplexityReaderCountsStructureAcrossChunkBoundaries(t *testing.T) {
	// Brackets and an escaped quote inside the first string are data, not
	// structure. The actual token count is: root, two keys, two values, array,
	// object, and three primitive values = 9. Maximum depth is 3.
	body := `{"literal":"[\"{not-structure}\"]","values":[{},null,true,1]}`
	read := func(maxDepth, maxTokens int) error {
		reader := newJSONComplexityReader(iotest.OneByteReader(strings.NewReader(body)), maxDepth, maxTokens)
		_, err := io.ReadAll(reader)
		return err
	}
	if err := read(3, 9); err != nil {
		t.Fatalf("exact limits rejected valid JSON: %v", err)
	}
	if err := read(2, 9); !errors.Is(err, errHTTPJSONDepthLimit) {
		t.Fatalf("depth error = %v, want %v", err, errHTTPJSONDepthLimit)
	}
	if err := read(3, 8); !errors.Is(err, errHTTPJSONTokenLimit) {
		t.Fatalf("token error = %v, want %v", err, errHTTPJSONTokenLimit)
	}
}

func TestJSONComplexityReaderAcceptsUTF8AcrossEveryByteBoundary(t *testing.T) {
	body := []byte("{\"value\":\"\\u263a raw: \u0080 \u07ff \u0800 \ud7ff \ue000 \uffff \U00010000 \U0010ffff\"}")
	reader := newJSONComplexityReader(iotest.OneByteReader(bytes.NewReader(body)), 8, 16)
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("valid split UTF-8 rejected: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("valid split UTF-8 changed: got %q want %q", got, body)
	}
	var decoded map[string]string
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("accepted UTF-8 did not remain valid JSON: %v", err)
	}
}

func TestJSONComplexityReaderRejectsEveryInvalidUTF8Class(t *testing.T) {
	tests := map[string][]byte{
		"stray continuation":   {0x80},
		"invalid lead":         {0xff},
		"overlong two byte":    {0xc0, 0xaf},
		"overlong three byte":  {0xe0, 0x80, 0x80},
		"surrogate":            {0xed, 0xa0, 0x80},
		"overlong four byte":   {0xf0, 0x80, 0x80, 0x80},
		"above unicode max":    {0xf4, 0x90, 0x80, 0x80},
		"bad continuation":     {0xe2, 0x28, 0xa1},
		"truncated two byte":   {0xc2},
		"truncated three byte": {0xe2, 0x82},
		"truncated four byte":  {0xf0, 0x90, 0x80},
	}
	for name, invalid := range tests {
		t.Run(name, func(t *testing.T) {
			body := append([]byte(`{"value":"`), invalid...)
			// Leave truncated cases at EOF. For every other case, the suffix
			// proves a later valid quote cannot make the sequence acceptable.
			if !strings.HasPrefix(name, "truncated") {
				body = append(body, []byte(`"}`)...)
			}
			reader := newJSONComplexityReader(iotest.OneByteReader(bytes.NewReader(body)), 8, 16)
			if _, err := io.ReadAll(reader); !errors.Is(err, errHTTPJSONInvalidUTF8) {
				t.Fatalf("invalid UTF-8 error = %v, want %v", err, errHTTPJSONInvalidUTF8)
			}
		})
	}
}

func TestHTTPServerTransportRejectsInvalidUTF8BeforeDispatch(t *testing.T) {
	var calls atomic.Int64
	server := NewServer()
	server.AddTool(Tool{Name: "bounded", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		calls.Add(1)
		return textToolResult("ok"), nil
	})
	config := containmentHTTPConfig()
	config.MaxRequestBodyBytes = 64 << 10
	transport := newContainmentTransport(t, server, config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)

	body := append([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"bounded","arguments":{"seed_content":"`), 0xff)
	body = append(body, []byte(`"}}}`)...)
	request, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Mcp-Session-Id", sessionID)
	response, err := srv.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid UTF-8 status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler calls after invalid UTF-8 = %d, want 0", got)
	}
	stats := transport.Stats()
	if stats.RejectedInvalidUTF8 != 1 || stats.RejectedMessages == 0 || stats.InFlightDecodes != 0 || stats.InFlightProtectedDecodes != 0 {
		t.Fatalf("invalid UTF-8 rejection accounting = %+v", stats)
	}

	valid := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"bounded","arguments":{"seed_content":"valid \u2603"}}}`
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, valid)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("valid retry status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	eventuallyContainment(t, time.Second, func() bool { return calls.Load() == 1 })
}

func TestHTTPServerTransportRejectsJSONStructuralAmplificationBeforeDispatch(t *testing.T) {
	var calls atomic.Int64
	server := NewServer()
	server.AddTool(Tool{Name: "bounded", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		calls.Add(1)
		return textToolResult("ok"), nil
	})
	config := containmentHTTPConfig()
	config.MaxRequestBodyBytes = 64 << 10
	config.MaxJSONDepth = 16
	config.MaxJSONStructuralTokens = 32
	transport := newContainmentTransport(t, server, config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)

	wide := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"bounded","arguments":{"items":[` +
		strings.Repeat(`{},`, 100) + `{}` + `]}}}`
	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, wide)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("wide status = %d, want %d", response.StatusCode, http.StatusRequestEntityTooLarge)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler calls after structural rejection = %d, want 0", got)
	}
	stats := transport.Stats()
	if stats.RejectedJSONComplexity != 1 || stats.RejectedMessages == 0 || stats.InFlightDecodes != 0 || stats.InFlightProtectedDecodes != 0 {
		t.Fatalf("structural rejection accounting = %+v", stats)
	}

	// Complexity rejection is request-scoped, not a session poison. A normal
	// retry remains usable and proves the rejected request retained no lease.
	valid := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"bounded","arguments":{}}}`
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, valid)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("valid retry status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	eventuallyContainment(t, time.Second, func() bool { return calls.Load() == 1 })
}

func TestHTTPServerTransportRejectsExcessJSONDepthBeforeDispatch(t *testing.T) {
	var calls atomic.Int64
	server := NewServer()
	server.AddTool(Tool{Name: "bounded", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		calls.Add(1)
		return textToolResult("ok"), nil
	})
	config := containmentHTTPConfig()
	config.MaxRequestBodyBytes = 64 << 10
	config.MaxJSONDepth = 8
	config.MaxJSONStructuralTokens = 1024
	transport := newContainmentTransport(t, server, config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)

	deepValue := strings.Repeat("[", 16) + "0" + strings.Repeat("]", 16)
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"bounded","arguments":{"value":` + deepValue + `}}}`
	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, body)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("deep status = %d, want %d", response.StatusCode, http.StatusRequestEntityTooLarge)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler calls after depth rejection = %d, want 0", got)
	}
	if stats := transport.Stats(); stats.RejectedJSONComplexity != 1 || stats.InFlightDecodes != 0 {
		t.Fatalf("depth rejection accounting = %+v", stats)
	}
}

func TestHTTPServerTransportInitializationIsTransactional(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxInitializeRequestBodyBytes = 128
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

func TestInitializeDropsExperimentalCapabilitiesBeforeRetention(t *testing.T) {
	server := NewServer()
	raw, err := json.Marshal(InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      ImplementationInfo{Name: "bounded-client", Version: "1.2.3"},
		Capabilities: ClientCapabilities{
			Sampling: &ClientSamplingCapability{},
			Experimental: map[string]map[string]any{
				"large-extension": {"nested": strings.Repeat("x", 32<<10)},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, rpcErr := server.handleInitialize(raw); rpcErr != nil {
		t.Fatalf("handleInitialize: %+v", rpcErr)
	}
	if experimental := server.ClientCapabilities().Experimental; experimental != nil {
		t.Fatalf("experimental capabilities retained: %#v", experimental)
	}
	info := server.ClientInfo()
	if info == nil || info.Name != "bounded-client" || info.Version != "1.2.3" {
		t.Fatalf("bounded client info not retained correctly: %#v", info)
	}
}

func TestInitializeRejectsOversizeRetainedIdentityFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params InitializeParams
	}{
		{
			name: "protocol version",
			params: InitializeParams{
				ProtocolVersion: strings.Repeat("v", maxInitializeProtocolVersionBytes+1),
			},
		},
		{
			name: "client name",
			params: InitializeParams{
				ClientInfo: ImplementationInfo{Name: strings.Repeat("n", maxInitializeClientInfoFieldBytes+1)},
			},
		},
		{
			name: "client version",
			params: InitializeParams{
				ClientInfo: ImplementationInfo{Version: strings.Repeat("v", maxInitializeClientInfoFieldBytes+1)},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.params)
			if err != nil {
				t.Fatal(err)
			}
			server := NewServer()
			if _, rpcErr := server.handleInitialize(raw); rpcErr == nil || rpcErr.Code != jsonRPCCodeInvalidParams {
				t.Fatalf("oversize initialize field error = %+v", rpcErr)
			}
			if server.ClientInfo() != nil {
				t.Fatal("rejected initialize mutated retained client state")
			}
		})
	}
}

func TestEightRetainedSessionsStayBoundedAtFullDecodeSaturation(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxSessions = 8
	config.MaxSessionsPerPrincipal = 8
	config.MaxSessionsPerQuotaKey = 8
	config.MaxConcurrentMessages = 4
	config.MaxConcurrentMessagesPerSession = 2
	config.MaxRequestBodyBytes = 64 << 10
	config.MaxInitializeRequestBodyBytes = 64 << 10
	transport := newContainmentTransport(t, NewServer(), config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)

	initializeBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"experimental":{"large":{"payload":"` + strings.Repeat("x", 32<<10) + `"}}},"clientInfo":{"name":"bounded-client","version":"1"}}}`
	sessions := make([]*httpServerSession, 0, config.MaxSessions)
	for i := 0; i < config.MaxSessions; i++ {
		response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, "", nil, initializeBody)
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("initialize %d status = %d", i, response.StatusCode)
		}
		session, ok := transport.getSession(response.Header.Get("Mcp-Session-Id"))
		if !ok {
			t.Fatalf("initialize %d did not retain session", i)
		}
		if session.server.ClientCapabilities().Experimental != nil {
			t.Fatalf("initialize %d retained experimental graph", i)
		}
		sessions = append(sessions, session)
	}

	regular := make([]*httpDecodeLease, 0, config.MaxConcurrentMessages)
	protected := make([]*httpDecodeLease, 0, config.MaxConcurrentMessages)
	for i := 0; i < config.MaxConcurrentMessages; i++ {
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		lease, err := transport.acquireDecode(sessions[i], "", httptest.NewRecorder(), request)
		if err != nil || lease.protected {
			t.Fatalf("regular decode %d = protected:%v err:%v", i, lease != nil && lease.protected, err)
		}
		regular = append(regular, lease)
		sessions[i].server.mu.Lock()
		sessions[i].server.pending[1] = make(chan *jsonRPCMessage, 1)
		sessions[i].server.mu.Unlock()
	}
	for i := 0; i < config.MaxConcurrentMessages; i++ {
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		lease, err := transport.acquireDecode(sessions[i], "", httptest.NewRecorder(), request)
		if err != nil || !lease.protected {
			t.Fatalf("protected decode %d = protected:%v err:%v", i, lease != nil && lease.protected, err)
		}
		protected = append(protected, lease)
	}
	stats := transport.Stats()
	if stats.ActiveSessions != 8 || stats.InFlightDecodes != 4 || stats.InFlightProtectedDecodes != 4 {
		t.Fatalf("combined retained/decode saturation = %+v", stats)
	}
	for _, lease := range protected {
		lease.release()
	}
	for _, lease := range regular {
		lease.release()
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
	config.MaxSessionsPerQuotaKey = 2
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

func TestHTTPServerTransportAggregateQuotaDoesNotMergeAuthorizationPrincipals(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxSessions = 4
	config.MaxSessionsPerPrincipal = 2
	config.MaxSessionsPerQuotaKey = 2
	transport := newContainmentTransport(t, NewServer(), config)
	transport.SetSessionPrincipalFunc(func(r *http.Request) (string, error) {
		return r.Header.Get("X-Principal"), nil
	})
	transport.SetSessionQuotaKeyFunc(func(r *http.Request) (string, error) {
		return r.Header.Get("X-Quota-Key"), nil
	})

	request := func(principal, quota string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("X-Principal", principal)
		r.Header.Set("X-Quota-Key", quota)
		return r
	}
	first, err := transport.newSession(request("token-a", "workspace-a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := transport.newSession(request("token-b", "workspace-a"))
	if err != nil {
		t.Fatal(err)
	}
	if first.principal == second.principal || first.quotaKey != second.quotaKey {
		t.Fatalf("authorization/quota identities collapsed: first=%q/%q second=%q/%q", first.principal, first.quotaKey, second.principal, second.quotaKey)
	}
	if _, err := transport.newSession(request("token-c", "workspace-a")); !errors.Is(err, errHTTPQuotaKeyLimit) {
		t.Fatalf("third workspace-a session error = %v, want aggregate quota", err)
	}
	other, err := transport.newSession(request("token-d", "workspace-b"))
	if err != nil {
		t.Fatalf("workspace-a exhausted workspace-b capacity: %v", err)
	}
	first.close()
	second.close()
	other.close()
	if len(transport.quotaSessions) != 0 {
		t.Fatalf("closed sessions retained quota indexes: %#v", transport.quotaSessions)
	}
}

func TestHTTPServerTransportGlobalSessionQuotaIncludesProvisionalSessions(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxSessions = 1
	config.MaxSessionsPerPrincipal = 1
	config.MaxSessionsPerQuotaKey = 1
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
	responseIDs := len(session.responseIDs)
	activityAfter := session.lastActivity
	session.mu.Unlock()
	if stats.InFlightMessages != 0 || stats.InFlightResponseReservations != 0 || stats.ReservedResponseBytes != 0 ||
		decodeInFlight != 0 || controlInFlight != 0 || operations != 0 || messages != 0 || controls != 0 || sseOpen || responseIDs != 0 {
		t.Fatalf("hook failures leaked counters: stats=%+v decode=%d control=%d ops=%d messages=%d controls=%d sse=%v response_ids=%d",
			stats, decodeInFlight, controlInFlight, operations, messages, controls, sseOpen, responseIDs)
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

func TestHTTPServerTransportRejectsOversizedNestedResponseAndReleasesCaller(t *testing.T) {
	finished := make(chan error, 1)
	server := NewServer()
	server.AddTool(Tool{Name: "sample", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(ctx context.Context, rc *RequestContext, _ map[string]any) (*ToolResult, error) {
		_, err := rc.CreateMessage(ctx, &CreateMessageParams{
			Messages:  []SamplingMessage{{Role: "user", Content: MarshalSamplingContent(Content{Type: "text", Text: "hello"})}},
			MaxTokens: 1,
		})
		finished <- err
		return nil, err
	})
	config := containmentHTTPConfig()
	config.MaxNestedResponseBytes = 128
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

	toolResponse := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sample","arguments":{}}}`)
	toolResponse.Body.Close()
	if toolResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("tool status = %d", toolResponse.StatusCode)
	}
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

	oversized := `{"jsonrpc":"2.0","id":` + strconv.FormatInt(pendingID, 10) + `,"result":{"role":"assistant","content":{"type":"text","text":"` + strings.Repeat("x", 256) + `"},"model":"test","stopReason":"endTurn"}}`
	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, oversized)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized nested response status = %d, want %d", response.StatusCode, http.StatusRequestEntityTooLarge)
	}
	select {
	case err := <-finished:
		if err == nil || !strings.Contains(err.Error(), "nested response exceeds configured byte limit") {
			t.Fatalf("nested caller error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("oversized nested response stranded its server-side caller")
	}
	eventuallyContainment(t, time.Second, func() bool {
		stats := transport.Stats()
		return stats.RejectedNestedResponses == 1 && stats.InFlightMessages == 0 && stats.InFlightControlResponses == 0
	})

	// The malformed response consumes only its matched pending call, not the
	// session. Ordinary traffic remains usable.
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("valid retry status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
}

func TestHTTPServerTransportProtectedBodyCeilingCancelsPendingSession(t *testing.T) {
	config := containmentHTTPConfig()
	config.MaxNestedResponseBytes = 64
	config.MaxProtectedResponseBodyBytes = 128
	transport := newContainmentTransport(t, NewServer(), config)
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(session) {
		t.Fatalf("activate session: %v", err)
	}
	pending := make(chan *jsonRPCMessage, 1)
	session.server.mu.Lock()
	session.server.pending[1] = pending
	session.server.mu.Unlock()
	// Force this matched response through the protected reserve rather than an
	// available ordinary decode lane.
	transport.mu.Lock()
	transport.decodeInFlight = config.MaxConcurrentMessages
	transport.mu.Unlock()

	body := `{"jsonrpc":"2.0","id":1,"result":{"padding":"` + strings.Repeat("x", 256) + `"}}`
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	request.Header.Set("Mcp-Session-Id", session.id)
	recorder := httptest.NewRecorder()
	transport.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("protected oversize status = %d, want 413", recorder.Code)
	}
	select {
	case _, ok := <-pending:
		if ok {
			t.Fatal("oversize protected body delivered a response")
		}
	case <-time.After(time.Second):
		t.Fatal("oversize protected body stranded pending caller")
	}
	if _, ok := transport.getSession(session.id); ok {
		t.Fatal("uncorrelatable oversize protected body retained session")
	}
	transport.mu.Lock()
	transport.decodeInFlight = 0
	transport.mu.Unlock()
	if stats := transport.Stats(); stats.RejectedNestedResponses != 1 || stats.InFlightProtectedDecodes != 0 {
		t.Fatalf("protected body rejection stats = %+v", stats)
	}
}

func TestHTTPServerTransportProtectedDecodeCancellationCancelsPendingSession(t *testing.T) {
	config := containmentHTTPConfig()
	transport := newContainmentTransport(t, NewServer(), config)
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(session) {
		t.Fatalf("activate session: %v", err)
	}
	pending := make(chan *jsonRPCMessage, 1)
	session.server.mu.Lock()
	session.server.pending[1] = pending
	session.server.mu.Unlock()
	// Occupy the ordinary lanes so the pending response receives the protected
	// reserve, then cancel in the post-decode admission window represented by
	// releaseDecodedMessage.
	transport.mu.Lock()
	transport.decodeInFlight = config.MaxConcurrentMessages
	transport.mu.Unlock()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	lease, err := transport.acquireDecode(session, "", httptest.NewRecorder(), request)
	if err != nil || !lease.protected {
		t.Fatalf("protected decode lease = protected:%v err:%v", lease != nil && lease.protected, err)
	}
	lease.cancel()
	if err := transport.releaseDecodedMessage(session, lease); !errors.Is(err, context.Canceled) {
		t.Fatalf("release decoded message error = %v, want context cancellation", err)
	}
	select {
	case _, ok := <-pending:
		if ok {
			t.Fatal("canceled protected decode delivered a response")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled protected decode stranded pending caller")
	}
	if _, ok := transport.getSession(session.id); ok {
		t.Fatal("canceled protected decode retained session")
	}
	transport.mu.Lock()
	transport.decodeInFlight = 0
	transport.mu.Unlock()
	if stats := transport.Stats(); stats.RejectedNestedResponses != 1 || stats.InFlightProtectedDecodes != 0 {
		t.Fatalf("protected cancellation stats = %+v", stats)
	}
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
	leaseA1, err := transport.acquireSessionLease(sessionA, request, nil, leaseControl, "")
	if err != nil {
		t.Fatal(err)
	}
	leaseA2, err := transport.acquireSessionLease(sessionA, request, nil, leaseControl, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.acquireSessionLease(sessionA, request, nil, leaseControl, ""); !errors.Is(err, errHTTPMessageLimit) {
		t.Fatalf("per-session control limit = %v", err)
	}
	leaseB, err := transport.acquireSessionLease(sessionB, request, nil, leaseControl, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.acquireSessionLease(sessionB, request, nil, leaseControl, ""); !errors.Is(err, errHTTPMessageLimit) {
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

func TestHTTPServerTransportFailedSSEWriteReplaysCommittedResponseOnReconnect(t *testing.T) {
	server := NewServer()
	server.AddTool(Tool{Name: "create", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		return textToolResult("owner-token"), nil
	})
	transport := newContainmentTransport(t, server, containmentHTTPConfig())
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)
	call := `{"jsonrpc":"2.0","id":"create-1","method":"tools/call","params":{"name":"create","arguments":{}}}`
	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, call)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("tool status = %d, want 202", response.StatusCode)
	}
	session, ok := transport.getSession(sessionID)
	if !ok {
		t.Fatal("initialized session missing")
	}
	eventuallyContainment(t, time.Second, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		_, retained := session.responseIDs["s:create-1"]
		return len(session.outbox) == 1 && retained && session.inFlightMessages == 0
	})

	failed := newScriptedSSEResponseWriter(true)
	failedRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	failedRequest.Header.Set("Mcp-Session-Id", sessionID)
	transport.ServeHTTP(failed, failedRequest)
	if _, _, ok := session.peekOutbox(); !ok {
		t.Fatal("failed SSE write removed committed response")
	}
	session.mu.Lock()
	_, retained := session.responseIDs["s:create-1"]
	session.mu.Unlock()
	if !retained {
		t.Fatal("failed SSE write released response ID before delivery")
	}
	if _, ok := transport.getSession(sessionID); !ok {
		t.Fatal("failed SSE write detached reusable session")
	}

	replayed := newScriptedSSEResponseWriter(false)
	replayCtx, cancelReplay := context.WithCancel(context.Background())
	replayRequest := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(replayCtx)
	replayRequest.Header.Set("Mcp-Session-Id", sessionID)
	done := make(chan struct{})
	go func() {
		transport.ServeHTTP(replayed, replayRequest)
		close(done)
	}()
	select {
	case <-replayed.wrote:
	case <-time.After(time.Second):
		t.Fatal("reconnected SSE did not replay response")
	}
	eventuallyContainment(t, time.Second, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return len(session.outbox) == 0
	})
	cancelReplay()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconnected SSE did not release after cancellation")
	}
	if got := replayed.String(); !strings.Contains(got, "owner-token") {
		t.Fatalf("replayed SSE payload = %q", got)
	}
	if _, _, ok := session.peekOutbox(); ok {
		t.Fatal("successful replay did not acknowledge outbox entry")
	}
	session.mu.Lock()
	_, retained = session.responseIDs["s:create-1"]
	session.mu.Unlock()
	if retained {
		t.Fatal("successful replay retained delivered response ID")
	}
}

func TestHTTPServerTransportOutboxIsFIFOAndOverflowRejectsWithoutDiscardingSession(t *testing.T) {
	config := containmentHTTPConfig()
	config.OutboxMaxMessages = 3
	config.OutboxMaxBytes = 512
	config.ResponseReservationBytes = 512
	transport := newContainmentTransport(t, NewServer(), config)
	session, err := transport.newSession(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil || !transport.activateSession(session) {
		t.Fatalf("activate session: %v", err)
	}
	for _, payload := range [][]byte{[]byte("a"), []byte("bb"), bytes.Repeat([]byte{'c'}, 509)} {
		if err := transport.enqueue(session, payload); err != nil {
			t.Fatalf("enqueue %q: %v", payload, err)
		}
	}
	for _, want := range []string{"a", "bb", strings.Repeat("c", 509)} {
		got, ok := session.dequeue()
		if !ok || string(got) != want {
			t.Fatalf("dequeue = %q/%v, want %q", got, ok, want)
		}
	}
	if stats := transport.Stats(); stats.ActiveSessions != 1 || stats.OutboxOverflowClosures != 0 {
		t.Fatalf("FIFO accounting = %+v", stats)
	}

	if err := transport.enqueue(session, bytes.Repeat([]byte{'x'}, 513)); !errors.Is(err, errHTTPOutboxOverflow) {
		t.Fatalf("oversize enqueue error = %v", err)
	}
	stats := transport.Stats()
	if stats.ActiveSessions != 1 || stats.RejectedOutboxWrites != 1 || stats.OutboxOverflowClosures != 0 {
		t.Fatalf("overflow accounting = %+v", stats)
	}
	select {
	case <-session.ctx.Done():
		t.Fatal("overflow rejection canceled reusable session")
	default:
	}
}

func TestHTTPServerTransportOutboxMessageCountOverflowPreservesQueuedResponse(t *testing.T) {
	config := containmentHTTPConfig()
	config.OutboxMaxMessages = 1
	config.OutboxMaxBytes = 1024
	config.ResponseReservationBytes = 512
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
	if stats := transport.Stats(); stats.ActiveSessions != 1 || stats.RejectedOutboxWrites != 1 || stats.OutboxOverflowClosures != 0 {
		t.Fatalf("message overflow accounting = %+v", stats)
	}
	if payload, ok := session.dequeue(); !ok || string(payload) != "first" {
		t.Fatalf("preserved queued payload = %q/%v", payload, ok)
	}
	if err := transport.enqueue(session, []byte("second")); err != nil {
		t.Fatalf("session not reusable after draining: %v", err)
	}
}

func TestHTTPServerTransportReservesResponseBeforeStateChangingDispatch(t *testing.T) {
	var mutations atomic.Int64
	server := NewServer()
	server.AddTool(Tool{Name: "create", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		mutations.Add(1)
		return textToolResult("owner-token"), nil
	})
	config := containmentHTTPConfig()
	config.OutboxMaxMessages = 4
	config.OutboxMaxBytes = 1024
	config.ResponseReservationBytes = 512
	transport := newContainmentTransport(t, server, config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)

	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)
	session, ok := transport.getSession(sessionID)
	if !ok {
		t.Fatal("initialized session missing")
	}
	// This stale entry fits physically, but leaves less than the configured
	// response reservation. The request must be rejected before its handler can
	// create owner-protected state.
	if err := transport.enqueue(session, bytes.Repeat([]byte{'x'}, 600)); err != nil {
		t.Fatal(err)
	}
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create","arguments":{}}}`
	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, call)
	response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("reserved-capacity rejection = %d, want 429", response.StatusCode)
	}
	if got := mutations.Load(); got != 0 {
		t.Fatalf("handler mutations = %d, want 0", got)
	}
	stats := transport.Stats()
	if stats.RejectedResponseReservations != 1 || stats.InFlightResponseReservations != 0 || stats.ReservedResponseBytes != 0 {
		t.Fatalf("reservation rejection accounting = %+v", stats)
	}
	session.mu.Lock()
	active := session.state == httpSessionActive && !session.closed
	session.mu.Unlock()
	if !active {
		t.Fatal("reservation rejection closed the reusable session")
	}
	if _, ok := session.dequeue(); !ok {
		t.Fatal("stale outbox entry disappeared")
	}

	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, call)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("admitted call status = %d, want 202", response.StatusCode)
	}
	eventuallyContainment(t, time.Second, func() bool {
		return mutations.Load() == 1 && transport.Stats().InFlightResponseReservations == 0
	})
	if err := transport.enqueue(session, bytes.Repeat([]byte{'z'}, 1025)); !errors.Is(err, errHTTPOutboxOverflow) {
		t.Fatalf("post-response overflow = %v, want bounded rejection", err)
	}
	payload, ok := session.dequeue()
	if !ok || !bytes.Contains(payload, []byte("owner-token")) {
		t.Fatalf("reserved response = %q/%v", payload, ok)
	}
}

func TestHTTPServerTransportRejectsOversizeRequestIDBeforeDispatch(t *testing.T) {
	var calls atomic.Int64
	server := NewServer()
	server.AddTool(Tool{Name: "create", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		calls.Add(1)
		return textToolResult("owner-token"), nil
	})
	config := containmentHTTPConfig()
	transport := newContainmentTransport(t, server, config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)

	body := `{"jsonrpc":"2.0","id":"` + strings.Repeat("x", config.MaxRequestIDBytes) + `","method":"tools/call","params":{"name":"create","arguments":{}}}`
	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, body)
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize ID status = %d, want 413", response.StatusCode)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler calls = %d, want 0", got)
	}
	stats := transport.Stats()
	if stats.RejectedOversizeRequestIDs != 1 || stats.InFlightMessages != 0 || stats.InFlightResponseReservations != 0 || stats.ActiveSessions != 1 {
		t.Fatalf("oversize ID accounting = %+v", stats)
	}

	body = `{"jsonrpc":"2.0","id":{},"method":"tools/call","params":{"name":"create","arguments":{}}}`
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid ID type status = %d, want 400", response.StatusCode)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler calls after invalid ID = %d, want 0", got)
	}
	if stats = transport.Stats(); stats.RejectedInvalidRequestIDs != 1 || stats.ActiveSessions != 1 {
		t.Fatalf("invalid ID accounting = %+v", stats)
	}
	for _, invalidID := range []string{"99999999999999999999", "1.5", "1e3"} {
		body = `{"jsonrpc":"2.0","id":` + invalidID + `,"method":"tools/call","params":{"name":"create","arguments":{}}}`
		response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, body)
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("unsupported numeric ID %q status = %d, want 400", invalidID, response.StatusCode)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler calls after unsupported numeric IDs = %d, want 0", got)
	}
	if stats = transport.Stats(); stats.RejectedInvalidRequestIDs != 4 {
		t.Fatalf("unsupported numeric ID accounting = %+v", stats)
	}

	const maxIntID = "9223372036854775807"
	body = `{"jsonrpc":"2.0","id":` + maxIntID + `,"method":"tools/call","params":{"name":"create","arguments":{}}}`
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, body)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("exact integer ID status = %d, want 202", response.StatusCode)
	}
	session, ok := transport.getSession(sessionID)
	if !ok {
		t.Fatal("session disappeared after exact integer ID")
	}
	eventuallyContainment(t, time.Second, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return calls.Load() == 1 && len(session.outbox) == 1
	})
	payload, ok := session.dequeue()
	if !ok || !bytes.Contains(payload, []byte(`"id":`+maxIntID)) {
		t.Fatalf("exact correlated response = %q/%v", payload, ok)
	}

	body = `{"jsonrpc":"2.0","id":"\ud800","method":"tools/call","params":{"name":"create","arguments":{}}}`
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, body)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("surrogate ID status = %d, want 202", response.StatusCode)
	}
	eventuallyContainment(t, time.Second, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return calls.Load() == 2 && len(session.outbox) == 1
	})
	payload, ok = session.dequeue()
	if !ok || !bytes.Contains(payload, []byte(`"id":"\ud800"`)) {
		t.Fatalf("surrogate correlated response = %q/%v", payload, ok)
	}
}

func TestHTTPServerTransportRejectsOversizeInitializeIDBeforeSessionCreation(t *testing.T) {
	config := containmentHTTPConfig()
	transport := newContainmentTransport(t, NewServer(), config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	body := `{"jsonrpc":"2.0","id":"` + strings.Repeat("x", config.MaxRequestIDBytes) + `","method":"initialize","params":{}}`
	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, "", nil, body)
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize initialize ID status = %d, want 413", response.StatusCode)
	}
	stats := transport.Stats()
	if stats.ActiveSessions != 0 || stats.ProvisionalSessions != 0 || stats.RejectedOversizeRequestIDs != 1 {
		t.Fatalf("oversize initialize accounting = %+v", stats)
	}

	body = `{"jsonrpc":"2.0","id":"\ud800","method":"initialize","params":{}}`
	response = doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, "", nil, body)
	payload, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(payload, []byte(`"id":"\ud800"`)) {
		t.Fatalf("surrogate initialize response = status %d payload %q", response.StatusCode, payload)
	}
}

func TestHTTPServerTransportConcurrentRequestCannotStealReservedResponse(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int64
	server := NewServer()
	server.AddTool(Tool{Name: "create", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return textToolResult("owner-token"), nil
	})
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = 3
	config.MaxConcurrentMessagesPerSession = 3
	config.OutboxMaxMessages = 4
	config.OutboxMaxBytes = 1536
	config.ResponseReservationBytes = 1024
	transport := newContainmentTransport(t, server, config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)

	call := func(id int) int {
		body := `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"tools/call","params":{"name":"create","arguments":{}}}`
		response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, body)
		defer response.Body.Close()
		return response.StatusCode
	}
	if status := call(2); status != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", status)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first handler did not start")
	}
	if status := call(3); status != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", status)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
	close(release)
	eventuallyContainment(t, time.Second, func() bool {
		stats := transport.Stats()
		return stats.InFlightMessages == 0 && stats.InFlightResponseReservations == 0
	})
	session, ok := transport.getSession(sessionID)
	if !ok {
		t.Fatal("reservation pressure closed session")
	}
	payload, ok := session.dequeue()
	if !ok || !bytes.Contains(payload, []byte("owner-token")) {
		t.Fatalf("first reserved response = %q/%v", payload, ok)
	}
}

func TestHTTPServerTransportRejectsDuplicateIDUntilResponseDelivered(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int64
	server := NewServer()
	server.AddTool(Tool{Name: "create", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return textToolResult("owner-token"), nil
	})
	config := containmentHTTPConfig()
	config.MaxConcurrentMessages = 3
	config.MaxConcurrentMessagesPerSession = 3
	transport := newContainmentTransport(t, server, config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)
	call := `{"jsonrpc":"2.0","id":"same","method":"tools/call","params":{"name":"create","arguments":{}}}`

	request := func() int {
		response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, call)
		defer response.Body.Close()
		return response.StatusCode
	}
	if status := request(); status != http.StatusAccepted {
		t.Fatalf("first request status = %d, want 202", status)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first handler did not start")
	}
	if status := request(); status != http.StatusConflict {
		t.Fatalf("in-flight duplicate status = %d, want 409", status)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls after in-flight duplicate = %d, want 1", got)
	}
	close(release)
	session, ok := transport.getSession(sessionID)
	if !ok {
		t.Fatal("session disappeared")
	}
	eventuallyContainment(t, time.Second, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return len(session.outbox) == 1 && session.inFlightMessages == 0
	})
	if status := request(); status != http.StatusConflict {
		t.Fatalf("queued-response duplicate status = %d, want 409", status)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls before delivery = %d, want 1", got)
	}
	if payload, ok := session.dequeue(); !ok || !bytes.Contains(payload, []byte("owner-token")) {
		t.Fatalf("first response = %q/%v", payload, ok)
	}
	if status := request(); status != http.StatusAccepted {
		t.Fatalf("ID reuse after delivery status = %d, want 202", status)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("reused ID handler did not start")
	}
	eventuallyContainment(t, time.Second, func() bool { return calls.Load() == 2 })
	if stats := transport.Stats(); stats.RejectedDuplicateRequestIDs != 2 || stats.ActiveSessions != 1 {
		t.Fatalf("duplicate ID accounting = %+v", stats)
	}
}

func TestHTTPServerTransportOversizeNestedWriteCannotDestroyReservedResponse(t *testing.T) {
	var mutations atomic.Int64
	server := NewServer()
	server.AddTool(Tool{Name: "create", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(ctx context.Context, rc *RequestContext, _ map[string]any) (*ToolResult, error) {
		mutations.Add(1)
		_, err := rc.CreateMessage(ctx, &CreateMessageParams{
			Messages: []SamplingMessage{{
				Role:    "user",
				Content: MarshalSamplingContent(Content{Type: "text", Text: strings.Repeat("x", 2048)}),
			}},
			MaxTokens: 1,
		})
		if !errors.Is(err, errHTTPOutboxOverflow) {
			return nil, fmt.Errorf("unexpected nested write result: %w", err)
		}
		return textToolResult("owner-token"), nil
	})
	config := containmentHTTPConfig()
	config.OutboxMaxMessages = 4
	config.OutboxMaxBytes = 1024
	config.ResponseReservationBytes = 512
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

	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create","arguments":{}}}`
	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, call)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("tool status = %d, want 202", response.StatusCode)
	}
	eventuallyContainment(t, time.Second, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return mutations.Load() == 1 && len(session.outbox) == 1 && session.inFlightMessages == 0
	})
	payload, ok := session.dequeue()
	if !ok || !bytes.Contains(payload, []byte("owner-token")) {
		t.Fatalf("reserved response after nested overflow = %q/%v", payload, ok)
	}
	stats := transport.Stats()
	if stats.ActiveSessions != 1 || stats.RejectedOutboxWrites != 1 || stats.OutboxOverflowClosures != 0 {
		t.Fatalf("nested overflow accounting = %+v", stats)
	}
}

func TestHTTPServerTransportOversizeResponseUsesReservationForProtocolError(t *testing.T) {
	server := NewServer()
	server.AddTool(Tool{Name: "large", InputSchema: mustRawJSON([]byte(`{"type":"object"}`))}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		return textToolResult(strings.Repeat("x", 1024)), nil
	})
	config := containmentHTTPConfig()
	config.OutboxMaxBytes = 1024
	config.ResponseReservationBytes = 512
	transport := newContainmentTransport(t, server, config)
	srv := httptest.NewServer(transport)
	t.Cleanup(srv.Close)
	sessionID := initializeContainmentSession(t, srv.Client(), srv.URL, nil)
	session, ok := transport.getSession(sessionID)
	if !ok {
		t.Fatal("initialized session missing")
	}

	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"large","arguments":{}}}`
	response := doContainmentRequest(t, srv.Client(), srv.URL, http.MethodPost, sessionID, nil, call)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("tool status = %d, want 202", response.StatusCode)
	}
	eventuallyContainment(t, time.Second, func() bool {
		return transport.Stats().InFlightResponseReservations == 0
	})
	payload, ok := session.dequeue()
	if !ok {
		t.Fatal("oversize response did not emit bounded protocol error")
	}
	var message jsonRPCMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	if message.Error == nil || message.Error.Code != -32002 || bytes.Contains(payload, []byte(strings.Repeat("x", 64))) {
		t.Fatalf("oversize fallback response = %s", payload)
	}
	stats := transport.Stats()
	if stats.OversizeResponses != 1 || stats.OutboxOverflowClosures != 0 || stats.ActiveSessions != 1 {
		t.Fatalf("oversize response accounting = %+v", stats)
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
	lease, err := transport.acquireSessionLease(session, request, nil, leaseSSE, "")
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
	config.MaxSessionsPerQuotaKey = 4
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
		name       string
		close      func(*HTTPServerTransport, *httpServerSession)
		wantClosed bool
	}{
		{name: "principal revocation", close: func(transport *HTTPServerTransport, session *httpServerSession) {
			if got := transport.ClosePrincipal(session.principal); got != 1 {
				t.Fatalf("ClosePrincipal = %d, want 1", got)
			}
		}, wantClosed: true},
		{name: "absolute expiry", close: func(transport *HTTPServerTransport, session *httpServerSession) {
			transport.sweepOnce(session.createdAt.Add(transport.config.AbsoluteLifetime))
		}, wantClosed: true},
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
			if tc.wantClosed {
				select {
				case <-session.ctx.Done():
				case <-time.After(time.Second):
					t.Fatal("revoked/expired SSE session was not canceled")
				}
			} else {
				select {
				case <-session.ctx.Done():
					t.Fatal("transient SSE write failure canceled reusable session")
				default:
				}
				if _, ok := transport.getSession(session.id); !ok {
					t.Fatal("transient SSE write failure detached session")
				}
				if _, _, ok := session.peekOutbox(); !ok {
					t.Fatal("transient SSE write failure discarded queued payload")
				}
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
		config.MaxSessionsPerQuotaKey = 1
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
		config.MaxSessionsPerQuotaKey = 3
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

type scriptedSSEResponseWriter struct {
	header    http.Header
	failOnce  atomic.Bool
	wrote     chan struct{}
	wroteOnce sync.Once
	mu        sync.Mutex
	buf       bytes.Buffer
}

func newScriptedSSEResponseWriter(failOnce bool) *scriptedSSEResponseWriter {
	w := &scriptedSSEResponseWriter{header: make(http.Header), wrote: make(chan struct{})}
	w.failOnce.Store(failOnce)
	return w
}

func (w *scriptedSSEResponseWriter) Header() http.Header            { return w.header }
func (*scriptedSSEResponseWriter) WriteHeader(int)                  {}
func (*scriptedSSEResponseWriter) Flush()                           {}
func (*scriptedSSEResponseWriter) SetWriteDeadline(time.Time) error { return nil }

func (w *scriptedSSEResponseWriter) Write(p []byte) (int, error) {
	if w.failOnce.CompareAndSwap(true, false) {
		return 0, errors.New("injected SSE write failure")
	}
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	w.wroteOnce.Do(func() { close(w.wrote) })
	return n, err
}

func (w *scriptedSSEResponseWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
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

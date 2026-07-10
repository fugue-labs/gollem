package mcp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	httpSessionIDBytes          = 32
	httpSessionCollisionRetries = 8
)

var (
	errHTTPTransportClosed = errors.New("mcp: HTTP server transport is closed")
	errHTTPSessionLimit    = errors.New("mcp: HTTP session capacity exhausted")
	errHTTPPrincipalLimit  = errors.New("mcp: HTTP principal session capacity exhausted")
	errHTTPSessionInactive = errors.New("mcp: HTTP session is not active")
	errHTTPMessageLimit    = errors.New("mcp: HTTP message concurrency exhausted")
	errHTTPSSEAlreadyOpen  = errors.New("mcp: HTTP session already has an SSE stream")
	errHTTPOutboxOverflow  = errors.New("mcp: HTTP session outbox capacity exhausted")
)

// HTTPSessionAuthorizer authorizes a follow-up HTTP request against the
// identity captured in a session's context when it was initialized. Hosts
// should derive the request identity afresh (for example, from a bearer token
// validated by middleware) and compare it with the session owner carried by
// sessionCtx. Returning false rejects the request with HTTP 403. Returning an
// error rejects it with HTTP 500 without exposing the error to the caller.
//
// The authorizer runs before a follow-up GET can drain the session outbox,
// before a POST body is decoded or dispatched, and before DELETE removes the
// session.
type HTTPSessionAuthorizer func(sessionCtx context.Context, r *http.Request) (bool, error)

// HTTPSessionPrincipalFunc extracts a stable, non-secret principal identifier
// from a freshly authenticated request. The value is captured immutably at
// initialization, compared on every follow-up request, and used only for
// quotas and revocation indexing. A token hash is suitable; plaintext bearer
// credentials are not.
type HTTPSessionPrincipalFunc func(r *http.Request) (string, error)

// HTTPSessionPrincipalValidator reports whether a previously captured
// principal remains valid. It is called on follow-up requests and by the
// expiration sweeper. Sweep cadence is IdleTimeout/2, clamped to 10ms..1m.
// Returning an error is fail-closed for the request but retains the session so
// a temporary identity-store outage cannot mass-revoke clients. The callback
// must honor ctx cancellation.
type HTTPSessionPrincipalValidator func(ctx context.Context, principal string) (bool, error)

// HTTPSessionRequestContextFunc derives fresh request-scoped values for a
// follow-up operation. The transport always preserves the initializing
// session context with value precedence and independently binds cancellation
// to the current HTTP request and session lifetime.
type HTTPSessionRequestContextFunc func(sessionCtx context.Context, r *http.Request) context.Context

// HTTPServerTransport serves MCP over the streamable HTTP transport. Each MCP
// session gets its own cloned Server instance and bounded resource accounting.
type HTTPServerTransport struct {
	mu sync.Mutex

	template *Server
	config   HTTPServerTransportConfig
	sessions map[string]*httpServerSession
	// principalSessions includes both provisional and active sessions so a
	// burst of slow initializations cannot bypass per-principal admission.
	principalSessions map[string]map[string]*httpServerSession

	sessionCtx         func(*http.Request) context.Context
	sessionAuth        HTTPSessionAuthorizer
	principalFunc      HTTPSessionPrincipalFunc
	principalValidator HTTPSessionPrincipalValidator
	requestCtx         HTTPSessionRequestContextFunc

	sessionIDEntropy io.Reader
	entropyMu        sync.Mutex

	closed      bool
	sweepStop   chan struct{}
	sweepDone   chan struct{}
	closeDone   chan struct{}
	sweepCtx    context.Context
	sweepCancel context.CancelFunc

	stats HTTPServerTransportStats

	// Decode and response-control work are separately bounded from ordinary
	// asynchronous handlers. This prevents four handlers blocked on nested MCP
	// sampling from starving the responses required to release those handlers.
	decodeInFlight  int
	controlInFlight int
}

// SetSessionContextFunc installs a hook that derives each new MCP session's
// immutable base context from the HTTP request that initialized it. Request
// cancellation and deadlines are stripped: sessions outlive initialization.
// Values survive for the session lifetime and take precedence over values from
// SetSessionRequestContextFunc.
//
// A nil hook (the default) and a nil return both mean context.Background().
func (t *HTTPServerTransport) SetSessionContextFunc(f func(*http.Request) context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionCtx = f
}

// SetSessionAuthorizer installs an optional authorization hook applied to
// every follow-up GET, POST, and DELETE request. Multi-principal hosts should
// normally install both this hook and SetSessionPrincipalFunc.
func (t *HTTPServerTransport) SetSessionAuthorizer(f HTTPSessionAuthorizer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionAuth = f
}

// SetSessionPrincipalFunc installs the stable principal extractor used for
// automatic session binding, quotas, and ClosePrincipal. Configure it before
// accepting requests; ownership of existing sessions is never rewritten.
func (t *HTTPServerTransport) SetSessionPrincipalFunc(f HTTPSessionPrincipalFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.principalFunc = f
}

// SetSessionPrincipalValidator installs a current-state validator used on
// follow-up requests and during each expiration sweep.
func (t *HTTPServerTransport) SetSessionPrincipalValidator(f HTTPSessionPrincipalValidator) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.principalValidator = f
}

// SetSessionRequestContextFunc installs the fresh per-request context hook.
// The returned context may carry current policy/model/request values; session
// values captured at initialize remain immutable and take precedence.
func (t *HTTPServerTransport) SetSessionRequestContextFunc(f HTTPSessionRequestContextFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requestCtx = f
}

type httpSessionState uint8

const (
	httpSessionProvisional httpSessionState = iota
	httpSessionActive
	httpSessionClosed
)

type httpServerSession struct {
	mu sync.Mutex

	id             string
	principal      string
	principalBound bool
	transport      *HTTPServerTransport
	server         *Server
	ctx            context.Context
	cancel         context.CancelFunc
	createdAt      time.Time
	lastActivity   time.Time
	state          httpSessionState
	closed         bool // retained for diagnostics and compatibility with package tests

	operations       int
	inFlightMessages int
	controlInFlight  int
	sseOpen          bool

	outbox      [][]byte
	outboxBytes int64
	outboxWake  chan struct{}
}

// NewHTTPServerTransport binds a reusable Server template using bounded
// production defaults. It retains the historical no-error constructor while
// removing the historical unlimited behavior. Call Close during host shutdown
// to stop expiry/revocation sweeps and cancel sessions.
func NewHTTPServerTransport(server *Server) *HTTPServerTransport {
	transport, err := NewHTTPServerTransportWithConfig(server, DefaultHTTPServerTransportConfig())
	if err != nil {
		// The built-in defaults are compile-time constants. A panic here can
		// only indicate a programming error in this package.
		panic(err)
	}
	return transport
}

// NewHTTPServerTransportWithConfig binds a reusable Server template with
// explicit, mandatory resource limits. Call Close during host shutdown.
func NewHTTPServerTransportWithConfig(server *Server, config HTTPServerTransportConfig) (*HTTPServerTransport, error) {
	return newHTTPServerTransportWithConfig(server, config, rand.Reader)
}

// newHTTPServerTransport keeps entropy injection package-private so production
// callers cannot weaken opaque IDs while tests exercise failure/collision paths.
func newHTTPServerTransport(server *Server, entropy io.Reader) *HTTPServerTransport {
	transport, err := newHTTPServerTransportWithConfig(server, DefaultHTTPServerTransportConfig(), entropy)
	if err != nil {
		panic(err)
	}
	return transport
}

func newHTTPServerTransportWithConfig(server *Server, config HTTPServerTransportConfig, entropy io.Reader) (*HTTPServerTransport, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if server == nil {
		server = NewServer()
	}
	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	t := &HTTPServerTransport{
		template:          server,
		config:            config,
		sessions:          make(map[string]*httpServerSession),
		principalSessions: make(map[string]map[string]*httpServerSession),
		sessionIDEntropy:  entropy,
		sweepStop:         make(chan struct{}),
		sweepDone:         make(chan struct{}),
		closeDone:         make(chan struct{}),
		sweepCtx:          sweepCtx,
		sweepCancel:       sweepCancel,
	}
	go t.sweepLoop()
	return t, nil
}

// Run blocks until ctx is cancelled or Close is called. Cancelling ctx closes
// all active and provisional sessions.
func (t *HTTPServerTransport) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		err := ctx.Err()
		_ = t.Close()
		return err
	case <-t.closeDone:
		return nil
	}
}

// Close shuts down admission, atomically detaches every active/provisional
// session, cancels all operation contexts, and stops the sweeper. It is safe to
// call concurrently and waits for the winning close operation to finish.
func (t *HTTPServerTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		done := t.closeDone
		t.mu.Unlock()
		<-done
		return nil
	}
	t.closed = true
	sessions := make([]*httpServerSession, 0, len(t.sessions))
	for _, session := range t.sessions {
		session.mu.Lock()
		if t.detachSessionLocked(session) {
			sessions = append(sessions, session)
		}
		session.mu.Unlock()
	}
	close(t.sweepStop)
	t.sweepCancel()
	t.mu.Unlock()

	for _, session := range sessions {
		session.shutdown()
	}
	<-t.sweepDone
	close(t.closeDone)
	return nil
}

// ClosePrincipal atomically detaches and closes all active and provisional
// sessions bound to principal. Empty principals are intentionally not indexed.
func (t *HTTPServerTransport) ClosePrincipal(principal string) int {
	if principal == "" {
		return 0
	}

	t.mu.Lock()
	indexed := t.principalSessions[principal]
	sessions := make([]*httpServerSession, 0, len(indexed))
	for _, session := range indexed {
		session.mu.Lock()
		if t.detachSessionLocked(session) {
			sessions = append(sessions, session)
		}
		session.mu.Unlock()
	}
	t.mu.Unlock()

	for _, session := range sessions {
		session.shutdown()
	}
	return len(sessions)
}

// Stats returns a source-free accounting snapshot.
func (t *HTTPServerTransport) Stats() HTTPServerTransportStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}

// ServeHTTP implements http.Handler for the streamable HTTP MCP transport.
func (t *HTTPServerTransport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		t.handleStream(w, r)
	case http.MethodPost:
		t.handlePost(w, r)
	case http.MethodDelete:
		t.handleDelete(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (t *HTTPServerTransport) handleStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
		return
	}

	session, requestHook, ok := t.authorizedSession(w, r, sessionID)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	lease, err := t.acquireSessionLease(session, r, requestHook, leaseSSE)
	if err != nil {
		t.writeLeaseError(w, err)
		return
	}
	defer lease.release()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Mcp-Session-Id", sessionID)
	// Commit headers immediately so an empty outbox still establishes the
	// stream and competing GETs can receive a prompt one-stream rejection.
	flusher.Flush()

	for {
		if payload, found := session.dequeue(); found {
			if _, err := fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
			continue
		}

		select {
		case <-session.outboxWake:
		case <-lease.ctx.Done():
			return
		}
	}
}

func (t *HTTPServerTransport) handlePost(w http.ResponseWriter, r *http.Request) {
	decodeLease, err := t.acquireDecode()
	if err != nil {
		t.writeLeaseError(w, err)
		return
	}
	releaseDecodeHere := true
	defer func() {
		if releaseDecodeHere {
			decodeLease.release()
		}
	}()

	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		msg, ok := t.decodeOneMessage(w, r)
		if !ok {
			return
		}
		decodeLease.release()
		releaseDecodeHere = false
		if msg.Method != "initialize" {
			http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
			return
		}
		t.handleInitialize(w, r, msg)
		return
	}

	session, requestHook, ok := t.authorizedSession(w, r, sessionID)
	if !ok {
		return
	}
	msg, ok := t.decodeOneMessage(w, r)
	if !ok {
		return
	}
	decodeLease.release()
	releaseDecodeHere = false

	// A method-less message is a continuation of a server-initiated nested
	// request, not another ordinary handler. Only an ID currently outstanding
	// on this exact cloned Server may enter the separately bounded control lane.
	if msg.Method == "" {
		if !hasJSONRPCID(msg.ID) || !session.server.hasPendingResponse(msg.ID) {
			http.Error(w, "unmatched JSON-RPC response", http.StatusBadRequest)
			return
		}
		controlLease, err := t.acquireSessionLease(session, r, requestHook, leaseControl)
		if err != nil {
			t.writeLeaseError(w, err)
			return
		}
		defer controlLease.release()
		if !session.server.deliverPendingResponse(msg) {
			http.Error(w, "unmatched JSON-RPC response", http.StatusBadRequest)
			return
		}
		w.Header().Set("Mcp-Session-Id", sessionID)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	lease, err := t.acquireSessionLease(session, r, requestHook, leaseMessage)
	if err != nil {
		t.writeLeaseError(w, err)
		return
	}
	releaseHere := true
	defer func() {
		if releaseHere {
			lease.release()
		}
	}()
	w.Header().Set("Mcp-Session-Id", sessionID)
	w.WriteHeader(http.StatusAccepted)

	// Completion owns the admission lease until asynchronous request handling
	// really finishes. Notifications and nested responses release synchronously.
	session.server.handleMessageWithCompletion(lease.ctx, msg, lease.release)
	releaseHere = false
}

func (t *HTTPServerTransport) handleInitialize(w http.ResponseWriter, r *http.Request, msg *jsonRPCMessage) {
	session, err := t.newSession(r)
	if err != nil {
		t.writeSessionCreationError(w, err)
		return
	}
	rollback := true
	defer func() {
		if rollback {
			t.closeExpectedSession(session, false)
		}
	}()

	result, rpcErr := session.server.handleInitialize(msg.Params)
	payload, marshalErr := json.Marshal(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      rawJSONID(normalizeID(msg.ID)),
		Result:  mustRawResult(result, rpcErr == nil),
		Error:   rpcErr,
	})
	if marshalErr != nil {
		http.Error(w, "failed to encode initialize response", http.StatusInternalServerError)
		return
	}
	if rpcErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(payload)
		return
	}
	if !t.activateSession(session) {
		http.Error(w, "failed to activate session", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Session-Id", session.id)
	w.Header().Set("Cache-Control", "no-store")
	n, writeErr := w.Write(payload)
	if writeErr != nil || n != len(payload) {
		return
	}
	rollback = false
}

func (t *HTTPServerTransport) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
		return
	}

	session, _, ok := t.authorizedSession(w, r, sessionID)
	if !ok {
		return
	}
	if !t.closeExpectedSession(session, false) {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (t *HTTPServerTransport) decodeOneMessage(w http.ResponseWriter, r *http.Request) (*jsonRPCMessage, bool) {
	reader := http.MaxBytesReader(w, r.Body, t.config.MaxRequestBodyBytes)
	decoder := json.NewDecoder(reader)
	var msg jsonRPCMessage
	if err := decoder.Decode(&msg); err != nil {
		t.writeDecodeError(w, err)
		return nil, false
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			http.Error(w, "request must contain exactly one JSON value", http.StatusBadRequest)
		} else {
			t.writeDecodeError(w, err)
		}
		return nil, false
	}
	return &msg, true
}

func (t *HTTPServerTransport) writeDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid JSON", http.StatusBadRequest)
}

func (t *HTTPServerTransport) writeSessionCreationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errHTTPSessionLimit), errors.Is(err, errHTTPPrincipalLimit):
		t.setRetryAfter(w)
		http.Error(w, "session capacity exhausted", http.StatusTooManyRequests)
	case errors.Is(err, errHTTPTransportClosed):
		t.setRetryAfter(w)
		http.Error(w, "transport unavailable", http.StatusServiceUnavailable)
	default:
		http.Error(w, "failed to create session", http.StatusInternalServerError)
	}
}

func (t *HTTPServerTransport) writeLeaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errHTTPMessageLimit):
		t.setRetryAfter(w)
		http.Error(w, "message capacity exhausted", http.StatusTooManyRequests)
	case errors.Is(err, errHTTPSSEAlreadyOpen):
		t.setRetryAfter(w)
		http.Error(w, "SSE stream already open", http.StatusConflict)
	case errors.Is(err, errHTTPTransportClosed):
		t.setRetryAfter(w)
		http.Error(w, "transport unavailable", http.StatusServiceUnavailable)
	default:
		http.Error(w, "unknown session", http.StatusNotFound)
	}
}

func (t *HTTPServerTransport) setRetryAfter(w http.ResponseWriter) {
	seconds := t.config.RetryAfter / time.Second
	if t.config.RetryAfter%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(int64(seconds), 10))
}

func (t *HTTPServerTransport) newSession(r *http.Request) (*httpServerSession, error) {
	t.mu.Lock()
	hook := t.sessionCtx
	principalFunc := t.principalFunc
	if err := t.checkSessionCapacityLocked(""); err != nil {
		t.stats.RejectedSessionCreations++
		t.mu.Unlock()
		return nil, err
	}
	t.mu.Unlock()

	base := context.Background()
	if hook != nil {
		if derived := hook(r); derived != nil {
			base = derived
		}
	}
	// Preserve values but never inherit initialize-request cancellation.
	base = context.WithoutCancel(base)

	principal := ""
	principalBound := principalFunc != nil
	if principalFunc != nil {
		var err error
		principal, err = principalFunc(r)
		if err != nil {
			t.recordRejectedSessionCreation()
			return nil, fmt.Errorf("mcp: deriving HTTP session principal: %w", err)
		}
	}

	for attempt := 0; attempt < httpSessionCollisionRetries; attempt++ {
		sessionID, err := t.generateHTTPSessionID()
		if err != nil {
			t.recordRejectedSessionCreation()
			return nil, err
		}

		t.mu.Lock()
		if err := t.checkSessionCapacityLocked(principal); err != nil {
			t.stats.RejectedSessionCreations++
			t.mu.Unlock()
			return nil, err
		}
		if _, exists := t.sessions[sessionID]; exists {
			t.mu.Unlock()
			continue
		}

		ctx, cancel := context.WithCancel(base)
		now := time.Now()
		server := cloneServerTemplate(t.template)
		session := &httpServerSession{
			id:             sessionID,
			principal:      principal,
			principalBound: principalBound,
			transport:      t,
			server:         server,
			ctx:            ctx,
			cancel:         cancel,
			createdAt:      now,
			lastActivity:   now,
			state:          httpSessionProvisional,
			outboxWake:     make(chan struct{}, 1),
		}
		server.attachWriter(func(data []byte) error {
			return t.enqueue(session, data)
		})

		t.sessions[sessionID] = session
		if principal != "" {
			byPrincipal := t.principalSessions[principal]
			if byPrincipal == nil {
				byPrincipal = make(map[string]*httpServerSession)
				t.principalSessions[principal] = byPrincipal
			}
			byPrincipal[sessionID] = session
		}
		t.stats.ProvisionalSessions++
		t.mu.Unlock()
		return session, nil
	}

	t.recordRejectedSessionCreation()
	return nil, fmt.Errorf("mcp: exhausted %d HTTP session ID collision attempts", httpSessionCollisionRetries)
}

func (t *HTTPServerTransport) checkSessionCapacityLocked(principal string) error {
	if t.closed {
		return errHTTPTransportClosed
	}
	if len(t.sessions) >= t.config.MaxSessions {
		return errHTTPSessionLimit
	}
	if principal != "" && len(t.principalSessions[principal]) >= t.config.MaxSessionsPerPrincipal {
		return errHTTPPrincipalLimit
	}
	return nil
}

func (t *HTTPServerTransport) recordRejectedSessionCreation() {
	t.mu.Lock()
	t.stats.RejectedSessionCreations++
	t.mu.Unlock()
}

func (t *HTTPServerTransport) activateSession(session *httpServerSession) bool {
	var shutdown bool
	t.mu.Lock()
	session.mu.Lock()
	if t.closed || t.sessions[session.id] != session || session.state != httpSessionProvisional {
		session.mu.Unlock()
		t.mu.Unlock()
		return false
	}
	if t.sessionExpiredLocked(session, time.Now()) {
		t.detachSessionLocked(session)
		t.stats.ExpiredSessions++
		shutdown = true
	} else {
		session.state = httpSessionActive
		session.lastActivity = time.Now()
		t.stats.ProvisionalSessions--
		t.stats.ActiveSessions++
	}
	session.mu.Unlock()
	t.mu.Unlock()
	if shutdown {
		session.shutdown()
	}
	return !shutdown
}

func (t *HTTPServerTransport) getSession(id string) (*httpServerSession, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	session, ok := t.sessions[id]
	return session, ok
}

func (t *HTTPServerTransport) authorizedSession(w http.ResponseWriter, r *http.Request, id string) (*httpServerSession, HTTPSessionRequestContextFunc, bool) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		t.setRetryAfter(w)
		http.Error(w, "transport unavailable", http.StatusServiceUnavailable)
		return nil, nil, false
	}
	session, ok := t.sessions[id]
	authorize := t.sessionAuth
	principalFunc := t.principalFunc
	validator := t.principalValidator
	requestHook := t.requestCtx
	t.mu.Unlock()
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return nil, nil, false
	}

	if session.principalBound {
		if principalFunc == nil {
			http.Error(w, "session authorization failed", http.StatusInternalServerError)
			return nil, nil, false
		}
		principal, err := principalFunc(r)
		if err != nil {
			http.Error(w, "session authorization failed", http.StatusInternalServerError)
			return nil, nil, false
		}
		if principal != session.principal {
			http.Error(w, "session not authorized", http.StatusForbidden)
			return nil, nil, false
		}
	}
	if session.principal != "" && validator != nil {
		valid, err := validator(r.Context(), session.principal)
		if err != nil {
			http.Error(w, "session authorization failed", http.StatusInternalServerError)
			return nil, nil, false
		}
		if !valid {
			t.ClosePrincipal(session.principal)
			http.Error(w, "session not authorized", http.StatusForbidden)
			return nil, nil, false
		}
	}
	if authorize != nil {
		authorized, err := authorize(session.ctx, r)
		if err != nil {
			http.Error(w, "session authorization failed", http.StatusInternalServerError)
			return nil, nil, false
		}
		if !authorized {
			http.Error(w, "session not authorized", http.StatusForbidden)
			return nil, nil, false
		}
	}
	return session, requestHook, true
}

type leaseKind uint8

const (
	leaseMessage leaseKind = iota
	leaseSSE
	leaseControl
)

type httpSessionLease struct {
	once      sync.Once
	transport *HTTPServerTransport
	session   *httpServerSession
	kind      leaseKind
	ctx       context.Context
	cancel    context.CancelFunc
	stopFresh func() bool
	stopHTTP  func() bool
}

func (t *HTTPServerTransport) acquireSessionLease(session *httpServerSession, r *http.Request, hook HTTPSessionRequestContextFunc, kind leaseKind) (*httpSessionLease, error) {
	fresh := r.Context()
	if hook != nil {
		if derived := hook(session.ctx, r); derived != nil {
			fresh = derived
		}
	}

	var expired bool
	t.mu.Lock()
	session.mu.Lock()
	if t.closed {
		session.mu.Unlock()
		t.mu.Unlock()
		return nil, errHTTPTransportClosed
	}
	if t.sessions[session.id] != session || session.state != httpSessionActive {
		session.mu.Unlock()
		t.mu.Unlock()
		return nil, errHTTPSessionInactive
	}
	if t.sessionExpiredLocked(session, time.Now()) {
		t.detachSessionLocked(session)
		t.stats.ExpiredSessions++
		expired = true
	} else {
		switch kind {
		case leaseMessage:
			if t.stats.InFlightMessages >= t.config.MaxConcurrentMessages || session.inFlightMessages >= t.config.MaxConcurrentMessagesPerSession {
				t.stats.RejectedMessages++
				session.mu.Unlock()
				t.mu.Unlock()
				return nil, errHTTPMessageLimit
			}
			t.stats.InFlightMessages++
			session.inFlightMessages++
		case leaseSSE:
			if session.sseOpen {
				session.mu.Unlock()
				t.mu.Unlock()
				return nil, errHTTPSSEAlreadyOpen
			}
			session.sseOpen = true
		case leaseControl:
			if t.controlInFlight >= t.config.MaxConcurrentMessages || session.controlInFlight >= t.config.MaxConcurrentMessagesPerSession {
				t.stats.RejectedMessages++
				session.mu.Unlock()
				t.mu.Unlock()
				return nil, errHTTPMessageLimit
			}
			t.controlInFlight++
			session.controlInFlight++
		}
		session.operations++
		session.lastActivity = time.Now()
	}
	session.mu.Unlock()
	t.mu.Unlock()
	if expired {
		session.shutdown()
		return nil, errHTTPSessionInactive
	}

	// POST handlers are asynchronous by protocol: the HTTP request returns 202
	// before the result is delivered over SSE. Preserve fresh values but detach
	// POST-request cancellation so net/http does not immediately kill the tool.
	// A live SSE operation, by contrast, must end with its HTTP connection.
	freshValues := fresh
	httpValues := r.Context()
	if kind == leaseMessage {
		freshValues = context.WithoutCancel(freshValues)
		httpValues = context.WithoutCancel(httpValues)
	}
	base := immutableSessionRequestContext{
		Context: session.ctx,
		fresh:   freshValues,
		http:    httpValues,
	}
	operationCtx, cancel := context.WithCancel(base)
	lease := &httpSessionLease{
		transport: t,
		session:   session,
		kind:      kind,
		ctx:       operationCtx,
		cancel:    cancel,
	}
	if kind == leaseSSE || kind == leaseControl {
		lease.stopFresh = context.AfterFunc(fresh, cancel)
		lease.stopHTTP = context.AfterFunc(r.Context(), cancel)
	}
	return lease, nil
}

func (l *httpSessionLease) release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.cancel()
		if l.stopFresh != nil {
			l.stopFresh()
		}
		if l.stopHTTP != nil {
			l.stopHTTP()
		}

		t := l.transport
		session := l.session
		t.mu.Lock()
		session.mu.Lock()
		if session.operations <= 0 {
			session.mu.Unlock()
			t.mu.Unlock()
			panic("mcp: HTTP session operation accounting underflow")
		}
		session.operations--
		switch l.kind {
		case leaseMessage:
			if session.inFlightMessages <= 0 || t.stats.InFlightMessages <= 0 {
				session.mu.Unlock()
				t.mu.Unlock()
				panic("mcp: HTTP message accounting underflow")
			}
			session.inFlightMessages--
			t.stats.InFlightMessages--
		case leaseSSE:
			session.sseOpen = false
		case leaseControl:
			if session.controlInFlight <= 0 || t.controlInFlight <= 0 {
				session.mu.Unlock()
				t.mu.Unlock()
				panic("mcp: HTTP control-lane accounting underflow")
			}
			session.controlInFlight--
			t.controlInFlight--
		}
		session.lastActivity = time.Now()
		session.mu.Unlock()
		t.mu.Unlock()
	})
}

type httpDecodeLease struct {
	once      sync.Once
	transport *HTTPServerTransport
}

func (t *HTTPServerTransport) acquireDecode() (*httpDecodeLease, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errHTTPTransportClosed
	}
	if t.decodeInFlight >= t.config.MaxConcurrentMessages {
		t.stats.RejectedMessages++
		return nil, errHTTPMessageLimit
	}
	t.decodeInFlight++
	return &httpDecodeLease{transport: t}, nil
}

func (l *httpDecodeLease) release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.transport.mu.Lock()
		defer l.transport.mu.Unlock()
		if l.transport.decodeInFlight <= 0 {
			panic("mcp: HTTP decode accounting underflow")
		}
		l.transport.decodeInFlight--
	})
}

// immutableSessionRequestContext combines current request values/cancellation
// with immutable session values. Session values intentionally win on key
// collisions so a refresh hook cannot rewrite owner identity.
type immutableSessionRequestContext struct {
	context.Context
	fresh context.Context
	http  context.Context
}

func (c immutableSessionRequestContext) Value(key any) any {
	if value := c.Context.Value(key); value != nil {
		return value
	}
	if c.fresh != nil {
		if value := c.fresh.Value(key); value != nil {
			return value
		}
	}
	if c.http != nil {
		return c.http.Value(key)
	}
	return nil
}

func (c immutableSessionRequestContext) Deadline() (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, candidate := range []context.Context{c.Context, c.fresh, c.http} {
		if candidate == nil {
			continue
		}
		if deadline, ok := candidate.Deadline(); ok && (!found || deadline.Before(earliest)) {
			earliest = deadline
			found = true
		}
	}
	return earliest, found
}

func (t *HTTPServerTransport) enqueue(session *httpServerSession, data []byte) error {
	session.mu.Lock()
	if session.state == httpSessionClosed || session.closed {
		session.mu.Unlock()
		return errHTTPSessionInactive
	}
	if len(session.outbox) >= t.config.OutboxMaxMessages || int64(len(data)) > t.config.OutboxMaxBytes-session.outboxBytes {
		session.mu.Unlock()
		t.closeOutboxOverflow(session)
		return errHTTPOutboxOverflow
	}
	// Copy only after capacity admission. Oversize model/tool output is already
	// present in the caller, but must not trigger an additional unbounded copy.
	snapshot := append([]byte(nil), data...)
	session.outbox = append(session.outbox, snapshot)
	session.outboxBytes += int64(len(snapshot))
	select {
	case session.outboxWake <- struct{}{}:
	default:
	}
	session.mu.Unlock()
	return nil
}

func (s *httpServerSession) dequeue() ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.outbox) == 0 {
		return nil, false
	}
	payload := s.outbox[0]
	s.outbox[0] = nil
	s.outbox = s.outbox[1:]
	s.outboxBytes -= int64(len(payload))
	if len(s.outbox) == 0 {
		s.outbox = nil
	}
	return payload, true
}

func (t *HTTPServerTransport) closeOutboxOverflow(session *httpServerSession) {
	closed := false
	t.mu.Lock()
	session.mu.Lock()
	if t.detachSessionLocked(session) {
		t.stats.OutboxOverflowClosures++
		closed = true
	}
	session.mu.Unlock()
	t.mu.Unlock()
	if closed {
		session.shutdown()
	}
}

func (t *HTTPServerTransport) closeExpectedSession(session *httpServerSession, expired bool) bool {
	closed := false
	t.mu.Lock()
	session.mu.Lock()
	if t.detachSessionLocked(session) {
		if expired {
			t.stats.ExpiredSessions++
		}
		closed = true
	}
	session.mu.Unlock()
	t.mu.Unlock()
	if closed {
		session.shutdown()
	}
	return closed
}

// detachSessionLocked requires t.mu and session.mu, in that order.
func (t *HTTPServerTransport) detachSessionLocked(session *httpServerSession) bool {
	if t.sessions[session.id] != session || session.state == httpSessionClosed {
		return false
	}
	delete(t.sessions, session.id)
	if session.principal != "" {
		if indexed := t.principalSessions[session.principal]; indexed != nil {
			delete(indexed, session.id)
			if len(indexed) == 0 {
				delete(t.principalSessions, session.principal)
			}
		}
	}
	switch session.state {
	case httpSessionProvisional:
		t.stats.ProvisionalSessions--
	case httpSessionActive:
		t.stats.ActiveSessions--
	}
	session.state = httpSessionClosed
	session.closed = true
	// Discard source-bearing responses as soon as the session is detached.
	// Active writers hold their own snapshots and observe the closed state on
	// any later enqueue attempt.
	for i := range session.outbox {
		session.outbox[i] = nil
	}
	session.outbox = nil
	session.outboxBytes = 0
	return true
}

func (s *httpServerSession) shutdown() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.server != nil {
		_ = s.server.Close()
	}
	if s.outboxWake != nil {
		select {
		case s.outboxWake <- struct{}{}:
		default:
		}
	}
}

// close is retained for package-internal tests and standalone session owners.
// Transport-managed callers use closeExpectedSession so indexes and counters
// are updated atomically.
func (s *httpServerSession) close() {
	if s.transport != nil {
		if s.transport.closeExpectedSession(s, false) {
			return
		}
	}
	s.mu.Lock()
	s.state = httpSessionClosed
	s.closed = true
	s.mu.Unlock()
	s.shutdown()
}

func (t *HTTPServerTransport) sessionExpiredLocked(session *httpServerSession, now time.Time) bool {
	if !now.Before(session.createdAt.Add(t.config.AbsoluteLifetime)) {
		return true
	}
	return session.operations == 0 && !now.Before(session.lastActivity.Add(t.config.IdleTimeout))
}

func (t *HTTPServerTransport) sweepLoop() {
	ticker := time.NewTicker(t.config.sweepInterval())
	defer func() {
		ticker.Stop()
		close(t.sweepDone)
	}()
	for {
		select {
		case <-ticker.C:
			t.sweepOnce(time.Now())
		case <-t.sweepStop:
			return
		}
	}
}

func (t *HTTPServerTransport) sweepOnce(now time.Time) {
	var expired []*httpServerSession
	t.mu.Lock()
	for _, session := range t.sessions {
		session.mu.Lock()
		if t.sessionExpiredLocked(session, now) && t.detachSessionLocked(session) {
			t.stats.ExpiredSessions++
			expired = append(expired, session)
		}
		session.mu.Unlock()
	}
	validator := t.principalValidator
	principals := make([]string, 0, len(t.principalSessions))
	if validator != nil {
		for principal := range t.principalSessions {
			principals = append(principals, principal)
		}
	}
	t.mu.Unlock()

	for _, session := range expired {
		session.shutdown()
	}
	for _, principal := range principals {
		valid, err := validator(t.sweepCtx, principal)
		if err != nil {
			// Identity-store outages retain sessions and retry next sweep.
			continue
		}
		if !valid {
			t.ClosePrincipal(principal)
		}
	}
}

func cloneServerTemplate(template *Server) *Server {
	template.mu.Lock()
	defer template.mu.Unlock()

	cloned := NewServer(
		WithServerInfo(template.serverInfo),
		WithServerInstructions(template.instructions),
	)
	cloned.protocol = template.protocol
	cloned.tools = append([]serverTool(nil), template.tools...)
	cloned.resources = append([]Resource(nil), template.resources...)
	cloned.resourceTemplates = append([]ResourceTemplate(nil), template.resourceTemplates...)
	cloned.resourceReader = template.resourceReader
	cloned.prompts = append([]Prompt(nil), template.prompts...)
	cloned.promptGetter = template.promptGetter
	return cloned
}

func (t *HTTPServerTransport) generateHTTPSessionID() (string, error) {
	t.entropyMu.Lock()
	defer t.entropyMu.Unlock()
	return generateHTTPSessionID(t.sessionIDEntropy)
}

func generateHTTPSessionID(entropy io.Reader) (string, error) {
	if entropy == nil {
		return "", fmt.Errorf("mcp: generating HTTP session ID: entropy source is nil")
	}
	random := make([]byte, httpSessionIDBytes)
	if _, err := io.ReadFull(entropy, random); err != nil {
		return "", fmt.Errorf("mcp: generating HTTP session ID: %w", err)
	}
	return "mcp-" + base64.RawURLEncoding.EncodeToString(random), nil
}

func mustRawResult(result any, ok bool) json.RawMessage {
	if !ok || result == nil {
		return nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	return data
}

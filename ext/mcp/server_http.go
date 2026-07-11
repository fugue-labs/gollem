package mcp

import (
	"bytes"
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
	httpSessionIDBytes             = 32
	httpSessionCollisionRetries    = 8
	httpProtectedDecodesPerSession = 1
	defaultHTTPSSEWriteTimeout     = 30 * time.Second
)

var (
	errHTTPTransportClosed  = errors.New("mcp: HTTP server transport is closed")
	errHTTPSessionLimit     = errors.New("mcp: HTTP session capacity exhausted")
	errHTTPPrincipalLimit   = errors.New("mcp: HTTP principal session capacity exhausted")
	errHTTPQuotaKeyLimit    = errors.New("mcp: HTTP aggregate session quota exhausted")
	errHTTPSessionInactive  = errors.New("mcp: HTTP session is not active")
	errHTTPMessageLimit     = errors.New("mcp: HTTP message concurrency exhausted")
	errHTTPSSEAlreadyOpen   = errors.New("mcp: HTTP session already has an SSE stream")
	errHTTPOutboxOverflow   = errors.New("mcp: HTTP session outbox capacity exhausted")
	errHTTPResponseReserve  = errors.New("mcp: HTTP response reservation capacity exhausted")
	errHTTPResponseTooLarge = errors.New("mcp: encoded HTTP response exceeds its reservation")
	errHTTPDuplicateID      = errors.New("mcp: duplicate outstanding HTTP request ID")
	errHTTPRequestContext   = errors.New("mcp: HTTP request context refresh failed")
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
// quotas and revocation indexing. It also bounds concurrent pre-session
// initialize decodes per principal; transports without this hook retain only
// the global initialize-decode cap. A token hash is suitable; plaintext bearer
// credentials are not.
type HTTPSessionPrincipalFunc func(r *http.Request) (string, error)

// HTTPSessionQuotaKeyFunc extracts a stable non-secret aggregate quota key
// independently of the immutable authorization principal. Multi-key tenants
// can therefore share a workspace session cap without allowing one credential
// to authorize, validate, or revoke another credential's session.
type HTTPSessionQuotaKeyFunc func(r *http.Request) (string, error)

// HTTPSessionPrincipalValidator reports whether a previously captured
// principal remains valid. It is called on follow-up requests and by the
// expiration sweeper. Sweep cadence is IdleTimeout/2, clamped to 10ms..1m.
// Returning an error is fail-closed for the request but retains the session so
// a temporary identity-store outage cannot mass-revoke clients. The callback
// must honor ctx cancellation.
type HTTPSessionPrincipalValidator func(ctx context.Context, principal string) (bool, error)

// HTTPSessionRequestContextFunc derives fresh request-scoped values for a
// follow-up operation. The transport calls it only after bounded admission,
// propagates errors fail-closed before dispatch, and always preserves the
// initializing session context with value precedence. Cancellation remains
// independently bound to the current HTTP request and session lifetime.
type HTTPSessionRequestContextFunc func(sessionCtx context.Context, r *http.Request) (context.Context, error)

// HTTPServerTransport serves MCP over the streamable HTTP transport. Each MCP
// session gets its own cloned Server instance and bounded resource accounting.
type HTTPServerTransport struct {
	mu sync.Mutex
	// messageWG covers every admitted asynchronous message lease, including
	// handlers whose session is detached by expiry, revocation, or DELETE before
	// transport shutdown. Add is serialized with shutdown by mu: Close marks the
	// transport closed under that mutex before it waits, so no Add can race Wait.
	messageWG sync.WaitGroup

	template *Server
	config   HTTPServerTransportConfig
	sessions map[string]*httpServerSession
	// principalSessions includes both provisional and active sessions so a
	// burst of slow initializations cannot bypass per-principal admission.
	principalSessions map[string]map[string]*httpServerSession
	// quotaSessions provides a second aggregate cap (for example, workspace)
	// while principalSessions remains token-specific for authorization/revocation.
	quotaSessions map[string]map[string]*httpServerSession

	sessionCtx         func(*http.Request) context.Context
	sessionAuth        HTTPSessionAuthorizer
	principalFunc      HTTPSessionPrincipalFunc
	quotaKeyFunc       HTTPSessionQuotaKeyFunc
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
	decodeInFlight          int
	protectedDecodeInFlight int
	controlInFlight         int
	sseInFlight             int
	initializingByPrincipal map[string]int

	// SSE writes use a fixed non-zero bound. Request body deadlines are explicit
	// in HTTPServerTransportConfig.
	sseWriteTimeout time.Duration
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

// SetSessionQuotaKeyFunc installs the aggregate quota-key extractor. Configure
// it before accepting requests; existing session quota ownership is immutable.
func (t *HTTPServerTransport) SetSessionQuotaKeyFunc(f HTTPSessionQuotaKeyFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.quotaKeyFunc = f
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
// values captured at initialize remain immutable and take precedence. An error
// rejects the admitted operation with no dispatch and releases every lease.
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

type httpOutboxEntry struct {
	sequence   uint64
	data       []byte
	responseID string
}

type httpServerSession struct {
	mu sync.Mutex

	id             string
	principal      string
	quotaKey       string
	principalBound bool
	transport      *HTTPServerTransport
	server         *Server
	ctx            context.Context
	cancel         context.CancelFunc
	createdAt      time.Time
	lastActivity   time.Time
	state          httpSessionState
	closed         bool // retained for diagnostics and compatibility with package tests

	operations              int
	decodeInFlight          int
	protectedDecodeInFlight int
	inFlightMessages        int
	controlInFlight         int
	sseOpen                 bool

	outbox             []httpOutboxEntry
	outboxBytes        int64
	outboxWake         chan struct{}
	nextOutboxSequence uint64
	// responseIDs retains incoming IDs through dequeue, not merely handler
	// completion. This prevents duplicate state-changing calls from producing
	// two owner responses that a conforming JSON-RPC client cannot correlate.
	responseIDs map[string]struct{}

	// Response capacity is admitted before a request handler runs. Ordinary
	// nested requests and notifications may use only the unreserved remainder,
	// so they cannot make a completed state-changing response undeliverable.
	responseReservedMessages int
	responseReservedBytes    int64
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
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	if server == nil {
		server = NewServer()
	}
	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	t := &HTTPServerTransport{
		template:                server,
		config:                  config,
		sessions:                make(map[string]*httpServerSession),
		principalSessions:       make(map[string]map[string]*httpServerSession),
		quotaSessions:           make(map[string]map[string]*httpServerSession),
		sessionIDEntropy:        entropy,
		sweepStop:               make(chan struct{}),
		sweepDone:               make(chan struct{}),
		closeDone:               make(chan struct{}),
		sweepCtx:                sweepCtx,
		sweepCancel:             sweepCancel,
		sseWriteTimeout:         defaultHTTPSSEWriteTimeout,
		initializingByPrincipal: make(map[string]int),
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
	// Request dispatch is asynchronous. Join every admitted message before Close
	// returns so callers may safely tear down databases and other dependencies
	// used by tool handlers. A transport-wide lease wait is required here: a
	// session may have been detached by expiry, revocation, or DELETE while one
	// of its canceled handlers was still unwinding.
	t.messageWG.Wait()
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
	stats := t.stats
	stats.InFlightDecodes = t.decodeInFlight
	stats.InFlightProtectedDecodes = t.protectedDecodeInFlight
	stats.InFlightControlResponses = t.controlInFlight
	stats.InFlightSSEStreams = t.sseInFlight
	return stats
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
	_, ok = w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	lease, err := t.acquireSessionLease(session, r, requestHook, leaseSSE, "")
	if err != nil {
		t.writeLeaseError(w, err)
		return
	}
	defer lease.release()
	streamWriter, err := newHTTPSSEWriter(w, lease.ctx, t.sseWriteTimeout)
	if err != nil {
		// Do not call Write on a writer that cannot be deadline-bounded: even
		// attempting to render an error could retain the handler indefinitely.
		return
	}
	defer streamWriter.close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Mcp-Session-Id", sessionID)
	// Commit headers immediately so an empty outbox still establishes the
	// stream and competing GETs can receive a prompt one-stream rejection.
	if err := streamWriter.flush(); err != nil {
		return
	}

	for {
		if sequence, payload, found := session.peekOutbox(); found {
			if err := streamWriter.write(payload); err != nil {
				return
			}
			if !session.ackOutbox(sequence) {
				// The only legitimate removal while this one-per-session SSE
				// writer is active is session shutdown. Treat any accounting drift
				// as terminal instead of risking duplicate delivery.
				t.closeExpectedSession(session, false)
				return
			}
			continue
		}

		select {
		case <-session.outboxWake:
		case <-lease.ctx.Done():
			return
		}
	}
}

type httpSSEWriter struct {
	w          http.ResponseWriter
	controller *http.ResponseController
	ctx        context.Context
	timeout    time.Duration
	stopCancel func() bool
}

func newHTTPSSEWriter(w http.ResponseWriter, ctx context.Context, timeout time.Duration) (*httpSSEWriter, error) {
	if timeout <= 0 {
		timeout = defaultHTTPSSEWriteTimeout
	}
	writer := &httpSSEWriter{
		w:          w,
		controller: http.NewResponseController(w),
		ctx:        ctx,
		timeout:    timeout,
	}
	// Fail closed before committing SSE headers when the writer cannot provide
	// a real write deadline. Detached write goroutines are deliberately not a
	// fallback because a hostile writer could retain one forever.
	if err := writer.controller.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	if err := writer.controller.SetWriteDeadline(time.Time{}); err != nil {
		return nil, err
	}
	writer.stopCancel = context.AfterFunc(ctx, func() {
		_ = writer.controller.SetWriteDeadline(time.Now())
	})
	return writer, nil
}

func (w *httpSSEWriter) write(payload []byte) error {
	return w.withDeadline(func() error {
		if _, err := fmt.Fprintf(w.w, "event: message\ndata: %s\n\n", payload); err != nil {
			return err
		}
		return w.controller.Flush()
	})
}

func (w *httpSSEWriter) flush() error {
	return w.withDeadline(func() error {
		return w.controller.Flush()
	})
}

func (w *httpSSEWriter) withDeadline(operation func() error) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(w.timeout)
	if contextDeadline, ok := w.ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := w.controller.SetWriteDeadline(deadline); err != nil {
		return err
	}
	err := operation()
	if err != nil {
		return err
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	return w.controller.SetWriteDeadline(time.Time{})
}

func (w *httpSSEWriter) close() {
	if w.stopCancel != nil && !w.stopCancel() {
		// Cancellation won the race; preserve its expired deadline until the
		// blocked write has returned.
		return
	}
	_ = w.controller.SetWriteDeadline(time.Time{})
}

func (t *HTTPServerTransport) handlePost(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	var (
		session                    *httpServerSession
		requestHook                HTTPSessionRequestContextFunc
		initializationPrincipal    string
		initializationPrincipalSet bool
	)
	if sessionID != "" {
		var ok bool
		session, requestHook, ok = t.authorizedSession(w, r, sessionID)
		if !ok {
			return
		}
	} else {
		t.mu.Lock()
		principalFunc := t.principalFunc
		t.mu.Unlock()
		if principalFunc != nil {
			var err error
			initializationPrincipal, err = principalFunc(r)
			if err != nil {
				t.recordRejectedSessionCreation()
				http.Error(w, "failed to derive session principal", http.StatusInternalServerError)
				return
			}
			initializationPrincipalSet = true
		}
	}

	decodeLease, err := t.acquireDecode(session, initializationPrincipal, w, r)
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

	if sessionID == "" {
		msg, ok := t.decodeOneMessage(w, r, decodeLease.ctx, t.config.MaxInitializeRequestBodyBytes)
		if !ok {
			return
		}
		decodeErr := decodeLease.release()
		releaseDecodeHere = false
		if decodeErr != nil {
			t.writeDecodeError(w, decodeErr, decodeLease.ctx)
			return
		}
		if _, rejected := t.validateRequestID(w, msg); rejected {
			return
		}
		if msg.Method != "initialize" {
			http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
			return
		}
		t.handleInitialize(w, r, msg, initializationPrincipal, initializationPrincipalSet)
		return
	}

	bodyLimit := t.config.MaxRequestBodyBytes
	if decodeLease.protected {
		bodyLimit = t.config.MaxProtectedResponseBodyBytes
	}
	msg, ok := t.decodeOneMessage(w, r, decodeLease.ctx, bodyLimit)
	if !ok {
		t.closeAfterProtectedDecodeFailure(session, decodeLease)
		return
	}
	decodeErr := t.releaseDecodedMessage(session, decodeLease)
	releaseDecodeHere = false
	if decodeErr != nil {
		t.writeDecodeError(w, decodeErr, decodeLease.ctx)
		return
	}
	requestIDKey, rejected := t.validateRequestID(w, msg)
	if rejected {
		return
	}
	if decodeLease.protected && (msg.Method != "" || !hasJSONRPCID(msg.ID) || !session.server.hasPendingResponse(msg.ID)) {
		t.mu.Lock()
		t.stats.RejectedMessages++
		t.mu.Unlock()
		t.setRetryAfter(w)
		http.Error(w, "protected response decode rejected", http.StatusTooManyRequests)
		return
	}
	// A method-less message is a continuation of a server-initiated nested
	// request, not another ordinary handler. Only an ID currently outstanding
	// on this exact cloned Server may enter the separately bounded control lane.
	if msg.Method == "" {
		if !hasJSONRPCID(msg.ID) || !session.server.hasPendingResponse(msg.ID) {
			http.Error(w, "unmatched JSON-RPC response", http.StatusBadRequest)
			return
		}
		controlLease, err := t.acquireSessionLease(session, r, requestHook, leaseControl, "")
		if err != nil {
			t.writeLeaseError(w, err)
			return
		}
		defer controlLease.release()
		if nestedResponsePayloadBytes(msg) > t.config.MaxNestedResponseBytes {
			t.mu.Lock()
			t.stats.RejectedNestedResponses++
			t.stats.RejectedMessages++
			t.mu.Unlock()
			// Consume the pending call with a small synthetic error. Merely
			// rejecting this POST would strand the server-side sampling or
			// elicitation caller until its context timed out.
			oversize := &jsonRPCMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error: &jsonRPCError{
					Code:    -32001,
					Message: "nested response exceeds configured byte limit",
				},
			}
			if !session.server.deliverPendingResponse(oversize) {
				http.Error(w, "unmatched JSON-RPC response", http.StatusBadRequest)
				return
			}
			http.Error(w, "nested response too large", http.StatusRequestEntityTooLarge)
			return
		}
		if !session.server.deliverPendingResponse(msg) {
			http.Error(w, "unmatched JSON-RPC response", http.StatusBadRequest)
			return
		}
		w.Header().Set("Mcp-Session-Id", sessionID)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	lease, err := t.acquireSessionLease(session, r, requestHook, leaseMessage, requestIDKey)
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

func nestedResponsePayloadBytes(msg *jsonRPCMessage) int64 {
	if msg == nil {
		return 0
	}
	total := int64(len(msg.Result))
	if msg.Error != nil {
		total += int64(len(msg.Error.Message)) + int64(len(msg.Error.Data))
	}
	return total
}

func (t *HTTPServerTransport) validateRequestID(w http.ResponseWriter, msg *jsonRPCMessage) (string, bool) {
	if msg == nil || msg.ID == nil {
		return "", false
	}
	raw := bytes.TrimSpace(*msg.ID)
	if len(raw) > t.config.MaxRequestIDBytes {
		t.mu.Lock()
		t.stats.RejectedMessages++
		t.stats.RejectedOversizeRequestIDs++
		t.mu.Unlock()
		http.Error(w, "JSON-RPC request ID too large", http.StatusRequestEntityTooLarge)
		return "", true
	}
	if bytes.Equal(raw, []byte("null")) {
		return "", false
	}
	validType := false
	requestIDKey := ""
	if len(raw) > 0 && raw[0] == '"' {
		var value string
		validType = json.Unmarshal(raw, &value) == nil
		if validType {
			requestIDKey = "s:" + value
		}
	} else if len(raw) > 0 && (raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') {
		// Restrict numeric request IDs to signed integers that round-trip
		// exactly. normalizeID intentionally supports legacy float responses in
		// other transports, but a rounded/fractional owner-token response would
		// be uncorrelatable after a state-changing HTTP request.
		var value int64
		validType = json.Unmarshal(raw, &value) == nil
		if validType {
			requestIDKey = "i:" + strconv.FormatInt(value, 10)
		}
	}
	if validType {
		return requestIDKey, false
	}
	t.mu.Lock()
	t.stats.RejectedMessages++
	t.stats.RejectedInvalidRequestIDs++
	t.mu.Unlock()
	http.Error(w, "invalid JSON-RPC request ID", http.StatusBadRequest)
	return "", true
}

func (t *HTTPServerTransport) handleInitialize(w http.ResponseWriter, r *http.Request, msg *jsonRPCMessage, principal string, principalSet bool) {
	session, err := t.newSessionWithPrincipal(r, principal, principalSet)
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
		ID:      rawJSONID(responseID(msg.ID)),
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

// releaseDecodedMessage closes a protected session whenever the decode lease
// was canceled after JSON parsing succeeded but before the decoded message
// could be admitted. Protected lanes exist only to release server-initiated
// calls; returning without closing would strand an uncorrelatable pending call
// until its outer timeout. Ordinary decode cancellation remains request-local.
func (t *HTTPServerTransport) releaseDecodedMessage(session *httpServerSession, lease *httpDecodeLease) error {
	err := lease.release()
	if err != nil {
		t.closeAfterProtectedDecodeFailure(session, lease)
	}
	return err
}

func (t *HTTPServerTransport) closeAfterProtectedDecodeFailure(session *httpServerSession, lease *httpDecodeLease) {
	if lease == nil || !lease.protected {
		return
	}
	// The body could not be safely correlated with a pending ID. Close the
	// session and cancel every pending caller instead of retaining ambiguous
	// control state.
	t.mu.Lock()
	t.stats.RejectedNestedResponses++
	t.mu.Unlock()
	t.closeExpectedSession(session, false)
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

func (t *HTTPServerTransport) decodeOneMessage(w http.ResponseWriter, r *http.Request, decodeCtx context.Context, maxBodyBytes int64) (*jsonRPCMessage, bool) {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	boundedJSON := newJSONComplexityReader(reader, t.config.MaxJSONDepth, t.config.MaxJSONStructuralTokens)
	decoder := json.NewDecoder(boundedJSON)
	var msg jsonRPCMessage
	if err := decoder.Decode(&msg); err != nil {
		t.writeDecodeError(w, err, decodeCtx)
		return nil, false
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			http.Error(w, "request must contain exactly one JSON value", http.StatusBadRequest)
		} else {
			t.writeDecodeError(w, err, decodeCtx)
		}
		return nil, false
	}
	if decodeCtx != nil && decodeCtx.Err() != nil {
		t.writeDecodeError(w, decodeCtx.Err(), decodeCtx)
		return nil, false
	}
	return &msg, true
}

func (t *HTTPServerTransport) writeDecodeError(w http.ResponseWriter, err error, decodeCtx context.Context) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if errors.Is(err, errHTTPJSONDepthLimit) || errors.Is(err, errHTTPJSONTokenLimit) {
		t.mu.Lock()
		t.stats.RejectedJSONComplexity++
		t.stats.RejectedMessages++
		t.mu.Unlock()
		http.Error(w, "request JSON structure too complex", http.StatusRequestEntityTooLarge)
		return
	}
	if errors.Is(err, errHTTPJSONInvalidUTF8) {
		t.mu.Lock()
		t.stats.RejectedInvalidUTF8++
		t.stats.RejectedMessages++
		t.mu.Unlock()
		http.Error(w, "request JSON must be valid UTF-8", http.StatusBadRequest)
		return
	}
	var timeout interface{ Timeout() bool }
	if (decodeCtx != nil && errors.Is(decodeCtx.Err(), context.DeadlineExceeded)) || (errors.As(err, &timeout) && timeout.Timeout()) {
		w.Header().Set("Connection", "close")
		http.Error(w, "request body timeout", http.StatusRequestTimeout)
		return
	}
	if decodeCtx != nil && errors.Is(decodeCtx.Err(), context.Canceled) {
		w.Header().Set("Connection", "close")
		http.Error(w, "request canceled", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, "invalid JSON", http.StatusBadRequest)
}

func (t *HTTPServerTransport) writeSessionCreationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errHTTPSessionLimit), errors.Is(err, errHTTPPrincipalLimit), errors.Is(err, errHTTPQuotaKeyLimit):
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
	case errors.Is(err, errHTTPRequestContext):
		t.setRetryAfter(w)
		http.Error(w, "request policy unavailable", http.StatusServiceUnavailable)
	case errors.Is(err, errHTTPMessageLimit), errors.Is(err, errHTTPResponseReserve):
		t.setRetryAfter(w)
		http.Error(w, "message or response capacity exhausted", http.StatusTooManyRequests)
	case errors.Is(err, errHTTPDuplicateID):
		http.Error(w, "duplicate outstanding JSON-RPC request ID", http.StatusConflict)
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
	return t.newSessionWithPrincipal(r, "", false)
}

func (t *HTTPServerTransport) newSessionWithPrincipal(r *http.Request, principal string, principalSet bool) (*httpServerSession, error) {
	t.mu.Lock()
	hook := t.sessionCtx
	principalFunc := t.principalFunc
	quotaKeyFunc := t.quotaKeyFunc
	expired := t.reapExpiredAtCapacityLocked("", "", time.Now())
	capacityErr := t.checkSessionCapacityLocked("", "")
	if capacityErr != nil {
		t.stats.RejectedSessionCreations++
	}
	t.mu.Unlock()
	shutdownHTTPSessions(expired)
	if capacityErr != nil {
		return nil, capacityErr
	}

	base := context.Background()
	if hook != nil {
		if derived := hook(r); derived != nil {
			base = derived
		}
	}
	// Preserve values but never inherit initialize-request cancellation.
	base = context.WithoutCancel(base)

	principalBound := principalSet || principalFunc != nil
	if !principalSet && principalFunc != nil {
		var err error
		principal, err = principalFunc(r)
		if err != nil {
			t.recordRejectedSessionCreation()
			return nil, fmt.Errorf("mcp: deriving HTTP session principal: %w", err)
		}
	}
	quotaKey := ""
	if quotaKeyFunc != nil {
		var err error
		quotaKey, err = quotaKeyFunc(r)
		if err != nil {
			t.recordRejectedSessionCreation()
			return nil, fmt.Errorf("mcp: deriving HTTP session quota key: %w", err)
		}
		if quotaKey == "" {
			t.recordRejectedSessionCreation()
			return nil, fmt.Errorf("mcp: deriving HTTP session quota key: empty key")
		}
	}

	for attempt := 0; attempt < httpSessionCollisionRetries; attempt++ {
		sessionID, err := t.generateHTTPSessionID()
		if err != nil {
			t.recordRejectedSessionCreation()
			return nil, err
		}

		t.mu.Lock()
		expired = t.reapExpiredAtCapacityLocked(principal, quotaKey, time.Now())
		capacityErr = t.checkSessionCapacityLocked(principal, quotaKey)
		if capacityErr != nil {
			t.stats.RejectedSessionCreations++
			t.mu.Unlock()
			shutdownHTTPSessions(expired)
			return nil, capacityErr
		}
		if _, exists := t.sessions[sessionID]; exists {
			t.mu.Unlock()
			shutdownHTTPSessions(expired)
			continue
		}

		ctx, cancel := context.WithCancel(base)
		now := time.Now()
		server := cloneServerTemplate(t.template)
		session := &httpServerSession{
			id:             sessionID,
			principal:      principal,
			quotaKey:       quotaKey,
			principalBound: principalBound,
			transport:      t,
			server:         server,
			ctx:            ctx,
			cancel:         cancel,
			createdAt:      now,
			lastActivity:   now,
			state:          httpSessionProvisional,
			outboxWake:     make(chan struct{}, 1),
			responseIDs:    make(map[string]struct{}),
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
		if quotaKey != "" {
			byQuota := t.quotaSessions[quotaKey]
			if byQuota == nil {
				byQuota = make(map[string]*httpServerSession)
				t.quotaSessions[quotaKey] = byQuota
			}
			byQuota[sessionID] = session
		}
		t.stats.ProvisionalSessions++
		t.mu.Unlock()
		shutdownHTTPSessions(expired)
		return session, nil
	}

	t.recordRejectedSessionCreation()
	return nil, fmt.Errorf("mcp: exhausted %d HTTP session ID collision attempts", httpSessionCollisionRetries)
}

func (t *HTTPServerTransport) reapExpiredAtCapacityLocked(principal, quotaKey string, now time.Time) []*httpServerSession {
	globalFull := len(t.sessions) >= t.config.MaxSessions
	principalFull := principal != "" && len(t.principalSessions[principal]) >= t.config.MaxSessionsPerPrincipal
	quotaFull := quotaKey != "" && len(t.quotaSessions[quotaKey]) >= t.config.MaxSessionsPerQuotaKey
	if !globalFull && !principalFull && !quotaFull {
		return nil
	}
	return t.reapExpiredSessionsLocked(now)
}

// reapExpiredSessionsLocked requires t.mu and returns detached sessions for
// shutdown after the caller releases t.mu.
func (t *HTTPServerTransport) reapExpiredSessionsLocked(now time.Time) []*httpServerSession {
	var expired []*httpServerSession
	for _, session := range t.sessions {
		session.mu.Lock()
		if t.sessionExpiredLocked(session, now) && t.detachSessionLocked(session) {
			t.stats.ExpiredSessions++
			expired = append(expired, session)
		}
		session.mu.Unlock()
	}
	return expired
}

func shutdownHTTPSessions(sessions []*httpServerSession) {
	for _, session := range sessions {
		session.shutdown()
	}
}

func (t *HTTPServerTransport) checkSessionCapacityLocked(principal, quotaKey string) error {
	if t.closed {
		return errHTTPTransportClosed
	}
	if len(t.sessions) >= t.config.MaxSessions {
		return errHTTPSessionLimit
	}
	if principal != "" && len(t.principalSessions[principal]) >= t.config.MaxSessionsPerPrincipal {
		return errHTTPPrincipalLimit
	}
	if quotaKey != "" && len(t.quotaSessions[quotaKey]) >= t.config.MaxSessionsPerQuotaKey {
		return errHTTPQuotaKeyLimit
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
	once          sync.Once
	transport     *HTTPServerTransport
	session       *httpServerSession
	kind          leaseKind
	ctx           context.Context
	cancel        context.CancelFunc
	stopFresh     func() bool
	stopHTTP      func() bool
	touchActivity bool

	// responseReservationBytes is immutable after admission and accounts for
	// one in-flight response until release. responseReservationPending is
	// protected by session.mu and becomes false when the reservation is
	// atomically converted into an outbox entry.
	responseReservationBytes   int64
	responseReservationPending bool
	responseIDKey              string
}

func (t *HTTPServerTransport) acquireSessionLease(session *httpServerSession, r *http.Request, hook HTTPSessionRequestContextFunc, kind leaseKind, requestIDKey string) (*httpSessionLease, error) {
	reserveResponse := requestIDKey != "" && kind == leaseMessage
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
			if reserveResponse {
				if _, duplicate := session.responseIDs[requestIDKey]; duplicate {
					t.stats.RejectedMessages++
					t.stats.RejectedDuplicateRequestIDs++
					session.mu.Unlock()
					t.mu.Unlock()
					return nil, errHTTPDuplicateID
				}
			}
			if reserveResponse && (len(session.outbox)+session.responseReservedMessages >= t.config.OutboxMaxMessages ||
				t.config.ResponseReservationBytes > t.config.OutboxMaxBytes-session.outboxBytes-session.responseReservedBytes) {
				t.stats.RejectedMessages++
				t.stats.RejectedResponseReservations++
				session.mu.Unlock()
				t.mu.Unlock()
				return nil, errHTTPResponseReserve
			}
			t.stats.InFlightMessages++
			session.inFlightMessages++
			t.messageWG.Add(1)
			if reserveResponse {
				session.responseReservedMessages++
				session.responseReservedBytes += t.config.ResponseReservationBytes
				session.responseIDs[requestIDKey] = struct{}{}
				t.stats.InFlightResponseReservations++
				t.stats.ReservedResponseBytes += t.config.ResponseReservationBytes
			}
		case leaseSSE:
			if session.sseOpen {
				session.mu.Unlock()
				t.mu.Unlock()
				return nil, errHTTPSSEAlreadyOpen
			}
			if t.sseInFlight >= t.config.MaxConcurrentMessages {
				t.stats.RejectedMessages++
				session.mu.Unlock()
				t.mu.Unlock()
				return nil, errHTTPMessageLimit
			}
			session.sseOpen = true
			t.sseInFlight++
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
	}
	session.mu.Unlock()
	t.mu.Unlock()
	if expired {
		session.shutdown()
		return nil, errHTTPSessionInactive
	}

	// Admission is deliberately complete before current-policy work. A
	// saturated/revoked/closing session never reaches a host database hook.
	var responseReservationBytes int64
	if reserveResponse {
		responseReservationBytes = t.config.ResponseReservationBytes
	}
	lease := &httpSessionLease{
		transport:                  t,
		session:                    session,
		kind:                       kind,
		responseReservationBytes:   responseReservationBytes,
		responseReservationPending: reserveResponse,
		responseIDKey:              requestIDKey,
	}
	fresh := r.Context()
	if hook != nil {
		derived, err := hook(session.ctx, r)
		if err != nil {
			lease.release()
			return nil, fmt.Errorf("%w: %w", errHTTPRequestContext, err)
		}
		if derived != nil {
			fresh = derived
		}
	}
	// Only an admitted request whose fresh-policy hook succeeded counts as
	// activity. Repeated revoked/backend-error attempts cannot extend idle TTL.
	session.mu.Lock()
	session.lastActivity = time.Now()
	session.mu.Unlock()
	lease.touchActivity = true

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
	lease.ctx = operationCtx
	if reserveResponse {
		lease.ctx = context.WithValue(operationCtx, reservedResponseWriterContextKey{}, reservedResponseWriter(lease))
	}
	lease.cancel = cancel
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
		if l.kind == leaseMessage {
			defer l.transport.messageWG.Done()
		}
		if l.cancel != nil {
			l.cancel()
		}
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
		if l.responseReservationBytes > 0 {
			if t.stats.InFlightResponseReservations <= 0 || t.stats.ReservedResponseBytes < l.responseReservationBytes {
				session.mu.Unlock()
				t.mu.Unlock()
				panic("mcp: HTTP response reservation accounting underflow")
			}
			t.stats.InFlightResponseReservations--
			t.stats.ReservedResponseBytes -= l.responseReservationBytes
			if l.responseReservationPending {
				if session.responseReservedMessages <= 0 || session.responseReservedBytes < l.responseReservationBytes {
					session.mu.Unlock()
					t.mu.Unlock()
					panic("mcp: HTTP session response reservation accounting underflow")
				}
				session.responseReservedMessages--
				session.responseReservedBytes -= l.responseReservationBytes
				delete(session.responseIDs, l.responseIDKey)
				l.responseReservationPending = false
			}
		}
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
			if t.sseInFlight <= 0 {
				session.mu.Unlock()
				t.mu.Unlock()
				panic("mcp: HTTP SSE accounting underflow")
			}
			session.sseOpen = false
			t.sseInFlight--
		case leaseControl:
			if session.controlInFlight <= 0 || t.controlInFlight <= 0 {
				session.mu.Unlock()
				t.mu.Unlock()
				panic("mcp: HTTP control-lane accounting underflow")
			}
			session.controlInFlight--
			t.controlInFlight--
		}
		if l.touchActivity {
			session.lastActivity = time.Now()
		}
		session.mu.Unlock()
		t.mu.Unlock()
	})
}

func (l *httpSessionLease) writeReservedResponse(data []byte) error {
	if l == nil || l.transport == nil || l.session == nil || l.responseReservationBytes <= 0 {
		return errHTTPResponseReserve
	}
	if int64(len(data)) > l.responseReservationBytes {
		l.transport.mu.Lock()
		l.transport.stats.OversizeResponses++
		l.transport.mu.Unlock()
		return errHTTPResponseTooLarge
	}

	session := l.session
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state == httpSessionClosed || session.closed {
		return errHTTPSessionInactive
	}
	if !l.responseReservationPending {
		return errHTTPResponseReserve
	}
	if session.responseReservedMessages <= 0 || session.responseReservedBytes < l.responseReservationBytes {
		panic("mcp: HTTP reserved response capacity disappeared")
	}
	// The admission reservation guarantees both checks. Keep them explicit so
	// a future accounting change fails closed before allocating a snapshot.
	if len(session.outbox)+session.responseReservedMessages > l.transport.config.OutboxMaxMessages ||
		session.outboxBytes+session.responseReservedBytes > l.transport.config.OutboxMaxBytes {
		return errHTTPOutboxOverflow
	}

	snapshot := append([]byte(nil), data...)
	session.responseReservedMessages--
	session.responseReservedBytes -= l.responseReservationBytes
	l.responseReservationPending = false
	session.nextOutboxSequence++
	session.outbox = append(session.outbox, httpOutboxEntry{
		sequence:   session.nextOutboxSequence,
		data:       snapshot,
		responseID: l.responseIDKey,
	})
	session.outboxBytes += int64(len(snapshot))
	select {
	case session.outboxWake <- struct{}{}:
	default:
	}
	return nil
}

type httpDecodeLease struct {
	once                    sync.Once
	transport               *HTTPServerTransport
	session                 *httpServerSession
	protected               bool
	ctx                     context.Context
	cancel                  context.CancelFunc
	stopSession             func() bool
	stopTransport           func() bool
	stopBody                func() bool
	stopDeadline            func() bool
	controller              *http.ResponseController
	readDeadline            bool
	initializationPrincipal string
	terminalErr             error
}

func (t *HTTPServerTransport) acquireDecode(session *httpServerSession, initializationPrincipal string, w http.ResponseWriter, r *http.Request) (*httpDecodeLease, error) {
	allowProtected := session != nil && session.server.hasPendingRequests()
	var expired bool
	now := time.Now()
	t.mu.Lock()
	if session != nil {
		session.mu.Lock()
	}
	if t.closed {
		if session != nil {
			session.mu.Unlock()
		}
		t.mu.Unlock()
		return nil, errHTTPTransportClosed
	}
	if session != nil && (t.sessions[session.id] != session || session.state != httpSessionActive) {
		session.mu.Unlock()
		t.mu.Unlock()
		return nil, errHTTPSessionInactive
	}
	if session != nil && t.sessionExpiredLocked(session, now) {
		t.detachSessionLocked(session)
		t.stats.ExpiredSessions++
		expired = true
	}
	principalInitAvailable := initializationPrincipal == "" ||
		t.initializingByPrincipal[initializationPrincipal] < t.config.MaxConcurrentMessagesPerSession
	regularAvailable := !expired && principalInitAvailable && t.decodeInFlight < t.config.MaxConcurrentMessages &&
		(session == nil || session.decodeInFlight < t.config.MaxConcurrentMessagesPerSession)
	protectedAvailable := !expired && session != nil && allowProtected &&
		t.protectedDecodeInFlight < t.config.MaxConcurrentMessages &&
		session.protectedDecodeInFlight < httpProtectedDecodesPerSession
	protected := false
	switch {
	case regularAvailable:
		t.decodeInFlight++
		if session == nil && initializationPrincipal != "" {
			t.initializingByPrincipal[initializationPrincipal]++
		}
		if session != nil {
			session.decodeInFlight++
			session.operations++
			session.lastActivity = now
		}
	case protectedAvailable:
		protected = true
		t.protectedDecodeInFlight++
		session.protectedDecodeInFlight++
		session.operations++
		session.lastActivity = now
	case expired:
		// Shutdown after releasing accounting locks below.
	default:
		t.stats.RejectedMessages++
		if session != nil {
			session.mu.Unlock()
		}
		t.mu.Unlock()
		return nil, errHTTPMessageLimit
	}
	if session != nil {
		session.mu.Unlock()
	}
	t.mu.Unlock()
	if expired {
		session.shutdown()
		return nil, errHTTPSessionInactive
	}

	if r.Body == nil {
		r.Body = http.NoBody
	}
	ctx, cancel := context.WithTimeout(r.Context(), t.config.RequestBodyTimeout)
	lease := &httpDecodeLease{
		transport:               t,
		session:                 session,
		protected:               protected,
		ctx:                     ctx,
		cancel:                  cancel,
		controller:              http.NewResponseController(w),
		initializationPrincipal: initializationPrincipal,
	}
	lease.stopTransport = context.AfterFunc(t.sweepCtx, cancel)
	if session != nil {
		lease.stopSession = context.AfterFunc(session.ctx, cancel)
	}
	if deadline, ok := ctx.Deadline(); ok {
		lease.readDeadline = lease.controller.SetReadDeadline(deadline) == nil
	}
	lease.stopDeadline = context.AfterFunc(ctx, func() {
		_ = lease.controller.SetReadDeadline(time.Now())
	})
	// Closing Body is the fallback for custom readers and complements the real
	// connection read deadline. Well-behaved ReadClosers unblock Read on Close.
	lease.stopBody = context.AfterFunc(ctx, func() { _ = r.Body.Close() })
	return lease, nil
}

func (l *httpDecodeLease) release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		l.terminalErr = l.ctx.Err()
		bodyStopped := true
		deadlineStopped := true
		sessionStopped := true
		transportStopped := true
		if l.stopBody != nil {
			bodyStopped = l.stopBody()
		}
		if l.stopDeadline != nil {
			deadlineStopped = l.stopDeadline()
		}
		if l.stopSession != nil {
			sessionStopped = l.stopSession()
		}
		if l.stopTransport != nil {
			transportStopped = l.stopTransport()
		}
		if l.terminalErr == nil && (!bodyStopped || !deadlineStopped || !sessionStopped || !transportStopped) {
			l.terminalErr = context.Canceled
		}
		if l.cancel != nil {
			l.cancel()
		}
		if l.readDeadline && deadlineStopped && l.controller != nil {
			_ = l.controller.SetReadDeadline(time.Time{})
		}

		t := l.transport
		t.mu.Lock()
		if l.session != nil {
			l.session.mu.Lock()
		}
		if l.protected {
			if t.protectedDecodeInFlight <= 0 || l.session == nil || l.session.protectedDecodeInFlight <= 0 {
				if l.session != nil {
					l.session.mu.Unlock()
				}
				t.mu.Unlock()
				panic("mcp: HTTP protected decode accounting underflow")
			}
			t.protectedDecodeInFlight--
			l.session.protectedDecodeInFlight--
		} else {
			if t.decodeInFlight <= 0 || (l.session != nil && l.session.decodeInFlight <= 0) {
				if l.session != nil {
					l.session.mu.Unlock()
				}
				t.mu.Unlock()
				panic("mcp: HTTP decode accounting underflow")
			}
			t.decodeInFlight--
			if l.session == nil && l.initializationPrincipal != "" {
				count := t.initializingByPrincipal[l.initializationPrincipal]
				if count <= 0 {
					t.mu.Unlock()
					panic("mcp: HTTP initialization decode accounting underflow")
				}
				if count == 1 {
					delete(t.initializingByPrincipal, l.initializationPrincipal)
				} else {
					t.initializingByPrincipal[l.initializationPrincipal] = count - 1
				}
			}
			if l.session != nil {
				l.session.decodeInFlight--
			}
		}
		if l.session != nil {
			if l.session.operations <= 0 {
				l.session.mu.Unlock()
				t.mu.Unlock()
				panic("mcp: HTTP decode operation accounting underflow")
			}
			l.session.operations--
			l.session.lastActivity = time.Now()
			l.session.mu.Unlock()
		}
		t.mu.Unlock()
	})
	return l.terminalErr
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
	// A message that would fit the physical outbox but not its unreserved
	// remainder is rejected without closing the session. This is expected when
	// a nested sampling request is larger than the space left beside its
	// handler's final response; the handler can still return a small error using
	// the capacity admitted for it.
	physicalOverflow := len(session.outbox) >= t.config.OutboxMaxMessages ||
		int64(len(data)) > t.config.OutboxMaxBytes-session.outboxBytes
	reservedOverflow := len(session.outbox)+session.responseReservedMessages >= t.config.OutboxMaxMessages ||
		int64(len(data)) > t.config.OutboxMaxBytes-session.outboxBytes-session.responseReservedBytes
	if !physicalOverflow && reservedOverflow {
		session.mu.Unlock()
		t.recordRejectedOutboxWrite()
		return errHTTPResponseReserve
	}
	if physicalOverflow {
		session.mu.Unlock()
		t.recordRejectedOutboxWrite()
		return errHTTPOutboxOverflow
	}
	// Copy only after capacity admission. Oversize model/tool output is already
	// present in the caller, but must not trigger an additional unbounded copy.
	snapshot := append([]byte(nil), data...)
	session.nextOutboxSequence++
	session.outbox = append(session.outbox, httpOutboxEntry{
		sequence: session.nextOutboxSequence,
		data:     snapshot,
	})
	session.outboxBytes += int64(len(snapshot))
	select {
	case session.outboxWake <- struct{}{}:
	default:
	}
	session.mu.Unlock()
	return nil
}

func (t *HTTPServerTransport) recordRejectedOutboxWrite() {
	t.mu.Lock()
	t.stats.RejectedOutboxWrites++
	t.mu.Unlock()
}

func (s *httpServerSession) peekOutbox() (uint64, []byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.outbox) == 0 {
		return 0, nil, false
	}
	entry := s.outbox[0]
	return entry.sequence, entry.data, true
}

func (s *httpServerSession) ackOutbox(sequence uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.outbox) == 0 || s.outbox[0].sequence != sequence {
		return false
	}
	entry := s.outbox[0]
	s.outbox[0] = httpOutboxEntry{}
	s.outbox = s.outbox[1:]
	s.outboxBytes -= int64(len(entry.data))
	if entry.responseID != "" {
		delete(s.responseIDs, entry.responseID)
	}
	if len(s.outbox) == 0 {
		s.outbox = nil
	}
	return true
}

// dequeue is a package-test helper that models a successful immediate write.
// Production SSE delivery keeps the entry and request ID until write succeeds.
func (s *httpServerSession) dequeue() ([]byte, bool) {
	sequence, payload, ok := s.peekOutbox()
	if !ok || !s.ackOutbox(sequence) {
		return nil, false
	}
	return payload, true
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
	if session.quotaKey != "" {
		if indexed := t.quotaSessions[session.quotaKey]; indexed != nil {
			delete(indexed, session.id)
			if len(indexed) == 0 {
				delete(t.quotaSessions, session.quotaKey)
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
		session.outbox[i] = httpOutboxEntry{}
	}
	session.outbox = nil
	session.outboxBytes = 0
	clear(session.responseIDs)
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
	t.mu.Lock()
	expired := t.reapExpiredSessionsLocked(now)
	validator := t.principalValidator
	principals := make([]string, 0, len(t.principalSessions))
	if validator != nil {
		for principal := range t.principalSessions {
			principals = append(principals, principal)
		}
	}
	t.mu.Unlock()

	shutdownHTTPSessions(expired)
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

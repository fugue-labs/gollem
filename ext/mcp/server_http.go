package mcp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

const (
	httpSessionIDBytes          = 32
	httpSessionCollisionRetries = 8
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

// HTTPServerTransport serves MCP over the streamable HTTP transport.
// Each MCP session gets its own cloned Server instance.
type HTTPServerTransport struct {
	mu               sync.Mutex
	template         *Server
	sessions         map[string]*httpServerSession
	sessionCtx       func(*http.Request) context.Context
	sessionAuth      HTTPSessionAuthorizer
	sessionIDEntropy io.Reader
	entropyMu        sync.Mutex
}

// SetSessionContextFunc installs a hook that derives each new MCP
// session's base context from the HTTP request that initialized it.
// This is how hosts thread per-connection identity (a workspace
// resolved from the Authorization header, say) through to tool
// handlers, whose ctx is the session context.
//
// The returned context must NOT be (or derive its cancellation from)
// the request context — sessions outlive their initializing request.
// Carry values only, e.g.:
//
//	t.SetSessionContextFunc(func(r *http.Request) context.Context {
//		ws := resolveWorkspace(r.Header.Get("Authorization"))
//		return context.WithValue(context.Background(), workspaceKey{}, ws)
//	})
//
// A nil hook (the default) and a nil return both mean
// context.Background().
func (t *HTTPServerTransport) SetSessionContextFunc(f func(*http.Request) context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionCtx = f
}

// SetSessionAuthorizer installs the authorization hook applied to every
// follow-up GET, POST, and DELETE request. The hook is optional so transports
// protected by a single shared HTTP credential remain backward compatible;
// multi-principal hosts should always install one.
func (t *HTTPServerTransport) SetSessionAuthorizer(f HTTPSessionAuthorizer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionAuth = f
}

type httpServerSession struct {
	mu      sync.Mutex
	id      string
	server  *Server
	ctx     context.Context
	cancel  context.CancelFunc
	outbox  chan []byte
	backlog [][]byte
	closed  bool
}

// NewHTTPServerTransport binds a reusable Server template to an HTTP transport.
func NewHTTPServerTransport(server *Server) *HTTPServerTransport {
	return newHTTPServerTransport(server, rand.Reader)
}

// newHTTPServerTransport keeps entropy injection package-private so production
// callers cannot accidentally weaken session IDs while collision/failure tests
// can exercise the exact construction path deterministically.
func newHTTPServerTransport(server *Server, entropy io.Reader) *HTTPServerTransport {
	if server == nil {
		server = NewServer()
	}
	return &HTTPServerTransport{
		template:         server,
		sessions:         make(map[string]*httpServerSession),
		sessionIDEntropy: entropy,
	}
}

// Run blocks until ctx is cancelled, then closes all active sessions.
func (t *HTTPServerTransport) Run(ctx context.Context) error {
	<-ctx.Done()
	_ = t.Close()
	return ctx.Err()
}

// Close shuts down all active HTTP MCP sessions.
func (t *HTTPServerTransport) Close() error {
	t.mu.Lock()
	sessions := make([]*httpServerSession, 0, len(t.sessions))
	for _, session := range t.sessions {
		sessions = append(sessions, session)
	}
	t.sessions = make(map[string]*httpServerSession)
	t.mu.Unlock()

	for _, session := range sessions {
		session.close()
	}
	return nil
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
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (t *HTTPServerTransport) handleStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
		return
	}

	session, ok := t.authorizedSession(w, r, sessionID)
	if !ok {
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Mcp-Session-Id", sessionID)

	for _, payload := range session.drainBacklog() {
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
		flusher.Flush()
	}

	for {
		select {
		case payload, ok := <-session.outbox:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-session.ctx.Done():
			return
		}
	}
}

func (t *HTTPServerTransport) handlePost(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	var session *httpServerSession
	if sessionID != "" {
		var ok bool
		session, ok = t.authorizedSession(w, r, sessionID)
		if !ok {
			return
		}
	}

	var msg jsonRPCMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if msg.Method == "initialize" && sessionID == "" {
		var err error
		session, err = t.newSession(r)
		if err != nil {
			http.Error(w, "failed to create session", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", session.id)
		w.Header().Set("Cache-Control", "no-store")

		result, rpcErr := session.server.handleInitialize(msg.Params)
		payload, err := json.Marshal(jsonRPCMessage{
			JSONRPC: "2.0",
			ID:      rawJSONID(normalizeID(msg.ID)),
			Result:  mustRawResult(result, rpcErr == nil),
			Error:   rpcErr,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(payload)
		return
	}

	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
		return
	}

	w.Header().Set("Mcp-Session-Id", sessionID)

	go session.server.HandleMessage(session.ctx, &msg)
	w.WriteHeader(http.StatusAccepted)
}

func (t *HTTPServerTransport) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
		return
	}

	session, ok := t.authorizedSession(w, r, sessionID)
	if !ok {
		return
	}
	session, ok = t.deleteSession(sessionID, session)
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	session.close()
	w.WriteHeader(http.StatusNoContent)
}

func (t *HTTPServerTransport) newSession(r *http.Request) (*httpServerSession, error) {
	base := context.Background()
	t.mu.Lock()
	hook := t.sessionCtx
	t.mu.Unlock()
	if hook != nil {
		if derived := hook(r); derived != nil {
			base = derived
		}
	}
	for attempt := 0; attempt < httpSessionCollisionRetries; attempt++ {
		sessionID, err := t.generateHTTPSessionID()
		if err != nil {
			return nil, err
		}

		// Hold the session lock while checking the generated identifier and
		// publishing the fully constructed session. This reserves the ID before
		// any server clone/allocation and prevents concurrent creators from
		// racing between a preflight check and insertion.
		t.mu.Lock()
		if _, exists := t.sessions[sessionID]; exists {
			t.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(base)
		server := cloneServerTemplate(t.template)
		session := &httpServerSession{
			id:     sessionID,
			server: server,
			ctx:    ctx,
			cancel: cancel,
			outbox: make(chan []byte, 256),
		}
		server.attachWriter(func(data []byte) error {
			session.enqueue(data)
			return nil
		})

		t.sessions[sessionID] = session
		t.mu.Unlock()
		return session, nil
	}
	return nil, fmt.Errorf("mcp: exhausted %d HTTP session ID collision attempts", httpSessionCollisionRetries)
}

func (t *HTTPServerTransport) getSession(id string) (*httpServerSession, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	session, ok := t.sessions[id]
	return session, ok
}

// authorizedSession retrieves a session and authorizes the current request
// before the caller can observe or mutate any session-owned state.
func (t *HTTPServerTransport) authorizedSession(w http.ResponseWriter, r *http.Request, id string) (*httpServerSession, bool) {
	t.mu.Lock()
	session, ok := t.sessions[id]
	authorize := t.sessionAuth
	t.mu.Unlock()
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return nil, false
	}
	if authorize == nil {
		return session, true
	}
	authorized, err := authorize(session.ctx, r)
	if err != nil {
		http.Error(w, "session authorization failed", http.StatusInternalServerError)
		return nil, false
	}
	if !authorized {
		http.Error(w, "session not authorized", http.StatusForbidden)
		return nil, false
	}
	return session, true
}

func (t *HTTPServerTransport) deleteSession(id string, expected *httpServerSession) (*httpServerSession, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	session, ok := t.sessions[id]
	if ok && session == expected {
		delete(t.sessions, id)
		return session, true
	}
	return nil, false
}

func (s *httpServerSession) enqueue(data []byte) {
	snapshot := append([]byte(nil), data...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.outbox <- snapshot:
	default:
		s.backlog = append(s.backlog, snapshot)
	}
}

func (s *httpServerSession) drainBacklog() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.backlog) == 0 {
		return nil
	}
	items := append([][]byte(nil), s.backlog...)
	s.backlog = nil
	return items
}

func (s *httpServerSession) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.outbox)
	s.mu.Unlock()
	s.cancel()
	_ = s.server.Close()
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

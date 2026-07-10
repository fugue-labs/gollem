package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// ServerToolHandler handles an MCP tools/call request.
type ServerToolHandler func(context.Context, *RequestContext, map[string]any) (*ToolResult, error)

// ResourceReadHandler handles an MCP resources/read request.
type ResourceReadHandler func(context.Context, *RequestContext, string) (*ReadResourceResult, error)

// PromptGetHandler handles an MCP prompts/get request.
type PromptGetHandler func(context.Context, *RequestContext, string, map[string]string) (*PromptResult, error)

type serverTool struct {
	definition Tool
	handler    ServerToolHandler
}

// RequestContext represents a single client request being serviced by an MCP server.
// Nested client requests such as sampling/createMessage or roots/list must flow through it.
type RequestContext struct {
	server *Server
}

// reservedResponseWriter is installed only by bounded transports that admit
// response capacity before dispatch. Keeping it in the request context makes
// nested server-to-client calls use the ordinary writer while the one final
// response atomically consumes the capacity reserved for it.
type reservedResponseWriter interface {
	writeReservedResponse([]byte) error
}

type reservedResponseWriterContextKey struct{}

// ClientCapabilities returns the capabilities advertised by the connected client.
func (rc *RequestContext) ClientCapabilities() ClientCapabilities {
	if rc == nil || rc.server == nil {
		return ClientCapabilities{}
	}
	return rc.server.ClientCapabilities()
}

// ClientInfo returns the client identity observed during initialize.
func (rc *RequestContext) ClientInfo() *ImplementationInfo {
	if rc == nil || rc.server == nil {
		return nil
	}
	return rc.server.ClientInfo()
}

// ListRoots requests the current roots from the connected client.
func (rc *RequestContext) ListRoots(ctx context.Context) (*ListRootsResult, error) {
	if rc == nil || rc.server == nil {
		return nil, errors.New("mcp: request context is not attached to a server")
	}
	return rc.server.listRoots(ctx)
}

// CreateMessage requests client-side sampling from the connected client.
func (rc *RequestContext) CreateMessage(ctx context.Context, params *CreateMessageParams) (*CreateMessageResult, error) {
	if rc == nil || rc.server == nil {
		return nil, errors.New("mcp: request context is not attached to a server")
	}
	return rc.server.createMessage(ctx, params)
}

// CreateElicitation requests client-side elicitation from the connected client.
func (rc *RequestContext) CreateElicitation(ctx context.Context, params *ElicitationParams) (*ElicitationResult, error) {
	if rc == nil || rc.server == nil {
		return nil, errors.New("mcp: request context is not attached to a server")
	}
	return rc.server.createElicitation(ctx, params)
}

// Server is a single-session MCP server with tool/resource/prompt registries
// and support for nested client requests such as sampling and roots/list.
type Server struct {
	mu sync.Mutex

	// activeHandlers and idleCh replace sync.WaitGroup for request-handler
	// lifecycle accounting. A WaitGroup cannot safely race Add with Wait when
	// the count may be zero; HTTP shutdown and session deletion do exactly that.
	// Both fields are protected by mu. idleCh is closed whenever the count is
	// zero and replaced with an open channel on the zero-to-one transition.
	activeHandlers int
	idleCh         chan struct{}

	nextID  atomic.Int64
	pending map[int64]chan *jsonRPCMessage

	writeMu sync.Mutex
	writeFn func([]byte) error
	closed  bool
	peerEOF bool

	tools             []serverTool
	resources         []Resource
	resourceTemplates []ResourceTemplate
	resourceReader    ResourceReadHandler
	prompts           []Prompt
	promptGetter      PromptGetHandler

	serverInfo   ServerInfo
	instructions string
	protocol     string
	clientInfo   *ImplementationInfo
	clientCaps   ClientCapabilities
	clientReady  bool
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithServerInfo sets the server identity returned during initialize.
func WithServerInfo(info ServerInfo) ServerOption {
	return func(s *Server) {
		s.serverInfo = info
	}
}

// WithServerInstructions sets initialize.instructions.
func WithServerInstructions(instructions string) ServerOption {
	return func(s *Server) {
		s.instructions = instructions
	}
}

// NewServer constructs a reusable single-session MCP server.
func NewServer(opts ...ServerOption) *Server {
	idleCh := make(chan struct{})
	close(idleCh)
	s := &Server{
		pending: make(map[int64]chan *jsonRPCMessage),
		idleCh:  idleCh,
		serverInfo: ServerInfo{
			Name:    "gollem-mcp-server",
			Version: "1.0.0",
		},
		protocol: ProtocolVersion,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// AddTool registers or replaces a server tool.
func (s *Server) AddTool(tool Tool, handler ServerToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tools {
		if s.tools[i].definition.Name == tool.Name {
			s.tools[i] = serverTool{definition: tool, handler: handler}
			return
		}
	}
	s.tools = append(s.tools, serverTool{definition: tool, handler: handler})
}

// SetResources configures server resources and the optional read handler.
func (s *Server) SetResources(resources []Resource, templates []ResourceTemplate, reader ResourceReadHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources = append([]Resource(nil), resources...)
	s.resourceTemplates = append([]ResourceTemplate(nil), templates...)
	s.resourceReader = reader
}

// SetPrompts configures server prompts and the optional prompts/get handler.
func (s *Server) SetPrompts(prompts []Prompt, getter PromptGetHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append([]Prompt(nil), prompts...)
	s.promptGetter = getter
}

// ClientCapabilities returns the capabilities advertised by the connected client.
func (s *Server) ClientCapabilities() ClientCapabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneClientCapabilities(s.clientCaps)
}

// ClientInfo returns the client identity observed during initialize.
func (s *Server) ClientInfo() *ImplementationInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clientInfo == nil {
		return nil
	}
	info := *s.clientInfo
	return &info
}

// ProtocolVersion returns the negotiated protocol version for the current session.
func (s *Server) ProtocolVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.protocol == "" {
		return ProtocolVersion
	}
	return s.protocol
}

// NotifyToolsListChanged emits notifications/tools/list_changed.
func (s *Server) NotifyToolsListChanged(ctx context.Context) error {
	return s.notify(ctx, "notifications/tools/list_changed", nil)
}

// NotifyResourcesListChanged emits notifications/resources/list_changed.
func (s *Server) NotifyResourcesListChanged(ctx context.Context) error {
	return s.notify(ctx, "notifications/resources/list_changed", nil)
}

// NotifyPromptsListChanged emits notifications/prompts/list_changed.
func (s *Server) NotifyPromptsListChanged(ctx context.Context) error {
	return s.notify(ctx, "notifications/prompts/list_changed", nil)
}

// HandleMessage handles a single JSON-RPC message. Request dispatch is asynchronous
// so nested client requests can complete while the read loop continues.
func (s *Server) HandleMessage(ctx context.Context, msg *jsonRPCMessage) {
	s.handleMessageWithCompletion(ctx, msg, nil)
}

// handleMessageWithCompletion is the transport-facing form of HandleMessage.
// complete is invoked exactly once after the message has been fully handled,
// including asynchronous request handlers. This lets bounded transports hold
// an admission lease for the real handler lifetime instead of merely for
// dispatch.
func (s *Server) handleMessageWithCompletion(ctx context.Context, msg *jsonRPCMessage, complete func()) {
	finish := complete
	if finish == nil {
		finish = func() {}
	}
	if msg == nil {
		finish()
		return
	}
	if msg.Method != "" {
		if hasJSONRPCID(msg.ID) {
			if !s.beginHandler() {
				finish()
				return
			}
			go func() {
				defer finish()
				defer s.endHandler()
				s.handleRequest(ctx, msg)
			}()
			return
		}
		s.handleNotification(msg)
		finish()
		return
	}
	if !hasJSONRPCID(msg.ID) {
		finish()
		return
	}
	id, err := parsePendingID(msg.ID)
	if err != nil {
		finish()
		return
	}
	s.mu.Lock()
	ch, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	s.mu.Unlock()
	if ok {
		ch <- msg
	}
	finish()
}

// hasPendingResponse reports whether raw identifies a nested request currently
// outstanding on this single-session server. HTTP transports use it to admit
// only genuine response continuations to their bounded control lane.
func (s *Server) hasPendingResponse(raw *json.RawMessage) bool {
	id, err := parsePendingID(raw)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pending[id]
	return ok
}

func (s *Server) hasPendingRequests() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) > 0
}

// deliverPendingResponse atomically claims and delivers a nested response. It
// returns false for malformed, canceled, duplicate, or cross-session IDs.
func (s *Server) deliverPendingResponse(msg *jsonRPCMessage) bool {
	if msg == nil || msg.Method != "" || !hasJSONRPCID(msg.ID) {
		return false
	}
	id, err := parsePendingID(msg.ID)
	if err != nil {
		return false
	}
	s.mu.Lock()
	ch, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	ch <- msg
	return true
}

func (s *Server) beginHandler() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if s.activeHandlers == 0 {
		s.idleCh = make(chan struct{})
	}
	s.activeHandlers++
	return true
}

func (s *Server) endHandler() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeHandlers <= 0 {
		panic("mcp: server handler accounting underflow")
	}
	s.activeHandlers--
	if s.activeHandlers == 0 {
		close(s.idleCh)
	}
}

func (s *Server) handleNotification(msg *jsonRPCMessage) {
	if msg.Method == "notifications/initialized" {
		s.mu.Lock()
		s.clientReady = true
		s.mu.Unlock()
	}
}

func (s *Server) handleRequest(ctx context.Context, msg *jsonRPCMessage) {
	requestID := responseID(msg.ID)

	switch msg.Method {
	case "initialize":
		result, rpcErr := s.handleInitialize(msg.Params)
		_ = s.respond(ctx, requestID, result, rpcErr)
	case "tools/list":
		_ = s.respond(ctx, requestID, s.handleToolsList(), nil)
	case "tools/call":
		result, rpcErr := s.handleToolsCall(ctx, msg.Params)
		_ = s.respond(ctx, requestID, result, rpcErr)
	case "resources/list":
		_ = s.respond(ctx, requestID, s.handleResourcesList(), nil)
	case "resources/read":
		result, rpcErr := s.handleResourcesRead(ctx, msg.Params)
		_ = s.respond(ctx, requestID, result, rpcErr)
	case "resources/templates/list":
		_ = s.respond(ctx, requestID, s.handleResourceTemplatesList(), nil)
	case "prompts/list":
		_ = s.respond(ctx, requestID, s.handlePromptsList(), nil)
	case "prompts/get":
		result, rpcErr := s.handlePromptGet(ctx, msg.Params)
		_ = s.respond(ctx, requestID, result, rpcErr)
	default:
		_ = s.respond(ctx, requestID, nil, &jsonRPCError{
			Code:    jsonRPCCodeMethodNotFound,
			Message: "method not found: " + msg.Method,
		})
	}
}

func (s *Server) handleInitialize(raw json.RawMessage) (*InitializeResult, *jsonRPCError) {
	var params InitializeParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, &jsonRPCError{
				Code:    jsonRPCCodeInvalidParams,
				Message: fmt.Sprintf("invalid initialize params: %v", err),
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.protocol = ProtocolVersion
	if params.ProtocolVersion != "" {
		s.protocol = ProtocolVersion
	}
	s.clientCaps = cloneClientCapabilities(params.Capabilities)
	info := params.ClientInfo
	s.clientInfo = &info

	result := &InitializeResult{
		ProtocolVersion: s.protocol,
		Capabilities:    s.serverCapabilitiesLocked(),
		ServerInfo:      cloneServerInfo(s.serverInfo),
		Instructions:    s.instructions,
	}
	return result, nil
}

func (s *Server) handleToolsList() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	tools := make([]Tool, 0, len(s.tools))
	for _, tool := range s.tools {
		tools = append(tools, tool.definition)
	}
	return map[string]any{"tools": tools}
}

func (s *Server) handleToolsCall(ctx context.Context, raw json.RawMessage) (any, *jsonRPCError) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &jsonRPCError{
			Code:    jsonRPCCodeInvalidParams,
			Message: fmt.Sprintf("invalid tools/call params: %v", err),
		}
	}

	s.mu.Lock()
	var entry *serverTool
	for i := range s.tools {
		if s.tools[i].definition.Name == params.Name {
			tool := s.tools[i]
			entry = &tool
			break
		}
	}
	s.mu.Unlock()

	if entry == nil || entry.handler == nil {
		return nil, &jsonRPCError{
			Code:    jsonRPCCodeMethodNotFound,
			Message: "unknown tool: " + params.Name,
		}
	}

	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}

	result, err := entry.handler(ctx, &RequestContext{server: s}, params.Arguments)
	if err != nil {
		return nil, rpcErrorFromError(err)
	}
	if result == nil {
		return &ToolResult{Content: []Content{{Type: "text", Text: ""}}}, nil
	}
	return result, nil
}

func (s *Server) handleResourcesList() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"resources": append([]Resource(nil), s.resources...)}
}

func (s *Server) handleResourcesRead(ctx context.Context, raw json.RawMessage) (any, *jsonRPCError) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &jsonRPCError{
			Code:    jsonRPCCodeInvalidParams,
			Message: fmt.Sprintf("invalid resources/read params: %v", err),
		}
	}

	s.mu.Lock()
	reader := s.resourceReader
	s.mu.Unlock()
	if reader == nil {
		return nil, &jsonRPCError{
			Code:    jsonRPCCodeMethodNotFound,
			Message: "resources/read not supported",
		}
	}

	result, err := reader(ctx, &RequestContext{server: s}, params.URI)
	if err != nil {
		return nil, rpcErrorFromError(err)
	}
	return result, nil
}

func (s *Server) handleResourceTemplatesList() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"resourceTemplates": append([]ResourceTemplate(nil), s.resourceTemplates...)}
}

func (s *Server) handlePromptsList() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"prompts": append([]Prompt(nil), s.prompts...)}
}

func (s *Server) handlePromptGet(ctx context.Context, raw json.RawMessage) (any, *jsonRPCError) {
	var params struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &jsonRPCError{
			Code:    jsonRPCCodeInvalidParams,
			Message: fmt.Sprintf("invalid prompts/get params: %v", err),
		}
	}

	s.mu.Lock()
	getter := s.promptGetter
	s.mu.Unlock()
	if getter == nil {
		return nil, &jsonRPCError{
			Code:    jsonRPCCodeMethodNotFound,
			Message: "prompts/get not supported",
		}
	}

	result, err := getter(ctx, &RequestContext{server: s}, params.Name, params.Arguments)
	if err != nil {
		return nil, rpcErrorFromError(err)
	}
	return result, nil
}

func (s *Server) prepareCall() (int64, chan *jsonRPCMessage, error) {
	id := s.nextID.Add(1)
	ch := make(chan *jsonRPCMessage, 1)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, nil, errors.New("mcp: server is closed")
	}
	if s.peerEOF {
		return 0, nil, errors.New("mcp: connection closed")
	}
	s.pending[id] = ch
	return id, ch, nil
}

func (s *Server) removePending(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
}

func (s *Server) awaitResponse(ctx context.Context, id int64, ch chan *jsonRPCMessage) (json.RawMessage, error) {
	select {
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return nil, errors.New("mcp: connection closed while waiting for response")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		s.removePending(id)
		return nil, ctx.Err()
	}
}

func (s *Server) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id, ch, err := s.prepareCall()
	if err != nil {
		return nil, err
	}

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		s.removePending(id)
		return nil, err
	}
	if err := s.writeJSON(data); err != nil {
		s.removePending(id)
		return nil, err
	}
	return s.awaitResponse(ctx, id, ch)
}

func (s *Server) notify(ctx context.Context, method string, params any) error {
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return s.writeJSON(data)
}

func (s *Server) respond(ctx context.Context, id any, result any, rpcErr *jsonRPCError) error {
	var reservedWriter reservedResponseWriter
	if ctx != nil {
		reservedWriter, _ = ctx.Value(reservedResponseWriterContextKey{}).(reservedResponseWriter)
	}
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
		Error:   rpcErr,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		if reservedWriter != nil {
			return s.writeReservedProtocolError(reservedWriter, id, -32003, "failed to encode response")
		}
		return err
	}
	if reservedWriter != nil {
		err = s.writeReservedJSON(reservedWriter, data)
		if !errors.Is(err, errHTTPResponseTooLarge) {
			return err
		}
		// A response larger than the admitted reservation is an application
		// bug, but it must not close the session or silently strand the
		// caller. Replace it with a small, source-free protocol error while
		// leaving the reservation available for this retry.
		return s.writeReservedProtocolError(reservedWriter, id, -32002, "encoded response exceeds configured byte limit")
	}
	return s.writeJSON(data)
}

func (s *Server) writeReservedProtocolError(writer reservedResponseWriter, id any, code int, message string) error {
	fallback, err := json.Marshal(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonRPCError{
			Code:    code,
			Message: message,
		},
	})
	if err != nil {
		return err
	}
	return s.writeReservedJSON(writer, fallback)
}

func (s *Server) writeReservedJSON(writer reservedResponseWriter, data []byte) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errors.New("mcp: server is closed")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return writer.writeReservedResponse(data)
}

func (s *Server) writeJSON(data []byte) error {
	s.mu.Lock()
	writeFn := s.writeFn
	closed := s.closed
	s.mu.Unlock()

	if closed {
		return errors.New("mcp: server is closed")
	}
	if writeFn == nil {
		return errors.New("mcp: no active transport writer")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return writeFn(data)
}

func (s *Server) listRoots(ctx context.Context) (*ListRootsResult, error) {
	caps := s.ClientCapabilities()
	if caps.Roots == nil {
		return nil, errors.New("mcp: client does not advertise roots support")
	}
	result, err := s.call(ctx, "roots/list", nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: roots/list failed: %w", err)
	}
	var roots ListRootsResult
	if err := json.Unmarshal(result, &roots); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse roots/list result: %w", err)
	}
	return &roots, nil
}

func (s *Server) createMessage(ctx context.Context, params *CreateMessageParams) (*CreateMessageResult, error) {
	if params == nil {
		return nil, errors.New("mcp: nil sampling params")
	}
	caps := s.ClientCapabilities()
	if caps.Sampling == nil {
		return nil, errors.New("mcp: client does not advertise sampling support")
	}
	if len(params.Tools) > 0 && caps.Sampling.Tools == nil {
		return nil, errors.New("mcp: client does not advertise sampling.tools support")
	}
	if params.IncludeContext != "" && params.IncludeContext != "none" && caps.Sampling.Context == nil {
		return nil, errors.New("mcp: client does not advertise sampling.context support")
	}

	result, err := s.call(ctx, "sampling/createMessage", params)
	if err != nil {
		return nil, fmt.Errorf("mcp: sampling/createMessage failed: %w", err)
	}
	var sampled CreateMessageResult
	if err := json.Unmarshal(result, &sampled); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse sampling/createMessage result: %w", err)
	}
	return &sampled, nil
}

func (s *Server) createElicitation(ctx context.Context, params *ElicitationParams) (*ElicitationResult, error) {
	if params == nil {
		return nil, errors.New("mcp: nil elicitation params")
	}
	caps := s.ClientCapabilities()
	if caps.Elicitation == nil {
		return nil, errors.New("mcp: client does not advertise elicitation support")
	}
	mode := params.Mode
	if mode == "" {
		mode = "form"
	}
	if !supportsElicitationMode(caps.Elicitation, mode) {
		return nil, fmt.Errorf("mcp: client does not advertise elicitation.%s support", mode)
	}

	result, err := s.call(ctx, "elicitation/create", params)
	if err != nil {
		return nil, fmt.Errorf("mcp: elicitation/create failed: %w", err)
	}
	var elicited ElicitationResult
	if err := json.Unmarshal(result, &elicited); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse elicitation/create result: %w", err)
	}
	return &elicited, nil
}

// Close marks the current server session closed and fails pending nested requests.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.peerEOF = true
	s.failPendingLocked()
	return nil
}

// WaitIdle waits for all in-flight request handlers to finish.
func (s *Server) WaitIdle() {
	_ = s.WaitIdleContext(context.Background())
}

// WaitIdleContext waits for all request handlers that are currently in flight
// to finish, or returns ctx.Err(). Call Close before waiting when shutdown must
// prevent later handler admission.
func (s *Server) WaitIdleContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		s.mu.Lock()
		if s.activeHandlers == 0 {
			s.mu.Unlock()
			return nil
		}
		idle := s.idleCh
		s.mu.Unlock()

		select {
		case <-idle:
			// Re-check under the mutex. This also makes the method correct when
			// used before Close and a new handler arrives at the transition.
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Server) attachWriter(writeFn func([]byte) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeFn = writeFn
	s.closed = false
	s.peerEOF = false
}

func (s *Server) markPeerClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peerEOF = true
	s.failPendingLocked()
}

func (s *Server) failPendingLocked() {
	for id, ch := range s.pending {
		close(ch)
		delete(s.pending, id)
	}
}

func (s *Server) serverCapabilitiesLocked() ServerCapabilities {
	var caps ServerCapabilities
	if len(s.tools) > 0 {
		caps.Tools = &ToolCapabilities{}
	}
	if len(s.resources) > 0 || len(s.resourceTemplates) > 0 || s.resourceReader != nil {
		caps.Resources = &ResourceCapabilities{}
	}
	if len(s.prompts) > 0 || s.promptGetter != nil {
		caps.Prompts = &PromptCapabilities{}
	}
	return caps
}

func cloneServerInfo(info ServerInfo) *ServerInfo {
	cloned := info
	return &cloned
}

func supportsElicitationMode(cap *ElicitationCapability, mode string) bool {
	if cap == nil {
		return false
	}
	switch mode {
	case "", "form":
		return cap.Form != nil || cap.URL == nil
	case "url":
		return cap.URL != nil
	default:
		return false
	}
}

// ServerTransport is the minimal interface implemented by server transports.
type ServerTransport interface {
	io.Closer
	Run(context.Context) error
}

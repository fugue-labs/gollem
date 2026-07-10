package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
)

const (
	jsonRPCCodeParseError     = -32700
	jsonRPCCodeInvalidRequest = -32600
	jsonRPCCodeMethodNotFound = -32601
	jsonRPCCodeInvalidParams  = -32602
	jsonRPCCodeInternalError  = -32603
)

// Notification is a server-initiated JSON-RPC notification.
type Notification struct {
	Method string
	Params json.RawMessage
}

// NotificationHandler handles a server notification.
type NotificationHandler func(Notification)

// RequestHandler handles a server-initiated JSON-RPC request.
type RequestHandler func(context.Context, json.RawMessage) (any, error)

// RootsProvider returns the current set of roots exposed to an MCP server.
type RootsProvider func(context.Context) ([]Root, error)

// SamplingHandler handles sampling/createMessage requests from an MCP server.
// Optional sampling surfaces such as tools/context must be advertised via
// ClientConfig.Capabilities.
type SamplingHandler func(context.Context, *CreateMessageParams) (*CreateMessageResult, error)

// ElicitationHandler handles elicitation/create requests from an MCP server.
type ElicitationHandler func(context.Context, *ElicitationParams) (*ElicitationResult, error)

// ClientConfig configures client-side MCP capabilities exposed to servers.
type ClientConfig struct {
	ClientInfo         *ImplementationInfo
	Capabilities       ClientCapabilities
	RootsProvider      RootsProvider
	SamplingHandler    SamplingHandler
	ElicitationHandler ElicitationHandler
}

// StaticRoots returns a provider that always returns the same roots.
func StaticRoots(roots ...Root) RootsProvider {
	copied := append([]Root(nil), roots...)
	return func(context.Context) ([]Root, error) {
		return append([]Root(nil), copied...), nil
	}
}

func defaultClientConfig() ClientConfig {
	return ClientConfig{
		ClientInfo: &ImplementationInfo{
			Name:    clientName,
			Version: clientVersion,
		},
	}
}

// jsonRPCRequest is a JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonRPCResponse is a JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

// jsonRPCMessage is a generic JSON-RPC 2.0 message.
type jsonRPCMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *jsonRPCError    `json:"error,omitempty"`
}

// jsonRPCError is a JSON-RPC 2.0 error.
type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *jsonRPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

type rpcCall func(context.Context, string, any) (json.RawMessage, error)
type rpcNotify func(context.Context, string, any) error
type rpcRespond func(context.Context, any, any, *jsonRPCError) error

var samplingCapabilityRegistry sync.Map

type clientState struct {
	mu                   sync.Mutex
	nextID               atomic.Int64
	pending              map[int64]chan *jsonRPCMessage
	notificationHandlers map[string]map[int64]NotificationHandler
	requestHandlers      map[string]RequestHandler
	nextHandlerID        int64
	closed               bool
	protocolVersion      string
	capabilities         ServerCapabilities
	serverInfo           *ServerInfo
	instructions         string
	clientInfo           ImplementationInfo
	clientCapabilities   ClientCapabilities
}

func newClientState(configs ...ClientConfig) *clientState {
	cfg := defaultClientConfig()
	if len(configs) > 0 {
		override := configs[0]
		if override.ClientInfo != nil {
			info := *override.ClientInfo
			cfg.ClientInfo = &info
		}
		cfg.Capabilities = override.Capabilities
		cfg.RootsProvider = override.RootsProvider
		cfg.SamplingHandler = override.SamplingHandler
		cfg.ElicitationHandler = override.ElicitationHandler
	}

	state := &clientState{
		pending:              make(map[int64]chan *jsonRPCMessage),
		notificationHandlers: make(map[string]map[int64]NotificationHandler),
		requestHandlers:      make(map[string]RequestHandler),
		protocolVersion:      protocolVersion,
	}

	if cfg.ClientInfo != nil {
		state.clientInfo = *cfg.ClientInfo
	}
	state.clientCapabilities = resolveClientCapabilities(cfg)

	if cfg.RootsProvider != nil {
		state.requestHandlers["roots/list"] = func(ctx context.Context, _ json.RawMessage) (any, error) {
			roots, err := cfg.RootsProvider(ctx)
			if err != nil {
				return nil, &jsonRPCError{
					Code:    jsonRPCCodeInternalError,
					Message: fmt.Sprintf("mcp: roots provider failed: %v", err),
				}
			}
			return &ListRootsResult{Roots: append([]Root(nil), roots...)}, nil
		}
	}
	if cfg.SamplingHandler != nil {
		state.requestHandlers["sampling/createMessage"] = func(ctx context.Context, raw json.RawMessage) (any, error) {
			var params CreateMessageParams
			if err := json.Unmarshal(raw, &params); err != nil {
				return nil, &jsonRPCError{
					Code:    jsonRPCCodeInvalidParams,
					Message: fmt.Sprintf("mcp: invalid sampling params: %v", err),
				}
			}
			return cfg.SamplingHandler(ctx, &params)
		}
	}
	if cfg.ElicitationHandler != nil {
		state.requestHandlers["elicitation/create"] = func(ctx context.Context, raw json.RawMessage) (any, error) {
			var params ElicitationParams
			if err := json.Unmarshal(raw, &params); err != nil {
				return nil, &jsonRPCError{
					Code:    jsonRPCCodeInvalidParams,
					Message: fmt.Sprintf("mcp: invalid elicitation params: %v", err),
				}
			}
			return cfg.ElicitationHandler(ctx, &params)
		}
	}

	return state
}

func resolveClientCapabilities(cfg ClientConfig) ClientCapabilities {
	caps := cloneClientCapabilities(cfg.Capabilities)
	if cfg.RootsProvider != nil && caps.Roots == nil {
		caps.Roots = &RootsCapability{}
	}
	if cfg.SamplingHandler != nil {
		inferred := samplingCapabilitiesForHandler(cfg.SamplingHandler)
		if caps.Sampling == nil {
			if inferred != nil {
				caps.Sampling = inferred
			} else {
				caps.Sampling = &ClientSamplingCapability{}
			}
		} else if inferred != nil {
			if caps.Sampling.Context == nil && inferred.Context != nil {
				caps.Sampling.Context = &EmptyCapability{}
			}
			if caps.Sampling.Tools == nil && inferred.Tools != nil {
				caps.Sampling.Tools = &EmptyCapability{}
			}
		}
	}
	if cfg.ElicitationHandler != nil && caps.Elicitation == nil {
		caps.Elicitation = &ElicitationCapability{}
	}
	return caps
}

func registerSamplingCapabilities(handler SamplingHandler, caps *ClientSamplingCapability) SamplingHandler {
	if handler == nil || caps == nil {
		return handler
	}
	ptr := reflect.ValueOf(handler).Pointer()
	samplingCapabilityRegistry.Store(ptr, cloneSamplingCapability(caps))
	return handler
}

func samplingCapabilitiesForHandler(handler SamplingHandler) *ClientSamplingCapability {
	if handler == nil {
		return nil
	}
	ptr := reflect.ValueOf(handler).Pointer()
	caps, ok := samplingCapabilityRegistry.Load(ptr)
	if !ok {
		return nil
	}
	typed, ok := caps.(*ClientSamplingCapability)
	if !ok {
		return nil
	}
	return cloneSamplingCapability(typed)
}

func cloneSamplingCapability(in *ClientSamplingCapability) *ClientSamplingCapability {
	if in == nil {
		return nil
	}
	out := *in
	if in.Context != nil {
		empty := *in.Context
		out.Context = &empty
	}
	if in.Tools != nil {
		empty := *in.Tools
		out.Tools = &empty
	}
	return &out
}

func cloneClientCapabilities(in ClientCapabilities) ClientCapabilities {
	out := in
	if in.Roots != nil {
		roots := *in.Roots
		out.Roots = &roots
	}
	if in.Sampling != nil {
		sampling := *in.Sampling
		if in.Sampling.Context != nil {
			empty := *in.Sampling.Context
			sampling.Context = &empty
		}
		if in.Sampling.Tools != nil {
			empty := *in.Sampling.Tools
			sampling.Tools = &empty
		}
		out.Sampling = &sampling
	}
	if in.Elicitation != nil {
		elicitation := *in.Elicitation
		if in.Elicitation.Form != nil {
			empty := *in.Elicitation.Form
			elicitation.Form = &empty
		}
		if in.Elicitation.URL != nil {
			empty := *in.Elicitation.URL
			elicitation.URL = &empty
		}
		out.Elicitation = &elicitation
	}
	if in.Experimental != nil {
		out.Experimental = cloneNestedAnyMap(in.Experimental)
	}
	return out
}

func cloneNestedAnyMap(in map[string]map[string]any) map[string]map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAnyMap(v)
	}
	return out
}

// OnNotification registers a handler for a server notification method.
// Pass an empty method to receive all notifications.
func (s *clientState) OnNotification(method string, handler NotificationHandler) func() {
	key := method
	if key == "" {
		key = "*"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextHandlerID++
	handlerID := s.nextHandlerID
	if s.notificationHandlers[key] == nil {
		s.notificationHandlers[key] = make(map[int64]NotificationHandler)
	}
	s.notificationHandlers[key][handlerID] = handler

	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		handlers := s.notificationHandlers[key]
		if handlers == nil {
			return
		}
		delete(handlers, handlerID)
		if len(handlers) == 0 {
			delete(s.notificationHandlers, key)
		}
	}
}

// HandleRequest registers or replaces a handler for a server-initiated request.
func (s *clientState) HandleRequest(method string, handler RequestHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if handler == nil {
		delete(s.requestHandlers, method)
		return
	}
	s.requestHandlers[method] = handler
}

// Capabilities returns the server capabilities observed during initialize.
func (s *clientState) Capabilities() ServerCapabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneServerCapabilities(s.capabilities)
}

// ClientCapabilities returns the client capabilities this transport advertises.
func (s *clientState) ClientCapabilities() ClientCapabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneClientCapabilities(s.clientCapabilities)
}

// ServerInfo returns the server identity observed during initialize.
func (s *clientState) ServerInfo() *ServerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.serverInfo == nil {
		return nil
	}
	info := *s.serverInfo
	return &info
}

// ClientInfo returns the client identity advertised during initialize.
func (s *clientState) ClientInfo() ImplementationInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientInfo
}

// Instructions returns the optional server instructions from initialize.
func (s *clientState) Instructions() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.instructions
}

// ProtocolVersion returns the negotiated MCP protocol version.
func (s *clientState) ProtocolVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.protocolVersion
}

func (s *clientState) prepareCall() (int64, chan *jsonRPCMessage, error) {
	id := s.nextID.Add(1)
	ch := make(chan *jsonRPCMessage, 1)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, nil, errors.New("mcp: client is closed")
	}
	s.pending[id] = ch
	return id, ch, nil
}

func (s *clientState) removePending(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
}

func (s *clientState) awaitResponse(ctx context.Context, id int64, ch chan *jsonRPCMessage) (json.RawMessage, error) {
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

func (s *clientState) dispatchMessage(msg *jsonRPCMessage, respond rpcRespond) {
	if msg == nil {
		return
	}

	if msg.Method != "" {
		if hasJSONRPCID(msg.ID) {
			s.dispatchRequest(msg, respond)
			return
		}
		s.dispatchNotification(Notification{
			Method: msg.Method,
			Params: append(json.RawMessage(nil), msg.Params...),
		})
		return
	}

	if !hasJSONRPCID(msg.ID) {
		return
	}

	id, err := parsePendingID(msg.ID)
	if err != nil {
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
}

func (s *clientState) dispatchRequest(msg *jsonRPCMessage, respond rpcRespond) {
	if respond == nil {
		return
	}

	s.mu.Lock()
	handler := s.requestHandlers[msg.Method]
	s.mu.Unlock()

	requestID := responseID(msg.ID)
	params := append(json.RawMessage(nil), msg.Params...)

	go func() {
		if handler == nil {
			_ = respond(context.Background(), requestID, nil, &jsonRPCError{
				Code:    jsonRPCCodeMethodNotFound,
				Message: "method not found: " + msg.Method,
			})
			return
		}

		result, err := handler(context.Background(), params)
		if err != nil {
			_ = respond(context.Background(), requestID, nil, rpcErrorFromError(err))
			return
		}
		_ = respond(context.Background(), requestID, result, nil)
	}()
}

func (s *clientState) dispatchNotification(note Notification) {
	s.mu.Lock()
	specific := cloneNotificationHandlers(s.notificationHandlers[note.Method])
	wildcard := cloneNotificationHandlers(s.notificationHandlers["*"])
	s.mu.Unlock()

	for _, handler := range specific {
		go handler(note)
	}
	for _, handler := range wildcard {
		go handler(note)
	}
}

func (s *clientState) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for id, ch := range s.pending {
		close(ch)
		delete(s.pending, id)
	}
}

func (s *clientState) failPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.pending {
		close(ch)
		delete(s.pending, id)
	}
}

func (s *clientState) failPendingCall(id int64) {
	s.mu.Lock()
	ch, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	s.mu.Unlock()

	if ok {
		select {
		case ch <- nil:
		default:
		}
	}
}

func (s *clientState) setInitializeResult(result *InitializeResult) {
	if result == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.protocolVersion = result.ProtocolVersion
	s.capabilities = cloneServerCapabilities(result.Capabilities)
	s.instructions = result.Instructions
	if result.ServerInfo != nil {
		info := *result.ServerInfo
		s.serverInfo = &info
	} else {
		s.serverInfo = nil
	}
}

func initializeClient(ctx context.Context, state *clientState, call rpcCall, notify rpcNotify) error {
	params := InitializeParams{
		ProtocolVersion: protocolVersion,
		Capabilities:    state.ClientCapabilities(),
		ClientInfo:      state.ClientInfo(),
	}

	result, err := call(ctx, "initialize", params)
	if err != nil {
		return err
	}

	var init InitializeResult
	if err := json.Unmarshal(result, &init); err != nil {
		return fmt.Errorf("mcp: failed to parse initialize result: %w", err)
	}
	state.setInitializeResult(&init)

	return notify(ctx, "notifications/initialized", nil)
}

func notifyRootsListChanged(ctx context.Context, notify rpcNotify) error {
	return notify(ctx, "notifications/roots/list_changed", nil)
}

func listTools(ctx context.Context, call rpcCall) ([]Tool, error) {
	result, err := call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: tools/list failed: %w", err)
	}

	var list listToolsResult
	if err := json.Unmarshal(result, &list); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse tools list: %w", err)
	}
	return list.Tools, nil
}

func callTool(ctx context.Context, call rpcCall, name string, args map[string]any) (*ToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}

	result, err := call(ctx, "tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("mcp: tools/call failed: %w", err)
	}

	var toolResult ToolResult
	if err := json.Unmarshal(result, &toolResult); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse tool result: %w", err)
	}
	return &toolResult, nil
}

func listResources(ctx context.Context, call rpcCall) ([]Resource, error) {
	result, err := call(ctx, "resources/list", nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: resources/list failed: %w", err)
	}

	var list listResourcesResult
	if err := json.Unmarshal(result, &list); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse resources list: %w", err)
	}
	return list.Resources, nil
}

func readResource(ctx context.Context, call rpcCall, uri string) (*ReadResourceResult, error) {
	result, err := call(ctx, "resources/read", map[string]any{"uri": uri})
	if err != nil {
		return nil, fmt.Errorf("mcp: resources/read failed: %w", err)
	}

	var readResult ReadResourceResult
	if err := json.Unmarshal(result, &readResult); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse resource contents: %w", err)
	}
	return &readResult, nil
}

func listResourceTemplates(ctx context.Context, call rpcCall) ([]ResourceTemplate, error) {
	result, err := call(ctx, "resources/templates/list", nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: resources/templates/list failed: %w", err)
	}

	var list listResourceTemplatesResult
	if err := json.Unmarshal(result, &list); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse resource templates list: %w", err)
	}
	return list.ResourceTemplates, nil
}

func listPrompts(ctx context.Context, call rpcCall) ([]Prompt, error) {
	result, err := call(ctx, "prompts/list", nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: prompts/list failed: %w", err)
	}

	var list listPromptsResult
	if err := json.Unmarshal(result, &list); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse prompts list: %w", err)
	}
	return list.Prompts, nil
}

func getPrompt(ctx context.Context, call rpcCall, name string, args map[string]string) (*PromptResult, error) {
	params := map[string]any{"name": name}
	if len(args) > 0 {
		params["arguments"] = args
	}

	result, err := call(ctx, "prompts/get", params)
	if err != nil {
		return nil, fmt.Errorf("mcp: prompts/get failed: %w", err)
	}

	var promptResult PromptResult
	if err := json.Unmarshal(result, &promptResult); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse prompt result: %w", err)
	}
	return &promptResult, nil
}

func cloneServerCapabilities(in ServerCapabilities) ServerCapabilities {
	out := in
	if in.Tools != nil {
		tools := *in.Tools
		out.Tools = &tools
	}
	if in.Prompts != nil {
		prompts := *in.Prompts
		out.Prompts = &prompts
	}
	if in.Resources != nil {
		resources := *in.Resources
		out.Resources = &resources
	}
	if in.Experimental != nil {
		out.Experimental = cloneAnyMap(in.Experimental)
	}
	return out
}

func cloneNotificationHandlers(in map[int64]NotificationHandler) []NotificationHandler {
	if len(in) == 0 {
		return nil
	}
	out := make([]NotificationHandler, 0, len(in))
	for _, handler := range in {
		out = append(out, handler)
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func rpcErrorFromError(err error) *jsonRPCError {
	if err == nil {
		return nil
	}
	var rpcErr *jsonRPCError
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	return &jsonRPCError{
		Code:    jsonRPCCodeInternalError,
		Message: err.Error(),
	}
}

func hasJSONRPCID(raw *json.RawMessage) bool {
	if raw == nil {
		return false
	}
	trimmed := bytes.TrimSpace(*raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func parsePendingID(raw *json.RawMessage) (int64, error) {
	if raw == nil {
		return 0, errors.New("missing JSON-RPC id")
	}
	var id int64
	if err := json.Unmarshal(*raw, &id); err == nil {
		return id, nil
	}
	return 0, fmt.Errorf("unsupported response id: %s", string(*raw))
}

// normalizeID converts the raw JSON id to a concrete type for serialization.
func normalizeID(raw *json.RawMessage) any {
	if raw == nil {
		return nil
	}

	var intID int64
	if err := json.Unmarshal(*raw, &intID); err == nil {
		return intID
	}

	var floatID float64
	if err := json.Unmarshal(*raw, &floatID); err == nil {
		return floatID
	}

	var strID string
	if err := json.Unmarshal(*raw, &strID); err == nil {
		return strID
	}

	var rawCopy json.RawMessage
	rawCopy = append(rawCopy, (*raw)...)
	return rawCopy
}

// responseID preserves the request ID's exact JSON representation. Decoding
// through float64 can round large numbers, and Go's string decoder replaces
// lone UTF-16 surrogate escapes. Either rewrite makes a state-changing reply
// uncorrelatable to the original request. Transports may still reject ID forms
// they do not support before dispatch.
func responseID(raw *json.RawMessage) any {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), (*raw)...)
}

func rawJSONID(value any) *json.RawMessage {
	data, _ := json.Marshal(value)
	raw := json.RawMessage(data)
	return &raw
}

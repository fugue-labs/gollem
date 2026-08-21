package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
)

var ErrInteractionRequestFailed = errors.New("appserver: interaction request failed")

const (
	InteractionRequestUserInput = "item/tool/requestUserInput"
	InteractionToolCall         = "item/tool/call"
	InteractionMCPElicitation   = "mcpServer/elicitation/request"
	InteractionCurrentTimeRead  = "currentTime/read"
)

// InteractionService publishes runtime server-to-client interaction requests
// and resolves them from JSON-RPC responses sent by the client.
type InteractionService struct {
	mu       sync.Mutex
	counter  int64
	pending  map[string]pendingInteraction
	requests *RequestQueue
}

type pendingInteraction struct {
	meta InteractionRequestMeta
	ch   chan InteractionResponse
}

type InteractionRequest struct {
	Method    string         `json:"method"`
	RequestID string         `json:"requestId,omitempty"`
	ThreadID  string         `json:"threadId,omitempty"`
	TurnID    string         `json:"turnId,omitempty"`
	ItemID    string         `json:"itemId,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
}

type InteractionRequestMeta struct {
	RequestID string `json:"requestId"`
	Method    string `json:"method"`
	ThreadID  string `json:"threadId,omitempty"`
	TurnID    string `json:"turnId,omitempty"`
	ItemID    string `json:"itemId,omitempty"`
}

type InteractionResponse struct {
	InteractionRequestMeta
	Result json.RawMessage `json:"result,omitempty"`
	Error  *protocol.Error `json:"error,omitempty"`
}

type UserInputRequest struct {
	ThreadID         string         `json:"threadId,omitempty"`
	TurnID           string         `json:"turnId,omitempty"`
	ItemID           string         `json:"itemId,omitempty"`
	QuestionID       string         `json:"questionId,omitempty"`
	Header           string         `json:"header,omitempty"`
	Prompt           string         `json:"prompt"`
	Placeholder      string         `json:"placeholder,omitempty"`
	Required         bool           `json:"required,omitempty"`
	IsOther          bool           `json:"isOther,omitempty"`
	IsSecret         bool           `json:"isSecret,omitempty"`
	Options          []string       `json:"options,omitempty"`
	AutoResolutionMS *uint64        `json:"autoResolutionMs"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type DynamicToolCallRequest struct {
	ThreadID  string          `json:"threadId,omitempty"`
	TurnID    string          `json:"turnId,omitempty"`
	ItemID    string          `json:"itemId,omitempty"`
	CallID    string          `json:"callId,omitempty"`
	Namespace *string         `json:"namespace"`
	ToolName  string          `json:"toolName"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Metadata  map[string]any  `json:"metadata,omitempty"`
}

type MCPElicitationRequest struct {
	ThreadID string         `json:"threadId,omitempty"`
	TurnID   string         `json:"turnId,omitempty"`
	ItemID   string         `json:"itemId,omitempty"`
	ServerID string         `json:"serverId,omitempty"`
	Message  string         `json:"message"`
	Schema   map[string]any `json:"schema,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type CurrentTimeRequest struct {
	ThreadID string `json:"threadId"`
}

func NewInteractionService() *InteractionService {
	return &InteractionService{
		pending:  make(map[string]pendingInteraction),
		requests: NewRequestQueue(),
	}
}

func (s *InteractionService) setRequestQueue(q *RequestQueue) {
	if s == nil || q == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = q
}

func (s *InteractionService) RequestUserInput(ctx context.Context, req UserInputRequest) (InteractionResponse, error) {
	questionID := firstNonEmpty(strings.TrimSpace(req.QuestionID), strings.TrimSpace(req.ItemID), "input")
	header := strings.TrimSpace(req.Header)
	if header == "" {
		header = "Input"
	}
	var publicOptions []protocol.ToolRequestUserInputOption
	if len(req.Options) > 0 {
		publicOptions = make([]protocol.ToolRequestUserInputOption, 0, len(req.Options))
		for _, option := range req.Options {
			publicOptions = append(publicOptions, protocol.ToolRequestUserInputOption{Label: option, Description: ""})
		}
	}
	params := map[string]any{
		"questions": []protocol.ToolRequestUserInputQuestion{{
			ID:       questionID,
			Header:   header,
			Question: req.Prompt,
			IsOther:  req.IsOther,
			IsSecret: req.IsSecret,
			Options:  publicOptions,
		}},
		"autoResolutionMs": req.AutoResolutionMS,
		// Gollem waits synchronously for every runtime user-input response.
		"isBlocking": true,
	}
	return s.Request(ctx, InteractionRequest{
		Method:   InteractionRequestUserInput,
		ThreadID: req.ThreadID,
		TurnID:   req.TurnID,
		ItemID:   req.ItemID,
		Params:   params,
	})
}

func (s *InteractionService) RequestToolCall(ctx context.Context, req DynamicToolCallRequest) (InteractionResponse, error) {
	params := map[string]any{
		"callId":    strings.TrimSpace(req.CallID),
		"namespace": req.Namespace,
		"tool":      strings.TrimSpace(req.ToolName),
		"arguments": json.RawMessage(append([]byte(nil), req.Arguments...)),
	}
	return s.Request(ctx, InteractionRequest{
		Method:   InteractionToolCall,
		ThreadID: req.ThreadID,
		TurnID:   req.TurnID,
		ItemID:   req.ItemID,
		Params:   params,
	})
}

func (s *InteractionService) RequestMCPElicitation(ctx context.Context, req MCPElicitationRequest) (InteractionResponse, error) {
	schema := cloneStringAnyMap(req.Schema)
	if len(schema) == 0 {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	} else if schema["type"] == "object" {
		if _, ok := schema["properties"]; !ok {
			schema["properties"] = map[string]any{}
		}
	}
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return InteractionResponse{}, fmt.Errorf("encode MCP elicitation schema: %w", err)
	}
	var requestedSchema protocol.McpElicitationSchema
	if err := json.Unmarshal(schemaJSON, &requestedSchema); err != nil {
		return InteractionResponse{}, fmt.Errorf("validate MCP elicitation schema: %w", err)
	}
	requestedSchemaJSON, err := json.Marshal(requestedSchema)
	if err != nil {
		return InteractionResponse{}, fmt.Errorf("encode validated MCP elicitation schema: %w", err)
	}
	if err := json.Unmarshal(requestedSchemaJSON, &schema); err != nil {
		return InteractionResponse{}, fmt.Errorf("decode validated MCP elicitation schema: %w", err)
	}
	meta := json.RawMessage("null")
	if req.Metadata != nil {
		meta, err = json.Marshal(cloneStringAnyMap(req.Metadata))
		if err != nil {
			return InteractionResponse{}, fmt.Errorf("encode MCP elicitation metadata: %w", err)
		}
	}
	var turnID *string
	if value := strings.TrimSpace(req.TurnID); value != "" {
		turnID = &value
	}
	typedParams := protocol.McpServerElicitationRequestParams{
		ThreadID:        strings.TrimSpace(req.ThreadID),
		TurnID:          turnID,
		ServerName:      strings.TrimSpace(req.ServerID),
		Mode:            protocol.McpServerElicitationModeForm,
		Meta:            meta,
		Message:         req.Message,
		RequestedSchema: requestedSchemaJSON,
	}
	paramsJSON, err := json.Marshal(typedParams)
	if err != nil {
		return InteractionResponse{}, fmt.Errorf("encode MCP elicitation request: %w", err)
	}
	if len(paramsJSON) > runtimeInteractionPayloadMaxBytes {
		return InteractionResponse{}, fmt.Errorf("MCP elicitation request exceeds %d bytes", runtimeInteractionPayloadMaxBytes)
	}
	var params map[string]any
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return InteractionResponse{}, fmt.Errorf("decode MCP elicitation request: %w", err)
	}
	return s.Request(ctx, InteractionRequest{
		Method:   InteractionMCPElicitation,
		ThreadID: req.ThreadID,
		TurnID:   req.TurnID,
		ItemID:   req.ItemID,
		Params:   params,
	})
}

func (s *InteractionService) RequestCurrentTime(ctx context.Context, req CurrentTimeRequest) (InteractionResponse, error) {
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		return InteractionResponse{}, errors.New("current-time request requires a thread id")
	}
	return s.Request(ctx, InteractionRequest{
		Method:   InteractionCurrentTimeRead,
		ThreadID: threadID,
		Params:   map[string]any{"threadId": threadID},
	})
}

func (s *InteractionService) Request(ctx context.Context, req InteractionRequest) (InteractionResponse, error) {
	if s == nil || s.requests == nil {
		return InteractionResponse{}, errors.New("interaction service is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	method := strings.TrimSpace(req.Method)
	if !isSupportedInteractionMethod(method) {
		return InteractionResponse{}, fmt.Errorf("unsupported interaction method %q", req.Method)
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = s.nextRequestID()
	}
	itemID := strings.TrimSpace(req.ItemID)
	if itemID == "" {
		itemID = requestID
	}
	meta := InteractionRequestMeta{
		RequestID: requestID,
		Method:    method,
		ThreadID:  strings.TrimSpace(req.ThreadID),
		TurnID:    strings.TrimSpace(req.TurnID),
		ItemID:    itemID,
	}
	payload := cloneStringAnyMap(req.Params)
	if method == InteractionCurrentTimeRead {
		payload = map[string]any{"threadId": meta.ThreadID}
	} else {
		if method != InteractionRequestUserInput && method != InteractionToolCall && method != InteractionMCPElicitation {
			payload["requestId"] = requestID
		}
		payload["threadId"] = meta.ThreadID
		if method == InteractionMCPElicitation && meta.TurnID == "" {
			payload["turnId"] = nil
		} else {
			payload["turnId"] = meta.TurnID
		}
		if method != InteractionToolCall && method != InteractionMCPElicitation {
			payload["itemId"] = meta.ItemID
		}
		if method != InteractionRequestUserInput && method != InteractionToolCall && method != InteractionMCPElicitation {
			payload["startedAtMs"] = time.Now().UnixMilli()
		}
		if method == InteractionToolCall {
			callID, _ := payload["callId"].(string)
			if strings.TrimSpace(callID) == "" {
				payload["callId"] = firstNonEmpty(meta.ItemID, requestID)
			}
		}
		if method != InteractionRequestUserInput && method != InteractionToolCall && method != InteractionMCPElicitation && strings.TrimSpace(req.Reason) != "" {
			payload["reason"] = strings.TrimSpace(req.Reason)
		}
	}

	pending := pendingInteraction{meta: meta, ch: make(chan InteractionResponse, 1)}
	s.mu.Lock()
	if s.pending == nil {
		s.pending = make(map[string]pendingInteraction)
	}
	if _, exists := s.pending[requestID]; exists {
		s.mu.Unlock()
		return InteractionResponse{}, fmt.Errorf("interaction request %q is already pending", requestID)
	}
	s.pending[requestID] = pending
	s.mu.Unlock()

	s.requests.Publish(method, protocol.NewStringID(requestID), payload)
	select {
	case response := <-pending.ch:
		if response.Error != nil {
			return response, fmt.Errorf("%w: %s", ErrInteractionRequestFailed, response.Error.Message)
		}
		return response, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, requestID)
		s.mu.Unlock()
		return InteractionResponse{}, ctx.Err()
	}
}

func (s *InteractionService) Respond(resp protocol.Response) (InteractionResponse, bool, error) {
	if s == nil {
		return InteractionResponse{}, false, errors.New("interaction service is not configured")
	}
	requestID := requestIDString(resp.ID)
	if requestID == "" {
		return InteractionResponse{}, false, errors.New("response id is required")
	}
	s.mu.Lock()
	pending, ok := s.pending[requestID]
	if ok {
		delete(s.pending, requestID)
	}
	s.mu.Unlock()
	if !ok {
		return InteractionResponse{}, false, nil
	}
	response := InteractionResponse{
		InteractionRequestMeta: pending.meta,
		Result:                 append(json.RawMessage(nil), resp.Result...),
		Error:                  resp.Error,
	}
	var responseErr error
	if response.Error == nil {
		responseErr = validateInteractionResult(pending.meta.Method, response.Result)
		if responseErr != nil {
			response.Error = &protocol.Error{Code: protocol.CodeInvalidParams, Message: "invalid interaction response"}
			response.Result = nil
		}
	}
	pending.ch <- response
	return response, true, responseErr
}

func validateInteractionResult(method string, result json.RawMessage) error {
	if len(result) > runtimeInteractionPayloadMaxBytes {
		return fmt.Errorf("%s response exceeds %d bytes", method, runtimeInteractionPayloadMaxBytes)
	}
	switch method {
	case InteractionRequestUserInput:
		var response protocol.ToolRequestUserInputResponse
		if err := json.Unmarshal(result, &response); err != nil {
			return fmt.Errorf("decode user input response: %w", err)
		}
	case InteractionToolCall:
		var response protocol.DynamicToolCallResponse
		if err := json.Unmarshal(result, &response); err != nil {
			return fmt.Errorf("decode dynamic tool call response: %w", err)
		}
	case InteractionMCPElicitation:
		var response protocol.McpServerElicitationRequestResponse
		if err := json.Unmarshal(result, &response); err != nil {
			return fmt.Errorf("decode MCP elicitation response: %w", err)
		}
	case InteractionCurrentTimeRead:
		var response protocol.CurrentTimeReadResponse
		if err := json.Unmarshal(result, &response); err != nil {
			return fmt.Errorf("decode current-time response: %w", err)
		}
	}
	return nil
}

func (s *InteractionService) nextRequestID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	return fmt.Sprintf("interaction-%d", s.counter)
}

func isSupportedInteractionMethod(method string) bool {
	switch method {
	case InteractionRequestUserInput, InteractionToolCall, InteractionMCPElicitation, InteractionCurrentTimeRead:
		return true
	default:
		return false
	}
}

func requestIDString(id protocol.RequestID) string {
	switch value := id.Value().(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

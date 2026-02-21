package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// --- Message mapping tests ---

func TestBuildRequestBasic(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "You are helpful."},
				core.UserPromptPart{Content: "Hello"},
			},
			Timestamp: time.Now(),
		},
	}

	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.Model != Claude4Sonnet {
		t.Errorf("model = %q, want %q", req.Model, Claude4Sonnet)
	}
	if req.MaxTokens != 4096 {
		t.Errorf("max_tokens = %d, want 4096", req.MaxTokens)
	}
	if len(req.System) != 1 || req.System[0].Text != "You are helpful." {
		t.Errorf("system = %v, want [{text:'You are helpful.'}]", req.System)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("role = %q, want 'user'", req.Messages[0].Role)
	}
	if len(req.Messages[0].Content) != 1 || req.Messages[0].Content[0].Text != "Hello" {
		t.Errorf("content = %v, expected text 'Hello'", req.Messages[0].Content)
	}
}

func TestBuildRequestWithSettings(t *testing.T) {
	temp := 0.7
	maxTokens := 2048
	settings := &core.ModelSettings{
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	}

	req, err := buildRequest(nil, settings, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.MaxTokens != 2048 {
		t.Errorf("max_tokens = %d, want 2048", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Errorf("temperature = %v, want 0.7", req.Temperature)
	}
}

func TestBuildRequestWithTools(t *testing.T) {
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:        "search",
				Description: "Search the web",
				ParametersSchema: core.Schema{
					"type": "object",
					"properties": map[string]any{
						"query": core.Schema{"type": "string"},
					},
					"required": []string{"query"},
				},
			},
		},
	}

	req, err := buildRequest(nil, nil, params, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].Name != "search" {
		t.Errorf("tool name = %q, want 'search'", req.Tools[0].Name)
	}
	if req.Tools[0].Description != "Search the web" {
		t.Errorf("tool desc = %q, want 'Search the web'", req.Tools[0].Description)
	}

	var schema map[string]any
	if err := json.Unmarshal(req.Tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("failed to unmarshal tool schema: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want 'object'", schema["type"])
	}
}

func TestBuildRequestToolReturn(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "search",
					Content:    "found 5 results",
					ToolCallID: "call_123",
				},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	block := req.Messages[0].Content[0]
	if block.Type != "tool_result" {
		t.Errorf("type = %q, want 'tool_result'", block.Type)
	}
	if block.ToolUseID != "call_123" {
		t.Errorf("tool_use_id = %q, want 'call_123'", block.ToolUseID)
	}
	if block.Content != "found 5 results" {
		t.Errorf("content = %q, want 'found 5 results'", block.Content)
	}
}

func TestBuildRequestRetryPrompt(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.RetryPromptPart{
					Content:    "invalid output",
					ToolName:   "final_result",
					ToolCallID: "call_456",
				},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	block := req.Messages[0].Content[0]
	if block.Type != "tool_result" {
		t.Errorf("type = %q, want 'tool_result'", block.Type)
	}
	if !block.IsError {
		t.Error("expected is_error=true")
	}
	if block.Content != "invalid output" {
		t.Errorf("content = %q", block.Content)
	}
}

func TestBuildRequestRetryPromptWithoutToolID(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.RetryPromptPart{
					Content: "please try again",
				},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	block := req.Messages[0].Content[0]
	if block.Type != "text" {
		t.Errorf("type = %q, want 'text'", block.Type)
	}
	if block.Text != "please try again" {
		t.Errorf("text = %q", block.Text)
	}
}

func TestBuildRequestAssistantMessage(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Hello there"},
				core.ToolCallPart{
					ToolName:   "search",
					ArgsJSON:   `{"query":"test"}`,
					ToolCallID: "call_789",
				},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	msg := req.Messages[0]
	if msg.Role != "assistant" {
		t.Errorf("role = %q, want 'assistant'", msg.Role)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(msg.Content))
	}
	if msg.Content[0].Type != "text" || msg.Content[0].Text != "Hello there" {
		t.Errorf("first block: %+v", msg.Content[0])
	}
	if msg.Content[1].Type != "tool_use" || msg.Content[1].Name != "search" {
		t.Errorf("second block: %+v", msg.Content[1])
	}
}

func TestBuildRequestStream(t *testing.T) {
	req, err := buildRequest(nil, nil, nil, Claude4Sonnet, 4096, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !req.Stream {
		t.Error("expected stream=true")
	}
}

// --- Response parsing tests ---

func TestParseResponse(t *testing.T) {
	resp := &apiResponse{
		ID:   "msg_123",
		Role: "assistant",
		Content: []apiContentBlock{
			{Type: "text", Text: "Hello, world!"},
		},
		Model:      Claude4Sonnet,
		StopReason: "end_turn",
		Usage:      apiUsage{InputTokens: 10, OutputTokens: 5},
	}

	result := parseResponse(resp, Claude4Sonnet)
	if len(result.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(result.Parts))
	}
	tp, ok := result.Parts[0].(core.TextPart)
	if !ok {
		t.Fatalf("expected TextPart, got %T", result.Parts[0])
	}
	if tp.Content != "Hello, world!" {
		t.Errorf("content = %q", tp.Content)
	}
	if result.FinishReason != core.FinishReasonStop {
		t.Errorf("finish reason = %q, want 'stop'", result.FinishReason)
	}
	if result.Usage.InputTokens != 10 || result.Usage.OutputTokens != 5 {
		t.Errorf("usage = %+v", result.Usage)
	}
}

func TestParseResponseToolCall(t *testing.T) {
	resp := &apiResponse{
		Content: []apiContentBlock{
			{
				Type:  "tool_use",
				ID:    "call_abc",
				Name:  "search",
				Input: json.RawMessage(`{"query":"test"}`),
			},
		},
		StopReason: "tool_use",
	}

	result := parseResponse(resp, Claude4Sonnet)
	tc, ok := result.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatalf("expected ToolCallPart, got %T", result.Parts[0])
	}
	if tc.ToolName != "search" {
		t.Errorf("tool name = %q", tc.ToolName)
	}
	if tc.ArgsJSON != `{"query":"test"}` {
		t.Errorf("args = %q", tc.ArgsJSON)
	}
	if tc.ToolCallID != "call_abc" {
		t.Errorf("call id = %q", tc.ToolCallID)
	}
	if result.FinishReason != core.FinishReasonToolCall {
		t.Errorf("finish reason = %q, want 'tool_call'", result.FinishReason)
	}
}

func TestParseResponseThinking(t *testing.T) {
	resp := &apiResponse{
		Content: []apiContentBlock{
			{Type: "thinking", Thinking: "Let me think...", Signature: "sig123"},
			{Type: "text", Text: "Here's my answer"},
		},
		StopReason: "end_turn",
	}

	result := parseResponse(resp, Claude4Sonnet)
	if len(result.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(result.Parts))
	}
	tp, ok := result.Parts[0].(core.ThinkingPart)
	if !ok {
		t.Fatalf("expected ThinkingPart, got %T", result.Parts[0])
	}
	if tp.Content != "Let me think..." {
		t.Errorf("thinking = %q", tp.Content)
	}
}

func TestParseResponseStopReasons(t *testing.T) {
	tests := []struct {
		apiReason  string
		wantReason core.FinishReason
	}{
		{"end_turn", core.FinishReasonStop},
		{"stop_sequence", core.FinishReasonStop},
		{"max_tokens", core.FinishReasonLength},
		{"tool_use", core.FinishReasonToolCall},
		{"refusal", core.FinishReasonContentFilter},
		{"unknown", core.FinishReasonStop},
	}

	for _, tt := range tests {
		t.Run(tt.apiReason, func(t *testing.T) {
			got := mapStopReason(tt.apiReason)
			if got != tt.wantReason {
				t.Errorf("mapStopReason(%q) = %q, want %q", tt.apiReason, got, tt.wantReason)
			}
		})
	}
}

// --- SSE streaming tests ---

func TestParseSSEStream(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, Claude4Sonnet)

	var events []core.ModelResponseStreamEvent
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
	}

	// Should have: PartStart, 2x PartDelta (text), PartEnd
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	// Check PartStart
	start, ok := events[0].(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent, got %T", events[0])
	}
	if start.Index != 0 {
		t.Errorf("start index = %d, want 0", start.Index)
	}

	// Check deltas
	d1, ok := events[1].(core.PartDeltaEvent)
	if !ok {
		t.Fatalf("expected PartDeltaEvent, got %T", events[1])
	}
	td, ok := d1.Delta.(core.TextPartDelta)
	if !ok {
		t.Fatalf("expected TextPartDelta, got %T", d1.Delta)
	}
	if td.ContentDelta != "Hello" {
		t.Errorf("delta = %q, want 'Hello'", td.ContentDelta)
	}

	// Check final response
	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part in response, got %d", len(resp.Parts))
	}
	tp, ok := resp.Parts[0].(core.TextPart)
	if !ok {
		t.Fatalf("expected TextPart, got %T", resp.Parts[0])
	}
	if tp.Content != "Hello world" {
		t.Errorf("accumulated text = %q, want 'Hello world'", tp.Content)
	}

	// Check usage
	usage := stream.Usage()
	if usage.InputTokens != 10 {
		t.Errorf("input tokens = %d, want 10", usage.InputTokens)
	}
	if usage.OutputTokens != 2 {
		t.Errorf("output tokens = %d, want 2", usage.OutputTokens)
	}
}

func TestParseSSEStreamToolCall(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"search"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":":\"test\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, Claude4Sonnet)

	// Drain the stream.
	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Check final response has the tool call with accumulated args.
	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	tc, ok := resp.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatalf("expected ToolCallPart, got %T", resp.Parts[0])
	}
	if tc.ToolName != "search" {
		t.Errorf("tool name = %q", tc.ToolName)
	}
	if tc.ArgsJSON != `{"query":"test"}` {
		t.Errorf("args = %q, want {\"query\":\"test\"}", tc.ArgsJSON)
	}
	if tc.ToolCallID != "call_1" {
		t.Errorf("call id = %q", tc.ToolCallID)
	}
}

// --- Provider tests ---

func TestNewProviderDefaults(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p := New()

	if p.model != Claude4Sonnet {
		t.Errorf("model = %q, want %q", p.model, Claude4Sonnet)
	}
	if p.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, defaultBaseURL)
	}
	if p.apiKey != "test-key" {
		t.Errorf("apiKey = %q, want 'test-key'", p.apiKey)
	}
	if p.ModelName() != Claude4Sonnet {
		t.Errorf("ModelName() = %q", p.ModelName())
	}
}

func TestNewProviderOptions(t *testing.T) {
	p := New(
		WithAPIKey("my-key"),
		WithModel(Claude4Opus),
		WithBaseURL("https://custom.api.com"),
		WithMaxTokens(8192),
	)

	if p.apiKey != "my-key" {
		t.Errorf("apiKey = %q", p.apiKey)
	}
	if p.model != Claude4Opus {
		t.Errorf("model = %q", p.model)
	}
	if p.baseURL != "https://custom.api.com" {
		t.Errorf("baseURL = %q", p.baseURL)
	}
	if p.maxTokens != 8192 {
		t.Errorf("maxTokens = %d", p.maxTokens)
	}
}

// --- Integration test with httptest ---

func TestRequestIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers.
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing x-api-key header")
		}
		if r.Header.Get("anthropic-version") != anthropicVersion {
			t.Errorf("missing anthropic-version header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing content-type header")
		}

		// Verify request body.
		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if req.Model != Claude4Sonnet {
			t.Errorf("model = %q", req.Model)
		}

		// Return response.
		resp := apiResponse{
			ID:   "msg_test",
			Role: "assistant",
			Content: []apiContentBlock{
				{Type: "text", Text: "Hello from test!"},
			},
			Model:      Claude4Sonnet,
			StopReason: "end_turn",
			Usage:      apiUsage{InputTokens: 10, OutputTokens: 5},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}

	result, err := p.Request(context.Background(), messages, nil, &core.ModelRequestParameters{
		AllowTextOutput: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TextContent() != "Hello from test!" {
		t.Errorf("text = %q", result.TextContent())
	}
	if result.Usage.InputTokens != 10 {
		t.Errorf("input tokens = %d", result.Usage.InputTokens)
	}
}

func TestRequestStreamIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-5","usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Streamed!"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}

event: message_stop
data: {"type":"message_stop"}

`
		w.Write([]byte(sseData))
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Stream test"},
			},
		},
	}

	stream, err := p.RequestStream(context.Background(), messages, nil, &core.ModelRequestParameters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer stream.Close()

	// Drain events.
	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
	}

	resp := stream.Response()
	if resp.TextContent() != "Streamed!" {
		t.Errorf("text = %q, want 'Streamed!'", resp.TextContent())
	}
}

func TestRequestHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer server.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(server.URL))

	_, err := p.Request(context.Background(), nil, nil, &core.ModelRequestParameters{})
	if err == nil {
		t.Fatal("expected error")
	}

	httpErr, ok := err.(*core.ModelHTTPError)
	if !ok {
		t.Fatalf("expected ModelHTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", httpErr.StatusCode)
	}
}

// --- Extended thinking unit tests ---

func TestBuildRequestWithThinkingBudget(t *testing.T) {
	budget := 2048
	settings := &core.ModelSettings{
		ThinkingBudget: &budget,
	}

	req, err := buildRequest(nil, settings, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.Thinking == nil {
		t.Fatal("expected Thinking to be set")
	}
	if req.Thinking.Type != "enabled" {
		t.Errorf("thinking type = %q, want 'enabled'", req.Thinking.Type)
	}
	if req.Thinking.BudgetTokens != 2048 {
		t.Errorf("budget_tokens = %d, want 2048", req.Thinking.BudgetTokens)
	}
}

func TestBuildRequestThinkingStripsTemperature(t *testing.T) {
	budget := 1024
	temp := 0.7
	settings := &core.ModelSettings{
		ThinkingBudget: &budget,
		Temperature:    &temp,
	}

	req, err := buildRequest(nil, settings, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.Thinking == nil {
		t.Fatal("expected Thinking to be set")
	}
	if req.Temperature != nil {
		t.Errorf("expected temperature to be nil when thinking enabled, got %v", *req.Temperature)
	}
}

func TestBuildRequestNoThinkingByDefault(t *testing.T) {
	req, err := buildRequest(nil, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Thinking != nil {
		t.Errorf("expected Thinking to be nil by default, got %+v", req.Thinking)
	}
}

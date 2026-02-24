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

func TestMapUsageIncludesCacheTokens(t *testing.T) {
	// Anthropic reports non-cached tokens in InputTokens and cached tokens
	// separately. After normalization, core.Usage.InputTokens should be the
	// total (non-cached + cache_read + cache_write), matching OpenAI semantics.
	u := apiUsage{
		InputTokens:              500,
		OutputTokens:             100,
		CacheCreationInputTokens: 200,
		CacheReadInputTokens:     1000,
	}
	usage := mapUsage(u)

	// Total input = 500 + 200 + 1000 = 1700
	if usage.InputTokens != 1700 {
		t.Errorf("InputTokens = %d, want 1700 (500 non-cached + 200 cache write + 1000 cache read)", usage.InputTokens)
	}
	if usage.OutputTokens != 100 {
		t.Errorf("OutputTokens = %d, want 100", usage.OutputTokens)
	}
	if usage.CacheWriteTokens != 200 {
		t.Errorf("CacheWriteTokens = %d, want 200", usage.CacheWriteTokens)
	}
	if usage.CacheReadTokens != 1000 {
		t.Errorf("CacheReadTokens = %d, want 1000", usage.CacheReadTokens)
	}
}

func TestMapUsageNoCacheTokens(t *testing.T) {
	// Without cache tokens, InputTokens should be unchanged.
	u := apiUsage{InputTokens: 42, OutputTokens: 10}
	usage := mapUsage(u)
	if usage.InputTokens != 42 {
		t.Errorf("InputTokens = %d, want 42", usage.InputTokens)
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

func TestParseSSEStreamNoSpaceAfterColon(t *testing.T) {
	// Per the SSE spec, the space after the colon in "event:" and "data:" is
	// optional. Verify the parser handles both forms.
	sseData := "event:message_start\ndata:{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4-5\",\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\nevent:content_block_start\ndata:{\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent:content_block_delta\ndata:{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\nevent:content_block_stop\ndata:{\"type\":\"content_block_stop\",\"index\":0}\n\nevent:message_delta\ndata:{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent:message_stop\ndata:{\"type\":\"message_stop\"}\n\n"

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

	// Should have: PartStart, PartDelta, PartEnd
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	tp, ok := resp.Parts[0].(core.TextPart)
	if !ok {
		t.Fatalf("expected TextPart, got %T", resp.Parts[0])
	}
	if tp.Content != "OK" {
		t.Errorf("text = %q, want 'OK'", tp.Content)
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

// TestBuildRequestThinkingAutoAdjustsMaxTokens verifies that max_tokens is
// auto-adjusted when the thinking budget exceeds it. Anthropic requires
// max_tokens > budget_tokens; without this, the API returns 400.
func TestBuildRequestThinkingAutoAdjustsMaxTokens(t *testing.T) {
	budget := 10000
	settings := &core.ModelSettings{
		ThinkingBudget: &budget,
	}

	// Default max tokens is 4096, which is less than the budget (10000).
	req, err := buildRequest(nil, settings, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.MaxTokens <= budget {
		t.Errorf("max_tokens = %d, want > %d (budget_tokens)", req.MaxTokens, budget)
	}
	if req.MaxTokens != budget+16000 {
		t.Errorf("max_tokens = %d, want %d", req.MaxTokens, budget+16000)
	}
}

// TestBuildRequestThinkingKeepsExplicitMaxTokens verifies that an explicitly
// set MaxTokens > budget_tokens is preserved (not overridden).
func TestBuildRequestThinkingKeepsExplicitMaxTokens(t *testing.T) {
	budget := 10000
	maxTokens := 50000
	settings := &core.ModelSettings{
		ThinkingBudget: &budget,
		MaxTokens:      &maxTokens,
	}

	req, err := buildRequest(nil, settings, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.MaxTokens != 50000 {
		t.Errorf("max_tokens = %d, want 50000 (explicitly set)", req.MaxTokens)
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

// Regression: unsupported request part types (ImagePart, AudioPart, DocumentPart)
// were silently dropped. Now they return an error.
// TestBuildRequestEmptyResponseAlternation verifies that an empty ModelResponse
// (no parts) doesn't create adjacent user messages in the API request.
// In production, the agent appends empty responses to history and adds a retry
// request as the next ModelRequest. If the empty response generates no API
// message, adjacent user messages violate Anthropic's alternation requirement.
func TestBuildRequestEmptyResponseAlternation(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello"},
			},
		},
		// Empty response from model (no parts).
		core.ModelResponse{
			Parts: []core.ModelResponsePart{},
		},
		// Retry request from agent.
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.RetryPromptPart{Content: "empty response, please provide a result"},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify no adjacent user messages.
	for i := 1; i < len(req.Messages); i++ {
		if req.Messages[i-1].Role == req.Messages[i].Role {
			t.Errorf("adjacent %q messages at indices %d and %d", req.Messages[i].Role, i-1, i)
		}
	}
}

func TestBuildRequestRejectsUnsupportedParts(t *testing.T) {
	tests := []struct {
		name string
		part core.ModelRequestPart
	}{
		{"ImagePart", core.ImagePart{URL: "https://example.com/img.png", MIMEType: "image/png"}},
		{"AudioPart", core.AudioPart{URL: "https://example.com/audio.mp3", MIMEType: "audio/mp3"}},
		{"DocumentPart", core.DocumentPart{URL: "https://example.com/doc.pdf", MIMEType: "application/pdf"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := []core.ModelMessage{
				core.ModelRequest{
					Parts:     []core.ModelRequestPart{tt.part},
					Timestamp: time.Now(),
				},
			}
			_, err := buildRequest(messages, nil, &core.ModelRequestParameters{AllowTextOutput: true}, "claude-3", 1024, false)
			if err == nil {
				t.Errorf("expected error for unsupported %s, got nil", tt.name)
			}
			if err != nil && !strings.Contains(err.Error(), "unsupported request part type") {
				t.Errorf("expected 'unsupported request part type' in error, got %q", err.Error())
			}
		})
	}
}

// TestBuildRequestSystemOnlyRequestAlternation verifies that a ModelRequest
// containing ONLY SystemPromptParts doesn't create consecutive assistant
// messages in the API request. SystemPromptParts get extracted to the
// top-level system field — if no user message is emitted for the ModelRequest,
// adjacent assistant messages violate Anthropic's alternation requirement.
func TestBuildRequestSystemOnlyRequestAlternation(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello"},
			},
			Timestamp: time.Now(),
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Hi there"},
			},
		},
		// System-only request — SystemPromptPart extracted to top-level system
		// field, but no user message generated. This creates consecutive
		// assistant messages in the API request.
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "New context injected mid-conversation"},
			},
			Timestamp: time.Now(),
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Acknowledged"},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify no adjacent same-role messages.
	for i := 1; i < len(req.Messages); i++ {
		if req.Messages[i-1].Role == req.Messages[i].Role {
			t.Errorf("adjacent %q messages at indices %d and %d — would cause Anthropic 400", req.Messages[i].Role, i-1, i)
		}
	}

	// Should have 4 messages (user, assistant, user-placeholder, assistant).
	if len(req.Messages) != 4 {
		t.Errorf("expected 4 messages, got %d", len(req.Messages))
	}

	// The system blocks should still be extracted.
	if len(req.System) != 1 || req.System[0].Text != "New context injected mid-conversation" {
		t.Errorf("expected system block with context, got %v", req.System)
	}
}

// --- Prompt caching tests ---

func TestWithPromptCachingOption(t *testing.T) {
	p := New(WithAPIKey("test"), WithPromptCaching(true))
	if !p.promptCaching {
		t.Error("expected promptCaching=true")
	}
}

func TestPromptCachingEnvVar(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test")
	t.Setenv("ANTHROPIC_PROMPT_CACHING", "true")
	p := New()
	if !p.promptCaching {
		t.Error("expected promptCaching=true from env")
	}
}

func TestPromptCachingEnvVar1(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test")
	t.Setenv("ANTHROPIC_PROMPT_CACHING", "1")
	p := New()
	if !p.promptCaching {
		t.Error("expected promptCaching=true from env '1'")
	}
}

func TestPromptCachingDisabledByDefault(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test")
	p := New()
	if p.promptCaching {
		t.Error("expected promptCaching=false by default")
	}
}

func TestApplyCacheBreakpointsSystemAndTools(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "System A"},
				core.SystemPromptPart{Content: "System B"},
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{Name: "tool1", ParametersSchema: core.Schema{"type": "object"}},
			{Name: "tool2", ParametersSchema: core.Schema{"type": "object"}},
		},
	}

	req, err := buildRequest(messages, nil, params, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	applyCacheBreakpoints(req)

	// Last system block should have cache_control.
	if req.System[0].CacheControl != nil {
		t.Error("first system block should not have cache_control")
	}
	if req.System[1].CacheControl == nil || req.System[1].CacheControl.Type != "ephemeral" {
		t.Error("last system block should have cache_control ephemeral")
	}
	// Last tool should have cache_control.
	if req.Tools[0].CacheControl != nil {
		t.Error("first tool should not have cache_control")
	}
	if req.Tools[1].CacheControl == nil || req.Tools[1].CacheControl.Type != "ephemeral" {
		t.Error("last tool should have cache_control ephemeral")
	}
}

func TestApplyCacheBreakpointsNoSystemNoTools(t *testing.T) {
	req := &apiRequest{Model: Claude4Sonnet}
	applyCacheBreakpoints(req) // Should not panic.
}

func TestCacheBreakpointsOmittedWhenDisabled(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "System"},
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	// Don't call applyCacheBreakpoints — simulating disabled state.
	if req.System[0].CacheControl != nil {
		t.Error("cache_control should be nil when caching disabled")
	}
}

func TestCacheBreakpointsInPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		json.NewDecoder(r.Body).Decode(&raw)
		system, _ := raw["system"].([]any)
		if len(system) == 0 {
			t.Fatal("expected system blocks")
		}
		lastSys, _ := system[len(system)-1].(map[string]any)
		cc, _ := lastSys["cache_control"].(map[string]any)
		if cc == nil || cc["type"] != "ephemeral" {
			t.Errorf("expected cache_control ephemeral on last system block, got %v", cc)
		}
		resp := apiResponse{
			ID: "msg_1", Role: "assistant",
			Content:    []apiContentBlock{{Type: "text", Text: "ok"}},
			StopReason: "end_turn",
			Usage:      apiUsage{InputTokens: 5, OutputTokens: 1},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(WithAPIKey("k"), WithBaseURL(server.URL), WithPromptCaching(true))
	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.SystemPromptPart{Content: "System prompt"},
			core.UserPromptPart{Content: "hi"},
		}},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCacheControlOmittedFromPayloadWhenDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "cache_control") {
			t.Error("cache_control should not appear in payload when disabled")
		}
		resp := apiResponse{
			ID: "msg_1", Role: "assistant",
			Content:    []apiContentBlock{{Type: "text", Text: "ok"}},
			StopReason: "end_turn",
			Usage:      apiUsage{InputTokens: 5, OutputTokens: 1},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(WithAPIKey("k"), WithBaseURL(server.URL)) // caching not enabled
	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.SystemPromptPart{Content: "System prompt"},
			core.UserPromptPart{Content: "hi"},
		}},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

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

// toolAt extracts an apiTool from req.Tools[i] (Tools is []any).
func toolAt(t *testing.T, tools []any, i int) apiTool {
	t.Helper()
	v, ok := tools[i].(apiTool)
	if !ok {
		t.Fatalf("tools[%d] is %T, want apiTool", i, tools[i])
	}
	return v
}

// rawBlocks marshals apiContentBlock values to json.RawMessage for
// building test apiResponse.Content slices.
func rawBlocks(blocks ...apiContentBlock) []json.RawMessage {
	out := make([]json.RawMessage, len(blocks))
	for i, b := range blocks {
		data, _ := json.Marshal(b)
		out[i] = data
	}
	return out
}

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

	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.Model != ClaudeSonnet46 {
		t.Errorf("model = %q, want %q", req.Model, ClaudeSonnet46)
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

	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false, false, false)
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

func TestBuildRequestStopSequences(t *testing.T) {
	settings := &core.ModelSettings{
		StopSequences: []string{"END", "###"},
	}
	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.StopSequences) != 2 || req.StopSequences[0] != "END" || req.StopSequences[1] != "###" {
		t.Errorf("stop_sequences = %v, want [END ###]", req.StopSequences)
	}
	// Round-trip through JSON to confirm the field name.
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"stop_sequences":["END","###"]`) {
		t.Errorf("body missing stop_sequences: %s", body)
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

	req, err := buildRequest(nil, nil, params, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	if toolAt(t, req.Tools, 0).Name != "search" {
		t.Errorf("tool name = %q, want 'search'", toolAt(t, req.Tools, 0).Name)
	}
	if toolAt(t, req.Tools, 0).Description != "Search the web" {
		t.Errorf("tool desc = %q, want 'Search the web'", toolAt(t, req.Tools, 0).Description)
	}

	var schema map[string]any
	if err := json.Unmarshal(toolAt(t, req.Tools, 0).InputSchema, &schema); err != nil {
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

	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false, false, false)
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

	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false, false, false)
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

	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false, false, false)
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

	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false, false, false)
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
	req, err := buildRequest(nil, nil, nil, ClaudeSonnet46, 4096, true, false, false)
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
		Content: rawBlocks(
			apiContentBlock{Type: "text", Text: "Hello, world!"},
		),
		Model:      ClaudeSonnet46,
		StopReason: "end_turn",
		Usage:      apiUsage{InputTokens: 10, OutputTokens: 5},
	}

	result := parseResponse(resp, ClaudeSonnet46)
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
		Content: rawBlocks(
			apiContentBlock{
				Type:  "tool_use",
				ID:    "call_abc",
				Name:  "search",
				Input: json.RawMessage(`{"query":"test"}`),
			},
		),
		StopReason: "tool_use",
	}

	result := parseResponse(resp, ClaudeSonnet46)
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
		Content: rawBlocks(
			apiContentBlock{Type: "thinking", Thinking: "Let me think...", Signature: "sig123"},
			apiContentBlock{Type: "text", Text: "Here's my answer"},
		),
		StopReason: "end_turn",
	}

	result := parseResponse(resp, ClaudeSonnet46)
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
	stream := newStreamedResponse(body, ClaudeSonnet46)

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
	stream := newStreamedResponse(body, ClaudeSonnet46)

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
	stream := newStreamedResponse(body, ClaudeSonnet46)

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

	if p.model != ClaudeSonnet46 {
		t.Errorf("model = %q, want %q", p.model, ClaudeSonnet46)
	}
	if p.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, defaultBaseURL)
	}
	if p.apiKey != "test-key" {
		t.Errorf("apiKey = %q, want 'test-key'", p.apiKey)
	}
	if p.ModelName() != ClaudeSonnet46 {
		t.Errorf("ModelName() = %q", p.ModelName())
	}
}

func TestNewProviderOptions(t *testing.T) {
	p := New(
		WithAPIKey("my-key"),
		WithModel(ClaudeOpus46),
		WithBaseURL("https://custom.api.com"),
		WithMaxTokens(8192),
	)

	if p.apiKey != "my-key" {
		t.Errorf("apiKey = %q", p.apiKey)
	}
	if p.model != ClaudeOpus46 {
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
		if req.Model != ClaudeSonnet46 {
			t.Errorf("model = %q", req.Model)
		}

		// Return response.
		resp := apiResponse{
			ID:   "msg_test",
			Role: "assistant",
			Content: rawBlocks(
				apiContentBlock{Type: "text", Text: "Hello from test!"},
			),
			Model:      ClaudeSonnet46,
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

	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false, false, false)
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

	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false, false, false)
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
	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false, false, false)
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

	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.MaxTokens != 50000 {
		t.Errorf("max_tokens = %d, want 50000 (explicitly set)", req.MaxTokens)
	}
}

func TestBuildRequestNoThinkingByDefault(t *testing.T) {
	req, err := buildRequest(nil, nil, nil, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Thinking != nil {
		t.Errorf("expected Thinking to be nil by default, got %+v", req.Thinking)
	}
}

// TestOpus47RejectsThinkingBudget verifies that passing ThinkingBudget with
// Opus 4.7 fails at request build with a clear message pointing the caller to
// WithReasoningEffort instead. Opus 4.7 only supports adaptive thinking.
func TestOpus47RejectsThinkingBudget(t *testing.T) {
	budget := 2048
	settings := &core.ModelSettings{ThinkingBudget: &budget}

	_, err := buildRequest(nil, settings, nil, ClaudeOpus47, 4096, false, false, false)
	if err == nil {
		t.Fatal("expected error when ThinkingBudget is set on Opus 4.7")
	}
	msg := err.Error()
	if !strings.Contains(msg, "claude-opus-4-7") {
		t.Errorf("error should mention the model name, got: %s", msg)
	}
	if !strings.Contains(msg, "WithReasoningEffort") {
		t.Errorf("error should point to WithReasoningEffort, got: %s", msg)
	}
}

// --- Adaptive thinking unit tests ---

// TestBuildRequestAdaptiveThinking verifies {thinking: {type: "adaptive"}}
// is emitted on every model generation that accepts it, with no
// budget_tokens on the wire.
func TestBuildRequestAdaptiveThinking(t *testing.T) {
	on := true
	for _, model := range []string{
		ClaudeSonnet46, ClaudeOpus46, ClaudeOpus47, ClaudeOpus48, ClaudeFable5,
	} {
		t.Run(model, func(t *testing.T) {
			settings := &core.ModelSettings{AdaptiveThinking: &on}
			req, err := buildRequest(nil, settings, nil, model, 4096, false, false, false)
			if err != nil {
				t.Fatalf("buildRequest: %v", err)
			}
			if req.Thinking == nil {
				t.Fatal("expected Thinking to be set")
			}
			if req.Thinking.Type != "adaptive" {
				t.Errorf("thinking type = %q, want 'adaptive'", req.Thinking.Type)
			}
			wire, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(wire), `"thinking":{"type":"adaptive"}`) {
				t.Errorf("wire thinking shape wrong: %s", wire)
			}
			if strings.Contains(string(wire), "budget_tokens") {
				t.Errorf("budget_tokens must be absent for adaptive thinking: %s", wire)
			}
		})
	}
}

func TestBuildRequestAdaptiveThinkingStripsTemperature(t *testing.T) {
	on := true
	temp := 0.7
	settings := &core.ModelSettings{AdaptiveThinking: &on, Temperature: &temp}
	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.Temperature != nil {
		t.Errorf("expected temperature to be nil when adaptive thinking enabled, got %v", *req.Temperature)
	}
}

// TestBuildRequestAdaptiveThinkingKeepsMaxTokens verifies the adaptive path
// never silently rewrites the caller's MaxTokens — there is no budget to
// auto-adjust against (thinking tokens just count toward max_tokens).
func TestBuildRequestAdaptiveThinkingKeepsMaxTokens(t *testing.T) {
	on := true
	maxTokens := 8192
	settings := &core.ModelSettings{AdaptiveThinking: &on, MaxTokens: &maxTokens}
	req, err := buildRequest(nil, settings, nil, ClaudeOpus47, 4096, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.MaxTokens != 8192 {
		t.Errorf("max_tokens = %d, want 8192 (untouched)", req.MaxTokens)
	}
}

// TestBuildRequestAdaptiveThinkingFalseIsNoop verifies an explicit false
// behaves exactly like an unset field.
func TestBuildRequestAdaptiveThinkingFalseIsNoop(t *testing.T) {
	off := false
	settings := &core.ModelSettings{AdaptiveThinking: &off}
	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.Thinking != nil {
		t.Errorf("expected no thinking for AdaptiveThinking=false, got %+v", req.Thinking)
	}
}

// TestAdaptiveThinkingUnsupportedModels verifies pre-4.6 models reject
// adaptive thinking at build time with a clear error.
func TestAdaptiveThinkingUnsupportedModels(t *testing.T) {
	on := true
	for _, model := range []string{
		"claude-sonnet-4-5", "claude-opus-4-5", ClaudeHaiku45, "claude-3-5-sonnet-20241022",
	} {
		t.Run(model, func(t *testing.T) {
			settings := &core.ModelSettings{AdaptiveThinking: &on}
			_, err := buildRequest(nil, settings, nil, model, 4096, false, false, false)
			if err == nil {
				t.Fatalf("expected error for adaptive thinking on %q", model)
			}
			if !strings.Contains(err.Error(), "adaptive thinking") {
				t.Errorf("error should mention adaptive thinking, got: %v", err)
			}
		})
	}
}

// TestAdaptiveAndManualThinkingConflict verifies the two thinking modes are
// mutually exclusive regardless of model.
func TestAdaptiveAndManualThinkingConflict(t *testing.T) {
	on := true
	budget := 2048
	settings := &core.ModelSettings{AdaptiveThinking: &on, ThinkingBudget: &budget}
	_, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false, false, false)
	if err == nil {
		t.Fatal("expected error when both AdaptiveThinking and ThinkingBudget are set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should say mutually exclusive, got: %v", err)
	}
}

// TestOpus48AndFableRejectThinkingBudget verifies the post-4.7 flagships
// reject manual thinking the same way Opus 4.7 does.
func TestOpus48AndFableRejectThinkingBudget(t *testing.T) {
	budget := 2048
	for _, model := range []string{ClaudeOpus48, ClaudeFable5} {
		t.Run(model, func(t *testing.T) {
			settings := &core.ModelSettings{ThinkingBudget: &budget}
			_, err := buildRequest(nil, settings, nil, model, 4096, false, false, false)
			if err == nil {
				t.Fatalf("expected error when ThinkingBudget is set on %q", model)
			}
			if !strings.Contains(err.Error(), "AdaptiveThinking") {
				t.Errorf("error should point to AdaptiveThinking, got: %v", err)
			}
		})
	}
}

// TestBuildRequestWithEffort verifies output_config.effort is emitted for each
// valid value on models that accept it.
func TestBuildRequestWithEffort(t *testing.T) {
	cases := []struct {
		model  string
		effort string
	}{
		{ClaudeOpus47, "low"},
		{ClaudeOpus47, "medium"},
		{ClaudeOpus47, "high"},
		{ClaudeOpus47, "xhigh"},
		{ClaudeOpus47, "max"},
		{ClaudeOpus48, "medium"},
		{ClaudeOpus48, "xhigh"},
		{ClaudeOpus48, "max"},
		{ClaudeFable5, "medium"},
		{ClaudeFable5, "xhigh"},
		{ClaudeFable5, "max"},
		{ClaudeOpus46, "low"},
		{ClaudeOpus46, "max"},
		{ClaudeSonnet46, "medium"},
		{ClaudeSonnet46, "max"},
	}
	for _, tc := range cases {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			effort := tc.effort
			settings := &core.ModelSettings{ReasoningEffort: &effort}
			req, err := buildRequest(nil, settings, nil, tc.model, 4096, false, false, false)
			if err != nil {
				t.Fatalf("buildRequest: %v", err)
			}
			if req.OutputConfig == nil {
				t.Fatal("expected OutputConfig to be set")
			}
			if req.OutputConfig.Effort != tc.effort {
				t.Errorf("effort = %q, want %q", req.OutputConfig.Effort, tc.effort)
			}
		})
	}
}

// TestEffortGatingPerModel verifies that invalid (model, effort) combos are
// rejected at build time with a clear error: xhigh on non-4.7, max on <4.6,
// any effort on Haiku or pre-4.5 models.
func TestEffortGatingPerModel(t *testing.T) {
	cases := []struct {
		name   string
		model  string
		effort string
	}{
		{"xhigh_on_opus_46", ClaudeOpus46, "xhigh"},
		{"xhigh_on_sonnet_46", ClaudeSonnet46, "xhigh"},
		{"max_on_haiku", ClaudeHaiku45, "max"},
		{"low_on_haiku", ClaudeHaiku45, "low"},
		{"high_on_haiku", ClaudeHaiku45, "high"},
		{"any_on_3x", "claude-3-5-sonnet-20241022", "low"},
		{"unknown_value", ClaudeOpus47, "ultra"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			effort := tc.effort
			settings := &core.ModelSettings{ReasoningEffort: &effort}
			_, err := buildRequest(nil, settings, nil, tc.model, 4096, false, false, false)
			if err == nil {
				t.Fatalf("expected error for model=%q effort=%q", tc.model, tc.effort)
			}
		})
	}
}

// TestBuildRequestEffortAndThinkingBudgetCoexistOn46 verifies that on Opus 4.6
// or Sonnet 4.6, a caller may set both ThinkingBudget (legacy) and effort. The
// output should carry both fields. No warning is required; just don't crash.
func TestBuildRequestEffortAndThinkingBudgetCoexistOn46(t *testing.T) {
	budget := 2048
	effort := "medium"
	settings := &core.ModelSettings{
		ThinkingBudget:  &budget,
		ReasoningEffort: &effort,
	}

	req, err := buildRequest(nil, settings, nil, ClaudeOpus46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.Thinking == nil || req.Thinking.Type != "enabled" || req.Thinking.BudgetTokens != 2048 {
		t.Errorf("expected manual thinking to be emitted, got %+v", req.Thinking)
	}
	if req.OutputConfig == nil || req.OutputConfig.Effort != "medium" {
		t.Errorf("expected effort=medium in output_config, got %+v", req.OutputConfig)
	}
}

// TestNoEffortByDefault verifies output_config is omitted when no effort is set.
func TestNoEffortByDefault(t *testing.T) {
	req, err := buildRequest(nil, nil, nil, ClaudeOpus47, 4096, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.OutputConfig != nil {
		t.Errorf("expected OutputConfig nil by default, got %+v", req.OutputConfig)
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

	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false, false, false)
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

// TestBuildRequestRejectsAudio verifies that AudioPart is explicitly
// rejected — Anthropic's Messages API does not accept audio input.
// ImagePart and DocumentPart are now supported (see multimodal tests).
func TestBuildRequestRejectsAudio(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.AudioPart{URL: "https://example.com/audio.mp3", MIMEType: "audio/mp3"},
			},
			Timestamp: time.Now(),
		},
	}
	_, err := buildRequest(messages, nil, &core.ModelRequestParameters{AllowTextOutput: true}, "claude-3", 1024, false, false, false)
	if err == nil {
		t.Fatal("expected error for AudioPart, got nil")
	}
	if !strings.Contains(err.Error(), "audio input is not supported") {
		t.Errorf("error should mention audio not supported, got: %q", err.Error())
	}
}

// TestBuildRequestImageURL verifies an ImagePart with an https URL serializes
// as an image block with a url source.
func TestBuildRequestImageURL(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "What is in this image?"},
			core.ImagePart{URL: "https://example.com/cat.jpg"},
		}},
	}
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 1024, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
		t.Fatalf("expected 1 message with 2 content blocks, got %+v", req.Messages)
	}
	img := req.Messages[0].Content[1]
	if img.Type != "image" {
		t.Errorf("block type = %q, want image", img.Type)
	}
	if img.Source == nil || img.Source.Type != "url" || img.Source.URL != "https://example.com/cat.jpg" {
		t.Errorf("source = %+v, want {type: url, url: https://example.com/cat.jpg}", img.Source)
	}
}

// TestBuildRequestImageDataURI verifies a data:MIME;base64,... URI parses
// into a base64 source block.
func TestBuildRequestImageDataURI(t *testing.T) {
	// Tiny valid 1x1 PNG base64 is fine; this test only checks routing.
	dataURI := core.BinaryContent([]byte{1, 2, 3}, "image/png")
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.ImagePart{URL: dataURI},
		}},
	}
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 1024, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	img := req.Messages[0].Content[0]
	if img.Type != "image" {
		t.Errorf("block type = %q, want image", img.Type)
	}
	if img.Source == nil || img.Source.Type != "base64" {
		t.Fatalf("source = %+v, want base64", img.Source)
	}
	if img.Source.MediaType != "image/png" {
		t.Errorf("media_type = %q, want image/png", img.Source.MediaType)
	}
	if img.Source.Data == "" {
		t.Error("expected base64 data to be non-empty")
	}
}

// TestBuildRequestDocument verifies DocumentPart emits a document block
// with optional title.
func TestBuildRequestDocument(t *testing.T) {
	dataURI := core.BinaryContent([]byte("fake pdf"), "application/pdf")
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.DocumentPart{URL: dataURI, Title: "My Report"},
		}},
	}
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 1024, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	doc := req.Messages[0].Content[0]
	if doc.Type != "document" {
		t.Errorf("block type = %q, want document", doc.Type)
	}
	if doc.Title != "My Report" {
		t.Errorf("title = %q, want My Report", doc.Title)
	}
	if doc.Source == nil || doc.Source.Type != "base64" || doc.Source.MediaType != "application/pdf" {
		t.Errorf("source = %+v", doc.Source)
	}
}

// TestBuildRequestToolResultWithImages verifies a ToolReturnPart with
// Images emits a tool_result whose content is an array: text block first,
// then one image block per attached image. Round-trips through JSON.
func TestBuildRequestToolResultWithImages(t *testing.T) {
	img1 := core.ImagePart{URL: "https://example.com/a.png", MIMEType: "image/png"}
	img2 := core.ImagePart{URL: core.BinaryContent([]byte{9, 9, 9}, "image/jpeg")}
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.ToolReturnPart{
				ToolCallID: "call_1",
				Content:    "here are the screenshots",
				Images:     []core.ImagePart{img1, img2},
			},
		}},
	}
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 1024, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	// Verify via JSON to exercise the custom MarshalJSON path.
	raw, err := json.Marshal(req.Messages[0].Content[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Type      string            `json:"type"`
		ToolUseID string            `json:"tool_use_id"`
		Content   []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "tool_result" {
		t.Errorf("type = %q", decoded.Type)
	}
	if decoded.ToolUseID != "call_1" {
		t.Errorf("tool_use_id = %q", decoded.ToolUseID)
	}
	if len(decoded.Content) != 3 {
		t.Fatalf("expected 3 content blocks (text + 2 images), got %d", len(decoded.Content))
	}

	// First block should be text.
	var textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(decoded.Content[0], &textBlock)
	if textBlock.Type != "text" || textBlock.Text != "here are the screenshots" {
		t.Errorf("text block = %+v", textBlock)
	}

	// Second block: image with URL source.
	var imgBlock1 struct {
		Type   string    `json:"type"`
		Source apiSource `json:"source"`
	}
	_ = json.Unmarshal(decoded.Content[1], &imgBlock1)
	if imgBlock1.Type != "image" || imgBlock1.Source.Type != "url" {
		t.Errorf("img1 block = %+v", imgBlock1)
	}

	// Third block: image with base64 source.
	var imgBlock2 struct {
		Type   string    `json:"type"`
		Source apiSource `json:"source"`
	}
	_ = json.Unmarshal(decoded.Content[2], &imgBlock2)
	if imgBlock2.Type != "image" || imgBlock2.Source.Type != "base64" || imgBlock2.Source.MediaType != "image/jpeg" {
		t.Errorf("img2 block = %+v", imgBlock2)
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

	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false, false, false)
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

func TestAnthropicCacheControl_SystemPrompt(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "System prompt 1"},
				core.SystemPromptPart{Content: "System prompt 2"},
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.System) != 2 {
		t.Fatalf("expected 2 system blocks, got %d", len(req.System))
	}
	// First block should NOT have cache_control.
	if req.System[0].CacheControl != nil {
		t.Error("expected first system block without cache_control")
	}
	// Last block SHOULD have cache_control.
	if req.System[1].CacheControl == nil {
		t.Fatal("expected last system block with cache_control")
	}
	if req.System[1].CacheControl.Type != "ephemeral" {
		t.Errorf("cache_control type = %q, want 'ephemeral'", req.System[1].CacheControl.Type)
	}
}

func TestAnthropicCacheControl_Tools(t *testing.T) {
	params := &core.ModelRequestParameters{
		AllowTextOutput: true,
		FunctionTools: []core.ToolDefinition{
			{Name: "tool_a", Description: "A"},
			{Name: "tool_b", Description: "B"},
		},
	}
	req, err := buildRequest(nil, nil, params, ClaudeSonnet46, 4096, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(req.Tools))
	}
	if toolAt(t, req.Tools, 0).CacheControl != nil {
		t.Error("expected first tool without cache_control")
	}
	if toolAt(t, req.Tools, 1).CacheControl == nil {
		t.Fatal("expected last tool with cache_control")
	}
	if toolAt(t, req.Tools, 1).CacheControl.Type != "ephemeral" {
		t.Errorf("cache_control type = %q, want 'ephemeral'", toolAt(t, req.Tools, 1).CacheControl.Type)
	}
}

func TestAnthropicCacheControl_NoSystem(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.System) != 0 {
		t.Errorf("expected no system blocks, got %d", len(req.System))
	}
}

func TestAnthropicCacheControl_NoTools(t *testing.T) {
	req, err := buildRequest(nil, nil, nil, ClaudeSonnet46, 4096, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 0 {
		t.Errorf("expected no tools, got %d", len(req.Tools))
	}
}

func TestAnthropicCacheControl_Disabled(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "System prompt"},
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}
	params := &core.ModelRequestParameters{
		AllowTextOutput: true,
		FunctionTools: []core.ToolDefinition{
			{Name: "tool_a", Description: "A"},
		},
	}
	req, err := buildRequest(messages, nil, params, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	// Neither system nor tools should have cache_control when disabled.
	if len(req.System) > 0 && req.System[0].CacheControl != nil {
		t.Error("expected no cache_control on system when disabled")
	}
	if len(req.Tools) > 0 && toolAt(t, req.Tools, 0).CacheControl != nil {
		t.Error("expected no cache_control on tools when disabled")
	}
}

func TestAnthropicCacheControl_UsageTracking(t *testing.T) {
	usage := apiUsage{
		InputTokens:              100,
		OutputTokens:             50,
		CacheCreationInputTokens: 200,
		CacheReadInputTokens:     300,
	}
	mapped := mapUsage(usage)
	if mapped.CacheWriteTokens != 200 {
		t.Errorf("CacheWriteTokens = %d, want 200", mapped.CacheWriteTokens)
	}
	if mapped.CacheReadTokens != 300 {
		t.Errorf("CacheReadTokens = %d, want 300", mapped.CacheReadTokens)
	}
	if mapped.InputTokens != 600 {
		t.Errorf("InputTokens = %d, want 600 (100+200+300)", mapped.InputTokens)
	}
}

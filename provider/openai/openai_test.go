package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestBuildRequestBasic(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "You are helpful."},
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-4o" {
		t.Errorf("expected model gpt-4o, got %s", req.Model)
	}
	if req.MaxTokens != 4096 {
		t.Errorf("expected max_tokens 4096, got %d", req.MaxTokens)
	}
	if req.Stream {
		t.Error("expected stream=false")
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	// System message.
	if req.Messages[0].Role != "system" {
		t.Errorf("expected role system, got %s", req.Messages[0].Role)
	}
	if req.Messages[0].Content != "You are helpful." {
		t.Errorf("unexpected system content: %s", req.Messages[0].Content)
	}
	// User message.
	if req.Messages[1].Role != "user" {
		t.Errorf("expected role user, got %s", req.Messages[1].Role)
	}
	if req.Messages[1].Content != "Hello" {
		t.Errorf("unexpected user content: %s", req.Messages[1].Content)
	}
}

func TestBuildRequestWithSettings(t *testing.T) {
	maxTokens := 1000
	temp := 0.7
	topP := 0.9
	settings := &core.ModelSettings{
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		TopP:        &topP,
	}
	req, err := buildRequest(nil, settings, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.MaxTokens != 1000 {
		t.Errorf("expected max_tokens 1000, got %d", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %v", req.Temperature)
	}
	if req.TopP == nil || *req.TopP != 0.9 {
		t.Errorf("expected top_p 0.9, got %v", req.TopP)
	}
}

func TestBuildRequestWithTools(t *testing.T) {
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Get weather for a city",
				ParametersSchema: core.Schema{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	req, err := buildRequest(nil, nil, params, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].Type != "function" {
		t.Errorf("expected type function, got %s", req.Tools[0].Type)
	}
	if req.Tools[0].Function.Name != "get_weather" {
		t.Errorf("expected name get_weather, got %s", req.Tools[0].Function.Name)
	}
}

func TestBuildRequestWithResponseFormat(t *testing.T) {
	params := &core.ModelRequestParameters{
		OutputMode: core.OutputModeNative,
		OutputObject: &core.OutputObjectDefinition{
			Name: "result",
			JSONSchema: core.Schema{
				"type": "object",
				"properties": map[string]any{
					"answer": map[string]any{"type": "string"},
				},
			},
		},
	}
	req, err := buildRequest(nil, nil, params, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.ResponseFormat == nil {
		t.Fatal("expected response_format to be set")
	}
	if req.ResponseFormat.Type != "json_schema" {
		t.Errorf("expected type json_schema, got %s", req.ResponseFormat.Type)
	}
	if req.ResponseFormat.JSONSchema == nil {
		t.Fatal("expected json_schema to be set")
	}
	if req.ResponseFormat.JSONSchema.Name != "result" {
		t.Errorf("expected name result, got %s", req.ResponseFormat.JSONSchema.Name)
	}
	if !req.ResponseFormat.JSONSchema.Strict {
		t.Error("expected strict=true by default")
	}
}

func TestBuildRequestToolReturn(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "get_weather",
					Content:    "sunny, 72°F",
					ToolCallID: "call_123",
				},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	msg := req.Messages[0]
	if msg.Role != "tool" {
		t.Errorf("expected role tool, got %s", msg.Role)
	}
	if msg.ToolCallID != "call_123" {
		t.Errorf("expected tool_call_id call_123, got %s", msg.ToolCallID)
	}
	if msg.Content != "sunny, 72°F" {
		t.Errorf("unexpected content: %s", msg.Content)
	}
}

func TestBuildRequestRetryPrompt(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.RetryPromptPart{
					Content:    "Invalid JSON",
					ToolCallID: "call_456",
				},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	msg := req.Messages[0]
	if msg.Role != "tool" {
		t.Errorf("expected role tool, got %s", msg.Role)
	}
	if msg.ToolCallID != "call_456" {
		t.Errorf("expected tool_call_id call_456, got %s", msg.ToolCallID)
	}
}

func TestBuildRequestRetryPromptWithoutToolID(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.RetryPromptPart{
					Content: "Please try again",
				},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	msg := req.Messages[0]
	if msg.Role != "user" {
		t.Errorf("expected role user, got %s", msg.Role)
	}
	if msg.Content != "Please try again" {
		t.Errorf("unexpected content: %s", msg.Content)
	}
}

func TestBuildRequestAssistantMessage(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Hello!"},
				core.ToolCallPart{
					ToolName:   "search",
					ArgsJSON:   `{"query":"test"}`,
					ToolCallID: "call_789",
				},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	msg := req.Messages[0]
	if msg.Role != "assistant" {
		t.Errorf("expected role assistant, got %s", msg.Role)
	}
	if msg.Content != "Hello!" {
		t.Errorf("expected content 'Hello!', got '%s'", msg.Content)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_789" {
		t.Errorf("expected id call_789, got %s", tc.ID)
	}
	if tc.Function.Name != "search" {
		t.Errorf("expected name search, got %s", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"query":"test"}` {
		t.Errorf("unexpected arguments: %s", tc.Function.Arguments)
	}
}

func TestBuildRequestStream(t *testing.T) {
	req, err := buildRequest(nil, nil, nil, "gpt-4o", 4096, true)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Stream {
		t.Error("expected stream=true")
	}
	if req.StreamOptions == nil {
		t.Fatal("expected stream_options to be set")
	}
	if !req.StreamOptions.IncludeUsage {
		t.Error("expected include_usage=true")
	}
}

func TestParseResponse(t *testing.T) {
	resp := &apiResponse{
		Choices: []apiChoice{
			{
				Message: apiChatMsg{
					Role:    "assistant",
					Content: "Hello there!",
				},
				FinishReason: "stop",
			},
		},
		Usage: apiUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
		},
	}
	result := parseResponse(resp, "gpt-4o")
	if len(result.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(result.Parts))
	}
	tp, ok := result.Parts[0].(core.TextPart)
	if !ok {
		t.Fatal("expected TextPart")
	}
	if tp.Content != "Hello there!" {
		t.Errorf("unexpected content: %s", tp.Content)
	}
	if result.FinishReason != core.FinishReasonStop {
		t.Errorf("expected FinishReasonStop, got %s", result.FinishReason)
	}
	if result.Usage.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 5 {
		t.Errorf("expected 5 output tokens, got %d", result.Usage.OutputTokens)
	}
}

func TestParseResponseToolCall(t *testing.T) {
	resp := &apiResponse{
		Choices: []apiChoice{
			{
				Message: apiChatMsg{
					Role: "assistant",
					ToolCalls: []apiToolCall{
						{
							ID:   "call_abc",
							Type: "function",
							Function: apiToolFunction{
								Name:      "get_weather",
								Arguments: `{"city":"NYC"}`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
	}
	result := parseResponse(resp, "gpt-4o")
	if len(result.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(result.Parts))
	}
	tc, ok := result.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatal("expected ToolCallPart")
	}
	if tc.ToolName != "get_weather" {
		t.Errorf("expected get_weather, got %s", tc.ToolName)
	}
	if tc.ArgsJSON != `{"city":"NYC"}` {
		t.Errorf("unexpected args: %s", tc.ArgsJSON)
	}
	if tc.ToolCallID != "call_abc" {
		t.Errorf("expected call_abc, got %s", tc.ToolCallID)
	}
	if result.FinishReason != core.FinishReasonToolCall {
		t.Errorf("expected FinishReasonToolCall, got %s", result.FinishReason)
	}
}

func TestParseResponseStopReasons(t *testing.T) {
	tests := []struct {
		reason   string
		expected core.FinishReason
	}{
		{"stop", core.FinishReasonStop},
		{"length", core.FinishReasonLength},
		{"tool_calls", core.FinishReasonToolCall},
		{"content_filter", core.FinishReasonContentFilter},
		{"unknown", core.FinishReasonStop},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			resp := &apiResponse{
				Choices: []apiChoice{
					{
						Message:      apiChatMsg{Role: "assistant", Content: "test"},
						FinishReason: tt.reason,
					},
				},
			}
			result := parseResponse(resp, "gpt-4o")
			if result.FinishReason != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result.FinishReason)
			}
		})
	}
}

func TestParseSSEStreamText(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}

data: [DONE]

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	// First event should be part start with empty text (role only, then content "").
	// Actually, empty content won't trigger handleTextDelta.
	// The first real content chunk "Hello" starts the text part.
	event1, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	start, ok := event1.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent, got %T", event1)
	}
	tp, ok := start.Part.(core.TextPart)
	if !ok {
		t.Fatal("expected TextPart")
	}
	if tp.Content != "Hello" {
		t.Errorf("expected 'Hello', got '%s'", tp.Content)
	}

	// Second event should be delta with " world".
	event2, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	delta, ok := event2.(core.PartDeltaEvent)
	if !ok {
		t.Fatalf("expected PartDeltaEvent, got %T", event2)
	}
	td, ok := delta.Delta.(core.TextPartDelta)
	if !ok {
		t.Fatal("expected TextPartDelta")
	}
	if td.ContentDelta != " world" {
		t.Errorf("expected ' world', got '%s'", td.ContentDelta)
	}

	// Next call should return EOF (finish_reason + [DONE]).
	_, err = stream.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}

	// Verify final response.
	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	finalTp, ok := resp.Parts[0].(core.TextPart)
	if !ok {
		t.Fatal("expected TextPart in final response")
	}
	if finalTp.Content != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", finalTp.Content)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 2 {
		t.Errorf("expected 2 output tokens, got %d", resp.Usage.OutputTokens)
	}
}

func TestParseSSEStreamToolCall(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"ci"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"NYC\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	// First event: tool call start.
	event1, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	start, ok := event1.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent, got %T", event1)
	}
	tc, ok := start.Part.(core.ToolCallPart)
	if !ok {
		t.Fatal("expected ToolCallPart")
	}
	if tc.ToolName != "get_weather" {
		t.Errorf("expected get_weather, got %s", tc.ToolName)
	}
	if tc.ToolCallID != "call_abc" {
		t.Errorf("expected call_abc, got %s", tc.ToolCallID)
	}

	// Next 3 events should be argument deltas.
	expectedDeltas := []string{`{"ci`, `ty":"`, `NYC"}`}
	for i, expected := range expectedDeltas {
		event, err := stream.Next()
		if err != nil {
			t.Fatalf("delta %d: %v", i, err)
		}
		delta, ok := event.(core.PartDeltaEvent)
		if !ok {
			t.Fatalf("delta %d: expected PartDeltaEvent, got %T", i, event)
		}
		tcd, ok := delta.Delta.(core.ToolCallPartDelta)
		if !ok {
			t.Fatalf("delta %d: expected ToolCallPartDelta, got %T", i, delta.Delta)
		}
		if tcd.ArgsJSONDelta != expected {
			t.Errorf("delta %d: expected '%s', got '%s'", i, expected, tcd.ArgsJSONDelta)
		}
	}

	// Should get EOF.
	_, err = stream.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}

	// Verify accumulated args.
	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	finalTc, ok := resp.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatal("expected ToolCallPart in final response")
	}
	if finalTc.ArgsJSON != `{"city":"NYC"}` {
		t.Errorf("expected accumulated args, got '%s'", finalTc.ArgsJSON)
	}
	if resp.FinishReason != core.FinishReasonToolCall {
		t.Errorf("expected FinishReasonToolCall, got %s", resp.FinishReason)
	}
}

func TestParseSSEStreamMixedTextAndToolCallSameChunk(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-mixed","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hi","tool_calls":[{"index":0,"id":"call_mix","type":"function","function":{"name":"search","arguments":"{\"q\":\"x\"}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	event1, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	start1, ok := event1.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected first event PartStartEvent, got %T", event1)
	}
	if _, ok := start1.Part.(core.TextPart); !ok {
		t.Fatalf("expected first part to be TextPart, got %T", start1.Part)
	}

	event2, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	start2, ok := event2.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected second event PartStartEvent, got %T", event2)
	}
	tool, ok := start2.Part.(core.ToolCallPart)
	if !ok {
		t.Fatalf("expected second part to be ToolCallPart, got %T", start2.Part)
	}
	if tool.ToolName != "search" {
		t.Fatalf("expected tool name search, got %q", tool.ToolName)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}

	resp := stream.Response()
	if len(resp.Parts) != 2 {
		t.Fatalf("expected 2 response parts, got %d", len(resp.Parts))
	}
	if _, ok := resp.Parts[0].(core.TextPart); !ok {
		t.Fatalf("expected response part 0 to be TextPart, got %T", resp.Parts[0])
	}
	if _, ok := resp.Parts[1].(core.ToolCallPart); !ok {
		t.Fatalf("expected response part 1 to be ToolCallPart, got %T", resp.Parts[1])
	}
}

func TestParseSSEStreamFinalPartOrderDeterministic(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-order","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"A","tool_calls":[{"index":0,"id":"call_order","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]

`

	for i := range 50 {
		body := io.NopCloser(strings.NewReader(sseData))
		stream := newStreamedResponse(body, "gpt-4o")
		for {
			_, err := stream.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("iteration %d: unexpected stream error: %v", i, err)
			}
		}

		resp := stream.Response()
		if len(resp.Parts) != 2 {
			t.Fatalf("iteration %d: expected 2 response parts, got %d", i, len(resp.Parts))
		}
		if _, ok := resp.Parts[0].(core.TextPart); !ok {
			t.Fatalf("iteration %d: expected response part 0 TextPart, got %T", i, resp.Parts[0])
		}
		if _, ok := resp.Parts[1].(core.ToolCallPart); !ok {
			t.Fatalf("iteration %d: expected response part 1 ToolCallPart, got %T", i, resp.Parts[1])
		}
	}
}

func TestNewProviderDefaults(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key-123")
	p := New()
	if p.model != defaultModel {
		t.Errorf("expected model %s, got %s", defaultModel, p.model)
	}
	if p.baseURL != defaultBaseURL {
		t.Errorf("expected baseURL %s, got %s", defaultBaseURL, p.baseURL)
	}
	if p.apiKey != "test-key-123" {
		t.Errorf("expected API key from env, got %s", p.apiKey)
	}
	if p.ModelName() != defaultModel {
		t.Errorf("expected ModelName() %s, got %s", defaultModel, p.ModelName())
	}
}

func TestNewProviderOptions(t *testing.T) {
	p := New(
		WithAPIKey("my-key"),
		WithModel("gpt-4o-mini"),
		WithBaseURL("https://custom.api.com"),
		WithMaxTokens(2048),
	)
	if p.apiKey != "my-key" {
		t.Errorf("expected API key my-key, got %s", p.apiKey)
	}
	if p.model != "gpt-4o-mini" {
		t.Errorf("expected model gpt-4o-mini, got %s", p.model)
	}
	if p.baseURL != "https://custom.api.com" {
		t.Errorf("expected baseURL https://custom.api.com, got %s", p.baseURL)
	}
	if p.maxTokens != 2048 {
		t.Errorf("expected maxTokens 2048, got %d", p.maxTokens)
	}
}

func TestNewLiteLLM(t *testing.T) {
	p := NewLiteLLM("http://localhost:4000", WithAPIKey("lm-key"), WithModel("claude-3"))
	if p.baseURL != "http://localhost:4000" {
		t.Errorf("expected baseURL http://localhost:4000, got %s", p.baseURL)
	}
	if p.apiKey != "lm-key" {
		t.Errorf("expected API key lm-key, got %s", p.apiKey)
	}
	if p.model != "claude-3" {
		t.Errorf("expected model claude-3, got %s", p.model)
	}
}

func TestRequestIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers.
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}

		// Verify request body.
		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "gpt-4o" {
			t.Errorf("expected model gpt-4o, got %s", req.Model)
		}

		// Return a response.
		resp := apiResponse{
			ID:     "chatcmpl-123",
			Object: "chat.completion",
			Choices: []apiChoice{
				{
					Message: apiChatMsg{
						Role:    "assistant",
						Content: "Hi there!",
					},
					FinishReason: "stop",
				},
			},
			Usage: apiUsage{
				PromptTokens:     5,
				CompletionTokens: 3,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(server.URL))
	result, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}},
			Timestamp: time.Now(),
		},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.TextContent() != "Hi there!" {
		t.Errorf("expected 'Hi there!', got '%s'", result.TextContent())
	}
	if result.Usage.InputTokens != 5 {
		t.Errorf("expected 5 input tokens, got %d", result.Usage.InputTokens)
	}
}

func TestRequestStreamIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		chunks := []string{
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" there"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
			`data: [DONE]`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "%s\n\n", chunk)
			flusher.Flush()
		}
	}))
	defer server.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(server.URL))
	stream, err := p.RequestStream(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}},
			Timestamp: time.Now(),
		},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	// Collect all text.
	var text strings.Builder
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch e := event.(type) {
		case core.PartStartEvent:
			if tp, ok := e.Part.(core.TextPart); ok {
				text.WriteString(tp.Content)
			}
		case core.PartDeltaEvent:
			if td, ok := e.Delta.(core.TextPartDelta); ok {
				text.WriteString(td.ContentDelta)
			}
		}
	}

	if text.String() != "Hi there" {
		t.Errorf("expected 'Hi there', got '%s'", text.String())
	}

	resp := stream.Response()
	if resp.Usage.InputTokens != 5 {
		t.Errorf("expected 5 input tokens, got %d", resp.Usage.InputTokens)
	}
}

func TestRequestHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"Rate limit exceeded"}}`))
	}))
	defer server.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(server.URL))
	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}},
		},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}

	var httpErr *core.ModelHTTPError
	if !isHTTPError(err, &httpErr) {
		t.Fatalf("expected ModelHTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", httpErr.StatusCode)
	}
}

// isHTTPError extracts a ModelHTTPError from an error.
func isHTTPError(err error, target **core.ModelHTTPError) bool {
	for {
		if e, ok := err.(*core.ModelHTTPError); ok {
			*target = e
			return true
		}
		if unwrapper, ok := err.(interface{ Unwrap() error }); ok {
			err = unwrapper.Unwrap()
		} else {
			return false
		}
	}
}

// --- Reasoning effort unit tests ---

func TestBuildRequestWithReasoningEffort(t *testing.T) {
	effort := "high"
	settings := &core.ModelSettings{
		ReasoningEffort: &effort,
	}

	req, err := buildRequest(nil, settings, nil, "o3-mini", 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.ReasoningEffort == nil {
		t.Fatal("expected ReasoningEffort to be set")
	}
	if *req.ReasoningEffort != "high" {
		t.Errorf("reasoning_effort = %q, want 'high'", *req.ReasoningEffort)
	}
}

func TestBuildRequestNoReasoningEffortByDefault(t *testing.T) {
	req, err := buildRequest(nil, nil, nil, "gpt-4o-mini", 4096, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.ReasoningEffort != nil {
		t.Errorf("expected ReasoningEffort to be nil by default, got %q", *req.ReasoningEffort)
	}
}

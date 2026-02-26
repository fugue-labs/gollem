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

func TestBuildResponsesRequestCodexOmitsSamplingParams(t *testing.T) {
	temp := 0.2
	topP := 0.9
	settings := &core.ModelSettings{
		Temperature: &temp,
		TopP:        &topP,
	}

	req, err := buildResponsesRequest(nil, settings, nil, "gpt-5.2-codex", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if req.Temperature != nil {
		t.Fatalf("expected temperature to be omitted for codex, got %v", *req.Temperature)
	}
	if req.TopP != nil {
		t.Fatalf("expected top_p to be omitted for codex, got %v", *req.TopP)
	}
}

func TestBuildResponsesRequestNormalizesObjectToolSchema(t *testing.T) {
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:             "task_list",
				ParametersSchema: core.Schema{"type": "object"},
			},
		},
	}

	req, err := buildResponsesRequest(nil, nil, params, "gpt-5.2-codex", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}

	var schema map[string]any
	if err := json.Unmarshal(req.Tools[0].Parameters, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties object in normalized schema, got: %#v", schema["properties"])
	}
	if len(props) != 0 {
		t.Fatalf("expected empty properties object, got %v", props)
	}
}

func TestConvertMessagesToResponsesInputAssistantUsesOutputText(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "sys"},
				core.UserPromptPart{Content: "hello"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "assistant reply"},
			},
		},
	}

	input, err := convertMessagesToResponsesInput(messages)
	if err != nil {
		t.Fatalf("convertMessagesToResponsesInput failed: %v", err)
	}
	if len(input) != 3 {
		t.Fatalf("expected 3 input items, got %d", len(input))
	}

	getContentType := func(item map[string]any) string {
		content, ok := item["content"].([]map[string]string)
		if ok && len(content) > 0 {
			return content[0]["type"]
		}
		if contentAny, ok := item["content"].([]any); ok && len(contentAny) > 0 {
			if m, ok := contentAny[0].(map[string]any); ok {
				if s, ok := m["type"].(string); ok {
					return s
				}
			}
		}
		return ""
	}

	if got := getContentType(input[0]); got != "input_text" {
		t.Fatalf("system content type = %q, want input_text", got)
	}
	if got := getContentType(input[1]); got != "input_text" {
		t.Fatalf("user content type = %q, want input_text", got)
	}
	if got := getContentType(input[2]); got != "output_text" {
		t.Fatalf("assistant content type = %q, want output_text", got)
	}
}

func TestBuildRequestMultimodalUserContent(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "sys"},
				core.UserPromptPart{Content: "analyze this board"},
				core.ImagePart{
					URL:      "data:image/png;base64,AAAA",
					MIMEType: "image/png",
					Detail:   "high",
				},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" {
		t.Fatalf("expected first role system, got %q", req.Messages[0].Role)
	}

	user := req.Messages[1]
	if user.Role != "user" {
		t.Fatalf("expected user role, got %q", user.Role)
	}
	if user.Content != "" {
		t.Fatalf("expected empty string content for multimodal user message, got %q", user.Content)
	}
	if len(user.ContentParts) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(user.ContentParts))
	}
	if user.ContentParts[0].Type != "text" || user.ContentParts[0].Text != "analyze this board" {
		t.Fatalf("unexpected first content part: %+v", user.ContentParts[0])
	}
	if user.ContentParts[1].Type != "image_url" {
		t.Fatalf("expected image_url part, got %q", user.ContentParts[1].Type)
	}
	if user.ContentParts[1].ImageURL == nil {
		t.Fatal("expected image_url payload")
	}
	if user.ContentParts[1].ImageURL.URL != "data:image/png;base64,AAAA" {
		t.Fatalf("unexpected image url: %q", user.ContentParts[1].ImageURL.URL)
	}
	if user.ContentParts[1].ImageURL.Detail != "high" {
		t.Fatalf("unexpected image detail: %q", user.ContentParts[1].ImageURL.Detail)
	}
}

func TestBuildRequestMultimodalFlushesBeforeToolResult(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "analyze image"},
				core.ImagePart{
					URL:      "data:image/png;base64,AAAA",
					MIMEType: "image/png",
				},
				core.ToolReturnPart{
					ToolName:   "extract",
					ToolCallID: "call_123",
					Content:    "ok",
				},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Fatalf("expected first message role user, got %q", req.Messages[0].Role)
	}
	if len(req.Messages[0].ContentParts) != 2 {
		t.Fatalf("expected first message to keep multimodal parts, got %d", len(req.Messages[0].ContentParts))
	}
	if req.Messages[1].Role != "tool" {
		t.Fatalf("expected second message role tool, got %q", req.Messages[1].Role)
	}
	if req.Messages[1].ToolCallID != "call_123" {
		t.Fatalf("expected tool_call_id call_123, got %q", req.Messages[1].ToolCallID)
	}
	if req.Messages[1].Content != "ok" {
		t.Fatalf("expected tool content ok, got %q", req.Messages[1].Content)
	}
}

func TestConvertMessagesToResponsesInputMultimodal(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "sys"},
				core.UserPromptPart{Content: "analyze board"},
				core.ImagePart{
					URL:      "data:image/png;base64,AAAA",
					MIMEType: "image/png",
					Detail:   "high",
				},
				core.RetryPromptPart{Content: "be concise"},
				core.ToolReturnPart{
					ToolName:   "engine",
					ToolCallID: "call_1",
					Content:    map[string]any{"ok": true},
				},
			},
		},
	}

	input, err := convertMessagesToResponsesInput(messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 3 {
		t.Fatalf("expected 3 input entries, got %d", len(input))
	}

	if got := input[0]["role"]; got != "system" {
		t.Fatalf("expected first role system, got %#v", got)
	}

	user := input[1]
	if got := user["role"]; got != "user" {
		t.Fatalf("expected second role user, got %#v", got)
	}
	content, ok := user["content"].([]map[string]any)
	if !ok {
		t.Fatalf("expected user content []map[string]any, got %T", user["content"])
	}
	if len(content) != 3 {
		t.Fatalf("expected 3 user content items, got %d", len(content))
	}
	if content[0]["type"] != "input_text" || content[0]["text"] != "analyze board" {
		t.Fatalf("unexpected first user content: %#v", content[0])
	}
	if content[1]["type"] != "input_image" || content[1]["image_url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("unexpected second user content: %#v", content[1])
	}
	if content[1]["detail"] != "high" {
		t.Fatalf("unexpected image detail: %#v", content[1]["detail"])
	}
	if content[2]["type"] != "input_text" || content[2]["text"] != "be concise" {
		t.Fatalf("unexpected third user content: %#v", content[2])
	}

	tool := input[2]
	if tool["type"] != "function_call_output" || tool["call_id"] != "call_1" {
		t.Fatalf("unexpected tool output envelope: %#v", tool)
	}
	if tool["output"] != `{"ok":true}` {
		t.Fatalf("unexpected tool output content: %#v", tool["output"])
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

func TestBuildRequestNormalizesObjectToolSchema(t *testing.T) {
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:             "task_list",
				ParametersSchema: core.Schema{"type": "object"},
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

	var schema map[string]any
	if err := json.Unmarshal(req.Tools[0].Function.Parameters, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties object in normalized schema, got: %#v", schema["properties"])
	}
	if len(props) != 0 {
		t.Fatalf("expected empty properties object, got %v", props)
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

func TestBuildRequestNormalizesObjectOutputSchema(t *testing.T) {
	params := &core.ModelRequestParameters{
		OutputMode: core.OutputModeNative,
		OutputObject: &core.OutputObjectDefinition{
			Name:       "result",
			JSONSchema: core.Schema{"type": "object"},
		},
	}

	req, err := buildRequest(nil, nil, params, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.ResponseFormat == nil || req.ResponseFormat.JSONSchema == nil {
		t.Fatal("expected response_format json_schema")
	}

	var schema map[string]any
	if err := json.Unmarshal(req.ResponseFormat.JSONSchema.Schema, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties object in normalized output schema, got: %#v", schema["properties"])
	}
	if len(props) != 0 {
		t.Fatalf("expected empty properties object, got %v", props)
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
	t.Setenv("OPENAI_PROMPT_CACHE_KEY", "repo:gollem")
	t.Setenv("OPENAI_PROMPT_CACHE_RETENTION", "in_memory")
	t.Setenv("OPENAI_SERVICE_TIER", "priority")
	t.Setenv("OPENAI_TRANSPORT", "websocket")
	t.Setenv("OPENAI_WEBSOCKET_HTTP_FALLBACK", "1")
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
	if p.promptCacheKey != "repo:gollem" {
		t.Errorf("expected prompt cache key from env, got %q", p.promptCacheKey)
	}
	if p.promptCacheRetention != "in_memory" {
		t.Errorf("expected prompt cache retention from env, got %q", p.promptCacheRetention)
	}
	if p.serviceTier != "priority" {
		t.Errorf("expected service tier from env, got %q", p.serviceTier)
	}
	if p.transport != transportWebSocket {
		t.Errorf("expected transport websocket from env, got %q", p.transport)
	}
	if !p.wsHTTPFallback {
		t.Errorf("expected websocket HTTP fallback from env to be enabled")
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
		WithPromptCacheKey("stable-key"),
		WithPromptCacheRetention("24h"),
		WithServiceTier("priority"),
		WithTransport("websocket"),
		WithWebSocketHTTPFallback(true),
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
	if p.promptCacheKey != "stable-key" {
		t.Errorf("expected prompt cache key stable-key, got %q", p.promptCacheKey)
	}
	if p.promptCacheRetention != "24h" {
		t.Errorf("expected prompt cache retention 24h, got %q", p.promptCacheRetention)
	}
	if p.serviceTier != "priority" {
		t.Errorf("expected service tier priority, got %q", p.serviceTier)
	}
	if p.transport != transportWebSocket {
		t.Errorf("expected transport websocket, got %q", p.transport)
	}
	if !p.wsHTTPFallback {
		t.Errorf("expected websocket HTTP fallback to be enabled")
	}
}

func TestNormalizeTransport(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: transportHTTP},
		{in: "http", want: transportHTTP},
		{in: "websocket", want: transportWebSocket},
		{in: "WS", want: transportWebSocket},
		{in: "unknown", want: transportHTTP},
	}
	for _, tt := range tests {
		if got := normalizeTransport(tt.in); got != tt.want {
			t.Fatalf("normalizeTransport(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWithWebSocketHTTPFallbackOptionOverridesEnv(t *testing.T) {
	t.Setenv("OPENAI_WEBSOCKET_HTTP_FALLBACK", "1")
	p := New(WithWebSocketHTTPFallback(false))
	if p.wsHTTPFallback {
		t.Fatalf("expected explicit option false to override env value 1")
	}

	t.Setenv("OPENAI_WEBSOCKET_HTTP_FALLBACK", "0")
	p = New(WithWebSocketHTTPFallback(true))
	if !p.wsHTTPFallback {
		t.Fatalf("expected explicit option true to override env value 0")
	}
}

func TestProviderNewSessionIsolatesWebSocketState(t *testing.T) {
	p := New(
		WithAPIKey("my-key"),
		WithModel("gpt-5.3-codex"),
		WithBaseURL("https://api.openai.com"),
		WithPromptCacheKey("stable-key"),
		WithPromptCacheRetention("24h"),
		WithServiceTier("priority"),
		WithTransport("websocket"),
		WithWebSocketHTTPFallback(true),
	)
	p.useResponses = true
	p.wsConn = &responsesWebSocketConn{}
	p.wsPrevResponseID = "resp_old"
	p.wsLastInputSigs = []string{"a", "b"}

	session, ok := p.NewSession().(*Provider)
	if !ok {
		t.Fatalf("expected *Provider from NewSession, got %T", p.NewSession())
	}
	if session == p {
		t.Fatal("expected NewSession to return a distinct provider instance")
	}
	if session.apiKey != p.apiKey || session.model != p.model || session.baseURL != p.baseURL {
		t.Fatal("session provider should preserve core config fields")
	}
	if !session.wsHTTPFallback || session.transport != transportWebSocket || !session.useResponses {
		t.Fatalf("session provider should preserve websocket config, got fallback=%v transport=%q useResponses=%v",
			session.wsHTTPFallback, session.transport, session.useResponses)
	}
	if session.wsConn != nil || session.wsPrevResponseID != "" || len(session.wsLastInputSigs) != 0 {
		t.Fatal("session provider must start with empty websocket session state")
	}
}

func TestProviderCloseResetsWebSocketState(t *testing.T) {
	p := New(
		WithAPIKey("my-key"),
		WithModel("gpt-5.3-codex"),
		WithTransport("websocket"),
	)
	p.wsConn = &responsesWebSocketConn{}
	p.wsPrevResponseID = "resp_old"
	p.wsLastInputSigs = []string{"a", "b"}

	if err := p.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if p.wsConn != nil || p.wsPrevResponseID != "" || len(p.wsLastInputSigs) != 0 {
		t.Fatal("expected Close to clear websocket state")
	}
	// Idempotent close should be a no-op.
	if err := p.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
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

func TestNewOllama(t *testing.T) {
	p := NewOllama(WithModel("llama3"))
	if p.baseURL != "http://localhost:11434" {
		t.Errorf("expected baseURL http://localhost:11434, got %s", p.baseURL)
	}
	if p.apiKey != "ollama" {
		t.Errorf("expected API key ollama, got %s", p.apiKey)
	}
	if p.model != "llama3" {
		t.Errorf("expected model llama3, got %s", p.model)
	}
}

func TestNewOllamaDefaults(t *testing.T) {
	p := NewOllama()
	if p.baseURL != "http://localhost:11434" {
		t.Errorf("expected baseURL http://localhost:11434, got %s", p.baseURL)
	}
	if p.apiKey != "ollama" {
		t.Errorf("expected API key ollama, got %s", p.apiKey)
	}
	if p.model != defaultModel {
		t.Errorf("expected default model %s, got %s", defaultModel, p.model)
	}
}

func TestNewOllamaCustomURL(t *testing.T) {
	p := NewOllama(WithBaseURL("http://remote:11434"), WithModel("mistral"))
	if p.baseURL != "http://remote:11434" {
		t.Errorf("expected baseURL http://remote:11434, got %s", p.baseURL)
	}
	if p.model != "mistral" {
		t.Errorf("expected model mistral, got %s", p.model)
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
		if req.PromptCacheKey != "stable-key" {
			t.Errorf("expected prompt_cache_key stable-key, got %q", req.PromptCacheKey)
		}
		if req.PromptCacheRetention != "in_memory" {
			t.Errorf("expected prompt_cache_retention in_memory, got %q", req.PromptCacheRetention)
		}
		if req.ServiceTier != "priority" {
			t.Errorf("expected service_tier priority, got %q", req.ServiceTier)
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

	p := New(
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithPromptCacheKey("stable-key"),
		WithPromptCacheRetention("in_memory"),
		WithServiceTier("priority"),
	)
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

func TestRequestFallsBackToResponsesForCodex(t *testing.T) {
	chatHits := 0
	responsesHits := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			chatHits++
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"message":"This is not a chat model and thus not supported in the v1/chat/completions endpoint. Did you mean to use v1/completions?"}}`))
		case "/v1/responses":
			responsesHits++
			var req responsesRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode responses request: %v", err)
			}
			if req.Model != "gpt-5.2-codex" {
				t.Fatalf("expected model gpt-5.2-codex, got %q", req.Model)
			}
			if req.PromptCacheKey != "stable-key" {
				t.Fatalf("expected prompt_cache_key stable-key, got %q", req.PromptCacheKey)
			}
			if req.PromptCacheRetention != "in_memory" {
				t.Fatalf("expected prompt_cache_retention in_memory, got %q", req.PromptCacheRetention)
			}
			if req.ServiceTier != "priority" {
				t.Fatalf("expected service_tier priority, got %q", req.ServiceTier)
			}
			resp := responsesAPIResponse{
				ID:    "resp_123",
				Model: "gpt-5.2-codex",
				Output: []responsesOutputItem{
					{
						Type:      "function_call",
						Name:      "run_cmd",
						CallID:    "call_1",
						Arguments: `{"cmd":"ls"}`,
					},
				},
				Usage: responsesUsage{
					InputTokens:  12,
					OutputTokens: 5,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithModel("gpt-5.2-codex"),
		WithPromptCacheKey("stable-key"),
		WithPromptCacheRetention("in_memory"),
		WithServiceTier("priority"),
	)

	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:             "run_cmd",
				ParametersSchema: core.Schema{"type": "object"},
			},
		},
	}

	resp, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "list files"},
			},
		},
	}, nil, params)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if chatHits != 0 {
		t.Fatalf("expected no chat requests for codex model, got %d", chatHits)
	}
	if responsesHits != 1 {
		t.Fatalf("expected exactly one responses request, got %d", responsesHits)
	}
	toolCalls := resp.ToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].ToolName != "run_cmd" {
		t.Fatalf("expected tool run_cmd, got %q", toolCalls[0].ToolName)
	}
}

func TestRequestRetriesOnChatMismatchThenPinsResponses(t *testing.T) {
	chatHits := 0
	responsesHits := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			chatHits++
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"message":"This is not a chat model and thus not supported in the v1/chat/completions endpoint. Did you mean to use v1/completions?"}}`))
		case "/v1/responses":
			responsesHits++
			resp := responsesAPIResponse{
				ID:    "resp_123",
				Model: "gpt-5.2",
				Output: []responsesOutputItem{
					{
						Type: "message",
						Role: "assistant",
						Content: []responsesContentItem{
							{Type: "output_text", Text: "ok"},
						},
					},
				},
				Usage: responsesUsage{
					InputTokens:  3,
					OutputTokens: 1,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	p := New(
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithModel("gpt-5.2"),
	)

	for i := range 2 {
		resp, err := p.Request(context.Background(), []core.ModelMessage{
			core.ModelRequest{
				Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hi"}},
			},
		}, nil, nil)
		if err != nil {
			t.Fatalf("request %d failed: %v", i+1, err)
		}
		if got := resp.TextContent(); got != "ok" {
			t.Fatalf("request %d text = %q, want ok", i+1, got)
		}
	}

	if chatHits != 1 {
		t.Fatalf("expected 1 chat attempt before fallback pinning, got %d", chatHits)
	}
	if responsesHits != 2 {
		t.Fatalf("expected 2 responses calls, got %d", responsesHits)
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

func TestParseSSEStreamError(t *testing.T) {
	// OpenAI-compatible APIs may send error objects mid-stream.
	sseData := `data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}

data: {"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}

data: [DONE]

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	// First event should be the partial text.
	event1, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := event1.(core.PartStartEvent); !ok {
		t.Fatalf("expected PartStartEvent, got %T", event1)
	}

	// Next call should return the stream error.
	_, err = stream.Next()
	if err == nil {
		t.Fatal("expected error from stream")
	}
	if !strings.Contains(err.Error(), "Rate limit exceeded") {
		t.Errorf("expected error to contain 'Rate limit exceeded', got: %v", err)
	}
	if !strings.Contains(err.Error(), "rate_limit_error") {
		t.Errorf("expected error to contain 'rate_limit_error', got: %v", err)
	}
}

func TestParseSSEStreamErrorOnly(t *testing.T) {
	// Error sent before any content.
	sseData := `data: {"error":{"message":"Server overloaded","type":"server_error"}}

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	_, err := stream.Next()
	if err == nil {
		t.Fatal("expected error from stream")
	}
	if !strings.Contains(err.Error(), "Server overloaded") {
		t.Errorf("expected error to contain 'Server overloaded', got: %v", err)
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

// TestParseResponseEmptyToolCallIDs verifies that tool calls with empty IDs
// (common with Ollama, LiteLLM) get synthetic IDs so they can be matched
// in conversation history.
func TestParseResponseEmptyToolCallIDs(t *testing.T) {
	resp := &apiResponse{
		Choices: []apiChoice{
			{
				Message: apiChatMsg{
					Role: "assistant",
					ToolCalls: []apiToolCall{
						{
							ID:   "", // Ollama returns empty IDs
							Type: "function",
							Function: apiToolFunction{
								Name:      "bash",
								Arguments: `{"command":"ls"}`,
							},
						},
						{
							ID:   "", // Second call also empty
							Type: "function",
							Function: apiToolFunction{
								Name:      "view",
								Arguments: `{"file":"main.go"}`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
	}
	result := parseResponse(resp, "ollama/llama3")

	if len(result.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(result.Parts))
	}

	// Both tool calls should have non-empty synthetic IDs.
	tc0, ok := result.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatalf("part[0]: expected ToolCallPart, got %T", result.Parts[0])
	}
	if tc0.ToolCallID == "" {
		t.Error("tc0: expected non-empty synthetic ToolCallID")
	}
	if tc0.ToolCallID != "call_0" {
		t.Errorf("tc0: expected 'call_0', got %q", tc0.ToolCallID)
	}

	tc1, ok := result.Parts[1].(core.ToolCallPart)
	if !ok {
		t.Fatalf("part[1]: expected ToolCallPart, got %T", result.Parts[1])
	}
	if tc1.ToolCallID == "" {
		t.Error("tc1: expected non-empty synthetic ToolCallID")
	}
	if tc1.ToolCallID != "call_1" {
		t.Errorf("tc1: expected 'call_1', got %q", tc1.ToolCallID)
	}

	// IDs should be unique.
	if tc0.ToolCallID == tc1.ToolCallID {
		t.Errorf("tool call IDs should be unique, both are %q", tc0.ToolCallID)
	}
}

// TestParseSSEStreamEmptyToolCallIDs verifies that streaming tool calls
// with empty IDs (from Ollama/LiteLLM) get synthetic IDs in the final response.
func TestParseSSEStreamEmptyToolCallIDs(t *testing.T) {
	// Simulate Ollama streaming: tool calls with empty IDs.
	sseData := `data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"","type":"function","function":{"name":"view","arguments":"{\"file\":\"main.go\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "ollama/llama3")

	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	resp := stream.Response()
	if len(resp.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(resp.Parts))
	}

	tc0, ok := resp.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatalf("part[0]: expected ToolCallPart, got %T", resp.Parts[0])
	}
	if tc0.ToolCallID == "" {
		t.Error("tc0: expected non-empty synthetic ToolCallID for streaming")
	}

	tc1, ok := resp.Parts[1].(core.ToolCallPart)
	if !ok {
		t.Fatalf("part[1]: expected ToolCallPart, got %T", resp.Parts[1])
	}
	if tc1.ToolCallID == "" {
		t.Error("tc1: expected non-empty synthetic ToolCallID for streaming")
	}

	if tc0.ToolCallID == tc1.ToolCallID {
		t.Errorf("streaming tool call IDs should be unique, both are %q", tc0.ToolCallID)
	}
}

// TestBuildRequestSkipsEmptyAssistantMessage verifies that a ModelResponse
// containing only unsupported parts (e.g., ThinkingPart) does not produce an
// empty assistant message. This matters when conversations built with Anthropic
// (which supports ThinkingPart) are replayed through the OpenAI provider via
// FallbackModel or checkpoint resumption.
func TestBuildRequestSkipsEmptyAssistantMessage(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Think about this"},
			},
		},
		// A ModelResponse with only ThinkingPart — unsupported by OpenAI.
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ThinkingPart{Content: "Let me think...", Signature: "sig123"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "What did you think?"},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 2 messages: user + user. The ThinkingPart-only response
	// should be skipped, not produce an empty assistant message.
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages (skipping empty assistant), got %d", len(req.Messages))
	}
	for i, msg := range req.Messages {
		if msg.Role != "user" {
			t.Errorf("message[%d]: expected role 'user', got %q", i, msg.Role)
		}
	}
}

// TestBuildRequestKeepsNonEmptyAssistantMessage ensures that assistant messages
// with real content (TextPart or ToolCallPart) are still included.
func TestBuildRequestKeepsNonEmptyAssistantMessage(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello"},
			},
		},
		// A ModelResponse with ThinkingPart + TextPart — TextPart should remain.
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ThinkingPart{Content: "thinking...", Signature: "sig"},
				core.TextPart{Content: "Hello back!"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Thanks"},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 3 messages: user + assistant + user.
	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("msg[0]: expected 'user', got %q", req.Messages[0].Role)
	}
	if req.Messages[1].Role != "assistant" {
		t.Errorf("msg[1]: expected 'assistant', got %q", req.Messages[1].Role)
	}
	if req.Messages[1].Content != "Hello back!" {
		t.Errorf("msg[1]: expected 'Hello back!', got %q", req.Messages[1].Content)
	}
	if req.Messages[2].Role != "user" {
		t.Errorf("msg[2]: expected 'user', got %q", req.Messages[2].Role)
	}
}

// TestBuildRequestEmptyPartsResponse verifies that a ModelResponse with no
// parts (Parts: nil or empty) doesn't produce an empty assistant message.
func TestBuildRequestEmptyPartsResponse(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello"},
			},
		},
		core.ModelResponse{Parts: nil}, // empty response
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello again"},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}

	// Should skip the empty response.
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages (skipping empty response), got %d", len(req.Messages))
	}
	for _, msg := range req.Messages {
		if msg.Role != "user" {
			t.Errorf("expected 'user' role, got %q", msg.Role)
		}
	}
}

// TestNewProviderBaseURLNormalization verifies that trailing /v1 is stripped
// from the base URL to prevent double /v1/v1/... in API paths. The OpenAI
// Python client convention uses OPENAI_BASE_URL with /v1, but our endpoint
// path already includes /v1.
func TestNewProviderBaseURLNormalization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain URL", "https://api.openai.com", "https://api.openai.com"},
		{"trailing slash", "https://api.openai.com/", "https://api.openai.com"},
		{"trailing /v1", "https://api.x.ai/v1", "https://api.x.ai"},
		{"trailing /v1/", "https://api.x.ai/v1/", "https://api.x.ai"},
		{"localhost with /v1", "http://localhost:4000/v1", "http://localhost:4000"},
		{"no /v1", "http://localhost:4000", "http://localhost:4000"},
		{"v1 in path segment", "https://api.v1.example.com", "https://api.v1.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(WithBaseURL(tt.input), WithAPIKey("test"))
			if p.baseURL != tt.expected {
				t.Errorf("baseURL = %q, want %q", p.baseURL, tt.expected)
			}
		})
	}
}

// TestNewProviderBaseURLEnvNormalization verifies that OPENAI_BASE_URL with
// /v1 is normalized when no explicit base URL is set.
func TestNewProviderBaseURLEnvNormalization(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", "https://api.x.ai/v1")
	p := New()
	if p.baseURL != "https://api.x.ai" {
		t.Errorf("baseURL = %q, want %q", p.baseURL, "https://api.x.ai")
	}
}

// TestParseSSEStreamNoSpaceAfterColon verifies the OpenAI stream parser
// handles SSE data lines without the optional space after the colon,
// per the SSE specification.
func TestParseSSEStreamNoSpaceAfterColon(t *testing.T) {
	// "data:" without trailing space is valid per SSE spec.
	sseData := `data:{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

data:{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}

data:[DONE]

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	var text strings.Builder
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
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

	if text.String() != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", text.String())
	}

	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	tp, ok := resp.Parts[0].(core.TextPart)
	if !ok {
		t.Fatal("expected TextPart")
	}
	if tp.Content != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", tp.Content)
	}
}

// --- Tool choice modes ---

func TestBuildRequestToolChoiceModes(t *testing.T) {
	tests := []struct {
		name     string
		choice   *core.ToolChoice
		expected any
	}{
		{
			name:     "none",
			choice:   &core.ToolChoice{Mode: "none"},
			expected: "none",
		},
		{
			name:     "required",
			choice:   &core.ToolChoice{Mode: "required"},
			expected: "required",
		},
		{
			name:     "auto",
			choice:   &core.ToolChoice{Mode: "auto"},
			expected: "auto",
		},
		{
			name:   "specific tool",
			choice: &core.ToolChoice{ToolName: "get_weather"},
			expected: map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": "get_weather",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &core.ModelSettings{ToolChoice: tt.choice}
			req, err := buildRequest(nil, settings, nil, "gpt-4o", 4096, false)
			if err != nil {
				t.Fatal(err)
			}
			// Marshal both to JSON for comparison.
			got, _ := json.Marshal(req.ToolChoice)
			want, _ := json.Marshal(tt.expected)
			if string(got) != string(want) {
				t.Errorf("tool_choice = %s, want %s", got, want)
			}
		})
	}
}

// --- Unsupported request part ---
// The ModelRequestPart interface has an unexported method, so we can't create
// a custom type outside the core package. Instead, we don't test this error
// path directly as it's unreachable from production code without adding a
// new part type to core.

// --- ToolReturn with non-string content ---

func TestBuildRequestToolReturnNonString(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "search",
					Content:    map[string]any{"results": []string{"a", "b"}},
					ToolCallID: "call_99",
				},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	msg := req.Messages[0]
	if msg.Role != "tool" {
		t.Errorf("expected role tool, got %s", msg.Role)
	}
	// Non-string content should be JSON-marshaled.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(msg.Content), &parsed); err != nil {
		t.Fatalf("expected JSON content, got: %s", msg.Content)
	}
}

// --- ResponseFormat with strict=false ---

func TestBuildRequestResponseFormatStrictFalse(t *testing.T) {
	strictFalse := false
	params := &core.ModelRequestParameters{
		OutputMode: core.OutputModeNative,
		OutputObject: &core.OutputObjectDefinition{
			Name:       "result",
			JSONSchema: core.Schema{"type": "object"},
			Strict:     &strictFalse,
		},
	}
	req, err := buildRequest(nil, nil, params, "gpt-4o", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.ResponseFormat == nil {
		t.Fatal("expected response_format")
	}
	if req.ResponseFormat.JSONSchema.Strict {
		t.Error("expected strict=false")
	}
}

// --- Strict tool ---

func TestBuildRequestStrictTool(t *testing.T) {
	strict := true
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:             "calc",
				Description:      "Calculator",
				ParametersSchema: core.Schema{"type": "object"},
				Strict:           &strict,
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
	if req.Tools[0].Function.Strict == nil || !*req.Tools[0].Function.Strict {
		t.Error("expected strict=true on tool")
	}
}

// --- parseResponse: refusal ---

func TestParseResponseRefusal(t *testing.T) {
	resp := &apiResponse{
		Choices: []apiChoice{
			{
				Message: apiChatMsg{
					Role:    "assistant",
					Refusal: "I cannot help with that request.",
				},
				FinishReason: "stop",
			},
		},
	}
	result := parseResponse(resp, "gpt-4o")
	if len(result.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(result.Parts))
	}
	tp, ok := result.Parts[0].(core.TextPart)
	if !ok {
		t.Fatal("expected TextPart for refusal")
	}
	if tp.Content != "I cannot help with that request." {
		t.Errorf("unexpected refusal content: %s", tp.Content)
	}
}

// --- parseResponse: empty choices ---

func TestParseResponseEmptyChoices(t *testing.T) {
	resp := &apiResponse{Choices: nil}
	result := parseResponse(resp, "gpt-4o")
	if len(result.Parts) != 0 {
		t.Errorf("expected 0 parts for empty choices, got %d", len(result.Parts))
	}
	// mapFinishReason with empty choices should return stop.
	if result.FinishReason != core.FinishReasonStop {
		t.Errorf("expected FinishReasonStop, got %s", result.FinishReason)
	}
}

// --- parseResponse: empty args gets "{}" ---

func TestParseResponseEmptyToolArgs(t *testing.T) {
	resp := &apiResponse{
		Choices: []apiChoice{
			{
				Message: apiChatMsg{
					Role: "assistant",
					ToolCalls: []apiToolCall{
						{
							ID:   "call_x",
							Type: "function",
							Function: apiToolFunction{
								Name:      "noop",
								Arguments: "",
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
	}
	result := parseResponse(resp, "gpt-4o")
	tc, ok := result.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatal("expected ToolCallPart")
	}
	if tc.ArgsJSON != "{}" {
		t.Errorf("expected empty args to be '{}', got %q", tc.ArgsJSON)
	}
}

// --- mapUsage: cache + reasoning tokens ---

func TestMapUsageFull(t *testing.T) {
	u := apiUsage{
		PromptTokens:     100,
		CompletionTokens: 50,
		PromptTokensDetails: &apiPromptTokensDetails{
			CachedTokens: 30,
		},
		CompletionTokensDetails: &apiCompletionDetails{
			ReasoningTokens: 20,
		},
	}
	usage := mapUsage(u)
	if usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", usage.InputTokens)
	}
	if usage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 30 {
		t.Errorf("CacheReadTokens = %d, want 30", usage.CacheReadTokens)
	}
	if usage.Details == nil || usage.Details["reasoning_tokens"] != 20 {
		t.Errorf("expected reasoning_tokens=20 in details, got %v", usage.Details)
	}
}

// --- mapFinishReasonStr: all variants ---

func TestMapFinishReasonStrAll(t *testing.T) {
	tests := []struct {
		reason   string
		expected core.FinishReason
	}{
		{"stop", core.FinishReasonStop},
		{"length", core.FinishReasonLength},
		{"tool_calls", core.FinishReasonToolCall},
		{"content_filter", core.FinishReasonContentFilter},
		{"unknown_reason", core.FinishReasonStop},
		{"", core.FinishReasonStop},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			got := mapFinishReasonStr(tt.reason)
			if got != tt.expected {
				t.Errorf("mapFinishReasonStr(%q) = %s, want %s", tt.reason, got, tt.expected)
			}
		})
	}
}

// --- WithHTTPClient option ---

func TestWithHTTPClientOption(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	p := New(WithAPIKey("test"), WithHTTPClient(client))
	if p.httpClient != client {
		t.Error("expected custom HTTP client to be set")
	}
}

// --- doRequest: Retry-After header ---

func TestRequestHTTPErrorRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
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
		t.Fatalf("expected ModelHTTPError, got %T", err)
	}
	if httpErr.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", httpErr.RetryAfter)
	}
}

// --- RequestStream HTTP error ---

func TestRequestStreamHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"Internal server error"}}`))
	}))
	defer server.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(server.URL))
	_, err := p.RequestStream(context.Background(), []core.ModelMessage{
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
	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", httpErr.StatusCode)
	}
}

// --- Stream: non-JSON data line skipped ---

func TestParseSSEStreamSkipsInvalidJSON(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}

data: not-valid-json

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":"stop"}]}

data: [DONE]

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

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
	if text.String() != "Hi!" {
		t.Errorf("expected 'Hi!', got %q", text.String())
	}
}

// --- Stream: non-data lines skipped ---

func TestParseSSEStreamSkipsNonDataLines(t *testing.T) {
	sseData := `event: message
: comment line
data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":"stop"}]}

data: [DONE]

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	event, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	start, ok := event.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent, got %T", event)
	}
	if tp, ok := start.Part.(core.TextPart); !ok || tp.Content != "OK" {
		t.Errorf("expected 'OK', got %v", start.Part)
	}
}

// --- Stream: chunk with no choices skipped ---

func TestParseSSEStreamSkipsNoChoices(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":"stop"}]}

data: [DONE]

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	event, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	start, ok := event.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent, got %T", event)
	}
	if tp, ok := start.Part.(core.TextPart); !ok || tp.Content != "Hi" {
		t.Errorf("expected 'Hi', got %v", start.Part)
	}
	// Usage should still be captured from the no-choices chunk.
	if stream.Usage().InputTokens != 5 {
		t.Errorf("expected 5 input tokens from usage-only chunk, got %d", stream.Usage().InputTokens)
	}
}

// --- Stream: finish reason "length" ---

func TestParseSSEStreamFinishReasonLength(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"text"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}

data: [DONE]

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	resp := stream.Response()
	if resp.FinishReason != core.FinishReasonLength {
		t.Errorf("expected FinishReasonLength, got %s", resp.FinishReason)
	}
}

// --- Stream: two tool calls with delta ---

func TestParseSSEStreamTwoToolCallsWithDelta(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"search","arguments":""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":\"x\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"view","arguments":"{\"f\":\"y\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	resp := stream.Response()
	if len(resp.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(resp.Parts))
	}
	tc0, ok := resp.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatalf("part 0: expected ToolCallPart, got %T", resp.Parts[0])
	}
	if tc0.ToolName != "search" || tc0.ArgsJSON != `{"q":"x"}` {
		t.Errorf("tc0: unexpected %+v", tc0)
	}
	tc1, ok := resp.Parts[1].(core.ToolCallPart)
	if !ok {
		t.Fatalf("part 1: expected ToolCallPart, got %T", resp.Parts[1])
	}
	if tc1.ToolName != "view" || tc1.ArgsJSON != `{"f":"y"}` {
		t.Errorf("tc1: unexpected %+v", tc1)
	}
}

// --- Stream: Usage() accessor ---

func TestStreamUsageAccessor(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}}

data: [DONE]

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	usage := stream.Usage()
	if usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", usage.InputTokens)
	}
	if usage.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d, want 5", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 3 {
		t.Errorf("CacheReadTokens = %d, want 3", usage.CacheReadTokens)
	}
	if usage.Details == nil || usage.Details["reasoning_tokens"] != 2 {
		t.Errorf("expected reasoning_tokens=2, got %v", usage.Details)
	}
}

// --- Stream: EOF without [DONE] ---

func TestParseSSEStreamEOFWithoutDone(t *testing.T) {
	// Some servers close the connection without sending [DONE].
	sseData := `data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":"stop"}]}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	event, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := event.(core.PartStartEvent); !ok {
		t.Fatalf("expected PartStartEvent, got %T", event)
	}

	// Next should be EOF (stream ended without [DONE]).
	_, err = stream.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}

	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
}

// --- Stream: empty tool call args get "{}" in finalization ---

func TestParseSSEStreamEmptyToolCallArgs(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_e","type":"function","function":{"name":"ping","arguments":""}}]},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gpt-4o")

	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	tc, ok := resp.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatal("expected ToolCallPart")
	}
	if tc.ArgsJSON != "{}" {
		t.Errorf("expected empty args to be '{}', got %q", tc.ArgsJSON)
	}
}

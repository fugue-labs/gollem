package vertexai_anthropic

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

	"golang.org/x/oauth2"

	"github.com/fugue-labs/gollem/core"
)

func TestEndpointConstruction(t *testing.T) {
	p := New(WithProject("my-project"), WithLocation("us-east5"), WithModel(Claude4Sonnet))
	expected := "https://us-east5-aiplatform.googleapis.com/v1/projects/my-project/locations/us-east5/publishers/anthropic/models/claude-sonnet-4-5:rawPredict"
	if p.endpoint() != expected {
		t.Errorf("expected endpoint %s, got %s", expected, p.endpoint())
	}
}

func TestStreamEndpointConstruction(t *testing.T) {
	p := New(WithProject("my-project"), WithLocation("us-east5"), WithModel(Claude4Sonnet))
	expected := "https://us-east5-aiplatform.googleapis.com/v1/projects/my-project/locations/us-east5/publishers/anthropic/models/claude-sonnet-4-5:streamRawPredict"
	if p.streamEndpoint() != expected {
		t.Errorf("expected endpoint %s, got %s", expected, p.streamEndpoint())
	}
}

func TestBuildRequestHasAnthropicVersion(t *testing.T) {
	req, err := buildRequest(nil, nil, nil, "claude-sonnet-4-5", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.AnthropicVersion != anthropicVersion {
		t.Errorf("expected anthropic_version %s, got %s", anthropicVersion, req.AnthropicVersion)
	}
}

func TestBuildRequestAnthropicFormat(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "You are helpful."},
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, "claude-sonnet-4-5", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	// System blocks should be set.
	if len(req.System) != 1 {
		t.Fatalf("expected 1 system block, got %d", len(req.System))
	}
	if req.System[0].Text != "You are helpful." {
		t.Errorf("unexpected system text: %s", req.System[0].Text)
	}
	// User message.
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("expected role user, got %s", req.Messages[0].Role)
	}
}

func TestParseResponse(t *testing.T) {
	resp := &apiResponse{
		Content: []apiContentBlock{
			{Type: "text", Text: "Hello there!"},
		},
		StopReason: "end_turn",
		Usage: apiUsage{
			InputTokens:  10,
			OutputTokens: 5,
		},
	}
	result := parseResponse(resp, "claude-sonnet-4-5")
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
}

func TestParseResponseToolCall(t *testing.T) {
	resp := &apiResponse{
		Content: []apiContentBlock{
			{
				Type:  "tool_use",
				ID:    "call_123",
				Name:  "get_weather",
				Input: json.RawMessage(`{"city":"NYC"}`),
			},
		},
		StopReason: "tool_use",
	}
	result := parseResponse(resp, "claude-sonnet-4-5")
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
	if tc.ToolCallID != "call_123" {
		t.Errorf("expected call_123, got %s", tc.ToolCallID)
	}
}

func TestNewProviderDefaults(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	p := New()
	if p.model != defaultModel {
		t.Errorf("expected model %s, got %s", defaultModel, p.model)
	}
	if p.location != defaultLocation {
		t.Errorf("expected location %s, got %s", defaultLocation, p.location)
	}
	if p.project != "test-project" {
		t.Errorf("expected project test-project, got %s", p.project)
	}
}

func TestNewProviderOptions(t *testing.T) {
	p := New(
		WithProject("my-project"),
		WithLocation("europe-west1"),
		WithModel(Claude4Opus),
		WithMaxTokens(8192),
	)
	if p.project != "my-project" {
		t.Errorf("expected project my-project, got %s", p.project)
	}
	if p.location != "europe-west1" {
		t.Errorf("expected location europe-west1, got %s", p.location)
	}
	if p.model != Claude4Opus {
		t.Errorf("expected model %s, got %s", Claude4Opus, p.model)
	}
	if p.maxTokens != 8192 {
		t.Errorf("expected maxTokens 8192, got %d", p.maxTokens)
	}
}

func TestRequestIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify GCP auth header.
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %s", r.Header.Get("Authorization"))
		}

		// Verify the request has anthropic_version.
		var req apiRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.AnthropicVersion != anthropicVersion {
			t.Errorf("expected anthropic_version %s, got %s", anthropicVersion, req.AnthropicVersion)
		}

		resp := apiResponse{
			Content: []apiContentBlock{
				{Type: "text", Text: "Hi there!"},
			},
			StopReason: "end_turn",
			Usage: apiUsage{
				InputTokens:  5,
				OutputTokens: 3,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-east5"))
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{
			base:      server.Client().Transport,
			targetURL: server.URL,
		},
	}

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
}

func TestRequestStreamIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		events := []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi there\"}}\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n",
		}
		for _, event := range events {
			fmt.Fprint(w, event+"\n")
			flusher.Flush()
		}
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-east5"))
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{
			base:      server.Client().Transport,
			targetURL: server.URL,
		},
	}

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
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("expected 5 output tokens, got %d", resp.Usage.OutputTokens)
	}
}

// Regression: VertexAI Anthropic stream was missing thinking block and
// thinking_delta handling, present in the direct Anthropic provider.
func TestRequestStreamThinkingBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		events := []string{
			`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}` + "\n",
			`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}` + "\n",
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think about this."}}` + "\n",
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" The answer is 4."}}` + "\n",
			`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}` + "\n",
			`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}` + "\n",
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"The answer is 4."}}` + "\n",
			`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":1}` + "\n",
			`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}` + "\n",
			`event: message_stop` + "\n" + `data: {"type":"message_stop"}` + "\n",
		}
		for _, event := range events {
			fmt.Fprint(w, event+"\n")
			flusher.Flush()
		}
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-east5"))
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{
			base:      server.Client().Transport,
			targetURL: server.URL,
		},
	}

	stream, err := p.RequestStream(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "What is 2+2?"}},
			Timestamp: time.Now(),
		},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var thinkingContent strings.Builder
	var textContent strings.Builder
	hasThinkingStart := false
	hasThinkingDelta := false

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
			if _, ok := e.Part.(core.ThinkingPart); ok {
				hasThinkingStart = true
			}
		case core.PartDeltaEvent:
			if td, ok := e.Delta.(core.ThinkingPartDelta); ok {
				hasThinkingDelta = true
				thinkingContent.WriteString(td.ContentDelta)
			}
			if td, ok := e.Delta.(core.TextPartDelta); ok {
				textContent.WriteString(td.ContentDelta)
			}
		}
	}

	if !hasThinkingStart {
		t.Error("expected thinking PartStartEvent")
	}
	if !hasThinkingDelta {
		t.Error("expected thinking PartDeltaEvent")
	}
	if thinkingContent.String() != "Let me think about this. The answer is 4." {
		t.Errorf("unexpected thinking content: %q", thinkingContent.String())
	}
	if textContent.String() != "The answer is 4." {
		t.Errorf("unexpected text content: %q", textContent.String())
	}

	// Verify the final response has both parts.
	resp := stream.Response()
	if len(resp.Parts) != 2 {
		t.Fatalf("expected 2 parts in response, got %d", len(resp.Parts))
	}
	if _, ok := resp.Parts[0].(core.ThinkingPart); !ok {
		t.Errorf("expected first part to be ThinkingPart, got %T", resp.Parts[0])
	}
	if tp, ok := resp.Parts[1].(core.TextPart); !ok || tp.Content != "The answer is 4." {
		t.Errorf("expected second part to be TextPart with 'The answer is 4.', got %T %v", resp.Parts[1], resp.Parts[1])
	}
}

func TestRequestHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"Permission denied"}`))
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-east5"))
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{
			base:      server.Client().Transport,
			targetURL: server.URL,
		},
	}

	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}},
		},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildRequestThinkingAutoAdjustsMaxTokens(t *testing.T) {
	budget := 10000
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hi"}},
		},
	}
	settings := &core.ModelSettings{
		ThinkingBudget: &budget,
	}
	// Default max tokens is 4096, which is less than budget (10000).
	req, err := buildRequest(messages, settings, nil, "claude-sonnet-4-5", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.Thinking == nil {
		t.Fatal("expected thinking to be enabled")
	}
	// max_tokens must be > budget_tokens for the Anthropic API.
	if req.MaxTokens <= budget {
		t.Errorf("expected max_tokens > %d, got %d", budget, req.MaxTokens)
	}
	if req.MaxTokens != budget+16000 {
		t.Errorf("expected max_tokens = %d, got %d", budget+16000, req.MaxTokens)
	}
}

func TestBuildRequestThinkingKeepsExplicitMaxTokens(t *testing.T) {
	budget := 10000
	maxTokens := 50000
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hi"}},
		},
	}
	settings := &core.ModelSettings{
		ThinkingBudget: &budget,
		MaxTokens:      &maxTokens,
	}
	req, err := buildRequest(messages, settings, nil, "claude-sonnet-4-5", 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	// Explicit max_tokens (50000) > budget (10000), should be preserved.
	if req.MaxTokens != maxTokens {
		t.Errorf("expected max_tokens = %d, got %d", maxTokens, req.MaxTokens)
	}
}

// --- buildRequest tests ported from anthropic provider ---

func TestBuildRequestWithSettings(t *testing.T) {
	temp := 0.7
	maxTokens := 2048
	settings := &core.ModelSettings{
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	}
	req, err := buildRequest(nil, settings, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.MaxTokens != 2048 {
		t.Errorf("expected max_tokens 2048, got %d", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %v", req.Temperature)
	}
}

func TestBuildRequestWithTools(t *testing.T) {
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:        "search",
				Description: "Search the web",
				ParametersSchema: core.Schema{
					"type":       "object",
					"properties": map[string]any{"query": core.Schema{"type": "string"}},
					"required":   []string{"query"},
				},
			},
		},
	}
	req, err := buildRequest(nil, nil, params, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].Name != "search" {
		t.Errorf("expected tool name 'search', got %q", req.Tools[0].Name)
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
		t.Fatal(err)
	}
	block := req.Messages[0].Content[0]
	if block.Type != "tool_result" {
		t.Errorf("expected tool_result, got %q", block.Type)
	}
	if block.ToolUseID != "call_123" {
		t.Errorf("expected call_123, got %q", block.ToolUseID)
	}
	if block.Content != "found 5 results" {
		t.Errorf("expected 'found 5 results', got %q", block.Content)
	}
}

func TestBuildRequestToolReturnNonString(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "get_data",
					Content:    map[string]any{"key": "value"},
					ToolCallID: "call_456",
				},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	block := req.Messages[0].Content[0]
	if block.Content != `{"key":"value"}` {
		t.Errorf("expected JSON-marshaled content, got %q", block.Content)
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
		t.Fatal(err)
	}
	block := req.Messages[0].Content[0]
	if block.Type != "tool_result" {
		t.Errorf("expected tool_result, got %q", block.Type)
	}
	if !block.IsError {
		t.Error("expected is_error=true")
	}
}

func TestBuildRequestRetryPromptWithoutToolID(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.RetryPromptPart{Content: "please try again"},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	block := req.Messages[0].Content[0]
	if block.Type != "text" {
		t.Errorf("expected text, got %q", block.Type)
	}
	if block.Text != "please try again" {
		t.Errorf("expected 'please try again', got %q", block.Text)
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
		t.Fatal(err)
	}
	msg := req.Messages[0]
	if msg.Role != "assistant" {
		t.Errorf("expected role 'assistant', got %q", msg.Role)
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

func TestBuildRequestAssistantThinking(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ThinkingPart{Content: "Let me think...", Signature: "sig123"},
				core.TextPart{Content: "Answer"},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	msg := req.Messages[0]
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(msg.Content))
	}
	if msg.Content[0].Type != "thinking" || msg.Content[0].Thinking != "Let me think..." {
		t.Errorf("expected thinking block, got %+v", msg.Content[0])
	}
	if msg.Content[0].Signature != "sig123" {
		t.Errorf("expected signature 'sig123', got %q", msg.Content[0].Signature)
	}
}

func TestBuildRequestStream(t *testing.T) {
	req, err := buildRequest(nil, nil, nil, Claude4Sonnet, 4096, true)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Stream {
		t.Error("expected stream=true")
	}
}

func TestBuildRequestToolChoiceModes(t *testing.T) {
	tests := []struct {
		name       string
		tc         *core.ToolChoice
		wantType   string
		wantName   string
		wantNilTC  bool
		wantNoTool bool
	}{
		{"auto", &core.ToolChoice{Mode: "auto"}, "auto", "", false, false},
		{"required", &core.ToolChoice{Mode: "required"}, "any", "", false, false},
		{"specific", &core.ToolChoice{ToolName: "search"}, "tool", "search", false, false},
		{"none", &core.ToolChoice{Mode: "none"}, "", "", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := &core.ModelRequestParameters{
				FunctionTools: []core.ToolDefinition{{
					Name:             "search",
					ParametersSchema: core.Schema{"type": "object"},
				}},
			}
			settings := &core.ModelSettings{ToolChoice: tt.tc}
			req, err := buildRequest(nil, settings, params, Claude4Sonnet, 4096, false)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantNoTool && len(req.Tools) > 0 {
				t.Error("expected tools to be nil for mode 'none'")
			}
			if tt.wantNilTC && req.ToolChoice != nil {
				t.Errorf("expected nil tool_choice, got %+v", req.ToolChoice)
			}
			if !tt.wantNilTC && req.ToolChoice != nil {
				if req.ToolChoice.Type != tt.wantType {
					t.Errorf("expected type %q, got %q", tt.wantType, req.ToolChoice.Type)
				}
				if req.ToolChoice.Name != tt.wantName {
					t.Errorf("expected name %q, got %q", tt.wantName, req.ToolChoice.Name)
				}
			}
		})
	}
}

func TestBuildRequestEmptyResponseAlternation(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}},
		},
		core.ModelResponse{Parts: []core.ModelResponsePart{}},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.RetryPromptPart{Content: "empty response, please provide a result"},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(req.Messages); i++ {
		if req.Messages[i-1].Role == req.Messages[i].Role {
			t.Errorf("adjacent %q messages at indices %d and %d", req.Messages[i].Role, i-1, i)
		}
	}
}

func TestBuildRequestSystemOnlyRequestAlternation(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{core.TextPart{Content: "Hi"}},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "New context"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{core.TextPart{Content: "Acknowledged"}},
		},
	}
	req, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(req.Messages); i++ {
		if req.Messages[i-1].Role == req.Messages[i].Role {
			t.Errorf("adjacent %q messages at indices %d and %d", req.Messages[i].Role, i-1, i)
		}
	}
	if len(req.Messages) != 4 {
		t.Errorf("expected 4 messages, got %d", len(req.Messages))
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
				core.ModelRequest{Parts: []core.ModelRequestPart{tt.part}},
			}
			_, err := buildRequest(messages, nil, nil, Claude4Sonnet, 4096, false)
			if err == nil {
				t.Errorf("expected error for unsupported %s", tt.name)
			}
			if err != nil && !strings.Contains(err.Error(), "unsupported request part type") {
				t.Errorf("expected 'unsupported request part type', got %q", err.Error())
			}
		})
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
		t.Fatal(err)
	}
	if req.Temperature != nil {
		t.Errorf("expected temperature nil when thinking enabled, got %v", *req.Temperature)
	}
}

func TestBuildRequestNoThinkingByDefault(t *testing.T) {
	req, err := buildRequest(nil, nil, nil, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.Thinking != nil {
		t.Errorf("expected Thinking nil by default, got %+v", req.Thinking)
	}
}

// --- parseResponse tests ---

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
	if tp.Signature != "sig123" {
		t.Errorf("signature = %q", tp.Signature)
	}
}

func TestParseResponseToolCallNilInput(t *testing.T) {
	resp := &apiResponse{
		Content: []apiContentBlock{
			{Type: "tool_use", ID: "call_1", Name: "no_args", Input: nil},
		},
		StopReason: "tool_use",
	}
	result := parseResponse(resp, Claude4Sonnet)
	tc, ok := result.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatalf("expected ToolCallPart, got %T", result.Parts[0])
	}
	if tc.ArgsJSON != "{}" {
		t.Errorf("expected '{}' for nil input, got %q", tc.ArgsJSON)
	}
}

func TestMapStopReasonAll(t *testing.T) {
	tests := []struct {
		reason string
		want   core.FinishReason
	}{
		{"end_turn", core.FinishReasonStop},
		{"stop_sequence", core.FinishReasonStop},
		{"max_tokens", core.FinishReasonLength},
		{"tool_use", core.FinishReasonToolCall},
		{"refusal", core.FinishReasonContentFilter},
		{"unknown_reason", core.FinishReasonStop},
		{"", core.FinishReasonStop},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			if got := mapStopReason(tt.reason); got != tt.want {
				t.Errorf("mapStopReason(%q) = %q, want %q", tt.reason, got, tt.want)
			}
		})
	}
}

func TestMapUsageIncludesCacheTokens(t *testing.T) {
	u := apiUsage{
		InputTokens:              500,
		OutputTokens:             100,
		CacheCreationInputTokens: 200,
		CacheReadInputTokens:     1000,
	}
	usage := mapUsage(u)
	if usage.InputTokens != 1700 {
		t.Errorf("InputTokens = %d, want 1700", usage.InputTokens)
	}
	if usage.CacheWriteTokens != 200 {
		t.Errorf("CacheWriteTokens = %d, want 200", usage.CacheWriteTokens)
	}
	if usage.CacheReadTokens != 1000 {
		t.Errorf("CacheReadTokens = %d, want 1000", usage.CacheReadTokens)
	}
}

// --- SSE streaming tests ---

func TestStreamToolCall(t *testing.T) {
	sseData := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"search\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\\\":\\\"test\\\"}\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, Claude4Sonnet)
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
		t.Fatalf("expected ToolCallPart, got %T", resp.Parts[0])
	}
	if tc.ToolName != "search" || tc.ToolCallID != "call_1" {
		t.Errorf("tool = %q / %q", tc.ToolName, tc.ToolCallID)
	}
}

func TestStreamThinkingWithSignature(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":50,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" about this."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_part1"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_part2"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"The answer."}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":30}}

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
			t.Fatal(err)
		}
		events = append(events, event)
	}

	// Signature deltas don't emit events: start(thinking), delta, delta, end, start(text), delta, end = 7
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d", len(events))
	}

	resp := stream.Response()
	if len(resp.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(resp.Parts))
	}
	tp, ok := resp.Parts[0].(core.ThinkingPart)
	if !ok {
		t.Fatalf("expected ThinkingPart, got %T", resp.Parts[0])
	}
	if tp.Content != "Let me think about this." {
		t.Errorf("thinking = %q", tp.Content)
	}
	if tp.Signature != "sig_part1sig_part2" {
		t.Errorf("signature = %q, want 'sig_part1sig_part2'", tp.Signature)
	}
}

func TestStreamErrorEvent(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Partial"}}

event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, Claude4Sonnet)

	// Read start and delta events.
	_, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	_, err = stream.Next()
	if err != nil {
		t.Fatal(err)
	}

	// Next call should return the error.
	_, err = stream.Next()
	if err == nil {
		t.Fatal("expected error after error event")
	}
	if !strings.Contains(err.Error(), "Overloaded") {
		t.Errorf("error = %v, want to contain 'Overloaded'", err)
	}
	if !strings.Contains(err.Error(), "overloaded_error") {
		t.Errorf("error = %v, want to contain 'overloaded_error'", err)
	}
}

func TestStreamErrorEventUnparseable(t *testing.T) {
	sseData := "event: error\ndata: not-json\n\n"

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, Claude4Sonnet)

	_, err := stream.Next()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not-json") {
		t.Errorf("error = %v, want to contain raw data", err)
	}
}

func TestStreamNoSpaceAfterColon(t *testing.T) {
	sseData := "event:message_start\ndata:{\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\nevent:content_block_start\ndata:{\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent:content_block_delta\ndata:{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\nevent:content_block_stop\ndata:{\"type\":\"content_block_stop\",\"index\":0}\n\nevent:message_delta\ndata:{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent:message_stop\ndata:{\"type\":\"message_stop\"}\n\n"

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, Claude4Sonnet)

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
	tp, ok := resp.Parts[0].(core.TextPart)
	if !ok {
		t.Fatalf("expected TextPart, got %T", resp.Parts[0])
	}
	if tp.Content != "OK" {
		t.Errorf("expected 'OK', got %q", tp.Content)
	}
}

func TestStreamMultipleToolCalls(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":50,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"bash"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"ls\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_2","name":"view"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"main.go\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, Claude4Sonnet)

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
	tc1 := resp.Parts[0].(core.ToolCallPart)
	tc2 := resp.Parts[1].(core.ToolCallPart)
	if tc1.ArgsJSON != `{"command":"ls"}` {
		t.Errorf("tc1 args = %q", tc1.ArgsJSON)
	}
	if tc2.ArgsJSON != `{"file":"main.go"}` {
		t.Errorf("tc2 args = %q", tc2.ArgsJSON)
	}
}

func TestStreamEmptyToolArgs(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"no_args"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, Claude4Sonnet)

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
	tc := resp.Parts[0].(core.ToolCallPart)
	if tc.ArgsJSON != "{}" {
		t.Errorf("expected '{}' for empty args, got %q", tc.ArgsJSON)
	}
}

func TestStreamUnknownBlockType(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"unknown_type"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_stop
data: {"type":"message_stop"}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, Claude4Sonnet)

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
	if len(resp.Parts) != 0 {
		t.Errorf("expected 0 parts for unknown block type, got %d", len(resp.Parts))
	}
}

func TestStreamUsageAccessor(t *testing.T) {
	sseData := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":42,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, Claude4Sonnet)

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
	if usage.InputTokens != 42 {
		t.Errorf("expected 42 input tokens, got %d", usage.InputTokens)
	}
}

// --- Provider tests ---

func TestModelName(t *testing.T) {
	p := New(WithProject("p"), WithModel(Claude4Opus))
	if p.ModelName() != Claude4Opus {
		t.Errorf("expected %q, got %q", Claude4Opus, p.ModelName())
	}
}

func TestWithCredentialsFileOption(t *testing.T) {
	p := New(WithCredentialsFile("/path/to/creds.json"))
	if p.credentialsFile != "/path/to/creds.json" {
		t.Errorf("expected /path/to/creds.json, got %q", p.credentialsFile)
	}
}

func TestWithCredentialsJSONOption(t *testing.T) {
	data := []byte(`{"type":"service_account"}`)
	p := New(WithCredentialsJSON(data))
	if string(p.credentialsJSON) != string(data) {
		t.Errorf("credentials JSON mismatch")
	}
}

func TestWithHTTPClientOption(t *testing.T) {
	custom := &http.Client{Timeout: 30 * time.Second}
	p := New(WithHTTPClient(custom))
	if p.httpClient != custom {
		t.Error("expected custom HTTP client")
	}
}

// --- Prompt caching tests ---

func TestWithPromptCachingOption(t *testing.T) {
	p := New(WithPromptCaching(true))
	if !p.promptCaching {
		t.Error("expected promptCaching to be true")
	}
}

func TestPromptCachingEnvVarTrue(t *testing.T) {
	t.Setenv("VERTEXAI_ANTHROPIC_PROMPT_CACHING", "true")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	p := New()
	if !p.promptCaching {
		t.Error("expected promptCaching from env 'true'")
	}
}

func TestPromptCachingEnvVar1(t *testing.T) {
	t.Setenv("VERTEXAI_ANTHROPIC_PROMPT_CACHING", "1")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	p := New()
	if !p.promptCaching {
		t.Error("expected promptCaching from env '1'")
	}
}

func TestPromptCachingDisabledByDefault(t *testing.T) {
	t.Setenv("VERTEXAI_ANTHROPIC_PROMPT_CACHING", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	p := New()
	if p.promptCaching {
		t.Error("expected promptCaching false by default")
	}
}

func TestApplyCacheBreakpointsSystemAndTools(t *testing.T) {
	req := &apiRequest{
		System: []apiSystemBlock{
			{Type: "text", Text: "system1"},
			{Type: "text", Text: "system2"},
		},
		Tools: []apiTool{
			{Name: "tool1", InputSchema: json.RawMessage(`{}`)},
			{Name: "tool2", InputSchema: json.RawMessage(`{}`)},
		},
	}
	applyCacheBreakpoints(req)

	// Only last system block should have cache_control.
	if req.System[0].CacheControl != nil {
		t.Error("first system block should not have cache_control")
	}
	if req.System[1].CacheControl == nil || req.System[1].CacheControl.Type != "ephemeral" {
		t.Error("last system block should have ephemeral cache_control")
	}
	// Only last tool should have cache_control.
	if req.Tools[0].CacheControl != nil {
		t.Error("first tool should not have cache_control")
	}
	if req.Tools[1].CacheControl == nil || req.Tools[1].CacheControl.Type != "ephemeral" {
		t.Error("last tool should have ephemeral cache_control")
	}
}

func TestApplyCacheBreakpointsNoSystemNoTools(t *testing.T) {
	req := &apiRequest{}
	// Should not panic with empty system/tools.
	applyCacheBreakpoints(req)
}

func TestCacheBreakpointsOmittedWhenDisabled(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "You are helpful."},
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{{
			Name:             "search",
			ParametersSchema: core.Schema{"type": "object"},
		}},
	}
	req, err := buildRequest(messages, nil, params, Claude4Sonnet, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	// Without applyCacheBreakpoints, no cache_control should be set.
	for _, s := range req.System {
		if s.CacheControl != nil {
			t.Error("cache_control should not be set when caching is disabled")
		}
	}
	for _, tool := range req.Tools {
		if tool.CacheControl != nil {
			t.Error("tool cache_control should not be set when caching is disabled")
		}
	}
}

func TestCacheBreakpointsInPayload(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		resp := apiResponse{
			Content:    []apiContentBlock{{Type: "text", Text: "cached"}},
			StopReason: "end_turn",
			Usage:      apiUsage{InputTokens: 5, OutputTokens: 3},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(
		WithProject("test-project"),
		WithLocation("us-east5"),
		WithPromptCaching(true),
	)
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{base: server.Client().Transport, targetURL: server.URL},
	}

	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{{
			Name:             "search",
			Description:      "Search the web",
			ParametersSchema: core.Schema{"type": "object"},
		}},
	}

	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "You are helpful."},
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}, nil, params)
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatal(err)
	}

	// Verify system block has cache_control.
	system, ok := payload["system"].([]any)
	if !ok || len(system) == 0 {
		t.Fatal("expected system blocks in payload")
	}
	lastSystem := system[len(system)-1].(map[string]any)
	cc, ok := lastSystem["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("expected cache_control on last system block")
	}
	if cc["type"] != "ephemeral" {
		t.Errorf("expected ephemeral cache_control, got %v", cc["type"])
	}

	// Verify tool has cache_control.
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatal("expected tools in payload")
	}
	lastTool := tools[len(tools)-1].(map[string]any)
	toolCC, ok := lastTool["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("expected cache_control on last tool")
	}
	if toolCC["type"] != "ephemeral" {
		t.Errorf("expected ephemeral cache_control on tool, got %v", toolCC["type"])
	}
}

func TestCacheControlOmittedFromPayloadWhenDisabled(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		resp := apiResponse{
			Content:    []apiContentBlock{{Type: "text", Text: "no cache"}},
			StopReason: "end_turn",
			Usage:      apiUsage{InputTokens: 5, OutputTokens: 3},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-east5"))
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{base: server.Client().Transport, targetURL: server.URL},
	}

	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{{
			Name:             "search",
			ParametersSchema: core.Schema{"type": "object"},
		}},
	}

	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "You are helpful."},
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}, nil, params)
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatal(err)
	}

	// System blocks should not have cache_control.
	system, _ := payload["system"].([]any)
	for _, s := range system {
		sb := s.(map[string]any)
		if _, exists := sb["cache_control"]; exists {
			t.Error("cache_control should not be present when caching is disabled")
		}
	}

	// Tools should not have cache_control.
	tools, _ := payload["tools"].([]any)
	for _, tool := range tools {
		tb := tool.(map[string]any)
		if _, exists := tb["cache_control"]; exists {
			t.Error("tool cache_control should not be present when caching is disabled")
		}
	}
}

func TestRequestStreamHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Header().Set("Retry-After", "30")
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-east5"))
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{base: server.Client().Transport, targetURL: server.URL},
	}

	_, err := p.RequestStream(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}},
		},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	httpErr, ok := err.(*core.ModelHTTPError)
	if !ok {
		t.Fatalf("expected ModelHTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", httpErr.StatusCode)
	}
}

func TestParseHTTPErrorRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "45")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-east5"))
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{base: server.Client().Transport, targetURL: server.URL},
	}

	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}},
		},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	httpErr, ok := err.(*core.ModelHTTPError)
	if !ok {
		t.Fatalf("expected ModelHTTPError, got %T: %v", err, err)
	}
	if httpErr.RetryAfter != 45*time.Second {
		t.Errorf("expected RetryAfter 45s, got %v", httpErr.RetryAfter)
	}
}

// --- Test helpers ---

type staticTokenSource struct {
	token string
}

func (s *staticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token}, nil
}

type rewriteTransport struct {
	base      http.RoundTripper
	targetURL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.targetURL, "http://")
	transport := t.base
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(req)
}

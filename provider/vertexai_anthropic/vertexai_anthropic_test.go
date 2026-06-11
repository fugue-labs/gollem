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
	p := New(WithProject("my-project"), WithLocation("us-east5"), WithModel(ClaudeSonnet46))
	expected := "https://us-east5-aiplatform.googleapis.com/v1/projects/my-project/locations/us-east5/publishers/anthropic/models/claude-sonnet-4-6:rawPredict"
	if p.endpoint() != expected {
		t.Errorf("expected endpoint %s, got %s", expected, p.endpoint())
	}
}

func TestStreamEndpointConstruction(t *testing.T) {
	p := New(WithProject("my-project"), WithLocation("us-east5"), WithModel(ClaudeSonnet46))
	expected := "https://us-east5-aiplatform.googleapis.com/v1/projects/my-project/locations/us-east5/publishers/anthropic/models/claude-sonnet-4-6:streamRawPredict"
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
		WithModel(ClaudeOpus46),
		WithMaxTokens(8192),
	)
	if p.project != "my-project" {
		t.Errorf("expected project my-project, got %s", p.project)
	}
	if p.location != "europe-west1" {
		t.Errorf("expected location europe-west1, got %s", p.location)
	}
	if p.model != ClaudeOpus46 {
		t.Errorf("expected model %s, got %s", ClaudeOpus46, p.model)
	}
	if p.maxTokens != 8192 {
		t.Errorf("expected maxTokens 8192, got %d", p.maxTokens)
	}
}

func TestPromptCachingOption(t *testing.T) {
	p := New(WithPromptCaching(true))
	if !p.promptCachingEnabled {
		t.Error("expected prompt caching to be enabled")
	}
}

func TestPromptCachingEnvVar(t *testing.T) {
	t.Setenv("VERTEXAI_ANTHROPIC_PROMPT_CACHE", "true")
	p := New()
	if !p.promptCachingEnabled {
		t.Error("expected prompt caching from env")
	}
}

func TestPromptCachingOptionOverridesEnv(t *testing.T) {
	t.Setenv("VERTEXAI_ANTHROPIC_PROMPT_CACHE", "true")
	p := New(WithPromptCaching(false))
	if p.promptCachingEnabled {
		t.Error("expected option to override env and keep prompt caching disabled")
	}
}

func TestPromptCachingOptionFalseOverridesEnvTTL(t *testing.T) {
	t.Setenv("VERTEXAI_ANTHROPIC_PROMPT_CACHE", "")
	t.Setenv("VERTEXAI_ANTHROPIC_PROMPT_CACHE_TTL", "1h")
	p := New(WithPromptCaching(false))
	if p.promptCachingEnabled {
		t.Error("expected explicit disable to override env TTL")
	}
}

func TestPromptCacheTTLFromEnvEnablesCaching(t *testing.T) {
	t.Setenv("VERTEXAI_ANTHROPIC_PROMPT_CACHE", "")
	t.Setenv("VERTEXAI_ANTHROPIC_PROMPT_CACHE_TTL", "1h")
	p := New()
	if !p.promptCachingEnabled {
		t.Error("expected TTL env var to implicitly enable prompt caching")
	}
	if p.promptCacheTTL != "1h" {
		t.Errorf("expected prompt cache TTL 1h, got %q", p.promptCacheTTL)
	}
}

func TestPromptCachingOptionTrueStillUsesEnvTTL(t *testing.T) {
	t.Setenv("VERTEXAI_ANTHROPIC_PROMPT_CACHE_TTL", "30m")
	p := New(WithPromptCaching(true))
	if !p.promptCachingEnabled {
		t.Fatal("expected prompt caching to stay enabled")
	}
	if p.promptCacheTTL != "30m" {
		t.Fatalf("expected env TTL to be honored, got %q", p.promptCacheTTL)
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

func TestPromptCachingInRequestPayload(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		resp := apiResponse{
			Content:    []apiContentBlock{{Type: "text", Text: "ok"}},
			StopReason: "end_turn",
			Usage:      apiUsage{InputTokens: 5, OutputTokens: 2},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(
		WithProject("test-project"),
		WithLocation("us-east5"),
		WithPromptCaching(true),
	)
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
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatal(err)
	}
	cc, ok := payload["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("expected cache_control in payload, got %v", payload["cache_control"])
	}
	if cc["type"] != "ephemeral" {
		t.Errorf("expected cache_control.type=ephemeral, got %v", cc["type"])
	}
	if _, hasTTL := cc["ttl"]; hasTTL {
		t.Errorf("expected cache_control.ttl to be omitted, got %v", cc["ttl"])
	}
}

func TestPromptCachingWithTTLInRequestPayload(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		resp := apiResponse{
			Content:    []apiContentBlock{{Type: "text", Text: "ok"}},
			StopReason: "end_turn",
			Usage:      apiUsage{InputTokens: 5, OutputTokens: 2},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(
		WithProject("test-project"),
		WithLocation("us-east5"),
		WithPromptCaching(true),
		WithPromptCacheTTL("1h"),
	)
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
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatal(err)
	}
	cc, ok := payload["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("expected cache_control in payload, got %v", payload["cache_control"])
	}
	if cc["type"] != "ephemeral" {
		t.Errorf("expected cache_control.type=ephemeral, got %v", cc["type"])
	}
	if cc["ttl"] != "1h" {
		t.Errorf("expected cache_control.ttl=1h, got %v", cc["ttl"])
	}
}

func TestPromptCachingOmittedFromPayloadWhenDisabled(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		resp := apiResponse{
			Content:    []apiContentBlock{{Type: "text", Text: "ok"}},
			StopReason: "end_turn",
			Usage:      apiUsage{InputTokens: 5, OutputTokens: 2},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(
		WithProject("test-project"),
		WithLocation("us-east5"),
	)
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
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["cache_control"]; exists {
		t.Errorf("cache_control should be omitted when disabled, got %v", payload["cache_control"])
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

func TestPromptCachingInStreamingPayload(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		events := []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n",
		}
		for _, event := range events {
			fmt.Fprint(w, event+"\n")
			flusher.Flush()
		}
	}))
	defer server.Close()

	p := New(
		WithProject("test-project"),
		WithLocation("us-east5"),
		WithPromptCaching(true),
	)
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{
			base:      server.Client().Transport,
			targetURL: server.URL,
		},
	}

	stream, err := p.RequestStream(context.Background(), []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}},
		},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatal(err)
	}
	cc, ok := payload["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("expected cache_control in streaming payload, got %v", payload["cache_control"])
	}
	if cc["type"] != "ephemeral" {
		t.Errorf("expected cache_control.type=ephemeral, got %v", cc["type"])
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
	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false)
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

func TestBuildRequestStopSequences(t *testing.T) {
	settings := &core.ModelSettings{StopSequences: []string{"END", "###"}}
	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.StopSequences) != 2 || req.StopSequences[0] != "END" || req.StopSequences[1] != "###" {
		t.Errorf("stop_sequences = %v, want [END ###]", req.StopSequences)
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
	req, err := buildRequest(nil, nil, params, ClaudeSonnet46, 4096, false)
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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false)
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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false)
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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false)
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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false)
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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false)
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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false)
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
	req, err := buildRequest(nil, nil, nil, ClaudeSonnet46, 4096, true)
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
			req, err := buildRequest(nil, settings, params, ClaudeSonnet46, 4096, false)
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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false)
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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false)
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

// TestBuildRequestRejectsAudio verifies AudioPart is rejected — Vertex's
// Claude Messages API has no audio input support. ImagePart and DocumentPart
// are supported (see multimodal tests below).
func TestBuildRequestRejectsAudio(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.AudioPart{URL: "https://example.com/audio.mp3", MIMEType: "audio/mp3"},
		}},
	}
	_, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false)
	if err == nil {
		t.Fatal("expected error for AudioPart")
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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 1024, false)
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
	dataURI := core.BinaryContent([]byte{1, 2, 3}, "image/png")
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.ImagePart{URL: dataURI},
		}},
	}
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 1024, false)
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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 1024, false)
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
// Images emits a tool_result whose content is an array.
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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 1024, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

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
	if decoded.Type != "tool_result" || decoded.ToolUseID != "call_1" {
		t.Errorf("decoded = %+v", decoded)
	}
	if len(decoded.Content) != 3 {
		t.Fatalf("expected 3 content blocks (text + 2 images), got %d", len(decoded.Content))
	}

	var textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(decoded.Content[0], &textBlock)
	if textBlock.Type != "text" || textBlock.Text != "here are the screenshots" {
		t.Errorf("text block = %+v", textBlock)
	}

	var imgBlock1 struct {
		Type   string    `json:"type"`
		Source apiSource `json:"source"`
	}
	_ = json.Unmarshal(decoded.Content[1], &imgBlock1)
	if imgBlock1.Type != "image" || imgBlock1.Source.Type != "url" {
		t.Errorf("img1 = %+v", imgBlock1)
	}

	var imgBlock2 struct {
		Type   string    `json:"type"`
		Source apiSource `json:"source"`
	}
	_ = json.Unmarshal(decoded.Content[2], &imgBlock2)
	if imgBlock2.Type != "image" || imgBlock2.Source.Type != "base64" || imgBlock2.Source.MediaType != "image/jpeg" {
		t.Errorf("img2 = %+v", imgBlock2)
	}
}

func TestBuildRequestThinkingStripsTemperature(t *testing.T) {
	budget := 1024
	temp := 0.7
	settings := &core.ModelSettings{
		ThinkingBudget: &budget,
		Temperature:    &temp,
	}
	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.Temperature != nil {
		t.Errorf("expected temperature nil when thinking enabled, got %v", *req.Temperature)
	}
}

func TestBuildRequestNoThinkingByDefault(t *testing.T) {
	req, err := buildRequest(nil, nil, nil, ClaudeSonnet46, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.Thinking != nil {
		t.Errorf("expected Thinking nil by default, got %+v", req.Thinking)
	}
}

// TestOpus47RejectsThinkingBudget verifies that passing ThinkingBudget with
// Opus 4.7 fails at request build with a clear message pointing the caller to
// WithReasoningEffort instead. Opus 4.7 only supports adaptive thinking.
func TestOpus47RejectsThinkingBudget(t *testing.T) {
	budget := 2048
	settings := &core.ModelSettings{ThinkingBudget: &budget}

	_, err := buildRequest(nil, settings, nil, ClaudeOpus47, 4096, false)
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
		ClaudeSonnet46, ClaudeOpus46, ClaudeOpus47, "claude-opus-4-8", "claude-fable-5",
	} {
		t.Run(model, func(t *testing.T) {
			settings := &core.ModelSettings{AdaptiveThinking: &on}
			req, err := buildRequest(nil, settings, nil, model, 4096, false)
			if err != nil {
				t.Fatalf("buildRequest: %v", err)
			}
			if req.Thinking == nil || req.Thinking.Type != "adaptive" {
				t.Fatalf("expected adaptive thinking, got %+v", req.Thinking)
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
	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.Temperature != nil {
		t.Errorf("expected temperature to be nil when adaptive thinking enabled, got %v", *req.Temperature)
	}
}

// TestAdaptiveThinkingUnsupportedModels verifies pre-4.6 models reject
// adaptive thinking at build time with a clear error.
func TestAdaptiveThinkingUnsupportedModels(t *testing.T) {
	on := true
	for _, model := range []string{"claude-sonnet-4-5", "claude-opus-4-5", "claude-haiku-4-5"} {
		t.Run(model, func(t *testing.T) {
			settings := &core.ModelSettings{AdaptiveThinking: &on}
			_, err := buildRequest(nil, settings, nil, model, 4096, false)
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
	_, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false)
	if err == nil {
		t.Fatal("expected error when both AdaptiveThinking and ThinkingBudget are set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should say mutually exclusive, got: %v", err)
	}
}

// TestOpus48RejectsThinkingBudget verifies post-4.7 flagships reject manual
// thinking the same way Opus 4.7 does.
func TestOpus48RejectsThinkingBudget(t *testing.T) {
	budget := 2048
	for _, model := range []string{"claude-opus-4-8", "claude-fable-5"} {
		t.Run(model, func(t *testing.T) {
			settings := &core.ModelSettings{ThinkingBudget: &budget}
			_, err := buildRequest(nil, settings, nil, model, 4096, false)
			if err == nil {
				t.Fatalf("expected error when ThinkingBudget is set on %q", model)
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
		{ClaudeOpus46, "low"},
		{ClaudeOpus46, "max"},
		{ClaudeSonnet46, "medium"},
		{ClaudeSonnet46, "max"},
	}
	for _, tc := range cases {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			effort := tc.effort
			settings := &core.ModelSettings{ReasoningEffort: &effort}
			req, err := buildRequest(nil, settings, nil, tc.model, 4096, false)
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
		{"any_on_3x", "claude-3-5-sonnet", "low"},
		{"unknown_value", ClaudeOpus47, "ultra"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			effort := tc.effort
			settings := &core.ModelSettings{ReasoningEffort: &effort}
			_, err := buildRequest(nil, settings, nil, tc.model, 4096, false)
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

	req, err := buildRequest(nil, settings, nil, ClaudeOpus46, 4096, false)
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
	req, err := buildRequest(nil, nil, nil, ClaudeOpus47, 4096, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.OutputConfig != nil {
		t.Errorf("expected OutputConfig nil by default, got %+v", req.OutputConfig)
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
	result := parseResponse(resp, ClaudeSonnet46)
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
	stream := newStreamedResponse(body, ClaudeSonnet46)
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
	stream := newStreamedResponse(body, ClaudeSonnet46)

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
	stream := newStreamedResponse(body, ClaudeSonnet46)

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
	stream := newStreamedResponse(body, ClaudeSonnet46)

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
	stream := newStreamedResponse(body, ClaudeSonnet46)

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
	stream := newStreamedResponse(body, ClaudeSonnet46)

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
	stream := newStreamedResponse(body, ClaudeSonnet46)

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
	stream := newStreamedResponse(body, ClaudeSonnet46)

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
	stream := newStreamedResponse(body, ClaudeSonnet46)

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
	p := New(WithProject("p"), WithModel(ClaudeOpus46))
	if p.ModelName() != ClaudeOpus46 {
		t.Errorf("expected %q, got %q", ClaudeOpus46, p.ModelName())
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

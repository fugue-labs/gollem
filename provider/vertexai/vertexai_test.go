package vertexai

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

func TestBuildRequestBasic(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "You are helpful."},
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// System instruction should be set.
	if req.SystemInstruction == nil {
		t.Fatal("expected SystemInstruction to be set")
	}
	if req.SystemInstruction.Role != "user" {
		t.Errorf("expected role user, got %s", req.SystemInstruction.Role)
	}
	if len(req.SystemInstruction.Parts) != 1 || req.SystemInstruction.Parts[0].Text != "You are helpful." {
		t.Error("unexpected system instruction content")
	}
	// User message.
	if len(req.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(req.Contents))
	}
	if req.Contents[0].Role != "user" {
		t.Errorf("expected role user, got %s", req.Contents[0].Role)
	}
	if len(req.Contents[0].Parts) != 1 || req.Contents[0].Parts[0].Text != "Hello" {
		t.Error("unexpected user content")
	}
}

func TestBuildRequestWithSettings(t *testing.T) {
	maxTokens := 1000
	temp := 0.7
	settings := &core.ModelSettings{
		MaxTokens:   &maxTokens,
		Temperature: &temp,
	}
	req, err := buildRequest(nil, settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.GenerationConfig == nil {
		t.Fatal("expected GenerationConfig to be set")
	}
	if req.GenerationConfig.MaxOutputTokens != 1000 {
		t.Errorf("expected maxOutputTokens 1000, got %d", req.GenerationConfig.MaxOutputTokens)
	}
	if req.GenerationConfig.Temperature == nil || *req.GenerationConfig.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7")
	}
}

func TestBuildRequestWithTools(t *testing.T) {
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Get weather",
				ParametersSchema: core.Schema{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	req, err := buildRequest(nil, nil, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool decl, got %d", len(req.Tools))
	}
	if len(req.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("expected 1 function, got %d", len(req.Tools[0].FunctionDeclarations))
	}
	fn := req.Tools[0].FunctionDeclarations[0]
	if fn.Name != "get_weather" {
		t.Errorf("expected name get_weather, got %s", fn.Name)
	}
}

func TestBuildRequestToolReturn(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "get_weather",
					Content:    "sunny",
					ToolCallID: "call_123",
				},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(req.Contents))
	}
	part := req.Contents[0].Parts[0]
	if part.FunctionResponse == nil {
		t.Fatal("expected FunctionResponse")
	}
	if part.FunctionResponse.Name != "get_weather" {
		t.Errorf("expected get_weather, got %s", part.FunctionResponse.Name)
	}
	if part.FunctionResponse.Response["result"] != "sunny" {
		t.Errorf("unexpected response: %v", part.FunctionResponse.Response)
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
					ToolCallID: "search",
				},
			},
		},
	}
	req, err := buildRequest(messages, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(req.Contents))
	}
	content := req.Contents[0]
	if content.Role != "model" {
		t.Errorf("expected role model, got %s", content.Role)
	}
	if len(content.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(content.Parts))
	}
	if content.Parts[0].Text != "Hello!" {
		t.Errorf("expected text Hello!, got %s", content.Parts[0].Text)
	}
	if content.Parts[1].FunctionCall == nil {
		t.Fatal("expected FunctionCall")
	}
	if content.Parts[1].FunctionCall.Name != "search" {
		t.Errorf("expected search, got %s", content.Parts[1].FunctionCall.Name)
	}
}

func TestParseResponse(t *testing.T) {
	resp := &geminiResponse{
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Role:  "model",
					Parts: []geminiPart{{Text: "Hello there!"}},
				},
				FinishReason: "STOP",
			},
		},
		UsageMetadata: geminiUsage{
			PromptTokenCount:     10,
			CandidatesTokenCount: 5,
		},
	}
	result := parseResponse(resp, "gemini-2.5-flash")
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
}

func TestParseResponseFunctionCall(t *testing.T) {
	resp := &geminiResponse{
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Role: "model",
					Parts: []geminiPart{
						{
							FunctionCall: &geminiFunctionCall{
								Name: "get_weather",
								Args: map[string]any{"city": "NYC"},
							},
						},
					},
				},
				FinishReason: "STOP",
			},
		},
	}
	result := parseResponse(resp, "gemini-2.5-flash")
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
	// Verify args JSON.
	var args map[string]any
	json.Unmarshal([]byte(tc.ArgsJSON), &args)
	if args["city"] != "NYC" {
		t.Errorf("expected city NYC, got %v", args["city"])
	}
}

func TestParseResponseFinishReasons(t *testing.T) {
	tests := []struct {
		reason   string
		expected core.FinishReason
	}{
		{"STOP", core.FinishReasonStop},
		{"MAX_TOKENS", core.FinishReasonLength},
		{"SAFETY", core.FinishReasonContentFilter},
		{"RECITATION", core.FinishReasonContentFilter},
		{"UNKNOWN", core.FinishReasonStop},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			resp := &geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content:      geminiContent{Role: "model", Parts: []geminiPart{{Text: "test"}}},
						FinishReason: tt.reason,
					},
				},
			}
			result := parseResponse(resp, "gemini-2.5-flash")
			if result.FinishReason != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result.FinishReason)
			}
		})
	}
}

func TestParseSSEStreamText(t *testing.T) {
	sseData := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]},"finishReason":""}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1}}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":" world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-2.5-flash")

	// First event: text start.
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

	// Second event: text delta.
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

	// EOF.
	_, err = stream.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}

	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	finalTp, ok := resp.Parts[0].(core.TextPart)
	if !ok {
		t.Fatal("expected TextPart")
	}
	if finalTp.Content != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", finalTp.Content)
	}
}

func TestParseSSEStreamFunctionCall(t *testing.T) {
	sseData := `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"NYC"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-2.5-flash")

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

	_, err = stream.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}

	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
}

func TestParseSSEStreamMixedTextAndFunctionCallSameChunk(t *testing.T) {
	sseData := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"},{"functionCall":{"name":"get_weather","args":{"city":"NYC"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-2.5-flash")

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
	if tool.ToolName != "get_weather" {
		t.Fatalf("expected tool name get_weather, got %q", tool.ToolName)
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
		t.Fatalf("expected response part 0 TextPart, got %T", resp.Parts[0])
	}
	if tc, ok := resp.Parts[1].(core.ToolCallPart); !ok {
		t.Fatalf("expected response part 1 ToolCallPart, got %T", resp.Parts[1])
	} else {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.ArgsJSON), &args); err != nil {
			t.Fatalf("failed to parse tool args: %v", err)
		}
		if args["city"] != "NYC" {
			t.Fatalf("expected city NYC, got %v", args["city"])
		}
	}
}

func TestParseSSEStreamFinalPartOrderDeterministic(t *testing.T) {
	sseData := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"A"},{"functionCall":{"name":"lookup","args":{"k":"v"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}

`

	for i := range 50 {
		body := io.NopCloser(strings.NewReader(sseData))
		stream := newStreamedResponse(body, "gemini-2.5-flash")
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

func TestParseSSEStreamError(t *testing.T) {
	// Vertex AI Gemini can send error objects mid-stream.
	sseData := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hel"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}

data: {"error":{"code":429,"message":"Quota exceeded for aiplatform.googleapis.com","status":"RESOURCE_EXHAUSTED"}}

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-2.5-flash")

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
	if !strings.Contains(err.Error(), "Quota exceeded") {
		t.Errorf("expected error to contain 'Quota exceeded', got: %v", err)
	}
	if !strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
		t.Errorf("expected error to contain 'RESOURCE_EXHAUSTED', got: %v", err)
	}
}

func TestParseSSEStreamErrorOnly(t *testing.T) {
	// Error sent before any content.
	sseData := `data: {"error":{"code":500,"message":"Internal error","status":"INTERNAL"}}

`

	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-2.5-flash")

	_, err := stream.Next()
	if err == nil {
		t.Fatal("expected error from stream")
	}
	if !strings.Contains(err.Error(), "Internal error") {
		t.Errorf("expected error to contain 'Internal error', got: %v", err)
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
	if p.ModelName() != defaultModel {
		t.Errorf("expected ModelName() %s, got %s", defaultModel, p.ModelName())
	}
}

func TestNewProviderOptions(t *testing.T) {
	p := New(
		WithProject("my-project"),
		WithLocation("europe-west1"),
		WithModel(Gemini25Pro),
	)
	if p.project != "my-project" {
		t.Errorf("expected project my-project, got %s", p.project)
	}
	if p.location != "europe-west1" {
		t.Errorf("expected location europe-west1, got %s", p.location)
	}
	if p.model != Gemini25Pro {
		t.Errorf("expected model %s, got %s", Gemini25Pro, p.model)
	}
}

func TestEndpointConstruction(t *testing.T) {
	p := New(WithProject("my-project"), WithLocation("us-central1"), WithModel("gemini-2.5-flash"))
	expected := "https://us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-2.5-flash"
	if p.endpoint() != expected {
		t.Errorf("expected endpoint %s, got %s", expected, p.endpoint())
	}
}

func TestRequestIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header.
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %s", r.Header.Get("Authorization"))
		}

		// Return a response.
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role:  "model",
						Parts: []geminiPart{{Text: "Hi there!"}},
					},
					FinishReason: "STOP",
				},
			},
			UsageMetadata: geminiUsage{
				PromptTokenCount:     5,
				CandidatesTokenCount: 3,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create a provider with a mock token source.
	p := New(WithProject("test-project"), WithLocation("us-central1"))
	p.httpClient = server.Client()
	// Override the endpoint by using the test server URL.
	// We need to intercept setHeaders to inject our test token.
	p.tokenSource = &staticTokenSource{token: "test-token"}

	// Override endpoint via a custom HTTP transport that rewrites URLs.
	origClient := p.httpClient
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{
			base:      origClient.Transport,
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		chunks := []string{
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hi"}]},"finishReason":""}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}`,
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":" there"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2}}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "%s\n\n", chunk)
			flusher.Flush()
		}
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-central1"))
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
}

func TestRequestHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"message":"Permission denied"}}`))
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-central1"))
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

// --- Test helpers ---

// staticTokenSource provides a fixed OAuth2 token for testing.
type staticTokenSource struct {
	token string
}

func (s *staticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token}, nil
}

// rewriteTransport rewrites request URLs to point to the test server.
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

// TestBuildRequestMultipleSystemPrompts verifies that multiple SystemPromptParts
// across different ModelRequest messages are concatenated, not overwritten.
// This is critical for auto-context compression, which inserts a conversation
// summary as a second SystemPromptPart after the original task system prompt.
func TestBuildRequestMultipleSystemPrompts(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "You are a coding assistant."},
				core.UserPromptPart{Content: "Fix the bug"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "[Conversation Summary] Previously edited main.go and ran tests."},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Continue fixing"},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if req.SystemInstruction == nil {
		t.Fatal("expected SystemInstruction to be set")
	}

	// Both system prompts should be preserved as separate parts.
	if len(req.SystemInstruction.Parts) != 2 {
		t.Fatalf("expected 2 system instruction parts, got %d", len(req.SystemInstruction.Parts))
	}
	if req.SystemInstruction.Parts[0].Text != "You are a coding assistant." {
		t.Errorf("first system part = %q, want %q", req.SystemInstruction.Parts[0].Text, "You are a coding assistant.")
	}
	if req.SystemInstruction.Parts[1].Text != "[Conversation Summary] Previously edited main.go and ran tests." {
		t.Errorf("second system part = %q, want summary", req.SystemInstruction.Parts[1].Text)
	}

	// Should have 3 user content messages: the original user prompt,
	// a placeholder for the system-only request (prevents alternation
	// violations when model responses appear between them), and the
	// continuation prompt.
	if len(req.Contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(req.Contents))
	}
}

// TestParseSSEStreamNoSpaceAfterColon verifies the Gemini stream parser
// handles SSE data lines without the optional space after the colon,
// per the SSE specification.
func TestParseSSEStreamNoSpaceAfterColon(t *testing.T) {
	// "data:" without trailing space is valid per SSE spec.
	sseData := `data:{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]},"finishReason":""}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1}}

data:{"candidates":[{"content":{"role":"model","parts":[{"text":" world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-2.5-flash")

	// First event: text start.
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

	// Second event: text delta.
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

	// EOF.
	_, err = stream.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}

	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	finalTp, ok := resp.Parts[0].(core.TextPart)
	if !ok {
		t.Fatal("expected TextPart")
	}
	if finalTp.Content != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", finalTp.Content)
	}
}

// TestBuildRequestToolReturnNonObjectContent verifies that ToolReturnPart content
// that marshals to non-object JSON (arrays, numbers, etc.) is preserved rather
// than silently dropped. Gemini requires FunctionResponse.Response to be a map,
// so non-object values must be wrapped.
func TestBuildRequestToolReturnNonObjectContent(t *testing.T) {
	tests := []struct {
		name    string
		content any
		wantKey string // expected key in response map
	}{
		{
			name:    "string content",
			content: "hello",
			wantKey: "result",
		},
		{
			name:    "slice content",
			content: []string{"a", "b", "c"},
			wantKey: "result",
		},
		{
			name:    "int content",
			content: 42,
			wantKey: "result",
		},
		{
			name:    "bool content",
			content: true,
			wantKey: "result",
		},
		{
			name:    "object content",
			content: map[string]any{"key": "value"},
			wantKey: "key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := []core.ModelMessage{
				core.ModelRequest{
					Parts: []core.ModelRequestPart{
						core.ToolReturnPart{
							ToolName:   "test_tool",
							Content:    tt.content,
							ToolCallID: "call_0",
						},
					},
				},
			}
			req, err := buildRequest(messages, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(req.Contents) != 1 {
				t.Fatalf("expected 1 content, got %d", len(req.Contents))
			}
			part := req.Contents[0].Parts[0]
			if part.FunctionResponse == nil {
				t.Fatal("expected FunctionResponse")
			}
			resp := part.FunctionResponse.Response
			if len(resp) == 0 {
				t.Fatalf("response map is empty — non-object content was silently dropped")
			}
			if _, ok := resp[tt.wantKey]; !ok {
				t.Errorf("expected key %q in response, got %v", tt.wantKey, resp)
			}
		})
	}
}

// TestBuildRequestSystemOnlyPlaceholder verifies that a ModelRequest containing
// only SystemPromptParts emits a placeholder user message to prevent consecutive
// model messages (which violate the Gemini API alternation requirement).
func TestBuildRequestSystemOnlyPlaceholder(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Hi there"},
			},
		},
		// System-only request (e.g., from auto-context compression).
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "Updated context info"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Continuing"},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify no adjacent same-role messages.
	for i := 1; i < len(req.Contents); i++ {
		if req.Contents[i-1].Role == req.Contents[i].Role {
			t.Errorf("adjacent %q messages at indices %d and %d", req.Contents[i].Role, i-1, i)
		}
	}

	// Should have 4 contents: user, model, user(placeholder), model.
	if len(req.Contents) != 4 {
		t.Fatalf("expected 4 contents, got %d", len(req.Contents))
	}
}

// TestBuildRequestEmptyResponseAlternation verifies that an empty ModelResponse
// doesn't create adjacent user messages in the Gemini API request.
func TestBuildRequestThinkingConfig(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Think hard"},
			},
		},
	}

	budget := 4096
	settings := &core.ModelSettings{
		ThinkingBudget: &budget,
	}

	req, err := buildRequest(messages, settings, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.GenerationConfig == nil {
		t.Fatal("expected GenerationConfig to be set")
	}
	if req.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("expected ThinkingConfig to be set")
	}
	if req.GenerationConfig.ThinkingConfig.ThinkingBudget != 4096 {
		t.Errorf("expected ThinkingBudget 4096, got %d", req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	}
}

func TestBuildRequestToolChoice(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello"},
			},
		},
	}

	tests := []struct {
		name     string
		tc       *core.ToolChoice
		wantMode string
		wantName string
	}{
		{
			name:     "none",
			tc:       &core.ToolChoice{Mode: "none"},
			wantMode: "NONE",
		},
		{
			name:     "required",
			tc:       &core.ToolChoice{Mode: "required"},
			wantMode: "ANY",
		},
		{
			name:     "specific tool",
			tc:       &core.ToolChoice{ToolName: "my_tool"},
			wantMode: "ANY",
			wantName: "my_tool",
		},
		{
			name:     "auto",
			tc:       &core.ToolChoice{Mode: "auto"},
			wantMode: "AUTO",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &core.ModelSettings{ToolChoice: tt.tc}
			req, err := buildRequest(messages, settings, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.ToolConfig == nil {
				t.Fatal("expected ToolConfig to be set")
			}
			if req.ToolConfig.FunctionCallingConfig.Mode != tt.wantMode {
				t.Errorf("expected mode %q, got %q", tt.wantMode, req.ToolConfig.FunctionCallingConfig.Mode)
			}
			if tt.wantName != "" {
				names := req.ToolConfig.FunctionCallingConfig.AllowedFunctionNames
				if len(names) != 1 || names[0] != tt.wantName {
					t.Errorf("expected allowed function names [%s], got %v", tt.wantName, names)
				}
			}
		})
	}
}

func TestBuildRequestNativeStructuredOutput(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Return structured data"},
			},
		},
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}

	params := &core.ModelRequestParameters{
		OutputMode: core.OutputModeNative,
		OutputObject: &core.OutputObjectDefinition{
			JSONSchema: schema,
		},
	}

	req, err := buildRequest(messages, nil, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.GenerationConfig == nil {
		t.Fatal("expected GenerationConfig to be set")
	}
	if req.GenerationConfig.ResponseMimeType != "application/json" {
		t.Errorf("expected ResponseMimeType 'application/json', got %q", req.GenerationConfig.ResponseMimeType)
	}
	if req.GenerationConfig.ResponseSchema == nil {
		t.Fatal("expected ResponseSchema to be set")
	}
}

func TestBuildRequestRetryPromptPart(t *testing.T) {
	t.Run("with_tool_call_id", func(t *testing.T) {
		messages := []core.ModelMessage{
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.UserPromptPart{Content: "Hello"},
				},
			},
			core.ModelResponse{
				Parts: []core.ModelResponsePart{
					core.ToolCallPart{
						ToolName:   "my_tool",
						ArgsJSON:   "{}",
						ToolCallID: "call_1",
					},
				},
			},
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.RetryPromptPart{
						Content:    "invalid args",
						ToolName:   "my_tool",
						ToolCallID: "call_1",
					},
				},
			},
		}
		req, err := buildRequest(messages, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The retry should be a FunctionResponse with error content.
		lastContent := req.Contents[len(req.Contents)-1]
		if lastContent.Role != "user" {
			t.Errorf("expected role user, got %s", lastContent.Role)
		}
		found := false
		for _, p := range lastContent.Parts {
			if p.FunctionResponse != nil {
				found = true
				if p.FunctionResponse.Name != "my_tool" {
					t.Errorf("expected tool name 'my_tool', got %q", p.FunctionResponse.Name)
				}
				if _, ok := p.FunctionResponse.Response["error"]; !ok {
					t.Error("expected error key in function response")
				}
			}
		}
		if !found {
			t.Error("expected FunctionResponse part for retry with tool call ID")
		}
	})

	t.Run("without_tool_call_id", func(t *testing.T) {
		messages := []core.ModelMessage{
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.UserPromptPart{Content: "Hello"},
				},
			},
			core.ModelResponse{
				Parts: []core.ModelResponsePart{
					core.TextPart{Content: "Some response"},
				},
			},
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.RetryPromptPart{Content: "empty response, please provide a result"},
				},
			},
		}
		req, err := buildRequest(messages, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		lastContent := req.Contents[len(req.Contents)-1]
		if lastContent.Role != "user" {
			t.Errorf("expected role user, got %s", lastContent.Role)
		}
		// Should be a text part, not a function response.
		if lastContent.Parts[0].Text != "empty response, please provide a result" {
			t.Errorf("expected retry text, got %q", lastContent.Parts[0].Text)
		}
		if lastContent.Parts[0].FunctionResponse != nil {
			t.Error("expected no FunctionResponse for retry without tool call ID")
		}
	})
}

func TestBuildRequestThoughtSignatureRoundTrip(t *testing.T) {
	// Simulate a Gemini 3.x response with thought signature, followed by
	// tool return, to verify the signature survives the round-trip through
	// buildRequest.
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Use a tool"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "search",
					ArgsJSON:   `{"query":"test"}`,
					ToolCallID: "call_0",
					Metadata:   map[string]string{"thoughtSignature": "abc123sig"},
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "search",
					Content:    "result: found it",
					ToolCallID: "call_0",
				},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the model response content and verify thought signature.
	var foundSig bool
	for _, c := range req.Contents {
		if c.Role == "model" {
			for _, p := range c.Parts {
				if p.FunctionCall != nil && p.ThoughtSignature == "abc123sig" {
					foundSig = true
				}
			}
		}
	}
	if !foundSig {
		t.Error("thought signature not preserved in round-trip")
	}
}

func TestBuildRequestUnsupportedPartType(t *testing.T) {
	// Verify that unsupported request part types return an error.
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ImagePart{URL: "http://example.com/img.png", MIMEType: "image/png"},
			},
		},
	}
	_, err := buildRequest(messages, nil, nil)
	if err == nil {
		t.Fatal("expected error for unsupported part type")
	}
	if !strings.Contains(err.Error(), "unsupported request part type") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParseResponseThoughtSignature(t *testing.T) {
	resp := &geminiResponse{
		Candidates: []geminiCandidate{{
			Content: geminiContent{
				Role: "model",
				Parts: []geminiPart{
					{
						FunctionCall: &geminiFunctionCall{
							Name: "search",
							Args: map[string]any{"q": "test"},
						},
						ThoughtSignature: "sig_xyz",
					},
				},
			},
			FinishReason: "STOP",
		}},
		UsageMetadata: geminiUsage{
			PromptTokenCount:     10,
			CandidatesTokenCount: 5,
			TotalTokenCount:      15,
		},
	}

	result := parseResponse(resp, "gemini-2.5-flash")
	if len(result.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(result.Parts))
	}
	tc, ok := result.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatal("expected ToolCallPart")
	}
	if tc.Metadata == nil || tc.Metadata["thoughtSignature"] != "sig_xyz" {
		t.Errorf("expected thoughtSignature 'sig_xyz', got %v", tc.Metadata)
	}
}

func TestParseSSEStreamFinishReasons(t *testing.T) {
	tests := []struct {
		reason string
		want   core.FinishReason
	}{
		{"STOP", core.FinishReasonStop},
		{"MAX_TOKENS", core.FinishReasonLength},
		{"SAFETY", core.FinishReasonContentFilter},
		{"RECITATION", core.FinishReasonContentFilter},
		{"UNKNOWN", core.FinishReasonStop},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			// Build a minimal SSE stream with the specified finish reason.
			sseStream := fmt.Sprintf("event: message_start\ndata: {\"candidates\":[]}\n\n"+
				"event: content_block_start\ndata: {\"index\":0,\"part\":{\"text\":\"hi\"}}\n\n"+
				"event: content_block_stop\ndata: {\"index\":0}\n\n"+
				"event: message_end\ndata: {\"candidates\":[{\"finishReason\":\"%s\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}\n\n",
				tt.reason)

			body := io.NopCloser(strings.NewReader(sseStream))
			stream := newStreamedResponse(body, "gemini-test")

			for {
				_, err := stream.Next()
				if err != nil {
					break
				}
			}

			resp := stream.Response()
			if resp.FinishReason != tt.want {
				t.Errorf("expected %v, got %v", tt.want, resp.FinishReason)
			}
		})
	}
}

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

	req, err := buildRequest(messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify no adjacent same-role messages.
	for i := 1; i < len(req.Contents); i++ {
		if req.Contents[i-1].Role == req.Contents[i].Role {
			t.Errorf("adjacent %q messages at indices %d and %d", req.Contents[i].Role, i-1, i)
		}
	}
}

// --- Provider option tests ---

func TestWithCredentialsFileOption(t *testing.T) {
	p := New(WithCredentialsFile("/path/to/creds.json"))
	if p.credentialsFile != "/path/to/creds.json" {
		t.Errorf("expected credentialsFile '/path/to/creds.json', got %q", p.credentialsFile)
	}
}

func TestWithCredentialsJSONOption(t *testing.T) {
	data := []byte(`{"type":"service_account"}`)
	p := New(WithCredentialsJSON(data))
	if string(p.credentialsJSON) != string(data) {
		t.Errorf("expected credentialsJSON to be set")
	}
}

func TestWithHTTPClientOption(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	p := New(WithHTTPClient(client))
	if p.httpClient != client {
		t.Error("expected custom HTTP client to be set")
	}
}

// --- Endpoint tests ---

func TestEndpointGlobalLocation(t *testing.T) {
	p := New(WithProject("my-project"), WithLocation("global"), WithModel("gemini-2.5-flash"))
	expected := "https://aiplatform.googleapis.com/v1/projects/my-project/locations/global/publishers/google/models/gemini-2.5-flash"
	if got := p.endpoint(); got != expected {
		t.Errorf("endpoint = %q, want %q", got, expected)
	}
}

// --- mapFinishReason with empty candidates ---

func TestMapFinishReasonEmptyCandidates(t *testing.T) {
	resp := &geminiResponse{Candidates: nil}
	if got := mapFinishReason(resp); got != core.FinishReasonStop {
		t.Errorf("mapFinishReason(empty candidates) = %s, want FinishReasonStop", got)
	}
}

// --- parseResponse with empty candidates ---

func TestParseResponseEmptyCandidates(t *testing.T) {
	resp := &geminiResponse{Candidates: nil}
	result := parseResponse(resp, "gemini-2.5-flash")
	if len(result.Parts) != 0 {
		t.Errorf("expected 0 parts for empty candidates, got %d", len(result.Parts))
	}
}

// --- parseResponse with nil function call args ---

func TestParseResponseNilFunctionCallArgs(t *testing.T) {
	resp := &geminiResponse{
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Role: "model",
					Parts: []geminiPart{
						{
							FunctionCall: &geminiFunctionCall{
								Name: "ping",
								Args: nil,
							},
						},
					},
				},
				FinishReason: "STOP",
			},
		},
	}
	result := parseResponse(resp, "gemini-2.5-flash")
	tc, ok := result.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatal("expected ToolCallPart")
	}
	if tc.ArgsJSON != "{}" {
		t.Errorf("expected '{}' for nil args, got %q", tc.ArgsJSON)
	}
}

// --- parseHTTPError with Retry-After header ---

func TestRequestHTTPErrorRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "45")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"Quota exceeded"}}`))
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-central1"))
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
	httpErr, ok := err.(*core.ModelHTTPError)
	if !ok {
		t.Fatalf("expected *core.ModelHTTPError, got %T", err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", httpErr.StatusCode)
	}
	if httpErr.RetryAfter != 45*time.Second {
		t.Errorf("RetryAfter = %v, want 45s", httpErr.RetryAfter)
	}
}

// --- RequestStream HTTP error ---

func TestRequestStreamHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"Server error"}}`))
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-central1"))
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{
			base:      server.Client().Transport,
			targetURL: server.URL,
		},
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
		t.Fatalf("expected *core.ModelHTTPError, got %T", err)
	}
	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", httpErr.StatusCode)
	}
}

// --- Stream: Usage() accessor ---

func TestStreamUsageAccessor(t *testing.T) {
	sseData := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":8,"totalTokenCount":28}}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-2.5-flash")

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
	if usage.InputTokens != 20 {
		t.Errorf("InputTokens = %d, want 20", usage.InputTokens)
	}
	if usage.OutputTokens != 8 {
		t.Errorf("OutputTokens = %d, want 8", usage.OutputTokens)
	}
}

// --- Stream: invalid JSON skipped ---

func TestParseSSEStreamSkipsInvalidJSON(t *testing.T) {
	sseData := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hi"}]},"finishReason":""}]}

data: not-valid-json

data: {"candidates":[{"content":{"role":"model","parts":[{"text":"!"}]},"finishReason":"STOP"}]}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-2.5-flash")

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
data: {"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}]}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-2.5-flash")

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

// --- Stream: no-candidate chunk skipped but usage captured ---

func TestParseSSEStreamSkipsNoCandidates(t *testing.T) {
	sseData := `data: {"candidates":[],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":0}}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-2.5-flash")

	event, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := event.(core.PartStartEvent); !ok {
		t.Fatalf("expected PartStartEvent, got %T", event)
	}
}

// --- Stream: function call with thought signature ---

func TestParseSSEStreamFunctionCallThoughtSigPreserved(t *testing.T) {
	sseData := `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"search","args":{"q":"test"}},"thoughtSignature":"sig_abc"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-3.1-pro-preview")

	event, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	start, ok := event.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent, got %T", event)
	}
	tc, ok := start.Part.(core.ToolCallPart)
	if !ok {
		t.Fatalf("expected ToolCallPart, got %T", start.Part)
	}
	if tc.Metadata == nil || tc.Metadata["thoughtSignature"] != "sig_abc" {
		t.Errorf("expected thoughtSignature 'sig_abc', got %v", tc.Metadata)
	}
}

// --- Stream: function call with nil args ---

func TestParseSSEStreamFunctionCallNilArgs(t *testing.T) {
	sseData := `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"ping"}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}

`
	body := io.NopCloser(strings.NewReader(sseData))
	stream := newStreamedResponse(body, "gemini-2.5-flash")

	event, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	start, ok := event.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent, got %T", event)
	}
	tc, ok := start.Part.(core.ToolCallPart)
	if !ok {
		t.Fatalf("expected ToolCallPart, got %T", start.Part)
	}
	if tc.ArgsJSON != "{}" {
		t.Errorf("expected '{}' for nil args, got %q", tc.ArgsJSON)
	}
}

// --- buildRequest with TopP ---

func TestBuildRequestWithTopP(t *testing.T) {
	topP := 0.95
	settings := &core.ModelSettings{TopP: &topP}
	req, err := buildRequest(nil, settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.GenerationConfig == nil {
		t.Fatal("expected GenerationConfig")
	}
	if req.GenerationConfig.TopP == nil || *req.GenerationConfig.TopP != 0.95 {
		t.Errorf("expected TopP 0.95, got %v", req.GenerationConfig.TopP)
	}
}

// --- Cached content tests ---

func TestWithCachedContentOption(t *testing.T) {
	p := New(WithCachedContent("projects/my-project/locations/us-central1/cachedContents/abc123"))
	if p.cachedContent != "projects/my-project/locations/us-central1/cachedContents/abc123" {
		t.Errorf("expected cachedContent to be set, got %q", p.cachedContent)
	}
}

func TestCachedContentEnvVar(t *testing.T) {
	t.Setenv("VERTEXAI_CACHED_CONTENT", "projects/test/locations/us/cachedContents/env123")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	p := New()
	if p.cachedContent != "projects/test/locations/us/cachedContents/env123" {
		t.Errorf("expected cachedContent from env, got %q", p.cachedContent)
	}
}

func TestCachedContentOptionOverridesEnv(t *testing.T) {
	t.Setenv("VERTEXAI_CACHED_CONTENT", "from-env")
	p := New(WithCachedContent("from-option"))
	if p.cachedContent != "from-option" {
		t.Errorf("expected option to override env, got %q", p.cachedContent)
	}
}

func TestCachedContentDisabledByDefault(t *testing.T) {
	t.Setenv("VERTEXAI_CACHED_CONTENT", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	p := New()
	if p.cachedContent != "" {
		t.Errorf("expected empty cachedContent by default, got %q", p.cachedContent)
	}
}

func TestCachedContentInRequestPayload(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		resp := geminiResponse{
			Candidates: []geminiCandidate{{
				Content:      geminiContent{Role: "model", Parts: []geminiPart{{Text: "cached response"}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: geminiUsage{PromptTokenCount: 5, CandidatesTokenCount: 3},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(
		WithProject("test-project"),
		WithLocation("us-central1"),
		WithCachedContent("projects/test-project/locations/us-central1/cachedContents/cache-abc"),
	)
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{base: server.Client().Transport, targetURL: server.URL},
	}

	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}}},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatal(err)
	}
	cc, ok := payload["cachedContent"].(string)
	if !ok || cc != "projects/test-project/locations/us-central1/cachedContents/cache-abc" {
		t.Errorf("expected cachedContent in payload, got %v", payload["cachedContent"])
	}
}

func TestCachedContentOmittedFromPayloadWhenUnset(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		resp := geminiResponse{
			Candidates: []geminiCandidate{{
				Content:      geminiContent{Role: "model", Parts: []geminiPart{{Text: "no cache"}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: geminiUsage{PromptTokenCount: 5, CandidatesTokenCount: 3},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(WithProject("test-project"), WithLocation("us-central1"))
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{base: server.Client().Transport, targetURL: server.URL},
	}

	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}}},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["cachedContent"]; exists {
		t.Error("cachedContent should be omitted from payload when not configured")
	}
}

func TestCachedContentInStreamingPayload(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hi\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":1}}\n\n")
	}))
	defer server.Close()

	p := New(
		WithProject("test-project"),
		WithLocation("us-central1"),
		WithCachedContent("projects/test-project/locations/us-central1/cachedContents/stream-cache"),
	)
	p.tokenSource = &staticTokenSource{token: "test-token"}
	p.httpClient = &http.Client{
		Transport: &rewriteTransport{base: server.Client().Transport, targetURL: server.URL},
	}

	stream, err := p.RequestStream(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}}},
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
	cc, ok := payload["cachedContent"].(string)
	if !ok || cc != "projects/test-project/locations/us-central1/cachedContents/stream-cache" {
		t.Errorf("expected cachedContent in streaming payload, got %v", payload["cachedContent"])
	}
}

func TestMapUsageWithCachedContentTokens(t *testing.T) {
	u := geminiUsage{
		PromptTokenCount:        100,
		CandidatesTokenCount:    50,
		TotalTokenCount:         150,
		CachedContentTokenCount: 80,
	}
	usage := mapUsage(u)
	if usage.InputTokens != 100 {
		t.Errorf("expected InputTokens 100, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 50 {
		t.Errorf("expected OutputTokens 50, got %d", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 80 {
		t.Errorf("expected CacheReadTokens 80, got %d", usage.CacheReadTokens)
	}
}

func TestMapUsageWithoutCachedContentTokens(t *testing.T) {
	u := geminiUsage{
		PromptTokenCount:     100,
		CandidatesTokenCount: 50,
		TotalTokenCount:      150,
	}
	usage := mapUsage(u)
	if usage.CacheReadTokens != 0 {
		t.Errorf("expected CacheReadTokens 0 when not set, got %d", usage.CacheReadTokens)
	}
}

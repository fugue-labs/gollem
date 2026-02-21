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

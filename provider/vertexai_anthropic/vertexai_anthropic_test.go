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

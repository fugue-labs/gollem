package vertexai_anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fugue-labs/gollem/provider/conformance"
)

func TestCatalogCapabilityConformance(t *testing.T) {
	// Keep this list in lockstep with the Vertex Anthropic entries in the
	// app-server catalog. Haiku deliberately omits reasoning visibility.
	for _, tc := range []struct {
		model     string
		reasoning bool
	}{
		{ClaudeSonnet46, true},
		{ClaudeOpus46, true},
		{ClaudeOpus47, true},
		{ClaudeHaiku45, false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			fixture := &vertexAnthropicConformanceFixture{t: t, model: tc.model}
			server := httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
			defer server.Close()

			model := New(
				WithProject("conformance-project"),
				WithLocation("us-east5"),
				WithModel(tc.model),
				WithPromptCaching(true),
			)
			model.tokenSource = &staticTokenSource{token: "conformance-token"}
			model.httpClient = &http.Client{Transport: &rewriteTransport{
				base:      server.Client().Transport,
				targetURL: server.URL,
			}}

			claims := conformance.Claims{ToolCalls: true, StructuredOutput: true, Vision: true, PromptCacheActivation: true, Streaming: true, Usage: true, ReasoningVisibility: tc.reasoning}
			driver := conformance.Driver{
				Name:                  "Vertex AI Anthropic " + tc.model,
				Model:                 model,
				Claims:                claims,
				PromptCacheActivation: fixture.verify,
				Expectations: conformance.Expectations{
					ResponseText:          "vertex anthropic response",
					ToolName:              "conformance_echo",
					ToolCallID:            "call_vertex_anthropic",
					ToolArgumentsJSON:     `{"value":"ok"}`,
					StructuredOutputValue: "vertex anthropic structured",
					VisionText:            "vertex anthropic vision",
					StreamText:            "vertex anthropic stream",
					ReasoningText:         "vertex anthropic reasoning",
				},
			}
			if tc.reasoning {
				driver.ReasoningModel = model
			}
			if err := conformance.Verify(t.Context(), driver); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type vertexAnthropicConformanceFixture struct {
	t     *testing.T
	model string

	mu                 sync.Mutex
	cachedRequestCount int
	toolRequestCount   int
	visionRequestCount int
}

func (f *vertexAnthropicConformanceFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "Bearer conformance-token" {
		f.t.Errorf("Authorization = %q, want conformance bearer token", got)
	}
	if want := "/models/" + f.model + ":"; !strings.Contains(r.URL.Path, want) {
		f.t.Errorf("Vertex Anthropic request path = %q, want model route containing %q", r.URL.Path, want)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("read Vertex Anthropic conformance request: %v", err)
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		f.t.Errorf("decode Vertex Anthropic conformance request: %v", err)
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if cacheControl, _ := request["cache_control"].(map[string]any); cacheControl["type"] == "ephemeral" {
		f.mu.Lock()
		f.cachedRequestCount++
		f.mu.Unlock()
	}

	payload := string(body)
	if strings.HasSuffix(r.URL.Path, ":streamRawPredict") {
		if strings.Contains(payload, "reasoning conformance") {
			f.writeReasoningStream(w)
			return
		}
		f.writeStream(w)
		return
	}

	switch {
	case strings.Contains(payload, "structured output conformance"):
		if !vertexAnthropicHasTool(request, "final_result") {
			f.t.Error("Vertex Anthropic structured-output request omitted final_result tool")
		}
		f.writeResponse(w, []any{map[string]any{
			"type": "tool_use", "id": "call_vertex_structured", "name": "final_result",
			"input": map[string]any{"value": "vertex anthropic structured"},
		}}, "tool_use")
	case strings.Contains(payload, "vision conformance"):
		if !vertexAnthropicHasImage(request) {
			f.t.Error("Vertex Anthropic vision request omitted a base64 image block")
		}
		f.mu.Lock()
		f.visionRequestCount++
		f.mu.Unlock()
		f.writeResponse(w, []any{map[string]any{"type": "text", "text": "vertex anthropic vision"}}, "end_turn")
	default:
		if !vertexAnthropicHasTool(request, "conformance_echo") {
			f.t.Error("Vertex Anthropic tool request omitted conformance_echo")
		}
		f.mu.Lock()
		f.toolRequestCount++
		f.mu.Unlock()
		f.writeResponse(w, []any{
			map[string]any{"type": "text", "text": "vertex anthropic response"},
			map[string]any{
				"type": "tool_use", "id": "call_vertex_anthropic", "name": "conformance_echo",
				"input": map[string]any{"value": "ok"},
			},
		}, "tool_use")
	}
}

func (f *vertexAnthropicConformanceFixture) writeResponse(w http.ResponseWriter, content []any, stopReason string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"content":     content,
		"stop_reason": stopReason,
		"usage":       map[string]any{"input_tokens": 3, "output_tokens": 2},
	})
}

func (f *vertexAnthropicConformanceFixture) writeStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"vertex anthropic stream"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}

`)
}

func (f *vertexAnthropicConformanceFixture) writeReasoningStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"vertex anthropic reasoning"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}

`)
}

func (f *vertexAnthropicConformanceFixture) verify() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cachedRequestCount < 2 {
		return fmt.Errorf("cache_control appeared on %d requests, want normal and streaming requests", f.cachedRequestCount)
	}
	if f.toolRequestCount == 0 {
		return fmt.Errorf("fixture did not observe the declared function tool")
	}
	if f.visionRequestCount == 0 {
		return fmt.Errorf("fixture did not observe the image request")
	}
	return nil
}

func vertexAnthropicHasTool(request map[string]any, name string) bool {
	tools, _ := request["tools"].([]any)
	for _, rawTool := range tools {
		tool, _ := rawTool.(map[string]any)
		if tool["name"] == name {
			return true
		}
	}
	return false
}

func vertexAnthropicHasImage(request map[string]any) bool {
	messages, _ := request["messages"].([]any)
	for _, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		blocks, _ := message["content"].([]any)
		for _, rawBlock := range blocks {
			block, _ := rawBlock.(map[string]any)
			source, _ := block["source"].(map[string]any)
			if block["type"] == "image" && source["type"] == "base64" {
				return true
			}
		}
	}
	return false
}

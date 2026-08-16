package vertexai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fugue-labs/gollem/provider/conformance"
)

func TestCatalogCapabilityConformance(t *testing.T) {
	// Keep this list in lockstep with the Vertex Gemini entries in the
	// app-server catalog. The catalog exposes each profile as runnable, so the
	// fixture must observe its exact model route rather than a representative.
	for _, modelName := range []string{
		Gemini25Flash,
		Gemini25Pro,
		Gemini31ProPreview,
		Gemini3FlashPreview,
		Gemini20Flash,
	} {
		t.Run(modelName, func(t *testing.T) {
			fixture := &geminiConformanceFixture{t: t, model: modelName}
			server := httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
			defer server.Close()

			model := New(
				WithProject("conformance-project"),
				WithLocation("us-central1"),
				WithModel(modelName),
				WithCachedContent("projects/conformance/locations/us-central1/cachedContents/catalog-proof"),
			)
			model.tokenSource = &staticTokenSource{token: "conformance-token"}
			model.httpClient = &http.Client{Transport: &rewriteTransport{
				base:      server.Client().Transport,
				targetURL: server.URL,
			}}

			err := conformance.Verify(t.Context(), conformance.Driver{
				Name:                    "Vertex AI Gemini " + modelName,
				Model:                   model,
				Claims:                  conformance.Claims{ToolCalls: true, StructuredOutput: true, Vision: true, PromptCacheActivation: true, StopSequences: true, Streaming: true, Usage: true},
				PromptCacheActivation:   fixture.verify,
				StopSequencesActivation: fixture.verifyStopSequences,
				Expectations: conformance.Expectations{
					ResponseText:          "vertex gemini response",
					ToolName:              "conformance_echo",
					ToolCallID:            "call_0",
					ToolArgumentsJSON:     `{"value":"ok"}`,
					StructuredOutputValue: "vertex gemini structured",
					VisionText:            "vertex gemini vision",
					StreamText:            "vertex gemini stream",
					StopSequenceText:      "vertex gemini stop sequences",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

type geminiConformanceFixture struct {
	t     *testing.T
	model string

	mu                 sync.Mutex
	cachedRequestCount int
	toolRequestCount   int
	visionRequestCount int
	stopSequenceCount  int
}

func (f *geminiConformanceFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "Bearer conformance-token" {
		f.t.Errorf("Authorization = %q, want conformance bearer token", got)
	}
	if want := "/models/" + f.model + ":"; !strings.Contains(r.URL.Path, want) {
		f.t.Errorf("Vertex Gemini request path = %q, want model route containing %q", r.URL.Path, want)
	}

	var request map[string]any
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		f.t.Errorf("decode Gemini conformance request: %v", err)
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if request["cachedContent"] == "projects/conformance/locations/us-central1/cachedContents/catalog-proof" {
		f.mu.Lock()
		f.cachedRequestCount++
		f.mu.Unlock()
	}

	prompt := geminiConformancePrompt(request)
	if strings.HasSuffix(r.URL.Path, ":streamGenerateContent") {
		f.writeStream(w)
		return
	}

	switch {
	case strings.Contains(prompt, "stop sequence conformance"):
		if !geminiConformanceHasStopSequences(request, []string{"conformance-stop"}) {
			f.t.Errorf("Gemini stop-sequence request = %#v, want generationConfig.stopSequences [conformance-stop]", request)
		}
		f.mu.Lock()
		f.stopSequenceCount++
		f.mu.Unlock()
		f.writeResponse(w, "vertex gemini stop sequences", nil)
	case strings.Contains(prompt, "structured output conformance"):
		f.writeResponse(w, "{\"value\":\"vertex gemini structured\"}", nil)
	case strings.Contains(prompt, "vision conformance"):
		if !geminiConformanceHasInlineImage(request) {
			f.t.Error("Gemini vision request omitted inlineData image input")
		}
		f.mu.Lock()
		f.visionRequestCount++
		f.mu.Unlock()
		f.writeResponse(w, "vertex gemini vision", nil)
	default:
		if !geminiConformanceHasFunction(request, "conformance_echo") {
			f.t.Error("Gemini tool request omitted conformance_echo function declaration")
		}
		f.mu.Lock()
		f.toolRequestCount++
		f.mu.Unlock()
		f.writeResponse(w, "vertex gemini response", map[string]any{"name": "conformance_echo", "args": map[string]any{"value": "ok"}})
	}
}

func (f *geminiConformanceFixture) verifyStopSequences() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopSequenceCount != 1 {
		return fmt.Errorf("fixture observed %d stop-sequence requests, want 1", f.stopSequenceCount)
	}
	return nil
}

func (f *geminiConformanceFixture) writeResponse(w http.ResponseWriter, text string, functionCall map[string]any) {
	parts := []any{map[string]any{"text": text}}
	if functionCall != nil {
		parts = append(parts, map[string]any{"functionCall": functionCall})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"role": "model", "parts": parts},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{"promptTokenCount": 3, "candidatesTokenCount": 2},
	})
}

func (f *geminiConformanceFixture) writeStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"vertex gemini stream\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2}}\n\n")
}

func (f *geminiConformanceFixture) verify() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cachedRequestCount < 2 {
		return fmt.Errorf("cached content appeared on %d requests, want normal and streaming requests", f.cachedRequestCount)
	}
	if f.toolRequestCount == 0 {
		return fmt.Errorf("fixture did not observe the declared function tool")
	}
	if f.visionRequestCount == 0 {
		return fmt.Errorf("fixture did not observe the image request")
	}
	return nil
}

func geminiConformancePrompt(request map[string]any) string {
	contents, _ := request["contents"].([]any)
	var text strings.Builder
	for _, rawContent := range contents {
		content, _ := rawContent.(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if value, _ := part["text"].(string); value != "" {
				text.WriteString(value)
			}
		}
	}
	return text.String()
}

func geminiConformanceHasFunction(request map[string]any, name string) bool {
	tools, _ := request["tools"].([]any)
	for _, rawTool := range tools {
		tool, _ := rawTool.(map[string]any)
		functions, _ := tool["functionDeclarations"].([]any)
		for _, rawFunction := range functions {
			function, _ := rawFunction.(map[string]any)
			if function["name"] == name {
				return true
			}
		}
	}
	return false
}

func geminiConformanceHasInlineImage(request map[string]any) bool {
	contents, _ := request["contents"].([]any)
	for _, rawContent := range contents {
		content, _ := rawContent.(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if _, ok := part["inlineData"].(map[string]any); ok {
				return true
			}
		}
	}
	return false
}

func geminiConformanceHasStopSequences(request map[string]any, want []string) bool {
	generationConfig, _ := request["generationConfig"].(map[string]any)
	values, _ := generationConfig["stopSequences"].([]any)
	if len(values) != len(want) {
		return false
	}
	for index, value := range values {
		if value != want[index] {
			return false
		}
	}
	return true
}

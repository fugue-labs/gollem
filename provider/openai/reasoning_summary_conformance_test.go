package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fugue-labs/gollem/provider/conformance"
)

func TestCatalogReasoningSummaryConformance(t *testing.T) {
	for _, modelName := range []string{GPT5, GPT5Mini, GPT5Nano, GPT5Codex} {
		t.Run(modelName, func(t *testing.T) {
			fixture := &reasoningSummaryConformanceFixture{t: t, model: modelName}
			server := httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
			defer server.Close()

			model := New(
				WithAPIKey("conformance-token"),
				WithBaseURL(server.URL+"/openai.com"),
				WithModel(modelName),
			)
			err := conformance.Verify(t.Context(), conformance.Driver{
				Name:                  "OpenAI reasoning summary " + modelName,
				Model:                 model,
				ReasoningSummaryModel: model,
				Claims:                conformance.Claims{ReasoningSummary: true},
				Expectations: conformance.Expectations{
					ResponseText:         "openai summary response",
					ReasoningSummaryText: "openai selected summary",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !fixture.summaryRequest {
				t.Fatal("reasoning summary fixture did not observe the selected summary request")
			}
		})
	}
}

type reasoningSummaryConformanceFixture struct {
	t              *testing.T
	model          string
	summaryRequest bool
}

func (f *reasoningSummaryConformanceFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/openai.com/v1/responses" {
		f.t.Errorf("path = %q, want /openai.com/v1/responses", r.URL.Path)
		http.NotFound(w, r)
		return
	}
	if got := r.Header.Get("Authorization"); got != "Bearer conformance-token" {
		f.t.Errorf("Authorization = %q, want static bearer token", got)
	}
	var req responsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("decode request: %v", err)
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Model != f.model {
		f.t.Errorf("model = %q, want %q", req.Model, f.model)
	}
	if req.Stream == nil || !*req.Stream {
		f.writeResponse(w)
		return
	}
	if req.Reasoning == nil || req.Reasoning.Summary != "concise" {
		f.t.Errorf("reasoning summary = %#v, want concise", req.Reasoning)
	}
	f.summaryRequest = true
	f.writeStream(w)
}

func (f *reasoningSummaryConformanceFixture) writeResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(responsesAPIResponse{
		ID:    "resp-summary-normal",
		Model: f.model,
		Output: []responsesOutputItem{{
			Type:    "message",
			Role:    "assistant",
			Content: []responsesContentItem{{Type: "output_text", Text: "openai summary response"}},
		}},
		Usage: responsesUsage{InputTokens: 3, OutputTokens: 2},
	})
}

func (f *reasoningSummaryConformanceFixture) writeStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","summary":[]}}

data: {"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"openai selected summary"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","summary":[{"type":"summary_text","text":"openai selected summary"}]}}

data: {"type":"response.completed","response":{"id":"resp-summary-stream","model":"`+f.model+`","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"openai selected summary"}]}],"usage":{"input_tokens":3,"output_tokens":2}}}

data: [DONE]

`)
}

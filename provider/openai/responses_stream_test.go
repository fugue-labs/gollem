package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestResponsesStreamedResponse_TextOnly(t *testing.T) {
	sse := `data: {"type":"response.created","response":{"id":"resp-1"}}

data: {"type":"response.output_item.added","item":{"type":"message","role":"assistant"}}

data: {"type":"response.output_text.delta","delta":"Hello "}

data: {"type":"response.output_text.delta","delta":"world!"}

data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello world!"}]}}

data: {"type":"response.completed","response":{"id":"resp-1","output":[],"usage":{"input_tokens":10,"output_tokens":5}}}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sse))
	s := newResponsesStreamedResponse(body, "gpt-5")

	assertResponsesStreamTextOnly(t, s)
}

func TestResponsesStreamedResponse_TextOnlyAcceptsResponseDone(t *testing.T) {
	sse := `data: {"type":"response.created","response":{"id":"resp-1"}}

data: {"type":"response.output_item.added","item":{"type":"message","role":"assistant"}}

data: {"type":"response.output_text.delta","delta":"Hello "}

data: {"type":"response.output_text.delta","delta":"world!"}

data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello world!"}]}}

data: {"type":"response.done","response":{"id":"resp-1","output":[],"usage":{"input_tokens":10,"output_tokens":5}}}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sse))
	s := newResponsesStreamedResponse(body, "gpt-5")

	assertResponsesStreamTextOnly(t, s)
}

func assertResponsesStreamTextOnly(t *testing.T, s *responsesStreamedResponse) {
	t.Helper()

	// First event: PartStartEvent with first delta.
	ev1, err := s.Next()
	if err != nil {
		t.Fatalf("Next() #1: %v", err)
	}
	start, ok := ev1.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent, got %T", ev1)
	}
	if tp, ok := start.Part.(core.TextPart); !ok || tp.Content != "Hello " {
		t.Errorf("expected TextPart 'Hello ', got %v", start.Part)
	}

	// Second event: PartDeltaEvent with second delta.
	ev2, err := s.Next()
	if err != nil {
		t.Fatalf("Next() #2: %v", err)
	}
	delta, ok := ev2.(core.PartDeltaEvent)
	if !ok {
		t.Fatalf("expected PartDeltaEvent, got %T", ev2)
	}
	if td, ok := delta.Delta.(core.TextPartDelta); !ok || td.ContentDelta != "world!" {
		t.Errorf("expected delta 'world!', got %v", delta.Delta)
	}

	// No more events (output_item.done for message is suppressed since text
	// was already streamed, and the terminal response event triggers finalization).
	_, err = s.Next()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}

	// Verify final response.
	resp := s.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	if tp, ok := resp.Parts[0].(core.TextPart); !ok || tp.Content != "Hello world!" {
		t.Errorf("expected accumulated text 'Hello world!', got %v", resp.Parts[0])
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestResponsesStreamedResponse_ToolCall(t *testing.T) {
	sse := `data: {"type":"response.output_item.done","item":{"type":"function_call","name":"bash","call_id":"call_abc","arguments":"{\"cmd\":\"ls\"}"}}

data: {"type":"response.completed","response":{"id":"resp-2","output":[],"usage":{"input_tokens":20,"output_tokens":10}}}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sse))
	s := newResponsesStreamedResponse(body, "gpt-5")

	ev1, err := s.Next()
	if err != nil {
		t.Fatalf("Next(): %v", err)
	}
	start, ok := ev1.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent, got %T", ev1)
	}
	tc, ok := start.Part.(core.ToolCallPart)
	if !ok {
		t.Fatalf("expected ToolCallPart, got %T", start.Part)
	}
	if tc.ToolName != "bash" || tc.ToolCallID != "call_abc" || tc.ArgsJSON != `{"cmd":"ls"}` {
		t.Errorf("unexpected tool call: %+v", tc)
	}

	_, err = s.Next()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}

	resp := s.Response()
	if resp.FinishReason != core.FinishReasonToolCall {
		t.Errorf("expected FinishReasonToolCall, got %v", resp.FinishReason)
	}
}

func TestResponsesStreamedResponse_Failed(t *testing.T) {
	sse := `data: {"type":"response.failed","response":{"error":{"message":"context length exceeded","code":"context_length_exceeded"}}}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sse))
	s := newResponsesStreamedResponse(body, "gpt-5")

	_, err := s.Next()
	if err == nil {
		t.Fatal("expected error from response.failed, got nil")
	}
	if strings.Contains(err.Error(), "context length exceeded") {
		t.Errorf("response failure leaked provider message: %v", err)
	}
	if !strings.Contains(err.Error(), "context_length_exceeded") {
		t.Errorf("expected source-free failure classification, got: %v", err)
	}
}

func TestResponsesStreamedResponse_Incomplete(t *testing.T) {
	// Verify that response.incomplete preserves partial text and usage.
	sse := `data: {"type":"response.output_text.delta","delta":"partial "}

data: {"type":"response.output_text.delta","delta":"output"}

data: {"type":"response.incomplete","response":{"id":"resp-inc","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":100,"output_tokens":50}}}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sse))
	s := newResponsesStreamedResponse(body, "gpt-5")

	// Consume the two text deltas.
	s.Next() // PartStartEvent "partial "
	s.Next() // PartDeltaEvent "output"

	// Stream should be done (incomplete triggers finalization).
	_, err := s.Next()
	if err != io.EOF {
		t.Fatalf("expected io.EOF after incomplete, got %v", err)
	}

	resp := s.Response()
	// Partial text should be preserved via finalize().
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part (partial text), got %d", len(resp.Parts))
	}
	if tp, ok := resp.Parts[0].(core.TextPart); !ok || tp.Content != "partial output" {
		t.Errorf("expected 'partial output', got %v", resp.Parts[0])
	}
	if resp.FinishReason != core.FinishReasonLength {
		t.Errorf("expected FinishReasonLength, got %v", resp.FinishReason)
	}
	// Usage should be extracted from the incomplete response.
	if resp.Usage.InputTokens != 100 || resp.Usage.OutputTokens != 50 {
		t.Errorf("expected usage {100, 50}, got %+v", resp.Usage)
	}
}

func TestResponsesStreamedResponse_TextAndToolCall(t *testing.T) {
	sse := `data: {"type":"response.output_text.delta","delta":"Thinking..."}

data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Thinking..."}]}}

data: {"type":"response.output_item.done","item":{"type":"function_call","name":"grep","call_id":"call_1","arguments":"{\"pattern\":\"foo\"}"}}

data: {"type":"response.completed","response":{"id":"resp-3","output":[],"usage":{"input_tokens":30,"output_tokens":15}}}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sse))
	s := newResponsesStreamedResponse(body, "gpt-5")

	// Text start.
	ev1, err := s.Next()
	if err != nil {
		t.Fatalf("Next() #1: %v", err)
	}
	if _, ok := ev1.(core.PartStartEvent); !ok {
		t.Fatalf("expected PartStartEvent, got %T", ev1)
	}

	// Tool call.
	ev2, err := s.Next()
	if err != nil {
		t.Fatalf("Next() #2: %v", err)
	}
	start, ok := ev2.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent for tool call, got %T", ev2)
	}
	if _, ok := start.Part.(core.ToolCallPart); !ok {
		t.Fatalf("expected ToolCallPart, got %T", start.Part)
	}

	_, err = s.Next()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}

	resp := s.Response()
	if len(resp.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(resp.Parts))
	}
	// Text at index 0, tool call at index 1.
	if _, ok := resp.Parts[0].(core.TextPart); !ok {
		t.Errorf("expected TextPart at index 0, got %T", resp.Parts[0])
	}
	if _, ok := resp.Parts[1].(core.ToolCallPart); !ok {
		t.Errorf("expected ToolCallPart at index 1, got %T", resp.Parts[1])
	}
	if resp.FinishReason != core.FinishReasonToolCall {
		t.Errorf("expected FinishReasonToolCall, got %v", resp.FinishReason)
	}
}

func TestRequestStreamViaResponses(t *testing.T) {
	sse := `data: {"type":"response.output_text.delta","delta":"Hi"}

data: {"type":"response.output_text.delta","delta":" there"}

data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi there"}]}}

data: {"type":"response.completed","response":{"id":"resp-4","output":[],"usage":{"input_tokens":5,"output_tokens":3}}}

data: [DONE]
`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify streaming request.
		var req responsesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Stream == nil || !*req.Stream {
			t.Error("expected stream=true in request")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(sse))
	}))
	defer ts.Close()

	p := New(
		WithModel("gpt-5"),
		WithAPIKey("test-key"),
		WithBaseURL(ts.URL),
	)
	p.useResponses = true

	stream, err := p.RequestStream(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hi"}}},
	}, nil, nil)
	if err != nil {
		t.Fatalf("RequestStream: %v", err)
	}
	defer stream.Close()

	// Consume stream.
	var text strings.Builder
	for {
		ev, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		switch e := ev.(type) {
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
		t.Errorf("expected streamed text 'Hi there', got %q", text.String())
	}

	resp := stream.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 3 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestResponsesStreamedResponse_IncrementalToolCall(t *testing.T) {
	// Verify incremental tool call argument streaming via output_item.added
	// + function_call_arguments.delta + output_item.done.
	sse := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"bash","call_id":"call_xyz","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"cm"}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"d\":\"ls\"}"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","name":"bash","call_id":"call_xyz","arguments":"{\"cmd\":\"ls\"}"}}

data: {"type":"response.completed","response":{"id":"resp-5","output":[],"usage":{"input_tokens":10,"output_tokens":5}}}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sse))
	s := newResponsesStreamedResponse(body, "gpt-5")

	// First event: PartStartEvent for the tool call (from output_item.added).
	ev1, err := s.Next()
	if err != nil {
		t.Fatalf("Next() #1: %v", err)
	}
	start, ok := ev1.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent, got %T", ev1)
	}
	tc, ok := start.Part.(core.ToolCallPart)
	if !ok {
		t.Fatalf("expected ToolCallPart, got %T", start.Part)
	}
	if tc.ToolName != "bash" || tc.ToolCallID != "call_xyz" {
		t.Errorf("unexpected tool call start: %+v", tc)
	}

	// Second event: PartDeltaEvent with first args delta.
	ev2, err := s.Next()
	if err != nil {
		t.Fatalf("Next() #2: %v", err)
	}
	delta1, ok := ev2.(core.PartDeltaEvent)
	if !ok {
		t.Fatalf("expected PartDeltaEvent, got %T", ev2)
	}
	if td, ok := delta1.Delta.(core.ToolCallPartDelta); !ok || td.ArgsJSONDelta != `{"cm` {
		t.Errorf("expected args delta '{\"cm', got %v", delta1.Delta)
	}

	// Third event: PartDeltaEvent with second args delta.
	ev3, err := s.Next()
	if err != nil {
		t.Fatalf("Next() #3: %v", err)
	}
	delta2, ok := ev3.(core.PartDeltaEvent)
	if !ok {
		t.Fatalf("expected PartDeltaEvent, got %T", ev3)
	}
	if td, ok := delta2.Delta.(core.ToolCallPartDelta); !ok || td.ArgsJSONDelta != `d":"ls"}` {
		t.Errorf("expected args delta 'd\":\"ls\"}', got %v", delta2.Delta)
	}

	// No more events (output_item.done finalizes, response.completed triggers finalization).
	_, err = s.Next()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}

	// Verify final response.
	resp := s.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	tc2, ok := resp.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatalf("expected ToolCallPart, got %T", resp.Parts[0])
	}
	if tc2.ArgsJSON != `{"cmd":"ls"}` {
		t.Errorf("expected args '{\"cmd\":\"ls\"}', got %q", tc2.ArgsJSON)
	}
	if resp.FinishReason != core.FinishReasonToolCall {
		t.Errorf("expected FinishReasonToolCall, got %v", resp.FinishReason)
	}
}

func TestResponsesStreamedResponse_IncrementalToolCall_IncompleteStream(t *testing.T) {
	// Verify that finalize() flushes accumulated tool call args when
	// the stream ends before output_item.done (e.g., response.incomplete).
	sse := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"bash","call_id":"call_inc","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"partial\":"}

data: {"type":"response.incomplete","response":{"id":"resp-inc","usage":{"input_tokens":10,"output_tokens":5}}}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sse))
	s := newResponsesStreamedResponse(body, "gpt-5")

	// Consume events: PartStartEvent + PartDeltaEvent.
	s.Next() // PartStartEvent
	s.Next() // PartDeltaEvent

	// Stream ends.
	_, err := s.Next()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}

	resp := s.Response()
	if len(resp.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Parts))
	}
	tc, ok := resp.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatalf("expected ToolCallPart, got %T", resp.Parts[0])
	}
	// Should have the partial args from the buffer.
	if tc.ArgsJSON != `{"partial":` {
		t.Errorf("expected partial args '{\"partial\":', got %q", tc.ArgsJSON)
	}
}

func TestRequestStreamViaResponses_JSONFallback(t *testing.T) {
	// Verify that when a server returns JSON instead of SSE (e.g., streaming
	// not supported), RequestStream still returns a valid StreamedResponse.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(responsesAPIResponse{
			Output: []responsesOutputItem{
				{Type: "message", Content: []responsesContentItem{{Type: "output_text", Text: "fallback response"}}},
			},
			Usage: responsesUsage{InputTokens: 8, OutputTokens: 4},
		})
	}))
	defer ts.Close()

	p := New(
		WithModel("test-model"),
		WithAPIKey("test-key"),
		WithBaseURL(ts.URL),
	)
	p.useResponses = true

	stream, err := p.RequestStream(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hi"}}},
	}, nil, nil)
	if err != nil {
		t.Fatalf("RequestStream: %v", err)
	}
	defer stream.Close()

	// Should yield PartStartEvent with the text.
	ev, err := stream.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	start, ok := ev.(core.PartStartEvent)
	if !ok {
		t.Fatalf("expected PartStartEvent, got %T", ev)
	}
	if tp, ok := start.Part.(core.TextPart); !ok || tp.Content != "fallback response" {
		t.Errorf("expected text 'fallback response', got %v", start.Part)
	}

	// Should be done.
	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}

	resp := stream.Response()
	if resp.Usage.InputTokens != 8 || resp.Usage.OutputTokens != 4 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

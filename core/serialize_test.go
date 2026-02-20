package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestMarshalUnmarshalMessages_RoundTrip(t *testing.T) {
	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	messages := []ModelMessage{
		ModelRequest{
			Parts: []ModelRequestPart{
				SystemPromptPart{Content: "You are a helpful assistant.", Timestamp: now},
				UserPromptPart{Content: "Hello!", Timestamp: now.Add(time.Second)},
			},
			Timestamp: now,
		},
		ModelResponse{
			Parts: []ModelResponsePart{
				TextPart{Content: "Hi there! How can I help you?"},
			},
			Usage:        Usage{InputTokens: 10, OutputTokens: 20},
			ModelName:    "test-model",
			FinishReason: FinishReasonStop,
			Timestamp:    now.Add(2 * time.Second),
		},
		ModelRequest{
			Parts: []ModelRequestPart{
				UserPromptPart{Content: "What is 2+2?", Timestamp: now.Add(3 * time.Second)},
			},
			Timestamp: now.Add(3 * time.Second),
		},
		ModelResponse{
			Parts: []ModelResponsePart{
				ToolCallPart{ToolName: "calculator", ArgsJSON: `{"expr":"2+2"}`, ToolCallID: "call-1"},
			},
			Usage:        Usage{InputTokens: 15, OutputTokens: 5},
			ModelName:    "test-model",
			FinishReason: FinishReasonToolCall,
			Timestamp:    now.Add(4 * time.Second),
		},
		ModelRequest{
			Parts: []ModelRequestPart{
				ToolReturnPart{
					ToolName:   "calculator",
					Content:    "4",
					ToolCallID: "call-1",
					Timestamp:  now.Add(5 * time.Second),
				},
			},
			Timestamp: now.Add(5 * time.Second),
		},
	}

	data, err := MarshalMessages(messages)
	if err != nil {
		t.Fatalf("MarshalMessages failed: %v", err)
	}

	got, err := UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages failed: %v", err)
	}

	if len(got) != len(messages) {
		t.Fatalf("message count = %d, want %d", len(got), len(messages))
	}

	// Verify first message (request with system + user prompts).
	req0, ok := got[0].(ModelRequest)
	if !ok {
		t.Fatal("message[0]: expected ModelRequest")
	}
	if !req0.Timestamp.Equal(now) {
		t.Errorf("message[0].Timestamp = %v, want %v", req0.Timestamp, now)
	}
	if len(req0.Parts) != 2 {
		t.Fatalf("message[0].Parts count = %d, want 2", len(req0.Parts))
	}
	sp, ok := req0.Parts[0].(SystemPromptPart)
	if !ok {
		t.Fatal("message[0].Parts[0]: expected SystemPromptPart")
	}
	if sp.Content != "You are a helpful assistant." {
		t.Errorf("SystemPromptPart.Content = %q, want %q", sp.Content, "You are a helpful assistant.")
	}
	if !sp.Timestamp.Equal(now) {
		t.Errorf("SystemPromptPart.Timestamp = %v, want %v", sp.Timestamp, now)
	}
	up, ok := req0.Parts[1].(UserPromptPart)
	if !ok {
		t.Fatal("message[0].Parts[1]: expected UserPromptPart")
	}
	if up.Content != "Hello!" {
		t.Errorf("UserPromptPart.Content = %q, want %q", up.Content, "Hello!")
	}

	// Verify second message (response with text).
	resp1, ok := got[1].(ModelResponse)
	if !ok {
		t.Fatal("message[1]: expected ModelResponse")
	}
	if resp1.ModelName != "test-model" {
		t.Errorf("message[1].ModelName = %q, want %q", resp1.ModelName, "test-model")
	}
	if resp1.FinishReason != FinishReasonStop {
		t.Errorf("message[1].FinishReason = %q, want %q", resp1.FinishReason, FinishReasonStop)
	}
	if resp1.Usage.InputTokens != 10 || resp1.Usage.OutputTokens != 20 {
		t.Errorf("message[1].Usage = %+v, want InputTokens=10, OutputTokens=20", resp1.Usage)
	}
	if len(resp1.Parts) != 1 {
		t.Fatalf("message[1].Parts count = %d, want 1", len(resp1.Parts))
	}
	tp, ok := resp1.Parts[0].(TextPart)
	if !ok {
		t.Fatal("message[1].Parts[0]: expected TextPart")
	}
	if tp.Content != "Hi there! How can I help you?" {
		t.Errorf("TextPart.Content = %q, want %q", tp.Content, "Hi there! How can I help you?")
	}

	// Verify fourth message (response with tool call).
	resp3, ok := got[3].(ModelResponse)
	if !ok {
		t.Fatal("message[3]: expected ModelResponse")
	}
	if len(resp3.Parts) != 1 {
		t.Fatalf("message[3].Parts count = %d, want 1", len(resp3.Parts))
	}
	tc, ok := resp3.Parts[0].(ToolCallPart)
	if !ok {
		t.Fatal("message[3].Parts[0]: expected ToolCallPart")
	}
	if tc.ToolName != "calculator" {
		t.Errorf("ToolCallPart.ToolName = %q, want %q", tc.ToolName, "calculator")
	}
	if tc.ArgsJSON != `{"expr":"2+2"}` {
		t.Errorf("ToolCallPart.ArgsJSON = %q, want %q", tc.ArgsJSON, `{"expr":"2+2"}`)
	}
	if tc.ToolCallID != "call-1" {
		t.Errorf("ToolCallPart.ToolCallID = %q, want %q", tc.ToolCallID, "call-1")
	}

	// Verify fifth message (request with tool return).
	req4, ok := got[4].(ModelRequest)
	if !ok {
		t.Fatal("message[4]: expected ModelRequest")
	}
	if len(req4.Parts) != 1 {
		t.Fatalf("message[4].Parts count = %d, want 1", len(req4.Parts))
	}
	tr, ok := req4.Parts[0].(ToolReturnPart)
	if !ok {
		t.Fatal("message[4].Parts[0]: expected ToolReturnPart")
	}
	if tr.ToolName != "calculator" {
		t.Errorf("ToolReturnPart.ToolName = %q, want %q", tr.ToolName, "calculator")
	}
	// After round-trip, string content comes back as string.
	if tr.Content != "4" {
		t.Errorf("ToolReturnPart.Content = %v, want %q", tr.Content, "4")
	}
	if tr.ToolCallID != "call-1" {
		t.Errorf("ToolReturnPart.ToolCallID = %q, want %q", tr.ToolCallID, "call-1")
	}
}

func TestMarshalMessages_AllPartTypes(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	messages := []ModelMessage{
		ModelRequest{
			Parts: []ModelRequestPart{
				SystemPromptPart{Content: "system", Timestamp: now},
				UserPromptPart{Content: "user input", Timestamp: now},
				ToolReturnPart{
					ToolName:   "search",
					Content:    map[string]any{"results": []any{"a", "b"}},
					ToolCallID: "tc-1",
					Timestamp:  now,
				},
				RetryPromptPart{
					Content:    "please retry",
					ToolName:   "search",
					ToolCallID: "tc-1",
					Timestamp:  now,
				},
			},
			Timestamp: now,
		},
		ModelResponse{
			Parts: []ModelResponsePart{
				TextPart{Content: "here are the results"},
				ToolCallPart{ToolName: "search", ArgsJSON: `{"q":"test"}`, ToolCallID: "tc-2"},
				ThinkingPart{Content: "I should search first", Signature: "sig-abc"},
			},
			Usage:        Usage{InputTokens: 100, OutputTokens: 50, CacheWriteTokens: 10, CacheReadTokens: 5},
			ModelName:    "gpt-4",
			FinishReason: FinishReasonStop,
			Timestamp:    now,
		},
	}

	data, err := MarshalMessages(messages)
	if err != nil {
		t.Fatalf("MarshalMessages failed: %v", err)
	}

	got, err := UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages failed: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("message count = %d, want 2", len(got))
	}

	// Verify all request parts.
	req, ok := got[0].(ModelRequest)
	if !ok {
		t.Fatal("message[0]: expected ModelRequest")
	}
	if len(req.Parts) != 4 {
		t.Fatalf("request parts = %d, want 4", len(req.Parts))
	}

	if _, ok := req.Parts[0].(SystemPromptPart); !ok {
		t.Errorf("part[0]: expected SystemPromptPart, got %T", req.Parts[0])
	}
	if _, ok := req.Parts[1].(UserPromptPart); !ok {
		t.Errorf("part[1]: expected UserPromptPart, got %T", req.Parts[1])
	}

	tr, ok := req.Parts[2].(ToolReturnPart)
	if !ok {
		t.Fatalf("part[2]: expected ToolReturnPart, got %T", req.Parts[2])
	}
	// ToolReturnPart.Content with map round-trips through JSON.
	// After deserialization, the map comes back as map[string]any.
	contentMap, ok := tr.Content.(map[string]any)
	if !ok {
		t.Fatalf("ToolReturnPart.Content: expected map[string]any, got %T", tr.Content)
	}
	results, ok := contentMap["results"].([]any)
	if !ok {
		t.Fatalf("ToolReturnPart.Content[results]: expected []any, got %T", contentMap["results"])
	}
	if len(results) != 2 {
		t.Errorf("ToolReturnPart.Content[results] length = %d, want 2", len(results))
	}

	rp, ok := req.Parts[3].(RetryPromptPart)
	if !ok {
		t.Fatalf("part[3]: expected RetryPromptPart, got %T", req.Parts[3])
	}
	if rp.Content != "please retry" {
		t.Errorf("RetryPromptPart.Content = %q, want %q", rp.Content, "please retry")
	}
	if rp.ToolName != "search" {
		t.Errorf("RetryPromptPart.ToolName = %q, want %q", rp.ToolName, "search")
	}
	if rp.ToolCallID != "tc-1" {
		t.Errorf("RetryPromptPart.ToolCallID = %q, want %q", rp.ToolCallID, "tc-1")
	}

	// Verify all response parts.
	resp, ok := got[1].(ModelResponse)
	if !ok {
		t.Fatal("message[1]: expected ModelResponse")
	}
	if len(resp.Parts) != 3 {
		t.Fatalf("response parts = %d, want 3", len(resp.Parts))
	}

	if tp, ok := resp.Parts[0].(TextPart); !ok {
		t.Errorf("part[0]: expected TextPart, got %T", resp.Parts[0])
	} else if tp.Content != "here are the results" {
		t.Errorf("TextPart.Content = %q, want %q", tp.Content, "here are the results")
	}

	if tc, ok := resp.Parts[1].(ToolCallPart); !ok {
		t.Errorf("part[1]: expected ToolCallPart, got %T", resp.Parts[1])
	} else {
		if tc.ToolName != "search" {
			t.Errorf("ToolCallPart.ToolName = %q, want %q", tc.ToolName, "search")
		}
		if tc.ArgsJSON != `{"q":"test"}` {
			t.Errorf("ToolCallPart.ArgsJSON = %q, want %q", tc.ArgsJSON, `{"q":"test"}`)
		}
	}

	if th, ok := resp.Parts[2].(ThinkingPart); !ok {
		t.Errorf("part[2]: expected ThinkingPart, got %T", resp.Parts[2])
	} else {
		if th.Content != "I should search first" {
			t.Errorf("ThinkingPart.Content = %q, want %q", th.Content, "I should search first")
		}
		if th.Signature != "sig-abc" {
			t.Errorf("ThinkingPart.Signature = %q, want %q", th.Signature, "sig-abc")
		}
	}

	// Verify usage round-trips.
	if resp.Usage.InputTokens != 100 {
		t.Errorf("Usage.InputTokens = %d, want 100", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 50 {
		t.Errorf("Usage.OutputTokens = %d, want 50", resp.Usage.OutputTokens)
	}
	if resp.Usage.CacheWriteTokens != 10 {
		t.Errorf("Usage.CacheWriteTokens = %d, want 10", resp.Usage.CacheWriteTokens)
	}
	if resp.Usage.CacheReadTokens != 5 {
		t.Errorf("Usage.CacheReadTokens = %d, want 5", resp.Usage.CacheReadTokens)
	}
}

func TestMarshalMessages_EmptySlice(t *testing.T) {
	data, err := MarshalMessages([]ModelMessage{})
	if err != nil {
		t.Fatalf("MarshalMessages failed: %v", err)
	}

	// Should serialize to an empty JSON array.
	if string(data) != "[]" {
		t.Errorf("empty slice serialized to %q, want %q", string(data), "[]")
	}

	got, err := UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("deserialized length = %d, want 0", len(got))
	}
}

func TestUnmarshalMessages_InvalidJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"garbage", "not json at all"},
		{"invalid envelope", `[{"kind":"request","data":"not raw json"}]`},
		{"unknown kind", `[{"kind":"unknown","data":{}}]`},
		{"bad request part", `[{"kind":"request","data":{"parts":[{"type":"system-prompt","data":"bad"}],"timestamp":"2025-01-01T00:00:00Z"}}]`},
		{"unknown part type", `[{"kind":"request","data":{"parts":[{"type":"unknown","data":{}}],"timestamp":"2025-01-01T00:00:00Z"}}]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := UnmarshalMessages([]byte(tt.input))
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestRunResultAllMessagesJSON(t *testing.T) {
	now := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

	result := &RunResult[string]{
		Output: "hello",
		Messages: []ModelMessage{
			ModelRequest{
				Parts: []ModelRequestPart{
					UserPromptPart{Content: "Say hello", Timestamp: now},
				},
				Timestamp: now,
			},
			ModelResponse{
				Parts: []ModelResponsePart{
					TextPart{Content: "hello"},
				},
				ModelName:    "test-model",
				FinishReason: FinishReasonStop,
				Timestamp:    now.Add(time.Second),
			},
		},
		RunID: "run-123",
	}

	data, err := result.AllMessagesJSON()
	if err != nil {
		t.Fatalf("AllMessagesJSON failed: %v", err)
	}

	// Verify it produces valid JSON.
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("AllMessagesJSON produced invalid JSON: %v", err)
	}
	if len(raw) != 2 {
		t.Errorf("AllMessagesJSON produced %d messages, want 2", len(raw))
	}

	// Verify round-trip.
	got, err := UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages on AllMessagesJSON output failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("round-trip message count = %d, want 2", len(got))
	}

	req, ok := got[0].(ModelRequest)
	if !ok {
		t.Fatal("message[0]: expected ModelRequest")
	}
	if len(req.Parts) != 1 {
		t.Fatalf("message[0].Parts count = %d, want 1", len(req.Parts))
	}
	up, ok := req.Parts[0].(UserPromptPart)
	if !ok {
		t.Fatal("message[0].Parts[0]: expected UserPromptPart")
	}
	if up.Content != "Say hello" {
		t.Errorf("UserPromptPart.Content = %q, want %q", up.Content, "Say hello")
	}
}

func TestRunResultAllMessagesJSON_WithAgent(t *testing.T) {
	model := NewTestModel(TextResponse("Hello, world!"))
	agent := NewAgent[string](model)

	result, err := agent.Run(context.Background(), "Say hello")
	if err != nil {
		t.Fatalf("agent.Run failed: %v", err)
	}

	data, err := result.AllMessagesJSON()
	if err != nil {
		t.Fatalf("AllMessagesJSON failed: %v", err)
	}

	got, err := UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages failed: %v", err)
	}

	if len(got) != len(result.Messages) {
		t.Fatalf("round-trip message count = %d, want %d", len(got), len(result.Messages))
	}

	// Verify we can re-feed the messages to the model.
	for i, msg := range got {
		origKind := result.Messages[i].messageKind()
		gotKind := msg.messageKind()
		if gotKind != origKind {
			t.Errorf("message[%d].kind = %q, want %q", i, gotKind, origKind)
		}
	}
}

func TestMarshalMessages_ToolReturnWithStringContent(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	messages := []ModelMessage{
		ModelRequest{
			Parts: []ModelRequestPart{
				ToolReturnPart{
					ToolName:   "echo",
					Content:    "plain string result",
					ToolCallID: "call-1",
					Timestamp:  now,
				},
			},
			Timestamp: now,
		},
	}

	data, err := MarshalMessages(messages)
	if err != nil {
		t.Fatalf("MarshalMessages failed: %v", err)
	}

	got, err := UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages failed: %v", err)
	}

	req := got[0].(ModelRequest)
	tr := req.Parts[0].(ToolReturnPart)
	if tr.Content != "plain string result" {
		t.Errorf("ToolReturnPart.Content = %v (%T), want %q", tr.Content, tr.Content, "plain string result")
	}
}

func TestMarshalMessages_ToolReturnWithMapContent(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	messages := []ModelMessage{
		ModelRequest{
			Parts: []ModelRequestPart{
				ToolReturnPart{
					ToolName:   "lookup",
					Content:    map[string]any{"key": "value", "count": float64(42)},
					ToolCallID: "call-2",
					Timestamp:  now,
				},
			},
			Timestamp: now,
		},
	}

	data, err := MarshalMessages(messages)
	if err != nil {
		t.Fatalf("MarshalMessages failed: %v", err)
	}

	got, err := UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages failed: %v", err)
	}

	req := got[0].(ModelRequest)
	tr := req.Parts[0].(ToolReturnPart)
	m, ok := tr.Content.(map[string]any)
	if !ok {
		t.Fatalf("ToolReturnPart.Content: expected map[string]any, got %T", tr.Content)
	}
	if m["key"] != "value" {
		t.Errorf("Content[key] = %v, want %q", m["key"], "value")
	}
	if m["count"] != float64(42) {
		t.Errorf("Content[count] = %v, want 42", m["count"])
	}
}

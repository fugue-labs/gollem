package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestSupportsToolSearch(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4-5-20250929", true},
		{"claude-sonnet-4-0", true},
		{"claude-opus-4-6", true},
		{"claude-opus-4-6-preview", true},
		{ClaudeFable5, false},
		{"claude-sonnet-5-foo", true},       // forward-compat
		{"claude-mythos-preview-abc", true}, // mythos catch-all
		{"claude-haiku-4-5-20251001", false},
		{"claude-3-5-sonnet-20241022", false},
		{"", false},
		{"gpt-4o", false},
	}
	for _, tc := range cases {
		got := supportsToolSearch(tc.model)
		if got != tc.want {
			t.Errorf("supportsToolSearch(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestBuildRequestEmitsDeferLoading(t *testing.T) {
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:             "search",
				Description:      "Search the web",
				ParametersSchema: core.Schema{"type": "object"},
				DeferLoading:     true,
			},
		},
	}

	req, err := buildRequest(nil, nil, params, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	// Should have 2 items: builtin tool_search + user tool.
	if len(req.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(req.Tools))
	}

	// First should be the built-in.
	builtin, ok := req.Tools[0].(apiBuiltinTool)
	if !ok {
		t.Fatalf("tools[0] should be apiBuiltinTool, got %T", req.Tools[0])
	}
	if builtin.Type != toolSearchToolRegexType {
		t.Errorf("builtin.Type = %q, want %q", builtin.Type, toolSearchToolRegexType)
	}
	if builtin.Name != toolSearchToolRegexName {
		t.Errorf("builtin.Name = %q, want %q", builtin.Name, toolSearchToolRegexName)
	}

	// Second should be the user tool with defer_loading: true.
	userTool := toolAt(t, req.Tools, 1)
	if !userTool.DeferLoading {
		t.Error("user tool should have DeferLoading=true")
	}
	if userTool.Name != "search" {
		t.Errorf("user tool name = %q, want %q", userTool.Name, "search")
	}

	// Verify the wire JSON contains "defer_loading": true.
	data, _ := json.Marshal(req)
	if s := string(data); !jsonContains(s, `"defer_loading":true`) {
		t.Errorf("wire JSON should contain defer_loading:true, got %s", s)
	}
}

func TestBuildRequestSilentDegradeOnHaiku(t *testing.T) {
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:             "search",
				ParametersSchema: core.Schema{"type": "object"},
				DeferLoading:     true,
			},
		},
	}

	req, err := buildRequest(nil, nil, params, ClaudeHaiku45, 4096, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	// Should have 1 item only (no built-in injected).
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool on unsupported model, got %d", len(req.Tools))
	}

	// Tool should NOT have defer_loading on the wire.
	userTool := toolAt(t, req.Tools, 0)
	if userTool.DeferLoading {
		t.Error("DeferLoading should not be emitted on unsupported model")
	}
}

func TestBuildRequestNoBuiltinWhenNoneDeferred(t *testing.T) {
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:             "search",
				ParametersSchema: core.Schema{"type": "object"},
				// DeferLoading is false (default).
			},
		},
	}

	req, err := buildRequest(nil, nil, params, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	// No built-in: just the 1 user tool.
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}

	// Must be a user tool, not a built-in.
	if _, ok := req.Tools[0].(apiBuiltinTool); ok {
		t.Error("should not inject built-in when no tool is deferred")
	}
}

func TestBuildRequestBuiltinSkippedWhenDisabled(t *testing.T) {
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:             "search",
				ParametersSchema: core.Schema{"type": "object"},
				DeferLoading:     true,
			},
		},
	}

	// disableToolSearch = true
	req, err := buildRequest(nil, nil, params, ClaudeSonnet46, 4096, false, false, true)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	// Only user tool (no built-in), but defer_loading still emitted.
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	userTool := toolAt(t, req.Tools, 0)
	if !userTool.DeferLoading {
		t.Error("defer_loading should still be emitted even when built-in is disabled")
	}
}

func TestBuildRequestCacheControlSkipsDeferredTools(t *testing.T) {
	// Mix of deferred and non-deferred tools. Anthropic rejects
	// cache_control + defer_loading on the same tool, so the marker
	// must go on the last NON-deferred user tool.
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{
				Name:             "always_loaded",
				ParametersSchema: core.Schema{"type": "object"},
				// NOT deferred — should get cache_control.
			},
			{
				Name:             "search",
				ParametersSchema: core.Schema{"type": "object"},
				DeferLoading:     true,
			},
			{
				Name:             "calculate",
				ParametersSchema: core.Schema{"type": "object"},
				DeferLoading:     true,
			},
		},
	}

	// enableCache = true
	req, err := buildRequest(nil, nil, params, ClaudeSonnet46, 4096, false, true, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	// 4 items: builtin + 3 user tools.
	if len(req.Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(req.Tools))
	}

	// Built-in at [0] must NOT have cache_control.
	if _, ok := req.Tools[0].(apiBuiltinTool); !ok {
		t.Fatalf("tools[0] should be apiBuiltinTool, got %T", req.Tools[0])
	}

	// [1] always_loaded (non-deferred) — should get cache_control.
	nonDeferred := toolAt(t, req.Tools, 1)
	if nonDeferred.CacheControl == nil {
		t.Error("non-deferred tool should have cache_control set")
	}

	// [2] search (deferred) — must NOT have cache_control.
	deferred1 := toolAt(t, req.Tools, 2)
	if deferred1.CacheControl != nil {
		t.Error("deferred tool must NOT have cache_control (Anthropic rejects this)")
	}

	// [3] calculate (deferred) — must NOT have cache_control.
	deferred2 := toolAt(t, req.Tools, 3)
	if deferred2.CacheControl != nil {
		t.Error("deferred tool must NOT have cache_control (Anthropic rejects this)")
	}
}

func TestBuildRequestCacheControlAllDeferred(t *testing.T) {
	// All tools are deferred — no user tool should get cache_control.
	// The system block marker still applies.
	params := &core.ModelRequestParameters{
		FunctionTools: []core.ToolDefinition{
			{Name: "a", ParametersSchema: core.Schema{"type": "object"}, DeferLoading: true},
			{Name: "b", ParametersSchema: core.Schema{"type": "object"}, DeferLoading: true},
		},
	}

	req, err := buildRequest(nil, nil, params, ClaudeSonnet46, 4096, false, true, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	// No tool should have cache_control.
	for i, tool := range req.Tools {
		if t2, ok := tool.(apiTool); ok && t2.CacheControl != nil {
			t.Errorf("tools[%d] (%q) should NOT have cache_control when all tools are deferred", i, t2.Name)
		}
	}
}

func TestParseResponseToolReferencePreserved(t *testing.T) {
	// Simulate a response with a tool_reference block (from tool search).
	toolRef := json.RawMessage(`{"type":"tool_reference","tool_name":"get_weather"}`)
	resp := &apiResponse{
		Content: []json.RawMessage{
			mustMarshal(apiContentBlock{Type: "text", Text: "Found it."}),
			toolRef,
		},
		StopReason: "end_turn",
	}

	result := parseResponse(resp, ClaudeSonnet46)
	if len(result.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(result.Parts))
	}

	// First part: text.
	if _, ok := result.Parts[0].(core.TextPart); !ok {
		t.Errorf("parts[0] should be TextPart, got %T", result.Parts[0])
	}

	// Second part: ProviderMetadataPart preserving tool_reference.
	meta, ok := result.Parts[1].(core.ProviderMetadataPart)
	if !ok {
		t.Fatalf("parts[1] should be ProviderMetadataPart, got %T", result.Parts[1])
	}
	if meta.Provider != "anthropic" {
		t.Errorf("Provider = %q, want 'anthropic'", meta.Provider)
	}
	if meta.Kind != "tool_reference" {
		t.Errorf("Kind = %q, want 'tool_reference'", meta.Kind)
	}
	// Payload should contain the original tool_name.
	if !jsonContains(string(meta.Payload), `"tool_name":"get_weather"`) {
		t.Errorf("Payload should contain tool_name, got %s", meta.Payload)
	}
}

func TestParseResponseServerToolUsePreserved(t *testing.T) {
	// server_tool_use must be preserved (not dropped) per API contract.
	serverBlock := json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_123","name":"tool_search_tool_regex","input":{"query":"weather"}}`)
	resp := &apiResponse{
		Content: []json.RawMessage{serverBlock},
	}

	result := parseResponse(resp, ClaudeSonnet46)
	if len(result.Parts) != 1 {
		t.Fatalf("expected 1 part (server_tool_use preserved), got %d", len(result.Parts))
	}
	meta, ok := result.Parts[0].(core.ProviderMetadataPart)
	if !ok {
		t.Fatalf("expected ProviderMetadataPart, got %T", result.Parts[0])
	}
	if meta.Kind != "server_tool_use" {
		t.Errorf("Kind = %q, want 'server_tool_use'", meta.Kind)
	}
}

func TestParseResponseUnknownBlockPreserved(t *testing.T) {
	// Any unknown block type should be preserved for future-proofing.
	unknownBlock := json.RawMessage(`{"type":"some_future_type","data":"hello"}`)
	resp := &apiResponse{
		Content: []json.RawMessage{unknownBlock},
	}

	result := parseResponse(resp, ClaudeSonnet46)
	if len(result.Parts) != 1 {
		t.Fatalf("expected 1 preserved part, got %d", len(result.Parts))
	}
	meta := result.Parts[0].(core.ProviderMetadataPart)
	if meta.Kind != "some_future_type" {
		t.Errorf("Kind = %q, want 'some_future_type'", meta.Kind)
	}
}

func TestBuildRequestRoundTripsProviderMetadata(t *testing.T) {
	// A ProviderMetadataPart from a previous assistant response should be
	// emitted back in the assistant's content via rawOverride.
	toolRefJSON := json.RawMessage(`{"type":"tool_reference","tool_name":"get_weather"}`)

	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Found it."},
				core.ProviderMetadataPart{
					Provider: "anthropic",
					Kind:     "tool_reference",
					Payload:  toolRefJSON,
				},
			},
		},
	}

	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	assistant := req.Messages[0]
	if assistant.Role != "assistant" {
		t.Fatalf("role = %q, want 'assistant'", assistant.Role)
	}
	if len(assistant.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(assistant.Content))
	}

	// First block: text.
	if assistant.Content[0].Type != "text" {
		t.Errorf("content[0].Type = %q, want 'text'", assistant.Content[0].Type)
	}

	// Second block: rawOverride should emit the tool_reference JSON.
	data, _ := json.Marshal(assistant.Content[1])
	if !jsonContains(string(data), `"tool_reference"`) {
		t.Errorf("content[1] should contain tool_reference JSON, got %s", data)
	}
	if !jsonContains(string(data), `"get_weather"`) {
		t.Errorf("content[1] should contain get_weather, got %s", data)
	}
}

func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// jsonContains checks for a substring in a JSON string.
func jsonContains(json, substr string) bool {
	return len(json) >= len(substr) && containsSubstring(json, substr)
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

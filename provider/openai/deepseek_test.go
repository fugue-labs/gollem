package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func boolPtr(v bool) *bool        { return &v }
func strPtr(v string) *string     { return &v }
func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

func deepSeekServer(t *testing.T, handler func(t *testing.T, body map[string]any, w http.ResponseWriter)) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-deepseek-test" {
			t.Errorf("authorization = %q", got)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		bodies = append(bodies, body)
		handler(t, body, w)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func newTestDeepSeek(t *testing.T, srv *httptest.Server) *Provider {
	t.Helper()
	t.Setenv(DeepSeekAPIKeyEnv, "sk-deepseek-test")
	t.Setenv(DeepSeekBaseURLEnv, srv.URL)
	p := NewDeepSeek(WithHTTPClient(srv.Client()))
	if !p.IsDeepSeek() || p.ModelName() != DeepSeekV4Pro || p.shouldUseResponsesAPI() {
		t.Fatalf("NewDeepSeek defaults = deepseek:%v model:%q responses:%v", p.IsDeepSeek(), p.ModelName(), p.shouldUseResponsesAPI())
	}
	return p
}

func TestNewDeepSeekConfiguration(t *testing.T) {
	t.Setenv(DeepSeekAPIKeyEnv, "sk-env")
	t.Setenv(DeepSeekBaseURLEnv, "")
	p := NewDeepSeek()
	if p.baseURL != deepSeekDefaultBaseURL || p.apiKey != "sk-env" {
		t.Fatalf("defaults = base %q key %q", p.baseURL, p.apiKey)
	}
	if p.promptCacheKey != "" || p.promptCacheRetention != "" {
		t.Fatalf("OpenAI-only cache settings leaked: %q %q", p.promptCacheKey, p.promptCacheRetention)
	}
	t.Setenv(DeepSeekAPIKeyEnv, "")
	t.Setenv("OPENAI_API_KEY", "sk-openai-secret")
	if leaked := NewDeepSeek(); leaked.apiKey != "" {
		t.Fatalf("OPENAI_API_KEY must never be sent to DeepSeek, got %q", leaked.apiKey)
	}
	if explicit := NewDeepSeek(WithAPIKey("sk-explicit")); explicit.apiKey != "sk-explicit" {
		t.Fatalf("explicit key = %q", explicit.apiKey)
	}
	t.Setenv("OPENAI_API_KEY", "")
	explicit := NewDeepSeek(WithAPIKey("sk-explicit"), WithModel(DeepSeekV4Flash), WithBaseURL("https://proxy.test/v1"))
	if explicit.apiKey != "sk-explicit" || explicit.ModelName() != DeepSeekV4Flash || explicit.baseURL != "https://proxy.test" {
		t.Fatalf("explicit options = key %q model %q base %q", explicit.apiKey, explicit.ModelName(), explicit.baseURL)
	}
	profile := explicit.Profile()
	if !profile.SupportsToolCalls || !profile.SupportsStreaming || !profile.SupportsVision || profile.SupportsStructuredOutput {
		t.Fatalf("flash profile = %#v", profile)
	}
	if pro := NewDeepSeek(WithAPIKey("k")).Profile(); pro.SupportsVision {
		t.Fatalf("v4 pro should not advertise vision: %#v", pro)
	}
	session, ok := explicit.NewSession().(*Provider)
	if !ok || !session.IsDeepSeek() {
		t.Fatal("NewSession dropped the DeepSeek mode")
	}
}

func TestDeepSeekRequestShapeAndReasoningReplay(t *testing.T) {
	srv, bodies := deepSeekServer(t, func(_ *testing.T, _ map[string]any, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"deepseek-v4-pro","choices":[{"index":0,"message":{"role":"assistant","content":"done","reasoning_content":"thought"},"finish_reason":"stop"}],"usage":{"prompt_tokens":283,"completion_tokens":69,"prompt_cache_hit_tokens":256,"prompt_cache_miss_tokens":27,"prompt_tokens_details":{"cached_tokens":256},"completion_tokens_details":{"reasoning_tokens":24}}}`))
	})
	p := newTestDeepSeek(t, srv)

	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "weather?"}}},
		core.ModelResponse{Parts: []core.ModelResponsePart{
			core.ThinkingPart{Content: "I should check the weather."},
			core.ToolCallPart{ToolName: "get_weather", ArgsJSON: `{"city":"Paris"}`, ToolCallID: "call_0"},
		}},
		core.ModelRequest{Parts: []core.ModelRequestPart{core.ToolReturnPart{ToolName: "get_weather", ToolCallID: "call_0", Content: ""}}},
		core.ModelResponse{Parts: []core.ModelResponsePart{
			core.ThinkingPart{Content: "replayed on text-only turns too"},
			core.TextPart{Content: "Sunny."},
		}},
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "thanks"}}},
	}
	settings := &core.ModelSettings{
		MaxTokens:       intPtr(4096),
		Temperature:     floatPtr(0.7),
		ReasoningEffort: strPtr("max"),
	}
	resp, err := p.Request(context.Background(), messages, settings, nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if len(resp.Parts) != 2 {
		t.Fatalf("parts = %#v", resp.Parts)
	}
	if thinking, ok := resp.Parts[0].(core.ThinkingPart); !ok || thinking.Content != "thought" {
		t.Fatalf("first part = %#v, want thinking", resp.Parts[0])
	}
	if text, ok := resp.Parts[1].(core.TextPart); !ok || text.Content != "done" {
		t.Fatalf("second part = %#v, want text", resp.Parts[1])
	}
	if resp.Usage.InputTokens != 283 || resp.Usage.OutputTokens != 69 || resp.Usage.CacheReadTokens != 256 || resp.Usage.Details["reasoning_tokens"] != 24 {
		t.Fatalf("usage = %#v", resp.Usage)
	}

	if len(*bodies) != 1 {
		t.Fatalf("requests = %d", len(*bodies))
	}
	body := (*bodies)[0]
	if body["max_tokens"] != float64(4096) {
		t.Fatalf("max_tokens = %v", body["max_tokens"])
	}
	if _, ok := body["max_completion_tokens"]; ok {
		t.Fatal("max_completion_tokens must not be sent to DeepSeek")
	}
	for _, key := range []string{"prompt_cache_key", "prompt_cache_retention", "service_tier"} {
		if _, ok := body[key]; ok {
			t.Fatalf("%s must not be sent to DeepSeek", key)
		}
	}
	if thinking, _ := body["thinking"].(map[string]any); thinking["type"] != "enabled" || thinking["reasoning_effort"] != "max" {
		t.Fatalf("thinking = %v", body["thinking"])
	}
	if body["reasoning_effort"] != "max" || body["temperature"] != 0.7 {
		t.Fatalf("effort/temperature = %v / %v", body["reasoning_effort"], body["temperature"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("messages = %v", msgs)
	}
	toolTurn := msgs[1].(map[string]any)
	if toolTurn["role"] != "assistant" || toolTurn["reasoning_content"] != "I should check the weather." {
		t.Fatalf("tool-call assistant turn = %v", toolTurn)
	}
	if _, hasContent := toolTurn["content"]; hasContent {
		t.Fatalf("tool-call assistant turn should omit empty content: %v", toolTurn)
	}
	toolResult := msgs[2].(map[string]any)
	if toolResult["role"] != "tool" || toolResult["content"] != deepSeekEmptyToolOutput || toolResult["tool_call_id"] != "call_0" {
		t.Fatalf("empty tool result = %v", toolResult)
	}
	textTurn := msgs[3].(map[string]any)
	if textTurn["reasoning_content"] != "replayed on text-only turns too" || textTurn["content"] != "Sunny." {
		t.Fatalf("text-only assistant turn = %v", textTurn)
	}
}

func TestDeepSeekStreamKeepsInterleavedThinkingInOrder(t *testing.T) {
	chunks := []string{
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"reasoning_content":"plan"},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"content":"Step one."},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"reasoning_content":"revise"},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"content":"Step two."},"finish_reason":"stop"}]}`,
	}
	srv, _ := deepSeekServer(t, func(_ *testing.T, _ map[string]any, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	p := newTestDeepSeek(t, srv)
	stream, err := p.RequestStream(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hi"}}},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for {
		if _, err := stream.Next(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	var kinds []string
	for _, part := range stream.Response().Parts {
		switch v := part.(type) {
		case core.ThinkingPart:
			kinds = append(kinds, "thinking:"+v.Content)
		case core.TextPart:
			kinds = append(kinds, "text:"+v.Content)
		}
	}
	want := "thinking:plan|text:Step one.|thinking:revise|text:Step two."
	if strings.Join(kinds, "|") != want {
		t.Fatalf("parts = %v, want %s", kinds, want)
	}
}

func TestDeepSeekMaxTokensDefaultsToAPI(t *testing.T) {
	srv, bodies := deepSeekServer(t, func(_ *testing.T, _ map[string]any, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"deepseek-v4-pro","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	})
	p := newTestDeepSeek(t, srv)
	prompt := []core.ModelMessage{core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hi"}}}}
	if _, err := p.Request(context.Background(), prompt, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := (*bodies)[0]["max_tokens"]; ok {
		t.Fatalf("max_tokens should be omitted without an explicit setting: %v", (*bodies)[0])
	}
	if _, err := p.Request(context.Background(), prompt, &core.ModelSettings{MaxTokens: intPtr(1_000_000)}, nil); err != nil {
		t.Fatal(err)
	}
	if got := (*bodies)[1]["max_tokens"]; got != float64(DeepSeekMaxOutputTokens) {
		t.Fatalf("max_tokens = %v, want clamped to %d", got, DeepSeekMaxOutputTokens)
	}

	t.Setenv(DeepSeekAPIKeyEnv, "sk-deepseek-test")
	configured := NewDeepSeek(WithHTTPClient(srv.Client()), WithMaxTokens(2048))
	if _, err := configured.Request(context.Background(), prompt, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := (*bodies)[2]["max_tokens"]; got != float64(2048) {
		t.Fatalf("provider-level max_tokens = %v, want 2048", got)
	}
	if _, err := configured.Request(context.Background(), prompt, &core.ModelSettings{MaxTokens: intPtr(512)}, nil); err != nil {
		t.Fatal(err)
	}
	if got := (*bodies)[3]["max_tokens"]; got != float64(512) {
		t.Fatalf("per-request max_tokens = %v, want 512", got)
	}
}

func TestWithoutOpenAIEnvironmentIgnoresOpenAIOnlySettings(t *testing.T) {
	t.Setenv("OPENAI_SERVICE_TIER", "priority")
	t.Setenv("OPENAI_TRANSPORT", "websocket")
	t.Setenv("OPENAI_PROMPT_CACHE_KEY", "cache")
	t.Setenv("OPENAI_REASONING_SUMMARY", "detailed")
	p := New(WithoutOpenAIEnvironment(), WithAPIKey("k"), WithBaseURL("https://gateway.test/v1"))
	if p.serviceTier != "" || p.transport != transportHTTP || p.promptCacheKey != "" || p.reasoningSummary != "" {
		t.Fatalf("OpenAI-only settings leaked to a third-party endpoint: tier=%q transport=%q cache=%q summary=%q", p.serviceTier, p.transport, p.promptCacheKey, p.reasoningSummary)
	}
	plain := New(WithAPIKey("k"))
	if plain.serviceTier != "priority" {
		t.Fatalf("OpenAI itself should still honour OPENAI_SERVICE_TIER, got %q", plain.serviceTier)
	}
}

func TestDeepSeekThinkingToggle(t *testing.T) {
	srv, bodies := deepSeekServer(t, func(_ *testing.T, _ map[string]any, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"deepseek-v4-flash","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	})
	p := newTestDeepSeek(t, srv)
	p.model = DeepSeekV4Flash
	prompt := []core.ModelMessage{core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hi"}}}}

	cases := []struct {
		name         string
		settings     *core.ModelSettings
		wantThinking any
		wantEffort   any
	}{
		{"unset", nil, nil, nil},
		{"adaptive on", &core.ModelSettings{AdaptiveThinking: boolPtr(true)}, "enabled", nil},
		{"adaptive off", &core.ModelSettings{AdaptiveThinking: boolPtr(false)}, "disabled", nil},
		{"effort only", &core.ModelSettings{ReasoningEffort: strPtr("high")}, "enabled", "high"},
		{"budget", &core.ModelSettings{ThinkingBudget: intPtr(8000)}, "enabled", nil},
		{"off with budget", &core.ModelSettings{ThinkingBudget: intPtr(8000), AdaptiveThinking: boolPtr(false)}, "disabled", nil},
		{"medium rounds up", &core.ModelSettings{ReasoningEffort: strPtr("medium")}, "enabled", "high"},
		{"none disables", &core.ModelSettings{ReasoningEffort: strPtr("none")}, "disabled", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			*bodies = nil
			if _, err := p.Request(context.Background(), prompt, tc.settings, nil); err != nil {
				t.Fatalf("Request: %v", err)
			}
			body := (*bodies)[0]
			var gotThinking any
			if thinking, ok := body["thinking"].(map[string]any); ok {
				gotThinking = thinking["type"]
			}
			if gotThinking != tc.wantThinking || body["reasoning_effort"] != tc.wantEffort {
				t.Fatalf("thinking = %v effort = %v, want %v / %v", gotThinking, body["reasoning_effort"], tc.wantThinking, tc.wantEffort)
			}
		})
	}

	*bodies = nil
	_, err := p.Request(context.Background(), prompt, &core.ModelSettings{AdaptiveThinking: boolPtr(false), ReasoningEffort: strPtr("high")}, nil)
	if err == nil || !strings.Contains(err.Error(), "conflicts with ReasoningEffort") {
		t.Fatalf("conflict error = %v", err)
	}
	if len(*bodies) != 0 {
		t.Fatal("conflicting settings must fail before any request")
	}
	for _, effort := range []string{"minimal", "xhigh", "bogus"} {
		_, err := p.Request(context.Background(), prompt, &core.ModelSettings{ReasoningEffort: strPtr(effort)}, nil)
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Fatalf("effort %q error = %v", effort, err)
		}
	}
	if len(*bodies) != 0 {
		t.Fatal("unsupported efforts must fail before any request")
	}
}

func TestDeepSeekStreamSurfacesReasoningBeforeText(t *testing.T) {
	chunks := []string{
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":""},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"reasoning_content":"Let me "},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"reasoning_content":"think."},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]},"finish_reason":null}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"id":"s","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":2,"prompt_tokens_details":{"cached_tokens":8},"completion_tokens_details":{"reasoning_tokens":3}}}`,
	}
	srv, _ := deepSeekServer(t, func(_ *testing.T, body map[string]any, w http.ResponseWriter) {
		if body["stream"] != true {
			t.Errorf("stream flag = %v", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
		}
		_, _ = io.WriteString(w, ": keep-alive\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	p := newTestDeepSeek(t, srv)

	stream, err := p.RequestStream(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hi"}}},
	}, &core.ModelSettings{AdaptiveThinking: boolPtr(true)}, nil)
	if err != nil {
		t.Fatalf("RequestStream: %v", err)
	}
	defer stream.Close()

	var kinds []string
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		switch e := event.(type) {
		case core.PartStartEvent:
			switch part := e.Part.(type) {
			case core.ThinkingPart:
				kinds = append(kinds, "thinking-start:"+part.Content)
			case core.TextPart:
				kinds = append(kinds, "text-start:"+part.Content)
			case core.ToolCallPart:
				kinds = append(kinds, "tool-start:"+part.ToolName)
			}
		case core.PartDeltaEvent:
			switch delta := e.Delta.(type) {
			case core.ThinkingPartDelta:
				kinds = append(kinds, "thinking-delta:"+delta.ContentDelta)
			case core.TextPartDelta:
				kinds = append(kinds, "text-delta:"+delta.ContentDelta)
			case core.ToolCallPartDelta:
				kinds = append(kinds, "tool-delta:"+delta.ArgsJSONDelta)
			}
		}
	}
	want := []string{"thinking-start:Let me ", "thinking-delta:think.", "text-start:Hel", "text-delta:lo", "tool-start:lookup", "tool-delta:1}"}
	if strings.Join(kinds, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", kinds, want)
	}

	resp := stream.Response()
	if len(resp.Parts) != 3 {
		t.Fatalf("response parts = %#v", resp.Parts)
	}
	if thinking, ok := resp.Parts[0].(core.ThinkingPart); !ok || thinking.Content != "Let me think." {
		t.Fatalf("thinking part = %#v", resp.Parts[0])
	}
	if text, ok := resp.Parts[1].(core.TextPart); !ok || text.Content != "Hello" {
		t.Fatalf("text part = %#v", resp.Parts[1])
	}
	if call, ok := resp.Parts[2].(core.ToolCallPart); !ok || call.ArgsJSON != `{"q":1}` || call.ToolCallID != "call_1" {
		t.Fatalf("tool part = %#v", resp.Parts[2])
	}
	if resp.FinishReason != core.FinishReasonToolCall {
		t.Fatalf("finish reason = %v", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.CacheReadTokens != 8 || resp.Usage.Details["reasoning_tokens"] != 3 {
		t.Fatalf("usage = %#v", resp.Usage)
	}
}

func TestDeepSeekStreamSettingsErrorStillReportsTrace(t *testing.T) {
	t.Setenv(DeepSeekAPIKeyEnv, "sk-deepseek-test")
	var traces int
	p := NewDeepSeek(WithRequestObserver(func(RequestTrace) { traces++ }))
	_, err := p.RequestStream(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hi"}}},
	}, &core.ModelSettings{ReasoningEffort: strPtr("xhigh")}, nil)
	if err == nil {
		t.Fatal("unsupported effort accepted")
	}
	if traces != 1 {
		t.Fatalf("request observer fired %d times, want 1", traces)
	}
}

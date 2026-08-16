package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/provider/anthropic"
	"github.com/fugue-labs/gollem/provider/conformance"
	"github.com/fugue-labs/gollem/provider/openai"
)

func TestDeterministicProviderDriverConformance(t *testing.T) {
	openAIPromptCache := &promptCacheFixture{}
	openAIToolSearch := &toolSearchFixture{}
	openAINamespaceTools := &namespaceToolsFixture{}
	openAICancellationReady := make(chan struct{})
	openAIRetry := newRetryFixture()
	openAITimeout := newTimeoutFixture()
	openAIServer := httptest.NewServer(openAIConformanceFixture(t, openAIPromptCache, openAIToolSearch, openAINamespaceTools, openAICancellationReady, openAIRetry, openAITimeout))
	defer openAIServer.Close()
	anthropicPromptCache := &promptCacheFixture{}
	anthropicStopSequences := &stopSequenceFixture{}
	anthropicToolSearch := &toolSearchFixture{}
	anthropicCancellationReady := make(chan struct{})
	anthropicRetry := newRetryFixture()
	anthropicTimeout := newTimeoutFixture()
	anthropicServer := httptest.NewServer(anthropicConformanceFixture(t, anthropicPromptCache, anthropicToolSearch, anthropicStopSequences, anthropicCancellationReady, anthropicRetry, anthropicTimeout))
	defer anthropicServer.Close()

	cases := []struct {
		name  string
		model func() (conformance.Driver, error)
	}{
		{
			name: "native OpenAI",
			model: func() (conformance.Driver, error) {
				return conformance.Driver{
					Name:                     "native OpenAI",
					Model:                    openai.New(openai.WithAPIKey("test-openai-key"), openai.WithBaseURL(openAIServer.URL), openai.WithModel("gpt-4o"), openai.WithPromptCacheKey("conformance-cache"), openai.WithPromptCacheRetention("24h")),
					ReasoningModel:           openai.New(openai.WithAPIKey("test-openai-key"), openai.WithBaseURL(openAIServer.URL), openai.WithModel("gpt-5")),
					ToolSearchModel:          openai.New(openai.WithAPIKey("test-openai-key"), openai.WithBaseURL(openAIServer.URL), openai.WithModel("gpt-5.4")),
					NamespaceToolsModel:      openai.New(openai.WithAPIKey("test-openai-key"), openai.WithBaseURL(openAIServer.URL), openai.WithModel("gpt-5")),
					Claims:                   conformance.Claims{ToolCalls: true, ToolSearch: true, NamespaceTools: true, StructuredOutput: true, Vision: true, CacheReadUsage: true, PromptCacheActivation: true, Streaming: true, Usage: true, Cancellation: true, PartialStream: true, MalformedStream: true, DisconnectStream: true, Retryability: true, RequestTimeout: true, StreamTimeout: true, ReasoningVisibility: true},
					PromptCacheActivation:    openAIPromptCache.verify,
					ToolSearchActivation:     openAIToolSearch.verifyOpenAI,
					NamespaceToolsActivation: openAINamespaceTools.verify,
					CancellationReady:        openAICancellationReady,
					RequestTimeoutReady:      openAITimeout.readyFor("gpt-4o"),
					Expectations: conformance.Expectations{
						ResponseText:          "openai response",
						ToolName:              "conformance_echo",
						ToolCallID:            "call_openai",
						ToolArgumentsJSON:     `{"value":"ok"}`,
						ToolSearchText:        "openai tool search",
						NamespaceToolName:     "conformance_namespaced",
						NamespaceToolCallID:   "call_namespace_openai",
						NamespaceToolArgsJSON: `{"value":"namespaced"}`,
						Namespace:             "conformance",
						StructuredOutputValue: "openai structured",
						VisionText:            "openai vision",
						CacheReadTokens:       2,
						StreamText:            "openai stream",
						PartialText:           "openai partial",
						DisconnectText:        "openai disconnect",
						RetryText:             "openai retry",
						StreamTimeoutText:     "openai deadline",
						ReasoningText:         "openai reasoning",
					},
				}, nil
			},
		},
		{
			name: "OpenAI-compatible local",
			model: func() (conformance.Driver, error) {
				model, err := openai.NewLocalEndpoint(openai.LocalEndpointConfig{
					BaseURL: openAIServer.URL,
					Model:   "gpt-5.2-codex",
					Token:   "test-local-key",
				})
				if err != nil {
					return conformance.Driver{}, err
				}
				return conformance.Driver{
					Name:                "OpenAI-compatible local",
					Model:               model,
					Claims:              conformance.Claims{ToolCalls: true, Streaming: true, Usage: true, Cancellation: true, PartialStream: true, MalformedStream: true, DisconnectStream: true, Retryability: true, RequestTimeout: true, StreamTimeout: true},
					CancellationReady:   openAICancellationReady,
					RequestTimeoutReady: openAITimeout.readyFor("gpt-5.2-codex"),
					Expectations: conformance.Expectations{
						ResponseText:      "openai response",
						ToolName:          "conformance_echo",
						ToolCallID:        "call_openai",
						ToolArgumentsJSON: `{"value":"ok"}`,
						StreamText:        "openai stream",
						PartialText:       "openai partial",
						DisconnectText:    "openai disconnect",
						RetryText:         "openai retry",
						StreamTimeoutText: "openai deadline",
					},
				}, nil
			},
		},
		{
			name: "native Anthropic",
			model: func() (conformance.Driver, error) {
				model := anthropic.New(anthropic.WithAPIKey("test-anthropic-key"), anthropic.WithBaseURL(anthropicServer.URL), anthropic.WithModel(anthropic.ClaudeSonnet46))
				return conformance.Driver{
					Name:                    "native Anthropic",
					Model:                   model,
					ReasoningModel:          model,
					ToolSearchModel:         model,
					Claims:                  conformance.Claims{ToolCalls: true, ToolSearch: true, StructuredOutput: true, Vision: true, CacheReadUsage: true, PromptCacheActivation: true, StopSequences: true, Streaming: true, Usage: true, Cancellation: true, PartialStream: true, MalformedStream: true, DisconnectStream: true, Retryability: true, RequestTimeout: true, StreamTimeout: true, ReasoningVisibility: true},
					PromptCacheActivation:   anthropicPromptCache.verify,
					StopSequencesActivation: anthropicStopSequences.verify,
					ToolSearchActivation:    anthropicToolSearch.verifyAnthropic,
					CancellationReady:       anthropicCancellationReady,
					RequestTimeoutReady:     anthropicTimeout.readyFor(anthropic.ClaudeSonnet46),
					Expectations: conformance.Expectations{
						ResponseText:          "anthropic response",
						ToolName:              "conformance_echo",
						ToolCallID:            "call_anthropic",
						ToolArgumentsJSON:     `{"value":"ok"}`,
						ToolSearchText:        "anthropic tool search",
						StructuredOutputValue: "anthropic structured",
						VisionText:            "anthropic vision",
						CacheReadTokens:       2,
						StreamText:            "anthropic stream",
						PartialText:           "anthropic partial",
						DisconnectText:        "anthropic disconnect",
						RetryText:             "anthropic retry",
						StreamTimeoutText:     "anthropic deadline",
						ReasoningText:         "anthropic reasoning",
						StopSequenceText:      "anthropic stop sequences",
					},
				}, nil
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			driver, err := tt.model()
			if err != nil {
				t.Fatalf("new driver: %v", err)
			}
			if err := conformance.Verify(context.Background(), driver); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCatalogAnthropicProfileConformance(t *testing.T) {
	// Keep these profiles in lockstep with appserver/catalog. A representative
	// Sonnet fixture is not evidence that every catalog-listed Claude profile
	// accepts the same request shape or deferred tool configuration.
	for _, tc := range []struct {
		model      string
		toolSearch bool
		reasoning  bool
	}{
		{anthropic.ClaudeSonnet46, true, true},
		{anthropic.ClaudeOpus46, true, true},
		{anthropic.ClaudeOpus47, true, true},
		{anthropic.ClaudeOpus48, true, true},
		{anthropic.ClaudeFable5, false, true},
		{anthropic.ClaudeHaiku45, false, false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			promptCache := &promptCacheFixture{}
			toolSearch := &toolSearchFixture{}
			stopSequences := &stopSequenceFixture{}
			server := httptest.NewServer(anthropicConformanceFixture(
				t,
				promptCache,
				toolSearch,
				stopSequences,
				nil,
				nil,
				nil,
				tc.model,
			))
			defer server.Close()

			model := anthropic.New(
				anthropic.WithAPIKey("catalog-conformance-key"),
				anthropic.WithBaseURL(server.URL),
				anthropic.WithModel(tc.model),
			)
			claims := conformance.Claims{
				ToolCalls:             true,
				ToolSearch:            tc.toolSearch,
				StructuredOutput:      true,
				Vision:                true,
				CacheReadUsage:        true,
				PromptCacheActivation: true,
				StopSequences:         true,
				Streaming:             true,
				Usage:                 true,
				ReasoningVisibility:   tc.reasoning,
			}
			driver := conformance.Driver{
				Name:                    "native Anthropic " + tc.model,
				Model:                   model,
				Claims:                  claims,
				PromptCacheActivation:   promptCache.verify,
				StopSequencesActivation: stopSequences.verify,
				Expectations: conformance.Expectations{
					ResponseText:          "anthropic response",
					ToolName:              "conformance_echo",
					ToolCallID:            "call_anthropic",
					ToolArgumentsJSON:     `{"value":"ok"}`,
					StructuredOutputValue: "anthropic structured",
					VisionText:            "anthropic vision",
					CacheReadTokens:       2,
					StreamText:            "anthropic stream",
					ReasoningText:         "anthropic reasoning",
					StopSequenceText:      "anthropic stop sequences",
				},
			}
			if tc.toolSearch {
				driver.ToolSearchModel = model
				driver.ToolSearchActivation = toolSearch.verifyAnthropic
				driver.Expectations.ToolSearchText = "anthropic tool search"
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

type promptCacheFixture struct {
	mu      sync.Mutex
	request bool
	stream  bool
}

type stopSequenceFixture struct {
	mu       sync.Mutex
	observed int
}

func (f *stopSequenceFixture) record() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observed++
}

func (f *stopSequenceFixture) verify() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.observed != 1 {
		return fmt.Errorf("observed %d stop-sequence requests, want 1", f.observed)
	}
	return nil
}

func (f *promptCacheFixture) record(stream bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if stream {
		f.stream = true
		return
	}
	f.request = true
}

func (f *promptCacheFixture) verify() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.request || !f.stream {
		return fmt.Errorf("observed request=%t stream=%t, want both", f.request, f.stream)
	}
	return nil
}

type toolSearchFixture struct {
	mu        sync.Mutex
	openAI    bool
	anthropic bool
}

func (f *toolSearchFixture) recordOpenAI() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openAI = true
}

func (f *toolSearchFixture) recordAnthropic() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.anthropic = true
}

func (f *toolSearchFixture) verifyOpenAI() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.openAI {
		return fmt.Errorf("OpenAI fixture did not observe deferred-tool activation")
	}
	return nil
}

func (f *toolSearchFixture) verifyAnthropic() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.anthropic {
		return fmt.Errorf("Anthropic fixture did not observe deferred-tool activation")
	}
	return nil
}

type namespaceToolsFixture struct {
	mu       sync.Mutex
	observed bool
}

func (f *namespaceToolsFixture) record() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observed = true
}

func (f *namespaceToolsFixture) verify() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.observed {
		return fmt.Errorf("OpenAI fixture did not observe namespace grouping")
	}
	return nil
}

func TestVerifyRejectsUnprovenClaims(t *testing.T) {
	err := conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing structured-output fixture",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{StructuredOutput: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a structured-output claim without a typed expectation")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing vision fixture",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{Vision: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a vision claim without an expected response")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing tool-search model",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{ToolSearch: true},
	})
	if err == nil || !strings.Contains(err.Error(), "tool-search model") {
		t.Fatalf("Verify missing tool-search model error = %v", err)
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:            "missing tool-search fixture",
		Model:           openai.New(openai.WithAPIKey("test-key")),
		ToolSearchModel: openai.New(openai.WithAPIKey("test-key")),
		Claims:          conformance.Claims{ToolSearch: true},
		Expectations:    conformance.Expectations{ToolSearchText: "expected"},
	})
	if err == nil || !strings.Contains(err.Error(), "deferred-tool activation") {
		t.Fatalf("Verify missing tool-search fixture error = %v", err)
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing namespace-tools model",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{NamespaceTools: true},
	})
	if err == nil || !strings.Contains(err.Error(), "namespace-tools model") {
		t.Fatalf("Verify missing namespace-tools model error = %v", err)
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:                "missing namespace-tools fixture",
		Model:               openai.New(openai.WithAPIKey("test-key")),
		NamespaceToolsModel: openai.New(openai.WithAPIKey("test-key")),
		Claims:              conformance.Claims{NamespaceTools: true},
		Expectations: conformance.Expectations{
			NamespaceToolName:     "conformance_namespaced",
			NamespaceToolCallID:   "call_namespace",
			NamespaceToolArgsJSON: `{"value":"namespaced"}`,
			Namespace:             "conformance",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "namespace grouping") {
		t.Fatalf("Verify missing namespace-tools fixture error = %v", err)
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing cache-read fixture",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{CacheReadUsage: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a cache-read claim without expected tokens")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing tool fixture",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{ToolCalls: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a tool claim without an expected tool")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing tool call ID",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{ToolCalls: true},
		Expectations: conformance.Expectations{
			ToolName:          "conformance_echo",
			ToolArgumentsJSON: `{"value":"ok"}`,
		},
	})
	if err == nil {
		t.Fatal("Verify accepted a tool claim without an expected tool call ID")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing tool arguments",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{ToolCalls: true},
		Expectations: conformance.Expectations{
			ToolName:   "conformance_echo",
			ToolCallID: "call_conformance",
		},
	})
	if err == nil {
		t.Fatal("Verify accepted a tool claim without expected tool arguments")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing cancellation fixture",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{Cancellation: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a cancellation claim without a start signal")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing partial stream fixture",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{PartialStream: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a partial stream claim without expected partial text")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing disconnect stream fixture",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{DisconnectStream: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a disconnect stream claim without expected partial text")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing retry fixture",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{Retryability: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a retry claim without expected retry text")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing timeout fixture",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{RequestTimeout: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a timeout claim without a start signal")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing stream timeout fixture",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{StreamTimeout: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a stream timeout claim without expected partial text")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing reasoning model",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{ReasoningVisibility: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a reasoning claim without a reasoning model")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:           "missing reasoning fixture",
		Model:          openai.New(openai.WithAPIKey("test-key")),
		ReasoningModel: openai.New(openai.WithAPIKey("test-key")),
		Claims:         conformance.Claims{ReasoningVisibility: true},
	})
	if err == nil {
		t.Fatal("Verify accepted a reasoning claim without expected reasoning text")
	}
	err = conformance.Verify(context.Background(), conformance.Driver{
		Name:   "missing stop-sequence fixture",
		Model:  openai.New(openai.WithAPIKey("test-key")),
		Claims: conformance.Claims{StopSequences: true},
		Expectations: conformance.Expectations{
			StopSequenceText: "expected",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "stop-sequence fixture") {
		t.Fatalf("Verify missing stop-sequence fixture error = %v", err)
	}
}

func TestVerifyRejectsToolCallFieldMismatch(t *testing.T) {
	for _, tt := range []struct {
		name          string
		toolCallID    string
		argumentsJSON string
		want          string
	}{
		{
			name:          "call ID",
			toolCallID:    "call_other",
			argumentsJSON: `{"value":"ok"}`,
			want:          "call ID",
		},
		{
			name:          "arguments",
			toolCallID:    "call_conformance",
			argumentsJSON: `{"value":"other"}`,
			want:          "arguments",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			driver := conformance.Driver{
				Name: "mismatched tool call",
				Model: core.NewTestModel(core.ToolCallResponseWithID(
					"conformance_echo",
					`{"value":"ok"}`,
					"call_conformance",
				)),
				Claims: conformance.Claims{ToolCalls: true},
				Expectations: conformance.Expectations{
					ToolName:          "conformance_echo",
					ToolCallID:        tt.toolCallID,
					ToolArgumentsJSON: tt.argumentsJSON,
				},
			}
			err := conformance.Verify(context.Background(), driver)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Verify tool mismatch error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestVerifyMatchesTheExpectedToolCallAmongSameNamedCalls(t *testing.T) {
	driver := conformance.Driver{
		Name: "multiple tool calls",
		Model: core.NewTestModel(core.MultiToolCallResponse(
			core.ToolCallPart{
				ToolName:   "conformance_echo",
				ToolCallID: "call_first",
				ArgsJSON:   `{"value":"first"}`,
			},
			core.ToolCallPart{
				ToolName:   "conformance_echo",
				ToolCallID: "call_expected",
				ArgsJSON:   `{"value":"expected"}`,
			},
		)),
		Claims: conformance.Claims{ToolCalls: true},
		Expectations: conformance.Expectations{
			ToolName:          "conformance_echo",
			ToolCallID:        "call_expected",
			ToolArgumentsJSON: `{"value":"expected"}`,
		},
	}
	if err := conformance.Verify(context.Background(), driver); err != nil {
		t.Fatalf("Verify multiple tool calls: %v", err)
	}
}

func TestVerifyRejectsStructuredOutputValueMismatch(t *testing.T) {
	driver := conformance.Driver{
		Name: "mismatched structured output",
		Model: core.NewTestModel(
			core.TextResponse(""),
			core.TextResponse(`{"value":"other"}`),
		),
		Claims: conformance.Claims{StructuredOutput: true},
		Expectations: conformance.Expectations{
			StructuredOutputValue: "expected",
		},
	}
	err := conformance.Verify(context.Background(), driver)
	if err == nil || !strings.Contains(err.Error(), "structured output value") {
		t.Fatalf("Verify structured-output mismatch error = %v, want typed value mismatch", err)
	}
}

func openAIConformanceFixture(t *testing.T, promptCache *promptCacheFixture, toolSearch *toolSearchFixture, namespaceTools *namespaceToolsFixture, cancellationReady chan<- struct{}, retry *retryFixture, timeout *timeoutFixture) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/responses" {
			openAIResponsesConformanceFixture(t, w, r, toolSearch, namespaceTools)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("OpenAI path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Fatal("OpenAI fixture request had no authorization header")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read OpenAI request: %v", err)
		}
		var request struct {
			Model          string          `json:"model"`
			Stream         bool            `json:"stream"`
			Tools          json.RawMessage `json:"tools"`
			ResponseFormat json.RawMessage `json:"response_format"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode OpenAI request: %v", err)
		}
		if request.Model == "gpt-4o" && strings.Contains(string(body), "run conformance") {
			assertOpenAIPromptCache(t, body)
			promptCache.record(request.Stream)
		}
		if strings.Contains(string(body), "cancel conformance") {
			waitForCancellation(r, cancellationReady)
			return
		}
		if strings.Contains(string(body), "stream timeout conformance") {
			writeDeadlineBoundSSE(w, r, `data: {"id":"chatcmpl-deadline","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"openai deadline"},"finish_reason":null}]}

`)
			return
		}
		if strings.Contains(string(body), "timeout conformance") {
			timeout.waitForCancellation(r.Context(), request.Model)
			return
		}
		if strings.Contains(string(body), "partial stream conformance") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-partial\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"openai partial\"},\"finish_reason\":null}]}\n\n")
			return
		}
		if strings.Contains(string(body), "disconnect stream conformance") {
			writeTruncatedSSE(w, `data: {"id":"chatcmpl-disconnect","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"openai disconnect"},"finish_reason":null}]}

`)
			return
		}
		if strings.Contains(string(body), "malformed stream conformance") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"fixture_sensitive\":\n\n")
			return
		}
		if strings.Contains(string(body), "retry conformance") {
			if retry.firstAttempt(request.Model) {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = fmt.Fprint(w, `{"error":{"type":"rate_limit_error"}}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"chatcmpl-retry","object":"chat.completion","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"openai retry"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
			return
		}
		if strings.Contains(string(body), "structured output conformance") {
			assertOpenAIStructuredOutputFormat(t, request.ResponseFormat)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"chatcmpl-structured","object":"chat.completion","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"{\"value\":\"openai structured\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
			return
		}
		if strings.Contains(string(body), "vision conformance") {
			assertOpenAIVisionRequest(t, body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"chatcmpl-vision","object":"chat.completion","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"openai vision"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
			return
		}
		if request.Stream {
			if len(request.Tools) != 0 && string(request.Tools) != "null" {
				t.Fatalf("stream request unexpectedly included tools: %s", request.Tools)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, `data: {"id":"chatcmpl-conformance","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"openai stream"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}

data: [DONE]

`)
			return
		}
		if len(request.Tools) == 0 || string(request.Tools) == "null" {
			t.Fatal("tool-capable request did not include tools")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"chatcmpl-conformance","object":"chat.completion","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"openai response","tool_calls":[{"id":"call_openai","type":"function","function":{"name":"conformance_echo","arguments":"{\"value\":\"ok\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":2}}}`)
	})
}

func openAIResponsesConformanceFixture(t *testing.T, w http.ResponseWriter, r *http.Request, toolSearch *toolSearchFixture, namespaceTools *namespaceToolsFixture) {
	t.Helper()
	if r.Header.Get("Authorization") == "" {
		t.Fatal("OpenAI reasoning fixture request had no authorization header")
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read OpenAI reasoning request: %v", err)
	}
	var request struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode OpenAI reasoning request: %v", err)
	}
	if strings.Contains(string(body), "tool search conformance") {
		if request.Model != "gpt-5.4" || request.Stream {
			t.Fatalf("unexpected OpenAI tool-search request: model=%q stream=%t", request.Model, request.Stream)
		}
		assertOpenAIToolSearchRequest(t, body)
		toolSearch.recordOpenAI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"resp-tool-search","model":"gpt-5.4","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"openai tool search"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`)
		return
	}
	if strings.Contains(string(body), "namespace tools conformance") {
		if request.Model != "gpt-5" || request.Stream {
			t.Fatalf("unexpected OpenAI namespace-tools request: model=%q stream=%t", request.Model, request.Stream)
		}
		assertOpenAINamespaceToolsRequest(t, body)
		namespaceTools.record()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"resp-namespace","model":"gpt-5","output":[{"type":"function_call","name":"conformance_namespaced","namespace":"conformance","call_id":"call_namespace_openai","arguments":"{\"value\":\"namespaced\"}"}],"usage":{"input_tokens":3,"output_tokens":2}}`)
		return
	}
	if request.Model != "gpt-5" || !request.Stream || !strings.Contains(string(body), "reasoning conformance") {
		t.Fatalf("unexpected OpenAI reasoning request: model=%q stream=%t body=%s", request.Model, request.Stream, body)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","summary":[]}}

data: {"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"openai reasoning"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","summary":[{"type":"summary_text","text":"openai reasoning"}]}}

data: {"type":"response.output_text.delta","delta":"openai reasoning answer"}

data: {"type":"response.completed","response":{"id":"resp-reasoning","model":"gpt-5","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"openai reasoning"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"openai reasoning answer"}]}],"usage":{"input_tokens":3,"output_tokens":2}}}

data: [DONE]

`)
}

func anthropicConformanceFixture(t *testing.T, promptCache *promptCacheFixture, toolSearch *toolSearchFixture, stopSequences *stopSequenceFixture, cancellationReady chan<- struct{}, retry *retryFixture, timeout *timeoutFixture, expectedModel ...string) http.Handler {
	t.Helper()
	if len(expectedModel) > 1 {
		t.Fatalf("Anthropic conformance fixture got %d expected models, want at most one", len(expectedModel))
	}
	wantModel := ""
	if len(expectedModel) == 1 {
		wantModel = expectedModel[0]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("Anthropic path = %q, want /v1/messages", r.URL.Path)
		}
		if r.Header.Get("x-api-key") == "" {
			t.Fatal("Anthropic fixture request had no API key header")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read Anthropic request: %v", err)
		}
		var request struct {
			Model         string          `json:"model"`
			Stream        bool            `json:"stream"`
			Tools         json.RawMessage `json:"tools"`
			StopSequences []string        `json:"stop_sequences"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode Anthropic request: %v", err)
		}
		if wantModel != "" && request.Model != wantModel {
			t.Fatalf("Anthropic request model = %q, want %q", request.Model, wantModel)
		}
		if strings.Contains(string(body), "run conformance") {
			assertAnthropicPromptCache(t, body)
			promptCache.record(request.Stream)
		}
		if strings.Contains(string(body), "stop sequence conformance") {
			if !slices.Equal(request.StopSequences, []string{"conformance-stop"}) {
				t.Fatalf("Anthropic stop_sequences = %#v, want [conformance-stop]", request.StopSequences)
			}
			stopSequences.record()
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"msg-stop-sequences","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"anthropic stop sequences"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`)
			return
		}
		if strings.Contains(string(body), "cancel conformance") {
			waitForCancellation(r, cancellationReady)
			return
		}
		if strings.Contains(string(body), "stream timeout conformance") {
			writeDeadlineBoundSSE(w, r, `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"anthropic deadline"}}

`)
			return
		}
		if strings.Contains(string(body), "timeout conformance") {
			timeout.waitForCancellation(r.Context(), request.Model)
			return
		}
		if strings.Contains(string(body), "partial stream conformance") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"anthropic partial\"}}\n\n")
			return
		}
		if strings.Contains(string(body), "disconnect stream conformance") {
			writeTruncatedSSE(w, `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"anthropic disconnect"}}

`)
			return
		}
		if strings.Contains(string(body), "malformed stream conformance") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: content_block_delta\ndata: {\"fixture_sensitive\":\n\n")
			return
		}
		if strings.Contains(string(body), "retry conformance") {
			if retry.firstAttempt(request.Model) {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = fmt.Fprint(w, `{"error":{"type":"rate_limit_error"}}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"msg-retry","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"anthropic retry"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`)
			return
		}
		if strings.Contains(string(body), "structured output conformance") {
			assertAnthropicStructuredOutputTool(t, request.Tools)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"msg-structured","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"tool_use","id":"call_structured","name":"final_result","input":{"value":"anthropic structured"}}],"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":2}}`)
			return
		}
		if strings.Contains(string(body), "vision conformance") {
			assertAnthropicVisionRequest(t, body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"msg-vision","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"anthropic vision"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`)
			return
		}
		if strings.Contains(string(body), "tool search conformance") {
			if request.Stream {
				t.Fatal("Anthropic tool-search request was unexpectedly streaming")
			}
			assertAnthropicToolSearchRequest(t, request.Tools)
			toolSearch.recordAnthropic()
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"msg-tool-search","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"anthropic tool search"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`)
			return
		}
		if strings.Contains(string(body), "reasoning conformance") {
			if !request.Stream {
				t.Fatal("Anthropic reasoning request was not streaming")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"anthropic reasoning"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"anthropic reasoning answer"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}

`)
			return
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"anthropic stream"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}

`)
			return
		}
		if len(request.Tools) == 0 || string(request.Tools) == "null" {
			t.Fatal("tool-capable request did not include tools")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"msg-conformance","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"anthropic response"},{"type":"tool_use","id":"call_anthropic","name":"conformance_echo","input":{"value":"ok"}}],"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":2,"cache_read_input_tokens":2}}`)
	})
}

func assertOpenAIStructuredOutputFormat(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var format struct {
		Type       string          `json:"type"`
		JSONSchema json.RawMessage `json:"json_schema"`
	}
	if err := json.Unmarshal(raw, &format); err != nil {
		t.Fatalf("decode OpenAI response_format: %v", err)
	}
	if format.Type != "json_schema" {
		t.Fatalf("OpenAI response_format type = %q, want json_schema", format.Type)
	}
	var schema struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
		Strict bool            `json:"strict"`
	}
	if err := json.Unmarshal(format.JSONSchema, &schema); err != nil {
		t.Fatalf("decode OpenAI json_schema: %v", err)
	}
	if schema.Name != core.DefaultOutputToolName || !schema.Strict {
		t.Fatalf("OpenAI json_schema = %#v, want strict %q schema", schema, core.DefaultOutputToolName)
	}
	assertStructuredOutputSchema(t, schema.Schema)
}

func assertOpenAIPromptCache(t *testing.T, body []byte) {
	t.Helper()
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode OpenAI prompt-cache request: %v", err)
	}
	if request["prompt_cache_key"] != "conformance-cache" {
		t.Fatalf("OpenAI prompt_cache_key = %#v, want conformance-cache", request["prompt_cache_key"])
	}
	if request["prompt_cache_retention"] != "24h" {
		t.Fatalf("OpenAI prompt_cache_retention = %#v, want 24h", request["prompt_cache_retention"])
	}
}

func assertAnthropicPromptCache(t *testing.T, body []byte) {
	t.Helper()
	var request struct {
		System []struct {
			CacheControl *struct {
				Type string `json:"type"`
			} `json:"cache_control"`
		} `json:"system"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode Anthropic prompt-cache request: %v", err)
	}
	if len(request.System) == 0 || request.System[len(request.System)-1].CacheControl == nil || request.System[len(request.System)-1].CacheControl.Type != "ephemeral" {
		t.Fatalf("Anthropic prompt-cache system blocks = %#v, want final ephemeral marker", request.System)
	}
}

func assertAnthropicStructuredOutputTool(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var tools []struct {
		Name        string          `json:"name"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatalf("decode Anthropic tools: %v", err)
	}
	for _, tool := range tools {
		if tool.Name == core.DefaultOutputToolName {
			assertStructuredOutputSchema(t, tool.InputSchema)
			return
		}
	}
	t.Fatalf("Anthropic structured-output request omitted %q tool: %s", core.DefaultOutputToolName, raw)
}

func assertOpenAIToolSearchRequest(t *testing.T, body []byte) {
	t.Helper()
	var request struct {
		Tools []struct {
			Type         string `json:"type"`
			Name         string `json:"name"`
			DeferLoading bool   `json:"defer_loading"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode OpenAI tool-search request: %v", err)
	}
	if len(request.Tools) != 2 || request.Tools[0].Type != "tool_search" {
		t.Fatalf("OpenAI tool-search tools = %#v, want built-in plus deferred tool", request.Tools)
	}
	deferred := request.Tools[1]
	if deferred.Type != "function" || deferred.Name != "conformance_deferred" || !deferred.DeferLoading {
		t.Fatalf("OpenAI deferred tool = %#v, want conformance deferred function", deferred)
	}
}

func assertOpenAINamespaceToolsRequest(t *testing.T, body []byte) {
	t.Helper()
	var request struct {
		Tools []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Tools []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode OpenAI namespace-tools request: %v", err)
	}
	if len(request.Tools) != 1 || request.Tools[0].Type != "namespace" || request.Tools[0].Name != "conformance" {
		t.Fatalf("OpenAI namespace-tools groups = %#v, want one conformance namespace", request.Tools)
	}
	if len(request.Tools[0].Tools) != 1 || request.Tools[0].Tools[0].Type != "function" || request.Tools[0].Tools[0].Name != "conformance_namespaced" {
		t.Fatalf("OpenAI namespace-tools contents = %#v, want conformance function", request.Tools[0].Tools)
	}
}

func assertAnthropicToolSearchRequest(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var tools []struct {
		Type         string `json:"type"`
		Name         string `json:"name"`
		DeferLoading bool   `json:"defer_loading"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatalf("decode Anthropic tool-search tools: %v", err)
	}
	if len(tools) != 2 || tools[0].Type != "tool_search_tool_regex_20251119" || tools[0].Name != "tool_search_tool_regex" {
		t.Fatalf("Anthropic tool-search tools = %#v, want regex primitive plus deferred tool", tools)
	}
	deferred := tools[1]
	if deferred.Name != "conformance_deferred" || !deferred.DeferLoading {
		t.Fatalf("Anthropic deferred tool = %#v, want conformance deferred tool", deferred)
	}
}

func assertOpenAIVisionRequest(t *testing.T, body []byte) {
	t.Helper()
	var request struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode OpenAI vision request: %v", err)
	}
	if len(request.Messages) != 1 || request.Messages[0].Role != "user" {
		t.Fatalf("OpenAI vision messages = %#v, want one user message", request.Messages)
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL *struct {
			URL    string `json:"url"`
			Detail string `json:"detail"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(request.Messages[0].Content, &parts); err != nil {
		t.Fatalf("decode OpenAI vision content: %v", err)
	}
	if len(parts) != 2 || parts[0].Type != "text" || parts[0].Text != "vision conformance" {
		t.Fatalf("OpenAI vision content = %s, want text then image", request.Messages[0].Content)
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/png;base64,AQID" || parts[1].ImageURL.Detail != "low" {
		t.Fatalf("OpenAI vision image = %#v, want low-detail PNG data URI", parts[1])
	}
}

func assertAnthropicVisionRequest(t *testing.T, body []byte) {
	t.Helper()
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Source *struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode Anthropic vision request: %v", err)
	}
	if len(request.Messages) != 1 || request.Messages[0].Role != "user" {
		t.Fatalf("Anthropic vision messages = %#v, want one user message", request.Messages)
	}
	parts := request.Messages[0].Content
	if len(parts) != 2 || parts[0].Type != "text" || parts[0].Text != "vision conformance" {
		t.Fatalf("Anthropic vision content = %#v, want text then image", parts)
	}
	if parts[1].Type != "image" || parts[1].Source == nil || parts[1].Source.Type != "base64" || parts[1].Source.MediaType != "image/png" || parts[1].Source.Data != "AQID" {
		t.Fatalf("Anthropic vision image = %#v, want base64 PNG source", parts[1])
	}
}

func assertStructuredOutputSchema(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode structured-output schema: %v", err)
	}
	if schema.Type != "object" || len(schema.Properties) != 1 || schema.Properties["value"] == nil || len(schema.Required) != 1 || schema.Required[0] != "value" {
		t.Fatalf("unexpected structured-output schema: %s", raw)
	}
	var value struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(schema.Properties["value"], &value); err != nil || value.Type != "string" {
		t.Fatalf("structured-output value schema = %s, want string", schema.Properties["value"])
	}
}

func writeTruncatedSSE(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)+1))
	_, _ = fmt.Fprint(w, body)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeDeadlineBoundSSE(w http.ResponseWriter, request *http.Request, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, body)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	<-request.Context().Done()
}

func waitForCancellation(request *http.Request, ready chan<- struct{}) {
	select {
	case ready <- struct{}{}:
	case <-request.Context().Done():
		return
	}
	<-request.Context().Done()
}

type retryFixture struct {
	mu       sync.Mutex
	attempts map[string]int
}

func newRetryFixture() *retryFixture {
	return &retryFixture{attempts: make(map[string]int)}
}

func (f *retryFixture) firstAttempt(model string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts[model]++
	return f.attempts[model] == 1
}

type timeoutFixture struct {
	mu    sync.Mutex
	ready map[string]chan struct{}
}

func newTimeoutFixture() *timeoutFixture {
	return &timeoutFixture{ready: make(map[string]chan struct{})}
}

func (f *timeoutFixture) readyFor(model string) <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.channelForLocked(model)
}

func (f *timeoutFixture) waitForCancellation(ctx context.Context, model string) {
	f.markStarted(model)
	<-ctx.Done()
}

func (f *timeoutFixture) markStarted(model string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ready := f.channelForLocked(model)
	select {
	case <-ready:
	default:
		close(ready)
	}
}

func (f *timeoutFixture) channelForLocked(model string) chan struct{} {
	ready := f.ready[model]
	if ready == nil {
		ready = make(chan struct{})
		f.ready[model] = ready
	}
	return ready
}

// Package openai provides a core.Model implementation for OpenAI's
// Chat Completions API, supporting GPT and O-series models with function
// calling, native structured output, and server-sent event streaming.
//
// # Usage
//
//	model := openai.New(
//	    openai.WithAPIKey("sk-..."),    // or set OPENAI_API_KEY env var
//	    openai.WithModel(openai.GPT4o),
//	    openai.WithServiceTier("priority"), // or set OPENAI_SERVICE_TIER
//	    openai.WithTransport("websocket"), // optional: Responses API websocket mode
//	    openai.WithWebSocketHTTPFallback(false), // default false
//	)
//
// # WebSocket Mode (Responses API)
//
// WebSocket mode is intended for long-running, tool-call-heavy loops where
// low continuation overhead matters.
//
// Configuration:
//   - OPENAI_TRANSPORT=websocket (or WithTransport("websocket"))
//   - OPENAI_WEBSOCKET_HTTP_FALLBACK=0|1
//
// Important limitations and behavior:
//   - Applies only to Responses API models (for example Codex-style models).
//   - Uses non-streaming Request() semantics; RequestStream() for Responses
//     models remains unsupported.
//   - One in-flight response per provider session/connection.
//   - Continuation state is in-memory per session. If history is rewritten
//     (for example compression/summarization), the provider sends full context
//     and starts/continues safely without incremental delta reuse.
//   - In websocket mode, store=false is applied when store is unset.
//     If websocket falls back to HTTP, the original store intent is restored.
//   - For long-lived services, call Close() on the provider to release the
//     websocket connection explicitly.
//
// # DeepSeek
//
// Use NewDeepSeek for DeepSeek's Chat Completions API
// (https://api-docs.deepseek.com/). It pins the endpoint and reads
// DEEPSEEK_API_KEY (DEEPSEEK_BASE_URL overrides the host), and adds what a
// plain OpenAI-compatible endpoint lacks: the V4 thinking toggle driven by
// ModelSettings.AdaptiveThinking and ModelSettings.ReasoningEffort
// ("low", "high", "max"), reasoning_content surfaced as core.ThinkingPart
// and core.ThinkingPartDelta, replay of reasoning_content on prior
// assistant turns (required once tools are in play), max_tokens forwarded
// only when set (capped at DeepSeekMaxOutputTokens), and vision on the
// Flash line.
//
//	model := openai.NewDeepSeek(
//	    openai.WithModel(openai.DeepSeekV4Pro), // default
//	)
//
// # LiteLLM Proxy
//
// Use NewLiteLLM to connect to a LiteLLM proxy:
//
//	model := openai.NewLiteLLM("http://localhost:4000",
//	    openai.WithModel("claude-3-sonnet"),
//	)
//
// # Local OpenAI-Compatible Endpoint
//
// Use NewLocalEndpoint for a process-owned loopback Chat Completions server:
//
//	model, err := openai.NewLocalEndpoint(openai.LocalEndpointConfig{
//	    BaseURL: "http://127.0.0.1:11434",
//	    Model:   "my-local-tool-model",
//	})
//
// The profile only accepts HTTP(S) localhost or loopback addresses, always
// uses Chat Completions, and never inherits OPENAI_API_KEY or OPENAI_BASE_URL.
// It exposes tool calls and streaming, but does not claim structured output,
// vision, prompt caching, or Responses API behavior until separately proven.
// The app-server profile reads GOLLEM_LOCAL_OPENAI_BASE_URL,
// GOLLEM_LOCAL_OPENAI_MODEL, and GOLLEM_LOCAL_OPENAI_API_KEY from its own
// process environment; their values are not included in the provider catalog.
//
// # Latency Instrumentation
//
// WithRequestObserver installs a callback that receives one secret-safe
// RequestTrace per physical provider request, covering the HTTP and WebSocket
// transports. The trace measures token refresh, request upload, time to
// headers/first-event/first-token/terminal, sanitized error classification,
// Retry-After, and continuation/cache-continuity signals.
//
// It is intended for diagnosing latency (for example comparing ChatGPT
// OAuth-backed requests across transports). It deliberately carries no
// credentials, request content, or raw provider error bodies — only sizes,
// timings, and sanitized markers.
//
// Set OPENAI_REQUEST_TRACE=1 to install the default stderr observer when no
// explicit observer is configured. When unset, the provider incurs no
// instrumentation overhead.
package openai

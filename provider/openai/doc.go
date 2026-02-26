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
// # LiteLLM Proxy
//
// Use NewLiteLLM to connect to a LiteLLM proxy:
//
//	model := openai.NewLiteLLM("http://localhost:4000",
//	    openai.WithModel("claude-3-sonnet"),
//	)
package openai

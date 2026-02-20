// Package openai provides a core.Model implementation for OpenAI's
// Chat Completions API, supporting GPT and O-series models with function
// calling, native structured output, and server-sent event streaming.
//
// # Usage
//
//	model := openai.New(
//	    openai.WithAPIKey("sk-..."),    // or set OPENAI_API_KEY env var
//	    openai.WithModel(openai.GPT4o),
//	)
//
// # LiteLLM Proxy
//
// Use NewLiteLLM to connect to a LiteLLM proxy:
//
//	model := openai.NewLiteLLM("http://localhost:4000",
//	    openai.WithModel("claude-3-sonnet"),
//	)
package openai

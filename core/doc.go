// Package core is a production-grade Go agent framework for building
// LLM-powered agents with structured output, tool use, streaming, and
// multi-provider support.
//
// The core type is Agent[T], a generic agent that uses an LLM to produce
// typed output of type T. Agents can be configured with tools (via FuncTool),
// system prompts, output validators, and usage limits.
//
// # Sub-packages
//
// Leaf functionality is organized into sub-packages:
//   - core/orchestration: Multi-agent coordination (AgentTool, Handoff, Pipeline, ChainRun)
//   - core/streamutil: Stream text helpers (StreamText, StreamTextDelta, StreamTextDebounced)
//   - core/memory: Memory strategies (SlidingWindowMemory, TokenBudgetMemory, SummaryMemory)
//
// # Basic Usage
//
//	model := anthropic.New()
//	agent := core.NewAgent[MyOutput](model,
//	    core.WithSystemPrompt[MyOutput]("You are helpful."),
//	    core.WithTools[MyOutput](myTool),
//	)
//	result, err := agent.Run(ctx, "user prompt")
//
// # Providers
//
// Gollem supports multiple LLM providers through the Model interface:
//   - provider/anthropic: Anthropic Claude models
//   - provider/openai: OpenAI GPT and O-series models
//   - provider/vertexai: Google Gemini via Vertex AI
//   - provider/vertexai_anthropic: Claude via Vertex AI
//
// # Tools
//
// Use FuncTool to create type-safe tools from Go functions:
//
//	tool := core.FuncTool[MyParams]("name", "description",
//	    func(ctx context.Context, p MyParams) (string, error) { ... })
//
// # Streaming
//
// Use RunStream for real-time token streaming:
//
//	stream, _ := agent.RunStream(ctx, "prompt")
//	for text, err := range stream.StreamText(true) { ... }
package core

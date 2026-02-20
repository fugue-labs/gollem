// Package anthropic provides a core.Model implementation for Anthropic's
// Messages API, supporting Claude models with tool use, extended thinking,
// and server-sent event streaming.
//
// # Usage
//
//	model := anthropic.New(
//	    anthropic.WithAPIKey("sk-..."),    // or set ANTHROPIC_API_KEY env var
//	    anthropic.WithModel(anthropic.Claude4Sonnet),
//	)
//
// The provider reads ANTHROPIC_API_KEY from the environment by default.
package anthropic

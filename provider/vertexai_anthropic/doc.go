// Package vertexai_anthropic provides a core.Model implementation for
// Anthropic Claude models accessed through Google Cloud's Vertex AI
// rawPredict endpoint. This enables using Claude with GCP authentication
// instead of Anthropic API keys.
//
// # Usage
//
//	model := vertexai_anthropic.New(
//	    vertexai_anthropic.WithProject("my-project"),
//	    vertexai_anthropic.WithLocation("us-east5"),
//	    vertexai_anthropic.WithModel(vertexai_anthropic.Claude4Sonnet),
//	)
//
// The request format is identical to the Anthropic Messages API.
package vertexai_anthropic

// Package vertexai provides a core.Model implementation for Google's
// Vertex AI Gemini API, supporting Gemini models with function calling,
// streaming, and GCP authentication via Application Default Credentials
// or service accounts.
//
// # Usage
//
//	model := vertexai.New(
//	    vertexai.WithProject("my-project"),
//	    vertexai.WithLocation("us-central1"),
//	    vertexai.WithModel(vertexai.Gemini25Flash),
//	)
//
// The provider uses Application Default Credentials by default.
// Set GOOGLE_CLOUD_PROJECT for the project ID.
package vertexai

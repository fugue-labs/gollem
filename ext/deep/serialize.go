package deep

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// requestPartEnvelope wraps a ModelRequestPart for JSON serialization.
type requestPartEnvelope struct {
	Kind         string `json:"kind"`
	Content      string `json:"content,omitempty"`
	ToolName     string `json:"tool_name,omitempty"`
	ToolCallID   string `json:"tool_call_id,omitempty"`
	ToolContent  any    `json:"tool_content,omitempty"`
	Timestamp    *time.Time `json:"timestamp,omitempty"`
}

// responsePartEnvelope wraps a ModelResponsePart for JSON serialization.
type responsePartEnvelope struct {
	Kind       string `json:"kind"`
	Content    string `json:"content,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ArgsJSON   string `json:"args_json,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Signature  string `json:"signature,omitempty"`
}

// serializableRequest is the JSON-safe form of ModelRequest.
type serializableRequest struct {
	Parts     []requestPartEnvelope `json:"parts"`
	Timestamp time.Time             `json:"timestamp"`
}

// serializableResponse is the JSON-safe form of ModelResponse.
type serializableResponse struct {
	Parts        []responsePartEnvelope `json:"parts"`
	Usage        core.Usage           `json:"usage"`
	ModelName    string                 `json:"model_name"`
	FinishReason string                 `json:"finish_reason"`
	Timestamp    time.Time              `json:"timestamp"`
}

func encodeRequestParts(parts []core.ModelRequestPart) []requestPartEnvelope {
	envs := make([]requestPartEnvelope, 0, len(parts))
	for _, part := range parts {
		switch p := part.(type) {
		case core.SystemPromptPart:
			ts := p.Timestamp
			envs = append(envs, requestPartEnvelope{Kind: "system-prompt", Content: p.Content, Timestamp: &ts})
		case core.UserPromptPart:
			ts := p.Timestamp
			envs = append(envs, requestPartEnvelope{Kind: "user-prompt", Content: p.Content, Timestamp: &ts})
		case core.ToolReturnPart:
			ts := p.Timestamp
			envs = append(envs, requestPartEnvelope{Kind: "tool-return", ToolName: p.ToolName, ToolContent: p.Content, ToolCallID: p.ToolCallID, Timestamp: &ts})
		case core.RetryPromptPart:
			ts := p.Timestamp
			envs = append(envs, requestPartEnvelope{Kind: "retry-prompt", Content: p.Content, ToolName: p.ToolName, ToolCallID: p.ToolCallID, Timestamp: &ts})
		}
	}
	return envs
}

func decodeRequestParts(envs []requestPartEnvelope) []core.ModelRequestPart {
	parts := make([]core.ModelRequestPart, 0, len(envs))
	for _, env := range envs {
		ts := time.Time{}
		if env.Timestamp != nil {
			ts = *env.Timestamp
		}
		switch env.Kind {
		case "system-prompt":
			parts = append(parts, core.SystemPromptPart{Content: env.Content, Timestamp: ts})
		case "user-prompt":
			parts = append(parts, core.UserPromptPart{Content: env.Content, Timestamp: ts})
		case "tool-return":
			parts = append(parts, core.ToolReturnPart{ToolName: env.ToolName, Content: env.ToolContent, ToolCallID: env.ToolCallID, Timestamp: ts})
		case "retry-prompt":
			parts = append(parts, core.RetryPromptPart{Content: env.Content, ToolName: env.ToolName, ToolCallID: env.ToolCallID, Timestamp: ts})
		}
	}
	return parts
}

func encodeResponseParts(parts []core.ModelResponsePart) []responsePartEnvelope {
	envs := make([]responsePartEnvelope, 0, len(parts))
	for _, part := range parts {
		switch p := part.(type) {
		case core.TextPart:
			envs = append(envs, responsePartEnvelope{Kind: "text", Content: p.Content})
		case core.ToolCallPart:
			envs = append(envs, responsePartEnvelope{Kind: "tool-call", ToolName: p.ToolName, ArgsJSON: p.ArgsJSON, ToolCallID: p.ToolCallID})
		case core.ThinkingPart:
			envs = append(envs, responsePartEnvelope{Kind: "thinking", Content: p.Content, Signature: p.Signature})
		}
	}
	return envs
}

func decodeResponseParts(envs []responsePartEnvelope) []core.ModelResponsePart {
	parts := make([]core.ModelResponsePart, 0, len(envs))
	for _, env := range envs {
		switch env.Kind {
		case "text":
			parts = append(parts, core.TextPart{Content: env.Content})
		case "tool-call":
			parts = append(parts, core.ToolCallPart{ToolName: env.ToolName, ArgsJSON: env.ArgsJSON, ToolCallID: env.ToolCallID})
		case "thinking":
			parts = append(parts, core.ThinkingPart{Content: env.Content, Signature: env.Signature})
		}
	}
	return parts
}

// encodeMessages converts a slice of ModelMessage to JSON-safe messageEnvelopes.
func encodeMessages(messages []core.ModelMessage) []messageEnvelope {
	envs := make([]messageEnvelope, 0, len(messages))
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.ModelRequest:
			data, _ := json.Marshal(serializableRequest{
				Parts:     encodeRequestParts(m.Parts),
				Timestamp: m.Timestamp,
			})
			envs = append(envs, messageEnvelope{Kind: "request", RawData: data})
		case core.ModelResponse:
			data, _ := json.Marshal(serializableResponse{
				Parts:        encodeResponseParts(m.Parts),
				Usage:        m.Usage,
				ModelName:    m.ModelName,
				FinishReason: string(m.FinishReason),
				Timestamp:    m.Timestamp,
			})
			envs = append(envs, messageEnvelope{Kind: "response", RawData: data})
		}
	}
	return envs
}

// decodeMessages converts messageEnvelopes back to ModelMessage slice.
func decodeMessages(envs []messageEnvelope) ([]core.ModelMessage, error) {
	messages := make([]core.ModelMessage, 0, len(envs))
	for _, env := range envs {
		switch env.Kind {
		case "request":
			var sr serializableRequest
			if err := json.Unmarshal(env.RawData, &sr); err != nil {
				return nil, fmt.Errorf("unmarshaling request: %w", err)
			}
			messages = append(messages, core.ModelRequest{
				Parts:     decodeRequestParts(sr.Parts),
				Timestamp: sr.Timestamp,
			})
		case "response":
			var sr serializableResponse
			if err := json.Unmarshal(env.RawData, &sr); err != nil {
				return nil, fmt.Errorf("unmarshaling response: %w", err)
			}
			messages = append(messages, core.ModelResponse{
				Parts:        decodeResponseParts(sr.Parts),
				Usage:        sr.Usage,
				ModelName:    sr.ModelName,
				FinishReason: core.FinishReason(sr.FinishReason),
				Timestamp:    sr.Timestamp,
			})
		default:
			return nil, fmt.Errorf("unknown message kind: %s", env.Kind)
		}
	}
	return messages, nil
}

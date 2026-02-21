package core

import (
	"encoding/json"
	"fmt"
	"time"
)

// messageEnvelope wraps a ModelMessage for public JSON serialization.
type messageEnvelope struct {
	Kind string          `json:"kind"` // "request" or "response"
	Data json.RawMessage `json:"data"`
}

// requestJSON is the JSON-safe form of ModelRequest.
type requestJSON struct {
	Parts     []partEnvelope `json:"parts"`
	Timestamp time.Time      `json:"timestamp"`
}

// responseJSON is the JSON-safe form of ModelResponse.
type responseJSON struct {
	Parts        []partEnvelope `json:"parts"`
	Usage        Usage          `json:"usage"`
	ModelName    string         `json:"model_name"`
	FinishReason string         `json:"finish_reason"`
	Timestamp    time.Time      `json:"timestamp"`
}

// partEnvelope wraps a request or response part for JSON serialization.
type partEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// --- Request part JSON types ---

type systemPromptJSON struct {
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type userPromptJSON struct {
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type toolReturnJSON struct {
	ToolName   string          `json:"tool_name"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	Timestamp  time.Time       `json:"timestamp"`
}

type retryPromptJSON struct {
	Content    string    `json:"content"`
	ToolName   string    `json:"tool_name,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// --- Multimodal request part JSON types ---

type imagePartJSON struct {
	URL       string    `json:"url"`
	MIMEType  string    `json:"mime_type"`
	Detail    string    `json:"detail,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type audioPartJSON struct {
	URL       string    `json:"url"`
	MIMEType  string    `json:"mime_type"`
	Timestamp time.Time `json:"timestamp"`
}

type documentPartJSON struct {
	URL       string    `json:"url"`
	MIMEType  string    `json:"mime_type"`
	Title     string    `json:"title,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// --- Response part JSON types ---

type textPartJSON struct {
	Content string `json:"content"`
}

type toolCallPartJSON struct {
	ToolName   string            `json:"tool_name"`
	ArgsJSON   string            `json:"args_json"`
	ToolCallID string            `json:"tool_call_id"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type thinkingPartJSON struct {
	Content   string `json:"content"`
	Signature string `json:"signature,omitempty"`
}

// MarshalMessages serializes a conversation ([]ModelMessage) to JSON.
func MarshalMessages(messages []ModelMessage) ([]byte, error) {
	envelopes := make([]messageEnvelope, 0, len(messages))

	for _, msg := range messages {
		env, err := encodeMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("encoding message: %w", err)
		}
		envelopes = append(envelopes, env)
	}

	return json.Marshal(envelopes)
}

// UnmarshalMessages deserializes JSON back into []ModelMessage.
func UnmarshalMessages(data []byte) ([]ModelMessage, error) {
	var envelopes []messageEnvelope
	if err := json.Unmarshal(data, &envelopes); err != nil {
		return nil, fmt.Errorf("unmarshaling message envelopes: %w", err)
	}

	messages := make([]ModelMessage, 0, len(envelopes))
	for _, env := range envelopes {
		msg, err := decodeMessage(env)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

// encodeMessage converts a single ModelMessage to a messageEnvelope.
func encodeMessage(msg ModelMessage) (messageEnvelope, error) {
	switch m := msg.(type) {
	case ModelRequest:
		parts, err := encodeRequestParts(m.Parts)
		if err != nil {
			return messageEnvelope{}, err
		}
		data, err := json.Marshal(requestJSON{
			Parts:     parts,
			Timestamp: m.Timestamp,
		})
		if err != nil {
			return messageEnvelope{}, fmt.Errorf("marshaling request: %w", err)
		}
		return messageEnvelope{Kind: "request", Data: data}, nil

	case ModelResponse:
		parts, err := encodeResponseParts(m.Parts)
		if err != nil {
			return messageEnvelope{}, err
		}
		data, err := json.Marshal(responseJSON{
			Parts:        parts,
			Usage:        m.Usage,
			ModelName:    m.ModelName,
			FinishReason: string(m.FinishReason),
			Timestamp:    m.Timestamp,
		})
		if err != nil {
			return messageEnvelope{}, fmt.Errorf("marshaling response: %w", err)
		}
		return messageEnvelope{Kind: "response", Data: data}, nil

	default:
		return messageEnvelope{}, fmt.Errorf("unknown message type: %T", msg)
	}
}

// decodeMessage converts a messageEnvelope back to a ModelMessage.
func decodeMessage(env messageEnvelope) (ModelMessage, error) {
	switch env.Kind {
	case "request":
		var rj requestJSON
		if err := json.Unmarshal(env.Data, &rj); err != nil {
			return nil, fmt.Errorf("unmarshaling request: %w", err)
		}
		parts, err := decodeRequestParts(rj.Parts)
		if err != nil {
			return nil, err
		}
		return ModelRequest{
			Parts:     parts,
			Timestamp: rj.Timestamp,
		}, nil

	case "response":
		var rj responseJSON
		if err := json.Unmarshal(env.Data, &rj); err != nil {
			return nil, fmt.Errorf("unmarshaling response: %w", err)
		}
		parts, err := decodeResponseParts(rj.Parts)
		if err != nil {
			return nil, err
		}
		return ModelResponse{
			Parts:        parts,
			Usage:        rj.Usage,
			ModelName:    rj.ModelName,
			FinishReason: FinishReason(rj.FinishReason),
			Timestamp:    rj.Timestamp,
		}, nil

	default:
		return nil, fmt.Errorf("unknown message kind: %q", env.Kind)
	}
}

// encodeRequestParts converts request parts to part envelopes.
func encodeRequestParts(parts []ModelRequestPart) ([]partEnvelope, error) {
	envs := make([]partEnvelope, 0, len(parts))
	for _, part := range parts {
		env, err := encodeRequestPart(part)
		if err != nil {
			return nil, err
		}
		envs = append(envs, env)
	}
	return envs, nil
}

func encodeRequestPart(part ModelRequestPart) (partEnvelope, error) {
	switch p := part.(type) {
	case SystemPromptPart:
		data, err := json.Marshal(systemPromptJSON(p))
		if err != nil {
			return partEnvelope{}, err
		}
		return partEnvelope{Type: "system-prompt", Data: data}, nil

	case UserPromptPart:
		data, err := json.Marshal(userPromptJSON(p))
		if err != nil {
			return partEnvelope{}, err
		}
		return partEnvelope{Type: "user-prompt", Data: data}, nil

	case ToolReturnPart:
		contentData, err := json.Marshal(p.Content)
		if err != nil {
			return partEnvelope{}, fmt.Errorf("marshaling tool return content: %w", err)
		}
		data, err := json.Marshal(toolReturnJSON{
			ToolName:   p.ToolName,
			Content:    contentData,
			ToolCallID: p.ToolCallID,
			Timestamp:  p.Timestamp,
		})
		if err != nil {
			return partEnvelope{}, err
		}
		return partEnvelope{Type: "tool-return", Data: data}, nil

	case RetryPromptPart:
		data, err := json.Marshal(retryPromptJSON(p))
		if err != nil {
			return partEnvelope{}, err
		}
		return partEnvelope{Type: "retry-prompt", Data: data}, nil

	case ImagePart:
		data, err := json.Marshal(imagePartJSON(p))
		if err != nil {
			return partEnvelope{}, err
		}
		return partEnvelope{Type: "image", Data: data}, nil

	case AudioPart:
		data, err := json.Marshal(audioPartJSON(p))
		if err != nil {
			return partEnvelope{}, err
		}
		return partEnvelope{Type: "audio", Data: data}, nil

	case DocumentPart:
		data, err := json.Marshal(documentPartJSON(p))
		if err != nil {
			return partEnvelope{}, err
		}
		return partEnvelope{Type: "document", Data: data}, nil

	default:
		return partEnvelope{}, fmt.Errorf("unknown request part type: %T", part)
	}
}

// encodeResponseParts converts response parts to part envelopes.
func encodeResponseParts(parts []ModelResponsePart) ([]partEnvelope, error) {
	envs := make([]partEnvelope, 0, len(parts))
	for _, part := range parts {
		env, err := encodeResponsePart(part)
		if err != nil {
			return nil, err
		}
		envs = append(envs, env)
	}
	return envs, nil
}

func encodeResponsePart(part ModelResponsePart) (partEnvelope, error) {
	switch p := part.(type) {
	case TextPart:
		data, err := json.Marshal(textPartJSON(p))
		if err != nil {
			return partEnvelope{}, err
		}
		return partEnvelope{Type: "text", Data: data}, nil

	case ToolCallPart:
		data, err := json.Marshal(toolCallPartJSON(p))
		if err != nil {
			return partEnvelope{}, err
		}
		return partEnvelope{Type: "tool-call", Data: data}, nil

	case ThinkingPart:
		data, err := json.Marshal(thinkingPartJSON(p))
		if err != nil {
			return partEnvelope{}, err
		}
		return partEnvelope{Type: "thinking", Data: data}, nil

	default:
		return partEnvelope{}, fmt.Errorf("unknown response part type: %T", part)
	}
}

// decodeRequestParts converts part envelopes back to request parts.
func decodeRequestParts(envs []partEnvelope) ([]ModelRequestPart, error) {
	parts := make([]ModelRequestPart, 0, len(envs))
	for _, env := range envs {
		part, err := decodeRequestPart(env)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func decodeRequestPart(env partEnvelope) (ModelRequestPart, error) {
	switch env.Type {
	case "system-prompt":
		var p systemPromptJSON
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return nil, fmt.Errorf("unmarshaling system-prompt: %w", err)
		}
		return SystemPromptPart(p), nil

	case "user-prompt":
		var p userPromptJSON
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return nil, fmt.Errorf("unmarshaling user-prompt: %w", err)
		}
		return UserPromptPart(p), nil

	case "tool-return":
		var p toolReturnJSON
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return nil, fmt.Errorf("unmarshaling tool-return: %w", err)
		}
		// Decode the content as any — could be string, map, etc.
		var content any
		if err := json.Unmarshal(p.Content, &content); err != nil {
			return nil, fmt.Errorf("unmarshaling tool-return content: %w", err)
		}
		return ToolReturnPart{
			ToolName:   p.ToolName,
			Content:    content,
			ToolCallID: p.ToolCallID,
			Timestamp:  p.Timestamp,
		}, nil

	case "retry-prompt":
		var p retryPromptJSON
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return nil, fmt.Errorf("unmarshaling retry-prompt: %w", err)
		}
		return RetryPromptPart(p), nil

	case "image":
		var p imagePartJSON
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return nil, fmt.Errorf("unmarshaling image: %w", err)
		}
		return ImagePart(p), nil

	case "audio":
		var p audioPartJSON
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return nil, fmt.Errorf("unmarshaling audio: %w", err)
		}
		return AudioPart(p), nil

	case "document":
		var p documentPartJSON
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return nil, fmt.Errorf("unmarshaling document: %w", err)
		}
		return DocumentPart(p), nil

	default:
		return nil, fmt.Errorf("unknown request part type: %q", env.Type)
	}
}

// decodeResponseParts converts part envelopes back to response parts.
func decodeResponseParts(envs []partEnvelope) ([]ModelResponsePart, error) {
	parts := make([]ModelResponsePart, 0, len(envs))
	for _, env := range envs {
		part, err := decodeResponsePart(env)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func decodeResponsePart(env partEnvelope) (ModelResponsePart, error) {
	switch env.Type {
	case "text":
		var p textPartJSON
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return nil, fmt.Errorf("unmarshaling text: %w", err)
		}
		return TextPart(p), nil

	case "tool-call":
		var p toolCallPartJSON
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return nil, fmt.Errorf("unmarshaling tool-call: %w", err)
		}
		return ToolCallPart(p), nil

	case "thinking":
		var p thinkingPartJSON
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return nil, fmt.Errorf("unmarshaling thinking: %w", err)
		}
		return ThinkingPart(p), nil

	default:
		return nil, fmt.Errorf("unknown response part type: %q", env.Type)
	}
}

// AllMessagesJSON serializes the full conversation history of a RunResult to JSON.
func (r *RunResult[T]) AllMessagesJSON() ([]byte, error) {
	return MarshalMessages(r.Messages)
}

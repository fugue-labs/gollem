package vertexai_anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// Anthropic API types — identical format used via Vertex AI rawPredict.

type apiRequest struct {
	Model         string           `json:"model"`
	MaxTokens     int              `json:"max_tokens"`
	CacheControl  *apiCacheControl `json:"cache_control,omitempty"`
	System        []apiSystemBlock `json:"system,omitempty"`
	Messages      []apiMessage     `json:"messages"`
	Tools         []apiTool        `json:"tools,omitempty"`
	ToolChoice    *apiToolChoice   `json:"tool_choice,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
	Temperature   *float64         `json:"temperature,omitempty"`
	TopP          *float64         `json:"top_p,omitempty"`
	StopSequences []string         `json:"stop_sequences,omitempty"`
	Thinking      *apiThinking     `json:"thinking,omitempty"`
	OutputConfig  *apiOutputConfig `json:"output_config,omitempty"`
	// AnthropicVersion is sent in the request body for Vertex AI.
	AnthropicVersion string `json:"anthropic_version"`
}

type apiCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type apiToolChoice struct {
	Type string `json:"type"`           // "auto", "any", "tool"
	Name string `json:"name,omitempty"` // required when type is "tool"
}

// apiThinking controls extended thinking. Two modes:
//   - {type: "enabled", budget_tokens: N} — manual. Rejected by Opus 4.7+.
//   - {type: "adaptive"} — adaptive thinking. The only mode on Opus 4.7+.
type apiThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// apiOutputConfig carries the `effort` parameter. Values: low|medium|high|xhigh|max.
type apiOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type apiSystemBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiMessage struct {
	Role    string            `json:"role"`
	Content []apiContentBlock `json:"content"`
}

type apiContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	// image / document block
	Source *apiSource `json:"source,omitempty"`
	Title  string     `json:"title,omitempty"`
	// thinking block
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// contentBlocks, when non-nil, represents a tool_result with
	// structured multimodal content (e.g., text + images). When set,
	// MarshalJSON emits `content` as an array of blocks rather than a
	// string.
	contentBlocks []apiContentBlock `json:"-"`
}

// apiSource is the `source` object embedded in image/document blocks.
type apiSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// MarshalJSON emits a tool_result with array content when contentBlocks is
// set; otherwise falls back to standard struct marshaling.
func (b apiContentBlock) MarshalJSON() ([]byte, error) {
	if b.contentBlocks != nil {
		return json.Marshal(struct {
			Type      string            `json:"type"`
			ToolUseID string            `json:"tool_use_id,omitempty"`
			Content   []apiContentBlock `json:"content"`
			IsError   bool              `json:"is_error,omitempty"`
		}{
			Type:      b.Type,
			ToolUseID: b.ToolUseID,
			Content:   b.contentBlocks,
			IsError:   b.IsError,
		})
	}
	type alias apiContentBlock
	return json.Marshal(alias(b))
}

type apiTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type apiResponse struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Content    []apiContentBlock `json:"content"`
	Model      string            `json:"model"`
	StopReason string            `json:"stop_reason"`
	Usage      apiUsage          `json:"usage"`
}

type apiUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// buildRequest converts gollem messages into an Anthropic API request for Vertex AI.
func buildRequest(messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters, model string, defaultMaxTokens int, stream bool) (*apiRequest, error) {
	req := &apiRequest{
		Model:            model,
		MaxTokens:        defaultMaxTokens,
		Stream:           stream,
		AnthropicVersion: anthropicVersion,
	}

	if settings != nil {
		if settings.MaxTokens != nil {
			req.MaxTokens = *settings.MaxTokens
		}
		req.Temperature = settings.Temperature
		req.TopP = settings.TopP
		req.StopSequences = settings.StopSequences

		// Extended thinking. Two modes, mutually exclusive: adaptive (the
		// model decides; the only mode on Opus 4.7+) and the legacy manual
		// budget (rejected by Opus 4.7+). Fail fast with a pointer to the
		// correct option rather than a generic 400.
		adaptive := settings.AdaptiveThinking != nil && *settings.AdaptiveThinking
		manual := settings.ThinkingBudget != nil && *settings.ThinkingBudget > 0
		if adaptive && manual {
			return nil, errors.New(
				"vertexai_anthropic: AdaptiveThinking and ThinkingBudget are mutually exclusive; pick one thinking mode",
			)
		}
		if adaptive {
			if !supportsAdaptiveThinking(model) {
				return nil, fmt.Errorf(
					"vertexai_anthropic: model %q does not support adaptive thinking; use a Claude 4.6+ model, or ThinkingBudget for manual thinking on older models",
					model,
				)
			}
			req.Thinking = &apiThinking{Type: "adaptive"}
			// Anthropic requires temperature to be omitted when thinking is enabled.
			req.Temperature = nil
		}
		if manual {
			if !supportsManualThinking(model) {
				return nil, fmt.Errorf(
					"vertexai_anthropic: model %q does not support manual thinking; set AdaptiveThinking or use WithReasoningEffort(\"high\"|\"xhigh\"|\"max\") instead",
					model,
				)
			}
			req.Thinking = &apiThinking{
				Type:         "enabled",
				BudgetTokens: *settings.ThinkingBudget,
			}
			// Anthropic requires max_tokens > budget_tokens. Auto-adjust if the
			// caller didn't set MaxTokens high enough. Without this, the API
			// returns 400 when ThinkingBudget is set but MaxTokens is left at
			// the default (4096) or any value <= budget_tokens.
			if req.MaxTokens <= *settings.ThinkingBudget {
				req.MaxTokens = *settings.ThinkingBudget + 16000
			}
			// Anthropic requires temperature to be omitted when thinking is enabled.
			req.Temperature = nil
		}

		// Effort parameter — gated per model. xhigh is Opus-4.7/Mythos-only;
		// max needs 4.6+; low/medium/high needs any effort-capable model.
		if settings.ReasoningEffort != nil && *settings.ReasoningEffort != "" {
			effort := *settings.ReasoningEffort
			if !supportsEffortValue(model, effort) {
				return nil, fmt.Errorf(
					"vertexai_anthropic: model %q does not support reasoning effort %q",
					model, effort,
				)
			}
			req.OutputConfig = &apiOutputConfig{Effort: effort}
		}
	}

	if params != nil {
		allTools := params.AllToolDefs()
		for _, td := range allTools {
			schemaJSON, err := json.Marshal(td.ParametersSchema)
			if err != nil {
				return nil, err
			}
			req.Tools = append(req.Tools, apiTool{
				Name:        td.Name,
				Description: td.Description,
				InputSchema: schemaJSON,
			})
		}
	}

	// Apply tool choice from settings.
	if settings != nil && settings.ToolChoice != nil {
		tc := settings.ToolChoice
		switch {
		case tc.Mode == "none":
			req.Tools = nil
		case tc.Mode == "required":
			req.ToolChoice = &apiToolChoice{Type: "any"}
		case tc.ToolName != "":
			req.ToolChoice = &apiToolChoice{Type: "tool", Name: tc.ToolName}
		case tc.Mode == "auto":
			req.ToolChoice = &apiToolChoice{Type: "auto"}
		}
	}

	var systemBlocks []apiSystemBlock
	var apiMsgs []apiMessage

	for _, msg := range messages {
		switch m := msg.(type) {
		case core.ModelRequest:
			var userBlocks []apiContentBlock
			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.SystemPromptPart:
					systemBlocks = append(systemBlocks, apiSystemBlock{
						Type: "text",
						Text: p.Content,
					})
				case core.UserPromptPart:
					userBlocks = append(userBlocks, apiContentBlock{
						Type: "text",
						Text: p.Content,
					})
				case core.ToolReturnPart:
					content := ""
					switch v := p.Content.(type) {
					case string:
						content = v
					default:
						b, _ := json.Marshal(v)
						content = string(b)
					}
					if len(p.Images) > 0 {
						blocks := make([]apiContentBlock, 0, len(p.Images)+1)
						if content != "" {
							blocks = append(blocks, apiContentBlock{Type: "text", Text: content})
						}
						for _, img := range p.Images {
							src, err := toAnthropicSource(img.URL, img.MIMEType)
							if err != nil {
								return nil, fmt.Errorf("vertexai_anthropic: tool_result image: %w", err)
							}
							blocks = append(blocks, apiContentBlock{Type: "image", Source: src})
						}
						userBlocks = append(userBlocks, apiContentBlock{
							Type:          "tool_result",
							ToolUseID:     p.ToolCallID,
							contentBlocks: blocks,
						})
					} else {
						userBlocks = append(userBlocks, apiContentBlock{
							Type:      "tool_result",
							ToolUseID: p.ToolCallID,
							Content:   content,
						})
					}
				case core.ImagePart:
					src, err := toAnthropicSource(p.URL, p.MIMEType)
					if err != nil {
						return nil, fmt.Errorf("vertexai_anthropic: image: %w", err)
					}
					userBlocks = append(userBlocks, apiContentBlock{
						Type:   "image",
						Source: src,
					})
				case core.DocumentPart:
					src, err := toAnthropicSource(p.URL, p.MIMEType)
					if err != nil {
						return nil, fmt.Errorf("vertexai_anthropic: document: %w", err)
					}
					userBlocks = append(userBlocks, apiContentBlock{
						Type:   "document",
						Source: src,
						Title:  p.Title,
					})
				case core.AudioPart:
					return nil, errors.New("vertexai_anthropic: audio input is not supported by the Messages API")
				case core.RetryPromptPart:
					if p.ToolCallID != "" {
						userBlocks = append(userBlocks, apiContentBlock{
							Type:      "tool_result",
							ToolUseID: p.ToolCallID,
							Content:   p.Content,
							IsError:   true,
						})
					} else {
						userBlocks = append(userBlocks, apiContentBlock{
							Type: "text",
							Text: p.Content,
						})
					}
				default:
					return nil, fmt.Errorf("vertexai_anthropic provider: unsupported request part type %T", part)
				}
			}
			if len(userBlocks) > 0 {
				apiMsgs = append(apiMsgs, apiMessage{
					Role:    "user",
					Content: userBlocks,
				})
			} else if len(m.Parts) > 0 {
				// The ModelRequest contained only SystemPromptParts (extracted
				// above to the top-level system field). Emit a placeholder user
				// message to prevent consecutive assistant messages which violate
				// the alternation requirement and cause 400 errors.
				apiMsgs = append(apiMsgs, apiMessage{
					Role:    "user",
					Content: []apiContentBlock{{Type: "text", Text: "[system context updated]"}},
				})
			}

		case core.ModelResponse:
			var assistantBlocks []apiContentBlock
			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.TextPart:
					assistantBlocks = append(assistantBlocks, apiContentBlock{
						Type: "text",
						Text: p.Content,
					})
				case core.ToolCallPart:
					assistantBlocks = append(assistantBlocks, apiContentBlock{
						Type:  "tool_use",
						ID:    p.ToolCallID,
						Name:  p.ToolName,
						Input: json.RawMessage(p.ArgsJSON),
					})
				case core.ThinkingPart:
					assistantBlocks = append(assistantBlocks, apiContentBlock{
						Type:      "thinking",
						Thinking:  p.Content,
						Signature: p.Signature,
					})
				}
			}
			// Always emit an assistant message for a ModelResponse to maintain
			// proper user/assistant alternation. If the response had no content
			// (e.g., an empty response that triggered a retry), use a minimal
			// text block. Without this, adjacent user messages would cause a
			// 400 error from the Anthropic API.
			if len(assistantBlocks) == 0 {
				assistantBlocks = []apiContentBlock{{Type: "text", Text: ""}}
			}
			apiMsgs = append(apiMsgs, apiMessage{
				Role:    "assistant",
				Content: assistantBlocks,
			})
		}
	}

	req.System = systemBlocks
	req.Messages = apiMsgs
	return req, nil
}

// parseResponse converts an Anthropic API response to a core.ModelResponse.
func parseResponse(resp *apiResponse, modelName string) *core.ModelResponse {
	var parts []core.ModelResponsePart

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			parts = append(parts, core.TextPart{Content: block.Text})
		case "tool_use":
			argsJSON := "{}"
			if block.Input != nil {
				argsJSON = string(block.Input)
			}
			parts = append(parts, core.ToolCallPart{
				ToolName:   block.Name,
				ArgsJSON:   argsJSON,
				ToolCallID: block.ID,
			})
		case "thinking":
			parts = append(parts, core.ThinkingPart{
				Content:   block.Thinking,
				Signature: block.Signature,
			})
		}
	}

	return &core.ModelResponse{
		Parts:        parts,
		Usage:        mapUsage(resp.Usage),
		ModelName:    modelName,
		FinishReason: mapStopReason(resp.StopReason),
		Timestamp:    time.Now(),
	}
}

// toAnthropicSource converts a gollem part URL into an Anthropic `source`
// object. data:MIME;base64,DATA URIs become {type: "base64", media_type,
// data}; anything else is passed through as {type: "url", url}. If the
// caller provided an explicit MIMEType, it takes precedence over the one
// embedded in the data URI.
func toAnthropicSource(url, mimeOverride string) (*apiSource, error) {
	if rest, ok := strings.CutPrefix(url, "data:"); ok {
		semi := strings.Index(rest, ";")
		if semi < 0 {
			return nil, errors.New("malformed data URI: missing ';'")
		}
		mime := rest[:semi]
		data, ok := strings.CutPrefix(rest[semi+1:], "base64,")
		if !ok {
			return nil, errors.New("data URI must be base64-encoded")
		}
		if mimeOverride != "" {
			mime = mimeOverride
		}
		return &apiSource{Type: "base64", MediaType: mime, Data: data}, nil
	}
	if url == "" {
		return nil, errors.New("empty URL")
	}
	return &apiSource{Type: "url", URL: url}, nil
}

// mapStopReason maps Anthropic stop reasons to gollem FinishReasons.
func mapStopReason(reason string) core.FinishReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return core.FinishReasonStop
	case "max_tokens":
		return core.FinishReasonLength
	case "tool_use":
		return core.FinishReasonToolCall
	case "refusal":
		return core.FinishReasonContentFilter
	default:
		return core.FinishReasonStop
	}
}

// mapUsage converts Anthropic usage to gollem Usage.
func mapUsage(u apiUsage) core.Usage {
	return core.Usage{
		// InputTokens is the total prompt token count including cached tokens,
		// normalized to match OpenAI semantics. Anthropic's API reports non-cached
		// tokens separately, so we sum all three categories.
		InputTokens:      u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens,
		OutputTokens:     u.OutputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
	}
}

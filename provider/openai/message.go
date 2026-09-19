package openai

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// --- API request types ---

type apiRequest struct {
	Model                string             `json:"model"`
	Messages             []apiMessage       `json:"messages"`
	Tools                []apiToolDef       `json:"tools,omitempty"`
	ToolChoice           any                `json:"tool_choice,omitempty"`
	ServiceTier          string             `json:"service_tier,omitempty"`
	Stream               bool               `json:"stream,omitempty"`
	StreamOptions        *apiStreamOptions  `json:"stream_options,omitempty"`
	PromptCacheKey       string             `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string             `json:"prompt_cache_retention,omitempty"`
	MaxTokens            int                `json:"max_completion_tokens,omitempty"`
	Temperature          *float64           `json:"temperature,omitempty"`
	TopP                 *float64           `json:"top_p,omitempty"`
	ResponseFormat       *apiResponseFormat `json:"response_format,omitempty"`
	ReasoningEffort      *string            `json:"reasoning_effort,omitempty"`
	// MaxTokensLegacy and Thinking are only populated for DeepSeek, which
	// reads max_tokens rather than max_completion_tokens and exposes a
	// thinking toggle.
	MaxTokensLegacy int          `json:"max_tokens,omitempty"`
	Thinking        *apiThinking `json:"thinking,omitempty"`
}

type apiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type apiMessage struct {
	Role         string           `json:"role"` // system, user, assistant, tool
	Content      string           `json:"content,omitempty"`
	ContentParts []apiContentPart `json:"-"`
	ToolCalls    []apiToolCall    `json:"tool_calls,omitempty"`
	ToolCallID   string           `json:"tool_call_id,omitempty"`
	// ReasoningContent carries an assistant turn's chain of thought. It is
	// only serialized when emitReasoning is set (DeepSeek tool-call turns).
	ReasoningContent string `json:"-"`
	emitReasoning    bool
}

type apiContentPart struct {
	Type     string       `json:"type"` // "text", "image_url"
	Text     string       `json:"text,omitempty"`
	ImageURL *apiImageURL `json:"image_url,omitempty"`
}

type apiImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// MarshalJSON supports both string and array payloads for the "content" field.
// OpenAI chat expects string content for text-only messages and array content
// for multimodal messages (text + image parts).
func (m apiMessage) MarshalJSON() ([]byte, error) {
	payload := map[string]any{
		"role": m.Role,
	}
	if len(m.ContentParts) > 0 {
		payload["content"] = m.ContentParts
	} else if m.Content != "" {
		payload["content"] = m.Content
	}
	if len(m.ToolCalls) > 0 {
		payload["tool_calls"] = m.ToolCalls
	}
	if m.ToolCallID != "" {
		payload["tool_call_id"] = m.ToolCallID
	}
	if m.emitReasoning && m.ReasoningContent != "" {
		payload["reasoning_content"] = m.ReasoningContent
	}
	return json.Marshal(payload)
}

type apiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"` // "function"
	Function apiToolFunction `json:"function"`
}

type apiToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type apiToolDef struct {
	Type     string         `json:"type"` // "function"
	Function apiToolDefFunc `json:"function"`
}

// apiToolDefFunc is the Chat Completions function shape. DeferLoading
// is intentionally NOT included — tool_search is only available on the
// Responses API (gpt-5.4+). Chat Completions silently ignores the
// core.ToolDefinition.DeferLoading field.
type apiToolDefFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict,omitempty"`
}

type apiResponseFormat struct {
	Type       string         `json:"type"` // "json_schema" or "json_object"
	JSONSchema *apiJSONSchema `json:"json_schema,omitempty"`
}

type apiJSONSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict,omitempty"`
}

// --- API response types ---

type apiResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []apiChoice `json:"choices"`
	Usage   apiUsage    `json:"usage"`
}

type apiChoice struct {
	Index        int        `json:"index"`
	Message      apiChatMsg `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type apiChatMsg struct {
	Role             string        `json:"role"`
	Content          string        `json:"content"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ToolCalls        []apiToolCall `json:"tool_calls,omitempty"`
	Refusal          string        `json:"refusal,omitempty"`
}

type apiUsage struct {
	PromptTokens            int                     `json:"prompt_tokens"`
	CompletionTokens        int                     `json:"completion_tokens"`
	TotalTokens             int                     `json:"total_tokens"`
	PromptTokensDetails     *apiPromptTokensDetails `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *apiCompletionDetails   `json:"completion_tokens_details,omitempty"`
}

type apiPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
	AudioTokens  int `json:"audio_tokens,omitempty"`
}

type apiCompletionDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
	AudioTokens     int `json:"audio_tokens,omitempty"`
}

// buildRequest converts gollem messages into an OpenAI Chat Completions API request.
func buildRequest(messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters, model string, defaultMaxTokens int, stream bool) (*apiRequest, error) {
	req := &apiRequest{
		Model:     model,
		MaxTokens: defaultMaxTokens,
		Stream:    stream,
	}

	if stream {
		req.StreamOptions = &apiStreamOptions{IncludeUsage: true}
	}

	if settings != nil {
		if settings.MaxTokens != nil {
			req.MaxTokens = *settings.MaxTokens
		}
		req.Temperature = settings.Temperature
		req.TopP = settings.TopP
		req.ReasoningEffort = settings.ReasoningEffort
	}

	// Convert tool definitions.
	if params != nil {
		allTools := params.AllToolDefs()
		for _, td := range allTools {
			schemaJSON, err := marshalOpenAISchema(td.ParametersSchema)
			if err != nil {
				return nil, err
			}
			toolDef := apiToolDef{
				Type: "function",
				Function: apiToolDefFunc{
					Name:        td.Name,
					Description: td.Description,
					Parameters:  schemaJSON,
				},
			}
			if td.Strict != nil && *td.Strict {
				strict := true
				toolDef.Function.Strict = &strict
			}
			req.Tools = append(req.Tools, toolDef)
		}

		// Handle native structured output via response_format.
		if params.OutputMode == core.OutputModeNative && params.OutputObject != nil {
			schemaJSON, err := marshalOpenAISchema(params.OutputObject.JSONSchema)
			if err != nil {
				return nil, err
			}
			strict := true
			if params.OutputObject.Strict != nil {
				strict = *params.OutputObject.Strict
			}
			req.ResponseFormat = &apiResponseFormat{
				Type: "json_schema",
				JSONSchema: &apiJSONSchema{
					Name:   params.OutputObject.Name,
					Schema: schemaJSON,
					Strict: strict,
				},
			}
		}
	}

	// Apply tool choice from settings.
	if settings != nil && settings.ToolChoice != nil {
		tc := settings.ToolChoice
		switch {
		case tc.Mode == "none":
			req.ToolChoice = "none"
		case tc.Mode == "required":
			req.ToolChoice = "required"
		case tc.ToolName != "":
			req.ToolChoice = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": tc.ToolName,
				},
			}
		case tc.Mode == "auto":
			req.ToolChoice = "auto"
		}
	}

	// Convert messages.
	var apiMsgs []apiMessage

	for _, msg := range messages {
		switch m := msg.(type) {
		case core.ModelRequest:
			hasImage := false
			for _, part := range m.Parts {
				if _, ok := part.(core.ImagePart); ok {
					hasImage = true
					break
				}
			}

			if !hasImage {
				for _, part := range m.Parts {
					switch p := part.(type) {
					case core.SystemPromptPart:
						apiMsgs = append(apiMsgs, apiMessage{
							Role:    "system",
							Content: p.Content,
						})
					case core.UserPromptPart:
						apiMsgs = append(apiMsgs, apiMessage{
							Role:    "user",
							Content: p.Content,
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
						msg := apiMessage{
							Role:       "tool",
							ToolCallID: p.ToolCallID,
						}
						if len(p.Images) > 0 {
							msg.ContentParts = []apiContentPart{
								{Type: "text", Text: content},
							}
							for _, img := range p.Images {
								cp := apiContentPart{
									Type:     "image_url",
									ImageURL: &apiImageURL{URL: img.URL},
								}
								if img.Detail != "" {
									cp.ImageURL.Detail = img.Detail
								}
								msg.ContentParts = append(msg.ContentParts, cp)
							}
						} else {
							msg.Content = content
						}
						apiMsgs = append(apiMsgs, msg)
					case core.RetryPromptPart:
						if p.ToolCallID != "" {
							apiMsgs = append(apiMsgs, apiMessage{
								Role:       "tool",
								Content:    p.Content,
								ToolCallID: p.ToolCallID,
							})
						} else {
							apiMsgs = append(apiMsgs, apiMessage{
								Role:    "user",
								Content: p.Content,
							})
						}
					default:
						return nil, fmt.Errorf("openai provider: unsupported request part type %T", part)
					}
				}
				break
			}

			var userParts []apiContentPart
			flushUserParts := func() {
				if len(userParts) == 0 {
					return
				}
				if len(userParts) == 1 && userParts[0].Type == "text" {
					apiMsgs = append(apiMsgs, apiMessage{
						Role:    "user",
						Content: userParts[0].Text,
					})
				} else {
					cp := make([]apiContentPart, len(userParts))
					copy(cp, userParts)
					apiMsgs = append(apiMsgs, apiMessage{
						Role:         "user",
						ContentParts: cp,
					})
				}
				userParts = userParts[:0]
			}

			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.SystemPromptPart:
					flushUserParts()
					apiMsgs = append(apiMsgs, apiMessage{
						Role:    "system",
						Content: p.Content,
					})
				case core.UserPromptPart:
					userParts = append(userParts, apiContentPart{
						Type: "text",
						Text: p.Content,
					})
				case core.ImagePart:
					image := &apiImageURL{URL: p.URL}
					if p.Detail != "" {
						image.Detail = p.Detail
					}
					userParts = append(userParts, apiContentPart{
						Type:     "image_url",
						ImageURL: image,
					})
				case core.ToolReturnPart:
					flushUserParts()
					content := ""
					switch v := p.Content.(type) {
					case string:
						content = v
					default:
						b, _ := json.Marshal(v)
						content = string(b)
					}
					msg := apiMessage{
						Role:       "tool",
						ToolCallID: p.ToolCallID,
					}
					if len(p.Images) > 0 {
						msg.ContentParts = []apiContentPart{
							{Type: "text", Text: content},
						}
						for _, img := range p.Images {
							cp := apiContentPart{
								Type:     "image_url",
								ImageURL: &apiImageURL{URL: img.URL},
							}
							if img.Detail != "" {
								cp.ImageURL.Detail = img.Detail
							}
							msg.ContentParts = append(msg.ContentParts, cp)
						}
					} else {
						msg.Content = content
					}
					apiMsgs = append(apiMsgs, msg)
				case core.RetryPromptPart:
					if p.ToolCallID != "" {
						flushUserParts()
						apiMsgs = append(apiMsgs, apiMessage{
							Role:       "tool",
							Content:    p.Content,
							ToolCallID: p.ToolCallID,
						})
					} else {
						userParts = append(userParts, apiContentPart{
							Type: "text",
							Text: p.Content,
						})
					}
				default:
					return nil, fmt.Errorf("openai provider: unsupported request part type %T", part)
				}
			}
			flushUserParts()

		case core.ModelResponse:
			assistantMsg := apiMessage{Role: "assistant"}
			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.TextPart:
					assistantMsg.Content += p.Content
				case core.ThinkingPart:
					assistantMsg.ReasoningContent += p.Content
				case core.ToolCallPart:
					assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, apiToolCall{
						ID:   p.ToolCallID,
						Type: "function",
						Function: apiToolFunction{
							Name:      p.ToolName,
							Arguments: p.ArgsJSON,
						},
					})
				}
			}
			// Skip empty assistant messages (e.g., ThinkingPart-only responses
			// from Anthropic that are unsupported by OpenAI).
			if assistantMsg.Content != "" || len(assistantMsg.ToolCalls) > 0 {
				apiMsgs = append(apiMsgs, assistantMsg)
			}
		}
	}

	req.Messages = apiMsgs
	return req, nil
}

// parseResponse converts an OpenAI API response to a core.ModelResponse.
func parseResponse(resp *apiResponse, modelName string) *core.ModelResponse {
	var parts []core.ModelResponsePart

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]

		if choice.Message.ReasoningContent != "" {
			parts = append(parts, core.ThinkingPart{Content: choice.Message.ReasoningContent})
		}
		if choice.Message.Refusal != "" {
			parts = append(parts, core.TextPart{Content: choice.Message.Refusal})
		} else if choice.Message.Content != "" {
			parts = append(parts, core.TextPart{Content: choice.Message.Content})
		}

		for i, tc := range choice.Message.ToolCalls {
			argsJSON := tc.Function.Arguments
			if argsJSON == "" {
				argsJSON = "{}"
			}
			callID := tc.ID
			// OpenAI-compatible APIs (Ollama, LiteLLM) may return empty tool
			// call IDs. Generate synthetic IDs so tool results can be matched
			// back to their calls in the conversation history.
			if callID == "" {
				callID = fmt.Sprintf("call_%d", i)
			}
			parts = append(parts, core.ToolCallPart{
				ToolName:   tc.Function.Name,
				ArgsJSON:   argsJSON,
				ToolCallID: callID,
			})
		}
	}

	return &core.ModelResponse{
		Parts:        parts,
		Usage:        mapUsage(resp.Usage),
		ModelName:    modelName,
		FinishReason: mapFinishReason(resp),
		Timestamp:    time.Now(),
	}
}

// mapFinishReason maps OpenAI finish reasons to gollem FinishReasons.
func mapFinishReason(resp *apiResponse) core.FinishReason {
	if len(resp.Choices) == 0 {
		return core.FinishReasonStop
	}
	switch resp.Choices[0].FinishReason {
	case "stop":
		return core.FinishReasonStop
	case "length":
		return core.FinishReasonLength
	case "tool_calls":
		return core.FinishReasonToolCall
	case "content_filter":
		return core.FinishReasonContentFilter
	default:
		return core.FinishReasonStop
	}
}

// mapUsage converts OpenAI usage to gollem Usage.
func mapUsage(u apiUsage) core.Usage {
	usage := core.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
	}
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		usage.CacheReadTokens = u.PromptTokensDetails.CachedTokens
	}
	if u.CompletionTokensDetails != nil && u.CompletionTokensDetails.ReasoningTokens > 0 {
		usage.Details = map[string]int{
			"reasoning_tokens": u.CompletionTokensDetails.ReasoningTokens,
		}
	}
	return usage
}

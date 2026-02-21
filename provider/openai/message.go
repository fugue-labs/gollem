package openai

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// --- API request types ---

type apiRequest struct {
	Model            string             `json:"model"`
	Messages         []apiMessage       `json:"messages"`
	Tools            []apiToolDef       `json:"tools,omitempty"`
	ToolChoice       any                `json:"tool_choice,omitempty"`
	Stream           bool               `json:"stream,omitempty"`
	StreamOptions    *apiStreamOptions  `json:"stream_options,omitempty"`
	MaxTokens        int                `json:"max_completion_tokens,omitempty"`
	Temperature      *float64           `json:"temperature,omitempty"`
	TopP             *float64           `json:"top_p,omitempty"`
	ResponseFormat   *apiResponseFormat `json:"response_format,omitempty"`
	ReasoningEffort  *string            `json:"reasoning_effort,omitempty"`
}

type apiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type apiMessage struct {
	Role       string          `json:"role"` // system, user, assistant, tool
	Content    string          `json:"content,omitempty"`
	ToolCalls  []apiToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
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
	Type     string          `json:"type"` // "function"
	Function apiToolDefFunc  `json:"function"`
}

type apiToolDefFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict,omitempty"`
}

type apiResponseFormat struct {
	Type       string          `json:"type"` // "json_schema" or "json_object"
	JSONSchema *apiJSONSchema  `json:"json_schema,omitempty"`
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
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []apiToolCall `json:"tool_calls,omitempty"`
	Refusal   string        `json:"refusal,omitempty"`
}

type apiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
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
			schemaJSON, err := json.Marshal(td.ParametersSchema)
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
			schemaJSON, err := json.Marshal(params.OutputObject.JSONSchema)
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
					apiMsgs = append(apiMsgs, apiMessage{
						Role:       "tool",
						Content:    content,
						ToolCallID: p.ToolCallID,
					})
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

		case core.ModelResponse:
			assistantMsg := apiMessage{Role: "assistant"}
			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.TextPart:
					assistantMsg.Content += p.Content
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
			apiMsgs = append(apiMsgs, assistantMsg)
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

		if choice.Message.Refusal != "" {
			parts = append(parts, core.TextPart{Content: choice.Message.Refusal})
		} else if choice.Message.Content != "" {
			parts = append(parts, core.TextPart{Content: choice.Message.Content})
		}

		for _, tc := range choice.Message.ToolCalls {
			argsJSON := tc.Function.Arguments
			if argsJSON == "" {
				argsJSON = "{}"
			}
			parts = append(parts, core.ToolCallPart{
				ToolName:   tc.Function.Name,
				ArgsJSON:   argsJSON,
				ToolCallID: tc.ID,
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
	return core.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
	}
}

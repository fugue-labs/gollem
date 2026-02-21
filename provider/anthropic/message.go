package anthropic

import (
	"encoding/json"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// --- API request types ---

type apiRequest struct {
	Model       string           `json:"model"`
	MaxTokens   int              `json:"max_tokens"`
	System      []apiSystemBlock `json:"system,omitempty"`
	Messages    []apiMessage     `json:"messages"`
	Tools       []apiTool        `json:"tools,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	TopP        *float64         `json:"top_p,omitempty"`
	Thinking    *apiThinking     `json:"thinking,omitempty"`
}

type apiThinking struct {
	Type         string `json:"type"`           // "enabled" or "disabled"
	BudgetTokens int    `json:"budget_tokens"`  // Max tokens for thinking
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
	Type string `json:"type"`
	// text block
	Text string `json:"text,omitempty"`
	// tool_use block
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result block
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	// thinking block
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type apiTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// --- API response types ---

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

// buildRequest converts gollem messages into an Anthropic API request.
func buildRequest(messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters, model string, defaultMaxTokens int, stream bool) (*apiRequest, error) {
	req := &apiRequest{
		Model:     model,
		MaxTokens: defaultMaxTokens,
		Stream:    stream,
	}

	if settings != nil {
		if settings.MaxTokens != nil {
			req.MaxTokens = *settings.MaxTokens
		}
		req.Temperature = settings.Temperature
		req.TopP = settings.TopP

		// Enable extended thinking if ThinkingBudget is set.
		if settings.ThinkingBudget != nil && *settings.ThinkingBudget > 0 {
			req.Thinking = &apiThinking{
				Type:         "enabled",
				BudgetTokens: *settings.ThinkingBudget,
			}
			// Anthropic requires temperature to be omitted when thinking is enabled.
			req.Temperature = nil
		}
	}

	// Convert tool definitions.
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

	// Convert messages.
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
					userBlocks = append(userBlocks, apiContentBlock{
						Type:      "tool_result",
						ToolUseID: p.ToolCallID,
						Content:   content,
					})
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
				}
			}
			if len(userBlocks) > 0 {
				apiMsgs = append(apiMsgs, apiMessage{
					Role:    "user",
					Content: userBlocks,
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
			if len(assistantBlocks) > 0 {
				apiMsgs = append(apiMsgs, apiMessage{
					Role:    "assistant",
					Content: assistantBlocks,
				})
			}
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
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
	}
}

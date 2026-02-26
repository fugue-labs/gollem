package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

type responsesRequest struct {
	Model                string              `json:"model"`
	Store                *bool               `json:"store,omitempty"`
	Input                []map[string]any    `json:"input"`
	PreviousResponseID   string              `json:"previous_response_id,omitempty"`
	Tools                []responsesToolDef  `json:"tools,omitempty"`
	ToolChoice           any                 `json:"tool_choice,omitempty"`
	ServiceTier          string              `json:"service_tier,omitempty"`
	PromptCacheKey       string              `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string              `json:"prompt_cache_retention,omitempty"`
	MaxOutputTokens      int                 `json:"max_output_tokens,omitempty"`
	Temperature          *float64            `json:"temperature,omitempty"`
	TopP                 *float64            `json:"top_p,omitempty"`
	Reasoning            *responsesReasoning `json:"reasoning,omitempty"`
	Text                 *responsesText      `json:"text,omitempty"`
}

type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type responsesText struct {
	Format *responsesTextFormat `json:"format,omitempty"`
}

type responsesTextFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Strict bool            `json:"strict,omitempty"`
}

type responsesToolDef struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

type responsesAPIResponse struct {
	ID                string                      `json:"id"`
	Model             string                      `json:"model"`
	Status            string                      `json:"status,omitempty"`
	IncompleteDetails *responsesIncompleteDetails `json:"incomplete_details,omitempty"`
	Output            []responsesOutputItem       `json:"output"`
	Usage             responsesUsage              `json:"usage"`
}

type responsesIncompleteDetails struct {
	Reason string `json:"reason,omitempty"`
}

type responsesOutputItem struct {
	Type      string                 `json:"type"`
	Role      string                 `json:"role,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Refusal   string                 `json:"refusal,omitempty"`
	Content   []responsesContentItem `json:"content,omitempty"`
}

type responsesContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesUsage struct {
	InputTokens         int                           `json:"input_tokens"`
	OutputTokens        int                           `json:"output_tokens"`
	InputTokensDetails  *responsesInputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *responsesOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

type responsesInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type responsesOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

func (p *Provider) requestViaResponses(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	req, err := buildResponsesRequest(messages, settings, params, p.model, p.maxTokens)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to build responses request: %w", err)
	}
	req.PromptCacheKey = p.promptCacheKey
	req.PromptCacheRetention = p.promptCacheRetention
	req.ServiceTier = p.serviceTier
	return p.requestViaResponsesWithReq(ctx, req)
}

func (p *Provider) requestViaResponsesWithReq(ctx context.Context, req *responsesRequest) (*core.ModelResponse, error) {
	if p.shouldUseResponsesWebSocket() {
		// Keep websocket continuations strictly in-memory on the active socket,
		// aligned with WebSocket mode guidance and ZDR/store=false compatibility.
		origStore := cloneBoolPtr(req.Store)
		if req.Store == nil {
			storeFalse := false
			req.Store = &storeFalse
		}
		resp, wsErr := p.requestViaResponsesWebSocket(ctx, req)
		if wsErr == nil {
			return resp, nil
		}
		if !p.wsHTTPFallback {
			return nil, wsErr
		}
		// Restore caller intent for HTTP fallback; websocket-specific store=false
		// coercion should not leak into the fallback transport.
		req.Store = cloneBoolPtr(origStore)
	}

	return p.requestViaResponsesHTTP(ctx, req)
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func (p *Provider) requestViaResponsesHTTP(ctx context.Context, req *responsesRequest) (*core.ModelResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to marshal responses request: %w", err)
	}

	resp, err := p.doRequest(ctx, responsesEndpoint, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var apiResp responsesAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("openai: failed to decode responses API response: %w", err)
	}

	return parseResponsesResponse(&apiResp, p.model), nil
}

func buildResponsesRequest(messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters, model string, defaultMaxTokens int) (*responsesRequest, error) {
	req := &responsesRequest{
		Model:           model,
		MaxOutputTokens: defaultMaxTokens,
	}

	if settings != nil {
		if settings.MaxTokens != nil {
			req.MaxOutputTokens = *settings.MaxTokens
		}
		// Codex-style models exposed through Responses API reject sampling
		// parameters like temperature/top_p.
		if !modelNeedsResponsesAPI(model) {
			req.Temperature = settings.Temperature
			req.TopP = settings.TopP
		}
		if settings.ReasoningEffort != nil {
			req.Reasoning = &responsesReasoning{Effort: *settings.ReasoningEffort}
		}
	}

	if params != nil {
		allTools := params.AllToolDefs()
		for _, td := range allTools {
			schemaJSON, err := marshalOpenAISchema(td.ParametersSchema)
			if err != nil {
				return nil, err
			}
			req.Tools = append(req.Tools, responsesToolDef{
				Type:        "function",
				Name:        td.Name,
				Description: td.Description,
				Parameters:  schemaJSON,
				Strict:      td.Strict,
			})
		}

		if params.OutputMode == core.OutputModeNative && params.OutputObject != nil {
			schemaJSON, err := marshalOpenAISchema(params.OutputObject.JSONSchema)
			if err != nil {
				return nil, err
			}
			strict := true
			if params.OutputObject.Strict != nil {
				strict = *params.OutputObject.Strict
			}
			req.Text = &responsesText{
				Format: &responsesTextFormat{
					Type:   "json_schema",
					Name:   params.OutputObject.Name,
					Schema: schemaJSON,
					Strict: strict,
				},
			}
		}
	}

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
				"name": tc.ToolName,
			}
		case tc.Mode == "auto":
			req.ToolChoice = "auto"
		}
	}

	input, err := convertMessagesToResponsesInput(messages)
	if err != nil {
		return nil, err
	}
	req.Input = input
	return req, nil
}

func convertMessagesToResponsesInput(messages []core.ModelMessage) ([]map[string]any, error) {
	var input []map[string]any
	assistantCallIndex := 0

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
						input = append(input, responsesMessage("system", p.Content))
					case core.UserPromptPart:
						input = append(input, responsesMessage("user", p.Content))
					case core.ToolReturnPart:
						content := stringifyToolContent(p.Content)
						input = append(input, map[string]any{
							"type":    "function_call_output",
							"call_id": p.ToolCallID,
							"output":  content,
						})
					case core.RetryPromptPart:
						if p.ToolCallID != "" {
							input = append(input, map[string]any{
								"type":    "function_call_output",
								"call_id": p.ToolCallID,
								"output":  p.Content,
							})
						} else {
							input = append(input, responsesMessage("user", p.Content))
						}
					default:
						return nil, fmt.Errorf("openai responses provider: unsupported request part type %T", part)
					}
				}
				break
			}

			contentType := func(role string) string {
				if role == "assistant" {
					return "output_text"
				}
				return "input_text"
			}

			var userContent []map[string]any
			flushUser := func() {
				if len(userContent) == 0 {
					return
				}
				items := make([]map[string]any, len(userContent))
				copy(items, userContent)
				input = append(input, map[string]any{
					"type":    "message",
					"role":    "user",
					"content": items,
				})
				userContent = userContent[:0]
			}

			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.SystemPromptPart:
					flushUser()
					input = append(input, responsesMessage("system", p.Content))
				case core.UserPromptPart:
					userContent = append(userContent, map[string]any{
						"type": contentType("user"),
						"text": p.Content,
					})
				case core.ImagePart:
					item := map[string]any{
						"type":      "input_image",
						"image_url": p.URL,
					}
					if p.Detail != "" {
						item["detail"] = p.Detail
					}
					userContent = append(userContent, item)
				case core.ToolReturnPart:
					flushUser()
					content := stringifyToolContent(p.Content)
					input = append(input, map[string]any{
						"type":    "function_call_output",
						"call_id": p.ToolCallID,
						"output":  content,
					})
				case core.RetryPromptPart:
					if p.ToolCallID != "" {
						flushUser()
						input = append(input, map[string]any{
							"type":    "function_call_output",
							"call_id": p.ToolCallID,
							"output":  p.Content,
						})
					} else {
						userContent = append(userContent, map[string]any{
							"type": contentType("user"),
							"text": p.Content,
						})
					}
				default:
					return nil, fmt.Errorf("openai responses provider: unsupported request part type %T", part)
				}
			}
			flushUser()

		case core.ModelResponse:
			var assistantText strings.Builder
			flushAssistantText := func() {
				if assistantText.Len() == 0 {
					return
				}
				input = append(input, responsesMessage("assistant", assistantText.String()))
				assistantText.Reset()
			}

			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.TextPart:
					assistantText.WriteString(p.Content)
				case core.ToolCallPart:
					flushAssistantText()
					callID := p.ToolCallID
					if callID == "" {
						callID = fmt.Sprintf("call_%d", assistantCallIndex)
						assistantCallIndex++
					}
					argsJSON := p.ArgsJSON
					if argsJSON == "" {
						argsJSON = "{}"
					}
					input = append(input, map[string]any{
						"type":      "function_call",
						"call_id":   callID,
						"name":      p.ToolName,
						"arguments": argsJSON,
					})
				}
			}
			flushAssistantText()
		}
	}

	return input, nil
}

func responsesMessage(role, text string) map[string]any {
	contentType := "input_text"
	if role == "assistant" {
		contentType = "output_text"
	}
	return map[string]any{
		"type": "message",
		"role": role,
		"content": []map[string]string{
			{
				"type": contentType,
				"text": text,
			},
		},
	}
}

func stringifyToolContent(v any) string {
	switch c := v.(type) {
	case string:
		return c
	default:
		b, _ := json.Marshal(c)
		return string(b)
	}
}

func parseResponsesResponse(resp *responsesAPIResponse, modelName string) *core.ModelResponse {
	var parts []core.ModelResponsePart
	hasToolCalls := false

	for i, item := range resp.Output {
		switch item.Type {
		case "message":
			text := parseResponsesMessageText(item)
			if text != "" {
				parts = append(parts, core.TextPart{Content: text})
			} else if item.Refusal != "" {
				parts = append(parts, core.TextPart{Content: item.Refusal})
			}
		case "function_call":
			argsJSON := item.Arguments
			if argsJSON == "" {
				argsJSON = "{}"
			}
			callID := item.CallID
			if callID == "" {
				callID = fmt.Sprintf("call_%d", i)
			}
			parts = append(parts, core.ToolCallPart{
				ToolName:   item.Name,
				ArgsJSON:   argsJSON,
				ToolCallID: callID,
			})
			hasToolCalls = true
		}
	}

	return &core.ModelResponse{
		Parts:        parts,
		Usage:        mapResponsesUsage(resp.Usage),
		ModelName:    modelName,
		FinishReason: mapResponsesFinishReason(resp, hasToolCalls),
		Timestamp:    time.Now(),
	}
}

func parseResponsesMessageText(item responsesOutputItem) string {
	var text strings.Builder
	for _, content := range item.Content {
		switch content.Type {
		case "output_text", "text", "input_text":
			text.WriteString(content.Text)
		}
	}
	return text.String()
}

func mapResponsesFinishReason(resp *responsesAPIResponse, hasToolCalls bool) core.FinishReason {
	if hasToolCalls {
		return core.FinishReasonToolCall
	}
	if resp.IncompleteDetails != nil && strings.Contains(resp.IncompleteDetails.Reason, "max_output_tokens") {
		return core.FinishReasonLength
	}
	return core.FinishReasonStop
}

func mapResponsesUsage(u responsesUsage) core.Usage {
	usage := core.Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
	}
	if u.InputTokensDetails != nil && u.InputTokensDetails.CachedTokens > 0 {
		usage.CacheReadTokens = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil && u.OutputTokensDetails.ReasoningTokens > 0 {
		usage.Details = map[string]int{
			"reasoning_tokens": u.OutputTokensDetails.ReasoningTokens,
		}
	}
	return usage
}

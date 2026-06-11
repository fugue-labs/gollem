package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// --- API request types ---

type apiRequest struct {
	Model         string           `json:"model"`
	MaxTokens     int              `json:"max_tokens"`
	System        []apiSystemBlock `json:"system,omitempty"`
	Messages      []apiMessage     `json:"messages"`
	Tools         []any            `json:"tools,omitempty"` // apiTool or apiBuiltinTool
	ToolChoice    *apiToolChoice   `json:"tool_choice,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
	Temperature   *float64         `json:"temperature,omitempty"`
	TopP          *float64         `json:"top_p,omitempty"`
	StopSequences []string         `json:"stop_sequences,omitempty"`
	Thinking      *apiThinking     `json:"thinking,omitempty"`
	OutputConfig  *apiOutputConfig `json:"output_config,omitempty"`
}

type apiToolChoice struct {
	Type string `json:"type"`           // "auto", "any", "tool"
	Name string `json:"name,omitempty"` // required when type is "tool"
}

// apiThinking controls extended thinking. Two modes:
//   - {type: "enabled", budget_tokens: N} — manual extended thinking. Rejected
//     by Opus 4.7+ (4.7, 4.8, Fable); deprecated on Opus 4.6 / Sonnet 4.6.
//   - {type: "adaptive"} — adaptive thinking. The only mode on Opus 4.7+;
//     recommended on 4.6 models. BudgetTokens is omitted on the wire.
type apiThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// apiOutputConfig is the nested object carrying the `effort` parameter.
// Effort values: "low", "medium", "high", "xhigh" (Opus 4.7+ only),
// "max" (Opus 4.6+/Sonnet 4.6+).
type apiOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type apiCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type apiSystemBlock struct {
	Type         string           `json:"type"`
	Text         string           `json:"text"`
	CacheControl *apiCacheControl `json:"cache_control,omitempty"`
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
	// image / document block
	Source *apiSource `json:"source,omitempty"`
	Title  string     `json:"title,omitempty"`
	// thinking block
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// contentBlocks, when non-nil, represents a tool_result with
	// structured multimodal content (e.g., text + images). When set,
	// MarshalJSON emits `content` as an array of blocks rather than a
	// string. Unexported to keep the common string-Content path intact.
	contentBlocks []apiContentBlock

	// rawOverride, when non-nil, replaces the entire struct during JSON
	// marshaling. Used to round-trip opaque provider content blocks
	// (server_tool_use, tool_reference, etc.) that don't fit the typed
	// fields above. The block is emitted as-is without interpretation.
	rawOverride json.RawMessage `json:"-"`
}

// apiSource is the `source` object embedded in image/document blocks.
// Two forms: {type: "base64", media_type, data} or {type: "url", url}.
type apiSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// MarshalJSON emits rawOverride verbatim when set. When contentBlocks is
// non-nil, emits a tool_result shape where `content` is an array of blocks.
// Otherwise falls back to standard struct marshaling.
func (b apiContentBlock) MarshalJSON() ([]byte, error) {
	if b.rawOverride != nil {
		return b.rawOverride, nil
	}
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
	Name         string           `json:"name"`
	Description  string           `json:"description,omitempty"`
	InputSchema  json.RawMessage  `json:"input_schema"`
	CacheControl *apiCacheControl `json:"cache_control,omitempty"`
	DeferLoading bool             `json:"defer_loading,omitempty"`
}

// apiBuiltinTool represents an Anthropic server-side built-in tool like
// tool_search_tool_regex_20251119. Different shape from apiTool (has type,
// no input_schema). Both coexist in apiRequest.Tools via []any.
type apiBuiltinTool struct {
	Type string `json:"type"` // e.g., "tool_search_tool_regex_20251119"
	Name string `json:"name"` // e.g., "tool_search_tool_regex"
}

// --- API response types ---

type apiResponse struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Content    []json.RawMessage `json:"content"` // parsed per-block in parseResponse
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
// If enableCache is true, cache_control markers are added to the last system
// block and last tool definition to enable Anthropic's prompt caching.
func buildRequest(messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters, model string, defaultMaxTokens int, stream bool, enableCache bool, disableToolSearch bool) (*apiRequest, error) {
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
		req.StopSequences = settings.StopSequences

		// Extended thinking. Two modes, mutually exclusive:
		//   - adaptive: the model decides when/how much to think. The only
		//     mode on Opus 4.7+; recommended on 4.6.
		//   - manual budget: legacy {type: "enabled", budget_tokens}.
		//     Rejected by Opus 4.7+; deprecated on 4.6.
		// Fail fast with a pointer to the correct option rather than
		// letting the API return a generic 400.
		adaptive := settings.AdaptiveThinking != nil && *settings.AdaptiveThinking
		manual := settings.ThinkingBudget != nil && *settings.ThinkingBudget > 0
		if adaptive && manual {
			return nil, errors.New(
				"anthropic: AdaptiveThinking and ThinkingBudget are mutually exclusive; pick one thinking mode",
			)
		}
		if adaptive {
			if !supportsAdaptiveThinking(model) {
				return nil, fmt.Errorf(
					"anthropic: model %q does not support adaptive thinking; use a Claude 4.6+ model, or ThinkingBudget for manual thinking on older models",
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
					"anthropic: model %q does not support manual thinking; set AdaptiveThinking or use WithReasoningEffort(\"high\"|\"xhigh\"|\"max\") instead",
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

		// Effort parameter. Gated per model: xhigh is Opus-4.7/Mythos-only;
		// max requires 4.6+; low/medium/high require any effort-capable model
		// (Mythos/4.7/4.6/4.5 opus+sonnet). Haiku 4.5 and 3.x never support it.
		if settings.ReasoningEffort != nil && *settings.ReasoningEffort != "" {
			effort := *settings.ReasoningEffort
			if !supportsEffortValue(model, effort) {
				return nil, fmt.Errorf(
					"anthropic: model %q does not support reasoning effort %q",
					model, effort,
				)
			}
			req.OutputConfig = &apiOutputConfig{Effort: effort}
		}
	}

	// Convert tool definitions. If any tool has DeferLoading=true and the
	// model supports tool search, auto-inject the built-in tool_search
	// regex tool and emit defer_loading on the wire.
	if params != nil {
		allTools := params.AllToolDefs()
		modelSupports := supportsToolSearch(model)

		// Check if any tool requests deferred loading.
		anyDeferred := false
		for _, td := range allTools {
			if td.DeferLoading {
				anyDeferred = true
				break
			}
		}

		// Auto-inject the built-in tool_search tool when applicable.
		if anyDeferred && modelSupports && !disableToolSearch {
			req.Tools = append(req.Tools, apiBuiltinTool{
				Type: toolSearchToolRegexType,
				Name: toolSearchToolRegexName,
			})
		}

		for _, td := range allTools {
			schemaJSON, err := json.Marshal(td.ParametersSchema)
			if err != nil {
				return nil, err
			}
			at := apiTool{
				Name:        td.Name,
				Description: td.Description,
				InputSchema: schemaJSON,
			}
			// Only emit defer_loading on models that support it. Silent
			// degrade on unsupported models: the flag is preserved in core
			// but never reaches the wire, so the tool ships inline.
			if td.DeferLoading && modelSupports {
				at.DeferLoading = true
			}
			req.Tools = append(req.Tools, at)
		}
	}

	// Apply tool choice from settings.
	if settings != nil && settings.ToolChoice != nil {
		tc := settings.ToolChoice
		switch {
		case tc.Mode == "none":
			// Anthropic doesn't have a "none" tool_choice type;
			// the way to prevent tool use is to omit tools entirely.
			req.Tools = nil
		case tc.Mode == "required":
			req.ToolChoice = &apiToolChoice{Type: "any"}
		case tc.ToolName != "":
			req.ToolChoice = &apiToolChoice{Type: "tool", Name: tc.ToolName}
		case tc.Mode == "auto":
			req.ToolChoice = &apiToolChoice{Type: "auto"}
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
					if len(p.Images) > 0 {
						blocks := make([]apiContentBlock, 0, len(p.Images)+1)
						if content != "" {
							blocks = append(blocks, apiContentBlock{Type: "text", Text: content})
						}
						for _, img := range p.Images {
							src, err := toAnthropicSource(img.URL, img.MIMEType)
							if err != nil {
								return nil, fmt.Errorf("anthropic: tool_result image: %w", err)
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
						return nil, fmt.Errorf("anthropic: image: %w", err)
					}
					userBlocks = append(userBlocks, apiContentBlock{
						Type:   "image",
						Source: src,
					})
				case core.DocumentPart:
					src, err := toAnthropicSource(p.URL, p.MIMEType)
					if err != nil {
						return nil, fmt.Errorf("anthropic: document: %w", err)
					}
					userBlocks = append(userBlocks, apiContentBlock{
						Type:   "document",
						Source: src,
						Title:  p.Title,
					})
				case core.AudioPart:
					return nil, errors.New("anthropic: audio input is not supported by the Messages API")
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
					return nil, fmt.Errorf("anthropic provider: unsupported request part type %T", part)
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
				// Anthropic's alternation requirement and cause 400 errors.
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
				case core.ProviderMetadataPart:
					if p.Provider == "anthropic" {
						assistantBlocks = append(assistantBlocks, apiContentBlock{
							rawOverride: p.Payload,
						})
					}
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

	// Apply cache_control markers to the last system block and last
	// non-deferred user tool. This enables Anthropic's prompt caching for
	// the stable prefix (system instructions + tool definitions).
	//
	// Walk backwards through Tools skipping:
	//   - apiBuiltinTool entries (tool_search_tool_regex at position 0)
	//   - apiTool entries with DeferLoading=true (Anthropic rejects
	//     cache_control + defer_loading on the same tool)
	if enableCache {
		ephemeral := &apiCacheControl{Type: "ephemeral"}
		if len(req.System) > 0 {
			req.System[len(req.System)-1].CacheControl = ephemeral
		}
		for i := len(req.Tools) - 1; i >= 0; i-- {
			if t, ok := req.Tools[i].(apiTool); ok && !t.DeferLoading {
				t.CacheControl = ephemeral
				req.Tools[i] = t
				break
			}
		}
	}

	return req, nil
}

// parseResponse converts an Anthropic API response to a core.ModelResponse.
// Known content block types (text, tool_use, thinking) are parsed into typed
// gollem parts. Unknown types (server_tool_use, tool_search_tool_result,
// tool_reference, and any future server tool types) are preserved as
// ProviderMetadataPart for lossless round-tripping in conversation history.
func parseResponse(resp *apiResponse, modelName string) *core.ModelResponse {
	var parts []core.ModelResponsePart

	for _, raw := range resp.Content {
		var typeOnly struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &typeOnly); err != nil {
			continue
		}

		switch typeOnly.Type {
		case "text":
			var block apiContentBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				continue
			}
			parts = append(parts, core.TextPart{Content: block.Text})

		case "tool_use":
			var block apiContentBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				continue
			}
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
			var block apiContentBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				continue
			}
			parts = append(parts, core.ThinkingPart{
				Content:   block.Thinking,
				Signature: block.Signature,
			})

		default:
			// Preserve ALL unknown content blocks for round-tripping.
			// The Anthropic API contract requires assistant content blocks
			// to be sent back in conversation history exactly as received.
			// This covers server_tool_use, tool_search_tool_result,
			// tool_reference, and any future server tool types.
			parts = append(parts, core.ProviderMetadataPart{
				Provider: "anthropic",
				Kind:     typeOnly.Type,
				Payload:  append(json.RawMessage(nil), raw...),
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

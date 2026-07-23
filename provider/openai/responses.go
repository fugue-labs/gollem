package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

type responsesRequest struct {
	Model                string              `json:"model"`
	Instructions         string              `json:"instructions,omitempty"`
	Store                *bool               `json:"store,omitempty"`
	Stream               *bool               `json:"stream,omitempty"`
	Input                []map[string]any    `json:"input"`
	PreviousResponseID   string              `json:"previous_response_id,omitempty"`
	Tools                []any               `json:"tools,omitempty"` // responsesToolDef, responsesNamespace, or tool_search built-in
	ToolChoice           any                 `json:"tool_choice,omitempty"`
	ParallelToolCalls    *bool               `json:"parallel_tool_calls,omitempty"`
	ServiceTier          string              `json:"service_tier,omitempty"`
	PromptCacheKey       string              `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string              `json:"prompt_cache_retention,omitempty"`
	PromptCacheOptions   *promptCacheOptions `json:"prompt_cache_options,omitempty"`
	MaxOutputTokens      int                 `json:"max_output_tokens,omitempty"`
	Temperature          *float64            `json:"temperature,omitempty"`
	TopP                 *float64            `json:"top_p,omitempty"`
	Reasoning            *responsesReasoning `json:"reasoning,omitempty"`
	Text                 *responsesText      `json:"text,omitempty"`
}

type promptCacheOptions struct {
	TTL string `json:"ttl,omitempty"`
}

type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"` // "auto", "concise", "detailed"
	Context string `json:"context,omitempty"`
}

type responsesText struct {
	Format    *responsesTextFormat `json:"format,omitempty"`
	Verbosity string               `json:"verbosity,omitempty"` // "low", "medium", "high"
}

type responsesTextFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Strict bool            `json:"strict,omitempty"`
}

type responsesToolDef struct {
	Type         string          `json:"type"`
	Name         string          `json:"name,omitempty"` // omitempty: tool_search built-in has no name
	Description  string          `json:"description,omitempty"`
	Parameters   json.RawMessage `json:"parameters,omitempty"`
	Strict       *bool           `json:"strict,omitempty"`
	DeferLoading bool            `json:"defer_loading,omitempty"`
}

// responsesNamespace wraps related tools for OpenAI's namespace grouping.
// Tools with the same Namespace are grouped into a namespace object for
// better tool_search token efficiency. The namespace description is
// always visible to the model; only deferred function details are hidden.
type responsesNamespace struct {
	Type        string             `json:"type"` // "namespace"
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Tools       []responsesToolDef `json:"tools"`
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
	Namespace string                 `json:"namespace,omitempty"` // populated on function_call when tool is in a namespace
	Arguments string                 `json:"arguments,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Refusal   string                 `json:"refusal,omitempty"`
	Content   []responsesContentItem `json:"content,omitempty"`
	Summary   []responsesSummaryItem `json:"summary,omitempty"` // populated on reasoning items (o-series / GPT-5)
}

type responsesContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// responsesSummaryItem holds one summary_text chunk of a reasoning item.
// OpenAI emits them in-order; concatenate to reconstruct the full summary.
type responsesSummaryItem struct {
	Type string `json:"type"` // "summary_text"
	Text string `json:"text,omitempty"`
}

type responsesUsage struct {
	InputTokens         int                           `json:"input_tokens"`
	OutputTokens        int                           `json:"output_tokens"`
	InputTokensDetails  *responsesInputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *responsesOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

type responsesInputTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

type responsesOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

func (p *Provider) requestViaResponses(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	req, err := buildResponsesRequest(messages, settings, params, p.model, p.maxTokens, p.disableToolSearch)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to build responses request: %w", err)
	}
	p.applyResponsesEndpointSettings(req)
	return p.requestViaResponsesWithReq(ctx, req)
}

// applyResponsesEndpointSettings configures endpoint-specific fields on the
// request (caching, service tier, reasoning, verbosity). Shared by both the
// synchronous and streaming request paths.
func (p *Provider) applyResponsesEndpointSettings(req *responsesRequest) {
	if p.isChatGPTEndpoint() {
		// ChatGPT backend supports prefix-based caching via prompt_cache_key.
		req.PromptCacheKey = p.promptCacheKey
		p.applyChatGPTRequirements(req)
	} else if p.isOpenAIEndpoint() {
		req.PromptCacheKey = p.promptCacheKey
		if isGPT56Model(req.Model) {
			// GPT-5.6 replaces the legacy retention policy with a minimum
			// cache lifetime. 30m is currently the only supported TTL.
			req.PromptCacheRetention = ""
			req.PromptCacheOptions = &promptCacheOptions{TTL: "30m"}
		} else {
			req.PromptCacheRetention = p.promptCacheRetention
			req.PromptCacheOptions = nil
		}
		req.ServiceTier = p.serviceTier
		if p.reasoningSummary != "" && req.Reasoning != nil {
			req.Reasoning.Summary = p.reasoningSummary
		}
		if p.textVerbosity != "" {
			if req.Text == nil {
				req.Text = &responsesText{}
			}
			req.Text.Verbosity = p.textVerbosity
		}
	} else {
		// Non-OpenAI endpoints (xAI, etc.) don't support reasoning effort
		// or prompt_cache_key, but do support prompt_cache_retention.
		//
		// Exception: ChatGPT auth with a custom/proxy URL still gets caching
		// since the backend supports it regardless of proxy routing.
		if p.hasChatGPTAuth() {
			req.PromptCacheKey = p.promptCacheKey
		}
		req.Reasoning = nil
		req.PromptCacheRetention = p.promptCacheRetention
	}
}

// requestStreamViaResponses builds a Responses API request with streaming
// enabled and returns a StreamedResponse that yields events as they arrive.
//
// This always uses HTTP (not WebSocket) because the WebSocket transport
// doesn't expose a streaming interface — it buffers the full response
// internally. The non-streaming Request() path continues to use WebSocket
// when configured; RequestStream() falls back to HTTP SSE which provides
// true incremental event delivery.
func (p *Provider) requestStreamViaResponses(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	req, err := buildResponsesRequest(messages, settings, params, p.model, p.maxTokens, p.disableToolSearch)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to build responses request: %w", err)
	}

	// Force streaming.
	streamTrue := true
	req.Stream = &streamTrue

	p.applyResponsesEndpointSettings(req)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to marshal responses request: %w", err)
	}

	resp, err := p.doRequest(ctx, p.responsesEP(), body)
	if err != nil {
		return nil, err
	}

	// If the server returned JSON instead of SSE (e.g., it doesn't support
	// streaming or ignored the stream=true flag), parse as a complete
	// response and wrap it so callers always get a valid StreamedResponse.
	//
	// Exception: the ChatGPT backend (chatgpt.com) always returns SSE when
	// stream=true is set, but may not include "text/event-stream" in the
	// Content-Type header. For ChatGPT endpoints, always parse as SSE to
	// avoid attempting JSON decode on SSE data (which fails with "invalid
	// character" errors because SSE lines start with "event:" or "data:").
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") && !p.isChatGPTEndpoint() {
		defer resp.Body.Close()
		var apiResp responsesAPIResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return nil, fmt.Errorf("openai: failed to decode responses API response: %w", err)
		}
		bound, bindErr := p.parseBoundResponsesResponse(&apiResp)
		if bindErr != nil {
			return nil, bindErr
		}
		return newPrebuiltResponsesStream(bound), nil
	}

	return newBoundResponsesStreamedResponse(resp.Body, p.model, p.resolveResponseModel), nil
}

// applyChatGPTRequirements modifies a request for the ChatGPT backend:
// applies the Codex Responses request shape and forces store=false, stream=true.
func (p *Provider) applyChatGPTRequirements(req *responsesRequest) {
	var instructions []string
	var filtered []map[string]any
	for _, item := range req.Input {
		if role, _ := item["role"].(string); role == "system" {
			if text := extractTextContent(item["content"]); text != "" {
				instructions = append(instructions, text)
			}
			continue
		}
		filtered = append(filtered, item)
	}

	if isGPT56Model(req.Model) {
		// Responses Lite carries tools and developer instructions as input
		// items instead of top-level fields. This is the request contract used
		// by Codex 0.144+ for the GPT-5.6 family.
		tools := req.Tools
		if tools == nil {
			tools = []any{}
		}
		prefix := []map[string]any{{
			"type":  "additional_tools",
			"role":  "developer",
			"tools": tools,
		}}
		if len(instructions) > 0 {
			prefix = append(prefix, responsesMessage("developer", strings.Join(instructions, "\n\n")))
		}
		req.Input = append(prefix, filtered...)
		req.Instructions = ""
		req.Tools = nil
		parallelToolCalls := false
		req.ParallelToolCalls = &parallelToolCalls
		if req.Reasoning == nil {
			req.Reasoning = &responsesReasoning{}
		}
		req.Reasoning.Context = "all_turns"
	} else {
		// Standard Codex Responses requests use top-level instructions.
		if len(instructions) > 0 {
			req.Instructions = strings.Join(instructions, "\n\n")
		}
		req.Input = filtered
	}

	// ChatGPT backend requires store=false and stream=true.
	storeFalse := false
	req.Store = &storeFalse
	streamTrue := true
	req.Stream = &streamTrue

	// Don't send fields unsupported by the ChatGPT backend.
	// PromptCacheKey is kept — ChatGPT backend supports prefix-based caching.
	req.PromptCacheRetention = ""
	req.PromptCacheOptions = nil
	req.ServiceTier = ""
	req.MaxOutputTokens = 0
}

// extractTextContent extracts text from a content field that may be a plain
// string or a structured array like [{"type": "input_text", "text": "..."}].
// Handles both []map[string]string (from responsesMessage) and []any (from JSON decode).
func extractTextContent(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	// Handle []map[string]string (built by responsesMessage).
	if arr, ok := content.([]map[string]string); ok {
		var texts []string
		for _, m := range arr {
			if t := m["text"]; t != "" {
				texts = append(texts, t)
			}
		}
		return strings.Join(texts, "\n")
	}
	// Handle []any (from JSON-decoded content).
	if arr, ok := content.([]any); ok {
		var texts []string
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["text"].(string); ok && t != "" {
					texts = append(texts, t)
				}
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
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

	resp, err := p.doRequest(ctx, p.responsesEP(), body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// ChatGPT backend requires stream=true and returns SSE events.
	// Parse the stream and extract the final terminal response event.
	if req.Stream != nil && *req.Stream {
		return p.parseSSEResponses(resp)
	}

	var apiResp responsesAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("openai: failed to decode responses API response: %w", err)
	}

	return p.parseBoundResponsesResponse(&apiResp)
}

// parseSSEResponses reads an SSE stream and returns the final response.
//
// The chatgpt.com/backend-api/codex backend sends `response.completed`
// events with an empty `output:[]` array — the actual message content
// is delivered via `output_item.done` events with type=message that
// contain the assembled `content[].text`. We accumulate those item-done
// events as we go, and merge them into the final response if its own
// output is empty (which it always is for codex). This is the bug fix
// for sleepy meta-evolution: without this, every codex call returns
// empty text and looks like a rate limit.
func (p *Provider) parseSSEResponses(resp *http.Response) (*core.ModelResponse, error) {
	scanner := bufio.NewScanner(resp.Body)
	// Allow large lines (SSE events can be big).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var finalResp *responsesAPIResponse
	var streamedItems []responsesOutputItem // accumulated from output_item.done events
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var event struct {
			Type     string               `json:"type"`
			Response responsesAPIResponse `json:"response,omitempty"`
			Item     responsesOutputItem  `json:"item,omitempty"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		switch event.Type {
		case "response.completed", "response.done":
			finalResp = &event.Response
		case "response.output_item.done":
			// Accumulate completed message items so we can recover
			// the response text even when the terminal event has
			// output:[] (codex backend behavior).
			if event.Item.Type == "message" || event.Item.Type == "function_call" {
				streamedItems = append(streamedItems, event.Item)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// Some OpenAI-compatible backends can successfully deliver a terminal
		// response event and then tear down the HTTP/2 stream before [DONE]. In
		// that case we already have the complete response payload, so prefer it
		// over surfacing a transport error.
		if finalResp == nil {
			return nil, fmt.Errorf("openai: SSE read error: %w", err)
		}
	}
	if finalResp == nil {
		return nil, errors.New("openai: no terminal response event in stream")
	}
	// Codex backend fix: if the terminal response has no output items
	// but we accumulated streamed message items, use those instead.
	if len(finalResp.Output) == 0 && len(streamedItems) > 0 {
		finalResp.Output = streamedItems
	}
	return p.parseBoundResponsesResponse(finalResp)
}

func buildResponsesRequest(messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters, model string, defaultMaxTokens int, disableToolSearch bool) (*responsesRequest, error) {
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
		modelSupports := responsesSupportsToolSearch(model)

		// Check if any tool requests deferred loading.
		anyDeferred := false
		for _, td := range allTools {
			if td.DeferLoading {
				anyDeferred = true
				break
			}
		}

		// Auto-inject the built-in tool_search when applicable.
		if anyDeferred && modelSupports && !disableToolSearch {
			req.Tools = append(req.Tools, responsesToolDef{Type: "tool_search"})
		}

		// Group tools by namespace. Empty namespace = standalone.
		type nsGroup struct {
			desc  string
			tools []responsesToolDef
		}
		namespaces := make(map[string]*nsGroup)
		var namespaceOrder []string
		var standalone []responsesToolDef

		for _, td := range allTools {
			schemaJSON, err := marshalOpenAISchema(td.ParametersSchema)
			if err != nil {
				return nil, err
			}
			rtd := responsesToolDef{
				Type:        "function",
				Name:        td.Name,
				Description: td.Description,
				Parameters:  schemaJSON,
				Strict:      td.Strict,
			}
			if td.DeferLoading && modelSupports {
				rtd.DeferLoading = true
			}

			if td.Namespace != "" {
				g, ok := namespaces[td.Namespace]
				if !ok {
					// Use namespace name as description. A follow-up could add
					// ToolDefinition.NamespaceDescription for richer descriptions.
					g = &nsGroup{desc: td.Namespace}
					namespaces[td.Namespace] = g
					namespaceOrder = append(namespaceOrder, td.Namespace)
				}
				g.tools = append(g.tools, rtd)
			} else {
				standalone = append(standalone, rtd)
			}
		}

		// Emit namespace groups first, then standalone tools.
		for _, ns := range namespaceOrder {
			g := namespaces[ns]
			req.Tools = append(req.Tools, responsesNamespace{
				Type:        "namespace",
				Name:        ns,
				Description: g.desc,
				Tools:       g.tools,
			})
		}
		for _, t := range standalone {
			req.Tools = append(req.Tools, t)
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
			hasMultimodal := false
			for _, part := range m.Parts {
				switch part.(type) {
				case core.ImagePart, core.AudioPart, core.DocumentPart:
					hasMultimodal = true
				}
				if hasMultimodal {
					break
				}
			}

			if !hasMultimodal {
				for _, part := range m.Parts {
					switch p := part.(type) {
					case core.SystemPromptPart:
						input = append(input, responsesMessage("system", p.Content))
					case core.UserPromptPart:
						input = append(input, responsesMessage("user", p.Content))
					case core.ToolReturnPart:
						input = append(input, toolReturnInputItems(p)...)
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
				case core.AudioPart:
					audioObj, err := openaiInputAudio(p.URL, p.MIMEType)
					if err != nil {
						return nil, fmt.Errorf("openai responses: audio: %w", err)
					}
					userContent = append(userContent, map[string]any{
						"type":        "input_audio",
						"input_audio": audioObj,
					})
				case core.DocumentPart:
					fileItem, err := openaiInputFile(p.URL, p.MIMEType, p.Title)
					if err != nil {
						return nil, fmt.Errorf("openai responses: document: %w", err)
					}
					userContent = append(userContent, fileItem)
				case core.ToolReturnPart:
					flushUser()
					input = append(input, toolReturnInputItems(p)...)
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

// toolReturnInputItems renders a ToolReturnPart as Responses-API input items:
// always a function_call_output (whose "output" must be a plain string), and
// — when the tool returned images — a follow-up user message carrying them,
// since the Responses API rejects non-string output payloads.
func toolReturnInputItems(p core.ToolReturnPart) []map[string]any {
	items := []map[string]any{{
		"type":    "function_call_output",
		"call_id": p.ToolCallID,
		"output":  stringifyToolContent(p.Content),
	}}
	if len(p.Images) == 0 {
		return items
	}
	imgContent := []map[string]any{
		{"type": "input_text", "text": "Images returned by tool call " + p.ToolCallID + ":"},
	}
	for _, img := range p.Images {
		item := map[string]any{
			"type":      "input_image",
			"image_url": img.URL,
		}
		if img.Detail != "" {
			item["detail"] = img.Detail
		}
		imgContent = append(imgContent, item)
	}
	return append(items, map[string]any{
		"type":    "message",
		"role":    "user",
		"content": imgContent,
	})
}

// openaiInputAudio converts a gollem AudioPart URL into the input_audio
// object expected by the Responses API: {data: base64, format: "mp3"|"wav"}.
// Only data URIs are supported because OpenAI doesn't accept audio URL refs.
func openaiInputAudio(url, mimeOverride string) (map[string]any, error) {
	rest, ok := strings.CutPrefix(url, "data:")
	if !ok {
		return nil, fmt.Errorf("openai requires base64 data URI for audio; got %q", url)
	}
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
	var format string
	switch mime {
	case "audio/mp3", "audio/mpeg":
		format = "mp3"
	case "audio/wav", "audio/x-wav":
		format = "wav"
	default:
		return nil, fmt.Errorf("unsupported audio MIME %q; OpenAI accepts mp3 or wav", mime)
	}
	return map[string]any{"data": data, "format": format}, nil
}

// openaiInputFile builds the input_file content item. Accepts either a
// bare file_id (prefixed "file-") or a data:...;base64,... URI. For data
// URIs, OpenAI wants the full URI passed in `file_data` plus a `filename`.
func openaiInputFile(url, mimeOverride, title string) (map[string]any, error) {
	item := map[string]any{"type": "input_file"}
	if strings.HasPrefix(url, "file-") {
		item["file_id"] = url
		return item, nil
	}
	if !strings.HasPrefix(url, "data:") {
		return nil, fmt.Errorf("openai requires data URI or file_id for documents; got %q", url)
	}
	// Validate the data URI is base64 before passing it through.
	rest := strings.TrimPrefix(url, "data:")
	semi := strings.Index(rest, ";")
	if semi < 0 {
		return nil, errors.New("malformed data URI: missing ';'")
	}
	if _, ok := strings.CutPrefix(rest[semi+1:], "base64,"); !ok {
		return nil, errors.New("data URI must be base64-encoded")
	}
	_ = mimeOverride // MIME is embedded in the data URI; OpenAI parses it.
	item["file_data"] = url
	if title != "" {
		item["filename"] = title
	} else {
		item["filename"] = "document"
	}
	return item, nil
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
		case "reasoning":
			if summary := parseResponsesReasoningSummary(item); summary != "" {
				parts = append(parts, core.ThinkingPart{Content: summary})
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
			part := core.ToolCallPart{
				ToolName:   item.Name,
				ArgsJSON:   argsJSON,
				ToolCallID: callID,
			}
			if item.Namespace != "" {
				part.Metadata = map[string]string{"namespace": item.Namespace}
			}
			parts = append(parts, part)
			hasToolCalls = true
			// tool_search_call and tool_search_output are server-side plumbing
			// that execute within a single response turn. The model discovers
			// a deferred tool and calls it in the same response. On subsequent
			// requests gollem only needs to send the function_call_output for
			// the actual tool invocation — the search items are NOT required
			// to be round-tripped because the Responses API maintains its own
			// server-side state. Silently dropped here; unknown future item
			// types also fall through harmlessly.
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

func parseResponsesReasoningSummary(item responsesOutputItem) string {
	var text strings.Builder
	for _, s := range item.Summary {
		if s.Type == "summary_text" {
			text.WriteString(s.Text)
		}
	}
	return text.String()
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
	if u.InputTokensDetails != nil && u.InputTokensDetails.CacheWriteTokens > 0 {
		usage.CacheWriteTokens = u.InputTokensDetails.CacheWriteTokens
	}
	if u.OutputTokensDetails != nil && u.OutputTokensDetails.ReasoningTokens > 0 {
		usage.Details = map[string]int{
			"reasoning_tokens": u.OutputTokensDetails.ReasoningTokens,
		}
	}
	return usage
}

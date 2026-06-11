// Package anthropic provides a core.Model implementation for Anthropic's
// Messages API, supporting Claude models with tool use, streaming, and
// extended thinking.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// Model constants for Anthropic Claude models.
//
// Only Claude 4.6 and newer are first-class here. Callers targeting older
// models (Sonnet 4.5, Opus 4.5, etc.) may still pass the raw model string via
// WithModel; the provider falls back to the legacy manual-thinking path for
// those, but does not expose them as symbolic constants.
const (
	ClaudeFable5   = "claude-fable-5"
	ClaudeOpus48   = "claude-opus-4-8"
	ClaudeOpus47   = "claude-opus-4-7"
	ClaudeOpus46   = "claude-opus-4-6"
	ClaudeSonnet46 = "claude-sonnet-4-6"
	ClaudeHaiku45  = "claude-haiku-4-5-20251001"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	defaultModel     = ClaudeSonnet46
	defaultMaxTokens = 4096
	anthropicVersion = "2023-06-01"
	messagesEndpoint = "/v1/messages"

	toolSearchToolRegexType = "tool_search_tool_regex_20251119"
	toolSearchToolRegexName = "tool_search_tool_regex"
)

// supportsToolSearch reports whether the given model supports Anthropic's
// server-side tool search feature. Per Anthropic docs, it is available on
// Claude Sonnet 4.0+, Claude Opus 4.0+, and the Mythos Preview models.
// NOT available on Haiku 4.x or earlier Claude 3.x models.
func supportsToolSearch(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(m, "mythos") {
		return true
	}
	for _, family := range []string{"sonnet-", "opus-"} {
		if idx := strings.Index(m, family); idx >= 0 {
			rest := m[idx+len(family):]
			if len(rest) > 0 && rest[0] >= '4' && rest[0] <= '9' {
				return true
			}
		}
	}
	return false
}

// isOpus47 reports whether the model string identifies Claude Opus 4.7 (or
// Mythos Preview, which behaves the same way for thinking/effort purposes).
// Opus 4.7 uses adaptive thinking exclusively; manual {type: "enabled",
// budget_tokens} is rejected by the API.
func isOpus47(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(m, "mythos") {
		return true
	}
	return strings.Contains(m, "opus-4-7")
}

// isOpus48OrFable reports whether the model string identifies a post-4.7
// flagship — Claude Opus 4.8 or the Fable tier. Same thinking/effort
// surface as Opus 4.7: adaptive-only thinking, full effort range
// including "xhigh" and "max".
func isOpus48OrFable(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "opus-4-8") || strings.Contains(m, "fable")
}

// isOpus46OrSonnet46 reports whether the model string identifies Opus 4.6 or
// Sonnet 4.6. These models accept both manual and adaptive thinking, and
// support the "max" effort level.
func isOpus46OrSonnet46(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "opus-4-6") || strings.Contains(m, "sonnet-4-6")
}

// supportsEffort reports whether the given model accepts the output_config.effort
// parameter. Per Anthropic docs, supported on Fable, Opus 4.8, Opus 4.7
// (and Mythos), Opus 4.6, Sonnet 4.6, and Opus 4.5. Notably NOT supported
// on Haiku 4.5 or any 3.x model.
func supportsEffort(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if isOpus47(m) || isOpus48OrFable(m) || isOpus46OrSonnet46(m) {
		return true
	}
	return strings.Contains(m, "opus-4-5")
}

// supportsManualThinking reports whether {thinking: {type: "enabled",
// budget_tokens: N}} is accepted by the model. Opus 4.7+ (4.7, 4.8, Fable,
// Mythos) rejects it — adaptive thinking is the only mode there. Opus 4.6 /
// Sonnet 4.6 accept it but deprecate it. Older models continue to accept it.
func supportsManualThinking(model string) bool {
	return !isOpus47(model) && !isOpus48OrFable(model)
}

// supportsAdaptiveThinking reports whether {thinking: {type: "adaptive"}}
// is accepted by the model: the Claude 4.6 generation and newer (Opus 4.6,
// Sonnet 4.6, Opus 4.7, Opus 4.8, Fable, Mythos). Models 4.5 and older,
// and Haiku, only have manual thinking.
func supportsAdaptiveThinking(model string) bool {
	return isOpus47(model) || isOpus48OrFable(model) || isOpus46OrSonnet46(model)
}

// supportsEffortValue reports whether a given effort value is accepted by the
// model. "xhigh" requires Opus 4.7+ (4.7, 4.8, Fable, Mythos). "max" requires
// Opus 4.6+, Sonnet 4.6+, or Opus 4.7+.
func supportsEffortValue(model, effort string) bool {
	switch effort {
	case "low", "medium", "high":
		return supportsEffort(model)
	case "xhigh":
		return isOpus47(model) || isOpus48OrFable(model)
	case "max":
		return isOpus47(model) || isOpus48OrFable(model) || isOpus46OrSonnet46(model)
	default:
		return false
	}
}

// Provider implements core.Model for Anthropic's Messages API.
type Provider struct {
	apiKey             string
	model              string
	baseURL            string
	httpClient         *http.Client
	maxTokens          int
	disablePromptCache bool
	disableToolSearch  bool
}

// Option configures the Anthropic provider.
type Option func(*Provider)

// WithAPIKey sets the API key. If not set, reads from ANTHROPIC_API_KEY env var.
func WithAPIKey(key string) Option {
	return func(p *Provider) {
		p.apiKey = key
	}
}

// WithModel sets the model to use.
func WithModel(model string) Option {
	return func(p *Provider) {
		p.model = model
	}
}

// WithBaseURL sets a custom base URL.
func WithBaseURL(url string) Option {
	return func(p *Provider) {
		p.baseURL = url
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) {
		p.httpClient = c
	}
}

// WithMaxTokens sets the default max tokens for requests.
func WithMaxTokens(n int) Option {
	return func(p *Provider) {
		p.maxTokens = n
	}
}

// WithPromptCacheDisabled disables automatic cache_control markers on system
// prompts and tool definitions. Use this for Zero Data Retention compliance
// or to opt out of prompt caching.
func WithPromptCacheDisabled() Option {
	return func(p *Provider) {
		p.disablePromptCache = true
	}
}

// WithToolSearchDisabled disables automatic injection of Anthropic's
// tool_search_tool_regex built-in when any tool has DeferLoading=true.
// Use this to opt out of the default regex variant (e.g., to hand-inject
// the bm25 variant yourself). Has no effect when no tool has DeferLoading=true.
// Note: per-tool defer_loading: true is still emitted even when the built-in
// is disabled, so a caller who manually includes a tool_search tool gets
// deferred tools without the auto-injected built-in.
func WithToolSearchDisabled() Option {
	return func(p *Provider) {
		p.disableToolSearch = true
	}
}

// New creates a new Anthropic provider with the given options.
func New(opts ...Option) *Provider {
	p := &Provider{
		model:      defaultModel,
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
		maxTokens:  defaultMaxTokens,
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.apiKey == "" {
		p.apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	return p
}

// ModelName returns the model identifier.
func (p *Provider) ModelName() string {
	return p.model
}

// Request sends messages to Anthropic and returns a complete response.
func (p *Provider) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, false, !p.disablePromptCache, p.disableToolSearch)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to build request: %w", err)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to marshal request: %w", err)
	}

	resp, err := p.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var apiResp apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("anthropic: failed to decode response: %w", err)
	}

	return parseResponse(&apiResp, p.model), nil
}

// RequestStream sends messages and returns a streaming response.
func (p *Provider) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, true, !p.disablePromptCache, p.disableToolSearch)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to build request: %w", err)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to marshal request: %w", err)
	}

	resp, err := p.doRequest(ctx, body) //nolint:bodyclose // Response body ownership transfers to streamedResponse.
	if err != nil {
		return nil, err
	}

	return newStreamedResponse(resp.Body, p.model), nil
}

// doRequest sends a single HTTP request and returns the response or a typed
// error. Retry logic is handled at the model level by modelutil.RetryModel,
// which uses this error's RetryAfter field for backoff.
func (p *Provider) doRequest(ctx context.Context, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+messagesEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to create HTTP request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: HTTP request failed: %w", err)
	}

	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	httpErr := &core.ModelHTTPError{
		Message:    "anthropic API error: " + string(respBody),
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
		ModelName:  p.model,
	}

	// Parse Retry-After header for 429 responses so the model-level
	// retry (modelutil.RetryModel) can use appropriate backoff.
	if resp.StatusCode == http.StatusTooManyRequests {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil {
				httpErr.RetryAfter = time.Duration(secs) * time.Second
			}
		}
	}

	return nil, httpErr
}

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
}

// Verify Provider implements core.Model.
var _ core.Model = (*Provider)(nil)

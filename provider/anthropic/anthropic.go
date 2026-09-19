// Package anthropic provides a core.Model implementation for Anthropic's
// Messages API, supporting Claude models with tool use, streaming, and
// extended thinking.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
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
	ClaudeMythos51 = "claude-mythos-5-1"
	ClaudeFable51  = "claude-fable-5-1"
	ClaudeFable5   = "claude-fable-5"
	ClaudeOpus5    = "claude-opus-5"
	ClaudeSonnet5  = "claude-sonnet-5"
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
		if slices.ContainsFunc(majorVersionsAfter(m, family), func(major byte) bool { return major >= '4' && major <= '9' }) {
			return true
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

// isClaude5Family reports whether the model string identifies the Claude 5
// generation of the Opus or Sonnet line (claude-opus-5, claude-sonnet-5, and
// any dated or "@"-suffixed variant). Fable and Mythos are matched by name
// elsewhere. Claude 5 has the same request surface as Opus 4.7+: adaptive-only
// thinking, the full effort range including "xhigh" and "max", and no
// temperature/top_p/top_k sampling controls.
//
// The match is anchored on the major version segment so "opus-4-5" (Opus 4.5)
// never matches "opus-5".
func isClaude5Family(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return hasMajorVersion(m, "opus-", '5') || hasMajorVersion(m, "sonnet-", '5')
}

// hasMajorVersion reports whether m contains family immediately followed by
// the given major-version digit as a complete version segment, i.e. followed
// by end of string, "-", "@", or ".". This distinguishes "opus-5" and
// "opus-5-20260401" from "opus-4-5" and "opus-50".
func hasMajorVersion(m, family string, major byte) bool {
	return slices.Contains(majorVersionsAfter(m, family), major)
}

// majorVersionsAfter returns each major-version digit that follows an
// occurrence of family as a complete version segment (terminated by end of
// string, "-", "@", or "."). It is the single model-id grammar shared by the
// family predicates and supportsToolSearch.
func majorVersionsAfter(m, family string) []byte {
	var majors []byte
	for idx := strings.Index(m, family); idx >= 0; {
		rest := m[idx+len(family):]
		if len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
			if len(rest) == 1 {
				majors = append(majors, rest[0])
			} else {
				switch rest[1] {
				case '-', '@', '.':
					majors = append(majors, rest[0])
				}
			}
		}
		next := strings.Index(rest, family)
		if next < 0 {
			break
		}
		idx += len(family) + next
	}
	return majors
}

// isAdaptiveOnly reports whether the model rejects manual thinking budgets
// and sampling controls: Opus 4.7, Opus 4.8, Fable, Mythos, and the Claude 5
// generation.
func isAdaptiveOnly(model string) bool {
	return isOpus47(model) || isOpus48OrFable(model) || isClaude5Family(model)
}

// isOpus46OrSonnet46 reports whether the model string identifies Opus 4.6 or
// Sonnet 4.6. These models accept both manual and adaptive thinking, and
// support the "max" effort level.
func isOpus46OrSonnet46(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "opus-4-6") || strings.Contains(m, "sonnet-4-6")
}

// supportsEffort reports whether the given model accepts the output_config.effort
// parameter. Per Anthropic docs, supported on the Claude 5 generation, Fable,
// Opus 4.8, Opus 4.7 (and Mythos), Opus 4.6, Sonnet 4.6, and Opus 4.5.
// Notably NOT supported on Haiku 4.5 or any 3.x model.
func supportsEffort(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if isAdaptiveOnly(m) || isOpus46OrSonnet46(m) {
		return true
	}
	return strings.Contains(m, "opus-4-5")
}

// supportsManualThinking reports whether {thinking: {type: "enabled",
// budget_tokens: N}} is accepted by the model. Opus 4.7+ (4.7, 4.8, Fable,
// Mythos) and the Claude 5 generation reject it — adaptive thinking is the
// only mode there. Opus 4.6 / Sonnet 4.6 accept it but deprecate it. Older
// models continue to accept it.
func supportsManualThinking(model string) bool {
	return !isAdaptiveOnly(model)
}

// supportsAdaptiveThinking reports whether {thinking: {type: "adaptive"}}
// is accepted by the model: the Claude 4.6 generation and newer (Opus 4.6,
// Sonnet 4.6, Opus 4.7, Opus 4.8, Fable, Mythos, Opus 5, Sonnet 5). Models
// 4.5 and older, and Haiku, only have manual thinking.
func supportsAdaptiveThinking(model string) bool {
	return isAdaptiveOnly(model) || isOpus46OrSonnet46(model)
}

// supportsSampling reports whether the model accepts temperature and top_p.
// Opus 4.7+ (4.7, 4.8, Fable, Mythos) and the Claude 5 generation return 400
// on either parameter; every older model accepts them.
func supportsSampling(model string) bool {
	return !isAdaptiveOnly(model)
}

// supportsEffortValue reports whether a given effort value is accepted by the
// model. "xhigh" requires Opus 4.7+ (4.7, 4.8, Fable, Mythos) or Claude 5.
// "max" additionally works on Opus 4.6 / Sonnet 4.6.
func supportsEffortValue(model, effort string) bool {
	switch effort {
	case "low", "medium", "high":
		return supportsEffort(model)
	case "xhigh":
		return isAdaptiveOnly(model)
	case "max":
		return isAdaptiveOnly(model) || isOpus46OrSonnet46(model)
	default:
		return false
	}
}

// SupportsAdaptiveThinking reports whether the model accepts
// {thinking: {type: "adaptive"}} (Claude 4.6 generation and newer).
func SupportsAdaptiveThinking(model string) bool { return supportsAdaptiveThinking(model) }

// SupportsManualThinking reports whether the model accepts a manual
// {thinking: {type: "enabled", budget_tokens: N}} configuration.
func SupportsManualThinking(model string) bool { return supportsManualThinking(model) }

// SupportsEffort reports whether the model accepts output_config.effort at all.
func SupportsEffort(model string) bool { return supportsEffort(model) }

// SupportsEffortValue reports whether the model accepts the specific
// output_config.effort value ("low", "medium", "high", "xhigh", or "max").
func SupportsEffortValue(model, effort string) bool { return supportsEffortValue(model, effort) }

// SupportsToolSearch reports whether the model supports Anthropic's
// server-side tool search tool.
func SupportsToolSearch(model string) bool { return supportsToolSearch(model) }

// SupportsSampling reports whether the model accepts temperature and top_p;
// Opus 4.7+ and Claude 5 reject them.
func SupportsSampling(model string) bool { return supportsSampling(model) }

// Provider implements core.Model for Anthropic's Messages API.
type Provider struct {
	apiKey             string
	model              string
	baseURL            string
	httpClient         *http.Client
	maxTokens          int
	disablePromptCache bool
	disableToolSearch  bool
	requestObserver    RequestObserver
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

// WithRequestObserver installs a secret-safe per-request diagnostics callback.
// It receives timing, bounded request shape, normalized usage, and sanitized
// error information only; it never receives credentials, request content,
// endpoints, headers, or raw provider error bodies.
func WithRequestObserver(observer RequestObserver) Option {
	return func(p *Provider) {
		p.requestObserver = observer
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
	if p.requestObserver == nil && isTruthy(os.Getenv("ANTHROPIC_REQUEST_TRACE")) {
		p.requestObserver = DefaultStderrRequestObserver()
	}
	return p
}

// ModelName returns the model identifier.
func (p *Provider) ModelName() string {
	return p.model
}

// Request sends messages to Anthropic and returns a complete response.
func (p *Provider) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	ri := newRequestInstrumentation(p.requestObserver, p.model)
	defer ri.finish()
	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, false, p.promptCacheEnabled(settings), p.disableToolSearch)
	if err != nil {
		ri.recordError(err)
		return nil, fmt.Errorf("anthropic: failed to build request: %w", err)
	}

	body, err := json.Marshal(req)
	if err != nil {
		ri.recordError(err)
		return nil, fmt.Errorf("anthropic: failed to marshal request: %w", err)
	}
	ri.setRequestShape(len(body), len(req.System)+len(req.Messages))

	resp, err := p.doRequest(ctx, body, ri)
	if err != nil {
		ri.recordError(err)
		return nil, err
	}
	defer resp.Body.Close()

	var apiResp apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		ri.recordError(err)
		return nil, fmt.Errorf("anthropic: failed to decode response: %w", err)
	}

	response := parseResponse(&apiResp, p.model)
	ri.recordUsage(response.Usage)
	ri.recordTerminal()
	return response, nil
}

// RequestStream sends messages and returns a streaming response.
func (p *Provider) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	ri := newRequestInstrumentation(p.requestObserver, p.model)
	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, true, p.promptCacheEnabled(settings), p.disableToolSearch)
	if err != nil {
		ri.recordError(err)
		ri.finish()
		return nil, fmt.Errorf("anthropic: failed to build request: %w", err)
	}

	body, err := json.Marshal(req)
	if err != nil {
		ri.recordError(err)
		ri.finish()
		return nil, fmt.Errorf("anthropic: failed to marshal request: %w", err)
	}
	ri.setRequestShape(len(body), len(req.System)+len(req.Messages))

	resp, err := p.doRequest(ctx, body, ri) //nolint:bodyclose // Response body ownership transfers to streamedResponse.
	if err != nil {
		ri.recordError(err)
		ri.finish()
		return nil, err
	}

	return newStreamedResponse(resp.Body, p.model, ri), nil
}

func (p *Provider) promptCacheEnabled(settings *core.ModelSettings) bool {
	if p.disablePromptCache {
		return false
	}
	return settings == nil || settings.PromptCacheEnabled == nil || *settings.PromptCacheEnabled
}

// doRequest sends a single HTTP request and returns the response or a typed
// error. Retry logic is handled at the model level by modelutil.RetryModel,
// which uses this error's RetryAfter field for backoff.
func (p *Provider) doRequest(ctx context.Context, body []byte, ri *requestInstrumentation) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+messagesEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to create HTTP request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: HTTP request failed: %w", err)
	}
	ri.recordHeaders(resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}

	classification := readProviderErrorClassification(resp.Body)
	resp.Body.Close()
	ri.classifyHTTPResponse(resp.StatusCode, classification)

	httpErr := sanitizedProviderHTTPError(resp.StatusCode, classification, p.model)

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

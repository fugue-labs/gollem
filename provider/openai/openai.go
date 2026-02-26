// Package openai provides a core.Model implementation for OpenAI's
// Chat Completions API, supporting GPT and O-series models with tool use,
// streaming, and native structured output.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// Model constants for OpenAI models.
const (
	GPT4o     = "gpt-4o"
	GPT4oMini = "gpt-4o-mini"
	O3        = "o3"
	O3Mini    = "o3-mini"
	O4Mini    = "o4-mini"
)

const (
	defaultBaseURL          = "https://api.openai.com"
	defaultModel            = GPT4o
	defaultMaxTokens        = 4096
	chatCompletionsEndpoint = "/v1/chat/completions"
	responsesEndpoint       = "/v1/responses"
)

const (
	transportHTTP      = "http"
	transportWebSocket = "websocket"
)

// Provider implements core.Model for OpenAI's Chat Completions API.
type Provider struct {
	apiKey               string
	model                string
	baseURL              string
	httpClient           *http.Client
	maxTokens            int
	promptCacheKey       string
	promptCacheRetention string
	serviceTier          string
	transport            string
	wsHTTPFallback       bool
	wsHTTPFallbackSet    bool
	useResponses         bool
	wsConn               *responsesWebSocketConn
	wsPrevResponseID     string
	wsLastInputSigs      []string
	wsMu                 sync.Mutex
}

// Option configures the OpenAI provider.
type Option func(*Provider)

// WithAPIKey sets the API key. If not set, reads from OPENAI_API_KEY env var.
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

// WithBaseURL sets a custom base URL (useful for proxies and compatible APIs).
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

// WithPromptCacheKey sets a stable prompt cache key for OpenAI prompt caching.
// Useful to increase cache hit rates across related requests.
func WithPromptCacheKey(key string) Option {
	return func(p *Provider) {
		p.promptCacheKey = key
	}
}

// WithPromptCacheRetention sets OpenAI prompt cache retention policy
// (for example: "in_memory" or "24h", model-dependent).
func WithPromptCacheRetention(retention string) Option {
	return func(p *Provider) {
		p.promptCacheRetention = retention
	}
}

// WithServiceTier sets the OpenAI service tier (for example: "default", "flex",
// or "priority"). If not set, reads from OPENAI_SERVICE_TIER env var.
func WithServiceTier(tier string) Option {
	return func(p *Provider) {
		p.serviceTier = tier
	}
}

// WithTransport sets the request transport for OpenAI provider.
// Supported values: "http" (default) and "websocket".
func WithTransport(transport string) Option {
	return func(p *Provider) {
		p.transport = transport
	}
}

// WithWebSocketHTTPFallback controls whether websocket transport may silently
// fall back to HTTP responses on websocket failures. Default: false.
func WithWebSocketHTTPFallback(enabled bool) Option {
	return func(p *Provider) {
		p.wsHTTPFallback = enabled
		p.wsHTTPFallbackSet = true
	}
}

// New creates a new OpenAI provider with the given options.
// Supports OPENAI_API_KEY and OPENAI_BASE_URL environment variables
// for compatibility with OpenAI-compatible APIs (xAI, Together, etc.).
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
		p.apiKey = os.Getenv("OPENAI_API_KEY")
	}
	// Support OPENAI_BASE_URL env var (standard for OpenAI-compatible APIs).
	if p.baseURL == defaultBaseURL {
		if envURL := os.Getenv("OPENAI_BASE_URL"); envURL != "" {
			p.baseURL = envURL
		}
	}
	if p.promptCacheKey == "" {
		p.promptCacheKey = os.Getenv("OPENAI_PROMPT_CACHE_KEY")
	}
	if p.promptCacheRetention == "" {
		p.promptCacheRetention = os.Getenv("OPENAI_PROMPT_CACHE_RETENTION")
	}
	if p.serviceTier == "" {
		p.serviceTier = os.Getenv("OPENAI_SERVICE_TIER")
	}
	if p.transport == "" {
		p.transport = os.Getenv("OPENAI_TRANSPORT")
	}
	p.transport = normalizeTransport(p.transport)
	if !p.wsHTTPFallbackSet {
		if raw := strings.TrimSpace(os.Getenv("OPENAI_WEBSOCKET_HTTP_FALLBACK")); raw != "" {
			p.wsHTTPFallback = isTruthy(raw)
		}
	}
	// Strip trailing /v1 or /v1/ from the base URL. Our endpoint path
	// already includes /v1, so a base URL with /v1 (which is the convention
	// in the OpenAI Python client) would produce /v1/v1/chat/completions.
	p.baseURL = strings.TrimRight(p.baseURL, "/")
	p.baseURL = strings.TrimSuffix(p.baseURL, "/v1")
	return p
}

// NewLiteLLM creates an OpenAI-compatible provider configured for a LiteLLM proxy.
func NewLiteLLM(baseURL string, opts ...Option) *Provider {
	allOpts := append([]Option{WithBaseURL(baseURL)}, opts...)
	p := New(allOpts...)
	// LiteLLM uses OPENAI_API_KEY by default, no special handling needed.
	return p
}

// NewOllama creates an OpenAI-compatible provider configured for a local Ollama instance.
// By default it connects to http://localhost:11434 and uses a dummy API key since
// Ollama does not require authentication. The model should be set via WithModel
// to match a model pulled in Ollama (e.g., "llama3", "mistral", "codellama").
func NewOllama(opts ...Option) *Provider {
	allOpts := append([]Option{
		WithBaseURL("http://localhost:11434"),
		WithAPIKey("ollama"),
	}, opts...)
	return New(allOpts...)
}

// NewSession returns an equivalent provider instance with isolated transient
// request/session state (for example websocket continuation state). Use this
// when spawning parallel agents that must not share a websocket chain.
func (p *Provider) NewSession() core.Model {
	return &Provider{
		apiKey:               p.apiKey,
		model:                p.model,
		baseURL:              p.baseURL,
		httpClient:           p.httpClient,
		maxTokens:            p.maxTokens,
		promptCacheKey:       p.promptCacheKey,
		promptCacheRetention: p.promptCacheRetention,
		serviceTier:          p.serviceTier,
		transport:            p.transport,
		wsHTTPFallback:       p.wsHTTPFallback,
		wsHTTPFallbackSet:    p.wsHTTPFallbackSet,
		useResponses:         p.useResponses,
	}
}

// Close releases transport resources held by the provider (for example an
// active responses websocket connection). It is safe to call multiple times.
func (p *Provider) Close() error {
	p.wsMu.Lock()
	defer p.wsMu.Unlock()
	if p.wsConn == nil || p.wsConn.conn == nil {
		p.wsConn = nil
		p.wsPrevResponseID = ""
		p.wsLastInputSigs = nil
		return nil
	}
	err := p.wsConn.conn.Close()
	p.wsConn = nil
	p.wsPrevResponseID = ""
	p.wsLastInputSigs = nil
	return err
}

// ModelName returns the model identifier.
func (p *Provider) ModelName() string {
	return p.model
}

// Request sends messages to OpenAI and returns a complete response.
func (p *Provider) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	if p.shouldUseResponsesAPI() {
		return p.requestViaResponses(ctx, messages, settings, params)
	}

	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, false)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to build request: %w", err)
	}
	req.PromptCacheKey = p.promptCacheKey
	req.PromptCacheRetention = p.promptCacheRetention
	req.ServiceTier = p.serviceTier

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to marshal request: %w", err)
	}

	resp, err := p.doRequest(ctx, chatCompletionsEndpoint, body)
	if err != nil {
		if isChatCompletionsMismatch(err) {
			// Some models (e.g. Codex variants) are only available via /v1/responses.
			p.useResponses = true
			return p.requestViaResponses(ctx, messages, settings, params)
		}
		return nil, err
	}
	defer resp.Body.Close()

	var apiResp apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("openai: failed to decode response: %w", err)
	}

	return parseResponse(&apiResp, p.model), nil
}

// RequestStream sends messages and returns a streaming response.
func (p *Provider) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	if p.shouldUseResponsesAPI() {
		return nil, fmt.Errorf("openai: streaming is not supported for model %q via the responses API", p.model)
	}

	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, true)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to build request: %w", err)
	}
	req.PromptCacheKey = p.promptCacheKey
	req.PromptCacheRetention = p.promptCacheRetention
	req.ServiceTier = p.serviceTier

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to marshal request: %w", err)
	}

	resp, err := p.doRequest(ctx, chatCompletionsEndpoint, body) //nolint:bodyclose // Response body ownership transfers to streamedResponse.
	if err != nil {
		if isChatCompletionsMismatch(err) {
			p.useResponses = true
			return nil, fmt.Errorf("openai: model %q requires the responses API; streaming is currently unavailable", p.model)
		}
		return nil, err
	}

	return newStreamedResponse(resp.Body, p.model), nil
}

// doRequest sends a single HTTP request and returns the response or a typed
// error. Retry logic is handled at the model level by modelutil.RetryModel,
// which uses this error's RetryAfter field for backoff.
func (p *Provider) doRequest(ctx context.Context, endpoint string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: failed to create HTTP request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: HTTP request failed: %w", err)
	}

	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	httpErr := &core.ModelHTTPError{
		Message:    "openai API error: " + string(respBody),
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
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
}

func (p *Provider) shouldUseResponsesAPI() bool {
	return p.useResponses || modelNeedsResponsesAPI(p.model)
}

func (p *Provider) shouldUseResponsesWebSocket() bool {
	return p.shouldUseResponsesAPI() && p.transport == transportWebSocket
}

func normalizeTransport(transport string) string {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "", transportHTTP:
		return transportHTTP
	case "ws", "wss", transportWebSocket:
		return transportWebSocket
	default:
		return transportHTTP
	}
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func responsesWebSocketURL(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("openai: invalid base URL %q: %w", baseURL, err)
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "wss", "ws":
		// Leave as-is.
	default:
		return "", fmt.Errorf("openai: unsupported base URL scheme %q for websocket mode", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + responsesEndpoint
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func modelNeedsResponsesAPI(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "codex")
}

func isChatCompletionsMismatch(err error) bool {
	var httpErr *core.ModelHTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	if httpErr.StatusCode != http.StatusNotFound {
		return false
	}
	body := strings.ToLower(httpErr.Body)
	msg := strings.ToLower(httpErr.Message)
	return strings.Contains(body, "not a chat model") || strings.Contains(msg, "not a chat model")
}

// Verify Provider implements core.Model.
var _ core.Model = (*Provider)(nil)

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
	"github.com/fugue-labs/gollem/modelutil"
	"github.com/google/uuid"
)

// Model constants for OpenAI models.
const (
	// GPT-4 family (legacy).
	GPT4o     = "gpt-4o"
	GPT4oMini = "gpt-4o-mini"

	// o-series reasoning models.
	O3     = "o3"
	O3Mini = "o3-mini"
	O4Mini = "o4-mini"

	// GPT-5 family. Supports reasoning by default. Use WithReasoningEffort
	// to control thinking depth (minimal|low|medium|high).
	GPT5       = "gpt-5"
	GPT5Mini   = "gpt-5-mini"
	GPT5Nano   = "gpt-5-nano"
	GPT5Codex  = "gpt-5-codex"
	GPT56      = "gpt-5.6"
	GPT56Sol   = "gpt-5.6-sol"
	GPT56Terra = "gpt-5.6-terra"
	GPT56Luna  = "gpt-5.6-luna"
)

const (
	defaultBaseURL          = "https://api.openai.com"
	chatgptBaseURL          = "https://chatgpt.com/backend-api/codex"
	defaultModel            = GPT4o
	defaultMaxTokens        = 4096
	chatCompletionsEndpoint = "/v1/chat/completions"
	responsesEndpoint       = "/v1/responses"
	chatgptResponsesEP      = "/responses"
	chatgptResponsesLiteHdr = "X-OpenAI-Internal-Codex-Responses-Lite"
	// GPT-5.6 requires the Responses Lite request contract introduced by
	// Codex 0.144.0. The backend gates Luna on this compatibility version.
	chatgptUserAgent = "codex_cli_rs/0.144.0"
)

const (
	transportHTTP      = "http"
	transportWebSocket = "websocket"
)

// TokenRefresher is a function that returns a fresh access token.
// It is called before each request when the provider is in ChatGPT auth mode.
type TokenRefresher func() (accessToken string, err error)

// Provider implements core.Model for OpenAI's Chat Completions API.
type Provider struct {
	apiKey                  string
	model                   string
	baseURL                 string
	httpClient              *http.Client
	maxTokens               int
	promptCacheKey          string
	promptCacheRetention    string
	serviceTier             string
	transport               string
	wsHTTPFallback          bool
	wsHTTPFallbackSet       bool
	useResponses            bool
	disableToolSearch       bool
	strictModelBinding      bool
	wsConn                  *responsesWebSocketConn
	wsAuthToken             string
	wsPrevResponseID        string
	wsLastInputSigs         []string
	wsMu                    sync.Mutex
	reasoningSummary        string
	textVerbosity           string
	chatgptAccountID        string
	tokenRefresher          TokenRefresher
	reasoningSummaryHandler func(text string)
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

// WithReasoningSummary sets the reasoning summary mode for the Responses API.
// Supported values: "auto", "concise", "detailed". Only applies when the model
// produces reasoning output.
func WithReasoningSummary(summary string) Option {
	return func(p *Provider) {
		p.reasoningSummary = summary
	}
}

// WithReasoningSummaryHandler installs a callback that receives each
// reasoning summary text chunk as the websocket streams it. The
// Responses API emits these under "response.reasoning_summary_text.done"
// (and deltas under ".delta"); this hook lets callers surface the
// model's thinking to users in real time. Only fires on the websocket
// transport; no-op on HTTP.
func WithReasoningSummaryHandler(fn func(text string)) Option {
	return func(p *Provider) {
		p.reasoningSummaryHandler = fn
	}
}

// WithTextVerbosity sets the text output verbosity for the Responses API.
// Supported values: "low", "medium", "high". Lower verbosity produces shorter
// responses, directly reducing output tokens.
func WithTextVerbosity(verbosity string) Option {
	return func(p *Provider) {
		p.textVerbosity = verbosity
	}
}

// WithChatGPTAuth configures the provider for ChatGPT subscription access.
// This sets the access token and account ID for subscription-based usage,
// forces the Responses API (required for subscription access), and routes
// requests to the ChatGPT backend endpoint (chatgpt.com/backend-api/codex)
// instead of the standard API endpoint.
func WithChatGPTAuth(accessToken, accountID string) Option {
	return func(p *Provider) {
		p.apiKey = accessToken
		p.chatgptAccountID = accountID
		p.useResponses = true
		p.baseURL = chatgptBaseURL
	}
}

// WithTokenRefresher sets a function that is called before each request to
// obtain a fresh access token. This is used with ChatGPT subscription auth
// to automatically refresh expired OAuth tokens.
func WithTokenRefresher(refresher TokenRefresher) Option {
	return func(p *Provider) {
		p.tokenRefresher = refresher
	}
}

// WithStrictModelBinding requires every successful response to identify the
// requested model (or a dated/tagged snapshot of that exact model family).
// Missing or conflicting provider model identity fails the request. This is
// intended for provenance-sensitive source-bearing workloads.
func WithStrictModelBinding() Option {
	return func(p *Provider) {
		p.strictModelBinding = true
	}
}

// WithToolSearchDisabled disables automatic injection of OpenAI's
// tool_search built-in when any tool has DeferLoading=true. Has no
// effect when no tool has DeferLoading=true or when the model does not
// support tool_search (Chat Completions, non-gpt-5.4+ models).
func WithToolSearchDisabled() Option {
	return func(p *Provider) {
		p.disableToolSearch = true
	}
}

// WithResumedResponsesChain seeds the Responses API continuation state from a
// prior process (for example after a sidecar restart). id is a response id
// previously captured via PreviousResponseID(); inputSigs are the matching
// input-item signatures previously captured via LastInputSignatures(). When
// both are set, the first request this provider sends is delivered as a delta
// on top of id, preserving the server-side reasoning and prompt cache rather
// than cold-starting.
//
// If the chain is no longer valid (the response aged out server-side, or the
// rebuilt input no longer matches the stored signatures byte-for-byte) the
// existing recovery path in responses_ws.go drops the id and transparently
// retries with the full input.
//
// Passing id="" or inputSigs=nil is a no-op for that parameter.
func WithResumedResponsesChain(id string, inputSigs []string) Option {
	return func(p *Provider) {
		if id = strings.TrimSpace(id); id != "" {
			p.wsPrevResponseID = id
		}
		if len(inputSigs) > 0 {
			p.wsLastInputSigs = append([]string(nil), inputSigs...)
		}
	}
}

// PreviousResponseID returns the most recent OpenAI Responses response id this
// provider has observed, or "" if none. Persist this alongside
// LastInputSignatures() to resume the Responses chain in a future process;
// pair with WithResumedResponsesChain on the next construction.
func (p *Provider) PreviousResponseID() string {
	p.wsMu.Lock()
	defer p.wsMu.Unlock()
	return p.wsPrevResponseID
}

// LastInputSignatures returns a copy of the signatures of the input items
// from the most recent Responses API request, in order. Persist these
// alongside PreviousResponseID() to resume the Responses chain in a future
// process.
func (p *Provider) LastInputSignatures() []string {
	p.wsMu.Lock()
	defer p.wsMu.Unlock()
	if len(p.wsLastInputSigs) == 0 {
		return nil
	}
	out := make([]string, len(p.wsLastInputSigs))
	copy(out, p.wsLastInputSigs)
	return out
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
	// Skip when in ChatGPT auth mode to avoid overriding the subscription endpoint.
	if p.baseURL == defaultBaseURL && p.chatgptAccountID == "" {
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
	if p.reasoningSummary == "" {
		p.reasoningSummary = os.Getenv("OPENAI_REASONING_SUMMARY")
	}
	if p.textVerbosity == "" {
		p.textVerbosity = os.Getenv("OPENAI_TEXT_VERBOSITY")
	}
	// Strip trailing /v1 or /v1/ from the base URL. Our endpoint path
	// already includes /v1, so a base URL with /v1 (which is the convention
	// in the OpenAI Python client) would produce /v1/v1/chat/completions.
	p.baseURL = strings.TrimRight(p.baseURL, "/")
	p.baseURL = strings.TrimSuffix(p.baseURL, "/v1")
	// Auto-set prompt cache key for OpenAI and ChatGPT endpoints.
	// This dramatically improves cache hit rates (60% → 87%+) and cached
	// tokens get 75-90% discounts on modern models. The ChatGPT backend
	// supports the same prefix-based caching via prompt_cache_key.
	if p.promptCacheKey == "" && (p.isOpenAIEndpoint() || p.isChatGPTEndpoint()) {
		p.promptCacheKey = uuid.New().String()
	}
	if p.promptCacheRetention == "" && p.isOpenAIEndpoint() {
		p.promptCacheRetention = "24h"
	}
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

// NewXAI creates an OpenAI-compatible provider configured for xAI's API.
// By default it connects to https://api.x.ai with prompt cache retention
// set to "24h" for cost savings. The API key is read from XAI_API_KEY first,
// then OPENAI_API_KEY, or can be set explicitly via WithAPIKey.
func NewXAI(opts ...Option) *Provider {
	allOpts := []Option{
		WithBaseURL("https://api.x.ai"),
		WithPromptCacheRetention("24h"),
	}
	// Prefer XAI_API_KEY over OPENAI_API_KEY for xAI provider.
	if key := os.Getenv("XAI_API_KEY"); key != "" {
		allOpts = append(allOpts, WithAPIKey(key))
	}
	allOpts = append(allOpts, opts...)
	p := New(allOpts...)
	p.useResponses = true // xAI only supports Responses API
	return p
}

// NewSession returns an equivalent provider instance with isolated transient
// request/session state (for example websocket continuation state). Use this
// when spawning parallel agents that must not share a websocket chain.
func (p *Provider) NewSession() core.Model {
	return &Provider{
		apiKey:                  p.apiKey,
		model:                   p.model,
		baseURL:                 p.baseURL,
		httpClient:              p.httpClient,
		maxTokens:               p.maxTokens,
		promptCacheKey:          p.promptCacheKey,
		promptCacheRetention:    p.promptCacheRetention,
		serviceTier:             p.serviceTier,
		transport:               p.transport,
		wsHTTPFallback:          p.wsHTTPFallback,
		wsHTTPFallbackSet:       p.wsHTTPFallbackSet,
		useResponses:            p.useResponses,
		disableToolSearch:       p.disableToolSearch,
		strictModelBinding:      p.strictModelBinding,
		reasoningSummary:        p.reasoningSummary,
		textVerbosity:           p.textVerbosity,
		chatgptAccountID:        p.chatgptAccountID,
		tokenRefresher:          p.tokenRefresher,
		reasoningSummaryHandler: p.reasoningSummaryHandler,
	}
}

// Close releases transport resources held by the provider (for example an
// active responses websocket connection). It is safe to call multiple times.
func (p *Provider) Close() error {
	p.wsMu.Lock()
	defer p.wsMu.Unlock()
	if p.wsConn == nil || p.wsConn.conn == nil {
		p.wsConn = nil
		p.wsAuthToken = ""
		p.wsPrevResponseID = ""
		p.wsLastInputSigs = nil
		return nil
	}
	err := p.wsConn.conn.Close()
	p.wsConn = nil
	p.wsAuthToken = ""
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
	responseModel, err := p.resolveResponseModel(apiResp.Model)
	if err != nil {
		return nil, err
	}
	return parseResponse(&apiResp, responseModel), nil
}

// RequestStream sends messages and returns a streaming response.
func (p *Provider) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	if p.shouldUseResponsesAPI() {
		return p.requestStreamViaResponses(ctx, messages, settings, params)
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
			return p.requestStreamViaResponses(ctx, messages, settings, params)
		}
		return nil, err
	}

	return newBoundStreamedResponse(resp.Body, p.model, p.resolveResponseModel), nil
}

// responsesEP returns the Responses API endpoint path. ChatGPT subscription
// uses /responses (on the chatgpt.com backend), while the standard API uses
// /v1/responses.
func (p *Provider) responsesEP() string {
	if p.hasChatGPTAuth() {
		return chatgptResponsesEP
	}
	return responsesEndpoint
}

// doRequest sends a single HTTP request and returns the response or a typed
// error. Retry logic is handled at the model level by modelutil.RetryModel,
// which uses this error's RetryAfter field for backoff.
func (p *Provider) doRequest(ctx context.Context, endpoint string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: failed to create HTTP request: %w", err)
	}
	if err := p.setHeaders(httpReq); err != nil {
		return nil, err
	}

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

func (p *Provider) authorizationToken() (string, error) {
	if p.tokenRefresher == nil {
		return p.apiKey, nil
	}
	token, err := p.tokenRefresher()
	if err != nil {
		return "", fmt.Errorf("openai: refreshing access token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return "", errors.New("openai: refreshing access token: empty token")
	}
	return token, nil
}

// ModelIdentityError means the provider answered with a model identity that
// cannot be bound to the requested route.
type ModelIdentityError struct {
	Requested string
	Actual    string
}

func (e *ModelIdentityError) Error() string {
	if strings.TrimSpace(e.Actual) == "" {
		return fmt.Sprintf("openai: response omitted model identity for requested model %q", e.Requested)
	}
	return fmt.Sprintf("openai: response model identity %q does not match requested model %q", e.Actual, e.Requested)
}

func compatibleResponseModel(requested, actual string) bool {
	requested = strings.TrimSpace(requested)
	actual = strings.TrimSpace(actual)
	if requested == "" || actual == "" {
		return false
	}
	if actual == requested {
		return true
	}
	// OpenAI aliases commonly resolve to dated snapshots (model-YYYY-MM-DD).
	// OpenAI-compatible local runtimes commonly report an explicit tag
	// (model:latest). Neither rule admits a different model family.
	if snapshot, ok := strings.CutPrefix(actual, requested+"-"); ok {
		if _, err := time.Parse("2006-01-02", snapshot); err == nil {
			return true
		}
	}
	if tag, ok := strings.CutPrefix(actual, requested+":"); ok {
		return tag != "" && !strings.ContainsAny(tag, " \t\r\n")
	}
	return false
}

func (p *Provider) resolveResponseModel(actual string) (string, error) {
	actual = strings.TrimSpace(actual)
	if p.strictModelBinding && !compatibleResponseModel(p.model, actual) {
		return "", &ModelIdentityError{Requested: p.model, Actual: actual}
	}
	if actual != "" {
		return actual, nil
	}
	return p.model, nil
}

func (p *Provider) parseBoundResponsesResponse(resp *responsesAPIResponse) (*core.ModelResponse, error) {
	responseModel, err := p.resolveResponseModel(resp.Model)
	if err != nil {
		return nil, err
	}
	return parseResponsesResponse(resp, responseModel), nil
}

func (p *Provider) setHeaders(req *http.Request) error {
	token, err := p.authorizationToken()
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if p.hasChatGPTAuth() {
		if p.chatgptAccountID != "" {
			req.Header.Set("ChatGPT-Account-ID", p.chatgptAccountID)
		}
		// The ChatGPT backend (chatgpt.com) requires a User-Agent and originator
		// header to pass Cloudflare bot protection and gate model compatibility.
		req.Header.Set("User-Agent", chatgptUserAgent)
		req.Header.Set("originator", "codex_cli_rs")
		if isGPT56Model(p.model) {
			req.Header.Set(chatgptResponsesLiteHdr, "true")
		}
	}
	return nil
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

func responsesWebSocketURL(baseURL, endpoint string) (string, error) {
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
	parsed.Path = strings.TrimRight(parsed.Path, "/") + endpoint
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func modelNeedsResponsesAPI(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "codex") || strings.Contains(m, "multi-agent") || strings.HasPrefix(m, "gpt-5")
}

func isGPT56Model(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return m == GPT56 || strings.HasPrefix(m, GPT56+"-")
}

// responsesSupportsToolSearch reports whether the given model supports
// OpenAI's native Responses API tool_search. Per OpenAI docs, this is
// available on gpt-5.4 and later. Codex and multi-agent variants are
// excluded (they use specialized inline tool sets).
func responsesSupportsToolSearch(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(m, "codex") || strings.Contains(m, "multi-agent") {
		return false
	}
	if !strings.HasPrefix(m, "gpt-5.") {
		return false
	}
	rest := strings.TrimPrefix(m, "gpt-5.")
	var digits []byte
	for i := range len(rest) {
		c := rest[i]
		if c < '0' || c > '9' {
			break
		}
		digits = append(digits, c)
	}
	if len(digits) == 0 {
		return false
	}
	n, err := strconv.Atoi(string(digits))
	if err != nil {
		return false
	}
	return n >= 4
}

func isChatCompletionsMismatch(err error) bool {
	var httpErr *core.ModelHTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	body := strings.ToLower(httpErr.Body)
	msg := strings.ToLower(httpErr.Message)
	combined := body + " " + msg
	if httpErr.StatusCode == http.StatusNotFound {
		return strings.Contains(combined, "not a chat model")
	}
	if httpErr.StatusCode == http.StatusBadRequest {
		return strings.Contains(combined, "please use /v1/responses")
	}
	return false
}

// isOpenAIEndpoint reports whether the provider is configured for the
// official OpenAI API (as opposed to a compatible third-party like xAI).
func (p *Provider) isOpenAIEndpoint() bool {
	return strings.Contains(p.baseURL, "openai.com")
}

// isChatGPTEndpoint reports whether the provider targets the ChatGPT
// subscription backend (chatgpt.com), which has different request format
// requirements (instructions, store=false, stream=true, SSE responses).
func (p *Provider) isChatGPTEndpoint() bool {
	return strings.Contains(p.baseURL, "chatgpt.com")
}

// hasChatGPTAuth reports whether ChatGPT subscription auth is configured.
// Used for setting auth-related headers (Account-ID, User-Agent, originator).
func (p *Provider) hasChatGPTAuth() bool {
	return p.chatgptAccountID != "" || p.isChatGPTEndpoint()
}

// Profile returns the model's capability profile. Vision is supported by
// GPT-4o, GPT-4o-mini, O-series, and Codex models.
func (p *Provider) Profile() modelutil.ModelProfile {
	return modelutil.ModelProfile{
		SupportsToolCalls:        true,
		SupportsStructuredOutput: true,
		SupportsVision:           modelSupportsVision(p.model),
		SupportsStreaming:        true,
	}
}

// modelSupportsVision returns true for models known to support image input.
func modelSupportsVision(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	// GPT-4o variants, O-series, and Codex all support vision.
	// Only older text-only models (gpt-3.5, gpt-4-base) lack vision.
	for _, prefix := range []string{"gpt-3.5", "gpt-4-turbo-2024-04-09"} {
		if strings.HasPrefix(m, prefix) {
			return false
		}
	}
	// ft: fine-tuned models may not support vision.
	if strings.HasPrefix(m, "ft:gpt-3.5") {
		return false
	}
	return true
}

// Verify Provider implements core.Model and modelutil.Profiled.
var _ core.Model = (*Provider)(nil)
var _ modelutil.Profiled = (*Provider)(nil)

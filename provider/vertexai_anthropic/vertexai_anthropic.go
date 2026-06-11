// Package vertexai_anthropic provides a core.Model implementation for
// Anthropic Claude models accessed through Google Cloud's Vertex AI,
// using the rawPredict endpoint with GCP authentication.
package vertexai_anthropic

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
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/fugue-labs/gollem/core"
)

// Model constants for Claude models via Vertex AI.
// Note: Vertex AI uses model IDs WITHOUT date suffixes, unlike the direct Anthropic API.
//
// Only Claude 4.6 and newer are first-class here. Callers targeting older
// models may still pass the raw model string via WithModel; the provider falls
// back to the legacy manual-thinking path for those.
const (
	ClaudeOpus47   = "claude-opus-4-7"
	ClaudeOpus46   = "claude-opus-4-6"
	ClaudeSonnet46 = "claude-sonnet-4-6"
	ClaudeHaiku45  = "claude-haiku-4-5"
)

const (
	defaultLocation  = "us-east5"
	defaultModel     = ClaudeSonnet46
	defaultMaxTokens = 4096
	anthropicVersion = "vertex-2023-10-16"
	cloudScope       = "https://www.googleapis.com/auth/cloud-platform"
)

// isOpus47 reports whether the model string identifies Opus 4.7 or Mythos.
// These models only accept adaptive thinking.
func isOpus47(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(m, "mythos") {
		return true
	}
	return strings.Contains(m, "opus-4-7")
}

// isOpus48OrFable reports whether the model string identifies a post-4.7
// flagship — Opus 4.8 or the Fable tier. Same thinking/effort surface as
// Opus 4.7: adaptive-only thinking, full effort range.
func isOpus48OrFable(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "opus-4-8") || strings.Contains(m, "fable")
}

// isOpus46OrSonnet46 reports whether the model is Opus 4.6 or Sonnet 4.6.
func isOpus46OrSonnet46(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "opus-4-6") || strings.Contains(m, "sonnet-4-6")
}

// supportsEffort reports whether the model accepts the output_config.effort
// parameter. Per Anthropic docs: Fable, Opus 4.8/4.7/4.6/4.5 (and Mythos),
// Sonnet 4.6. Haiku 4.5 and Claude 3.x do NOT support it.
func supportsEffort(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if isOpus47(m) || isOpus48OrFable(m) || isOpus46OrSonnet46(m) {
		return true
	}
	return strings.Contains(m, "opus-4-5")
}

// supportsManualThinking reports whether {thinking: {type: "enabled",
// budget_tokens: N}} is accepted. Opus 4.7+ (4.7, 4.8, Fable, Mythos)
// rejects it.
func supportsManualThinking(model string) bool {
	return !isOpus47(model) && !isOpus48OrFable(model)
}

// supportsAdaptiveThinking reports whether {thinking: {type: "adaptive"}}
// is accepted: the Claude 4.6 generation and newer (Opus 4.6, Sonnet 4.6,
// Opus 4.7, Opus 4.8, Fable, Mythos).
func supportsAdaptiveThinking(model string) bool {
	return isOpus47(model) || isOpus48OrFable(model) || isOpus46OrSonnet46(model)
}

// supportsEffortValue reports whether a given effort value is accepted.
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

// Provider implements core.Model for Claude via Vertex AI rawPredict.
type Provider struct {
	project    string
	location   string
	model      string
	httpClient *http.Client
	maxTokens  int

	promptCachingEnabled     bool
	promptCachingConfigured  bool
	promptCacheTTL           string
	promptCacheTTLConfigured bool

	credentialsFile string
	credentialsJSON []byte

	mu          sync.Mutex
	tokenSource oauth2.TokenSource
}

// Option configures the Vertex AI Anthropic provider.
type Option func(*Provider)

// WithProject sets the GCP project ID.
func WithProject(project string) Option {
	return func(p *Provider) {
		p.project = project
	}
}

// WithLocation sets the GCP region.
func WithLocation(location string) Option {
	return func(p *Provider) {
		p.location = location
	}
}

// WithModel sets the Claude model to use.
func WithModel(model string) Option {
	return func(p *Provider) {
		p.model = model
	}
}

// WithCredentialsFile sets the path to a service account JSON key file.
func WithCredentialsFile(path string) Option {
	return func(p *Provider) {
		p.credentialsFile = path
	}
}

// WithCredentialsJSON sets the raw service account JSON key bytes.
func WithCredentialsJSON(data []byte) Option {
	return func(p *Provider) {
		p.credentialsJSON = data
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

// WithPromptCaching enables or disables Anthropic prompt caching via Vertex AI.
// When enabled, requests include top-level cache_control with type=ephemeral.
func WithPromptCaching(enabled bool) Option {
	return func(p *Provider) {
		p.promptCachingEnabled = enabled
		p.promptCachingConfigured = true
	}
}

// WithPromptCacheTTL sets optional Anthropic prompt cache TTL (e.g. "5m", "1h").
// Setting a TTL also implicitly enables prompt caching.
func WithPromptCacheTTL(ttl string) Option {
	return func(p *Provider) {
		p.promptCacheTTL = strings.TrimSpace(ttl)
		p.promptCacheTTLConfigured = true
	}
}

// New creates a new Vertex AI Anthropic provider with the given options.
func New(opts ...Option) *Provider {
	p := &Provider{
		location:   defaultLocation,
		model:      defaultModel,
		httpClient: http.DefaultClient,
		maxTokens:  defaultMaxTokens,
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.project == "" {
		p.project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if !p.promptCacheTTLConfigured && p.promptCacheTTL == "" {
		p.promptCacheTTL = strings.TrimSpace(os.Getenv("VERTEXAI_ANTHROPIC_PROMPT_CACHE_TTL"))
	}
	if !p.promptCachingConfigured {
		p.promptCachingEnabled = envEnabled("VERTEXAI_ANTHROPIC_PROMPT_CACHE")
	}
	// TTL implies caching unless explicitly disabled via WithPromptCaching(false).
	if p.promptCacheTTL != "" && (!p.promptCachingConfigured || p.promptCachingEnabled) {
		p.promptCachingEnabled = true
	}
	return p
}

// ModelName returns the model identifier.
func (p *Provider) ModelName() string {
	return p.model
}

// endpoint returns the rawPredict endpoint URL for the model.
func (p *Provider) endpoint() string {
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:rawPredict",
		p.location, p.project, p.location, p.model)
}

// streamEndpoint returns the streaming endpoint URL.
func (p *Provider) streamEndpoint() string {
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:streamRawPredict",
		p.location, p.project, p.location, p.model)
}

// getToken returns a valid OAuth2 access token for GCP.
func (p *Provider) getToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.tokenSource == nil {
		ts, err := p.createTokenSource(ctx)
		if err != nil {
			return "", fmt.Errorf("vertexai_anthropic: failed to create token source: %w", err)
		}
		p.tokenSource = ts
	}

	token, err := p.tokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("vertexai_anthropic: failed to get token: %w", err)
	}
	return token.AccessToken, nil
}

// createTokenSource creates an OAuth2 token source based on configuration.
func (p *Provider) createTokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	if p.credentialsJSON != nil {
		creds, err := google.CredentialsFromJSON(ctx, p.credentialsJSON, cloudScope) //nolint:staticcheck // deprecated but still functional
		if err != nil {
			return nil, err
		}
		return creds.TokenSource, nil
	}
	if p.credentialsFile != "" {
		data, err := os.ReadFile(p.credentialsFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read credentials file: %w", err)
		}
		creds, err := google.CredentialsFromJSON(ctx, data, cloudScope) //nolint:staticcheck // deprecated but still functional
		if err != nil {
			return nil, err
		}
		return creds.TokenSource, nil
	}
	ts, err := google.DefaultTokenSource(ctx, cloudScope)
	if err != nil {
		return nil, err
	}
	return ts, nil
}

// Request sends messages to Claude via Vertex AI and returns a complete response.
func (p *Provider) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, false)
	if err != nil {
		return nil, fmt.Errorf("vertexai_anthropic: failed to build request: %w", err)
	}
	p.applyPromptCacheControl(req)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("vertexai_anthropic: failed to marshal request: %w", err)
	}

	url := p.endpoint()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("vertexai_anthropic: failed to create HTTP request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq); err != nil {
		return nil, err
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vertexai_anthropic: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, p.parseHTTPError(resp)
	}

	var apiResp apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("vertexai_anthropic: failed to decode response: %w", err)
	}

	return parseResponse(&apiResp, p.model), nil
}

// RequestStream sends messages and returns a streaming response.
func (p *Provider) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, true)
	if err != nil {
		return nil, fmt.Errorf("vertexai_anthropic: failed to build request: %w", err)
	}
	p.applyPromptCacheControl(req)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("vertexai_anthropic: failed to marshal request: %w", err)
	}

	url := p.streamEndpoint()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("vertexai_anthropic: failed to create HTTP request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq); err != nil {
		return nil, err
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vertexai_anthropic: HTTP request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, p.parseHTTPError(resp)
	}

	return newStreamedResponse(resp.Body, p.model), nil
}

func (p *Provider) setHeaders(ctx context.Context, req *http.Request) error {
	req.Header.Set("Content-Type", "application/json")
	token, err := p.getToken(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// parseHTTPError constructs a ModelHTTPError from a non-200 response,
// including Retry-After header parsing for rate-limited responses.
func (p *Provider) parseHTTPError(resp *http.Response) error {
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	httpErr := &core.ModelHTTPError{
		Message:    "vertexai_anthropic API error: " + string(respBody),
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
		ModelName:  p.model,
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil {
				httpErr.RetryAfter = time.Duration(secs) * time.Second
			}
		}
	}
	return httpErr
}

func envEnabled(name string) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (p *Provider) applyPromptCacheControl(req *apiRequest) {
	if req == nil || !p.promptCachingEnabled {
		return
	}
	cc := &apiCacheControl{Type: "ephemeral"}
	if p.promptCacheTTL != "" {
		cc.TTL = p.promptCacheTTL
	}
	req.CacheControl = cc
}

// Verify Provider implements core.Model.
var _ core.Model = (*Provider)(nil)

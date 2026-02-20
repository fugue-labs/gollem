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

	"github.com/fugue-labs/gollem/core"
)

// Model constants for Anthropic Claude models.
const (
	Claude4Opus   = "claude-opus-4-6"
	Claude4Sonnet = "claude-sonnet-4-5-20250929"
	Claude4Haiku  = "claude-haiku-4-5-20251001"
)

const (
	defaultBaseURL       = "https://api.anthropic.com"
	defaultModel         = Claude4Sonnet
	defaultMaxTokens     = 4096
	anthropicVersion     = "2023-06-01"
	messagesEndpoint     = "/v1/messages"
)

// Provider implements core.Model for Anthropic's Messages API.
type Provider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
	maxTokens  int
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
	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, false)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to build request: %w", err)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+messagesEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to create HTTP request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &core.ModelHTTPError{
			Message:    "anthropic API error: " + string(respBody),
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			ModelName:  p.model,
		}
	}

	var apiResp apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("anthropic: failed to decode response: %w", err)
	}

	return parseResponse(&apiResp, p.model), nil
}

// RequestStream sends messages and returns a streaming response.
func (p *Provider) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, true)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to build request: %w", err)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+messagesEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to create HTTP request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: HTTP request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &core.ModelHTTPError{
			Message:    "anthropic API error: " + string(respBody),
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			ModelName:  p.model,
		}
	}

	return newStreamedResponse(resp.Body, p.model), nil
}

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
}

// Verify Provider implements core.Model.
var _ core.Model = (*Provider)(nil)

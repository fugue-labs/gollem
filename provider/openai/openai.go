// Package openai provides a core.Model implementation for OpenAI's
// Chat Completions API, supporting GPT and O-series models with tool use,
// streaming, and native structured output.
package openai

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
)

// Provider implements core.Model for OpenAI's Chat Completions API.
type Provider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
	maxTokens  int
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

// New creates a new OpenAI provider with the given options.
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
	return p
}

// NewLiteLLM creates an OpenAI-compatible provider configured for a LiteLLM proxy.
func NewLiteLLM(baseURL string, opts ...Option) *Provider {
	allOpts := append([]Option{WithBaseURL(baseURL)}, opts...)
	p := New(allOpts...)
	// LiteLLM uses OPENAI_API_KEY by default, no special handling needed.
	return p
}

// ModelName returns the model identifier.
func (p *Provider) ModelName() string {
	return p.model
}

// Request sends messages to OpenAI and returns a complete response.
func (p *Provider) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, false)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to build request: %w", err)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+chatCompletionsEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: failed to create HTTP request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &core.ModelHTTPError{
			Message:    "openai API error: " + string(respBody),
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			ModelName:  p.model,
		}
	}

	var apiResp apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("openai: failed to decode response: %w", err)
	}

	return parseResponse(&apiResp, p.model), nil
}

// RequestStream sends messages and returns a streaming response.
func (p *Provider) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	req, err := buildRequest(messages, settings, params, p.model, p.maxTokens, true)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to build request: %w", err)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+chatCompletionsEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: failed to create HTTP request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: HTTP request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &core.ModelHTTPError{
			Message:    "openai API error: " + string(respBody),
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			ModelName:  p.model,
		}
	}

	return newStreamedResponse(resp.Body, p.model), nil
}

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
}

// Verify Provider implements core.Model.
var _ core.Model = (*Provider)(nil)

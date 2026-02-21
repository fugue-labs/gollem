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
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/fugue-labs/gollem/core"
)

// Model constants for Claude models via Vertex AI.
// Note: Vertex AI uses model IDs WITHOUT date suffixes, unlike the direct Anthropic API.
const (
	Claude4Opus   = "claude-opus-4-6"
	Claude4Sonnet = "claude-sonnet-4-5"
	Claude4Haiku  = "claude-haiku-4-5"
)

const (
	defaultLocation  = "us-east5"
	defaultModel     = Claude4Sonnet
	defaultMaxTokens = 4096
	anthropicVersion = "vertex-2023-10-16"
	cloudScope       = "https://www.googleapis.com/auth/cloud-platform"
)

// Provider implements core.Model for Claude via Vertex AI rawPredict.
type Provider struct {
	project    string
	location   string
	model      string
	httpClient *http.Client
	maxTokens  int

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
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &core.ModelHTTPError{
			Message:    "vertexai_anthropic API error: " + string(respBody),
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			ModelName:  p.model,
		}
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
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &core.ModelHTTPError{
			Message:    "vertexai_anthropic API error: " + string(respBody),
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			ModelName:  p.model,
		}
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

// Verify Provider implements core.Model.
var _ core.Model = (*Provider)(nil)

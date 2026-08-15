// Package vertexai provides a core.Model implementation for Google's
// Vertex AI Gemini API, supporting Gemini models with tool use, streaming,
// and GCP authentication via Application Default Credentials or service accounts.
package vertexai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"

	"golang.org/x/oauth2"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/provider/internal/gcpauth"
)

// Model constants for Gemini models.
const (
	Gemini31ProPreview  = "gemini-3.1-pro-preview"
	Gemini3FlashPreview = "gemini-3-flash-preview"
	Gemini25Pro         = "gemini-2.5-pro"
	Gemini25Flash       = "gemini-2.5-flash"
	Gemini20Flash       = "gemini-2.0-flash"
)

const (
	defaultLocation = "us-central1"
	defaultModel    = Gemini25Flash
)

// Provider implements core.Model for Vertex AI Gemini API.
type Provider struct {
	project         string
	location        string
	model           string
	httpClient      *http.Client
	credentialsFile string
	credentialsJSON []byte
	cachedContent   string

	mu          sync.Mutex
	tokenSource oauth2.TokenSource
}

// Option configures the Vertex AI provider.
type Option func(*Provider)

// WithProject sets the GCP project ID.
func WithProject(project string) Option {
	return func(p *Provider) {
		p.project = project
	}
}

// WithLocation sets the GCP region (e.g., "us-central1").
func WithLocation(location string) Option {
	return func(p *Provider) {
		p.location = location
	}
}

// WithModel sets the Gemini model to use.
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

// WithCachedContent sets the resource name of an existing context cache to
// attach to requests (e.g., "projects/.../locations/.../cachedContents/...").
// When set, the Gemini API uses the cached content instead of re-processing
// the corresponding prefix tokens, reducing cost and latency.
func WithCachedContent(name string) Option {
	return func(p *Provider) {
		p.cachedContent = name
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) {
		p.httpClient = c
	}
}

// New creates a new Vertex AI Gemini provider with the given options.
func New(opts ...Option) *Provider {
	p := &Provider{
		location:   defaultLocation,
		model:      defaultModel,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.project == "" {
		p.project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if p.cachedContent == "" {
		p.cachedContent = os.Getenv("VERTEXAI_CACHED_CONTENT")
	}
	return p
}

// ModelName returns the model identifier.
func (p *Provider) ModelName() string {
	return p.model
}

// endpoint returns the base endpoint URL for the model.
func (p *Provider) endpoint() string {
	host := p.location + "-aiplatform.googleapis.com"
	if p.location == "global" {
		host = "aiplatform.googleapis.com"
	}
	return fmt.Sprintf("https://%s/v1/projects/%s/locations/%s/publishers/google/models/%s",
		host, p.project, p.location, p.model)
}

// getToken returns a valid OAuth2 access token for GCP.
func (p *Provider) getToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	token, err := gcpauth.AccessToken(ctx, &p.tokenSource, gcpauth.Credentials{
		File: p.credentialsFile,
		JSON: p.credentialsJSON,
	})
	if err != nil {
		var sourceCreationError *gcpauth.SourceCreationError
		if errors.As(err, &sourceCreationError) {
			return "", fmt.Errorf("vertexai: failed to create token source: %w", err)
		}
		return "", fmt.Errorf("vertexai: failed to get token: %w", err)
	}
	return token, nil
}

// Request sends messages to Vertex AI Gemini and returns a complete response.
func (p *Provider) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	req, err := buildRequest(messages, settings, params)
	if err != nil {
		return nil, fmt.Errorf("vertexai: failed to build request: %w", err)
	}
	p.applyCacheSettings(req, settings)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("vertexai: failed to marshal request: %w", err)
	}

	url := p.endpoint() + ":generateContent"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("vertexai: failed to create HTTP request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq); err != nil {
		return nil, err
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vertexai: HTTP request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, p.parseHTTPError(resp)
	}
	defer resp.Body.Close()

	var apiResp geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("vertexai: failed to decode response: %w", err)
	}

	return parseResponse(&apiResp, p.model), nil
}

// RequestStream sends messages and returns a streaming response.
func (p *Provider) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	req, err := buildRequest(messages, settings, params)
	if err != nil {
		return nil, fmt.Errorf("vertexai: failed to build request: %w", err)
	}
	p.applyCacheSettings(req, settings)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("vertexai: failed to marshal request: %w", err)
	}

	url := p.endpoint() + ":streamGenerateContent?alt=sse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("vertexai: failed to create HTTP request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq); err != nil {
		return nil, err
	}

	// The error helper closes non-successful bodies; successful streams transfer
	// ownership to newStreamedResponse.
	resp, err := p.httpClient.Do(httpReq) //nolint:bodyclose // ownership is split by the status branch below
	if err != nil {
		return nil, fmt.Errorf("vertexai: HTTP request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, p.parseHTTPError(resp)
	}

	return newStreamedResponse(resp.Body, p.model), nil
}

func (p *Provider) setHeaders(ctx context.Context, req *http.Request) error {
	token, err := p.getToken(ctx)
	if err != nil {
		return err
	}
	gcpauth.SetJSONBearer(req, token)
	return nil
}

// applyCacheSettings attaches the cached content reference to the request
// if configured on the provider.
func (p *Provider) applyCacheSettings(req *geminiRequest, settings *core.ModelSettings) {
	if p.cachedContent != "" && (settings == nil || settings.PromptCacheEnabled == nil || *settings.PromptCacheEnabled) {
		req.CachedContent = p.cachedContent
	}
}

// parseHTTPError constructs a bounded, redacted ModelHTTPError from a non-200 response.
func (p *Provider) parseHTTPError(resp *http.Response) error {
	return gcpauth.ParseHTTPError("vertexai", p.model, resp)
}

// Verify Provider implements core.Model.
var _ core.Model = (*Provider)(nil)

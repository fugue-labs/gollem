package catwalk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultURL is the hosted registry Crush ships with.
const DefaultURL = "https://catwalk.charm.sh"

// URLEnv overrides the registry base URL when set. Crush honours the same
// variable, so a local mirror serves both tools.
const URLEnv = "CATWALK_URL"

// ErrNotModified is returned by Fetch when the server reports the caller's
// ETag is still current.
var ErrNotModified = errors.New("catwalk: providers not modified")

// maxResponseBytes bounds the registry payload. The live registry is well
// under 2 MiB; anything larger is treated as corrupt rather than buffered.
const maxResponseBytes = 16 << 20

// Client fetches the provider registry over HTTP.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// ResolveURL returns the registry base URL: override when non-empty, then
// $CATWALK_URL read through lookup, then DefaultURL. Trailing slashes are
// removed. A nil lookup reads the process environment.
func ResolveURL(lookup func(string) (string, bool), override string) string {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	url := strings.TrimSpace(override)
	if url == "" {
		value, _ := lookup(URLEnv)
		url = strings.TrimSpace(value)
	}
	if url == "" {
		url = DefaultURL
	}
	return strings.TrimRight(url, "/")
}

// NewClient returns a client for baseURL. An empty baseURL selects
// $CATWALK_URL, then DefaultURL. A nil httpClient uses a 30 second timeout.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: ResolveURL(nil, baseURL), httpClient: httpClient}
}

// BaseURL returns the registry base URL the client targets.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// Fetch downloads the registry. When etag is non-empty it is sent as
// If-None-Match and ErrNotModified is returned on a 304. The returned etag is
// the server's current ETag (possibly empty).
func (c *Client) Fetch(ctx context.Context, etag string) ([]Provider, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v2/providers", nil)
	if err != nil {
		return nil, "", fmt.Errorf("catwalk: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gollem-catwalk/1")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("catwalk: fetch providers: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil, etag, ErrNotModified
	case http.StatusOK:
	default:
		return nil, "", fmt.Errorf("catwalk: fetch providers: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("catwalk: read providers: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, "", fmt.Errorf("catwalk: providers payload exceeds %d bytes", maxResponseBytes)
	}
	providers, err := Parse(body)
	if err != nil {
		return nil, "", err
	}
	if len(providers) == 0 {
		return nil, "", errors.New("catwalk: registry returned no providers")
	}
	return providers, resp.Header.Get("ETag"), nil
}

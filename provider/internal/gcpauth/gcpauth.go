// Package gcpauth contains shared GCP authentication and HTTP-error plumbing
// for Vertex providers. Provider packages retain their own request semantics
// and prefix the returned errors with their public provider identity.
package gcpauth

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/fugue-labs/gollem/provider/internal/vertexerror"
)

const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// Credentials identifies the process-owner supplied credential material used
// to acquire a GCP access token. An empty value uses application default
// credentials.
type Credentials struct {
	File string
	JSON []byte
}

// SourceCreationError distinguishes lazy credential construction from a token
// refresh failure so callers can preserve their existing diagnostics.
type SourceCreationError struct {
	err error
}

func (e *SourceCreationError) Error() string { return e.err.Error() }

func (e *SourceCreationError) Unwrap() error { return e.err }

// AccessToken resolves and caches a token source supplied by the caller. The
// caller owns synchronization around source when concurrent requests are
// possible.
func AccessToken(ctx context.Context, source *oauth2.TokenSource, credentials Credentials) (string, error) {
	if *source == nil {
		created, err := createTokenSource(ctx, credentials)
		if err != nil {
			return "", &SourceCreationError{err: err}
		}
		*source = created
	}

	token, err := (*source).Token()
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

func createTokenSource(ctx context.Context, credentials Credentials) (oauth2.TokenSource, error) {
	if credentials.JSON != nil {
		creds, err := google.CredentialsFromJSON(ctx, credentials.JSON, cloudPlatformScope) //nolint:staticcheck,nolintlint // credentials are explicitly configured by the process owner
		if err != nil {
			return nil, err
		}
		return creds.TokenSource, nil
	}
	if credentials.File != "" {
		data, err := os.ReadFile(credentials.File)
		if err != nil {
			return nil, fmt.Errorf("failed to read credentials file: %w", err)
		}
		creds, err := google.CredentialsFromJSON(ctx, data, cloudPlatformScope) //nolint:staticcheck,nolintlint // credentials are explicitly configured by the process owner
		if err != nil {
			return nil, err
		}
		return creds.TokenSource, nil
	}
	return google.DefaultTokenSource(ctx, cloudPlatformScope)
}

// SetJSONBearer applies the headers common to Vertex JSON API requests.
func SetJSONBearer(req *http.Request, accessToken string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
}

// ParseHTTPError closes a non-successful Vertex response and returns its
// bounded, redacted provider error.
func ParseHTTPError(provider, model string, resp *http.Response) error {
	defer resp.Body.Close()
	return vertexerror.NewHTTPError(provider, resp.StatusCode, resp.Body, resp.Header.Get("Retry-After"), model)
}

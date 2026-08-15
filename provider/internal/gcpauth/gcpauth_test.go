package gcpauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestAccessTokenUsesExistingSource(t *testing.T) {
	var source oauth2.TokenSource = staticTokenSource("test-token")

	token, err := AccessToken(context.Background(), &source, Credentials{})
	if err != nil {
		t.Fatalf("AccessToken() error = %v", err)
	}
	if token != "test-token" {
		t.Errorf("AccessToken() = %q, want test-token", token)
	}
}

func TestAccessTokenClassifiesSourceCreationFailure(t *testing.T) {
	var source oauth2.TokenSource

	_, err := AccessToken(context.Background(), &source, Credentials{
		File: "/definitely/not/a/gcp-credential.json",
	})
	if err == nil {
		t.Fatal("AccessToken() error = nil, want source creation error")
	}
	var creationError *SourceCreationError
	if !errors.As(err, &creationError) {
		t.Fatalf("AccessToken() error = %T %v, want SourceCreationError", err, err)
	}
	if !strings.Contains(err.Error(), "failed to read credentials file") {
		t.Errorf("AccessToken() error = %q, want redacted file-read context", err)
	}
}

func TestSetJSONBearer(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://example.invalid", nil)

	SetJSONBearer(req, "test-token")

	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q, want bearer token", got)
	}
}

type staticTokenSource string

func (s staticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: string(s)}, nil
}

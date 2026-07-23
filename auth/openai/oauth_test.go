package openai

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveLoadCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	creds := &Credentials{
		AccessToken:  "at-test-123",
		RefreshToken: "rt-test-456",
		IDToken:      "id-test-789",
		AccountID:    "acct-001",
		AuthMode:     "chatgpt",
		ExpiresAt:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}

	if err := SaveCredentialsTo(creds, path); err != nil {
		t.Fatalf("SaveCredentialsTo: %v", err)
	}

	// Verify file permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected file permissions 0600, got %o", perm)
	}

	loaded, err := LoadCredentialsFrom(path)
	if err != nil {
		t.Fatalf("LoadCredentialsFrom: %v", err)
	}

	if loaded.AccessToken != creds.AccessToken {
		t.Errorf("AccessToken: got %q, want %q", loaded.AccessToken, creds.AccessToken)
	}
	if loaded.RefreshToken != creds.RefreshToken {
		t.Errorf("RefreshToken: got %q, want %q", loaded.RefreshToken, creds.RefreshToken)
	}
	if loaded.IDToken != creds.IDToken {
		t.Errorf("IDToken: got %q, want %q", loaded.IDToken, creds.IDToken)
	}
	if loaded.AccountID != creds.AccountID {
		t.Errorf("AccountID: got %q, want %q", loaded.AccountID, creds.AccountID)
	}
	if loaded.AuthMode != creds.AuthMode {
		t.Errorf("AuthMode: got %q, want %q", loaded.AuthMode, creds.AuthMode)
	}
	if !loaded.ExpiresAt.Equal(creds.ExpiresAt) {
		t.Errorf("ExpiresAt: got %v, want %v", loaded.ExpiresAt, creds.ExpiresAt)
	}
}

func TestLoadCredentials_NotFound(t *testing.T) {
	_, err := LoadCredentialsFrom("/nonexistent/path/auth.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestRefreshIfNeeded_NotExpired(t *testing.T) {
	creds := &Credentials{
		AccessToken:  "still-valid",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}

	result, err := RefreshIfNeeded(creds)
	if err != nil {
		t.Fatalf("RefreshIfNeeded: %v", err)
	}
	// Should return the same credentials without making any HTTP calls.
	if result != creds {
		t.Error("expected same credentials pointer when not expired")
	}
}

func TestRefreshIfNeededRejectsInvalidCredentials(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		creds *Credentials
		want  string
	}{
		{name: "nil", want: "credentials are nil"},
		{
			name:  "empty access token",
			creds: &Credentials{RefreshToken: "refresh", ExpiresAt: now.Add(time.Hour)},
			want:  "access token is empty",
		},
		{
			name:  "expired without refresh token",
			creds: &Credentials{AccessToken: "expired", ExpiresAt: now.Add(-time.Minute)},
			want:  "refresh token is empty",
		},
		{
			name:  "unknown expiry without refresh token",
			creds: &Credentials{AccessToken: "unknown-expiry"},
			want:  "refresh token is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := refreshIfNeeded(tt.creds, now, http.DefaultClient, "https://invalid.example")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("refreshIfNeeded() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestRefreshIfNeededPreservesOmittedRotatingTokens(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	idToken := makeTestJWT(map[string]any{"account_id": "acct-1"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.FormValue("refresh_token"); got != "refresh-old" {
			t.Errorf("refresh_token = %q, want refresh-old", got)
		}
		if got := r.FormValue("client_id"); got != ClientID {
			t.Errorf("client_id = %q, want %q", got, ClientID)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-new",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	got, err := refreshIfNeeded(&Credentials{
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		IDToken:      idToken,
		AccountID:    "acct-1",
		AuthMode:     "chatgpt",
		ExpiresAt:    now.Add(-time.Minute),
	}, now, server.Client(), server.URL)
	if err != nil {
		t.Fatalf("refreshIfNeeded: %v", err)
	}
	if got.AccessToken != "access-new" {
		t.Errorf("AccessToken = %q, want access-new", got.AccessToken)
	}
	if got.RefreshToken != "refresh-old" {
		t.Errorf("RefreshToken = %q, want preserved refresh-old", got.RefreshToken)
	}
	if got.IDToken != idToken {
		t.Error("IDToken was not preserved")
	}
	if got.AccountID != "acct-1" {
		t.Errorf("AccountID = %q, want acct-1", got.AccountID)
	}
	if got.AuthMode != "chatgpt" {
		t.Errorf("AuthMode = %q, want chatgpt", got.AuthMode)
	}
	if want := now.Add(time.Hour); !got.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, want)
	}
}

func TestRefreshIfNeededAcceptsSameAccountRotation(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	idToken := makeTestJWT(map[string]any{"account_id": "acct-1"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-new",
			"refresh_token": "refresh-new",
			"id_token":      idToken,
			"expires_in":    1200,
		})
	}))
	defer server.Close()

	got, err := refreshIfNeeded(&Credentials{
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		AccountID:    "acct-1",
		ExpiresAt:    now.Add(-time.Minute),
	}, now, server.Client(), server.URL)
	if err != nil {
		t.Fatalf("refreshIfNeeded: %v", err)
	}
	if got.RefreshToken != "refresh-new" || got.IDToken != idToken || got.AccountID != "acct-1" {
		t.Fatalf("rotated credentials not retained: %#v", got)
	}
}

func TestRefreshIfNeededRejectsAccountIdentityChange(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-new",
			"id_token":     makeTestJWT(map[string]any{"account_id": "acct-2"}),
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	_, err := refreshIfNeeded(&Credentials{
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		AccountID:    "acct-1",
		ExpiresAt:    now.Add(-time.Minute),
	}, now, server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "account identity changed") {
		t.Fatalf("refreshIfNeeded() error = %v, want account identity changed", err)
	}
}

func TestRefreshIfNeededBoundsAndRedactsErrorResponses(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	baseCreds := &Credentials{
		AccessToken:  "access-old",
		RefreshToken: "refresh-secret",
		ExpiresAt:    now.Add(-time.Minute),
	}

	t.Run("redacts response body", func(t *testing.T) {
		const secret = "server-echoed-secret"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, secret, http.StatusUnauthorized)
		}))
		defer server.Close()

		_, err := refreshIfNeeded(baseCreds, now, server.Client(), server.URL)
		if err == nil {
			t.Fatal("expected refresh error")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("refresh error leaked response body: %v", err)
		}
		if !strings.Contains(err.Error(), "HTTP 401") {
			t.Fatalf("refresh error = %v, want HTTP status", err)
		}
	})

	t.Run("bounds response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("x", maxOAuthResponseBytes+1)))
		}))
		defer server.Close()

		_, err := refreshIfNeeded(baseCreds, now, server.Client(), server.URL)
		if err == nil || !strings.Contains(err.Error(), "response exceeds limit") {
			t.Fatalf("refreshIfNeeded() error = %v, want response limit error", err)
		}
	})
}

func TestRefreshIfNeededRejectsMalformedSuccessfulResponse(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "empty access token", body: `{"expires_in":3600}`, want: "access token is empty"},
		{name: "negative expiry", body: `{"access_token":"new","expires_in":-1}`, want: "expires_in is negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			_, err := refreshIfNeeded(&Credentials{
				AccessToken:  "old",
				RefreshToken: "refresh",
				ExpiresAt:    now.Add(-time.Minute),
			}, now, server.Client(), server.URL)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("refreshIfNeeded() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestRefreshIfNeededDefaultsZeroExpiry(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"new","expires_in":0}`)
	}))
	defer server.Close()

	got, err := refreshIfNeeded(&Credentials{
		AccessToken:  "old",
		RefreshToken: "refresh",
		ExpiresAt:    now.Add(-time.Minute),
	}, now, server.Client(), server.URL)
	if err != nil {
		t.Fatalf("refreshIfNeeded: %v", err)
	}
	if want := now.Add(time.Hour); !got.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, want)
	}
}

func TestRefreshIfNeededRejectsUnavailableTransport(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	creds := &Credentials{
		AccessToken:  "old",
		RefreshToken: "refresh",
		ExpiresAt:    now.Add(-time.Minute),
	}
	for _, tt := range []struct {
		name     string
		client   *http.Client
		endpoint string
	}{
		{name: "nil client", endpoint: "https://auth.invalid"},
		{name: "empty endpoint", client: http.DefaultClient},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := refreshIfNeeded(creds, now, tt.client, tt.endpoint)
			if err == nil || !strings.Contains(err.Error(), "OAuth transport is unavailable") {
				t.Fatalf("refreshIfNeeded() error = %v, want unavailable transport", err)
			}
		})
	}
}

func TestReadOAuthResponseRejectsNilAndReaderFailure(t *testing.T) {
	if _, err := readOAuthResponse(nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("readOAuthResponse(nil) error = %v", err)
	}
	_, err := readOAuthResponse(failingOAuthReader{})
	if err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("readOAuthResponse(failing) error = %v", err)
	}
}

func TestRefreshIfNeededPropagatesTransportError(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport failed")
	})}
	_, err := refreshIfNeeded(&Credentials{
		AccessToken:  "old",
		RefreshToken: "refresh",
		ExpiresAt:    now.Add(-time.Minute),
	}, now, client, "https://auth.invalid")
	if err == nil || !strings.Contains(err.Error(), "transport failed") {
		t.Fatalf("refreshIfNeeded() error = %v, want transport failure", err)
	}
}

func TestExtractAccountID_Direct(t *testing.T) {
	claims := map[string]any{
		"account_id": "acct-direct-123",
		"sub":        "user-1",
	}
	token := makeTestJWT(claims)

	id := extractAccountID(token)
	if id != "acct-direct-123" {
		t.Errorf("expected acct-direct-123, got %q", id)
	}
}

func TestExtractAccountID_Organizations(t *testing.T) {
	claims := map[string]any{
		"sub": "user-2",
		"organizations": []any{
			map[string]any{"id": "org-abc-456", "name": "My Team"},
		},
	}
	token := makeTestJWT(claims)

	id := extractAccountID(token)
	if id != "org-abc-456" {
		t.Errorf("expected org-abc-456, got %q", id)
	}
}

func TestExtractAccountID_NoMatch(t *testing.T) {
	claims := map[string]any{
		"sub": "user-3",
	}
	token := makeTestJWT(claims)

	id := extractAccountID(token)
	if id != "" {
		t.Errorf("expected empty, got %q", id)
	}
}

func TestExtractAccountID_InvalidToken(t *testing.T) {
	if id := extractAccountID("not-a-jwt"); id != "" {
		t.Errorf("expected empty for invalid token, got %q", id)
	}
	if id := extractAccountID("a.b.c"); id != "" {
		t.Errorf("expected empty for non-base64 payload, got %q", id)
	}
}

func TestGeneratePKCE(t *testing.T) {
	v, c, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE: %v", err)
	}
	if len(v) == 0 {
		t.Error("verifier should not be empty")
	}
	if len(c) == 0 {
		t.Error("challenge should not be empty")
	}
	if v == c {
		t.Error("verifier and challenge should differ (S256)")
	}

	// Generate again and verify uniqueness.
	v2, c2, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE (2nd): %v", err)
	}
	if v == v2 {
		t.Error("verifiers should be unique across calls")
	}
	if c == c2 {
		t.Error("challenges should be unique across calls")
	}
}

func TestSaveCredentials_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "deep", "nested", "auth.json")

	creds := &Credentials{AccessToken: "test", AuthMode: "chatgpt"}
	if err := SaveCredentialsTo(creds, nested); err != nil {
		t.Fatalf("SaveCredentialsTo with nested dir: %v", err)
	}

	// Verify the directory was created with correct permissions.
	info, err := os.Stat(filepath.Dir(nested))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory to be created")
	}
}

// makeTestJWT creates a minimal JWT (unsigned) with the given claims payload.
func makeTestJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(claims)
	payloadEnc := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + payloadEnc + ".signature"
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type failingOAuthReader struct{}

func (failingOAuthReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

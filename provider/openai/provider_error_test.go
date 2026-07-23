package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestReadProviderErrorClassificationBoundsAndRedacts(t *testing.T) {
	const secret = "source-fragment-must-not-escape"
	body := `{"error":{"type":"authentication_error","message":"` +
		secret + `"}}` + strings.Repeat("x", maxProviderErrorBodyBytes)
	got := readProviderErrorClassification(strings.NewReader(body))
	if strings.Contains(got, secret) {
		t.Fatalf("classification leaked provider payload: %q", got)
	}
	if !strings.Contains(got, "unauthorized") || !strings.Contains(got, "response_too_large") {
		t.Fatalf("classification = %q, want unauthorized and response_too_large", got)
	}
}

func TestReadProviderErrorClassificationRejectsUnreadableBodies(t *testing.T) {
	if got := readProviderErrorClassification(nil); got != "response_unreadable" {
		t.Fatalf("nil classification = %q", got)
	}
	if got := readProviderErrorClassification(providerErrorReader{}); got != "response_unreadable" {
		t.Fatalf("reader failure classification = %q", got)
	}
}

func TestProviderHTTPErrorRedactsResponsePayload(t *testing.T) {
	const secret = "private-source-in-provider-error"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"type":"authentication_error","message":"`+secret+`"}}`)
	}))
	defer server.Close()

	p := New(WithAPIKey("key"), WithBaseURL(server.URL), WithModel("gpt-4o"))
	_, err := p.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "sensitive request"}}},
	}, nil, nil)
	var httpErr *core.ModelHTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Request() error = %v, want ModelHTTPError", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(httpErr.Body, secret) {
		t.Fatalf("provider error leaked response payload: %#v", httpErr)
	}
	if httpErr.Body != "unauthorized" || !strings.Contains(httpErr.Message, "HTTP 401") {
		t.Fatalf("sanitized provider error = %#v", httpErr)
	}
}

func TestResponsesWebSocketErrorsRedactProviderPayload(t *testing.T) {
	const secret = "private-source-in-websocket-error"
	err := responsesWebSocketError(responsesWSEvent{
		Status:  http.StatusTooManyRequests,
		Code:    "rate_limit_exceeded",
		Message: secret,
	}, "gpt-5")
	var httpErr *core.ModelHTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("responsesWebSocketError() = %v, want ModelHTTPError", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(httpErr.Body, secret) {
		t.Fatalf("websocket error leaked response payload: %#v", httpErr)
	}
	if httpErr.Body != "rate_limited" {
		t.Fatalf("Body = %q, want rate_limited", httpErr.Body)
	}
}

type providerErrorReader struct{}

func (providerErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

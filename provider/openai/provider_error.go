package openai

import (
	"fmt"
	"io"
	"strings"

	"github.com/fugue-labs/gollem/core"
)

// Provider error payloads are untrusted and may echo request data. Keep them
// bounded for classification and expose only fixed, source-free markers.
const maxProviderErrorBodyBytes = 64 << 10

var providerErrorMarkers = []struct {
	marker string
	terms  []string
}{
	{marker: "previous_response_not_found", terms: []string{"previous_response_not_found", "previous response with id"}},
	{marker: "websocket_connection_limit_reached", terms: []string{"websocket_connection_limit_reached", "connection limit reached"}},
	{marker: "not_chat_model", terms: []string{"not a chat model"}},
	{marker: "use_responses", terms: []string{"please use /v1/responses", "use the /v1/responses", "use /v1/responses"}},
	{marker: "max_output_tokens", terms: []string{"max_output_tokens"}},
	{marker: "context_length_exceeded", terms: []string{"context_length_exceeded", "context length exceeded"}},
	{marker: "rate_limited", terms: []string{"resource_exhausted", "rate_limit", "rate limit", "too many requests"}},
	{marker: "unauthorized", terms: []string{"unauthorized", "authentication_error", "invalid_api_key"}},
	{marker: "forbidden", terms: []string{"forbidden", "permission_error"}},
	{marker: "timeout", terms: []string{"timeout", "timed out"}},
	{marker: "server_error", terms: []string{"server_error", "internal server error"}},
	{marker: "invalid_request", terms: []string{"invalid_request", "bad request"}},
	{marker: "response_incomplete", terms: []string{"response.incomplete"}},
	{marker: "response_failed", terms: []string{"response.failed"}},
}

func classifyProviderError(values ...string) string {
	combined := strings.ToLower(strings.Join(values, " "))
	markers := make([]string, 0, 2)
	for _, candidate := range providerErrorMarkers {
		for _, term := range candidate.terms {
			if strings.Contains(combined, term) {
				markers = append(markers, candidate.marker)
				break
			}
		}
	}
	if len(markers) == 0 {
		return "provider_error"
	}
	return strings.Join(markers, ",")
}

func readProviderErrorClassification(reader io.Reader) string {
	if reader == nil {
		return "response_unreadable"
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxProviderErrorBodyBytes+1))
	if err != nil {
		return "response_unreadable"
	}
	oversized := len(body) > maxProviderErrorBodyBytes
	if oversized {
		body = body[:maxProviderErrorBodyBytes]
	}
	classification := classifyProviderError(string(body))
	if oversized {
		return classification + ",response_too_large"
	}
	return classification
}

func sanitizedProviderHTTPError(prefix string, status int, classification, model string) *core.ModelHTTPError {
	if classification == "" {
		classification = "provider_error"
	}
	return &core.ModelHTTPError{
		Message:    fmt.Sprintf("%s (HTTP %d; %s)", prefix, status, classification),
		StatusCode: status,
		Body:       classification,
		ModelName:  model,
	}
}

//go:build e2e

package e2e

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/provider/anthropic"
	"github.com/fugue-labs/gollem/provider/openai"
)

// TestSmokeTextRequest verifies basic non-streaming text request for all providers.
func TestSmokeTextRequest(t *testing.T) {
	for _, p := range allProviders() {
		t.Run(p.name, func(t *testing.T) {
			skipIfNoCredentials(t, p.credEnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			model := p.newFn()

			messages := []core.ModelMessage{
				core.ModelRequest{
					Parts: []core.ModelRequestPart{
						core.UserPromptPart{Content: "Respond with exactly the word 'hello' and nothing else."},
					},
				},
			}

			resp, err := model.Request(ctx, messages, nil, nil)
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("Request failed: %v", err)
			}

			if resp == nil {
				t.Fatal("response is nil")
			}
			if len(resp.Parts) == 0 {
				t.Fatal("response has no parts")
			}

			text := resp.TextContent()
			if !strings.Contains(strings.ToLower(text), "hello") {
				t.Errorf("expected response to contain 'hello', got: %q", text)
			}

			if resp.ModelName == "" {
				t.Error("ModelName is empty")
			}

			t.Logf("Provider=%s Model=%s Text=%q InputTokens=%d OutputTokens=%d",
				p.name, resp.ModelName, text, resp.Usage.InputTokens, resp.Usage.OutputTokens)
		})
	}
}

// TestSmokeStream verifies basic streaming for all providers.
func TestSmokeStream(t *testing.T) {
	for _, p := range allProviders() {
		t.Run(p.name, func(t *testing.T) {
			skipIfNoCredentials(t, p.credEnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			model := p.newFn()

			messages := []core.ModelMessage{
				core.ModelRequest{
					Parts: []core.ModelRequestPart{
						core.UserPromptPart{Content: "Respond with exactly the word 'hello' and nothing else."},
					},
				},
			}

			stream, err := model.RequestStream(ctx, messages, nil, nil)
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("RequestStream failed: %v", err)
			}
			defer stream.Close()

			var (
				partStarts int
				partDeltas int
			)

			for {
				event, err := stream.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("stream.Next() error: %v", err)
				}
				switch event.(type) {
				case core.PartStartEvent:
					partStarts++
				case core.PartDeltaEvent:
					partDeltas++
				}
			}

			resp := stream.Response()
			if resp == nil {
				t.Fatal("stream.Response() is nil after consuming stream")
			}

			text := resp.TextContent()
			if !strings.Contains(strings.ToLower(text), "hello") {
				t.Errorf("expected streamed response to contain 'hello', got: %q", text)
			}

			if partStarts == 0 {
				t.Error("received zero PartStartEvents")
			}

			usage := stream.Usage()
			t.Logf("Provider=%s Text=%q PartStarts=%d PartDeltas=%d InputTokens=%d OutputTokens=%d",
				p.name, text, partStarts, partDeltas, usage.InputTokens, usage.OutputTokens)
		})
	}
}

// TestProviderAuthError verifies that invalid credentials produce proper errors.
func TestProviderAuthError(t *testing.T) {
	tests := []struct {
		name  string
		model core.Model
	}{
		{
			name:  "Anthropic",
			model: anthropic.New(anthropic.WithAPIKey("invalid-key"), anthropic.WithMaxTokens(10)),
		},
		{
			name:  "OpenAI",
			model: openai.New(openai.WithAPIKey("invalid-key"), openai.WithMaxTokens(10)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			messages := []core.ModelMessage{
				core.ModelRequest{
					Parts: []core.ModelRequestPart{
						core.UserPromptPart{Content: "test"},
					},
				},
			}

			_, err := tt.model.Request(ctx, messages, nil, nil)
			if err == nil {
				t.Fatal("expected error with invalid API key, got nil")
			}

			var httpErr *core.ModelHTTPError
			if !errors.As(err, &httpErr) {
				t.Logf("error type: %T, message: %v", err, err)
				// Non-HTTP errors are also acceptable (e.g., connection refused)
			} else {
				if httpErr.StatusCode < 400 {
					t.Errorf("expected HTTP error status >= 400, got %d", httpErr.StatusCode)
				}
				t.Logf("Got expected HTTP error: status=%d", httpErr.StatusCode)
			}
		})
	}
}

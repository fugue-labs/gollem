//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// Test output types.
type MathAnswer struct {
	Answer      int    `json:"answer"`
	Explanation string `json:"explanation"`
}

type SentimentResult struct {
	Sentiment  string  `json:"sentiment"`
	Confidence float64 `json:"confidence"`
}

// TestStructuredOutputToolMode tests structured output extraction (default tool mode).
func TestStructuredOutputToolMode(t *testing.T) {
	for _, p := range allProviders() {
		t.Run(p.name, func(t *testing.T) {
			skipIfNoCredentials(t, p.credEnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			agent := core.NewAgent[MathAnswer](p.newFn())

			result, err := agent.Run(ctx, "What is 15 + 27? Return the answer as a number with a brief explanation.")
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("agent.Run failed: %v", err)
			}

			if result.Output.Answer != 42 {
				t.Errorf("expected answer=42, got %d", result.Output.Answer)
			}
			if result.Output.Explanation == "" {
				t.Error("expected non-empty explanation")
			}

			t.Logf("Provider=%s Answer=%d Explanation=%q", p.name, result.Output.Answer, result.Output.Explanation)
		})
	}
}

// TestStructuredOutputWithValidator tests output validation with retry.
func TestStructuredOutputWithValidator(t *testing.T) {
	for _, p := range allProviders() {
		t.Run(p.name, func(t *testing.T) {
			skipIfNoCredentials(t, p.credEnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			agent := core.NewAgent[SentimentResult](p.newFn(),
				core.WithOutputValidator(func(ctx context.Context, rc *core.RunContext, output SentimentResult) (SentimentResult, error) {
					if output.Confidence < 0 || output.Confidence > 1 {
						return output, &core.ModelRetryError{
							Message: "confidence must be between 0 and 1",
						}
					}
					return output, nil
				}),
			)

			result, err := agent.Run(ctx, "Analyze the sentiment of: 'I love this product, it is amazing!' Return sentiment as 'positive', 'negative', or 'neutral' and confidence between 0 and 1.")
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("agent.Run failed: %v", err)
			}

			if result.Output.Sentiment != "positive" {
				t.Errorf("expected sentiment='positive', got %q", result.Output.Sentiment)
			}
			if result.Output.Confidence < 0 || result.Output.Confidence > 1 {
				t.Errorf("expected confidence in [0,1], got %f", result.Output.Confidence)
			}

			t.Logf("Provider=%s Sentiment=%q Confidence=%.2f", p.name, result.Output.Sentiment, result.Output.Confidence)
		})
	}
}

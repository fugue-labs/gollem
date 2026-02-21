//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestStructuredOutputNativeMode verifies native JSON schema output mode.
func TestStructuredOutputNativeMode(t *testing.T) {
	anthropicOnly(t)

	type Sentiment struct {
		Label      string  `json:"label" jsonschema:"enum=positive,negative,neutral"`
		Confidence float64 `json:"confidence"`
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[Sentiment](newAnthropicProvider(),
		core.WithOutputOptions[Sentiment](core.WithOutputMode(core.OutputModeNative)),
	)

	result, err := agent.Run(ctx, "Analyze sentiment: 'I love this product!'")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run failed: %v", err)
	}

	if result.Output.Label == "" {
		t.Error("expected non-empty sentiment label")
	}
	if result.Output.Confidence <= 0 || result.Output.Confidence > 1.0 {
		t.Logf("confidence value: %f (may be out of expected 0-1 range)", result.Output.Confidence)
	}

	t.Logf("Sentiment: label=%q confidence=%.2f", result.Output.Label, result.Output.Confidence)
}

// TestStructuredOutputSliceType verifies structured output with non-object types (wrapped in object).
func TestStructuredOutputSliceType(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[[]string](newAnthropicProvider())

	result, err := agent.Run(ctx, "List 3 colors. Return a JSON array of strings.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run failed: %v", err)
	}

	if len(result.Output) == 0 {
		t.Error("expected non-empty slice output")
	}

	t.Logf("Slice output: %v", result.Output)
}

// TestStructuredOutputWithRepair verifies the output repair flow.
func TestStructuredOutputWithRepair(t *testing.T) {
	anthropicOnly(t)

	type Config struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	repairCalled := false
	agent := core.NewAgent[Config](newAnthropicProvider(),
		core.WithOutputRepair(func(ctx context.Context, raw string, parseErr error) (Config, error) {
			repairCalled = true
			// Return a valid config as repair.
			return Config{Host: "repaired.example.com", Port: 8080}, nil
		}),
	)

	result, err := agent.Run(ctx, "Return a JSON object with 'host' and 'port' fields for a server config.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run failed: %v", err)
	}

	if result.Output.Host == "" {
		t.Error("expected non-empty host")
	}

	t.Logf("Output: host=%q port=%d repairCalled=%v", result.Output.Host, result.Output.Port, repairCalled)
}

// TestStructuredOutputWithValidatorRetry verifies output validators can trigger model retries.
func TestStructuredOutputWithValidatorRetry(t *testing.T) {
	anthropicOnly(t)

	type Answer struct {
		Value int `json:"value"`
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	validationCalls := 0
	agent := core.NewAgent[Answer](newAnthropicProvider(),
		core.WithOutputValidator(func(ctx context.Context, rc *core.RunContext, output Answer) (Answer, error) {
			validationCalls++
			if output.Value < 0 {
				return output, &core.ModelRetryError{Message: "Value must be non-negative"}
			}
			return output, nil
		}),
	)

	result, err := agent.Run(ctx, "Return a JSON object with 'value' set to 42.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run failed: %v", err)
	}

	if result.Output.Value != 42 {
		t.Logf("Got value %d (model may return different number)", result.Output.Value)
	}
	if validationCalls == 0 {
		t.Error("expected validator to be called at least once")
	}

	t.Logf("Value=%d ValidationCalls=%d", result.Output.Value, validationCalls)
}

//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/streamutil"
)

// TestStreamTextDelta verifies delta streaming via streamutil.
func TestStreamTextDelta(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	model := newAnthropicProvider()

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Say 'hello world'"},
			},
		},
	}

	stream, err := model.RequestStream(ctx, messages, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("RequestStream failed: %v", err)
	}
	defer stream.Close()

	var deltas []string
	for text, err := range streamutil.StreamTextDelta(stream) {
		if err != nil {
			t.Fatalf("StreamTextDelta error: %v", err)
		}
		deltas = append(deltas, text)
	}

	if len(deltas) == 0 {
		t.Error("expected at least one text delta")
	}

	// Each delta should be non-empty.
	for i, d := range deltas {
		if d == "" {
			t.Errorf("delta[%d] is empty", i)
		}
	}

	t.Logf("Received %d deltas, first: %q", len(deltas), deltas[0])
}

// TestStreamTextAccumulated verifies accumulated streaming via streamutil.
func TestStreamTextAccumulated(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	model := newAnthropicProvider()

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Say 'hello world'"},
			},
		},
	}

	stream, err := model.RequestStream(ctx, messages, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("RequestStream failed: %v", err)
	}
	defer stream.Close()

	var accumulated []string
	for text, err := range streamutil.StreamTextAccumulated(stream) {
		if err != nil {
			t.Fatalf("StreamTextAccumulated error: %v", err)
		}
		accumulated = append(accumulated, text)
	}

	if len(accumulated) == 0 {
		t.Fatal("expected at least one accumulated text")
	}

	// Each successive accumulated string should be >= previous.
	for i := 1; i < len(accumulated); i++ {
		if len(accumulated[i]) < len(accumulated[i-1]) {
			t.Errorf("accumulated[%d] (%d chars) is shorter than accumulated[%d] (%d chars)",
				i, len(accumulated[i]), i-1, len(accumulated[i-1]))
		}
	}

	// Last accumulated should be the full text.
	finalText := accumulated[len(accumulated)-1]
	t.Logf("Final accumulated text: %q (%d chunks)", finalText, len(accumulated))
}

// TestStreamTextDebounced verifies debounced streaming groups events.
func TestStreamTextDebounced(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	model := newAnthropicProvider()

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Count from 1 to 10"},
			},
		},
	}

	stream, err := model.RequestStream(ctx, messages, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("RequestStream failed: %v", err)
	}
	defer stream.Close()

	var chunks []string
	for text, err := range streamutil.StreamTextDebounced(stream, 100*time.Millisecond) {
		if err != nil {
			t.Fatalf("StreamTextDebounced error: %v", err)
		}
		chunks = append(chunks, text)
	}

	if len(chunks) == 0 {
		t.Error("expected at least one debounced chunk")
	}

	t.Logf("Debounced: %d chunks", len(chunks))
}

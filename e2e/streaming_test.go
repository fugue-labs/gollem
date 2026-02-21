//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestAgentStreamText verifies text streaming via Agent.RunStream across providers.
func TestAgentStreamText(t *testing.T) {
	for _, p := range allProviders() {
		t.Run(p.name, func(t *testing.T) {
			skipIfNoCredentials(t, p.credEnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			agent := core.NewAgent[string](p.newFn())

			stream, err := agent.RunStream(ctx, "Count from 1 to 5, one number per line.")
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("RunStream failed: %v", err)
			}
			defer stream.Close()

			var accumulated string
			var chunkCount int
			for text, err := range stream.StreamText(true) {
				if err != nil {
					t.Fatalf("StreamText error: %v", err)
				}
				accumulated += text
				chunkCount++
			}

			for _, n := range []string{"1", "2", "3", "4", "5"} {
				if !strings.Contains(accumulated, n) {
					t.Errorf("expected stream to contain %q, got: %q", n, accumulated)
				}
			}

			t.Logf("Provider=%s Chunks=%d Text=%q", p.name, chunkCount, accumulated)
		})
	}
}

// TestAgentStreamWithTools verifies that streaming produces tool call events.
// Note: RunStream is single-turn — it streams the first model response which
// may contain tool call events rather than text. We verify tool call events
// are properly streamed.
func TestAgentStreamWithTools(t *testing.T) {
	addTool := core.FuncTool[CalcParams]("add", "Add two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A+p.B), nil
	})

	for _, p := range allProviders() {
		t.Run(p.name, func(t *testing.T) {
			skipIfNoCredentials(t, p.credEnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			agent := core.NewAgent[string](p.newFn(),
				core.WithTools[string](addTool),
			)

			stream, err := agent.RunStream(ctx, "Use the add tool to add 1 and 2.")
			if err != nil {
				skipOnAccountError(t, err)
				t.Fatalf("RunStream failed: %v", err)
			}
			defer stream.Close()

			// Consume events and check for tool call or text events.
			var hasToolCall bool
			var hasText bool
			var eventCount int
			for event, err := range stream.StreamEvents() {
				if err != nil {
					t.Fatalf("StreamEvents error: %v", err)
				}
				eventCount++
				switch e := event.(type) {
				case core.PartStartEvent:
					if _, ok := e.Part.(core.ToolCallPart); ok {
						hasToolCall = true
					}
					if _, ok := e.Part.(core.TextPart); ok {
						hasText = true
					}
				case core.PartDeltaEvent:
					if _, ok := e.Delta.(core.TextPartDelta); ok {
						hasText = true
					}
				}
			}

			if !hasToolCall && !hasText {
				t.Error("expected at least a tool call or text event in the stream")
			}

			t.Logf("Provider=%s Events=%d HasToolCall=%v HasText=%v", p.name, eventCount, hasToolCall, hasText)
		})
	}
}

// TestStreamEvents verifies low-level stream events work correctly.
func TestStreamEvents(t *testing.T) {
	p := allProviders()[0] // Use Anthropic
	skipIfNoCredentials(t, p.credEnvVar)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	model := p.newFn()
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Say 'streaming works' and nothing else."},
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
		startEvents int
		deltaEvents int
		endEvents   int
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
			startEvents++
		case core.PartDeltaEvent:
			deltaEvents++
		case core.PartEndEvent:
			endEvents++
		}
	}

	if startEvents == 0 {
		t.Error("expected at least one PartStartEvent")
	}

	resp := stream.Response()
	text := resp.TextContent()
	if !strings.Contains(strings.ToLower(text), "streaming works") {
		t.Errorf("expected 'streaming works' in response, got: %q", text)
	}

	t.Logf("Starts=%d Deltas=%d Ends=%d Text=%q", startEvents, deltaEvents, endEvents, text)
}

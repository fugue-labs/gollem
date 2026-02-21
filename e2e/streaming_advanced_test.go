//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestStreamGetOutputAfterPartialConsume verifies that GetOutput works after partial consumption.
func TestStreamGetOutputAfterPartialConsume(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	stream, err := agent.RunStream(ctx, "Count from 1 to 5.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("RunStream failed: %v", err)
	}

	// Consume a few text events via StreamText.
	count := 0
	for text, err := range stream.StreamText(true) {
		if err != nil {
			t.Fatalf("StreamText error: %v", err)
		}
		count++
		if count >= 3 {
			break // Partial consumption
		}
		_ = text
	}

	// Now get the full output.
	output, err := stream.GetOutput()
	if err != nil {
		t.Fatalf("GetOutput after partial consume: %v", err)
	}

	if output == nil {
		t.Fatal("expected non-nil output from GetOutput")
	}

	t.Logf("Got output after %d partial reads: text=%q", count, output.TextContent())
}

// TestStreamWithMultipleToolCalls streams a response with tool calls.
func TestStreamWithMultipleToolCalls(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	addTool := core.FuncTool[CalcParams]("add", "Add two numbers", func(ctx context.Context, rc *core.RunContext, p CalcParams) (string, error) {
		return fmt.Sprintf("%d", p.A+p.B), nil
	})

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](addTool),
	)

	stream, err := agent.RunStream(ctx, "Use the add tool to compute 10+20.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("RunStream failed: %v", err)
	}

	// Collect all events.
	var eventTypes []string
	for event, err := range stream.StreamEvents() {
		if err != nil {
			t.Fatalf("StreamEvents error: %v", err)
		}
		switch event.(type) {
		case core.PartStartEvent:
			eventTypes = append(eventTypes, "start")
		case core.PartDeltaEvent:
			eventTypes = append(eventTypes, "delta")
		case core.PartEndEvent:
			eventTypes = append(eventTypes, "end")
		}
	}

	if len(eventTypes) == 0 {
		t.Error("expected at least some stream events")
	}

	// Should have start/delta/end cycle.
	hasStart := false
	hasEnd := false
	for _, et := range eventTypes {
		if et == "start" {
			hasStart = true
		}
		if et == "end" {
			hasEnd = true
		}
	}

	if !hasStart {
		t.Error("expected at least one PartStartEvent")
	}
	if !hasEnd {
		t.Error("expected at least one PartEndEvent")
	}

	t.Logf("Stream event types (%d total): first few: %v", len(eventTypes), eventTypes[:min(5, len(eventTypes))])
}

// TestStreamCloseEarly verifies that closing a stream early doesn't panic.
func TestStreamCloseEarly(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	stream, err := agent.RunStream(ctx, "Write a long essay about the history of computing.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("RunStream failed: %v", err)
	}

	// Read just one event then close.
	for _, err := range stream.StreamEvents() {
		if err != nil {
			break
		}
		break // Read one event then stop.
	}

	// Close the stream.
	err = stream.Close()
	if err != nil {
		t.Logf("Close returned error (may be expected): %v", err)
	}

	t.Log("Stream closed early without panic")
}

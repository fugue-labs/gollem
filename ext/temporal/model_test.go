package temporal

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestTemporalModel_PassThrough(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Hello!"))
	tm := NewTemporalModel(model, "test-agent", DefaultActivityConfig())

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello"},
			},
			Timestamp: time.Now(),
		},
	}

	// Outside a workflow, should delegate directly.
	resp, err := tm.Request(context.Background(), messages, nil, &core.ModelRequestParameters{
		AllowTextOutput: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TextContent() != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", resp.TextContent())
	}
}

func TestTemporalModel_ModelName(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Hi"))
	tm := NewTemporalModel(model, "my-agent", DefaultActivityConfig())

	if name := tm.ModelName(); name != "test-model" {
		t.Errorf("expected 'test-model', got %q", name)
	}
}

func TestTemporalModel_ActivityNames(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Hi"))
	tm := NewTemporalModel(model, "my-agent", DefaultActivityConfig())

	reqName := tm.ModelRequestActivityName()
	if reqName != "agent__my-agent__model_request" {
		t.Errorf("unexpected request activity name: %s", reqName)
	}

	streamName := tm.ModelRequestStreamActivityName()
	if streamName != "agent__my-agent__model_request_stream" {
		t.Errorf("unexpected stream activity name: %s", streamName)
	}
}

func TestTemporalModel_ModelRequestActivity(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("Activity response"))
	tm := NewTemporalModel(model, "test-agent", DefaultActivityConfig())

	params := requestParams{
		Messages: []core.ModelMessage{
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.UserPromptPart{Content: "Hello"},
				},
				Timestamp: time.Now(),
			},
		},
		Parameters: &core.ModelRequestParameters{
			AllowTextOutput: true,
		},
	}

	resp, err := tm.ModelRequestActivity(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TextContent() != "Activity response" {
		t.Errorf("expected 'Activity response', got %q", resp.TextContent())
	}
}

func TestDefaultActivityConfig(t *testing.T) {
	config := DefaultActivityConfig()
	if config.StartToCloseTimeout != 60*time.Second {
		t.Errorf("expected 60s timeout, got %v", config.StartToCloseTimeout)
	}
	if config.MaxRetries != 0 {
		t.Errorf("expected 0 max retries, got %d", config.MaxRetries)
	}
}

func TestCompletedStream(t *testing.T) {
	resp := &core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.TextPart{Content: "Completed"},
		},
		Usage: core.Usage{InputTokens: 10, OutputTokens: 5},
	}

	stream := &completedStream{response: resp}

	// First Next should return EOF.
	_, err := stream.Next()
	if err == nil {
		t.Fatal("expected EOF")
	}

	// Response should return the wrapped response.
	got := stream.Response()
	if got.TextContent() != "Completed" {
		t.Errorf("expected 'Completed', got %q", got.TextContent())
	}

	// Usage should match.
	usage := stream.Usage()
	if usage.InputTokens != 10 || usage.OutputTokens != 5 {
		t.Errorf("unexpected usage: %+v", usage)
	}

	// Close should be nil.
	if err := stream.Close(); err != nil {
		t.Errorf("unexpected close error: %v", err)
	}
}

package core

import (
	"context"
	"testing"
	"time"
)

func TestAgentMiddleware_Called(t *testing.T) {
	var called bool
	mw := func(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters, next func(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error)) (*ModelResponse, error) {
		called = true
		return next(ctx, messages, settings, params)
	}

	model := NewTestModel(TextResponse("result"))
	agent := NewAgent[string](model, WithAgentMiddleware[string](mw))

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected middleware to be called")
	}
}

func TestAgentMiddleware_ModifyRequest(t *testing.T) {
	mw := func(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters, next func(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error)) (*ModelResponse, error) {
		// Add a system prompt to messages.
		modified := make([]ModelMessage, 0, len(messages)+1)
		modified = append(modified, ModelRequest{
			Parts:     []ModelRequestPart{SystemPromptPart{Content: "injected by middleware"}},
			Timestamp: time.Now(),
		})
		modified = append(modified, messages...)
		return next(ctx, modified, settings, params)
	}

	model := NewTestModel(TextResponse("ok"))
	agent := NewAgent[string](model, WithAgentMiddleware[string](mw))

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	// Check that the model received the extra message.
	calls := model.Calls()
	if len(calls) < 1 {
		t.Fatal("expected at least 1 model call")
	}
	// First call should have more messages due to middleware injection.
	if len(calls[0].Messages) < 2 {
		t.Errorf("expected at least 2 messages (injected + original), got %d", len(calls[0].Messages))
	}
}

func TestAgentMiddleware_Chain(t *testing.T) {
	var order []string

	mw1 := func(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters, next func(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error)) (*ModelResponse, error) {
		order = append(order, "mw1-before")
		resp, err := next(ctx, messages, settings, params)
		order = append(order, "mw1-after")
		return resp, err
	}

	mw2 := func(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters, next func(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error)) (*ModelResponse, error) {
		order = append(order, "mw2-before")
		resp, err := next(ctx, messages, settings, params)
		order = append(order, "mw2-after")
		return resp, err
	}

	model := NewTestModel(TextResponse("done"))
	agent := NewAgent[string](model,
		WithAgentMiddleware[string](mw1),
		WithAgentMiddleware[string](mw2),
	)

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	// mw1 is outermost, mw2 is inner.
	expected := []string{"mw1-before", "mw2-before", "mw2-after", "mw1-after"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d entries, got %d: %v", len(expected), len(order), order)
	}
	for i, e := range expected {
		if order[i] != e {
			t.Errorf("order[%d]: expected %q, got %q", i, e, order[i])
		}
	}
}

func TestLoggingMiddleware(t *testing.T) {
	var logs []string
	logger := func(msg string) {
		logs = append(logs, msg)
	}

	model := NewTestModel(TextResponse("logged"))
	agent := NewAgent[string](model, WithAgentMiddleware[string](LoggingMiddleware(logger)))

	_, err := agent.Run(context.Background(), "test logging")
	if err != nil {
		t.Fatal(err)
	}

	if len(logs) < 2 {
		t.Errorf("expected at least 2 log entries (request + response), got %d", len(logs))
	}
}

func TestTimingMiddleware(t *testing.T) {
	var durations []time.Duration
	callback := func(d time.Duration) {
		durations = append(durations, d)
	}

	model := NewTestModel(TextResponse("timed"))
	agent := NewAgent[string](model, WithAgentMiddleware[string](TimingMiddleware(callback)))

	_, err := agent.Run(context.Background(), "test timing")
	if err != nil {
		t.Fatal(err)
	}

	if len(durations) < 1 {
		t.Fatal("expected at least 1 duration recorded")
	}
	if durations[0] <= 0 {
		t.Error("expected positive duration")
	}
}

func TestAgentMiddleware_SkipCall(t *testing.T) {
	mw := func(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters, next func(context.Context, []ModelMessage, *ModelSettings, *ModelRequestParameters) (*ModelResponse, error)) (*ModelResponse, error) {
		// Skip the actual model call and return a canned response.
		return TextResponse("intercepted"), nil
	}

	model := NewTestModel(TextResponse("should not be called"))
	agent := NewAgent[string](model, WithAgentMiddleware[string](mw))

	result, err := agent.Run(context.Background(), "test skip")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "intercepted" {
		t.Errorf("expected 'intercepted', got %q", result.Output)
	}

	// Model should not have been called.
	if len(model.Calls()) != 0 {
		t.Errorf("expected 0 model calls, got %d", len(model.Calls()))
	}
}

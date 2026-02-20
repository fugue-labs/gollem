package core

import (
	"context"
	"testing"
)

func TestAutoContext_NoCompression(t *testing.T) {
	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "short"}}},
	}

	config := &AutoContextConfig{
		MaxTokens: 10000, // way above the message size
		KeepLastN: 4,
	}

	result, err := autoCompressMessages(context.Background(), messages, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != len(messages) {
		t.Errorf("expected %d messages (no compression), got %d", len(messages), len(result))
	}
}

func TestAutoContext_CompressesOld(t *testing.T) {
	// Create enough messages to exceed the token threshold.
	var messages []ModelMessage
	for range 20 {
		messages = append(messages, ModelRequest{
			Parts: []ModelRequestPart{
				UserPromptPart{Content: "This is a fairly long user message that has many words in it to inflate the token count above our threshold"},
			},
		})
		messages = append(messages, ModelResponse{
			Parts: []ModelResponsePart{TextPart{Content: "This is a long assistant response with plenty of content to drive up the estimated token count"}},
		})
	}

	summaryModel := NewTestModel(TextResponse("Summary of the conversation so far."))

	config := &AutoContextConfig{
		MaxTokens:    100, // very low threshold to force compression
		KeepLastN:    4,
		SummaryModel: summaryModel,
	}

	result, err := autoCompressMessages(context.Background(), messages, config, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should be compressed: 1 summary + 4 recent = 5
	if len(result) != 5 {
		t.Errorf("expected 5 messages after compression, got %d", len(result))
	}
}

func TestAutoContext_KeepsRecent(t *testing.T) {
	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "old message 1 with lots of words to inflate token count"}}},
		ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "old response 1 with many tokens"}}},
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "recent 1"}}},
		ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "recent 2"}}},
	}

	summaryModel := NewTestModel(TextResponse("Summary"))
	config := &AutoContextConfig{
		MaxTokens:    5, // force compression
		KeepLastN:    2,
		SummaryModel: summaryModel,
	}

	result, err := autoCompressMessages(context.Background(), messages, config, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should be: 1 summary + 2 recent = 3
	if len(result) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result))
	}

	// Last two should be the recent messages.
	if req, ok := result[1].(ModelRequest); ok {
		if up, ok := req.Parts[0].(UserPromptPart); ok {
			if up.Content != "recent 1" {
				t.Errorf("expected 'recent 1', got %q", up.Content)
			}
		}
	}
}

func TestAutoContext_CustomModel(t *testing.T) {
	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "old message with lots of words"}}},
		ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "old response with lots of content"}}},
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "recent"}}},
		ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "recent response"}}},
	}

	customModel := NewTestModel(TextResponse("Custom summary"))
	config := &AutoContextConfig{
		MaxTokens:    5,
		KeepLastN:    2,
		SummaryModel: customModel,
	}

	result, err := autoCompressMessages(context.Background(), messages, config, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Check that the summary uses the custom model.
	if req, ok := result[0].(ModelRequest); ok {
		for _, part := range req.Parts {
			if sp, ok := part.(SystemPromptPart); ok {
				if sp.Content != "[Conversation Summary] Custom summary" {
					t.Errorf("expected custom summary, got %q", sp.Content)
				}
			}
		}
	}
}

func TestAutoContext_AgentIntegration(t *testing.T) {
	// Create a model that returns text responses.
	model := NewTestModel(TextResponse("result"))

	agent := NewAgent[string](model,
		WithAutoContext[string](AutoContextConfig{
			MaxTokens: 100000, // high threshold, no compression
			KeepLastN: 4,
		}),
	)

	result, err := agent.Run(context.Background(), "test auto context")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "result" {
		t.Errorf("expected 'result', got %q", result.Output)
	}
}

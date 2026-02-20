package core

import (
	"context"
	"testing"
	"time"
)

func TestStripSystemPrompts(t *testing.T) {
	messages := []ModelMessage{
		ModelRequest{
			Parts: []ModelRequestPart{
				SystemPromptPart{Content: "system", Timestamp: time.Now()},
				UserPromptPart{Content: "user", Timestamp: time.Now()},
			},
			Timestamp: time.Now(),
		},
		ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "response"}}},
	}

	filter := StripSystemPrompts()
	result, err := filter(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}

	// First message should have only the user prompt.
	req, ok := result[0].(ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	if len(req.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(req.Parts))
	}
	if _, ok := req.Parts[0].(UserPromptPart); !ok {
		t.Error("expected UserPromptPart after stripping system prompts")
	}
}

func TestKeepLastN(t *testing.T) {
	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "1"}}},
		ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "2"}}},
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "3"}}},
		ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "4"}}},
	}

	filter := KeepLastN(2)
	result, err := filter(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}

	// Should be the last 2 messages.
	if req, ok := result[0].(ModelRequest); !ok || req.Parts[0].(UserPromptPart).Content != "3" {
		t.Error("expected last 2 messages starting with '3'")
	}
}

func TestSummarizeHistory(t *testing.T) {
	summarizer := NewTestModel(TextResponse("conversation summary"))
	filter := SummarizeHistory(summarizer)

	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "hello"}}},
		ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "hi there"}}},
	}

	result, err := filter(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 summary message, got %d", len(result))
	}
	req, ok := result[0].(ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	sysPart, ok := req.Parts[0].(SystemPromptPart)
	if !ok {
		t.Fatal("expected SystemPromptPart")
	}
	if sysPart.Content != "[Conversation Summary] conversation summary" {
		t.Errorf("unexpected summary: %q", sysPart.Content)
	}
}

func TestChainFilters(t *testing.T) {
	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{
			SystemPromptPart{Content: "sys"},
			UserPromptPart{Content: "user"},
		}},
		ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "resp1"}}},
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "user2"}}},
		ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "resp2"}}},
	}

	// Chain: strip system prompts, then keep last 2.
	filter := ChainFilters(StripSystemPrompts(), KeepLastN(2))
	result, err := filter(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
}

func TestChainRunWithFilter(t *testing.T) {
	model1 := NewTestModel(TextResponse("step1"))
	model2 := NewTestModel(TextResponse("step2"))

	agent1 := NewAgent[string](model1)
	agent2 := NewAgent[string](model2)

	result, err := ChainRunWithFilter(
		context.Background(),
		agent1, agent2,
		"initial",
		func(output string) string { return "transform: " + output },
		KeepLastN(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "step2" {
		t.Errorf("expected 'step2', got %q", result.Output)
	}
}

func TestHandoffWithFilter(t *testing.T) {
	model1 := NewTestModel(TextResponse("first"))
	model2 := NewTestModel(TextResponse("second"))

	agent1 := NewAgent[string](model1)
	agent2 := NewAgent[string](model2)

	handoff := NewHandoff[string]()
	handoff.AddStep("step1", agent1, nil)
	handoff.AddStepWithFilter("step2", agent2,
		func(prev string) string { return "from: " + prev },
		KeepLastN(1),
	)

	result, err := handoff.Run(context.Background(), "start")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "second" {
		t.Errorf("expected 'second', got %q", result.Output)
	}
}

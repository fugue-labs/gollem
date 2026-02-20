package orchestration_test

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/orchestration"
)

func TestStripSystemPrompts(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "system", Timestamp: time.Now()},
				core.UserPromptPart{Content: "user", Timestamp: time.Now()},
			},
			Timestamp: time.Now(),
		},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "response"}}},
	}

	filter := orchestration.StripSystemPrompts()
	result, err := filter(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}

	// First message should have only the user prompt.
	req, ok := result[0].(core.ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	if len(req.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(req.Parts))
	}
	if _, ok := req.Parts[0].(core.UserPromptPart); !ok {
		t.Error("expected UserPromptPart after stripping system prompts")
	}
}

func TestKeepLastN(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "1"}}},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "2"}}},
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "3"}}},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "4"}}},
	}

	filter := orchestration.KeepLastN(2)
	result, err := filter(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}

	// Should be the last 2 messages.
	if req, ok := result[0].(core.ModelRequest); !ok || req.Parts[0].(core.UserPromptPart).Content != "3" {
		t.Error("expected last 2 messages starting with '3'")
	}
}

func TestSummarizeHistory(t *testing.T) {
	summarizer := core.NewTestModel(core.TextResponse("conversation summary"))
	filter := orchestration.SummarizeHistory(summarizer)

	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hello"}}},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "hi there"}}},
	}

	result, err := filter(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 summary message, got %d", len(result))
	}
	req, ok := result[0].(core.ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	sysPart, ok := req.Parts[0].(core.SystemPromptPart)
	if !ok {
		t.Fatal("expected SystemPromptPart")
	}
	if sysPart.Content != "[Conversation Summary] conversation summary" {
		t.Errorf("unexpected summary: %q", sysPart.Content)
	}
}

func TestChainFilters(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.SystemPromptPart{Content: "sys"},
			core.UserPromptPart{Content: "user"},
		}},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "resp1"}}},
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "user2"}}},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "resp2"}}},
	}

	// Chain: strip system prompts, then keep last 2.
	filter := orchestration.ChainFilters(orchestration.StripSystemPrompts(), orchestration.KeepLastN(2))
	result, err := filter(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
}

func TestChainRunWithFilter(t *testing.T) {
	model1 := core.NewTestModel(core.TextResponse("step1"))
	model2 := core.NewTestModel(core.TextResponse("step2"))

	agent1 := core.NewAgent[string](model1)
	agent2 := core.NewAgent[string](model2)

	result, err := orchestration.ChainRunWithFilter(
		context.Background(),
		agent1, agent2,
		"initial",
		func(output string) string { return "transform: " + output },
		orchestration.KeepLastN(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "step2" {
		t.Errorf("expected 'step2', got %q", result.Output)
	}
}

func TestHandoffWithFilter(t *testing.T) {
	model1 := core.NewTestModel(core.TextResponse("first"))
	model2 := core.NewTestModel(core.TextResponse("second"))

	agent1 := core.NewAgent[string](model1)
	agent2 := core.NewAgent[string](model2)

	handoff := orchestration.NewHandoff[string]()
	handoff.AddStep("step1", agent1, nil)
	handoff.AddStepWithFilter("step2", agent2,
		func(prev string) string { return "from: " + prev },
		orchestration.KeepLastN(1),
	)

	result, err := handoff.Run(context.Background(), "start")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "second" {
		t.Errorf("expected 'second', got %q", result.Output)
	}
}

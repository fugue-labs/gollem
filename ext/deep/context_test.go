package deep

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestContextManager_NoCompression(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("summary"))
	cm := NewContextManager(model,
		WithMaxContextTokens(100000),
		WithOffloadThreshold(20000),
	)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello"},
			},
			Timestamp: time.Now(),
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Hi there!"},
			},
		},
	}

	result, err := cm.ProcessMessages(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(messages) {
		t.Errorf("expected %d messages, got %d", len(messages), len(result))
	}
}

func TestContextManager_Tier1_OffloadLargeResults(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("summary"))

	// Set a very low offload threshold.
	cm := NewContextManager(model,
		WithMaxContextTokens(100000),
		WithOffloadThreshold(10), // 10 tokens ≈ 40 chars
	)

	// Create a tool return with large content (>200 chars so preview is truncated).
	largeContent := strings.Repeat("a", 500) // 500 chars ≈ 125 tokens, well over threshold.
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Search"},
			},
			Timestamp: time.Now(),
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{ToolName: "search", ArgsJSON: `{"q":"test"}`, ToolCallID: "tc1"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "search",
					Content:    largeContent,
					ToolCallID: "tc1",
					Timestamp:  time.Now(),
				},
			},
			Timestamp: time.Now(),
		},
	}

	result, err := cm.ProcessMessages(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The tool return should be offloaded.
	req, ok := result[2].(core.ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest at index 2")
	}
	trp, ok := req.Parts[0].(core.ToolReturnPart)
	if !ok {
		t.Fatal("expected ToolReturnPart")
	}
	content, ok := trp.Content.(string)
	if !ok {
		t.Fatal("expected string content")
	}
	if !strings.Contains(content, "offloaded") {
		t.Errorf("expected offloaded summary, got: %s", content)
	}
	if content == largeContent {
		t.Error("original large content should have been replaced with summary")
	}
	if len(content) >= len(largeContent) {
		t.Errorf("offloaded content should be shorter than original (%d >= %d)", len(content), len(largeContent))
	}
}

func TestContextManager_Tier1_WithStore(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("summary"))
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	cm := NewContextManager(model,
		WithMaxContextTokens(100000),
		WithOffloadThreshold(10),
		WithContextStore(store),
	)

	largeContent := strings.Repeat("x", 200)
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "tool1",
					Content:    largeContent,
					ToolCallID: "tc1",
					Timestamp:  time.Now(),
				},
			},
			Timestamp: time.Now(),
		},
	}

	_, err = cm.ProcessMessages(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify content was stored.
	got, err := store.Retrieve("offload_1")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got != largeContent {
		t.Error("stored content doesn't match original")
	}
}

func TestContextManager_Tier2_OffloadInputs(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("summary"))

	// Low thresholds to trigger tier 2.
	cm := NewContextManager(model,
		WithMaxContextTokens(100),
		WithOffloadThreshold(5),
		WithCompressionThreshold(0.5),
	)

	// Create messages with large tool call args that exceed the threshold.
	largeArgs := `{"data":"` + strings.Repeat("b", 200) + `"}`
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Do something"},
			},
			Timestamp: time.Now(),
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{ToolName: "tool1", ArgsJSON: largeArgs, ToolCallID: "tc1"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{ToolName: "tool1", Content: "ok", ToolCallID: "tc1", Timestamp: time.Now()},
			},
			Timestamp: time.Now(),
		},
	}

	result, err := cm.ProcessMessages(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The tool call args should be offloaded in the response.
	resp, ok := result[1].(core.ModelResponse)
	if !ok {
		t.Fatal("expected ModelResponse at index 1")
	}
	tcp, ok := resp.Parts[0].(core.ToolCallPart)
	if !ok {
		t.Fatal("expected ToolCallPart")
	}
	if strings.Contains(tcp.ArgsJSON, strings.Repeat("b", 200)) {
		t.Error("original large args should not be present")
	}
	if !strings.Contains(tcp.ArgsJSON, "_offloaded") {
		t.Errorf("expected offloaded args, got: %s", tcp.ArgsJSON)
	}
}

func TestContextManager_Tier3_Summarization(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("This is a concise summary of the conversation."))

	// Very low thresholds to force summarization.
	cm := NewContextManager(model,
		WithMaxContextTokens(20),
		WithOffloadThreshold(100000), // High so tier 1/2 don't trigger.
		WithCompressionThreshold(0.1),
	)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "You are helpful."},
				core.UserPromptPart{Content: "Tell me about Go."},
			},
			Timestamp: time.Now(),
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Go is a programming language."},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Tell me more."},
			},
			Timestamp: time.Now(),
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "It was created by Google."},
			},
		},
	}

	result, err := cm.ProcessMessages(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result should be shorter — first messages summarized.
	if len(result) >= len(messages) {
		t.Errorf("expected fewer messages after summarization, got %d (was %d)", len(result), len(messages))
	}

	// First message should contain the summary.
	req, ok := result[0].(core.ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest at index 0")
	}
	spp, ok := req.Parts[0].(core.SystemPromptPart)
	if !ok {
		t.Fatal("expected SystemPromptPart")
	}
	if !strings.Contains(spp.Content, "Conversation Summary") {
		t.Errorf("expected conversation summary, got: %s", spp.Content)
	}
}

func TestContextManager_AsHistoryProcessor(t *testing.T) {
	model := core.NewTestModel(
		// First call is from the agent; second call would be summarization (if triggered).
		core.TextResponse("Agent response"),
	)

	cm := NewContextManager(model,
		WithMaxContextTokens(100000), // High limit — no compression.
		WithOffloadThreshold(20000),
	)

	proc := cm.AsHistoryProcessor()

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Hello"},
			},
			Timestamp: time.Now(),
		},
	}

	result, err := proc(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 message, got %d", len(result))
	}
}

func TestContextManager_EmptyMessages(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("summary"))
	cm := NewContextManager(model)

	result, err := cm.ProcessMessages(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 messages for nil input, got %d", len(result))
	}
}

func TestContextManager_SmallResultsNotOffloaded(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("summary"))
	cm := NewContextManager(model,
		WithOffloadThreshold(20000),
	)

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "tool1",
					Content:    "small result",
					ToolCallID: "tc1",
					Timestamp:  time.Now(),
				},
			},
			Timestamp: time.Now(),
		},
	}

	result, err := cm.ProcessMessages(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := result[0].(core.ModelRequest)
	trp := req.Parts[0].(core.ToolReturnPart)
	if trp.Content != "small result" {
		t.Errorf("small result should not be modified, got: %v", trp.Content)
	}
}

func TestContextManager_CustomTokenCounter(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("summary"))
	custom := &fixedTokenCounter{tokensPerChar: 1} // 1 token per char.

	cm := NewContextManager(model,
		WithTokenCounter(custom),
		WithMaxContextTokens(100),
		WithOffloadThreshold(10),
	)

	// 20 chars = 20 tokens with custom counter, should trigger offload.
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "tool1",
					Content:    strings.Repeat("x", 20),
					ToolCallID: "tc1",
					Timestamp:  time.Now(),
				},
			},
			Timestamp: time.Now(),
		},
	}

	result, err := cm.ProcessMessages(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := result[0].(core.ModelRequest)
	trp := req.Parts[0].(core.ToolReturnPart)
	content, ok := trp.Content.(string)
	if !ok {
		t.Fatal("expected string content")
	}
	if !strings.Contains(content, "offloaded") {
		t.Errorf("expected offloaded content with custom counter, got: %s", content)
	}
}

// fixedTokenCounter counts 1 token per character.
type fixedTokenCounter struct {
	tokensPerChar int
}

func (f *fixedTokenCounter) CountTokens(content string) int {
	return len(content) * f.tokensPerChar
}

func (f *fixedTokenCounter) CountMessageTokens(messages []core.ModelMessage) int {
	total := 0
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.ModelRequest:
			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.SystemPromptPart:
					total += f.CountTokens(p.Content)
				case core.UserPromptPart:
					total += f.CountTokens(p.Content)
				case core.ToolReturnPart:
					total += f.CountTokens(strings.Repeat("x", len(p.Content.(string))))
				}
			}
		case core.ModelResponse:
			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.TextPart:
					total += f.CountTokens(p.Content)
				case core.ToolCallPart:
					total += f.CountTokens(p.ArgsJSON)
				}
			}
		}
	}
	return total
}

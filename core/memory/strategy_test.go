package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/memory"
)

func makeMessages(n int) []core.ModelMessage {
	messages := make([]core.ModelMessage, 0, n)
	for i := range n {
		if i%2 == 0 {
			messages = append(messages, core.ModelRequest{
				Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "msg-" + string(rune('A'+i)), Timestamp: time.Now()}},
				Timestamp: time.Now(),
			})
		} else {
			messages = append(messages, core.ModelResponse{
				Parts:     []core.ModelResponsePart{core.TextPart{Content: "resp-" + string(rune('A'+i))}},
				Timestamp: time.Now(),
			})
		}
	}
	return messages
}

// msgText extracts a comparable string from a message for testing.
func msgText(msg core.ModelMessage) string {
	switch m := msg.(type) {
	case core.ModelRequest:
		for _, p := range m.Parts {
			if up, ok := p.(core.UserPromptPart); ok {
				return up.Content
			}
			if sp, ok := p.(core.SystemPromptPart); ok {
				return sp.Content
			}
		}
	case core.ModelResponse:
		return m.TextContent()
	}
	return ""
}

func TestSlidingWindowMemory(t *testing.T) {
	proc := memory.SlidingWindowMemory(2)

	// 10 messages: first + last 4 = 5
	messages := makeMessages(10)
	result, err := proc(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(result))
	}

	// First message should be preserved.
	if msgText(result[0]) != msgText(messages[0]) {
		t.Error("first message not preserved")
	}

	// Last 4 should be the tail.
	for i := 1; i < len(result); i++ {
		expected := messages[len(messages)-4+i-1]
		if msgText(result[i]) != msgText(expected) {
			t.Errorf("message %d mismatch", i)
		}
	}
}

func TestSlidingWindowMemory_PreservesFirst(t *testing.T) {
	proc := memory.SlidingWindowMemory(2)

	messages := makeMessages(10)
	result, err := proc(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}

	// First message should always be the original first.
	if msgText(result[0]) != msgText(messages[0]) {
		t.Error("first message was not preserved")
	}
}

func TestSlidingWindowMemory_SmallConversation(t *testing.T) {
	proc := memory.SlidingWindowMemory(5)

	messages := makeMessages(4) // Under window
	result, err := proc(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != len(messages) {
		t.Errorf("expected %d messages (unchanged), got %d", len(messages), len(result))
	}
}

func TestTokenBudgetMemory(t *testing.T) {
	proc := memory.TokenBudgetMemory(50) // Very tight budget

	// Create messages with known content length.
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.SystemPromptPart{Content: "You are helpful.", Timestamp: time.Now()},
			core.UserPromptPart{Content: "Hello", Timestamp: time.Now()},
		}, Timestamp: time.Now()},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "Hi there! How can I help you today? I am here to assist with anything."}}, Timestamp: time.Now()},
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Tell me about Go programming language and its features.", Timestamp: time.Now()}}, Timestamp: time.Now()},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "Go is a statically typed language designed for simplicity and performance."}}, Timestamp: time.Now()},
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Thanks", Timestamp: time.Now()}}, Timestamp: time.Now()},
	}

	result, err := proc(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}

	// Should have dropped some messages.
	if len(result) >= len(messages) {
		t.Errorf("expected fewer messages, got %d", len(result))
	}

	// First and last should be preserved.
	if msgText(result[0]) != msgText(messages[0]) {
		t.Error("first message not preserved")
	}
	if msgText(result[len(result)-1]) != msgText(messages[len(messages)-1]) {
		t.Error("last message not preserved")
	}
}

func TestTokenBudgetMemory_SmallConversation(t *testing.T) {
	proc := memory.TokenBudgetMemory(10000) // Very generous budget

	messages := makeMessages(4)
	result, err := proc(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != len(messages) {
		t.Errorf("expected %d messages (unchanged), got %d", len(messages), len(result))
	}
}

func TestSummaryMemory(t *testing.T) {
	// Create a summarizer model that returns a canned summary.
	summarizer := core.NewTestModel(core.TextResponse("User asked about Go and got a helpful response."))

	proc := memory.SummaryMemory(summarizer, 4)

	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.SystemPromptPart{Content: "You are helpful.", Timestamp: time.Now()},
			core.UserPromptPart{Content: "Tell me about Go", Timestamp: time.Now()},
		}, Timestamp: time.Now()},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "Go is great!"}}, Timestamp: time.Now()},
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "What about concurrency?", Timestamp: time.Now()}}, Timestamp: time.Now()},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "Goroutines are lightweight threads."}}, Timestamp: time.Now()},
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "And channels?", Timestamp: time.Now()}}, Timestamp: time.Now()},
		core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: "Channels enable safe communication."}}, Timestamp: time.Now()},
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "Thanks!", Timestamp: time.Now()}}, Timestamp: time.Now()},
	}

	result, err := proc(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}

	// Should be shorter than original.
	if len(result) >= len(messages) {
		t.Errorf("expected fewer messages after summary, got %d (was %d)", len(result), len(messages))
	}

	// First message should be preserved.
	if msgText(result[0]) != msgText(messages[0]) {
		t.Error("first message not preserved")
	}

	// Should contain a summary system prompt.
	found := false
	for _, msg := range result {
		if req, ok := msg.(core.ModelRequest); ok {
			for _, part := range req.Parts {
				if sp, ok := part.(core.SystemPromptPart); ok {
					if len(sp.Content) > 0 {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Error("expected summary system prompt in result")
	}

	// Summarizer model should have been called.
	if len(summarizer.Calls()) == 0 {
		t.Error("summarizer was not called")
	}
}

func TestSummaryMemory_ShortConversation(t *testing.T) {
	summarizer := core.NewTestModel(core.TextResponse("summary"))
	proc := memory.SummaryMemory(summarizer, 10)

	messages := makeMessages(4) // Under limit
	result, err := proc(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != len(messages) {
		t.Errorf("expected %d messages (unchanged), got %d", len(messages), len(result))
	}

	// Summarizer should NOT have been called.
	if len(summarizer.Calls()) != 0 {
		t.Error("summarizer should not be called for short conversations")
	}
}

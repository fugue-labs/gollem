package deep

import (
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestDefaultTokenCounter(t *testing.T) {
	tc := DefaultTokenCounter()

	tests := []struct {
		name    string
		content string
		wantMin int
		wantMax int
	}{
		{"empty", "", 0, 0},
		{"short", "hi", 1, 1},
		{"medium", "hello world this is a test", 6, 7},
		{"long", string(make([]byte, 400)), 99, 101},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tc.CountTokens(tt.content)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("CountTokens(%q) = %d, want between %d and %d", tt.content, got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestCountMessageTokens(t *testing.T) {
	tc := DefaultTokenCounter()

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "You are a helpful assistant."},
				core.UserPromptPart{Content: "What is the weather?"},
			},
			Timestamp: time.Now(),
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "The weather is sunny today."},
			},
		},
	}

	total := tc.CountMessageTokens(messages)
	if total <= 0 {
		t.Errorf("expected positive token count, got %d", total)
	}
	// Rough check: 3 strings of moderate length should be >10 tokens.
	if total < 10 {
		t.Errorf("expected at least 10 tokens for 3 text parts, got %d", total)
	}
}

func TestCountMessageTokens_ToolParts(t *testing.T) {
	tc := DefaultTokenCounter()

	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "search",
					ArgsJSON:   `{"query": "test search query"}`,
					ToolCallID: "tc1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "search",
					Content:    "Search results here with some content",
					ToolCallID: "tc1",
					Timestamp:  time.Now(),
				},
			},
			Timestamp: time.Now(),
		},
	}

	total := tc.CountMessageTokens(messages)
	if total <= 0 {
		t.Errorf("expected positive token count for tool parts, got %d", total)
	}
}

func TestCountMessageTokens_ThinkingParts(t *testing.T) {
	tc := DefaultTokenCounter()

	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ThinkingPart{Content: "Let me think about this step by step..."},
				core.TextPart{Content: "Here's my answer."},
			},
		},
	}

	total := tc.CountMessageTokens(messages)
	if total <= 0 {
		t.Errorf("expected positive token count for thinking parts, got %d", total)
	}
}

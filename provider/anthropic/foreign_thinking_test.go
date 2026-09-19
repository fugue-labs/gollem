package anthropic

import (
	"testing"

	"github.com/fugue-labs/gollem/core"
)

// Thinking produced by another provider (for example DeepSeek
// reasoning_content) carries no signature and must not be replayed to Claude,
// which rejects unsigned thinking blocks. Claude's own signed thinking is kept.
func TestBuildRequestDropsUnsignedThinking(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hi"}}},
		core.ModelResponse{Parts: []core.ModelResponsePart{
			core.ThinkingPart{Content: "foreign reasoning"},
			core.ThinkingPart{Content: "claude reasoning", Signature: "sig"},
			core.TextPart{Content: "answer"},
		}},
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "next"}}},
	}
	req, err := buildRequest(messages, nil, nil, ClaudeOpus5, 4096, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var thinking []string
	for _, msg := range req.Messages {
		for _, block := range msg.Content {
			if block.Type == "thinking" {
				if block.Signature == "" {
					t.Fatalf("unsigned thinking block replayed: %#v", block)
				}
				thinking = append(thinking, block.Thinking)
			}
		}
	}
	if len(thinking) != 1 || thinking[0] != "claude reasoning" {
		t.Fatalf("replayed thinking = %v, want only the signed block", thinking)
	}
}

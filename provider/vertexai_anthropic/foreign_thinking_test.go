package vertexai_anthropic

import (
	"testing"

	"github.com/fugue-labs/gollem/core"
)

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
	req, err := buildRequest(messages, nil, nil, ClaudeSonnet46, 4096, false)
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

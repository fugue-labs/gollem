package deep

import (
	"fmt"

	"github.com/fugue-labs/gollem/core"
)

// TokenCounter estimates token counts for messages.
type TokenCounter interface {
	CountTokens(content string) int
	CountMessageTokens(messages []core.ModelMessage) int
}

// defaultTokenCounter uses a simple heuristic (~4 chars per token).
type defaultTokenCounter struct{}

// DefaultTokenCounter returns a heuristic token counter (~4 chars per token).
func DefaultTokenCounter() TokenCounter {
	return &defaultTokenCounter{}
}

func (d *defaultTokenCounter) CountTokens(content string) int {
	if len(content) == 0 {
		return 0
	}
	// Approximate: 1 token per 4 characters, minimum 1.
	tokens := len(content) / 4
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}

func (d *defaultTokenCounter) CountMessageTokens(messages []core.ModelMessage) int {
	total := 0
	for _, msg := range messages {
		total += d.countMessage(msg)
	}
	return total
}

func (d *defaultTokenCounter) countMessage(msg core.ModelMessage) int {
	tokens := 0
	switch m := msg.(type) {
	case core.ModelRequest:
		for _, part := range m.Parts {
			tokens += d.countRequestPart(part)
		}
	case core.ModelResponse:
		for _, part := range m.Parts {
			tokens += d.countResponsePart(part)
		}
	}
	return tokens
}

func (d *defaultTokenCounter) countRequestPart(part core.ModelRequestPart) int {
	switch p := part.(type) {
	case core.SystemPromptPart:
		return d.CountTokens(p.Content)
	case core.UserPromptPart:
		return d.CountTokens(p.Content)
	case core.ToolReturnPart:
		return d.CountTokens(fmt.Sprintf("%v", p.Content))
	case core.RetryPromptPart:
		return d.CountTokens(p.Content)
	default:
		return 0
	}
}

func (d *defaultTokenCounter) countResponsePart(part core.ModelResponsePart) int {
	switch p := part.(type) {
	case core.TextPart:
		return d.CountTokens(p.Content)
	case core.ToolCallPart:
		return d.CountTokens(p.ArgsJSON)
	case core.ThinkingPart:
		return d.CountTokens(p.Content)
	default:
		return 0
	}
}

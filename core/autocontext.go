package core

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AutoContextConfig configures automatic context window management.
type AutoContextConfig struct {
	MaxTokens    int   // maximum token estimate before summarization
	KeepLastN    int   // number of recent messages to always keep (default: 4)
	SummaryModel Model // model to use for summarization (optional, uses agent model if nil)
}

// WithAutoContext enables automatic context window management.
// When estimated tokens exceed MaxTokens, older messages are summarized.
func WithAutoContext[T any](config AutoContextConfig) AgentOption[T] {
	if config.KeepLastN <= 0 {
		config.KeepLastN = 4
	}
	return func(a *Agent[T]) {
		a.autoContext = &config
	}
}

// estimateTokens estimates the token count of messages using a simple word-based heuristic.
// Uses ~1.3 tokens per word as a rough approximation.
func estimateTokens(messages []ModelMessage) int {
	total := 0
	for _, msg := range messages {
		switch m := msg.(type) {
		case ModelRequest:
			for _, part := range m.Parts {
				switch p := part.(type) {
				case SystemPromptPart:
					total += estimateStringTokens(p.Content)
				case UserPromptPart:
					total += estimateStringTokens(p.Content)
				case ToolReturnPart:
					if s, ok := p.Content.(string); ok {
						total += estimateStringTokens(s)
					}
				case RetryPromptPart:
					total += estimateStringTokens(p.Content)
				}
			}
		case ModelResponse:
			for _, part := range m.Parts {
				switch p := part.(type) {
				case TextPart:
					total += estimateStringTokens(p.Content)
				case ToolCallPart:
					total += estimateStringTokens(p.ArgsJSON)
				}
			}
		}
	}
	return total
}

// estimateStringTokens estimates token count for a string.
func estimateStringTokens(s string) int {
	words := len(strings.Fields(s))
	// ~1.3 tokens per word
	return int(float64(words) * 1.3)
}

// autoCompressMessages summarizes old messages to fit within the token budget.
func autoCompressMessages(ctx context.Context, messages []ModelMessage, config *AutoContextConfig, fallbackModel Model) ([]ModelMessage, error) {
	estimated := estimateTokens(messages)
	if estimated <= config.MaxTokens {
		return messages, nil
	}

	// Keep the last N messages.
	keepN := config.KeepLastN
	if keepN >= len(messages) {
		return messages, nil // can't compress further
	}

	oldMessages := messages[:len(messages)-keepN]
	recentMessages := messages[len(messages)-keepN:]

	// Build a summary of old messages.
	summaryModel := config.SummaryModel
	if summaryModel == nil {
		summaryModel = fallbackModel
	}

	// Build summary prompt.
	var sb strings.Builder
	sb.WriteString("Summarize this conversation concisely, preserving key information:\n\n")
	for _, msg := range oldMessages {
		switch m := msg.(type) {
		case ModelRequest:
			for _, part := range m.Parts {
				if up, ok := part.(UserPromptPart); ok {
					fmt.Fprintf(&sb, "User: %s\n", up.Content)
				}
			}
		case ModelResponse:
			if text := m.TextContent(); text != "" {
				fmt.Fprintf(&sb, "Assistant: %s\n", text)
			}
		}
	}

	summaryReq := []ModelMessage{
		ModelRequest{
			Parts:     []ModelRequestPart{UserPromptPart{Content: sb.String(), Timestamp: time.Now()}},
			Timestamp: time.Now(),
		},
	}

	summaryResp, err := summaryModel.Request(ctx, summaryReq, nil, &ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		return messages, err
	}

	// Build new message list with summary + recent messages.
	summaryMsg := ModelRequest{
		Parts: []ModelRequestPart{
			SystemPromptPart{
				Content:   "[Conversation Summary] " + summaryResp.TextContent(),
				Timestamp: time.Now(),
			},
		},
		Timestamp: time.Now(),
	}

	result := make([]ModelMessage, 0, 1+len(recentMessages))
	result = append(result, summaryMsg)
	result = append(result, recentMessages...)
	return result, nil
}

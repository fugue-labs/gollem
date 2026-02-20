package core

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SlidingWindowMemory keeps only the last N message pairs (request + response).
// Always preserves the first message (system prompt) and the last message.
func SlidingWindowMemory(windowSize int) HistoryProcessor {
	return func(ctx context.Context, messages []ModelMessage) ([]ModelMessage, error) {
		if len(messages) <= windowSize*2+1 {
			// Under the window, return as-is.
			return messages, nil
		}

		// Keep first message (system prompt/initial request), then last windowSize*2 messages.
		result := make([]ModelMessage, 0, windowSize*2+1)
		result = append(result, messages[0])
		start := len(messages) - windowSize*2
		if start < 1 {
			start = 1
		}
		result = append(result, messages[start:]...)
		return result, nil
	}
}

// TokenBudgetMemory keeps messages within an approximate token budget,
// dropping the oldest messages (after the system prompt) first.
// Token estimation: ~4 characters per token.
func TokenBudgetMemory(maxTokens int) HistoryProcessor {
	return func(ctx context.Context, messages []ModelMessage) ([]ModelMessage, error) {
		total := estimateMessageTokens(messages)
		if total <= maxTokens {
			return messages, nil
		}

		// Always keep first and last messages.
		if len(messages) <= 2 {
			return messages, nil
		}

		// Drop messages from position 1 until under budget.
		result := make([]ModelMessage, len(messages))
		copy(result, messages)

		for len(result) > 2 && estimateMessageTokens(result) > maxTokens {
			result = append(result[:1], result[2:]...)
		}

		return result, nil
	}
}

// estimateMessageTokens estimates total tokens in a message list.
// Uses a simple heuristic of ~4 characters per token.
func estimateMessageTokens(messages []ModelMessage) int {
	total := 0
	for _, msg := range messages {
		switch m := msg.(type) {
		case ModelRequest:
			for _, part := range m.Parts {
				switch p := part.(type) {
				case SystemPromptPart:
					total += len(p.Content) / 4
				case UserPromptPart:
					total += len(p.Content) / 4
				case ToolReturnPart:
					if s, ok := p.Content.(string); ok {
						total += len(s) / 4
					} else {
						total += 50 // estimate for structured content
					}
				case RetryPromptPart:
					total += len(p.Content) / 4
				}
			}
		case ModelResponse:
			for _, part := range m.Parts {
				switch p := part.(type) {
				case TextPart:
					total += len(p.Content) / 4
				case ToolCallPart:
					total += len(p.ArgsJSON) / 4
				}
			}
		}
	}
	if total == 0 {
		total = 1
	}
	return total
}

// SummaryMemory summarizes older messages when the conversation exceeds
// maxMessages, using the provided model to generate summaries. The summary
// replaces the dropped messages as a system prompt.
func SummaryMemory(summarizer Model, maxMessages int) HistoryProcessor {
	return func(ctx context.Context, messages []ModelMessage) ([]ModelMessage, error) {
		if len(messages) <= maxMessages {
			return messages, nil
		}

		// Split: keep first message, summarize middle, keep last maxMessages/2 messages.
		keepLast := maxMessages / 2
		if keepLast < 1 {
			keepLast = 1
		}
		if keepLast >= len(messages) {
			return messages, nil
		}

		toSummarize := messages[1 : len(messages)-keepLast]
		if len(toSummarize) == 0 {
			return messages, nil
		}

		// Build a summarization prompt.
		var sb strings.Builder
		for _, msg := range toSummarize {
			switch m := msg.(type) {
			case ModelRequest:
				for _, part := range m.Parts {
					if up, ok := part.(UserPromptPart); ok {
						sb.WriteString("User: ")
						sb.WriteString(up.Content)
						sb.WriteString("\n")
					}
				}
			case ModelResponse:
				text := m.TextContent()
				if text != "" {
					sb.WriteString("Assistant: ")
					sb.WriteString(text)
					sb.WriteString("\n")
				}
			}
		}
		summaryText := sb.String()

		// Ask the summarizer model to summarize.
		summaryReq := ModelRequest{
			Parts: []ModelRequestPart{
				UserPromptPart{
					Content:   "Summarize this conversation concisely:\n" + summaryText,
					Timestamp: time.Now(),
				},
			},
			Timestamp: time.Now(),
		}

		resp, err := summarizer.Request(ctx, []ModelMessage{summaryReq}, nil, &ModelRequestParameters{
			AllowTextOutput: true,
		})
		if err != nil {
			return nil, fmt.Errorf("summary memory: %w", err)
		}

		summary := resp.TextContent()
		if summary == "" {
			summary = "(conversation summary unavailable)"
		}

		// Reconstruct: first message + summary as system prompt + recent messages.
		result := make([]ModelMessage, 0, keepLast+2)
		result = append(result, messages[0])
		result = append(result, ModelRequest{
			Parts: []ModelRequestPart{
				SystemPromptPart{
					Content:   "[Conversation Summary] " + summary,
					Timestamp: time.Now(),
				},
			},
			Timestamp: time.Now(),
		})
		result = append(result, messages[len(messages)-keepLast:]...)

		return result, nil
	}
}

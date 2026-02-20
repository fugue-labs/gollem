package core

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// HandoffFilter transforms messages at agent handoff boundaries.
type HandoffFilter func(ctx context.Context, messages []ModelMessage) ([]ModelMessage, error)

// StripSystemPrompts removes all system prompt parts from messages.
func StripSystemPrompts() HandoffFilter {
	return func(_ context.Context, messages []ModelMessage) ([]ModelMessage, error) {
		result := make([]ModelMessage, 0, len(messages))
		for _, msg := range messages {
			if req, ok := msg.(ModelRequest); ok {
				var filtered []ModelRequestPart
				for _, part := range req.Parts {
					if _, isSys := part.(SystemPromptPart); !isSys {
						filtered = append(filtered, part)
					}
				}
				if len(filtered) > 0 {
					result = append(result, ModelRequest{Parts: filtered, Timestamp: req.Timestamp})
				}
			} else {
				result = append(result, msg)
			}
		}
		return result, nil
	}
}

// KeepLastN keeps only the last N messages.
func KeepLastN(n int) HandoffFilter {
	return func(_ context.Context, messages []ModelMessage) ([]ModelMessage, error) {
		if len(messages) <= n {
			return messages, nil
		}
		return messages[len(messages)-n:], nil
	}
}

// SummarizeHistory uses a model to summarize the conversation before handoff.
func SummarizeHistory(summarizer Model) HandoffFilter {
	return func(ctx context.Context, messages []ModelMessage) ([]ModelMessage, error) {
		// Build a summary prompt from the conversation.
		var sb strings.Builder
		for _, msg := range messages {
			if req, ok := msg.(ModelRequest); ok {
				for _, part := range req.Parts {
					if up, ok := part.(UserPromptPart); ok {
						sb.WriteString("User: ")
						sb.WriteString(up.Content)
						sb.WriteString("\n")
					}
				}
			} else if resp, ok := msg.(ModelResponse); ok {
				text := resp.TextContent()
				if text != "" {
					sb.WriteString("Assistant: ")
					sb.WriteString(text)
					sb.WriteString("\n")
				}
			}
		}
		content := sb.String()

		if content == "" {
			return messages, nil
		}

		summaryReq := ModelRequest{
			Parts: []ModelRequestPart{
				UserPromptPart{
					Content:   "Summarize this conversation concisely:\n" + content,
					Timestamp: time.Now(),
				},
			},
			Timestamp: time.Now(),
		}
		resp, err := summarizer.Request(ctx, []ModelMessage{summaryReq}, nil, &ModelRequestParameters{
			AllowTextOutput: true,
		})
		if err != nil {
			return nil, fmt.Errorf("summarize history: %w", err)
		}

		summary := resp.TextContent()
		return []ModelMessage{
			ModelRequest{
				Parts: []ModelRequestPart{
					SystemPromptPart{Content: "[Conversation Summary] " + summary, Timestamp: time.Now()},
				},
				Timestamp: time.Now(),
			},
		}, nil
	}
}

// ChainFilters applies multiple filters in sequence.
func ChainFilters(filters ...HandoffFilter) HandoffFilter {
	return func(ctx context.Context, messages []ModelMessage) ([]ModelMessage, error) {
		var err error
		for _, f := range filters {
			messages, err = f(ctx, messages)
			if err != nil {
				return nil, err
			}
		}
		return messages, nil
	}
}

// ChainRunWithFilter is like ChainRun but applies a filter to messages between agents.
func ChainRunWithFilter[A, B any](ctx context.Context, first *Agent[A], second *Agent[B], prompt string, transform func(A) string, filter HandoffFilter, opts ...RunOption) (*RunResult[B], error) {
	firstResult, err := first.Run(ctx, prompt, opts...)
	if err != nil {
		return nil, err
	}

	// Apply filter to messages.
	filteredMessages, err := filter(ctx, firstResult.Messages)
	if err != nil {
		return nil, fmt.Errorf("handoff filter: %w", err)
	}

	secondPrompt := transform(firstResult.Output)
	secondOpts := append([]RunOption{WithMessages(filteredMessages...)}, opts...)
	secondResult, err := second.Run(ctx, secondPrompt, secondOpts...)
	if err != nil {
		return nil, err
	}

	secondResult.Usage.IncrRun(firstResult.Usage)
	return secondResult, nil
}

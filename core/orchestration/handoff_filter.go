package orchestration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// HandoffFilter transforms messages at agent handoff boundaries.
type HandoffFilter func(ctx context.Context, messages []core.ModelMessage) ([]core.ModelMessage, error)

// StripSystemPrompts removes all system prompt parts from messages.
func StripSystemPrompts() HandoffFilter {
	return func(_ context.Context, messages []core.ModelMessage) ([]core.ModelMessage, error) {
		result := make([]core.ModelMessage, 0, len(messages))
		for _, msg := range messages {
			if req, ok := msg.(core.ModelRequest); ok {
				var filtered []core.ModelRequestPart
				for _, part := range req.Parts {
					if _, isSys := part.(core.SystemPromptPart); !isSys {
						filtered = append(filtered, part)
					}
				}
				if len(filtered) > 0 {
					result = append(result, core.ModelRequest{Parts: filtered, Timestamp: req.Timestamp})
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
	return func(_ context.Context, messages []core.ModelMessage) ([]core.ModelMessage, error) {
		if len(messages) <= n {
			return messages, nil
		}
		return messages[len(messages)-n:], nil
	}
}

// SummarizeHistory uses a model to summarize the conversation before handoff.
func SummarizeHistory(summarizer core.Model) HandoffFilter {
	return func(ctx context.Context, messages []core.ModelMessage) ([]core.ModelMessage, error) {
		// Build a summary prompt from the conversation.
		var sb strings.Builder
		for _, msg := range messages {
			if req, ok := msg.(core.ModelRequest); ok {
				for _, part := range req.Parts {
					if up, ok := part.(core.UserPromptPart); ok {
						sb.WriteString("User: ")
						sb.WriteString(up.Content)
						sb.WriteString("\n")
					}
				}
			} else if resp, ok := msg.(core.ModelResponse); ok {
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

		summaryReq := core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{
					Content:   "Summarize this conversation concisely:\n" + content,
					Timestamp: time.Now(),
				},
			},
			Timestamp: time.Now(),
		}
		resp, err := summarizer.Request(ctx, []core.ModelMessage{summaryReq}, nil, &core.ModelRequestParameters{
			AllowTextOutput: true,
		})
		if err != nil {
			return nil, fmt.Errorf("summarize history: %w", err)
		}

		summary := resp.TextContent()
		return []core.ModelMessage{
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.SystemPromptPart{Content: "[Conversation Summary] " + summary, Timestamp: time.Now()},
				},
				Timestamp: time.Now(),
			},
		}, nil
	}
}

// ChainFilters applies multiple filters in sequence.
func ChainFilters(filters ...HandoffFilter) HandoffFilter {
	return func(ctx context.Context, messages []core.ModelMessage) ([]core.ModelMessage, error) {
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
func ChainRunWithFilter[A, B any](ctx context.Context, first *core.Agent[A], second *core.Agent[B], prompt string, transform func(A) string, filter HandoffFilter, opts ...core.RunOption) (*core.RunResult[B], error) {
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
	secondOpts := append([]core.RunOption{core.WithMessages(filteredMessages...)}, opts...)
	secondResult, err := second.Run(ctx, secondPrompt, secondOpts...)
	if err != nil {
		return nil, err
	}

	secondResult.Usage.IncrRun(firstResult.Usage)
	return secondResult, nil
}

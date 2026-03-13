package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
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

// AutoCompressMessages exposes the agent's auto-context compaction logic for
// alternative execution backends that need to preserve the same behavior.
func AutoCompressMessages(ctx context.Context, messages []ModelMessage, config *AutoContextConfig, fallbackModel Model, tokenCount int) ([]ModelMessage, error) {
	return autoCompressMessages(ctx, messages, config, fallbackModel, tokenCount)
}

// EstimateTokens estimates the token count of messages using a simple word-based heuristic.
// Uses ~1.3 tokens per word as a rough approximation.
// Exported so that middleware (e.g., ContextOverflowMiddleware) can compute
// before/after token counts for compaction reporting.
func EstimateTokens(messages []ModelMessage) int {
	return estimateTokens(messages)
}

// estimateTokens is the internal implementation.
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
				case ThinkingPart:
					total += estimateStringTokens(p.Content)
				}
			}
		}
	}
	return total
}

// truncateStr truncates a string to at most maxBytes without splitting
// multi-byte UTF-8 characters. Returns the original string if it fits.
func truncateStr(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes] + "..."
}

// estimateStringTokens estimates token count for a string.
func estimateStringTokens(s string) int {
	words := len(strings.Fields(s))
	// ~1.3 tokens per word
	return int(float64(words) * 1.3)
}

// currentContextTokenCount returns the best available size for the current
// message history. Provider-reported input tokens are more accurate for the
// last request, but state.messages may have grown since then, so we never let
// the current history estimate fall below the actual in-memory messages.
func currentContextTokenCount(messages []ModelMessage, lastInputTokens int) int {
	estimated := estimateTokens(messages)
	if lastInputTokens > estimated {
		return lastInputTokens
	}
	return estimated
}

// autoCompressMessages summarizes old messages to fit within the token budget.
// tokenCount is the current context size — either a real provider count from
// the last model response or a heuristic estimate on the first turn.
func autoCompressMessages(ctx context.Context, messages []ModelMessage, config *AutoContextConfig, fallbackModel Model, tokenCount int) ([]ModelMessage, error) {
	if tokenCount <= config.MaxTokens {
		return messages, nil
	}

	// Keep the last N messages AND the first message (which contains the
	// task description, system prompt, and environment context). Losing
	// the original task requirements is a common failure mode.
	keepN := config.KeepLastN
	if keepN >= len(messages) {
		return messages, nil // can't compress further
	}

	// Always preserve the first message (task + system prompt).
	// Summarize messages[1:startRecent], keep messages[0] and messages[startRecent:].
	firstMsg := messages[0]

	// Determine where recent messages start. The summary is emitted as a
	// ModelResponse (assistant role), so recentMessages must start with a
	// ModelRequest (user role) to maintain proper user/assistant alternation.
	// This is required by Anthropic's API and is good practice in general.
	startRecent := len(messages) - keepN
	if startRecent > 1 {
		if _, isResp := messages[startRecent].(ModelResponse); isResp {
			startRecent--
		}
	}

	oldMessages := messages[1:startRecent]
	recentMessages := messages[startRecent:]

	if len(oldMessages) == 0 {
		return messages, nil // nothing to compress
	}

	// Build a summary of old messages.
	summaryModel := config.SummaryModel
	if summaryModel == nil {
		summaryModel = fallbackModel
	}

	// Build summary prompt that includes tool calls and results,
	// not just user/assistant text. This preserves critical context
	// about what files were modified, what tests were run, and what
	// errors occurred.
	var sb strings.Builder
	sb.WriteString("Summarize this conversation concisely, preserving:\n")
	sb.WriteString("- What files were created, edited, or read (include EXACT file paths)\n")
	sb.WriteString("- What commands were run and their results (especially test results with pass/fail COUNTS and specific error messages)\n")
	sb.WriteString("- Key decisions made and current approach being used\n")
	sb.WriteString("- Any constraints or requirements discovered (include exact values: sizes, thresholds, formats)\n")
	sb.WriteString("- What approaches were tried and whether they succeeded or failed (include WHY they failed)\n")
	sb.WriteString("- BEST RESULT SO FAR: what was the best test pass count or closest-to-correct output, and what exact code/config produced it\n")
	sb.WriteString("- Current state: what's done, what's remaining, and what the NEXT STEP should be\n")
	sb.WriteString("- CRITICAL: preserve any discovered format requirements (JSON schema, CSV headers, encoding, trailing newlines)\n\n")
	for _, msg := range oldMessages {
		switch m := msg.(type) {
		case ModelRequest:
			for _, part := range m.Parts {
				switch p := part.(type) {
				case UserPromptPart:
					fmt.Fprintf(&sb, "User: %s\n", truncateStr(p.Content, 500))
				case ToolReturnPart:
					fmt.Fprintf(&sb, "[Tool result: %s] %s\n", p.ToolName, truncateStr(fmt.Sprintf("%v", p.Content), 800))
				}
			}
		case ModelResponse:
			if text := m.TextContent(); text != "" {
				fmt.Fprintf(&sb, "Assistant: %s\n", truncateStr(text, 500))
			}
			for _, part := range m.Parts {
				if tc, ok := part.(ToolCallPart); ok {
					fmt.Fprintf(&sb, "[Tool call: %s] %s\n", tc.ToolName, truncateStr(tc.ArgsJSON, 500))
				}
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

	// If the summary model returned empty text, fall back to the original
	// messages rather than creating a near-empty summary that discards history.
	if summaryResp.TextContent() == "" {
		return messages, nil
	}

	// Build new message list: first message + summary + recent messages.
	// The summary is emitted as a ModelResponse (assistant role) to maintain
	// proper user/assistant message alternation. Using a ModelRequest with
	// SystemPromptPart caused providers that extract system prompts to a
	// separate field (e.g., Anthropic) to produce no API message for the
	// summary, resulting in adjacent user messages that violate the
	// alternation requirement.
	summaryText := "[Conversation Summary] " + summaryResp.TextContent()
	if pin := BuildInstructionPin(messages); pin != "" {
		summaryText = "[Conversation Summary]\n" + pin + "\n\n" + summaryResp.TextContent()
	}

	summaryMsg := ModelResponse{
		Parts: []ModelResponsePart{
			TextPart{Content: summaryText},
		},
		Timestamp: time.Now(),
	}

	result := make([]ModelMessage, 0, 2+len(recentMessages))
	result = append(result, firstMsg)
	result = append(result, summaryMsg)
	result = append(result, recentMessages...)

	// Strip tool results whose matching tool calls were dropped during
	// compression. APIs (Anthropic, OpenAI) reject conversations where
	// tool_result blocks reference tool_use IDs that no longer exist.
	result = stripOrphanedToolResults(result)

	return result, nil
}

// stripOrphanedToolResults removes ToolReturnParts and RetryPromptParts whose
// matching ToolCallParts were dropped during compression.
func stripOrphanedToolResults(messages []ModelMessage) []ModelMessage {
	// Collect all tool call IDs present in the messages.
	callIDs := make(map[string]bool)
	for _, msg := range messages {
		if resp, ok := msg.(ModelResponse); ok {
			for _, part := range resp.Parts {
				if tc, ok := part.(ToolCallPart); ok {
					callIDs[tc.ToolCallID] = true
				}
			}
		}
	}

	// Scan for orphaned tool results and convert to user text.
	out := make([]ModelMessage, 0, len(messages))
	for _, msg := range messages {
		req, ok := msg.(ModelRequest)
		if !ok {
			out = append(out, msg)
			continue
		}

		var filtered []ModelRequestPart
		modified := false
		for _, part := range req.Parts {
			switch p := part.(type) {
			case ToolReturnPart:
				if !callIDs[p.ToolCallID] {
					content := ""
					switch v := p.Content.(type) {
					case string:
						content = v
					case nil:
						// No content to preserve.
					default:
						// Structured data (map, slice, number, etc.) — marshal
						// to JSON string so it's preserved in the conversation.
						if b, err := json.Marshal(v); err == nil {
							content = string(b)
						} else {
							content = fmt.Sprintf("%v", v)
						}
					}
					if content != "" {
						filtered = append(filtered, UserPromptPart{
							Content: fmt.Sprintf("[Previous %s result] %s",
								p.ToolName, truncateStr(content, 500)),
						})
					}
					modified = true
					continue
				}
			case RetryPromptPart:
				if p.ToolCallID != "" && !callIDs[p.ToolCallID] {
					if p.Content != "" {
						filtered = append(filtered, UserPromptPart{
							Content: "[Previous tool error] " + truncateStr(p.Content, 500),
						})
					}
					modified = true
					continue
				}
			}
			filtered = append(filtered, part)
		}

		if modified {
			if len(filtered) == 0 {
				// Keep a placeholder instead of dropping — dropping the
				// ModelRequest entirely can create consecutive ModelResponse
				// (assistant-role) messages, which violates the alternation
				// requirement and causes Anthropic 400 errors.
				req.Parts = []ModelRequestPart{
					UserPromptPart{Content: "[Previous tool results removed during context recovery]"},
				}
			} else {
				req.Parts = filtered
			}
		}
		out = append(out, req)
	}

	return out
}

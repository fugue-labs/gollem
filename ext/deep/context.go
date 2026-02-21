package deep

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// ContextManager manages conversation context for long-running agents.
// It automatically compresses context when approaching token limits using
// a three-tier strategy:
//
//   - Tier 1: Offload large tool results to filesystem
//   - Tier 2: Offload large tool call inputs at compression threshold
//   - Tier 3: Summarize older conversation turns via LLM
type ContextManager struct {
	model                core.Model
	tokenCounter         TokenCounter
	store                ContextStore
	maxContextTokens     int
	offloadThreshold     int
	compressionThreshold float64
	mu                   sync.Mutex
	offloadCounter       int
}

// ContextOption configures the ContextManager.
type ContextOption func(*ContextManager)

// NewContextManager creates a context manager with the given options.
func NewContextManager(model core.Model, opts ...ContextOption) *ContextManager {
	cm := &ContextManager{
		model:                model,
		tokenCounter:         DefaultTokenCounter(),
		maxContextTokens:     100000,
		offloadThreshold:     20000,
		compressionThreshold: 0.85,
	}
	for _, opt := range opts {
		opt(cm)
	}
	return cm
}

// WithMaxContextTokens sets the maximum context window size.
func WithMaxContextTokens(n int) ContextOption {
	return func(cm *ContextManager) {
		cm.maxContextTokens = n
	}
}

// WithOffloadThreshold sets the token threshold for offloading (default: 20000).
func WithOffloadThreshold(n int) ContextOption {
	return func(cm *ContextManager) {
		cm.offloadThreshold = n
	}
}

// WithCompressionThreshold sets the context usage percentage to trigger compression (default: 0.85).
func WithCompressionThreshold(pct float64) ContextOption {
	return func(cm *ContextManager) {
		cm.compressionThreshold = pct
	}
}

// WithTokenCounter sets a custom token counter.
func WithTokenCounter(tc TokenCounter) ContextOption {
	return func(cm *ContextManager) {
		cm.tokenCounter = tc
	}
}

// WithContextStore sets the store for offloaded content.
func WithContextStore(store ContextStore) ContextOption {
	return func(cm *ContextManager) {
		cm.store = store
	}
}

// ProcessMessages applies context compression strategies to messages.
//
// Tier 1: Offload large tool results (>threshold tokens) to filesystem, replace with summary.
// Tier 2: At compression threshold, offload large tool call inputs.
// Tier 3: Summarize older conversation turns via LLM.
func (cm *ContextManager) ProcessMessages(ctx context.Context, messages []core.ModelMessage) ([]core.ModelMessage, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	// Make a copy so we don't mutate the original.
	result := make([]core.ModelMessage, len(messages))
	copy(result, messages)

	// Tier 1: Offload large tool results.
	result = cm.tier1OffloadResults(result)

	// Check if we need further compression.
	totalTokens := cm.tokenCounter.CountMessageTokens(result)
	threshold := int(float64(cm.maxContextTokens) * cm.compressionThreshold)

	if totalTokens < threshold {
		return result, nil
	}

	// Tier 2: Offload large tool call inputs.
	result = cm.tier2OffloadInputs(result)

	totalTokens = cm.tokenCounter.CountMessageTokens(result)
	if totalTokens < threshold {
		return result, nil
	}

	// Tier 3: Summarize older conversation turns via LLM.
	// On summarization failure, gracefully degrade by returning the current result.
	summarized, summarizeErr := cm.tier3Summarize(ctx, result)
	if summarizeErr != nil {
		return result, nil //nolint:nilerr // graceful degradation on summarization failure
	}
	return summarized, nil
}

// AsHistoryProcessor returns a function compatible with core.WithHistoryProcessor.
func (cm *ContextManager) AsHistoryProcessor() func(ctx context.Context, messages []core.ModelMessage) ([]core.ModelMessage, error) {
	return cm.ProcessMessages
}

// tier1OffloadResults offloads large tool result content.
func (cm *ContextManager) tier1OffloadResults(messages []core.ModelMessage) []core.ModelMessage {
	result := make([]core.ModelMessage, len(messages))
	for i, msg := range messages {
		req, ok := msg.(core.ModelRequest)
		if !ok {
			result[i] = msg
			continue
		}

		modified := false
		parts := make([]core.ModelRequestPart, len(req.Parts))
		for j, part := range req.Parts {
			trp, ok := part.(core.ToolReturnPart)
			if !ok {
				parts[j] = part
				continue
			}

			content := fmt.Sprintf("%v", trp.Content)
			tokens := cm.tokenCounter.CountTokens(content)
			if tokens < cm.offloadThreshold {
				parts[j] = part
				continue
			}

			// Offload this content.
			summary := cm.offloadContent(content, tokens)
			modified = true
			parts[j] = core.ToolReturnPart{
				ToolName:   trp.ToolName,
				Content:    summary,
				ToolCallID: trp.ToolCallID,
				Timestamp:  trp.Timestamp,
			}
		}

		if modified {
			result[i] = core.ModelRequest{Parts: parts, Timestamp: req.Timestamp}
		} else {
			result[i] = msg
		}
	}
	return result
}

// tier2OffloadInputs offloads large tool call argument content.
func (cm *ContextManager) tier2OffloadInputs(messages []core.ModelMessage) []core.ModelMessage {
	result := make([]core.ModelMessage, len(messages))
	for i, msg := range messages {
		resp, ok := msg.(core.ModelResponse)
		if !ok {
			result[i] = msg
			continue
		}

		modified := false
		parts := make([]core.ModelResponsePart, len(resp.Parts))
		for j, part := range resp.Parts {
			tcp, ok := part.(core.ToolCallPart)
			if !ok {
				parts[j] = part
				continue
			}

			tokens := cm.tokenCounter.CountTokens(tcp.ArgsJSON)
			if tokens < cm.offloadThreshold {
				parts[j] = part
				continue
			}

			summary := cm.offloadContent(tcp.ArgsJSON, tokens)
			modified = true
			parts[j] = core.ToolCallPart{
				ToolName:   tcp.ToolName,
				ArgsJSON:   fmt.Sprintf(`{"_offloaded": %q}`, summary),
				ToolCallID: tcp.ToolCallID,
			}
		}

		if modified {
			result[i] = core.ModelResponse{
				Parts:        parts,
				Usage:        resp.Usage,
				ModelName:    resp.ModelName,
				FinishReason: resp.FinishReason,
				Timestamp:    resp.Timestamp,
			}
		} else {
			result[i] = msg
		}
	}
	return result
}

// tier3Summarize summarizes older messages via LLM.
func (cm *ContextManager) tier3Summarize(ctx context.Context, messages []core.ModelMessage) ([]core.ModelMessage, error) {
	if len(messages) <= 2 {
		return messages, nil
	}

	// Find the split point: summarize the first half of messages.
	splitIdx := len(messages) / 2
	if splitIdx < 1 {
		splitIdx = 1
	}

	// Build a text representation of the messages to summarize.
	var sb strings.Builder
	for _, msg := range messages[:splitIdx] {
		switch m := msg.(type) {
		case core.ModelRequest:
			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.SystemPromptPart:
					sb.WriteString("System: ")
					sb.WriteString(p.Content)
					sb.WriteString("\n")
				case core.UserPromptPart:
					sb.WriteString("User: ")
					sb.WriteString(p.Content)
					sb.WriteString("\n")
				case core.ToolReturnPart:
					sb.WriteString("Tool ")
					sb.WriteString(p.ToolName)
					sb.WriteString(": ")
					fmt.Fprintf(&sb, "%v", p.Content)
					sb.WriteString("\n")
				}
			}
		case core.ModelResponse:
			sb.WriteString("Assistant: ")
			sb.WriteString(m.TextContent())
			sb.WriteString("\n")
		}
	}

	// Ask the model to summarize.
	summaryReq := core.ModelRequest{
		Parts: []core.ModelRequestPart{
			core.SystemPromptPart{Content: "Summarize the following conversation concisely, preserving key information, decisions, and context needed for continuation."},
			core.UserPromptPart{Content: sb.String()},
		},
		Timestamp: time.Now(),
	}

	resp, err := cm.model.Request(ctx, []core.ModelMessage{summaryReq}, nil, &core.ModelRequestParameters{
		AllowTextOutput: true,
	})
	if err != nil {
		return nil, fmt.Errorf("summarization request failed: %w", err)
	}

	summaryText := resp.TextContent()
	if summaryText == "" {
		return messages, nil
	}

	// Build new message list: summary + recent messages.
	newMessages := make([]core.ModelMessage, 0, 1+len(messages)-splitIdx)
	newMessages = append(newMessages, core.ModelRequest{
		Parts: []core.ModelRequestPart{
			core.SystemPromptPart{
				Content:   "[Conversation Summary]\n" + summaryText,
				Timestamp: time.Now(),
			},
		},
		Timestamp: time.Now(),
	})
	newMessages = append(newMessages, messages[splitIdx:]...)
	return newMessages, nil
}

// offloadContent stores content and returns a summary string.
func (cm *ContextManager) offloadContent(content string, tokens int) string {
	cm.mu.Lock()
	cm.offloadCounter++
	key := fmt.Sprintf("offload_%d", cm.offloadCounter)
	cm.mu.Unlock()

	// Try to store in the context store.
	stored := false
	if cm.store != nil {
		if err := cm.store.Store(key, content); err == nil {
			stored = true
		}
	}

	// Build summary from first 200 chars.
	preview := content
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}
	if !stored {
		return fmt.Sprintf("[Content could not be offloaded — %d tokens retained inline. Summary: %s]", tokens, preview)
	}
	return fmt.Sprintf("[Content offloaded — %d tokens. Summary: %s]", tokens, preview)
}

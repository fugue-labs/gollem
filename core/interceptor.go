package core

import (
	"context"
	"regexp"
)

// MessageAction represents what to do with a message after interception.
type MessageAction int

const (
	// MessageAllow passes the message through unchanged.
	MessageAllow MessageAction = iota
	// MessageDrop silently drops the message (skip this model request).
	MessageDrop
	// MessageModify replaces the messages with a modified version.
	MessageModify
)

// InterceptResult is the result of intercepting a message.
type InterceptResult struct {
	Action   MessageAction
	Messages []ModelMessage // used when Action == MessageModify
	Reason   string         // optional reason for drop/modify
}

// MessageInterceptor intercepts messages before model requests.
type MessageInterceptor func(ctx context.Context, messages []ModelMessage) InterceptResult

// ResponseInterceptor intercepts model responses.
type ResponseInterceptor func(ctx context.Context, response *ModelResponse) InterceptResult

// WithMessageInterceptor adds a pre-request message interceptor.
func WithMessageInterceptor[T any](interceptor MessageInterceptor) AgentOption[T] {
	return func(a *Agent[T]) {
		a.messageInterceptors = append(a.messageInterceptors, interceptor)
	}
}

// WithResponseInterceptor adds a post-response interceptor.
func WithResponseInterceptor[T any](interceptor ResponseInterceptor) AgentOption[T] {
	return func(a *Agent[T]) {
		a.responseInterceptors = append(a.responseInterceptors, interceptor)
	}
}

// RedactPII replaces patterns matching the regex with a placeholder in user prompts.
func RedactPII(pattern string, replacement string) MessageInterceptor {
	re := regexp.MustCompile(pattern)
	return func(_ context.Context, messages []ModelMessage) InterceptResult {
		modified := make([]ModelMessage, len(messages))
		changed := false
		for i, msg := range messages {
			if req, ok := msg.(ModelRequest); ok {
				newParts := make([]ModelRequestPart, len(req.Parts))
				for j, part := range req.Parts {
					if up, ok := part.(UserPromptPart); ok {
						redacted := re.ReplaceAllString(up.Content, replacement)
						if redacted != up.Content {
							changed = true
							newParts[j] = UserPromptPart{Content: redacted, Timestamp: up.Timestamp}
							continue
						}
					}
					newParts[j] = part
				}
				modified[i] = ModelRequest{Parts: newParts, Timestamp: req.Timestamp}
			} else {
				modified[i] = msg
			}
		}
		if changed {
			return InterceptResult{Action: MessageModify, Messages: modified}
		}
		return InterceptResult{Action: MessageAllow}
	}
}

// AuditLog logs all messages to a callback before allowing them through.
func AuditLog(logger func(direction string, messages []ModelMessage)) MessageInterceptor {
	return func(_ context.Context, messages []ModelMessage) InterceptResult {
		logger("request", messages)
		return InterceptResult{Action: MessageAllow}
	}
}

// runMessageInterceptors runs all message interceptors and returns the (possibly modified) messages.
// Returns nil messages and true if any interceptor drops the message.
func runMessageInterceptors(ctx context.Context, interceptors []MessageInterceptor, messages []ModelMessage) ([]ModelMessage, bool) {
	for _, interceptor := range interceptors {
		result := interceptor(ctx, messages)
		switch result.Action {
		case MessageDrop:
			return nil, true
		case MessageModify:
			messages = result.Messages
		case MessageAllow:
			// continue
		}
	}
	return messages, false
}

// runResponseInterceptors runs all response interceptors.
// Returns true if any interceptor drops the response.
func runResponseInterceptors(ctx context.Context, interceptors []ResponseInterceptor, resp *ModelResponse) bool {
	for _, interceptor := range interceptors {
		result := interceptor(ctx, resp)
		if result.Action == MessageDrop {
			return true
		}
		if result.Action == MessageModify && len(result.Messages) > 0 {
			// Modify the response parts from the first ModelResponse in Messages.
			if modResp, ok := result.Messages[0].(ModelResponse); ok {
				resp.Parts = modResp.Parts
			}
		}
	}
	return false
}


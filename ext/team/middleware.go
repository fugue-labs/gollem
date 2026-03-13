package team

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fugue-labs/gollem/core"
)

// TeamAwarenessMiddleware drains the teammate's mailbox before each model
// request and injects pending messages as a UserPromptPart. This ensures
// the agent sees messages from other teammates between turns.
func TeamAwarenessMiddleware(tm *Teammate) core.AgentMiddleware {
	return core.RequestOnlyMiddleware(func(
		ctx context.Context,
		messages []core.ModelMessage,
		settings *core.ModelSettings,
		params *core.ModelRequestParameters,
		next func(context.Context, []core.ModelMessage, *core.ModelSettings, *core.ModelRequestParameters) (*core.ModelResponse, error),
	) (*core.ModelResponse, error) {
		// Drain any pending messages.
		pending := tm.filterControlMessages(tm.mailbox.DrainAll())
		shutdownMsg, hasShutdown := tm.shutdownRequest()
		if len(pending) == 0 && !hasShutdown {
			return next(ctx, messages, settings, params)
		}

		for _, msg := range pending {
			preview := msg.Summary
			if preview == "" {
				preview = msg.Content
			}
			teamName := "unknown-team"
			if tm != nil && tm.team != nil && tm.team.name != "" {
				teamName = tm.team.name
			}
			memberName := "unknown-member"
			if tm != nil && tm.name != "" {
				memberName = tm.name
			}
			fmt.Fprintf(os.Stderr,
				"[gollem] team:%s mailbox:%s <- %s (%s): %s\n",
				teamName, memberName, msg.From, msg.Type, previewForLog(preview, 240))
		}

		// Format messages and inject as UserPromptPart.
		var parts []string
		for _, msg := range pending {
			parts = append(parts, fmt.Sprintf("[Message from %s]: %s", msg.From, msg.Content))
		}
		if hasShutdown {
			from := shutdownMsg.From
			if from == "" {
				from = "team"
			}
			reason := shutdownMsg.Content
			if reason == "" {
				reason = "No reason provided"
			}
			// TODO: Phase 1+ should stop expressing shutdown via prompt text.
			// The control path is now out-of-band; this injection remains only so
			// the worker can wrap up gracefully within the current run.
			parts = append(parts, fmt.Sprintf("[SHUTDOWN REQUEST from %s]: %s — Wrap up your current work and finish.", from, reason))
		}

		injection := strings.Join(parts, "\n\n")
		if hasShutdown {
			injection += "\n\nIMPORTANT: A shutdown has been requested. Complete your current task as quickly as possible and provide your final response."
		}

		// Merge into the last ModelRequest to avoid consecutive user-role
		// messages, which cause a 400 error from Anthropic's API.
		newMessages := make([]core.ModelMessage, len(messages))
		copy(newMessages, messages)
		merged := false
		for i := len(newMessages) - 1; i >= 0; i-- {
			if req, ok := newMessages[i].(core.ModelRequest); ok {
				newParts := make([]core.ModelRequestPart, len(req.Parts)+1)
				copy(newParts, req.Parts)
				newParts[len(req.Parts)] = core.UserPromptPart{Content: injection}
				req.Parts = newParts
				newMessages[i] = req
				merged = true
				break
			}
		}
		if !merged {
			// Fallback: no ModelRequest found, append a new one.
			newMessages = append(newMessages, core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.UserPromptPart{Content: injection},
				},
			})
		}

		return next(ctx, newMessages, settings, params)
	})
}

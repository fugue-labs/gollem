package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

const (
	threadCompactionItemKind   = "contextCompaction"
	threadCompactionSummaryMax = 6000
)

type threadCompactStartParams = protocol.ThreadCompactStartParams
type threadCompactStartResponse = protocol.ThreadCompactStartResponse
type threadCompactionPayload = protocol.ContextCompactionItem

func (s *Server) handleThreadCompactStart(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/compact/start")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadCompactStartParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := firstNonEmpty(params.ThreadID, params.ID)
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	thread, err := st.GetThread(ctx, threadID)
	if err != nil {
		return nil, mapError("thread/compact/start", err)
	}
	if thread.Status == store.ThreadDeleted {
		return nil, mapError("thread/compact/start", store.ErrThreadDeleted)
	}
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: thread.ID})
	if err != nil {
		return nil, mapError("thread/compact/start", err)
	}
	summary := summarizeCompactionMessages(runtimeMessagesFromItems(compactionWindowItems(items)), threadCompactionSummaryMax)
	now := time.Now().UTC()
	turn, err := st.CreateTurn(ctx, store.CreateTurnRequest{
		ThreadID: thread.ID,
		Input:    mustRuntimeJSON(map[string]any{"type": threadCompactionItemKind, "requestedAt": now}),
		Metadata: map[string]any{"kind": threadCompactionItemKind},
	})
	if err != nil {
		return nil, mapError("thread/compact/start", err)
	}
	started, err := st.StartTurn(ctx, turn.ID)
	if err != nil {
		return nil, mapError("thread/compact/start", err)
	}
	item, err := st.AppendItem(ctx, store.AppendItemRequest{
		ThreadID: thread.ID,
		TurnID:   started.ID,
		Kind:     threadCompactionItemKind,
		Status:   "completed",
		Payload: mustRuntimeJSON(threadCompactionPayload{
			Type:      threadCompactionItemKind,
			Summary:   summary,
			CreatedAt: now,
		}),
	})
	if err != nil {
		return nil, mapError("thread/compact/start", err)
	}
	completed, err := st.CompleteTurn(ctx, store.CompleteTurnRequest{
		ID:     started.ID,
		Status: store.TurnCompleted,
		Result: mustRuntimeJSON(map[string]any{
			"type":   threadCompactionItemKind,
			"itemId": item.ID,
		}),
	})
	if err != nil {
		return nil, mapError("thread/compact/start", err)
	}
	s.markThreadLoaded(thread)
	publishTurnStarted(s, started)
	s.publishItemStarted(started, item)
	publishItemCompleted(s, completed, item)
	publishTurnCompleted(s, completed)
	s.PublishNotification("thread/compacted", contextCompactedNotificationParams{
		ThreadID: thread.ID,
		TurnID:   completed.ID,
	})
	return threadCompactStartResponse{}, nil
}

func (s *Server) publishItemStarted(turn *store.Turn, item *store.Item) {
	if s == nil || turn == nil || item == nil {
		return
	}
	started := *item
	started.Status = "running"
	s.PublishNotification("item/started", runtimeItemNotificationParams{
		ThreadID: turn.ThreadID,
		TurnID:   turn.ID,
		ItemID:   item.ID,
		Item:     protocolTimelineItem(&started),
		At:       time.Now().UTC(),
	})
}

func compactionWindowItems(items []*store.Item) []*store.Item {
	start := 0
	for i, item := range items {
		if item != nil && item.Kind == threadCompactionItemKind {
			start = i
		}
	}
	return items[start:]
}

func summarizeCompactionMessages(messages []core.ModelMessage, maxRunes int) string {
	if len(messages) == 0 {
		return "No prior model-visible messages."
	}
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		switch typed := message.(type) {
		case core.ModelRequest:
			for _, part := range typed.Parts {
				switch p := part.(type) {
				case core.SystemPromptPart:
					lines = append(lines, "system: "+compactWhitespace(p.Content))
				case core.UserPromptPart:
					lines = append(lines, "user: "+compactWhitespace(p.Content))
				case core.ToolReturnPart:
					lines = append(lines, "tool result: "+p.ToolName+" "+compactWhitespace(runtimeReplayContentString(p.Content)))
				case core.RetryPromptPart:
					lines = append(lines, "tool error: "+p.ToolName+" "+compactWhitespace(p.Content))
				}
			}
		case core.ModelResponse:
			for _, part := range typed.Parts {
				switch p := part.(type) {
				case core.TextPart:
					if text := compactWhitespace(p.Content); text != "" {
						lines = append(lines, "assistant: "+text)
					}
				case core.ToolCallPart:
					lines = append(lines, "tool call: "+p.ToolName+" "+compactWhitespace(p.ArgsJSON))
				}
			}
		}
	}
	summary := strings.TrimSpace(strings.Join(lines, "\n"))
	if summary == "" {
		return "No prior model-visible messages."
	}
	return truncateRunesFromStart(summary, maxRunes)
}

func truncateRunesFromStart(text string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return "[truncated earlier compacted context]\n" + string(runes[len(runes)-maxRunes:])
}

func runtimeMessageFromCompactionItem(raw json.RawMessage) (core.ModelMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var payload threadCompactionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	summary := strings.TrimSpace(payload.Summary)
	if summary == "" {
		return nil, false
	}
	content := "Previous conversation context summary:\n" + summary
	return core.ModelRequest{
		Parts:     []core.ModelRequestPart{core.SystemPromptPart{Content: content, Timestamp: payload.CreatedAt}},
		Timestamp: payload.CreatedAt,
	}, true
}

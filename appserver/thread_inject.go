package appserver

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

const threadInjectedResponseItemKind = "response_item"

type threadInjectItemsParams struct {
	ID       string            `json:"id,omitempty"`
	ThreadID string            `json:"threadId,omitempty"`
	Items    []json.RawMessage `json:"items"`
}

func (p threadInjectItemsParams) threadID() string {
	return firstNonEmpty(p.ThreadID, p.ID)
}

type threadInjectItemsResponse struct{}

func (s *Server) handleThreadInjectItems(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/inject_items")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadInjectItemsParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := params.threadID()
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	if params.Items == nil {
		return nil, invalidParams("items is required", nil)
	}
	thread, err := st.GetThread(ctx, threadID)
	if err != nil {
		return nil, mapError("thread/inject_items", err)
	}
	if thread.Status == store.ThreadDeleted {
		return nil, mapError("thread/inject_items", store.ErrThreadDeleted)
	}
	s.markThreadLoaded(thread)
	for _, rawItem := range params.Items {
		payload := cloneJSONRaw(rawItem)
		if len(payload) == 0 {
			payload = json.RawMessage("null")
		}
		item, err := st.AppendItem(ctx, store.AppendItemRequest{
			ThreadID: thread.ID,
			Kind:     threadInjectedResponseItemKind,
			Status:   "completed",
			Payload:  payload,
		})
		if err != nil {
			return nil, mapError("thread/inject_items", err)
		}
		s.PublishNotification("item/completed", runtimeItemNotificationParams{
			ThreadID: thread.ID,
			ItemID:   item.ID,
			Item:     protocolTimelineItem(item),
			At:       time.Now().UTC(),
		})
	}
	return threadInjectItemsResponse{}, nil
}

func runtimeMessageFromInjectedResponseItem(raw json.RawMessage) (core.ModelMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, false
	}
	role := strings.ToLower(strings.TrimSpace(stringValue(item, "role")))
	if role == "" {
		return nil, false
	}
	text := strings.TrimSpace(firstNonEmpty(
		stringValue(item, "text"),
		stringValue(item, "input_text"),
		stringValue(item, "output_text"),
		textFromJSONValue(item["content"]),
	))
	if text == "" {
		return nil, false
	}
	timestamp := timestampFromResponseItem(item)
	switch role {
	case "assistant":
		return core.ModelResponse{
			Parts:        []core.ModelResponsePart{core.TextPart{Content: text}},
			ModelName:    stringValue(item, "model"),
			FinishReason: core.FinishReasonStop,
			Timestamp:    timestamp,
		}, true
	case "system", "developer":
		return core.ModelRequest{
			Parts:     []core.ModelRequestPart{core.SystemPromptPart{Content: text, Timestamp: timestamp}},
			Timestamp: timestamp,
		}, true
	case "user":
		return core.ModelRequest{
			Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: text, Timestamp: timestamp}},
			Timestamp: timestamp,
		}, true
	default:
		return nil, false
	}
}

func textFromJSONValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(textFromJSONValue(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"text", "input_text", "output_text", "content"} {
			if text := strings.TrimSpace(textFromJSONValue(typed[key])); text != "" {
				return text
			}
		}
	}
	return ""
}

func timestampFromResponseItem(item map[string]any) time.Time {
	for _, key := range []string{"createdAt", "created_at", "timestamp"} {
		if value, ok := item[key]; ok {
			if ts, ok := parseResponseItemTime(value); ok {
				return ts
			}
		}
	}
	return time.Time{}
}

func parseResponseItemTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case string:
		if ts, err := time.Parse(time.RFC3339Nano, typed); err == nil {
			return ts, true
		}
		if unix, err := strconv.ParseFloat(typed, 64); err == nil {
			return unixSecondsToTime(unix), true
		}
	case float64:
		return unixSecondsToTime(typed), true
	case int64:
		return time.Unix(typed, 0).UTC(), true
	case int:
		return time.Unix(int64(typed), 0).UTC(), true
	}
	return time.Time{}, false
}

func unixSecondsToTime(seconds float64) time.Time {
	sec := int64(seconds)
	nsec := int64((seconds - float64(sec)) * 1_000_000_000)
	return time.Unix(sec, nsec).UTC()
}

func stringValue(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func cloneJSONRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}

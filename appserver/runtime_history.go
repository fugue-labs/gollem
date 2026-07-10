package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

const runtimeReplayIdentifierMaxBytes = 64

type runtimeReplayToolRecord struct {
	tool      string
	arguments any
	result    any
	errorText string
	success   bool
}

type runtimeHistoryBuilder struct {
	dynamicParents map[string]struct{}
	callIDCounts   map[string]int
	generatedIDs   int
}

func runtimeMessagesFromItems(items []*store.Item) []core.ModelMessage {
	builder := runtimeHistoryBuilder{
		dynamicParents: make(map[string]struct{}),
		callIDCounts:   make(map[string]int),
	}
	builder.collectDynamicParents(items)

	messages := make([]core.ModelMessage, 0, len(items))
	for _, item := range items {
		if item == nil || len(item.Payload) == 0 {
			continue
		}
		switch item.Kind {
		case threadInjectedResponseItemKind:
			if message, ok := runtimeMessageFromInjectedResponseItem(item.Payload); ok {
				messages = append(messages, message)
			}
		case threadCompactionItemKind:
			if message, ok := runtimeMessageFromCompactionItem(item.Payload); ok {
				messages = append(messages, message)
			}
		case "message":
			if message, ok := runtimeMessageFromStoredItem(item.Payload); ok {
				messages = append(messages, message)
			}
		case runtimeDynamicToolCallItemKind:
			if record, ok := runtimeReplayDynamicToolRecord(item); ok {
				messages = append(messages, builder.toolMessages(item, record)...)
			}
		case threadShellCommandItemKind:
			if builder.isReplayedChild(item) {
				continue
			}
			if record, ok := runtimeReplayCommandRecord(item); ok {
				messages = append(messages, builder.toolMessages(item, record)...)
			}
		case runtimeFileChangeItemKind:
			if builder.isReplayedChild(item) {
				continue
			}
			if record, ok := runtimeReplayFileChangeRecord(item); ok {
				messages = append(messages, builder.toolMessages(item, record)...)
			}
		case runtimeMCPToolCallItemKind:
			if builder.isReplayedChild(item) {
				continue
			}
			if record, ok := runtimeReplayMCPRecord(item); ok {
				messages = append(messages, builder.toolMessages(item, record)...)
			}
		}
	}
	return messages
}

func (b *runtimeHistoryBuilder) collectDynamicParents(items []*store.Item) {
	for _, item := range items {
		if item == nil || item.ID == "" || item.Kind != runtimeDynamicToolCallItemKind {
			continue
		}
		if _, ok := runtimeReplayDynamicToolRecord(item); ok {
			b.dynamicParents[item.ID] = struct{}{}
		}
	}
}

func (b *runtimeHistoryBuilder) isReplayedChild(item *store.Item) bool {
	if item == nil || item.ParentItemID == "" {
		return false
	}
	_, ok := b.dynamicParents[item.ParentItemID]
	return ok
}

func (b *runtimeHistoryBuilder) toolMessages(item *store.Item, record runtimeReplayToolRecord) []core.ModelMessage {
	callID := b.nextCallID(item)
	toolName := runtimeReplayIdentifier(record.tool, "historical_tool")
	calledAt := runtimeReplayItemTime(item, false)
	completedAt := runtimeReplayItemTime(item, true)
	call := core.ModelResponse{
		Parts: []core.ModelResponsePart{core.ToolCallPart{
			ToolName:   toolName,
			ArgsJSON:   runtimeReplayArgumentsJSON(record.arguments),
			ToolCallID: callID,
		}},
		FinishReason: core.FinishReasonToolCall,
		Timestamp:    calledAt,
	}
	var result core.ModelRequestPart
	if record.success {
		result = core.ToolReturnPart{
			ToolName:   toolName,
			Content:    runtimeReplayBoundedValue(record.result),
			ToolCallID: callID,
			Timestamp:  completedAt,
		}
	} else {
		result = core.RetryPromptPart{
			ToolName:   toolName,
			Content:    boundedRuntimeToolOutput(record.errorText),
			ToolCallID: callID,
			Timestamp:  completedAt,
		}
	}
	return []core.ModelMessage{
		call,
		core.ModelRequest{Parts: []core.ModelRequestPart{result}, Timestamp: completedAt},
	}
}

func (b *runtimeHistoryBuilder) nextCallID(item *store.Item) string {
	base := ""
	if item != nil {
		base = item.ID
		if strings.TrimSpace(base) == "" && item.Seq > 0 {
			base = fmt.Sprintf("history_call_%d", item.Seq)
		}
	}
	if strings.TrimSpace(base) == "" {
		b.generatedIDs++
		base = fmt.Sprintf("history_call_generated_%d", b.generatedIDs)
	}
	base = runtimeReplayIdentifier(base, "history_call")
	b.callIDCounts[base]++
	count := b.callIDCounts[base]
	if count == 1 {
		return base
	}
	suffix := fmt.Sprintf("_%d", count)
	if len(base)+len(suffix) > runtimeReplayIdentifierMaxBytes {
		base = base[:runtimeReplayIdentifierMaxBytes-len(suffix)]
	}
	return base + suffix
}

func runtimeMessageFromStoredItem(raw json.RawMessage) (core.ModelMessage, bool) {
	var payload runtimeMessagePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	switch payload.Role {
	case "user":
		return core.ModelRequest{
			Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: payload.Text, Timestamp: payload.CreatedAt}},
			Timestamp: payload.CreatedAt,
		}, true
	case "assistant":
		return core.ModelResponse{
			Parts:        []core.ModelResponsePart{core.TextPart{Content: payload.Text}},
			ModelName:    payload.Model,
			FinishReason: core.FinishReasonStop,
			Timestamp:    payload.CreatedAt,
		}, true
	default:
		return nil, false
	}
}

func runtimeReplayDynamicToolRecord(item *store.Item) (runtimeReplayToolRecord, bool) {
	var payload runtimeDynamicToolCallPayload
	if item == nil || json.Unmarshal(item.Payload, &payload) != nil || strings.TrimSpace(payload.Tool) == "" {
		return runtimeReplayToolRecord{}, false
	}
	status := firstRuntimeNonEmpty(item.Status, payload.Status, runtimeToolStatusInProgress)
	outputParts := make([]string, 0, len(payload.ContentItems))
	for _, content := range payload.ContentItems {
		if content.Text != "" {
			outputParts = append(outputParts, content.Text)
		}
	}
	output := boundedRuntimeToolOutput(strings.Join(outputParts, "\n"))
	success := status == runtimeToolStatusCompleted
	if payload.Success != nil {
		success = success && *payload.Success
	}
	result := runtimeReplayDecodedText(output)
	if output == "" {
		result = map[string]any{"status": status}
	}
	record := runtimeReplayToolRecord{
		tool:      payload.Tool,
		arguments: payload.Arguments,
		result:    result,
		success:   success,
	}
	if !success {
		record.errorText = runtimeReplayFailureText(payload.Tool, status, output)
	}
	return record, true
}

func runtimeReplayCommandRecord(item *store.Item) (runtimeReplayToolRecord, bool) {
	var payload threadShellCommandPayload
	if item == nil || json.Unmarshal(item.Payload, &payload) != nil || strings.TrimSpace(payload.Command) == "" {
		return runtimeReplayToolRecord{}, false
	}
	status := firstRuntimeNonEmpty(item.Status, payload.Status, commandExecutionStatusInProgress)
	result := map[string]any{
		"status":     status,
		"processId":  payload.ProcessID,
		"exitCode":   payload.ExitCode,
		"durationMs": payload.DurationMS,
	}
	if payload.AggregatedOutput != nil {
		result["output"] = boundedRuntimeToolOutput(*payload.AggregatedOutput)
	}
	success := status == commandExecutionStatusCompleted
	record := runtimeReplayToolRecord{
		tool: "thread_shell_command",
		arguments: map[string]any{
			"command": payload.Command,
			"cwd":     payload.CWD,
			"source":  payload.Source,
		},
		result:  result,
		success: success,
	}
	if !success {
		record.errorText = runtimeReplayFailureValue("thread_shell_command", status, result)
	}
	return record, true
}

func runtimeReplayFileChangeRecord(item *store.Item) (runtimeReplayToolRecord, bool) {
	var payload runtimeFileChangePayload
	if item == nil || json.Unmarshal(item.Payload, &payload) != nil || len(payload.Changes) == 0 {
		return runtimeReplayToolRecord{}, false
	}
	status := firstRuntimeNonEmpty(item.Status, payload.Status, runtimeFileChangeStatusInProgress)
	result := map[string]any{
		"status":   status,
		"changes":  payload.Changes,
		"evidence": payload.Evidence,
	}
	success := status == runtimeFileChangeStatusCompleted
	record := runtimeReplayToolRecord{
		tool:      "file_change",
		arguments: map[string]any{"changes": payload.Changes},
		result:    result,
		success:   success,
	}
	if !success {
		record.errorText = runtimeReplayFailureValue("file_change", status, result)
	}
	return record, true
}

func runtimeReplayMCPRecord(item *store.Item) (runtimeReplayToolRecord, bool) {
	var payload runtimeMCPToolCallPayload
	if item == nil || json.Unmarshal(item.Payload, &payload) != nil || strings.TrimSpace(payload.Server) == "" || strings.TrimSpace(payload.Tool) == "" {
		return runtimeReplayToolRecord{}, false
	}
	status := firstRuntimeNonEmpty(item.Status, payload.Status, runtimeMCPStatusInProgress)
	result := map[string]any{
		"server": payload.Server,
		"tool":   payload.Tool,
		"status": status,
		"result": payload.Result,
		"error":  payload.Error,
	}
	success := status == runtimeMCPStatusCompleted && payload.Error == nil
	record := runtimeReplayToolRecord{
		tool: "mcp_call_tool",
		arguments: map[string]any{
			"server":    payload.Server,
			"tool":      payload.Tool,
			"arguments": payload.Arguments,
		},
		result:  result,
		success: success,
	}
	if !success {
		record.errorText = runtimeReplayFailureValue("mcp_call_tool", status, result)
	}
	return record, true
}

func runtimeReplayArgumentsJSON(arguments any) string {
	if arguments == nil {
		return "{}"
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return runtimeReplaySummaryJSON("tool arguments could not be serialized", nil)
	}
	if len(raw) > runtimeToolPayloadMaxBytes {
		return runtimeReplaySummaryJSON("tool arguments exceed replay payload limit", raw)
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) == nil && object != nil {
		return string(raw)
	}
	wrapped, err := json.Marshal(map[string]any{"persistedArguments": arguments})
	if err != nil {
		return runtimeReplaySummaryJSON("tool arguments could not be wrapped", raw)
	}
	if len(wrapped) > runtimeToolPayloadMaxBytes {
		return runtimeReplaySummaryJSON("wrapped tool arguments exceed replay payload limit", wrapped)
	}
	return string(wrapped)
}

func runtimeReplaySummaryJSON(reason string, raw []byte) string {
	summary := runtimeToolPayloadSummary{
		Omitted: true,
		Reason:  reason,
		Bytes:   len(raw),
	}
	if len(raw) > 0 {
		summary.SHA256 = runtimeSHA256(raw)
	}
	encoded, _ := json.Marshal(summary)
	return string(encoded)
}

func runtimeReplayBoundedValue(value any) any {
	if text, ok := value.(string); ok {
		return boundedRuntimeToolOutput(text)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return runtimeToolPayloadSummary{Omitted: true, Reason: "tool result could not be serialized"}
	}
	if len(raw) <= runtimeToolPayloadMaxBytes {
		return value
	}
	return runtimeToolPayloadSummary{
		Omitted: true,
		Reason:  "tool result exceeds replay payload limit",
		Bytes:   len(raw),
		SHA256:  runtimeSHA256(raw),
	}
}

func runtimeReplayDecodedText(text string) any {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	var value any
	if json.Unmarshal([]byte(trimmed), &value) == nil {
		return value
	}
	return text
}

func runtimeReplayFailureText(tool, status, output string) string {
	if strings.TrimSpace(output) == "" {
		return fmt.Sprintf("historical tool %s ended with status %s", tool, status)
	}
	return fmt.Sprintf("historical tool %s ended with status %s: %s", tool, status, output)
}

func runtimeReplayFailureValue(tool, status string, value any) string {
	return runtimeReplayFailureText(tool, status, runtimeReplayContentString(value))
}

func runtimeReplayContentString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func runtimeReplayIdentifier(value, fallback string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	identifier := builder.String()
	if identifier == "" {
		identifier = fallback
	}
	if len(identifier) <= runtimeReplayIdentifierMaxBytes {
		return identifier
	}
	digest := runtimeSHA256([]byte(identifier))
	prefixBytes := runtimeReplayIdentifierMaxBytes - 1 - 16
	return identifier[:prefixBytes] + "_" + digest[:16]
}

func runtimeReplayItemTime(item *store.Item, completed bool) time.Time {
	if item == nil {
		return time.Time{}
	}
	if completed && !item.UpdatedAt.IsZero() {
		return item.UpdatedAt
	}
	return item.CreatedAt
}

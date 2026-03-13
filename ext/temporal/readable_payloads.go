package temporal

import (
	"encoding/json"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func decodeSerializedMessages(messages []core.SerializedMessage, raw json.RawMessage) ([]core.ModelMessage, error) {
	if len(messages) > 0 {
		return core.DecodeMessages(messages)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return core.UnmarshalMessages(raw)
}

func decodeSerializedSnapshot(snapshot *core.SerializedRunSnapshot, raw json.RawMessage) (*core.RunSnapshot, error) {
	if snapshot != nil {
		return core.DecodeRunSnapshot(snapshot)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return core.UnmarshalSnapshot(raw)
}

func decodeTrace(trace *core.RunTrace, raw json.RawMessage) (*core.RunTrace, error) {
	if trace != nil {
		return trace, nil
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var decoded core.RunTrace
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return &decoded, nil
}

func decodeTraceSteps(steps []core.TraceStep, raw json.RawMessage) ([]core.TraceStep, error) {
	if len(steps) > 0 {
		return append([]core.TraceStep(nil), steps...), nil
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var decoded []core.TraceStep
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func buildTemporalRunStateSnapshot(
	prompt string,
	messages []core.ModelMessage,
	usage core.RunUsage,
	lastInputTokens int,
	retries int,
	toolRetries map[string]int,
	runID string,
	runStep int,
	runStartTime time.Time,
	toolState map[string]any,
) *core.RunStateSnapshot {
	return &core.RunStateSnapshot{
		Messages:        cloneMessages(messages),
		Usage:           usage,
		LastInputTokens: lastInputTokens,
		Retries:         retries,
		ToolRetries:     cloneIntMap(toolRetries),
		RunID:           runID,
		RunStep:         runStep,
		RunStartTime:    runStartTime,
		Prompt:          prompt,
		ToolState:       cloneAnyMap(toolState),
		Timestamp:       time.Now(),
	}
}

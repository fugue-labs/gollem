package core

import (
	"encoding/json"
	"errors"
	"time"
)

// RunSnapshot is kept as the public compatibility name for RunStateSnapshot.
type RunSnapshot = RunStateSnapshot

// snapshotJSON is the JSON-safe serialization of RunStateSnapshot.
type snapshotJSON struct {
	Messages        json.RawMessage `json:"messages"`
	Usage           RunUsage        `json:"usage"`
	LastInputTokens int             `json:"last_input_tokens"`
	Retries         int             `json:"retries"`
	ToolRetries     map[string]int  `json:"tool_retries,omitempty"`
	RunID           string          `json:"run_id"`
	RunStep         int             `json:"run_step"`
	RunStartTime    time.Time       `json:"run_start_time"`
	Prompt          string          `json:"prompt"`
	ToolState       map[string]any  `json:"tool_state,omitempty"`
	Timestamp       time.Time       `json:"timestamp"`
}

// SerializedRunSnapshot is the structured JSON-safe form of a run snapshot.
type SerializedRunSnapshot struct {
	Messages        []SerializedMessage `json:"messages"`
	Usage           RunUsage            `json:"usage"`
	LastInputTokens int                 `json:"last_input_tokens"`
	Retries         int                 `json:"retries"`
	ToolRetries     map[string]int      `json:"tool_retries,omitempty"`
	RunID           string              `json:"run_id"`
	RunStep         int                 `json:"run_step"`
	RunStartTime    time.Time           `json:"run_start_time"`
	Prompt          string              `json:"prompt"`
	ToolState       map[string]any      `json:"tool_state,omitempty"`
	Timestamp       time.Time           `json:"timestamp"`
}

// Snapshot creates a serializable snapshot of the current agent run state.
// Call this from a hook (for example OnModelResponse) to capture mid-run state.
func Snapshot(rc *RunContext) *RunSnapshot {
	if rc == nil {
		return nil
	}

	snap := &RunStateSnapshot{
		Messages:     cloneMessages(rc.Messages),
		Usage:        rc.Usage,
		RunID:        rc.RunID,
		RunStep:      rc.RunStep,
		RunStartTime: rc.RunStartTime,
		Prompt:       rc.Prompt,
		Timestamp:    time.Now(),
	}

	if rc.runStateSnapshotGetter != nil {
		if richer := rc.runStateSnapshotGetter(); richer != nil {
			snap = richer
			snap.Messages = cloneMessages(rc.Messages)
			snap.Usage = rc.Usage
			snap.RunID = rc.RunID
			snap.RunStep = rc.RunStep
			snap.RunStartTime = rc.RunStartTime
			snap.Prompt = rc.Prompt
			snap.Timestamp = time.Now()
		}
	}

	if rc.toolStateGetter != nil {
		snap.ToolState = cloneAnyMap(rc.toolStateGetter())
	}

	return snap
}

// MarshalSnapshot serializes a snapshot to JSON using the message serialization API.
func MarshalSnapshot(snap *RunSnapshot) ([]byte, error) {
	encoded, err := EncodeRunSnapshot(snap)
	if err != nil {
		return nil, err
	}
	msgData, err := json.Marshal(encoded.Messages)
	if err != nil {
		return nil, err
	}
	return json.Marshal(snapshotJSON{
		Messages:        msgData,
		Usage:           encoded.Usage,
		LastInputTokens: encoded.LastInputTokens,
		Retries:         encoded.Retries,
		ToolRetries:     cloneIntMap(encoded.ToolRetries),
		RunID:           encoded.RunID,
		RunStep:         encoded.RunStep,
		RunStartTime:    encoded.RunStartTime,
		Prompt:          encoded.Prompt,
		ToolState:       cloneAnyMap(encoded.ToolState),
		Timestamp:       encoded.Timestamp,
	})
}

// UnmarshalSnapshot deserializes a snapshot from JSON.
func UnmarshalSnapshot(data []byte) (*RunSnapshot, error) {
	var sj snapshotJSON
	if err := json.Unmarshal(data, &sj); err != nil {
		return nil, err
	}
	var messages []SerializedMessage
	if err := json.Unmarshal(sj.Messages, &messages); err != nil {
		return nil, err
	}
	return DecodeRunSnapshot(&SerializedRunSnapshot{
		Messages:        messages,
		Usage:           sj.Usage,
		LastInputTokens: sj.LastInputTokens,
		Retries:         sj.Retries,
		ToolRetries:     cloneIntMap(sj.ToolRetries),
		RunID:           sj.RunID,
		RunStep:         sj.RunStep,
		RunStartTime:    sj.RunStartTime,
		Prompt:          sj.Prompt,
		ToolState:       cloneAnyMap(sj.ToolState),
		Timestamp:       sj.Timestamp,
	})
}

// EncodeRunSnapshot converts a run snapshot into its structured serialized form.
func EncodeRunSnapshot(snap *RunSnapshot) (*SerializedRunSnapshot, error) {
	if snap == nil {
		return nil, errors.New("nil run snapshot")
	}
	msgs, err := EncodeMessages(snap.Messages)
	if err != nil {
		return nil, err
	}
	return &SerializedRunSnapshot{
		Messages:        msgs,
		Usage:           snap.Usage,
		LastInputTokens: snap.LastInputTokens,
		Retries:         snap.Retries,
		ToolRetries:     cloneIntMap(snap.ToolRetries),
		RunID:           snap.RunID,
		RunStep:         snap.RunStep,
		RunStartTime:    snap.RunStartTime,
		Prompt:          snap.Prompt,
		ToolState:       cloneAnyMap(snap.ToolState),
		Timestamp:       snap.Timestamp,
	}, nil
}

// DecodeRunSnapshot converts a structured serialized snapshot back into a run snapshot.
func DecodeRunSnapshot(snap *SerializedRunSnapshot) (*RunSnapshot, error) {
	if snap == nil {
		return nil, nil
	}
	msgs, err := DecodeMessages(snap.Messages)
	if err != nil {
		return nil, err
	}
	return &RunStateSnapshot{
		Messages:        msgs,
		Usage:           snap.Usage,
		LastInputTokens: snap.LastInputTokens,
		Retries:         snap.Retries,
		ToolRetries:     cloneIntMap(snap.ToolRetries),
		RunID:           snap.RunID,
		RunStep:         snap.RunStep,
		RunStartTime:    snap.RunStartTime,
		Prompt:          snap.Prompt,
		ToolState:       cloneAnyMap(snap.ToolState),
		Timestamp:       snap.Timestamp,
	}, nil
}

// WithSnapshot resumes a run from a snapshot. The agent continues from the
// snapshot's serialized run state rather than starting fresh.
func WithSnapshot(snap *RunSnapshot) RunOption {
	return func(c *runConfig) {
		c.snapshot = snap
	}
}

// Branch creates a modified copy of the snapshot for exploring alternate paths.
func (s *RunStateSnapshot) Branch(modifier func(messages []ModelMessage) []ModelMessage) *RunSnapshot {
	msgs := cloneMessages(s.Messages)
	modified := modifier(msgs)
	return &RunStateSnapshot{
		Messages:        modified,
		Usage:           s.Usage,
		LastInputTokens: s.LastInputTokens,
		Retries:         s.Retries,
		ToolRetries:     cloneIntMap(s.ToolRetries),
		RunID:           s.RunID,
		RunStep:         s.RunStep,
		RunStartTime:    s.RunStartTime,
		Prompt:          s.Prompt,
		ToolState:       cloneAnyMap(s.ToolState),
		Timestamp:       time.Now(),
	}
}

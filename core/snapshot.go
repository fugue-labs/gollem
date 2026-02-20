package core

import (
	"encoding/json"
	"time"
)

// RunSnapshot captures the serializable state of an agent run at a point in time.
type RunSnapshot struct {
	Messages  []ModelMessage `json:"-"` // excluded from default JSON
	Usage     RunUsage       `json:"usage"`
	RunID     string         `json:"run_id"`
	RunStep   int            `json:"run_step"`
	Prompt    string         `json:"prompt"`
	Timestamp time.Time      `json:"timestamp"`
}

// snapshotJSON is the JSON-safe serialization of RunSnapshot.
type snapshotJSON struct {
	Messages  json.RawMessage `json:"messages"`
	Usage     RunUsage        `json:"usage"`
	RunID     string          `json:"run_id"`
	RunStep   int             `json:"run_step"`
	Prompt    string          `json:"prompt"`
	Timestamp time.Time       `json:"timestamp"`
}

// Snapshot creates a serializable snapshot of the current agent run state.
// Call this from a hook (OnModelResponse) to capture mid-run state.
func Snapshot(rc *RunContext) *RunSnapshot {
	msgs := make([]ModelMessage, len(rc.Messages))
	copy(msgs, rc.Messages)
	return &RunSnapshot{
		Messages:  msgs,
		Usage:     rc.Usage,
		RunID:     rc.RunID,
		RunStep:   rc.RunStep,
		Prompt:    rc.Prompt,
		Timestamp: time.Now(),
	}
}

// MarshalSnapshot serializes a snapshot to JSON using the message serialization API.
func MarshalSnapshot(snap *RunSnapshot) ([]byte, error) {
	msgData, err := MarshalMessages(snap.Messages)
	if err != nil {
		return nil, err
	}
	return json.Marshal(snapshotJSON{
		Messages:  msgData,
		Usage:     snap.Usage,
		RunID:     snap.RunID,
		RunStep:   snap.RunStep,
		Prompt:    snap.Prompt,
		Timestamp: snap.Timestamp,
	})
}

// UnmarshalSnapshot deserializes a snapshot from JSON.
func UnmarshalSnapshot(data []byte) (*RunSnapshot, error) {
	var sj snapshotJSON
	if err := json.Unmarshal(data, &sj); err != nil {
		return nil, err
	}
	msgs, err := UnmarshalMessages(sj.Messages)
	if err != nil {
		return nil, err
	}
	return &RunSnapshot{
		Messages:  msgs,
		Usage:     sj.Usage,
		RunID:     sj.RunID,
		RunStep:   sj.RunStep,
		Prompt:    sj.Prompt,
		Timestamp: sj.Timestamp,
	}, nil
}

// WithSnapshot resumes a run from a snapshot. The agent continues from the
// snapshot's message history rather than starting fresh.
func WithSnapshot(snap *RunSnapshot) RunOption {
	return func(c *runConfig) {
		c.messages = make([]ModelMessage, len(snap.Messages))
		copy(c.messages, snap.Messages)
	}
}

// Branch creates a modified copy of the snapshot for exploring alternate paths.
func (s *RunSnapshot) Branch(modifier func(messages []ModelMessage) []ModelMessage) *RunSnapshot {
	msgs := make([]ModelMessage, len(s.Messages))
	copy(msgs, s.Messages)
	modified := modifier(msgs)
	return &RunSnapshot{
		Messages:  modified,
		Usage:     s.Usage,
		RunID:     s.RunID,
		RunStep:   s.RunStep,
		Prompt:    s.Prompt,
		Timestamp: time.Now(),
	}
}

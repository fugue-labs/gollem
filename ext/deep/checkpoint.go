package deep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// Checkpoint captures the state of an agent run at a point in time.
type Checkpoint struct {
	Messages   []core.ModelMessage `json:"-"`
	Usage      core.RunUsage      `json:"usage"`
	RunID      string               `json:"run_id"`
	StepIndex  int                  `json:"step_index"`
	Timestamp  time.Time            `json:"timestamp"`
	Metadata   map[string]any       `json:"metadata,omitempty"`
	ToolStates map[string]any       `json:"tool_states,omitempty"`
}

// checkpointJSON is the serializable form of Checkpoint.
type checkpointJSON struct {
	Messages   []messageEnvelope `json:"messages"`
	Usage      core.RunUsage   `json:"usage"`
	RunID      string            `json:"run_id"`
	StepIndex  int               `json:"step_index"`
	Timestamp  time.Time         `json:"timestamp"`
	Metadata   map[string]any    `json:"metadata,omitempty"`
	ToolStates map[string]any    `json:"tool_states,omitempty"`
}

// messageEnvelope wraps a ModelMessage for JSON serialization.
type messageEnvelope struct {
	Kind    string          `json:"kind"` // "request" or "response"
	RawData json.RawMessage `json:"data"`
}

// MarshalJSON implements custom JSON marshaling for Checkpoint.
func (cp Checkpoint) MarshalJSON() ([]byte, error) {
	cj := checkpointJSON{
		Messages:   encodeMessages(cp.Messages),
		Usage:      cp.Usage,
		RunID:      cp.RunID,
		StepIndex:  cp.StepIndex,
		Timestamp:  cp.Timestamp,
		Metadata:   cp.Metadata,
		ToolStates: cp.ToolStates,
	}
	return json.Marshal(cj)
}

// UnmarshalJSON implements custom JSON unmarshaling for Checkpoint.
func (cp *Checkpoint) UnmarshalJSON(data []byte) error {
	var cj checkpointJSON
	if err := json.Unmarshal(data, &cj); err != nil {
		return err
	}
	messages, err := decodeMessages(cj.Messages)
	if err != nil {
		return fmt.Errorf("decoding messages: %w", err)
	}
	cp.Usage = cj.Usage
	cp.RunID = cj.RunID
	cp.StepIndex = cj.StepIndex
	cp.Timestamp = cj.Timestamp
	cp.Metadata = cj.Metadata
	cp.ToolStates = cj.ToolStates
	cp.Messages = messages
	return nil
}

// CheckpointStore persists and retrieves checkpoints.
type CheckpointStore interface {
	Save(ctx context.Context, checkpoint *Checkpoint) error
	Load(ctx context.Context, runID string) (*Checkpoint, error)
	List(ctx context.Context) ([]*Checkpoint, error)
	Delete(ctx context.Context, runID string) error
}

// FileCheckpointStore stores checkpoints as JSON files.
type FileCheckpointStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileCheckpointStore creates a file-based checkpoint store.
func NewFileCheckpointStore(dir string) (*FileCheckpointStore, error) {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "gollem-checkpoints")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating checkpoint directory: %w", err)
	}
	return &FileCheckpointStore{dir: dir}, nil
}

// checkpointFilename returns the filename for a checkpoint.
// If StepIndex > 0, uses "runID_step.json"; otherwise uses "runID.json"
// for backward compatibility.
func checkpointFilename(runID string, stepIndex int) string {
	if stepIndex > 0 {
		return fmt.Sprintf("%s_%d.json", runID, stepIndex)
	}
	return runID + ".json"
}

// Save persists a checkpoint to disk.
func (s *FileCheckpointStore) Save(_ context.Context, checkpoint *Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("marshaling checkpoint: %w", err)
	}

	path := filepath.Join(s.dir, checkpointFilename(checkpoint.RunID, checkpoint.StepIndex))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing checkpoint: %w", err)
	}
	return nil
}

// Load retrieves the latest checkpoint by run ID.
func (s *FileCheckpointStore) Load(_ context.Context, runID string) (*Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.loadLatest(runID)
}

// loadLatest finds the latest checkpoint for a run ID (must hold mu).
func (s *FileCheckpointStore) loadLatest(runID string) (*Checkpoint, error) {
	checkpoints, err := s.loadAllForRun(runID)
	if err != nil {
		return nil, err
	}
	if len(checkpoints) == 0 {
		return nil, fmt.Errorf("checkpoint %q not found", runID)
	}
	// Return the one with the highest step index.
	return checkpoints[len(checkpoints)-1], nil
}

// loadAllForRun returns all checkpoints for a run ID sorted by StepIndex (must hold mu).
func (s *FileCheckpointStore) loadAllForRun(runID string) ([]*Checkpoint, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("listing checkpoint directory: %w", err)
	}

	var checkpoints []*Checkpoint
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		// Match either "runID" or "runID_N".
		if name != runID && !strings.HasPrefix(name, runID+"_") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if readErr != nil {
			continue
		}
		var cp Checkpoint
		if unmarshalErr := json.Unmarshal(data, &cp); unmarshalErr != nil {
			continue
		}
		if cp.RunID == runID {
			checkpoints = append(checkpoints, &cp)
		}
	}

	sort.Slice(checkpoints, func(i, j int) bool {
		return checkpoints[i].StepIndex < checkpoints[j].StepIndex
	})

	return checkpoints, nil
}

// List returns all stored checkpoints.
func (s *FileCheckpointStore) List(_ context.Context) ([]*Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("listing checkpoints: %w", err)
	}

	var checkpoints []*Checkpoint
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}
		var cp Checkpoint
		if err := json.Unmarshal(data, &cp); err != nil {
			continue
		}
		checkpoints = append(checkpoints, &cp)
	}
	return checkpoints, nil
}

// Delete removes all checkpoints for a run ID.
func (s *FileCheckpointStore) Delete(_ context.Context, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("listing checkpoint directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		if name == runID || strings.HasPrefix(name, runID+"_") {
			path := filepath.Join(s.dir, entry.Name())
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("deleting checkpoint: %w", removeErr)
			}
		}
	}
	return nil
}

// GetHistory returns all checkpoints for a run in chronological order (by StepIndex).
func (s *FileCheckpointStore) GetHistory(ctx context.Context, runID string) ([]*Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.loadAllForRun(runID)
}

// ResumeFromCheckpoint loads a checkpoint and returns a RunOption that
// initializes the agent run with the checkpoint's message history.
func ResumeFromCheckpoint(store CheckpointStore, runID string) (core.RunOption, error) {
	cp, err := store.Load(context.Background(), runID)
	if err != nil {
		return nil, fmt.Errorf("loading checkpoint: %w", err)
	}
	return core.WithMessages(cp.Messages...), nil
}

// ReplayFrom creates a RunOption that resumes from a specific checkpoint step.
// It loads the checkpoint at the given stepIndex and returns a RunOption that
// initializes the agent run with that checkpoint's message history.
func ReplayFrom(store CheckpointStore, runID string, stepIndex int) (core.RunOption, error) {
	fcs, ok := store.(*FileCheckpointStore)
	if !ok {
		return nil, errors.New("ReplayFrom requires a *FileCheckpointStore")
	}

	fcs.mu.Lock()
	defer fcs.mu.Unlock()

	checkpoints, err := fcs.loadAllForRun(runID)
	if err != nil {
		return nil, fmt.Errorf("loading checkpoints: %w", err)
	}

	for _, cp := range checkpoints {
		if cp.StepIndex == stepIndex {
			msgs := make([]core.ModelMessage, len(cp.Messages))
			copy(msgs, cp.Messages)
			return core.WithMessages(msgs...), nil
		}
	}

	return nil, fmt.Errorf("checkpoint step %d not found for run %q", stepIndex, runID)
}

// ForkFrom creates a RunOption that starts from a checkpoint with modified state.
// The modifier function receives a copy of the checkpoint and can alter its
// Messages, Metadata, ToolStates, or any other fields before the run begins.
func ForkFrom(store CheckpointStore, runID string, stepIndex int, modifier func(*Checkpoint)) (core.RunOption, error) {
	fcs, ok := store.(*FileCheckpointStore)
	if !ok {
		return nil, errors.New("ForkFrom requires a *FileCheckpointStore")
	}

	fcs.mu.Lock()
	defer fcs.mu.Unlock()

	checkpoints, err := fcs.loadAllForRun(runID)
	if err != nil {
		return nil, fmt.Errorf("loading checkpoints: %w", err)
	}

	for _, cp := range checkpoints {
		if cp.StepIndex == stepIndex {
			// Create a copy of the checkpoint so the modifier doesn't affect the original.
			forked := &Checkpoint{
				Messages:   make([]core.ModelMessage, len(cp.Messages)),
				Usage:      cp.Usage,
				RunID:      cp.RunID,
				StepIndex:  cp.StepIndex,
				Timestamp:  cp.Timestamp,
				Metadata:   copyMap(cp.Metadata),
				ToolStates: copyMap(cp.ToolStates),
			}
			copy(forked.Messages, cp.Messages)

			if modifier != nil {
				modifier(forked)
			}

			return core.WithMessages(forked.Messages...), nil
		}
	}

	return nil, fmt.Errorf("checkpoint step %d not found for run %q", stepIndex, runID)
}

// ExportToolStates polls all tools for the StatefulTool interface and exports
// their state into a map keyed by tool name.
func ExportToolStates(tools []core.Tool) (map[string]any, error) {
	states := make(map[string]any)
	for _, t := range tools {
		if t.Stateful != nil {
			state, err := t.Stateful.ExportState()
			if err != nil {
				return nil, fmt.Errorf("exporting state for tool %q: %w", t.Definition.Name, err)
			}
			states[t.Definition.Name] = state
		}
	}
	return states, nil
}

// RestoreToolStates restores tool state from a checkpoint's ToolStates map.
func RestoreToolStates(tools []core.Tool, states map[string]any) error {
	if len(states) == 0 {
		return nil
	}
	for _, t := range tools {
		if t.Stateful != nil {
			if state, exists := states[t.Definition.Name]; exists {
				if err := t.Stateful.RestoreState(state); err != nil {
					return fmt.Errorf("restoring state for tool %q: %w", t.Definition.Name, err)
				}
			}
		}
	}
	return nil
}

// copyMap creates a shallow copy of a map[string]any.
func copyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}


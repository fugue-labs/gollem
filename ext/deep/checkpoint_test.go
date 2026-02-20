package deep

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestCheckpoint_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	cp := &Checkpoint{
		Messages: []core.ModelMessage{
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.UserPromptPart{Content: "Hello"},
				},
				Timestamp: time.Now(),
			},
		},
		Usage: core.RunUsage{
			Requests: 1,
		},
		RunID:     "run-123",
		Timestamp: time.Now(),
		Metadata:  map[string]any{"key": "value"},
	}

	ctx := context.Background()
	if err := store.Save(ctx, cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(ctx, "run-123")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.RunID != "run-123" {
		t.Errorf("RunID = %q, want %q", loaded.RunID, "run-123")
	}
	if loaded.Usage.Requests != 1 {
		t.Errorf("Requests = %d, want 1", loaded.Usage.Requests)
	}
}

func TestCheckpoint_LoadNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	_, err = store.Load(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent checkpoint")
	}
}

func TestCheckpoint_List(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	ctx := context.Background()

	// Save multiple checkpoints.
	for _, id := range []string{"run-1", "run-2", "run-3"} {
		cp := &Checkpoint{
			RunID:     id,
			Timestamp: time.Now(),
		}
		if err := store.Save(ctx, cp); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("expected 3 checkpoints, got %d", len(list))
	}
}

func TestCheckpoint_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	ctx := context.Background()
	cp := &Checkpoint{RunID: "run-del", Timestamp: time.Now()}
	if err := store.Save(ctx, cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.Delete(ctx, "run-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.Load(ctx, "run-del")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestCheckpoint_DeleteNonexistent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	// Should not error.
	if err := store.Delete(context.Background(), "nonexistent"); err != nil {
		t.Fatalf("Delete nonexistent: %v", err)
	}
}

func TestCheckpoint_DefaultDir(t *testing.T) {
	store, err := NewFileCheckpointStore("")
	if err != nil {
		t.Fatalf("NewFileCheckpointStore with empty dir: %v", err)
	}

	ctx := context.Background()
	cp := &Checkpoint{RunID: "run-default", Timestamp: time.Now()}
	if err := store.Save(ctx, cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(ctx, "run-default")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.RunID != "run-default" {
		t.Errorf("RunID = %q, want %q", loaded.RunID, "run-default")
	}

	_ = store.Delete(ctx, "run-default")
}

func TestResumeFromCheckpoint(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	ctx := context.Background()
	cp := &Checkpoint{
		Messages: []core.ModelMessage{
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.UserPromptPart{Content: "Previous conversation"},
				},
				Timestamp: time.Now(),
			},
		},
		RunID:     "resume-run",
		Timestamp: time.Now(),
	}
	if err := store.Save(ctx, cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	opt, err := ResumeFromCheckpoint(store, "resume-run")
	if err != nil {
		t.Fatalf("ResumeFromCheckpoint: %v", err)
	}
	if opt == nil {
		t.Fatal("expected non-nil RunOption")
	}
}

func TestResumeFromCheckpoint_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	_, err = ResumeFromCheckpoint(store, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent checkpoint")
	}
}

func TestCheckpoint_GetHistory(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	ctx := context.Background()

	// Save 5 checkpoints for the same run with different step indices.
	for i := range 5 {
		cp := &Checkpoint{
			RunID:     "history-run",
			StepIndex: i + 1,
			Timestamp: time.Now(),
			Messages: []core.ModelMessage{
				core.ModelRequest{
					Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "step"}},
					Timestamp: time.Now(),
				},
			},
		}
		if err := store.Save(ctx, cp); err != nil {
			t.Fatalf("Save step %d: %v", i+1, err)
		}
	}

	history, err := store.GetHistory(ctx, "history-run")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) != 5 {
		t.Fatalf("expected 5 checkpoints, got %d", len(history))
	}

	// Verify chronological order.
	for i, cp := range history {
		expected := i + 1
		if cp.StepIndex != expected {
			t.Errorf("history[%d].StepIndex = %d, want %d", i, cp.StepIndex, expected)
		}
	}
}

func TestCheckpoint_ReplayFrom(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	ctx := context.Background()

	// Save 5 checkpoints.
	for i := range 5 {
		cp := &Checkpoint{
			RunID:     "replay-run",
			StepIndex: i + 1,
			Timestamp: time.Now(),
			Messages: []core.ModelMessage{
				core.ModelRequest{
					Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "msg at step " + time.Now().String()}},
					Timestamp: time.Now(),
				},
			},
		}
		if err := store.Save(ctx, cp); err != nil {
			t.Fatalf("Save step %d: %v", i+1, err)
		}
	}

	// Replay from step 2.
	opt, err := ReplayFrom(store, "replay-run", 2)
	if err != nil {
		t.Fatalf("ReplayFrom: %v", err)
	}
	if opt == nil {
		t.Fatal("expected non-nil RunOption")
	}

	// Replay from nonexistent step.
	_, err = ReplayFrom(store, "replay-run", 99)
	if err == nil {
		t.Fatal("expected error for nonexistent step")
	}
}

func TestCheckpoint_ForkFrom(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	ctx := context.Background()

	cp := &Checkpoint{
		RunID:     "fork-run",
		StepIndex: 1,
		Timestamp: time.Now(),
		Messages: []core.ModelMessage{
			core.ModelRequest{
				Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "original"}},
				Timestamp: time.Now(),
			},
		},
		Metadata: map[string]any{"original": true},
	}
	if err := store.Save(ctx, cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Fork and modify metadata.
	opt, err := ForkFrom(store, "fork-run", 1, func(cp *Checkpoint) {
		cp.Metadata["forked"] = true
	})
	if err != nil {
		t.Fatalf("ForkFrom: %v", err)
	}
	if opt == nil {
		t.Fatal("expected non-nil RunOption")
	}

	// Verify the original checkpoint is not modified.
	loaded, err := store.Load(ctx, "fork-run")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := loaded.Metadata["forked"]; ok {
		t.Error("original checkpoint should not have 'forked' metadata")
	}
}

func TestCheckpoint_StepIndex(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	ctx := context.Background()

	// Save checkpoints with different step indices.
	for _, idx := range []int{1, 2, 3} {
		cp := &Checkpoint{
			RunID:     "step-run",
			StepIndex: idx,
			Timestamp: time.Now(),
		}
		if err := store.Save(ctx, cp); err != nil {
			t.Fatalf("Save step %d: %v", idx, err)
		}
	}

	// Load should return the highest step index.
	loaded, err := store.Load(ctx, "step-run")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.StepIndex != 3 {
		t.Errorf("Load returned StepIndex %d, want 3 (latest)", loaded.StepIndex)
	}
}

func TestCheckpoint_ToolState(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCheckpointStore(dir)
	if err != nil {
		t.Fatalf("NewFileCheckpointStore: %v", err)
	}

	ctx := context.Background()

	// Create a mock stateful tool.
	mockTool := &mockStatefulTool{state: map[string]any{"plan": "step 1"}}
	tools := []core.Tool{
		{
			Definition: core.ToolDefinition{Name: "planner"},
			Stateful:   mockTool,
		},
	}

	// Export tool states.
	states, err := ExportToolStates(tools)
	if err != nil {
		t.Fatalf("ExportToolStates: %v", err)
	}

	// Save checkpoint with tool states.
	cp := &Checkpoint{
		RunID:      "tool-state-run",
		StepIndex:  1,
		Timestamp:  time.Now(),
		ToolStates: states,
	}
	if err := store.Save(ctx, cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load and restore.
	loaded, err := store.Load(ctx, "tool-state-run")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Reset mock tool state.
	mockTool.state = nil

	// Restore tool states.
	if err := RestoreToolStates(tools, loaded.ToolStates); err != nil {
		t.Fatalf("RestoreToolStates: %v", err)
	}

	// Verify the tool state was restored.
	if mockTool.state == nil {
		t.Fatal("expected tool state to be restored")
	}
}

// mockStatefulTool implements core.StatefulTool for testing.
type mockStatefulTool struct {
	state map[string]any
}

func (m *mockStatefulTool) ExportState() (any, error) {
	return m.state, nil
}

func (m *mockStatefulTool) RestoreState(state any) error {
	if s, ok := state.(map[string]any); ok {
		m.state = s
	}
	return nil
}

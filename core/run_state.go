package core

import (
	"sync"
	"time"
)

// RunState tracks mutable state across an agent run.
type RunState struct {
	messages        []ModelMessage
	usage           RunUsage
	lastInputTokens int // input tokens from the most recent model response (0 on first turn)
	retries         int
	toolRetries     map[string]int
	runStep         int
	runID           string
	startTime       time.Time
	limits          UsageLimits
	detach          <-chan struct{} // UI detach signal; nil if not configured
	mu              sync.Mutex      // protects usage, toolRetries, and traceSteps during concurrent tool execution
	traceSteps      []TraceStep
}

// agentRunState is kept as an internal alias during the core refactor to avoid
// rewriting the entire package in one change.
type agentRunState = RunState

// RunStateSnapshot captures the serializable state of an agent run at a point in time.
type RunStateSnapshot struct {
	Messages        []ModelMessage `json:"-"` // excluded from default JSON
	Usage           RunUsage       `json:"usage"`
	LastInputTokens int            `json:"last_input_tokens"`
	Retries         int            `json:"retries"`
	ToolRetries     map[string]int `json:"tool_retries,omitempty"`
	RunID           string         `json:"run_id"`
	RunStep         int            `json:"run_step"`
	RunStartTime    time.Time      `json:"run_start_time"`
	Prompt          string         `json:"prompt"`
	ToolState       map[string]any `json:"tool_state,omitempty"`
	Timestamp       time.Time      `json:"timestamp"`
}

func newRunState(detach <-chan struct{}, limits UsageLimits) *RunState {
	return &RunState{
		toolRetries: make(map[string]int),
		runID:       newRunID(),
		startTime:   time.Now(),
		limits:      limits,
		detach:      detach,
	}
}

func (s *RunState) applySnapshot(snap *RunStateSnapshot) {
	if s == nil || snap == nil {
		return
	}
	s.messages = cloneMessages(snap.Messages)
	s.usage = snap.Usage
	s.lastInputTokens = snap.LastInputTokens
	s.retries = snap.Retries
	s.toolRetries = cloneIntMap(snap.ToolRetries)
	if s.toolRetries == nil {
		s.toolRetries = make(map[string]int)
	}
	s.runStep = snap.RunStep
	if snap.RunID != "" {
		s.runID = snap.RunID
	}
	if !snap.RunStartTime.IsZero() {
		s.startTime = snap.RunStartTime
	}
}

func (s *RunState) snapshot(prompt string, toolState map[string]any) *RunStateSnapshot {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	usage := s.usage
	toolRetries := cloneIntMap(s.toolRetries)
	s.mu.Unlock()

	return &RunStateSnapshot{
		Messages:        cloneMessages(s.messages),
		Usage:           usage,
		LastInputTokens: s.lastInputTokens,
		Retries:         s.retries,
		ToolRetries:     toolRetries,
		RunID:           s.runID,
		RunStep:         s.runStep,
		RunStartTime:    s.startTime,
		Prompt:          prompt,
		ToolState:       cloneAnyMap(toolState),
		Timestamp:       time.Now(),
	}
}

func cloneMessages(messages []ModelMessage) []ModelMessage {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]ModelMessage, len(messages))
	copy(cloned, messages)
	return cloned
}

func cloneIntMap(src map[string]int) map[string]int {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]int, len(src))
	for k, v := range src {
		cloned[k] = v
	}
	return cloned
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(src))
	for k, v := range src {
		cloned[k] = v
	}
	return cloned
}

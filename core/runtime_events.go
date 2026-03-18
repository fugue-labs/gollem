package core

import "time"

const (
	// RuntimeEventTypeRunStarted marks a run-start lifecycle event.
	RuntimeEventTypeRunStarted = "run_started"
	// RuntimeEventTypeRunCompleted marks a run-complete lifecycle event.
	RuntimeEventTypeRunCompleted = "run_completed"
	// RuntimeEventTypeToolCalled marks a tool-start lifecycle event.
	RuntimeEventTypeToolCalled = "tool_called"
	// RuntimeEventTypeToolCompleted marks a tool-complete lifecycle event.
	RuntimeEventTypeToolCompleted = "tool_completed"
	// RuntimeEventTypeApprovalRequested marks a tool-approval-requested lifecycle event.
	RuntimeEventTypeApprovalRequested = "approval_requested"
	// RuntimeEventTypeApprovalResolved marks a tool-approval-resolved lifecycle event.
	RuntimeEventTypeApprovalResolved = "approval_resolved"
	// RuntimeEventTypeDeferredWait marks a deferred-wait lifecycle event.
	RuntimeEventTypeDeferredWait = "deferred_wait"
)

// RuntimeEvent is implemented by built-in runtime lifecycle events.
type RuntimeEvent interface {
	RuntimeEventType() string
	RuntimeRunID() string
	RuntimeParentRunID() string
	RuntimeOccurredAt() time.Time
}

// RunStartedEvent is published when an agent run starts.
type RunStartedEvent struct {
	RunID       string
	ParentRunID string
	Prompt      string
	StartedAt   time.Time
}

func (e RunStartedEvent) RuntimeEventType() string     { return RuntimeEventTypeRunStarted }
func (e RunStartedEvent) RuntimeRunID() string         { return e.RunID }
func (e RunStartedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e RunStartedEvent) RuntimeOccurredAt() time.Time { return e.StartedAt }

// RunCompletedEvent is published when an agent run completes.
type RunCompletedEvent struct {
	RunID       string
	ParentRunID string
	Success     bool
	Error       string
	StartedAt   time.Time
	CompletedAt time.Time
}

func (e RunCompletedEvent) RuntimeEventType() string     { return RuntimeEventTypeRunCompleted }
func (e RunCompletedEvent) RuntimeRunID() string         { return e.RunID }
func (e RunCompletedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e RunCompletedEvent) RuntimeOccurredAt() time.Time { return e.CompletedAt }

// ToolCalledEvent is published when a tool call starts.
type ToolCalledEvent struct {
	RunID       string
	ParentRunID string
	ToolCallID  string
	ToolName    string
	ArgsJSON    string
	CalledAt    time.Time
}

func (e ToolCalledEvent) RuntimeEventType() string     { return RuntimeEventTypeToolCalled }
func (e ToolCalledEvent) RuntimeRunID() string         { return e.RunID }
func (e ToolCalledEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ToolCalledEvent) RuntimeOccurredAt() time.Time { return e.CalledAt }

// ToolCompletedEvent is published when a tool call finishes, whether it
// succeeded, failed, or deferred.
type ToolCompletedEvent struct {
	RunID       string
	ParentRunID string
	ToolCallID  string
	ToolName    string
	Result      string
	Error       string
	CompletedAt time.Time
}

func (e ToolCompletedEvent) RuntimeEventType() string     { return RuntimeEventTypeToolCompleted }
func (e ToolCompletedEvent) RuntimeRunID() string         { return e.RunID }
func (e ToolCompletedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ToolCompletedEvent) RuntimeOccurredAt() time.Time { return e.CompletedAt }

// ApprovalRequestedEvent is published when a tool requiring approval reaches
// the approval gate.
type ApprovalRequestedEvent struct {
	RunID       string
	ParentRunID string
	ToolCallID  string
	ToolName    string
	ArgsJSON    string
	RequestedAt time.Time
}

func (e ApprovalRequestedEvent) RuntimeEventType() string     { return RuntimeEventTypeApprovalRequested }
func (e ApprovalRequestedEvent) RuntimeRunID() string         { return e.RunID }
func (e ApprovalRequestedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ApprovalRequestedEvent) RuntimeOccurredAt() time.Time { return e.RequestedAt }

// ApprovalResolvedEvent is published when a tool approval request is resolved.
type ApprovalResolvedEvent struct {
	RunID       string
	ParentRunID string
	ToolCallID  string
	ToolName    string
	ArgsJSON    string
	Approved    bool
	Error       string
	ResolvedAt  time.Time
}

func (e ApprovalResolvedEvent) RuntimeEventType() string     { return RuntimeEventTypeApprovalResolved }
func (e ApprovalResolvedEvent) RuntimeRunID() string         { return e.RunID }
func (e ApprovalResolvedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ApprovalResolvedEvent) RuntimeOccurredAt() time.Time { return e.ResolvedAt }

// DeferredWaitEvent is published when a tool asks the runtime to wait for
// external resolution.
type DeferredWaitEvent struct {
	RunID       string
	ParentRunID string
	ToolCallID  string
	ToolName    string
	ArgsJSON    string
	Message     string
	WaitingAt   time.Time
}

func (e DeferredWaitEvent) RuntimeEventType() string     { return RuntimeEventTypeDeferredWait }
func (e DeferredWaitEvent) RuntimeRunID() string         { return e.RunID }
func (e DeferredWaitEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e DeferredWaitEvent) RuntimeOccurredAt() time.Time { return e.WaitingAt }

// NewRunStartedEvent constructs a standardized run-start event.
func NewRunStartedEvent(runID, parentRunID, prompt string, startedAt time.Time) RunStartedEvent {
	return RunStartedEvent{
		RunID:       runID,
		ParentRunID: parentRunID,
		Prompt:      prompt,
		StartedAt:   startedAt,
	}
}

// NewRunCompletedEvent constructs a standardized run-complete event.
func NewRunCompletedEvent(runID, parentRunID string, startedAt, completedAt time.Time, runErr error) RunCompletedEvent {
	evt := RunCompletedEvent{
		RunID:       runID,
		ParentRunID: parentRunID,
		Success:     runErr == nil,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}
	if runErr != nil {
		evt.Error = runErr.Error()
	}
	return evt
}

// NewToolCalledEvent constructs a standardized tool-start event.
func NewToolCalledEvent(runID, parentRunID, toolCallID, toolName, argsJSON string, calledAt time.Time) ToolCalledEvent {
	return ToolCalledEvent{
		RunID:       runID,
		ParentRunID: parentRunID,
		ToolCallID:  toolCallID,
		ToolName:    toolName,
		ArgsJSON:    argsJSON,
		CalledAt:    calledAt,
	}
}

// NewToolCompletedEvent constructs a standardized tool-complete event.
func NewToolCompletedEvent(runID, parentRunID, toolCallID, toolName, result string, completedAt time.Time, toolErr error) ToolCompletedEvent {
	evt := ToolCompletedEvent{
		RunID:       runID,
		ParentRunID: parentRunID,
		ToolCallID:  toolCallID,
		ToolName:    toolName,
		Result:      result,
		CompletedAt: completedAt,
	}
	if toolErr != nil {
		evt.Error = toolErr.Error()
	}
	return evt
}

// NewApprovalRequestedEvent constructs a standardized approval-requested event.
func NewApprovalRequestedEvent(runID, parentRunID, toolCallID, toolName, argsJSON string, requestedAt time.Time) ApprovalRequestedEvent {
	return ApprovalRequestedEvent{
		RunID:       runID,
		ParentRunID: parentRunID,
		ToolCallID:  toolCallID,
		ToolName:    toolName,
		ArgsJSON:    argsJSON,
		RequestedAt: requestedAt,
	}
}

// NewApprovalResolvedEvent constructs a standardized approval-resolved event.
func NewApprovalResolvedEvent(runID, parentRunID, toolCallID, toolName, argsJSON string, approved bool, resolvedAt time.Time, approvalErr error) ApprovalResolvedEvent {
	evt := ApprovalResolvedEvent{
		RunID:       runID,
		ParentRunID: parentRunID,
		ToolCallID:  toolCallID,
		ToolName:    toolName,
		ArgsJSON:    argsJSON,
		Approved:    approved,
		ResolvedAt:  resolvedAt,
	}
	if approvalErr != nil {
		evt.Error = approvalErr.Error()
	}
	return evt
}

// NewDeferredWaitEvent constructs a standardized deferred-wait event.
func NewDeferredWaitEvent(runID, parentRunID, toolCallID, toolName, argsJSON, message string, waitingAt time.Time) DeferredWaitEvent {
	return DeferredWaitEvent{
		RunID:       runID,
		ParentRunID: parentRunID,
		ToolCallID:  toolCallID,
		ToolName:    toolName,
		ArgsJSON:    argsJSON,
		Message:     message,
		WaitingAt:   waitingAt,
	}
}

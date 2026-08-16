package core

import (
	"errors"
	"time"
)

const (
	// RuntimeEventTypeRunStarted marks a run-start lifecycle event.
	RuntimeEventTypeRunStarted = "run_started"
	// RuntimeEventTypeRunCompleted marks a run-complete lifecycle event.
	RuntimeEventTypeRunCompleted = "run_completed"
	// RuntimeEventTypeToolCalled marks a tool-start lifecycle event.
	RuntimeEventTypeToolCalled = "tool_called"
	// RuntimeEventTypeToolCompleted marks a tool-end lifecycle event (success).
	RuntimeEventTypeToolCompleted = "tool_completed"
	// RuntimeEventTypeToolFailed marks a tool-end lifecycle event (error).
	RuntimeEventTypeToolFailed = "tool_failed"
	// RuntimeEventTypeTurnStarted marks the start of an agent turn.
	RuntimeEventTypeTurnStarted = "turn_started"
	// RuntimeEventTypeTurnCompleted marks the end of an agent turn.
	RuntimeEventTypeTurnCompleted = "turn_completed"
	// RuntimeEventTypeModelRequestStarted marks the start of a model request.
	RuntimeEventTypeModelRequestStarted = "model_request_started"
	// RuntimeEventTypeModelResponseCompleted marks the end of a model response.
	RuntimeEventTypeModelResponseCompleted = "model_response_completed"
	// RuntimeEventTypeModelDelta marks an incremental streamed model response delta.
	RuntimeEventTypeModelDelta = "model_delta"
	// RuntimeEventTypeApprovalRequested marks a tool approval request.
	RuntimeEventTypeApprovalRequested = "approval_requested"
	// RuntimeEventTypeApprovalResolved marks a tool approval resolution.
	RuntimeEventTypeApprovalResolved = "approval_resolved"
	// RuntimeEventTypeDeferredRequested marks a deferred tool request.
	RuntimeEventTypeDeferredRequested = "deferred_requested"
	// RuntimeEventTypeDeferredResolved marks a deferred tool resolution.
	RuntimeEventTypeDeferredResolved = "deferred_resolved"
	// RuntimeEventTypeRunWaiting marks a run entering a waiting state.
	RuntimeEventTypeRunWaiting = "run_waiting"
	// RuntimeEventTypeRunResumed marks a run resuming from a waiting state.
	RuntimeEventTypeRunResumed = "run_resumed"
	// RuntimeEventTypeArtifactChanged marks a workspace artifact/file mutation.
	RuntimeEventTypeArtifactChanged = "artifact_changed"
	// RuntimeEventTypeRetryScheduled marks a model retry requested by the runtime.
	RuntimeEventTypeRetryScheduled = "retry_scheduled"
	// RuntimeEventTypeCheckpointCreated marks a replay/fork checkpoint boundary.
	RuntimeEventTypeCheckpointCreated = "checkpoint_created"
	// RuntimeEventTypeTopologyTransitioned marks a runtime topology change.
	RuntimeEventTypeTopologyTransitioned = "topology_transitioned"
	// RuntimeEventTypeEvaluatorCompleted marks evaluator output attached to a run.
	RuntimeEventTypeEvaluatorCompleted = "evaluator_completed"
	// RuntimeEventTypeErrorRaised marks an execution error boundary.
	RuntimeEventTypeErrorRaised = "error_raised"
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
	Deferred    bool
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
		Deferred:    isDeferredRunError(runErr),
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}
	if runErr != nil {
		evt.Error = runErr.Error()
	}
	return evt
}

type deferredRunError interface {
	error
	deferredRunError()
}

func isDeferredRunError(err error) bool {
	if err == nil {
		return false
	}
	var deferred deferredRunError
	return errors.As(err, &deferred)
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

// ToolCompletedEvent is published when a tool call completes successfully.
type ToolCompletedEvent struct {
	RunID       string
	ParentRunID string
	ToolCallID  string
	ToolName    string
	Result      string
	DurationMs  int64
	CompletedAt time.Time
}

func (e ToolCompletedEvent) RuntimeEventType() string     { return RuntimeEventTypeToolCompleted }
func (e ToolCompletedEvent) RuntimeRunID() string         { return e.RunID }
func (e ToolCompletedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ToolCompletedEvent) RuntimeOccurredAt() time.Time { return e.CompletedAt }

// ToolFailedEvent is published when a tool call fails.
type ToolFailedEvent struct {
	RunID       string
	ParentRunID string
	ToolCallID  string
	ToolName    string
	Error       string
	DurationMs  int64
	FailedAt    time.Time
}

func (e ToolFailedEvent) RuntimeEventType() string     { return RuntimeEventTypeToolFailed }
func (e ToolFailedEvent) RuntimeRunID() string         { return e.RunID }
func (e ToolFailedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ToolFailedEvent) RuntimeOccurredAt() time.Time { return e.FailedAt }

// TurnStartedEvent is published when an agent turn begins.
type TurnStartedEvent struct {
	RunID       string
	ParentRunID string
	TurnNumber  int
	StartedAt   time.Time
}

func (e TurnStartedEvent) RuntimeEventType() string     { return RuntimeEventTypeTurnStarted }
func (e TurnStartedEvent) RuntimeRunID() string         { return e.RunID }
func (e TurnStartedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e TurnStartedEvent) RuntimeOccurredAt() time.Time { return e.StartedAt }

// TurnCompletedEvent is published when an agent turn ends.
// If the turn ended due to an error, Error is non-empty.
type TurnCompletedEvent struct {
	RunID        string
	ParentRunID  string
	TurnNumber   int
	HasToolCalls bool
	HasText      bool
	Error        string
	CompletedAt  time.Time
}

func (e TurnCompletedEvent) RuntimeEventType() string     { return RuntimeEventTypeTurnCompleted }
func (e TurnCompletedEvent) RuntimeRunID() string         { return e.RunID }
func (e TurnCompletedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e TurnCompletedEvent) RuntimeOccurredAt() time.Time { return e.CompletedAt }

// ModelRequestStartedEvent is published before a model request is sent.
type ModelRequestStartedEvent struct {
	RunID        string
	ParentRunID  string
	TurnNumber   int
	MessageCount int
	StartedAt    time.Time
}

func (e ModelRequestStartedEvent) RuntimeEventType() string {
	return RuntimeEventTypeModelRequestStarted
}
func (e ModelRequestStartedEvent) RuntimeRunID() string         { return e.RunID }
func (e ModelRequestStartedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ModelRequestStartedEvent) RuntimeOccurredAt() time.Time { return e.StartedAt }

// ModelResponseCompletedEvent is published after a model response is received.
type ModelResponseCompletedEvent struct {
	RunID        string
	ParentRunID  string
	TurnNumber   int
	FinishReason string
	InputTokens  int
	OutputTokens int
	HasToolCalls bool
	HasText      bool
	DurationMs   int64
	CompletedAt  time.Time
}

func (e ModelResponseCompletedEvent) RuntimeEventType() string {
	return RuntimeEventTypeModelResponseCompleted
}
func (e ModelResponseCompletedEvent) RuntimeRunID() string         { return e.RunID }
func (e ModelResponseCompletedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ModelResponseCompletedEvent) RuntimeOccurredAt() time.Time { return e.CompletedAt }

// ModelDeltaEvent is published for streamed model response deltas.
type ModelDeltaEvent struct {
	RunID        string
	ParentRunID  string
	TurnNumber   int
	PartIndex    int
	DeltaKind    string
	ContentDelta string
	DeltaAt      time.Time
}

func (e ModelDeltaEvent) RuntimeEventType() string     { return RuntimeEventTypeModelDelta }
func (e ModelDeltaEvent) RuntimeRunID() string         { return e.RunID }
func (e ModelDeltaEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ModelDeltaEvent) RuntimeOccurredAt() time.Time { return e.DeltaAt }

// ApprovalRequestedEvent is published when a tool requires approval.
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

// ApprovalResolvedEvent is published when a tool approval is resolved.
type ApprovalResolvedEvent struct {
	RunID       string
	ParentRunID string
	ToolCallID  string
	ToolName    string
	Approved    bool
	ResolvedAt  time.Time
}

func (e ApprovalResolvedEvent) RuntimeEventType() string     { return RuntimeEventTypeApprovalResolved }
func (e ApprovalResolvedEvent) RuntimeRunID() string         { return e.RunID }
func (e ApprovalResolvedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ApprovalResolvedEvent) RuntimeOccurredAt() time.Time { return e.ResolvedAt }

// DeferredRequestedEvent is published when a tool call is deferred.
type DeferredRequestedEvent struct {
	RunID       string
	ParentRunID string
	ToolCallID  string
	ToolName    string
	ArgsJSON    string
	RequestedAt time.Time
}

func (e DeferredRequestedEvent) RuntimeEventType() string     { return RuntimeEventTypeDeferredRequested }
func (e DeferredRequestedEvent) RuntimeRunID() string         { return e.RunID }
func (e DeferredRequestedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e DeferredRequestedEvent) RuntimeOccurredAt() time.Time { return e.RequestedAt }

// DeferredResolvedEvent is published when a deferred tool result is provided.
type DeferredResolvedEvent struct {
	RunID       string
	ParentRunID string
	ToolCallID  string
	ToolName    string
	Content     string
	IsError     bool
	ResolvedAt  time.Time
}

func (e DeferredResolvedEvent) RuntimeEventType() string     { return RuntimeEventTypeDeferredResolved }
func (e DeferredResolvedEvent) RuntimeRunID() string         { return e.RunID }
func (e DeferredResolvedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e DeferredResolvedEvent) RuntimeOccurredAt() time.Time { return e.ResolvedAt }

// RunWaitingEvent is published when a run enters a waiting state.
type RunWaitingEvent struct {
	RunID       string
	ParentRunID string
	Reason      string // "approval", "deferred", "approval_and_deferred"
	WaitingAt   time.Time
}

func (e RunWaitingEvent) RuntimeEventType() string     { return RuntimeEventTypeRunWaiting }
func (e RunWaitingEvent) RuntimeRunID() string         { return e.RunID }
func (e RunWaitingEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e RunWaitingEvent) RuntimeOccurredAt() time.Time { return e.WaitingAt }

// RunResumedEvent is published when a run resumes from a waiting state.
type RunResumedEvent struct {
	RunID       string
	ParentRunID string
	ResumedAt   time.Time
}

func (e RunResumedEvent) RuntimeEventType() string     { return RuntimeEventTypeRunResumed }
func (e RunResumedEvent) RuntimeRunID() string         { return e.RunID }
func (e RunResumedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e RunResumedEvent) RuntimeOccurredAt() time.Time { return e.ResumedAt }

// ArtifactChangedEvent is published after a tool mutates a workspace artifact.
type ArtifactChangedEvent struct {
	RunID                string
	ParentRunID          string
	ToolCallID           string
	ToolName             string
	Path                 string
	WorkspaceRoot        string
	Operation            string
	Bytes                int64
	BeforeExists         bool
	AfterExists          bool
	BeforeIsDir          bool
	AfterIsDir           bool
	BeforeIsRegular      bool
	AfterIsRegular       bool
	BeforeIsSymlink      bool
	AfterIsSymlink       bool
	BeforeHasSymlinkPath bool
	AfterHasSymlinkPath  bool
	BeforeLinkCount      uint64
	AfterLinkCount       uint64
	BeforeMode           uint32
	AfterMode            uint32
	BeforeSize           int64
	AfterSize            int64
	BeforeSHA256         string
	AfterSHA256          string
	BeforeContentBytes   []byte
	AfterContentBytes    []byte
	Diff                 string
	DiffTruncated        bool
	DiffOmittedReason    string
	BeforeContent        string
	AfterContent         string
	ContentEncoding      string
	ContentTruncated     bool
	ContentOmittedReason string
	ChangedAt            time.Time
}

func (e ArtifactChangedEvent) RuntimeEventType() string     { return RuntimeEventTypeArtifactChanged }
func (e ArtifactChangedEvent) RuntimeRunID() string         { return e.RunID }
func (e ArtifactChangedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ArtifactChangedEvent) RuntimeOccurredAt() time.Time { return e.ChangedAt }

// RetryScheduledEvent is published when the runtime schedules a model retry.
type RetryScheduledEvent struct {
	RunID       string
	ParentRunID string
	TurnNumber  int
	ToolName    string
	ToolCallID  string
	Reason      string
	Retry       int
	MaxRetries  int
	ScheduledAt time.Time
}

func (e RetryScheduledEvent) RuntimeEventType() string     { return RuntimeEventTypeRetryScheduled }
func (e RetryScheduledEvent) RuntimeRunID() string         { return e.RunID }
func (e RetryScheduledEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e RetryScheduledEvent) RuntimeOccurredAt() time.Time { return e.ScheduledAt }

// CheckpointCreatedEvent is published when a replay/fork checkpoint is created.
type CheckpointCreatedEvent struct {
	RunID        string
	ParentRunID  string
	CheckpointID string
	SnapshotID   string
	Step         int
	CreatedAt    time.Time
}

func (e CheckpointCreatedEvent) RuntimeEventType() string     { return RuntimeEventTypeCheckpointCreated }
func (e CheckpointCreatedEvent) RuntimeRunID() string         { return e.RunID }
func (e CheckpointCreatedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e CheckpointCreatedEvent) RuntimeOccurredAt() time.Time { return e.CreatedAt }

// TopologyTransitionedEvent is published when runtime topology changes.
type TopologyTransitionedEvent struct {
	RunID          string
	ParentRunID    string
	From           string
	To             string
	Reason         string
	TransitionedAt time.Time
}

func (e TopologyTransitionedEvent) RuntimeEventType() string {
	return RuntimeEventTypeTopologyTransitioned
}
func (e TopologyTransitionedEvent) RuntimeRunID() string         { return e.RunID }
func (e TopologyTransitionedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e TopologyTransitionedEvent) RuntimeOccurredAt() time.Time { return e.TransitionedAt }

// EvaluatorCompletedEvent is published when evaluator output is available.
type EvaluatorCompletedEvent struct {
	RunID       string
	ParentRunID string
	Name        string
	Score       *float64
	Passed      *bool
	Results     map[string]any
	CompletedAt time.Time
}

func (e EvaluatorCompletedEvent) RuntimeEventType() string     { return RuntimeEventTypeEvaluatorCompleted }
func (e EvaluatorCompletedEvent) RuntimeRunID() string         { return e.RunID }
func (e EvaluatorCompletedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e EvaluatorCompletedEvent) RuntimeOccurredAt() time.Time { return e.CompletedAt }

// ErrorRaisedEvent is published when a runtime error boundary is reached.
type ErrorRaisedEvent struct {
	RunID       string
	ParentRunID string
	TurnNumber  int
	ToolName    string
	ToolCallID  string
	Error       string
	// Cause retains the original error for in-process subscribers that need to
	// classify it. It is unsafe for persistence, logging, or network transport;
	// consumers must project it to a public-safe value before exposing it.
	Cause    error
	RaisedAt time.Time
}

func (e ErrorRaisedEvent) RuntimeEventType() string     { return RuntimeEventTypeErrorRaised }
func (e ErrorRaisedEvent) RuntimeRunID() string         { return e.RunID }
func (e ErrorRaisedEvent) RuntimeParentRunID() string   { return e.ParentRunID }
func (e ErrorRaisedEvent) RuntimeOccurredAt() time.Time { return e.RaisedAt }

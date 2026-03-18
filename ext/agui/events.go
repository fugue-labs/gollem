package agui

import (
	"encoding/json"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// EventType identifies a normalized AGUI event.
type EventType string

const (
	EventSessionOpened            EventType = "session.opened"
	EventSessionInputAccepted     EventType = "session.input.accepted"
	EventRunStarted               EventType = "run.started"
	EventTurnStarted              EventType = "turn.started"
	EventTurnCompleted            EventType = "turn.completed"
	EventModelRequestStarted      EventType = "model.request.started"
	EventModelOutputTextDelta     EventType = "model.output.text.delta"
	EventModelOutputTextStarted   EventType = "model.output.text.part_started"
	EventModelOutputTextCompleted EventType = "model.output.text.part_completed"
	EventModelOutputThinkingDelta EventType = "model.output.thinking.delta"
	EventModelOutputToolCallDelta EventType = "model.output.tool_call.delta"
	EventModelResponseCompleted   EventType = "model.response.completed"
	EventModelResponseDropped     EventType = "model.response.dropped"
	EventToolCallRequested        EventType = "tool.call.requested"
	EventToolExecutionStarted     EventType = "tool.execution.started"
	EventToolExecutionCompleted   EventType = "tool.execution.completed"
	EventToolExecutionFailed      EventType = "tool.execution.failed"
	EventToolResultReturned       EventType = "tool.result.returned"
	EventToolRetryRequested       EventType = "tool.retry_requested"
	EventToolDeferred             EventType = "tool.deferred"
	EventApprovalRequested        EventType = "approval.requested"
	EventApprovalApproved         EventType = "approval.approved"
	EventApprovalDenied           EventType = "approval.denied"
	EventExternalInputRequested   EventType = "external_input.requested"
	EventExternalInputProvided    EventType = "external_input.provided"
	EventSessionWaiting           EventType = "session.waiting"
	EventSessionResumed           EventType = "session.resumed"
	EventSessionSnapshot          EventType = "session.snapshot"
	EventSessionTraceUpdated      EventType = "session.trace.updated"
	EventSessionReplayCheckpoint  EventType = "session.replay_checkpoint"
	EventSessionCompleted         EventType = "session.completed"
	EventSessionFailed            EventType = "session.failed"
	EventSessionCancelled         EventType = "session.cancelled"
	EventSessionAborted           EventType = "session.aborted"
)

// Event is the normalized AGUI event envelope with replay metadata.
type Event struct {
	Sequence    int64           `json:"seq"`
	Type        EventType       `json:"type"`
	SessionID   string          `json:"session_id"`
	RunID       string          `json:"run_id,omitempty"`
	ParentRunID string          `json:"parent_run_id,omitempty"`
	TurnNumber  int             `json:"turn_number,omitempty"`
	Timestamp   time.Time       `json:"timestamp"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

// SessionOpenedPayload describes a newly created session.
type SessionOpenedPayload struct {
	Session Session `json:"session"`
}

// SessionInputAcceptedPayload records the initial input accepted by the adapter.
type SessionInputAcceptedPayload struct {
	Input SessionInput `json:"input"`
}

// RunStartedPayload describes the run binding for a session.
type RunStartedPayload struct {
	Mode SessionMode `json:"mode,omitempty"`
}

// TurnPayload describes a model turn boundary.
type TurnPayload struct {
	TurnNumber int `json:"turn_number"`
}

// ModelRequestStartedPayload describes a pending model request.
type ModelRequestStartedPayload struct {
	Messages []core.SerializedMessage `json:"messages,omitempty"`
}

// ModelOutputPartPayload identifies a streamed response part.
type ModelOutputPartPayload struct {
	Index int    `json:"index"`
	Kind  string `json:"kind,omitempty"`
}

// TextDeltaPayload carries a text delta from a streamed model output.
type TextDeltaPayload struct {
	Index   int    `json:"index"`
	Delta   string `json:"delta"`
	Content string `json:"content,omitempty"`
}

// ThinkingDeltaPayload carries an incremental reasoning delta.
type ThinkingDeltaPayload struct {
	Index   int    `json:"index"`
	Delta   string `json:"delta"`
	Content string `json:"content,omitempty"`
}

// ToolCallDeltaPayload carries streamed tool-call argument chunks.
type ToolCallDeltaPayload struct {
	Index      int    `json:"index"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ArgsDelta  string `json:"args_delta"`
	ArgsJSON   string `json:"args_json,omitempty"`
}

// ModelResponseCompletedPayload carries a completed model response.
type ModelResponseCompletedPayload struct {
	Response core.SerializedMessage `json:"response"`
}

// ModelResponseDroppedPayload describes a dropped provider/model response.
type ModelResponseDroppedPayload struct {
	Reason string `json:"reason,omitempty"`
}

// ToolCallRequestedPayload describes a requested tool invocation.
type ToolCallRequestedPayload struct {
	ToolName   string            `json:"tool_name"`
	ToolCallID string            `json:"tool_call_id"`
	ArgsJSON   string            `json:"args_json"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// ToolExecutionPayload describes a tool execution lifecycle event.
type ToolExecutionPayload struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id"`
	Attempt    int    `json:"attempt,omitempty"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ToolResultPayload describes the tool result returned into the transcript.
type ToolResultPayload struct {
	ToolName   string          `json:"tool_name"`
	ToolCallID string          `json:"tool_call_id"`
	Content    json.RawMessage `json:"content,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
}

// ToolRetryRequestedPayload captures a retry prompt after tool validation or denial.
type ToolRetryRequestedPayload struct {
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Message    string `json:"message"`
}

// ToolDeferredPayload describes a deferred tool request.
type ToolDeferredPayload struct {
	Request DeferredRequest `json:"request"`
}

// ApprovalRequestedPayload describes a tool approval request.
type ApprovalRequestedPayload struct {
	Request ToolApprovalRequest `json:"request"`
}

// ApprovalResolvedPayload describes approval approval/denial results.
type ApprovalResolvedPayload struct {
	Decision ApprovalDecisionPayload `json:"decision"`
}

// ExternalInputRequestedPayload describes a deferred external input request.
type ExternalInputRequestedPayload struct {
	Request DeferredRequest `json:"request"`
}

// ExternalInputProvidedPayload describes a supplied deferred result.
type ExternalInputProvidedPayload struct {
	Result DeferredResult `json:"result"`
}

// SessionWaitingPayload marks a session waiting state.
type SessionWaitingPayload struct {
	Reason           WaitingReason         `json:"reason"`
	PendingApprovals []ToolApprovalRequest `json:"pending_approvals,omitempty"`
	DeferredRequests []DeferredRequest     `json:"deferred_requests,omitempty"`
}

// SessionResumedPayload describes how a session resumed.
type SessionResumedPayload struct {
	FromSnapshot bool  `json:"from_snapshot,omitempty"`
	AfterSeq     int64 `json:"after_seq,omitempty"`
}

// SessionSnapshotPayload carries a full current state snapshot for replay fallback.
type SessionSnapshotPayload struct {
	State SessionState `json:"state"`
}

// SessionTraceUpdatedPayload carries a trace update.
type SessionTraceUpdatedPayload struct {
	Trace *core.RunTrace `json:"trace,omitempty"`
}

// SessionReplayCheckpointPayload advertises replay progress metadata.
type SessionReplayCheckpointPayload struct {
	Sequence int64 `json:"seq"`
}

// SessionTerminalPayload carries final session details.
type SessionTerminalPayload struct {
	State SessionState `json:"state"`
	Error string       `json:"error,omitempty"`
}

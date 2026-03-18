package agui

import (
	"encoding/json"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// SessionMode identifies the runtime backing an AGUI session.
type SessionMode string

const (
	SessionModeCoreRun    SessionMode = "core-run"
	SessionModeCoreStream SessionMode = "core-stream"
	SessionModeCoreIter   SessionMode = "core-iter"
	SessionModeTemporal   SessionMode = "temporal"
	SessionModeGraph      SessionMode = "graph"
	SessionModeTeam       SessionMode = "team"
)

// SessionStatus is the normalized lifecycle status exposed to AGUI clients.
type SessionStatus string

const (
	SessionStatusStarting  SessionStatus = "starting"
	SessionStatusRunning   SessionStatus = "running"
	SessionStatusWaiting   SessionStatus = "waiting"
	SessionStatusCompleted SessionStatus = "completed"
	SessionStatusFailed    SessionStatus = "failed"
	SessionStatusCancelled SessionStatus = "cancelled"
	SessionStatusAborted   SessionStatus = "aborted"
)

// WaitingReason describes why a session is blocked.
type WaitingReason string

const (
	WaitingReasonApproval            WaitingReason = "approval"
	WaitingReasonDeferred            WaitingReason = "deferred"
	WaitingReasonApprovalAndDeferred WaitingReason = "approval_and_deferred"
)

// Session holds the stable AGUI-facing session identity and current status.
type Session struct {
	SessionID   string         `json:"session_id"`
	RunID       string         `json:"run_id,omitempty"`
	ParentRunID string         `json:"parent_run_id,omitempty"`
	Mode        SessionMode    `json:"mode,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Status      SessionStatus  `json:"status"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// SessionInput captures the initial client-provided input accepted by a session.
type SessionInput struct {
	Prompt   string                   `json:"prompt,omitempty"`
	Messages []core.SerializedMessage `json:"messages,omitempty"`
	Metadata map[string]any           `json:"metadata,omitempty"`
}

// OpenSessionRequest starts a new AGUI session.
type OpenSessionRequest struct {
	SessionID string       `json:"session_id,omitempty"`
	Mode      SessionMode  `json:"mode,omitempty"`
	Input     SessionInput `json:"input,omitempty"`
}

// OpenSessionResponse returns the created session and any immediately available state.
type OpenSessionResponse struct {
	Session Session       `json:"session"`
	State   *SessionState `json:"state,omitempty"`
}

// ReconnectPayload requests replay from the event immediately after LastSequence.
type ReconnectPayload struct {
	LastSequence int64 `json:"last_seq,omitempty"`
}

// ReconnectStreamRequest reconnects a live stream using the AGUI session cursor.
type ReconnectStreamRequest struct {
	SessionID string           `json:"session_id"`
	Replay    ReconnectPayload `json:"replay,omitempty"`
}

// ReconnectStreamResponse returns current state and whether replay was available.
type ReconnectStreamResponse struct {
	Session         Session       `json:"session"`
	State           *SessionState `json:"state,omitempty"`
	ReplayAvailable bool          `json:"replay_available"`
	NextSequence    int64         `json:"next_seq,omitempty"`
}

// ToolApprovalRequest describes a tool call waiting on human approval.
type ToolApprovalRequest struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id"`
	ArgsJSON   string `json:"args_json"`
	Summary    string `json:"summary,omitempty"`
}

// ApprovalDecisionPayload resolves a pending approval request.
type ApprovalDecisionPayload struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id"`
	Approved   bool   `json:"approved"`
	Message    string `json:"message,omitempty"`
}

// DeferredRequest aliases the core deferred tool request payload used in snapshots.
type DeferredRequest = core.DeferredToolRequest

// DeferredResult aliases the core deferred tool result payload used for resume.
type DeferredResult = core.DeferredToolResult

// ResumePayload resumes a paused session from a snapshot plus optional deferred results.
type ResumePayload struct {
	Snapshot        *core.SerializedRunSnapshot `json:"snapshot,omitempty"`
	DeferredResults []DeferredResult            `json:"deferred_results,omitempty"`
}

// ResumeSessionRequest resumes a prior AGUI session.
type ResumeSessionRequest struct {
	SessionID string        `json:"session_id"`
	Resume    ResumePayload `json:"resume,omitempty"`
}

// ResumeSessionResponse returns the session and state after accepting a resume request.
type ResumeSessionResponse struct {
	Session Session       `json:"session"`
	State   *SessionState `json:"state,omitempty"`
}

// AbortPayload requests that a waiting or running session stop.
type AbortPayload struct {
	Reason string `json:"reason,omitempty"`
}

// ClientCommandType identifies an AGUI control-plane action.
type ClientCommandType string

const (
	CommandApproveToolCall ClientCommandType = "approve_tool_call"
	CommandDenyToolCall    ClientCommandType = "deny_tool_call"
	CommandSubmitDeferred  ClientCommandType = "submit_deferred_result"
	CommandAbortSession    ClientCommandType = "abort_session"
	CommandResumeSession   ClientCommandType = "resume_session"
	CommandReconnectStream ClientCommandType = "reconnect_stream"
)

// ClientCommand is the normalized control-plane envelope for AGUI actions.
type ClientCommand struct {
	Type      ClientCommandType `json:"type"`
	SessionID string            `json:"session_id"`
	Payload   json.RawMessage   `json:"payload,omitempty"`
}

// CommandResponse reports whether a control-plane command was accepted.
type CommandResponse struct {
	Type      ClientCommandType `json:"type"`
	SessionID string            `json:"session_id"`
	Accepted  bool              `json:"accepted"`
	State     *SessionState     `json:"state,omitempty"`
	Message   string            `json:"message,omitempty"`
}

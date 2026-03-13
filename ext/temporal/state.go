package temporal

import (
	"encoding/json"
	"errors"

	"github.com/fugue-labs/gollem/core"
)

const (
	workflowStatusQueryName    = "gollem.status"
	workflowApprovalSignalName = "gollem.tool_approval"
	workflowDeferredSignalName = "gollem.deferred_result"
	workflowAbortSignalName    = "gollem.abort"
)

// ToolApprovalRequest describes a tool call that is waiting on human approval.
type ToolApprovalRequest struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id"`
	ArgsJSON   string `json:"args_json"`
}

// ApprovalSignal resolves a pending tool approval request.
type ApprovalSignal struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id"`
	Approved   bool   `json:"approved"`
	Message    string `json:"message,omitempty"`
}

// DeferredResultSignal supplies the result for a previously deferred tool call.
type DeferredResultSignal struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

// DeferredToolResult converts the signal payload into the core resume type.
func (s DeferredResultSignal) DeferredToolResult() core.DeferredToolResult {
	return core.DeferredToolResult{
		ToolName:   s.ToolName,
		ToolCallID: s.ToolCallID,
		Content:    s.Content,
		IsError:    s.IsError,
	}
}

// AbortSignal requests that the workflow stop waiting and fail.
type AbortSignal struct {
	Reason string `json:"reason,omitempty"`
}

// WorkflowStatus is the queryable state of a Temporal agent run.
type WorkflowStatus struct {
	RunID                   string                      `json:"run_id"`
	RunStep                 int                         `json:"run_step"`
	Usage                   core.RunUsage               `json:"usage"`
	WorkflowName            string                      `json:"workflow_name,omitempty"`
	RegistrationName        string                      `json:"registration_name,omitempty"`
	Version                 string                      `json:"version,omitempty"`
	Messages                []core.SerializedMessage    `json:"messages,omitempty"`
	MessagesJSON            json.RawMessage             `json:"messages_json,omitempty"` // Deprecated: prefer Messages.
	Snapshot                *core.SerializedRunSnapshot `json:"snapshot,omitempty"`
	SnapshotJSON            json.RawMessage             `json:"snapshot_json,omitempty"` // Deprecated: prefer Snapshot.
	Trace                   *core.RunTrace              `json:"trace,omitempty"`
	TraceJSON               json.RawMessage             `json:"trace_json,omitempty"` // Deprecated: prefer Trace.
	Cost                    *core.RunCost               `json:"cost,omitempty"`
	PendingApprovals        []ToolApprovalRequest       `json:"pending_approvals,omitempty"`
	DeferredRequests        []core.DeferredToolRequest  `json:"deferred_requests,omitempty"`
	Waiting                 bool                        `json:"waiting"`
	WaitingReason           string                      `json:"waiting_reason,omitempty"`
	Completed               bool                        `json:"completed"`
	Aborted                 bool                        `json:"aborted,omitempty"`
	ContinueAsNewCount      int                         `json:"continue_as_new_count,omitempty"`
	CurrentHistoryLength    int                         `json:"current_history_length,omitempty"`
	CurrentHistorySize      int                         `json:"current_history_size,omitempty"`
	ContinueAsNewSuggested  bool                        `json:"continue_as_new_suggested,omitempty"`
	LastContinueAsNewReason string                      `json:"last_continue_as_new_reason,omitempty"`
	LastError               string                      `json:"last_error,omitempty"`
}

// DecodeWorkflowStatusMessages decodes the queried message history.
func DecodeWorkflowStatusMessages(status *WorkflowStatus) ([]core.ModelMessage, error) {
	if status == nil {
		return nil, errors.New("nil workflow status")
	}
	if len(status.Messages) > 0 {
		return core.DecodeMessages(status.Messages)
	}
	if len(status.MessagesJSON) == 0 {
		return nil, nil
	}
	return core.UnmarshalMessages(status.MessagesJSON)
}

// DecodeWorkflowStatusTrace decodes the queried workflow trace payload.
func DecodeWorkflowStatusTrace(status *WorkflowStatus) (*core.RunTrace, error) {
	if status == nil {
		return nil, errors.New("nil workflow status")
	}
	return decodeTrace(status.Trace, status.TraceJSON)
}

// StatusQueryName returns the workflow query name for status lookups.
func (ta *TemporalAgent[T]) StatusQueryName() string {
	return workflowStatusQueryName
}

// ApprovalSignalName returns the workflow signal name for tool approvals.
func (ta *TemporalAgent[T]) ApprovalSignalName() string {
	return workflowApprovalSignalName
}

// DeferredResultSignalName returns the workflow signal name for deferred tool results.
func (ta *TemporalAgent[T]) DeferredResultSignalName() string {
	return workflowDeferredSignalName
}

// AbortSignalName returns the workflow signal name for aborting a wait state.
func (ta *TemporalAgent[T]) AbortSignalName() string {
	return workflowAbortSignalName
}

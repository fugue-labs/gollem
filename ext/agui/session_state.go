package agui

import (
	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/temporal"
)

// SessionState is a JSON-safe snapshot of the current AGUI session state.
type SessionState struct {
	Session          Session                     `json:"session"`
	CurrentSequence  int64                       `json:"current_seq,omitempty"`
	WaitingReason    WaitingReason               `json:"waiting_reason,omitempty"`
	Messages         []core.SerializedMessage    `json:"messages,omitempty"`
	Snapshot         *core.SerializedRunSnapshot `json:"snapshot,omitempty"`
	Trace            *core.RunTrace              `json:"trace,omitempty"`
	Usage            core.RunUsage               `json:"usage"`
	Cost             *core.RunCost               `json:"cost,omitempty"`
	PendingApprovals []ToolApprovalRequest       `json:"pending_approvals,omitempty"`
	DeferredRequests []DeferredRequest           `json:"deferred_requests,omitempty"`
	DeferredResults  []DeferredResult            `json:"deferred_results,omitempty"`
	LastError        string                      `json:"last_error,omitempty"`
	Metadata         map[string]any              `json:"metadata,omitempty"`
}

// FinalStateSnapshot is the terminal payload persisted or returned at session end.
type FinalStateSnapshot = SessionState

// StateFromWorkflowStatus projects a Temporal workflow status into the normalized AGUI state model.
func StateFromWorkflowStatus(session Session, currentSeq int64, status *temporal.WorkflowStatus) *SessionState {
	if status == nil {
		return &SessionState{Session: session, CurrentSequence: currentSeq}
	}
	state := &SessionState{
		Session:          session,
		CurrentSequence:  currentSeq,
		Messages:         status.Messages,
		Snapshot:         status.Snapshot,
		Trace:            status.Trace,
		Usage:            status.Usage,
		Cost:             status.Cost,
		DeferredRequests: append([]DeferredRequest(nil), status.DeferredRequests...),
		LastError:        status.LastError,
	}
	if status.Waiting {
		state.WaitingReason = WaitingReason(status.WaitingReason)
	}
	if len(status.PendingApprovals) > 0 {
		state.PendingApprovals = make([]ToolApprovalRequest, 0, len(status.PendingApprovals))
		for _, approval := range status.PendingApprovals {
			state.PendingApprovals = append(state.PendingApprovals, ToolApprovalRequest{
				ToolName:   approval.ToolName,
				ToolCallID: approval.ToolCallID,
				ArgsJSON:   approval.ArgsJSON,
			})
		}
	}
	return state
}

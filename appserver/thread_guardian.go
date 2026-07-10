package appserver

import (
	"context"
	"encoding/json"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
)

const guardianDeniedActionUnavailableReason = "Guardian denied-action replay is not implemented because Gollem does not yet model guardian assessment events; use approval/respond for Gollem approval requests"

type threadApproveGuardianDeniedActionParams struct {
	ID       string          `json:"id,omitempty"`
	ThreadID string          `json:"threadId,omitempty"`
	Event    json.RawMessage `json:"event"`
}

func (p threadApproveGuardianDeniedActionParams) threadID() string {
	return firstNonEmpty(p.ThreadID, p.ID)
}

func (s *Server) handleThreadApproveGuardianDeniedAction(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	const method = "thread/approveGuardianDeniedAction"
	st, rpcErr := s.requireStore(method)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadApproveGuardianDeniedActionParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := params.threadID()
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	if len(params.Event) == 0 || string(params.Event) == "null" {
		return nil, invalidParams("event is required", nil)
	}
	thread, err := st.GetThread(ctx, threadID)
	if err != nil {
		return nil, mapError(method, err)
	}
	if thread.Status == store.ThreadDeleted {
		return nil, mapError(method, store.ErrThreadDeleted)
	}
	return nil, protocol.MethodUnavailableErrorWithReason(method, guardianDeniedActionUnavailableReason)
}

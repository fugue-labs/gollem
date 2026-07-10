package appserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
)

func (s *Server) handleThreadUnsubscribe(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/unsubscribe")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadIDParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := params.threadID()
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	thread, err := st.GetThread(ctx, threadID)
	if err != nil {
		if errors.Is(err, store.ErrThreadNotFound) {
			return threadUnsubscribeResponse{Status: "notLoaded"}, nil
		}
		return nil, mapError("thread/unsubscribe", err)
	}
	if thread.Status == store.ThreadDeleted {
		s.markThreadUnloaded(thread.ID)
		return threadUnsubscribeResponse{Status: "notLoaded"}, nil
	}
	return threadUnsubscribeResponse{Status: s.unsubscribeThread(thread.ID)}, nil
}

package appserver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
)

const threadRollbackDeprecationSummary = "thread/rollback is deprecated and will be removed soon"

type threadRollbackParams struct {
	ID       string `json:"id,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
	NumTurns int    `json:"numTurns"`
}

func (p threadRollbackParams) threadID() string {
	return firstNonEmpty(p.ThreadID, p.ID)
}

type threadRollbackResponse struct {
	Thread threadRollbackThread `json:"thread"`
}

type threadRollbackThread struct {
	*store.Thread
	Name  *string       `json:"name"`
	Turns []*store.Turn `json:"turns"`
}

func (s *Server) handleThreadRollback(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/rollback")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadRollbackParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := params.threadID()
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	if params.NumTurns < 1 {
		return nil, invalidParams("numTurns must be >= 1", nil)
	}
	s.publishThreadRollbackDeprecationNotice()
	result, err := st.RollbackThread(ctx, store.RollbackThreadRequest{
		ID:       threadID,
		NumTurns: params.NumTurns,
	})
	if err != nil {
		return nil, mapError("thread/rollback", err)
	}
	s.markThreadLoaded(result.Thread)
	return threadRollbackResponse{Thread: rollbackThreadWithTurns(result.Thread, result.Turns)}, nil
}

func (s *Server) publishThreadRollbackDeprecationNotice() {
	if s == nil {
		return
	}
	s.mu.Lock()
	clientName := s.clientInfo.Name
	s.mu.Unlock()
	if strings.EqualFold(clientName, "codex-tui") {
		return
	}
	s.PublishNotification("deprecationNotice", deprecationNoticeNotificationParams{
		Summary: threadRollbackDeprecationSummary,
		Details: nil,
	})
}

func rollbackThreadWithTurns(thread *store.Thread, turns []*store.Turn) threadRollbackThread {
	var name *string
	if thread != nil && thread.Title != "" {
		title := thread.Title
		name = &title
	}
	return threadRollbackThread{
		Thread: thread,
		Name:   name,
		Turns:  turns,
	}
}

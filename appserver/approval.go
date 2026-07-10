package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	toolfs "github.com/fugue-labs/gollem/appserver/tools/fs"
	toolgit "github.com/fugue-labs/gollem/appserver/tools/git"
	toolprocess "github.com/fugue-labs/gollem/appserver/tools/process"
	"github.com/fugue-labs/gollem/core"
)

var ErrApprovalRequestDenied = errors.New("appserver: approval request denied")

const (
	approvalThreadID = "appserver"
	approvalTurnID   = "appserver"
)

// ApprovalService bridges synchronous tool approval hooks to app-server
// server-to-client approval requests resolved by approval/respond.
type ApprovalService struct {
	mu       sync.Mutex
	counter  int64
	pending  map[string]pendingApproval
	requests *RequestQueue
}

type pendingApproval struct {
	ch       chan approvalResolution
	threadID string
}

type approvalResolution struct {
	Approved bool
	Message  string
}

type approvalRequestBase struct {
	protocol.ApprovalRequestBase
	requestID string
}

type approvalRespondParams = protocol.ApprovalRespondParams
type fileChangeApprovalParams = protocol.FileChangeApprovalRequestParams
type commandApprovalParams = protocol.CommandExecutionApprovalRequestParams
type permissionsApprovalParams = protocol.PermissionsApprovalRequestParams

type approvalRespondResult struct {
	protocol.ApprovalRespondResult
	threadID string
}

func NewApprovalService() *ApprovalService {
	return &ApprovalService{
		pending:  make(map[string]pendingApproval),
		requests: NewRequestQueue(),
	}
}

func (s *ApprovalService) setRequestQueue(q *RequestQueue) {
	if s == nil || q == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = q
}

func (s *ApprovalService) RequestSignal() <-chan struct{} {
	if s == nil || s.requests == nil {
		return nil
	}
	return s.requests.Signal()
}

func (s *ApprovalService) DrainRequests() []protocol.Request {
	if s == nil || s.requests == nil {
		return nil
	}
	return s.requests.Drain()
}

func (s *ApprovalService) FilesystemApproval(ctx context.Context, op toolfs.Operation) error {
	reason := fmt.Sprintf("Approve filesystem %s for %s", op.Kind, op.Path)
	base := s.base(ctx, reason)
	params := protocol.FileChangeApprovalRequestParams{
		ApprovalRequestBase: base.ApprovalRequestBase,
		GrantRoot:           nil,
		Operation:           string(op.Kind),
		Path:                op.Path,
		Destination:         op.Destination,
		Destructive:         op.Destructive,
	}
	return s.requestApproval(ctx, "item/fileChange/requestApproval", base.requestID, params)
}

func (s *ApprovalService) ProcessApproval(ctx context.Context, op toolprocess.Operation) error {
	command := strings.TrimSpace(strings.Join(append([]string{op.Command}, op.Args...), " "))
	reason := fmt.Sprintf("Approve process %s", op.Kind)
	base := s.base(ctx, reason)
	params := protocol.CommandExecutionApprovalRequestParams{
		ApprovalRequestBase: base.ApprovalRequestBase,
		Command:             command,
		Args:                append([]string(nil), op.Args...),
		CWD:                 op.WorkDir,
		Operation:           string(op.Kind),
		Signal:              op.Signal,
		Destructive:         op.Destructive,
	}
	return s.requestApproval(ctx, "item/commandExecution/requestApproval", base.requestID, params)
}

func (s *ApprovalService) GitApproval(ctx context.Context, op toolgit.Operation) error {
	reason := fmt.Sprintf("Approve git %s", op.Kind)
	cwd := op.Path
	if cwd == "" {
		cwd = "."
	}
	requestBase := s.base(ctx, reason)
	params := protocol.PermissionsApprovalRequestParams{
		ApprovalRequestBase: requestBase.ApprovalRequestBase,
		CWD:                 cwd,
		Operation:           string(op.Kind),
		Path:                op.Path,
		Branch:              op.Branch,
		Base:                op.Base,
		Message:             op.Message,
		Pathspecs:           append([]string(nil), op.Pathspecs...),
		Permissions: map[string]any{
			"kind":      "git",
			"operation": string(op.Kind),
			"mutating":  op.Mutating,
		},
	}
	return s.requestApproval(ctx, "item/permissions/requestApproval", requestBase.requestID, params)
}

func (s *ApprovalService) MCPToolApproval(ctx context.Context, serverName, toolName string, args map[string]any) error {
	reason := fmt.Sprintf("Approve MCP tool %s on %s", toolName, serverName)
	argKeys := make([]string, 0, len(args))
	for key := range args {
		argKeys = append(argKeys, key)
	}
	slices.Sort(argKeys)
	base := s.base(ctx, reason)
	params := protocol.PermissionsApprovalRequestParams{
		ApprovalRequestBase: base.ApprovalRequestBase,
		CWD:                 ".",
		Operation:           "mcpToolCall",
		Permissions: map[string]any{
			"kind":         "mcp",
			"server":       serverName,
			"tool":         toolName,
			"argumentKeys": argKeys,
			"mutating":     true,
		},
	}
	return s.requestApproval(ctx, "item/permissions/requestApproval", base.requestID, params)
}

func (s *Server) handleApprovalRespond(raw json.RawMessage) (any, *protocol.Error) {
	if s == nil || s.approvals == nil {
		return nil, protocol.MethodUnavailableErrorWithReason("approval/respond", "approval service is not configured")
	}
	var params approvalRespondParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	result, err := s.approvals.Respond(params)
	if err != nil {
		return nil, invalidParams("invalid approval response", err)
	}
	s.PublishNotification("serverRequest/resolved", serverRequestResolvedParams{
		ThreadID:  firstNonEmpty(result.threadID, approvalThreadID),
		RequestID: result.RequestID,
	})
	return result, nil
}

func (s *ApprovalService) Respond(params approvalRespondParams) (approvalRespondResult, error) {
	if s == nil {
		return approvalRespondResult{}, errors.New("approval service is not configured")
	}
	requestID := strings.TrimSpace(firstNonEmpty(params.RequestID, params.ID))
	if requestID == "" {
		return approvalRespondResult{}, errors.New("requestId is required")
	}
	s.mu.Lock()
	pending, ok := s.pending[requestID]
	if ok {
		delete(s.pending, requestID)
	}
	s.mu.Unlock()
	if !ok {
		return approvalRespondResult{}, fmt.Errorf("approval request %q is not pending", requestID)
	}
	pending.ch <- approvalResolution{Approved: params.Approved, Message: params.Message}
	return approvalRespondResult{
		ApprovalRespondResult: protocol.ApprovalRespondResult{OK: true, RequestID: requestID, Approved: params.Approved},
		threadID:              pending.threadID,
	}, nil
}

type serverRequestResolvedParams = protocol.ServerRequestResolvedNotificationParams

func (s *ApprovalService) base(ctx context.Context, reason string) approvalRequestBase {
	requestID := s.nextRequestID()
	runtimeContext := runtimeTurnContextFrom(ctx)
	threadID := firstNonEmpty(runtimeContext.ThreadID, approvalThreadID)
	turnID := firstNonEmpty(runtimeContext.TurnID, approvalTurnID)
	itemID := firstNonEmpty(runtimeApprovalItemIDFrom(ctx), core.ToolCallIDFromContext(ctx), requestID)
	return approvalRequestBase{
		requestID: requestID,
		ApprovalRequestBase: protocol.ApprovalRequestBase{
			ThreadID:    threadID,
			TurnID:      turnID,
			ItemID:      itemID,
			StartedAtMS: time.Now().UnixMilli(),
			Reason:      reason,
		},
	}
}

func (s *ApprovalService) nextRequestID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	return fmt.Sprintf("approval-%d", s.counter)
}

func (s *ApprovalService) requestApproval(ctx context.Context, method, requestID string, params any) error {
	if s == nil || s.requests == nil {
		return errors.New("approval service is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runtimeContext := runtimeTurnContextFrom(ctx)
	pending := pendingApproval{
		ch:       make(chan approvalResolution, 1),
		threadID: firstNonEmpty(runtimeContext.ThreadID, approvalThreadID),
	}
	s.mu.Lock()
	if s.pending == nil {
		s.pending = make(map[string]pendingApproval)
	}
	s.pending[requestID] = pending
	s.mu.Unlock()

	s.requests.Publish(method, protocol.NewStringID(requestID), params)
	select {
	case resolution := <-pending.ch:
		if resolution.Approved {
			return nil
		}
		if strings.TrimSpace(resolution.Message) != "" {
			return fmt.Errorf("%w: %s", ErrApprovalRequestDenied, strings.TrimSpace(resolution.Message))
		}
		return ErrApprovalRequestDenied
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, requestID)
		s.mu.Unlock()
		return ctx.Err()
	}
}

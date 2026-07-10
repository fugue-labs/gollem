package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	toolgit "github.com/fugue-labs/gollem/appserver/tools/git"
	toolprocess "github.com/fugue-labs/gollem/appserver/tools/process"
)

const (
	threadShellCommandItemKind = "commandExecution"

	commandExecutionStatusInProgress = "inProgress"
	commandExecutionStatusCompleted  = "completed"
	commandExecutionStatusFailed     = "failed"
	commandExecutionStatusDeclined   = "declined"
	commandExecutionSourceAgent      = "agent"
	commandExecutionSourceUserShell  = "userShell"
)

type threadShellCommandParams struct {
	ID       string `json:"id,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
	Command  string `json:"command"`
}

func (p threadShellCommandParams) threadID() string {
	return firstNonEmpty(p.ThreadID, p.ID)
}

type threadShellCommandResponse struct{}

type threadShellCommandRun struct {
	ThreadID   string
	TurnID     string
	ItemID     string
	Command    string
	CWD        string
	ProcessID  string
	BeforeDiff string
	StartedAt  time.Time
}

type threadShellCommandPayload = protocol.CommandExecutionItem
type threadShellCommandAction = protocol.CommandExecutionAction
type commandExecutionOutputDeltaNotificationParams = protocol.CommandExecutionOutputDeltaNotificationParams
type turnDiffUpdatedNotificationParams = protocol.TurnDiffUpdatedNotificationParams

func (s *Server) handleThreadShellCommand(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/shellCommand")
	if rpcErr != nil {
		return nil, rpcErr
	}
	processSvc, rpcErr := s.requireProcess("thread/shellCommand")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadShellCommandParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := params.threadID()
	command := strings.TrimSpace(params.Command)
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	if command == "" {
		return nil, invalidParams("command must not be empty", nil)
	}
	thread, err := st.GetThread(ctx, threadID)
	if err != nil {
		return nil, mapError("thread/shellCommand", err)
	}
	if thread.Status == store.ThreadDeleted {
		return nil, mapError("thread/shellCommand", store.ErrThreadDeleted)
	}
	cwd := thread.Workspace
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	startedAt := time.Now().UTC()
	turn, err := st.CreateTurn(ctx, store.CreateTurnRequest{
		ThreadID: thread.ID,
		Input: mustRuntimeJSON(map[string]any{
			"type":      threadShellCommandItemKind,
			"command":   command,
			"cwd":       cwd,
			"createdAt": startedAt,
		}),
		Metadata: map[string]any{"kind": threadShellCommandItemKind},
	})
	if err != nil {
		return nil, mapError("thread/shellCommand", err)
	}
	startedTurn, err := st.StartTurn(ctx, turn.ID)
	if err != nil {
		return nil, mapError("thread/shellCommand", err)
	}
	item, err := st.AppendItem(ctx, store.AppendItemRequest{
		ThreadID: thread.ID,
		TurnID:   startedTurn.ID,
		Kind:     threadShellCommandItemKind,
		Status:   commandExecutionStatusInProgress,
		Payload: mustRuntimeJSON(newThreadShellCommandPayload(
			command,
			cwd,
			"",
			commandExecutionStatusInProgress,
			"",
			nil,
			startedAt,
			nil,
		)),
	})
	if err != nil {
		return nil, mapError("thread/shellCommand", err)
	}
	s.markThreadLoaded(thread)
	publishTurnStarted(s, startedTurn)
	s.PublishNotification("item/started", runtimeItemNotificationParams{
		ThreadID: thread.ID,
		TurnID:   startedTurn.ID,
		ItemID:   item.ID,
		Item:     protocolTimelineItem(item),
		At:       startedAt,
	})

	beforeDiff := s.currentTurnGitDiff(ctx)
	snapshot, err := processSvc.Start(ctx, toolprocess.StartRequest{
		Command: command,
		Shell:   true,
		WorkDir: cwd,
	})
	if err != nil {
		status := commandExecutionStatusFailed
		if errors.Is(err, toolprocess.ErrApprovalDenied) {
			status = commandExecutionStatusDeclined
		}
		s.completeThreadShellCommand(context.Background(), threadShellCommandRun{
			ThreadID:   thread.ID,
			TurnID:     startedTurn.ID,
			ItemID:     item.ID,
			Command:    command,
			CWD:        cwd,
			BeforeDiff: beforeDiff,
			StartedAt:  startedAt,
		}, nil, status, err.Error())
		return nil, mapError("thread/shellCommand", err)
	}
	run := threadShellCommandRun{
		ThreadID:   thread.ID,
		TurnID:     startedTurn.ID,
		ItemID:     item.ID,
		Command:    command,
		CWD:        cwd,
		ProcessID:  snapshot.ID,
		BeforeDiff: beforeDiff,
		StartedAt:  startedAt,
	}
	s.registerThreadShellCommandProcess(snapshot.ID, run)
	if _, err := st.UpdateItem(ctx, store.UpdateItemRequest{
		ID:     item.ID,
		Status: commandExecutionStatusInProgress,
		Payload: mustRuntimeJSON(newThreadShellCommandPayload(
			command,
			cwd,
			snapshot.ID,
			commandExecutionStatusInProgress,
			"",
			nil,
			startedAt,
			nil,
		)),
	}); err != nil {
		return nil, mapError("thread/shellCommand", err)
	}
	go s.waitForThreadShellCommand(run)
	return threadShellCommandResponse{}, nil
}

func (s *Server) waitForThreadShellCommand(run threadShellCommandRun) {
	if s == nil || s.process == nil {
		return
	}
	snapshot, err := s.process.Wait(context.Background(), run.ProcessID)
	if err != nil {
		s.completeThreadShellCommand(context.Background(), run, nil, commandExecutionStatusFailed, err.Error())
		return
	}
	s.completeThreadShellCommand(context.Background(), run, snapshot, commandExecutionStatusFromProcess(snapshot.Status), "")
}

func (s *Server) completeThreadShellCommand(ctx context.Context, run threadShellCommandRun, snapshot *toolprocess.Snapshot, status string, errorText string) {
	if s == nil || s.store == nil || run.ItemID == "" {
		return
	}
	defer s.unregisterThreadShellCommandProcess(run.ProcessID)
	completedAt := time.Now().UTC()
	var processID string
	var output string
	var exitCode *int
	if snapshot != nil {
		processID = snapshot.ID
		output = strings.ToValidUTF8(string(snapshot.Stdout)+string(snapshot.Stderr), "\uFFFD")
		code := snapshot.ExitCode
		exitCode = &code
		if !snapshot.EndedAt.IsZero() {
			completedAt = snapshot.EndedAt
		}
		if errorText == "" {
			errorText = snapshot.Error
		}
	}
	if processID == "" {
		processID = run.ProcessID
	}
	var outputPtr *string
	if output != "" {
		outputPtr = &output
	}
	duration := completedAt.Sub(run.StartedAt).Milliseconds()
	item, err := s.store.UpdateItem(ctx, store.UpdateItemRequest{
		ID:     run.ItemID,
		Status: status,
		Payload: mustRuntimeJSON(newThreadShellCommandPayload(
			run.Command,
			run.CWD,
			processID,
			status,
			output,
			exitCode,
			run.StartedAt,
			&completedAt,
		)),
	})
	if err != nil {
		return
	}
	turnStatus := store.TurnCompleted
	if status != commandExecutionStatusCompleted {
		turnStatus = store.TurnFailed
	}
	completedTurn, err := s.store.CompleteTurn(ctx, store.CompleteTurnRequest{
		ID:     run.TurnID,
		Status: turnStatus,
		Error:  errorText,
		Result: mustRuntimeJSON(map[string]any{
			"type":             threadShellCommandItemKind,
			"itemId":           run.ItemID,
			"processId":        processID,
			"status":           status,
			"aggregatedOutput": outputPtr,
			"exitCode":         exitCode,
			"durationMs":       duration,
		}),
	})
	if err != nil {
		return
	}
	s.publishTurnDiffUpdatedIfChanged(ctx, completedTurn, run.BeforeDiff)
	publishItemCompleted(s, completedTurn, item)
	publishTurnCompleted(s, completedTurn)
}

func (s *Server) currentTurnGitDiff(ctx context.Context) string {
	if s == nil || s.git == nil {
		return ""
	}
	diff, err := s.git.Diff(ctx, toolgit.DiffRequest{})
	if err != nil || diff == nil {
		return ""
	}
	return diff.Patch
}

func (s *Server) publishTurnDiffUpdatedIfChanged(ctx context.Context, turn *store.Turn, before string) {
	if s == nil || turn == nil {
		return
	}
	after := s.currentTurnGitDiff(ctx)
	if after == "" || after == before {
		return
	}
	s.PublishNotification("turn/diff/updated", turnDiffUpdatedNotificationParams{
		ThreadID: turn.ThreadID,
		TurnID:   turn.ID,
		Diff:     after,
	})
}

func newThreadShellCommandPayload(command, cwd, processID, status, output string, exitCode *int, startedAt time.Time, completedAt *time.Time) threadShellCommandPayload {
	return newCommandExecutionPayload(command, cwd, processID, commandExecutionSourceUserShell, status, output, exitCode, startedAt, completedAt)
}

func newCommandExecutionPayload(command, cwd, processID, source, status, output string, exitCode *int, startedAt time.Time, completedAt *time.Time) threadShellCommandPayload {
	var processIDPtr *string
	if processID != "" {
		processIDPtr = &processID
	}
	var outputPtr *string
	if output != "" {
		outputPtr = &output
	}
	var durationPtr *int64
	if completedAt != nil {
		duration := completedAt.Sub(startedAt).Milliseconds()
		durationPtr = &duration
	}
	return threadShellCommandPayload{
		Type:      threadShellCommandItemKind,
		Command:   command,
		CWD:       cwd,
		ProcessID: processIDPtr,
		Source:    source,
		Status:    status,
		CommandActions: []threadShellCommandAction{{
			Type:    "unknown",
			Command: command,
		}},
		AggregatedOutput: outputPtr,
		ExitCode:         exitCode,
		DurationMS:       durationPtr,
		StartedAt:        startedAt,
		CompletedAt:      completedAt,
	}
}

func commandExecutionStatusFromProcess(status toolprocess.Status) string {
	switch status {
	case toolprocess.StatusCompleted:
		return commandExecutionStatusCompleted
	default:
		return commandExecutionStatusFailed
	}
}

func (s *Server) registerThreadShellCommandProcess(processID string, run threadShellCommandRun) {
	if s == nil || processID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commands == nil {
		s.commands = make(map[string]threadShellCommandRun)
	}
	s.commands[processID] = run
}

func (s *Server) lookupThreadShellCommandProcess(processID string) (threadShellCommandRun, bool) {
	if s == nil || processID == "" {
		return threadShellCommandRun{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.commands[processID]
	return run, ok
}

func (s *Server) unregisterThreadShellCommandProcess(processID string) {
	if s == nil || processID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.commands, processID)
}

// PublishProcessOutput publishes the generic process output notification and,
// for thread/shellCommand-owned processes, the Codex command-execution delta.
func (s *Server) PublishProcessOutput(event toolprocess.OutputEvent) {
	if s == nil {
		return
	}
	method, params := ProcessOutputNotification(event)
	s.PublishNotification(method, params)
	if s.isCommandExecProcess(event.ID) {
		method, params := commandExecOutputDeltaNotification(event)
		s.PublishNotification(method, params)
	}
	run, ok := s.lookupThreadShellCommandProcess(event.ID)
	if !ok {
		return
	}
	delta := strings.ToValidUTF8(string(event.Data), "\uFFFD")
	if delta == "" {
		return
	}
	s.PublishNotification("item/commandExecution/outputDelta", commandExecutionOutputDeltaNotificationParams{
		ThreadID: run.ThreadID,
		TurnID:   run.TurnID,
		ItemID:   run.ItemID,
		Delta:    delta,
	})
}

func (s *Server) PublishProcessExited(event toolprocess.ExitEvent) {
	if s == nil {
		return
	}
	method, params := ProcessExitedNotification(event)
	s.PublishNotification(method, params)
	s.unregisterCommandExecProcess(event.Snapshot.ID)
}

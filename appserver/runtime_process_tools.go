package appserver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	toolprocess "github.com/fugue-labs/gollem/appserver/tools/process"
	"github.com/fugue-labs/gollem/core"
)

const (
	runtimeProcessToolNamespace     = "workspace"
	runtimeProcessOutputMaxBytes    = 64 * 1024
	runtimeProcessStreamBufferBytes = runtimeProcessOutputMaxBytes / 2
	runtimeProcessDefaultTimeout    = 2 * time.Minute
	runtimeProcessMaxTimeout        = 5 * time.Minute
	runtimeProcessMaxArgumentBytes  = 16 * 1024
	runtimeProcessMaxArgumentCount  = 256
	runtimeProcessMaxListResults    = 32
	runtimeProcessSummaryMaxBytes   = 1536
	runtimeProcessMetadataMaxBytes  = 1024
	runtimeProcessMetadataMarker    = "[truncated]"
)

type runtimeProcessRunParams struct {
	Command        string   `json:"command" jsonschema:"description=Executable name or path; shell syntax is not interpreted"`
	Args           []string `json:"args,omitempty" jsonschema:"description=Arguments passed directly to the executable"`
	WorkDir        string   `json:"workdir,omitempty" jsonschema:"description=Workspace-relative working directory"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"description=Execution timeout in seconds (1-300); defaults to 120"`
}

type runtimeProcessIDParams struct {
	ID string `json:"id" jsonschema:"description=Workspace process identifier"`
}

type runtimeProcessRunResult struct {
	ID               string `json:"id,omitempty"`
	PID              int    `json:"pid,omitempty"`
	Command          string `json:"command"`
	CommandTruncated bool   `json:"commandTruncated,omitempty"`
	WorkDir          string `json:"workdir"`
	Status           string `json:"status"`
	ExitCode         int    `json:"exitCode"`
	Stdout           string `json:"stdout,omitempty"`
	Stderr           string `json:"stderr,omitempty"`
	StdoutTruncated  bool   `json:"stdoutTruncated,omitempty"`
	StderrTruncated  bool   `json:"stderrTruncated,omitempty"`
	Error            string `json:"error,omitempty"`
	DurationMS       int64  `json:"durationMs,omitempty"`
}

type runtimeProcessSummary struct {
	ID                 string     `json:"id"`
	PID                int        `json:"pid"`
	Command            string     `json:"command"`
	Args               []string   `json:"args,omitempty"`
	WorkDir            string     `json:"workdir"`
	Status             string     `json:"status"`
	ExitCode           *int       `json:"exitCode,omitempty"`
	StartedAt          time.Time  `json:"startedAt"`
	EndedAt            *time.Time `json:"endedAt,omitempty"`
	Error              string     `json:"error,omitempty"`
	ArgumentCount      int        `json:"argumentCount"`
	ArgumentsTruncated bool       `json:"argumentsTruncated,omitempty"`
	MetadataTruncated  bool       `json:"metadataTruncated,omitempty"`
}

type runtimeProcessListResult struct {
	Processes []runtimeProcessSummary `json:"processes"`
	Total     int                     `json:"total"`
	Truncated bool                    `json:"truncated,omitempty"`
}

type runtimeProcessStatusResult struct {
	Process         runtimeProcessSummary `json:"process"`
	Stdout          string                `json:"stdout,omitempty"`
	Stderr          string                `json:"stderr,omitempty"`
	StdoutTruncated bool                  `json:"stdoutTruncated,omitempty"`
	StderrTruncated bool                  `json:"stderrTruncated,omitempty"`
}

type runtimeCommandStartedEvent struct {
	RunID      string
	ToolCallID string
	ToolName   string
	Command    string
	WorkDir    string
	StartedAt  time.Time
	ItemID     *string
}

type runtimeCommandOutputEvent struct {
	RunID      string
	ToolCallID string
	ToolName   string
	ProcessID  string
	Data       []byte
	At         time.Time
}

type runtimeCommandCompletedEvent struct {
	RunID       string
	ToolCallID  string
	ToolName    string
	Snapshot    *toolprocess.Snapshot
	Error       string
	Declined    bool
	CompletedAt time.Time
}

// ProcessRuntimeTools adapts the workspace-scoped process service into
// provider-neutral model tools. Command execution remains approval-backed and
// publishes command lifecycle events for durable runtime item tracking.
func ProcessRuntimeTools(service *toolprocess.Service) []core.Tool {
	if service == nil {
		return nil
	}
	tools := []core.Tool{
		core.FuncTool[runtimeProcessRunParams](
			"workspace_run_command",
			"Run one executable with explicit arguments inside the workspace. Output and runtime are bounded, and execution uses the configured approval policy.",
			func(ctx context.Context, rc *core.RunContext, params runtimeProcessRunParams) (runtimeProcessRunResult, error) {
				timeout, err := validateRuntimeProcessRunParams(params)
				if err != nil {
					return runtimeProcessRunResult{}, err
				}
				command := formatRuntimeProcessCommand(params.Command, params.Args)
				workDir := strings.TrimSpace(params.WorkDir)
				if workDir == "" {
					workDir = "."
				}
				startedAt := time.Now().UTC()
				commandItemID := ""
				publishRuntimeCommandStarted(rc, runtimeCommandStartedEvent{
					RunID:      runtimeRunID(rc),
					ToolCallID: runtimeToolCallID(ctx, rc),
					ToolName:   runtimeToolName(rc, "workspace_run_command"),
					Command:    command,
					WorkDir:    workDir,
					StartedAt:  startedAt,
					ItemID:     &commandItemID,
				})
				approvalCtx := withRuntimeApprovalItemID(ctx, commandItemID)
				snapshot, runErr := service.Run(approvalCtx, toolprocess.StartRequest{
					Command:             strings.TrimSpace(params.Command),
					Args:                append([]string(nil), params.Args...),
					WorkDir:             workDir,
					Timeout:             timeout,
					MaxOutputBytes:      runtimeProcessStreamBufferBytes,
					SuppressGlobalSinks: true,
					OutputSink: func(event toolprocess.OutputEvent) {
						publishRuntimeCommandOutput(rc, runtimeCommandOutputEvent{
							RunID:      runtimeRunID(rc),
							ToolCallID: runtimeToolCallID(approvalCtx, rc),
							ToolName:   runtimeToolName(rc, "workspace_run_command"),
							ProcessID:  event.ID,
							Data:       append([]byte(nil), event.Data...),
							At:         event.At,
						})
					},
				})
				result := newRuntimeProcessRunResult(command, workDir, snapshot)
				if runErr == nil && snapshot != nil && snapshot.Status != toolprocess.StatusCompleted {
					runErr = runtimeProcessExitError(snapshot)
				}
				publishRuntimeCommandCompleted(rc, runtimeCommandCompletedEvent{
					RunID:       runtimeRunID(rc),
					ToolCallID:  runtimeToolCallID(ctx, rc),
					ToolName:    runtimeToolName(rc, "workspace_run_command"),
					Snapshot:    snapshot,
					Error:       runtimeErrorText(runErr),
					Declined:    errors.Is(runErr, toolprocess.ErrApprovalDenied),
					CompletedAt: time.Now().UTC(),
				})
				return result, runErr
			},
			core.WithToolSequential(true),
			core.WithToolTimeout(runtimeProcessMaxTimeout+30*time.Second),
		),
		core.FuncTool[struct{}](
			"workspace_list_processes",
			"List bounded metadata for processes owned by the workspace process service.",
			func(ctx context.Context, _ struct{}) (runtimeProcessListResult, error) {
				snapshots, err := service.List(ctx)
				if err != nil {
					return runtimeProcessListResult{}, err
				}
				total := len(snapshots)
				if len(snapshots) > runtimeProcessMaxListResults {
					snapshots = snapshots[:runtimeProcessMaxListResults]
				}
				processes := make([]runtimeProcessSummary, 0, len(snapshots))
				for i := range snapshots {
					processes = append(processes, newRuntimeProcessSummary(&snapshots[i]))
				}
				return runtimeProcessListResult{Processes: processes, Total: total, Truncated: total > len(processes)}, nil
			},
			core.WithToolConcurrencySafe(true),
			core.WithToolTimeout(30*time.Second),
		),
		core.FuncTool[runtimeProcessIDParams](
			"workspace_process_status",
			"Inspect one workspace process and its bounded captured output.",
			func(ctx context.Context, params runtimeProcessIDParams) (runtimeProcessStatusResult, error) {
				if strings.TrimSpace(params.ID) == "" {
					return runtimeProcessStatusResult{}, errors.New("id is required")
				}
				snapshot, err := service.Snapshot(ctx, strings.TrimSpace(params.ID))
				if err != nil {
					return runtimeProcessStatusResult{}, err
				}
				return runtimeProcessStatusResult{
					Process:         newRuntimeProcessSummary(snapshot),
					Stdout:          runtimeProcessStreamValue(snapshot.Stdout, snapshot.StdoutTruncated),
					Stderr:          runtimeProcessStreamValue(snapshot.Stderr, snapshot.StderrTruncated),
					StdoutTruncated: runtimeProcessStreamTruncated(snapshot.Stdout, snapshot.StdoutTruncated),
					StderrTruncated: runtimeProcessStreamTruncated(snapshot.Stderr, snapshot.StderrTruncated),
				}, nil
			},
			core.WithToolConcurrencySafe(true),
			core.WithToolTimeout(30*time.Second),
		),
	}
	for i := range tools {
		tools[i].Definition.Namespace = runtimeProcessToolNamespace
	}
	return tools
}

func validateRuntimeProcessRunParams(params runtimeProcessRunParams) (time.Duration, error) {
	command := strings.TrimSpace(params.Command)
	if command == "" {
		return 0, errors.New("command is required")
	}
	total := len(command)
	if len(params.Args) > runtimeProcessMaxArgumentCount {
		return 0, fmt.Errorf("args exceeds %d entries", runtimeProcessMaxArgumentCount)
	}
	for _, arg := range params.Args {
		total += len(arg)
	}
	if total > runtimeProcessMaxArgumentBytes {
		return 0, fmt.Errorf("command and args exceed %d bytes", runtimeProcessMaxArgumentBytes)
	}
	if len(params.WorkDir) > runtimeProcessMaxArgumentBytes {
		return 0, fmt.Errorf("workdir exceeds %d bytes", runtimeProcessMaxArgumentBytes)
	}
	if params.TimeoutSeconds < 0 || params.TimeoutSeconds > int(runtimeProcessMaxTimeout/time.Second) {
		return 0, fmt.Errorf("timeoutSeconds must be between 1 and %d when provided", int(runtimeProcessMaxTimeout/time.Second))
	}
	if params.TimeoutSeconds == 0 {
		return runtimeProcessDefaultTimeout, nil
	}
	return time.Duration(params.TimeoutSeconds) * time.Second, nil
}

func formatRuntimeProcessCommand(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteRuntimeProcessArgument(strings.TrimSpace(command)))
	for _, arg := range args {
		parts = append(parts, quoteRuntimeProcessArgument(arg))
	}
	return strings.Join(parts, " ")
}

func quoteRuntimeProcessArgument(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !runtimeProcessArgumentSafeRune(r)
	}) == -1 {
		return value
	}
	return strconv.Quote(value)
}

func runtimeProcessArgumentSafeRune(r rune) bool {
	return r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '@' || r == '%' || r == '+' || r == '=' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func newRuntimeProcessRunResult(command, workDir string, snapshot *toolprocess.Snapshot) runtimeProcessRunResult {
	boundedCommand, commandTruncated := boundedRuntimeProcessMetadata(command, runtimeProcessMaxArgumentBytes)
	boundedWorkDir, _ := boundedRuntimeProcessMetadata(workDir, runtimeProcessMetadataMaxBytes)
	result := runtimeProcessRunResult{Command: boundedCommand, CommandTruncated: commandTruncated, WorkDir: boundedWorkDir}
	if snapshot == nil {
		return result
	}
	result.ID = snapshot.ID
	result.PID = snapshot.PID
	result.WorkDir, _ = boundedRuntimeProcessMetadata(snapshot.WorkDir, runtimeProcessMetadataMaxBytes)
	result.Status = string(snapshot.Status)
	result.ExitCode = snapshot.ExitCode
	result.Stdout, result.StdoutTruncated = boundedRuntimeProcessStream(snapshot.Stdout, snapshot.StdoutTruncated)
	result.Stderr, result.StderrTruncated = boundedRuntimeProcessStream(snapshot.Stderr, snapshot.StderrTruncated)
	result.Error = snapshot.Error
	if !snapshot.EndedAt.IsZero() {
		result.DurationMS = snapshot.EndedAt.Sub(snapshot.StartedAt).Milliseconds()
	}
	return result
}

func newRuntimeProcessSummary(snapshot *toolprocess.Snapshot) runtimeProcessSummary {
	if snapshot == nil {
		return runtimeProcessSummary{}
	}
	command, commandTruncated := boundedRuntimeProcessMetadata(snapshot.Command, runtimeProcessMetadataMaxBytes)
	workDir, workDirTruncated := boundedRuntimeProcessMetadata(snapshot.WorkDir, runtimeProcessMetadataMaxBytes)
	errText, errTruncated := boundedRuntimeProcessMetadata(snapshot.Error, runtimeProcessMetadataMaxBytes)
	args, argsTruncated := boundedRuntimeProcessArguments(snapshot.Args, runtimeProcessSummaryMaxBytes-len(command))
	result := runtimeProcessSummary{
		ID:                 snapshot.ID,
		PID:                snapshot.PID,
		Command:            command,
		Args:               args,
		WorkDir:            workDir,
		Status:             string(snapshot.Status),
		StartedAt:          snapshot.StartedAt,
		Error:              errText,
		ArgumentCount:      len(snapshot.Args),
		ArgumentsTruncated: argsTruncated,
		MetadataTruncated:  commandTruncated || workDirTruncated || errTruncated,
	}
	if snapshot.Status != toolprocess.StatusRunning {
		exitCode := snapshot.ExitCode
		result.ExitCode = &exitCode
	}
	if !snapshot.EndedAt.IsZero() {
		endedAt := snapshot.EndedAt
		result.EndedAt = &endedAt
	}
	return result
}

func runtimeProcessExitError(snapshot *toolprocess.Snapshot) error {
	if snapshot == nil {
		return errors.New("command did not return a process snapshot")
	}
	message := strings.TrimSpace(snapshot.Error)
	if message == "" {
		message = fmt.Sprintf("command exited with status %s and code %d", snapshot.Status, snapshot.ExitCode)
	}
	return errors.New(message)
}

func boundedRuntimeProcessStream(data []byte, alreadyTruncated bool) (string, bool) {
	valid := strings.ToValidUTF8(string(data), "\uFFFD")
	if !alreadyTruncated && len(valid) <= runtimeProcessStreamBufferBytes {
		return valid, false
	}
	limit := runtimeProcessStreamBufferBytes - len(runtimeCommandOutputTruncatedMarker)
	return validRuntimeUTF8Prefix(valid, limit) + runtimeCommandOutputTruncatedMarker, true
}

func boundedRuntimeProcessMetadata(value string, limit int) (string, bool) {
	valid := strings.ToValidUTF8(value, "\uFFFD")
	if len(valid) <= limit {
		return valid, false
	}
	available := limit - len(runtimeProcessMetadataMarker)
	if available < 0 {
		available = 0
	}
	return validRuntimeUTF8Prefix(valid, available) + runtimeProcessMetadataMarker, true
}

func boundedRuntimeProcessArguments(args []string, limit int) ([]string, bool) {
	if limit <= 0 {
		return nil, len(args) > 0
	}
	result := make([]string, 0, len(args))
	used := 0
	for _, arg := range args {
		remaining := limit - used
		if remaining <= 0 {
			return result, true
		}
		bounded, truncated := boundedRuntimeProcessMetadata(arg, remaining)
		result = append(result, bounded)
		used += len(bounded)
		if truncated {
			return result, true
		}
	}
	return result, false
}

func runtimeProcessStreamValue(data []byte, alreadyTruncated bool) string {
	value, _ := boundedRuntimeProcessStream(data, alreadyTruncated)
	return value
}

func runtimeProcessStreamTruncated(data []byte, alreadyTruncated bool) bool {
	_, truncated := boundedRuntimeProcessStream(data, alreadyTruncated)
	return truncated
}

func runtimeRunID(rc *core.RunContext) string {
	if rc == nil {
		return ""
	}
	return rc.RunID
}

func runtimeToolCallID(ctx context.Context, rc *core.RunContext) string {
	if rc != nil && rc.ToolCallID != "" {
		return rc.ToolCallID
	}
	return core.ToolCallIDFromContext(ctx)
}

func runtimeToolName(rc *core.RunContext, fallback string) string {
	if rc != nil && rc.ToolName != "" {
		return rc.ToolName
	}
	return fallback
}

func runtimeErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func publishRuntimeCommandStarted(rc *core.RunContext, event runtimeCommandStartedEvent) {
	if rc != nil && rc.EventBus != nil {
		core.Publish(rc.EventBus, event)
	}
}

func publishRuntimeCommandOutput(rc *core.RunContext, event runtimeCommandOutputEvent) {
	if rc != nil && rc.EventBus != nil && len(event.Data) > 0 {
		core.Publish(rc.EventBus, event)
	}
}

func publishRuntimeCommandCompleted(rc *core.RunContext, event runtimeCommandCompletedEvent) {
	if rc != nil && rc.EventBus != nil {
		core.Publish(rc.EventBus, event)
	}
}

func validRuntimeUTF8Prefix(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	prefix := value[:limit]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}

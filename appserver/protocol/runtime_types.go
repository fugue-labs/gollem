package protocol

import (
	"encoding/json"
	"time"
)

const (
	ItemTypeDynamicToolCall   = "dynamicToolCall"
	ItemTypeCommandExecution  = "commandExecution"
	ItemTypeFileChange        = "fileChange"
	ItemTypeMCPToolCall       = "mcpToolCall"
	ItemTypeContextCompaction = "contextCompaction"

	ItemStatusInProgress = "inProgress"
	ItemStatusCompleted  = "completed"
	ItemStatusFailed     = "failed"
	ItemStatusDeclined   = "declined"
)

// TimelineItem is the durable item envelope returned by thread item APIs.
// Payload contains one of the concrete item types below when Kind is known.
type TimelineItem struct {
	ID           string          `json:"id"`
	ThreadID     string          `json:"threadId"`
	TurnID       string          `json:"turnId,omitempty"`
	ParentItemID string          `json:"parentItemId,omitempty"`
	Seq          int64           `json:"seq"`
	Kind         string          `json:"kind"`
	Status       string          `json:"status,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

type DynamicToolCallItem struct {
	Type         string                       `json:"type" jsonschema:"enum=dynamicToolCall"`
	ID           string                       `json:"id,omitempty"`
	Namespace    *string                      `json:"namespace"`
	Tool         string                       `json:"tool"`
	Arguments    any                          `json:"arguments"`
	Status       string                       `json:"status" jsonschema:"enum=inProgress|completed|failed"`
	ContentItems []DynamicToolCallContentItem `json:"contentItems"`
	Success      *bool                        `json:"success"`
	DurationMS   *int64                       `json:"durationMs"`
}

type DynamicToolCallContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolPayloadSummary struct {
	Omitted bool   `json:"omitted"`
	Reason  string `json:"reason"`
	Bytes   int    `json:"bytes"`
	SHA256  string `json:"sha256"`
}

type DynamicToolCallItemStartedNotificationParams struct {
	Item        DynamicToolCallItem `json:"item"`
	ThreadID    string              `json:"threadId"`
	TurnID      string              `json:"turnId"`
	StartedAtMS int64               `json:"startedAtMs"`
}

type DynamicToolCallItemCompletedNotificationParams struct {
	Item          DynamicToolCallItem `json:"item"`
	ThreadID      string              `json:"threadId"`
	TurnID        string              `json:"turnId"`
	CompletedAtMS int64               `json:"completedAtMs"`
}

type CommandExecutionItem struct {
	Type             string                   `json:"type" jsonschema:"enum=commandExecution"`
	ID               string                   `json:"id,omitempty"`
	Command          string                   `json:"command"`
	CWD              string                   `json:"cwd"`
	ProcessID        *string                  `json:"processId"`
	Source           string                   `json:"source" jsonschema:"enum=agent|userShell"`
	Status           string                   `json:"status" jsonschema:"enum=inProgress|completed|failed|declined"`
	CommandActions   []CommandExecutionAction `json:"commandActions"`
	AggregatedOutput *string                  `json:"aggregatedOutput"`
	ExitCode         *int                     `json:"exitCode"`
	DurationMS       *int64                   `json:"durationMs"`
	StartedAt        time.Time                `json:"startedAt"`
	CompletedAt      *time.Time               `json:"completedAt"`
}

type CommandExecutionAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type CommandExecutionItemStartedNotificationParams struct {
	Item        CommandExecutionItem `json:"item"`
	ThreadID    string               `json:"threadId"`
	TurnID      string               `json:"turnId"`
	StartedAtMS int64                `json:"startedAtMs"`
}

type CommandExecutionItemCompletedNotificationParams struct {
	Item          CommandExecutionItem `json:"item"`
	ThreadID      string               `json:"threadId"`
	TurnID        string               `json:"turnId"`
	CompletedAtMS int64                `json:"completedAtMs"`
}

type CommandExecutionOutputDeltaNotificationParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type FileChangeItem struct {
	Type     string                       `json:"type" jsonschema:"enum=fileChange"`
	ID       string                       `json:"id,omitempty"`
	Changes  []FileUpdateChange           `json:"changes"`
	Status   string                       `json:"status" jsonschema:"enum=inProgress|completed"`
	Evidence []FileChangeArtifactEvidence `json:"evidence,omitempty"`
}

type FileUpdateChange struct {
	Path string          `json:"path"`
	Kind PatchChangeKind `json:"kind"`
	Diff string          `json:"diff"`
}

type PatchChangeKind struct {
	Type     string  `json:"type" jsonschema:"enum=add|delete|update"`
	MovePath *string `json:"movePath,omitempty"`
}

func (k PatchChangeKind) MarshalJSON() ([]byte, error) {
	payload := map[string]any{"type": k.Type}
	if k.Type == "update" {
		payload["movePath"] = k.MovePath
	}
	return json.Marshal(payload)
}

func (k *PatchChangeKind) UnmarshalJSON(data []byte) error {
	var payload struct {
		Type     string  `json:"type"`
		MovePath *string `json:"movePath"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	k.Type = payload.Type
	k.MovePath = payload.MovePath
	return nil
}

type FileChangeArtifactEvidence struct {
	Path                 string `json:"path"`
	Operation            string `json:"operation"`
	Bytes                int64  `json:"bytes"`
	BeforeSHA256         string `json:"beforeSha256,omitempty"`
	AfterSHA256          string `json:"afterSha256,omitempty"`
	DiffTruncated        bool   `json:"diffTruncated,omitempty"`
	DiffOmittedReason    string `json:"diffOmittedReason,omitempty"`
	ContentEncoding      string `json:"contentEncoding,omitempty"`
	ContentTruncated     bool   `json:"contentTruncated,omitempty"`
	ContentOmittedReason string `json:"contentOmittedReason,omitempty"`
}

type FileChangeItemStartedNotificationParams struct {
	Item        FileChangeItem `json:"item"`
	ThreadID    string         `json:"threadId"`
	TurnID      string         `json:"turnId"`
	StartedAtMS int64          `json:"startedAtMs"`
}

type FileChangeItemCompletedNotificationParams struct {
	Item          FileChangeItem `json:"item"`
	ThreadID      string         `json:"threadId"`
	TurnID        string         `json:"turnId"`
	CompletedAtMS int64          `json:"completedAtMs"`
}

type FileChangePatchUpdatedNotificationParams struct {
	ThreadID string             `json:"threadId"`
	TurnID   string             `json:"turnId"`
	ItemID   string             `json:"itemId"`
	Changes  []FileUpdateChange `json:"changes"`
}

type MCPToolCallItem struct {
	Type              string             `json:"type" jsonschema:"enum=mcpToolCall"`
	ID                string             `json:"id,omitempty"`
	Server            string             `json:"server"`
	Tool              string             `json:"tool"`
	Status            string             `json:"status" jsonschema:"enum=inProgress|completed|failed"`
	Arguments         any                `json:"arguments"`
	AppContext        any                `json:"appContext"`
	MCPAppResourceURI *string            `json:"mcpAppResourceUri"`
	PluginID          *string            `json:"pluginId"`
	Result            *MCPToolCallResult `json:"result"`
	Error             *MCPToolCallError  `json:"error"`
	DurationMS        *int64             `json:"durationMs"`
}

type MCPContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type MCPToolCallResult struct {
	Content           []MCPContent `json:"content"`
	StructuredContent any          `json:"structuredContent"`
	Meta              any          `json:"_meta"`
}

type MCPToolCallError struct {
	Message string `json:"message"`
}

type MCPToolCallItemStartedNotificationParams struct {
	Item        MCPToolCallItem `json:"item"`
	ThreadID    string          `json:"threadId"`
	TurnID      string          `json:"turnId"`
	StartedAtMS int64           `json:"startedAtMs"`
}

type MCPToolCallItemCompletedNotificationParams struct {
	Item          MCPToolCallItem `json:"item"`
	ThreadID      string          `json:"threadId"`
	TurnID        string          `json:"turnId"`
	CompletedAtMS int64           `json:"completedAtMs"`
}

type MCPToolCallProgressNotificationParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Message  string `json:"message"`
}

type ThreadCompactStartParams struct {
	ID       string `json:"id,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
}

type ThreadCompactStartResponse struct{}

type ContextCompactionItem struct {
	Type      string    `json:"type" jsonschema:"enum=contextCompaction"`
	Summary   string    `json:"summary,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type ThreadCompactedNotificationParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type ThreadTokenUsageUpdatedNotificationParams struct {
	ThreadID   string     `json:"threadId"`
	TurnID     string     `json:"turnId"`
	TokenUsage TokenUsage `json:"tokenUsage"`
}

type TokenUsage struct {
	Total              TokenUsageBreakdown `json:"total"`
	Last               TokenUsageBreakdown `json:"last"`
	ModelContextWindow *int64              `json:"modelContextWindow"`
}

type TokenUsageBreakdown struct {
	TotalTokens           int64 `json:"totalTokens"`
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
}

type TurnDiffUpdatedNotificationParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Diff     string `json:"diff"`
}

type ItemLifecycleNotificationParams struct {
	ThreadID string        `json:"threadId"`
	TurnID   string        `json:"turnId,omitempty"`
	ItemID   string        `json:"itemId,omitempty"`
	Item     *TimelineItem `json:"item,omitempty"`
	At       time.Time     `json:"at"`
}

type ApprovalRequestBase struct {
	ThreadID    string `json:"threadId"`
	TurnID      string `json:"turnId"`
	ItemID      string `json:"itemId"`
	StartedAtMS int64  `json:"startedAtMs"`
	Reason      string `json:"reason,omitempty"`
}

type FileChangeApprovalRequestParams struct {
	ApprovalRequestBase
	GrantRoot   *string `json:"grantRoot"`
	Operation   string  `json:"operation,omitempty"`
	Path        string  `json:"path,omitempty"`
	Destination string  `json:"destination,omitempty"`
	Destructive bool    `json:"destructive,omitempty"`
}

type CommandExecutionApprovalRequestParams struct {
	ApprovalRequestBase
	ApprovalID     string                   `json:"approvalId,omitempty"`
	Command        string                   `json:"command,omitempty"`
	Args           []string                 `json:"args,omitempty"`
	CWD            string                   `json:"cwd,omitempty"`
	Operation      string                   `json:"operation,omitempty"`
	Signal         string                   `json:"signal,omitempty"`
	Destructive    bool                     `json:"destructive,omitempty"`
	CommandActions []CommandExecutionAction `json:"commandActions,omitempty"`
}

type PermissionsApprovalRequestParams struct {
	ApprovalRequestBase
	CWD         string         `json:"cwd"`
	Operation   string         `json:"operation,omitempty"`
	Path        string         `json:"path,omitempty"`
	Branch      string         `json:"branch,omitempty"`
	Base        string         `json:"base,omitempty"`
	Message     string         `json:"message,omitempty"`
	Pathspecs   []string       `json:"pathspecs,omitempty"`
	Permissions map[string]any `json:"permissions"`
}

type ApprovalRespondParams struct {
	RequestID string `json:"requestId"`
	ID        string `json:"id,omitempty"`
	Approved  bool   `json:"approved"`
	Message   string `json:"message,omitempty"`
}

type ApprovalRespondResult struct {
	OK        bool   `json:"ok"`
	RequestID string `json:"requestId"`
	Approved  bool   `json:"approved"`
}

type ServerRequestResolvedNotificationParams struct {
	ThreadID  string `json:"threadId"`
	RequestID string `json:"requestId"`
}

type DaemonShutdownState struct {
	Requested bool   `json:"requested"`
	Restart   bool   `json:"restart"`
	Reason    string `json:"reason,omitempty"`
}

type DaemonStatus struct {
	Status            string              `json:"status" jsonschema:"enum=running|stopping"`
	Name              string              `json:"name"`
	Version           string              `json:"version"`
	ProtocolVersion   string              `json:"protocolVersion"`
	PID               int                 `json:"pid"`
	StartedAt         time.Time           `json:"startedAt"`
	UptimeMillis      int64               `json:"uptimeMillis"`
	Transport         string              `json:"transport,omitempty"`
	WorkDir           string              `json:"workDir,omitempty"`
	StorePath         string              `json:"storePath,omitempty"`
	ShutdownRequested bool                `json:"shutdownRequested"`
	RestartRequested  bool                `json:"restartRequested"`
	Shutdown          DaemonShutdownState `json:"shutdown,omitempty"`
}

type DaemonVersion struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	ProtocolVersion string `json:"protocolVersion"`
	GoVersion       string `json:"goVersion"`
	GOOS            string `json:"goos"`
	GOARCH          string `json:"goarch"`
}

type DaemonStartResult struct {
	OK             bool         `json:"ok"`
	AlreadyRunning bool         `json:"alreadyRunning"`
	Status         DaemonStatus `json:"status"`
}

type DaemonStopResult struct {
	OK       bool         `json:"ok"`
	Stopping bool         `json:"stopping"`
	Restart  bool         `json:"restart"`
	Status   DaemonStatus `json:"status"`
}

type DaemonShutdownParams struct {
	Reason string `json:"reason,omitempty"`
}

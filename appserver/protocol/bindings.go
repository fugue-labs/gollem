package protocol

// WireTypeBinding maps one method to concrete exported parameter and result
// definitions. Multiple Params entries represent polymorphic payloads.
type WireTypeBinding struct {
	Method  string   `json:"method"`
	Surface Surface  `json:"surface"`
	Params  []string `json:"params,omitempty"`
	Result  []string `json:"result,omitempty"`
}

// ItemPayloadBinding maps a durable timeline item kind to the exported type
// carried in TimelineItem.Payload.
type ItemPayloadBinding struct {
	Kind string `json:"kind"`
	Type string `json:"type"`
}

var wireTypeBindings = []WireTypeBinding{
	{Method: "approval/respond", Surface: SurfaceGollemExtension, Params: []string{"ApprovalRespondParams"}, Result: []string{"ApprovalRespondResult"}},
	{Method: "cache/stats", Surface: SurfaceGollemExtension, Result: []string{"CacheStatsResponse"}},
	{Method: "command/exec/outputDelta", Surface: SurfaceServerNotification, Params: []string{"CommandExecOutputDeltaNotification"}},
	{Method: "command/exec/resize", Surface: SurfaceClientRequest, Params: []string{"CommandExecResizeParams"}, Result: []string{"CommandExecResizeResponse"}},
	{Method: "command/exec/terminate", Surface: SurfaceClientRequest, Params: []string{"CommandExecTerminateParams"}, Result: []string{"CommandExecTerminateResponse"}},
	{Method: "command/exec/write", Surface: SurfaceClientRequest, Params: []string{"CommandExecWriteParams"}, Result: []string{"CommandExecWriteResponse"}},
	{Method: "daemon/restart", Surface: SurfaceGollemExtension, Params: []string{"DaemonShutdownParams"}, Result: []string{"DaemonStopResult"}},
	{Method: "daemon/start", Surface: SurfaceGollemExtension, Result: []string{"DaemonStartResult"}},
	{Method: "daemon/status", Surface: SurfaceGollemExtension, Result: []string{"DaemonStatus"}},
	{Method: "daemon/stop", Surface: SurfaceGollemExtension, Params: []string{"DaemonShutdownParams"}, Result: []string{"DaemonStopResult"}},
	{Method: "daemon/version", Surface: SurfaceGollemExtension, Result: []string{"DaemonVersion"}},
	{Method: "deprecationNotice", Surface: SurfaceServerNotification, Params: []string{"DeprecationNoticeNotification"}},
	{Method: "fs/changed", Surface: SurfaceServerNotification, Params: []string{"FsChangedNotification", "FileChangedNotification"}},
	{Method: "fs/copy", Surface: SurfaceClientRequest, Params: []string{"FsCopyParams"}, Result: []string{"FsCopyResponse"}},
	{Method: "fs/createDirectory", Surface: SurfaceClientRequest, Params: []string{"FsCreateDirectoryParams"}, Result: []string{"FsCreateDirectoryResponse"}},
	{Method: "fs/getMetadata", Surface: SurfaceClientRequest, Params: []string{"FsGetMetadataParams"}, Result: []string{"FsGetMetadataResponse"}},
	{Method: "fs/readDirectory", Surface: SurfaceClientRequest, Params: []string{"FsReadDirectoryParams"}, Result: []string{"FsReadDirectoryResponse"}},
	{Method: "fs/readFile", Surface: SurfaceClientRequest, Params: []string{"FsReadFileParams"}, Result: []string{"FsReadFileResponse"}},
	{Method: "fs/remove", Surface: SurfaceClientRequest, Params: []string{"FsRemoveParams"}, Result: []string{"FsRemoveResponse"}},
	{Method: "fs/unwatch", Surface: SurfaceClientRequest, Params: []string{"FsUnwatchParams"}, Result: []string{"FsUnwatchResponse"}},
	{Method: "fs/watch", Surface: SurfaceClientRequest, Params: []string{"FsWatchParams"}, Result: []string{"FsWatchResponse"}},
	{Method: "fs/writeFile", Surface: SurfaceClientRequest, Params: []string{"FsWriteFileParams"}, Result: []string{"FsWriteFileResponse"}},
	{Method: "git/status", Surface: SurfaceGollemExtension, Params: []string{"OperationalListParams"}, Result: []string{"GitStatusResponse"}},
	{Method: "git/worktree/list", Surface: SurfaceGollemExtension, Params: []string{"OperationalListParams"}, Result: []string{"GitWorktreeListResponse"}},
	{Method: "initialize", Surface: SurfaceClientRequest, Params: []string{"InitializeParams"}, Result: []string{"InitializeResponse"}},
	{Method: "initialized", Surface: SurfaceClientNotification},
	{Method: "item/agentMessage/delta", Surface: SurfaceServerNotification, Params: []string{"RuntimeDeltaNotification"}},
	{Method: "item/commandExecution/outputDelta", Surface: SurfaceServerNotification, Params: []string{"CommandExecutionOutputDeltaNotification"}},
	{Method: "item/commandExecution/requestApproval", Surface: SurfaceServerRequest, Params: []string{"CommandExecutionApprovalRequestParams"}, Result: []string{"CommandExecutionRequestApprovalResponse"}},
	{Method: "item/completed", Surface: SurfaceServerNotification, Params: []string{"ItemCompletedNotification", "ItemLifecycleNotificationParams", "DynamicToolCallItemCompletedNotificationParams", "CommandExecutionItemCompletedNotificationParams", "FileChangeItemCompletedNotificationParams", "MCPToolCallItemCompletedNotificationParams"}},
	{Method: "item/fileChange/patchUpdated", Surface: SurfaceServerNotification, Params: []string{"FileChangePatchUpdatedNotification"}},
	{Method: "item/fileChange/requestApproval", Surface: SurfaceServerRequest, Params: []string{"FileChangeApprovalRequestParams"}, Result: []string{"FileChangeRequestApprovalResponse"}},
	{Method: "item/fileChange/revert", Surface: SurfaceGollemExtension, Params: []string{"FileChangeRevertParams"}, Result: []string{"FileChangeRevertResult"}},
	{Method: "item/mcpToolCall/progress", Surface: SurfaceServerNotification, Params: []string{"McpToolCallProgressNotification"}},
	{Method: "item/permissions/requestApproval", Surface: SurfaceServerRequest, Params: []string{"PermissionsApprovalRequestParams"}},
	{Method: "item/reasoning/textDelta", Surface: SurfaceServerNotification, Params: []string{"RuntimeDeltaNotification"}},
	{Method: "item/started", Surface: SurfaceServerNotification, Params: []string{"ItemStartedNotification", "ItemLifecycleNotificationParams", "DynamicToolCallItemStartedNotificationParams", "CommandExecutionItemStartedNotificationParams", "FileChangeItemStartedNotificationParams", "MCPToolCallItemStartedNotificationParams"}},
	{Method: "item/tool/call", Surface: SurfaceServerRequest, Params: []string{"DynamicToolCallParams"}, Result: []string{"DynamicToolCallResponse"}},
	{Method: "item/tool/requestUserInput", Surface: SurfaceServerRequest, Params: []string{"ToolRequestUserInputParams"}, Result: []string{"ToolRequestUserInputResponse"}},
	{Method: "mcpServer/elicitation/request", Surface: SurfaceServerRequest, Params: []string{"McpServerElicitationRequestParams"}, Result: []string{"McpServerElicitationRequestResponse"}},
	{Method: "currentTime/read", Surface: SurfaceServerRequest, Params: []string{"CurrentTimeReadParams"}, Result: []string{"CurrentTimeReadResponse"}},
	{Method: "model/list", Surface: SurfaceClientRequest, Params: []string{"ModelCatalogListParams"}, Result: []string{"ModelCatalogListResponse"}},
	{Method: "provider/health/probe", Surface: SurfaceGollemExtension, Params: []string{"ProviderHealthProbeParams"}, Result: []string{"ProviderHealthProbeResponse"}},
	{Method: "provider/list", Surface: SurfaceGollemExtension, Params: []string{"ProviderListParams"}, Result: []string{"ProviderListResponse"}},
	{Method: "serverRequest/resolved", Surface: SurfaceServerNotification, Params: []string{"ServerRequestResolvedNotification"}},
	{Method: "thread/archive", Surface: SurfaceClientRequest, Params: []string{"ThreadArchiveParams"}, Result: []string{"ThreadArchiveResponse"}},
	{Method: "thread/archived", Surface: SurfaceServerNotification, Params: []string{"ThreadArchivedNotification"}},
	{Method: "thread/backgroundTerminals/clean", Surface: SurfaceClientRequest, Result: []string{"BackgroundTerminalCleanResponse"}},
	{Method: "thread/backgroundTerminals/list", Surface: SurfaceClientRequest, Params: []string{"OperationalListParams"}, Result: []string{"BackgroundTerminalListResponse"}},
	{Method: "thread/backgroundTerminals/read", Surface: SurfaceClientRequest, Params: []string{"BackgroundTerminalReadParams"}, Result: []string{"BackgroundTerminalReadResponse"}},
	{Method: "thread/backgroundTerminals/resize", Surface: SurfaceClientRequest, Params: []string{"BackgroundTerminalResizeParams"}, Result: []string{"BackgroundTerminalResizeResponse"}},
	{Method: "thread/backgroundTerminals/terminate", Surface: SurfaceClientRequest, Params: []string{"BackgroundTerminalTerminateParams"}, Result: []string{"BackgroundTerminalTerminateResponse"}},
	{Method: "thread/backgroundTerminals/write", Surface: SurfaceClientRequest, Params: []string{"BackgroundTerminalWriteParams"}, Result: []string{"BackgroundTerminalWriteResponse"}},
	{Method: "thread/closed", Surface: SurfaceServerNotification, Params: []string{"ThreadClosedNotification"}},
	{Method: "thread/compact/start", Surface: SurfaceClientRequest, Params: []string{"ThreadCompactStartParams"}, Result: []string{"ThreadCompactStartResponse"}},
	{Method: "thread/compacted", Surface: SurfaceServerNotification, Params: []string{"ContextCompactedNotification"}},
	{Method: "thread/delete", Surface: SurfaceClientRequest, Params: []string{"ThreadDeleteParams"}, Result: []string{"ThreadDeleteResponse"}},
	{Method: "thread/deleted", Surface: SurfaceServerNotification, Params: []string{"ThreadDeletedNotification"}},
	{Method: "thread/goal/clear", Surface: SurfaceClientRequest, Params: []string{"ThreadGoalClearParams"}, Result: []string{"ThreadGoalClearResponse"}},
	{Method: "thread/goal/cleared", Surface: SurfaceServerNotification, Params: []string{"ThreadGoalClearedNotification"}},
	{Method: "thread/goal/get", Surface: SurfaceClientRequest, Params: []string{"ThreadGoalGetParams"}, Result: []string{"ThreadGoalGetResponse"}},
	{Method: "thread/goal/set", Surface: SurfaceClientRequest, Params: []string{"ThreadGoalSetParams"}, Result: []string{"ThreadGoalSetResponse"}},
	{Method: "thread/goal/updated", Surface: SurfaceServerNotification, Params: []string{"ThreadGoalUpdatedNotification"}},
	{Method: "thread/fork", Surface: SurfaceClientRequest, Params: []string{"ThreadHistoryForkParams"}, Result: []string{"ThreadHistoryForkResult"}},
	{Method: "thread/list", Surface: SurfaceClientRequest, Params: []string{"ThreadListParams"}, Result: []string{"ThreadListResult"}},
	{Method: "thread/loaded/list", Surface: SurfaceClientRequest, Params: []string{"ThreadLoadedListParams"}, Result: []string{"ThreadLoadedListResponse"}},
	{Method: "thread/memoryMode/set", Surface: SurfaceClientRequest, Params: []string{"ThreadMemoryModeSetParams"}, Result: []string{"ThreadMemoryModeSetResponse"}},
	{Method: "thread/metadata/update", Surface: SurfaceClientRequest, Params: []string{"ThreadMetadataUpdateParams"}, Result: []string{"ThreadMetadataUpdateResult"}},
	{Method: "thread/name/set", Surface: SurfaceClientRequest, Params: []string{"ThreadSetNameParams"}, Result: []string{"ThreadSetNameResponse"}},
	{Method: "thread/name/updated", Surface: SurfaceServerNotification, Params: []string{"ThreadNameUpdatedNotification"}},
	{Method: "thread/read", Surface: SurfaceClientRequest, Params: []string{"ThreadReadParams"}, Result: []string{"ThreadReadResult"}},
	{Method: "thread/rollback", Surface: SurfaceClientRequest, Params: []string{"ThreadHistoryRollbackParams"}, Result: []string{"ThreadHistoryRollbackResult"}},
	{Method: "thread/start", Surface: SurfaceClientRequest, Params: []string{"ThreadRunStartParams"}, Result: []string{"ThreadRunStartResult"}},
	{Method: "thread/started", Surface: SurfaceServerNotification, Params: []string{"RuntimeThreadNotification"}},
	{Method: "thread/tokenUsage/updated", Surface: SurfaceServerNotification, Params: []string{"ThreadTokenUsageUpdatedNotification"}},
	{Method: "thread/unarchive", Surface: SurfaceClientRequest, Params: []string{"ThreadUnarchiveParams"}, Result: []string{"ThreadUnarchiveResult"}},
	{Method: "thread/unarchived", Surface: SurfaceServerNotification, Params: []string{"ThreadUnarchivedNotification"}},
	{Method: "thread/unsubscribe", Surface: SurfaceClientRequest, Params: []string{"ThreadUnsubscribeParams"}, Result: []string{"ThreadUnsubscribeResponse"}},
	{Method: "turn/completed", Surface: SurfaceServerNotification, Params: []string{"RuntimeTurnNotification"}},
	{Method: "turn/diff/updated", Surface: SurfaceServerNotification, Params: []string{"TurnDiffUpdatedNotification"}},
	{Method: "turn/interrupt", Surface: SurfaceClientRequest, Params: []string{"TurnRunInterruptParams"}, Result: []string{"TurnRunInterruptResult"}},
	{Method: "turn/retry", Surface: SurfaceGollemExtension, Params: []string{"TurnRunRetryParams"}, Result: []string{"TurnRunRetryResult"}},
	{Method: "turn/start", Surface: SurfaceClientRequest, Params: []string{"TurnRunStartParams"}, Result: []string{"TurnRunStartResult"}},
	{Method: "turn/started", Surface: SurfaceServerNotification, Params: []string{"RuntimeTurnNotification"}},
	{Method: "turn/steer", Surface: SurfaceClientRequest, Params: []string{"TurnSteerParams"}, Result: []string{"TurnSteerResponse"}},
}

var itemPayloadBindings = []ItemPayloadBinding{
	{Kind: ItemTypeCommandExecution, Type: "CommandExecutionItem"},
	{Kind: ItemTypeContextCompaction, Type: "ContextCompactionItem"},
	{Kind: ItemTypeDynamicToolCall, Type: "DynamicToolCallItem"},
	{Kind: ItemTypeFileChange, Type: "FileChangeItem"},
	{Kind: ItemTypeMCPToolCall, Type: "MCPToolCallItem"},
}

func WireTypeBindings() []WireTypeBinding {
	out := make([]WireTypeBinding, len(wireTypeBindings))
	for i, binding := range wireTypeBindings {
		out[i] = binding
		out[i].Params = append([]string(nil), binding.Params...)
		out[i].Result = append([]string(nil), binding.Result...)
	}
	return out
}

// ItemPayloadBindings returns a stable copy of the known durable runtime item
// payload mappings.
func ItemPayloadBindings() []ItemPayloadBinding {
	out := make([]ItemPayloadBinding, len(itemPayloadBindings))
	copy(out, itemPayloadBindings)
	return out
}

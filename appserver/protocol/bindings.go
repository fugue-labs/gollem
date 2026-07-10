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
	{Method: "daemon/restart", Surface: SurfaceGollemExtension, Params: []string{"DaemonShutdownParams"}, Result: []string{"DaemonStopResult"}},
	{Method: "daemon/start", Surface: SurfaceGollemExtension, Result: []string{"DaemonStartResult"}},
	{Method: "daemon/status", Surface: SurfaceGollemExtension, Result: []string{"DaemonStatus"}},
	{Method: "daemon/stop", Surface: SurfaceGollemExtension, Params: []string{"DaemonShutdownParams"}, Result: []string{"DaemonStopResult"}},
	{Method: "daemon/version", Surface: SurfaceGollemExtension, Result: []string{"DaemonVersion"}},
	{Method: "item/commandExecution/outputDelta", Surface: SurfaceServerNotification, Params: []string{"CommandExecutionOutputDeltaNotificationParams"}},
	{Method: "item/commandExecution/requestApproval", Surface: SurfaceServerRequest, Params: []string{"CommandExecutionApprovalRequestParams"}},
	{Method: "item/completed", Surface: SurfaceServerNotification, Params: []string{"ItemLifecycleNotificationParams", "DynamicToolCallItemCompletedNotificationParams", "CommandExecutionItemCompletedNotificationParams", "FileChangeItemCompletedNotificationParams", "MCPToolCallItemCompletedNotificationParams"}},
	{Method: "item/fileChange/patchUpdated", Surface: SurfaceServerNotification, Params: []string{"FileChangePatchUpdatedNotificationParams"}},
	{Method: "item/fileChange/requestApproval", Surface: SurfaceServerRequest, Params: []string{"FileChangeApprovalRequestParams"}},
	{Method: "item/mcpToolCall/progress", Surface: SurfaceServerNotification, Params: []string{"MCPToolCallProgressNotificationParams"}},
	{Method: "item/permissions/requestApproval", Surface: SurfaceServerRequest, Params: []string{"PermissionsApprovalRequestParams"}},
	{Method: "item/started", Surface: SurfaceServerNotification, Params: []string{"ItemLifecycleNotificationParams", "DynamicToolCallItemStartedNotificationParams", "CommandExecutionItemStartedNotificationParams", "FileChangeItemStartedNotificationParams", "MCPToolCallItemStartedNotificationParams"}},
	{Method: "serverRequest/resolved", Surface: SurfaceServerNotification, Params: []string{"ServerRequestResolvedNotificationParams"}},
	{Method: "thread/compact/start", Surface: SurfaceClientRequest, Params: []string{"ThreadCompactStartParams"}, Result: []string{"ThreadCompactStartResponse"}},
	{Method: "thread/compacted", Surface: SurfaceServerNotification, Params: []string{"ThreadCompactedNotificationParams"}},
	{Method: "thread/tokenUsage/updated", Surface: SurfaceServerNotification, Params: []string{"ThreadTokenUsageUpdatedNotificationParams"}},
	{Method: "turn/diff/updated", Surface: SurfaceServerNotification, Params: []string{"TurnDiffUpdatedNotificationParams"}},
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

package protocol

import (
	"encoding/json"
	"reflect"
)

//go:generate go run ./internal/cmd/schema

// Schema is a JSON Schema document fragment.
type Schema map[string]any

// JSONSchema returns the foundational app-server envelope, method inventory,
// exported wire definitions, and their method and durable-item bindings.
func JSONSchema() Schema {
	defs := foundationalSchemaDefinitions()
	for name, schema := range wireSchemaDefinitions() {
		defs[name] = schema
	}
	return Schema{
		"$schema":                        "https://json-schema.org/draft/2020-12/schema",
		"title":                          "Gollem App Server Protocol",
		"type":                           "object",
		"$defs":                          defs,
		"x-gollem-protocol-version":      ProtocolVersion,
		"x-gollem-schema-version":        SchemaVersion,
		"x-gollem-methods":               Methods(),
		"x-gollem-type-bindings":         WireTypeBindings(),
		"x-gollem-item-payload-bindings": ItemPayloadBindings(),
	}
}

func foundationalSchemaDefinitions() Schema {
	return Schema{
		"RequestID": Schema{
			"oneOf": []any{
				Schema{"type": "string"},
				Schema{"type": "integer"},
			},
		},
		"Error": Schema{
			"type": "object",
			"properties": Schema{
				"code":    Schema{"type": "integer"},
				"message": Schema{"type": "string"},
				"data":    Schema{},
			},
			"required": []any{"code", "message"},
		},
		"Request": Schema{
			"type": "object",
			"properties": Schema{
				"id":     Schema{"$ref": "#/$defs/RequestID"},
				"method": methodEnumSchema(SurfaceClientRequest, SurfaceGollemExtension),
				"params": Schema{},
			},
			"required":             []any{"id", "method"},
			"additionalProperties": false,
		},
		"Notification": Schema{
			"type": "object",
			"properties": Schema{
				"method": methodEnumSchema(SurfaceServerNotification, SurfaceClientNotification),
				"params": Schema{},
			},
			"required":             []any{"method"},
			"additionalProperties": false,
		},
		"Response": Schema{
			"type": "object",
			"properties": Schema{
				"id":     Schema{"$ref": "#/$defs/RequestID"},
				"result": Schema{},
				"error":  Schema{"$ref": "#/$defs/Error"},
			},
			"required":             []any{"id"},
			"additionalProperties": false,
		},
	}
}

func wireSchemaDefinitions() Schema {
	return wireSchemasForDefinitions(wireSchemaDefinitionTypes())
}

func wireSchemaDefinitionTypes() []wireSchemaDefinition {
	return []wireSchemaDefinition{
		{Name: "AbsolutePathBuf", Type: reflect.TypeFor[AbsolutePathBuf]()},
		{Name: "ActivePermissionProfile", Type: reflect.TypeFor[ActivePermissionProfile]()},
		{Name: "AgentMessageDeltaNotification", Type: reflect.TypeFor[AgentMessageDeltaNotification]()},
		{Name: "AgentMessageInputContent", Type: reflect.TypeFor[AgentMessageInputContent]()},
		{Name: "AgentPath", Type: reflect.TypeFor[AgentPath]()},
		{Name: "AdditionalContextEntry", Type: reflect.TypeFor[AdditionalContextEntry]()},
		{Name: "AdditionalContextKind", Type: reflect.TypeFor[AdditionalContextKind]()},
		{Name: "AdditionalFileSystemPermissions", Type: reflect.TypeFor[AdditionalFileSystemPermissions]()},
		{Name: "AdditionalNetworkPermissions", Type: reflect.TypeFor[AdditionalNetworkPermissions]()},
		{Name: "AdditionalPermissionProfile", Type: reflect.TypeFor[AdditionalPermissionProfile]()},
		{Name: "Account", Type: reflect.TypeFor[Account]()},
		{Name: "AccountLoginCompletedNotification", Type: reflect.TypeFor[AccountLoginCompletedNotification]()},
		{Name: "AccountRateLimitsUpdatedNotification", Type: reflect.TypeFor[AccountRateLimitsUpdatedNotification]()},
		{Name: "AccountTokenUsageDailyBucket", Type: reflect.TypeFor[AccountTokenUsageDailyBucket]()},
		{Name: "AccountTokenUsageSummary", Type: reflect.TypeFor[AccountTokenUsageSummary]()},
		{Name: "AccountUpdatedNotification", Type: reflect.TypeFor[AccountUpdatedNotification]()},
		{Name: "AddCreditsNudgeCreditType", Type: reflect.TypeFor[AddCreditsNudgeCreditType]()},
		{Name: "AddCreditsNudgeEmailStatus", Type: reflect.TypeFor[AddCreditsNudgeEmailStatus]()},
		{Name: "AmazonBedrockCredentialSource", Type: reflect.TypeFor[AmazonBedrockCredentialSource]()},
		{Name: "AnalyticsConfig", Type: reflect.TypeFor[AnalyticsConfig]()},
		{Name: "AttestationGenerateParams", Type: reflect.TypeFor[AttestationGenerateParams]()},
		{Name: "AttestationGenerateResponse", Type: reflect.TypeFor[AttestationGenerateResponse]()},
		{Name: "AuthMode", Type: reflect.TypeFor[AuthMode]()},
		{Name: "ApprovalRequestBase", Type: reflect.TypeFor[ApprovalRequestBase]()},
		{Name: "ApprovalRespondParams", Type: reflect.TypeFor[ApprovalRespondParams]()},
		{Name: "ApprovalRespondResult", Type: reflect.TypeFor[ApprovalRespondResult]()},
		{Name: "ApprovalsReviewer", Type: reflect.TypeFor[ApprovalsReviewer]()},
		{Name: "BackgroundTerminal", Type: reflect.TypeFor[BackgroundTerminal]()},
		{Name: "BackgroundTerminalCleanResponse", Type: reflect.TypeFor[BackgroundTerminalCleanResponse]()},
		{Name: "BackgroundTerminalListResponse", Type: reflect.TypeFor[BackgroundTerminalListResponse]()},
		{Name: "BackgroundTerminalReadParams", Type: reflect.TypeFor[BackgroundTerminalReadParams]()},
		{Name: "BackgroundTerminalReadResponse", Type: reflect.TypeFor[BackgroundTerminalReadResponse]()},
		{Name: "BackgroundTerminalResizeParams", Type: reflect.TypeFor[BackgroundTerminalResizeParams]()},
		{Name: "BackgroundTerminalResizeResponse", Type: reflect.TypeFor[BackgroundTerminalResizeResponse]()},
		{Name: "BackgroundTerminalStatus", Type: reflect.TypeFor[BackgroundTerminalStatus]()},
		{Name: "BackgroundTerminalTerminateParams", Type: reflect.TypeFor[BackgroundTerminalTerminateParams]()},
		{Name: "BackgroundTerminalTerminateResponse", Type: reflect.TypeFor[BackgroundTerminalTerminateResponse]()},
		{Name: "BackgroundTerminalWriteParams", Type: reflect.TypeFor[BackgroundTerminalWriteParams]()},
		{Name: "BackgroundTerminalWriteResponse", Type: reflect.TypeFor[BackgroundTerminalWriteResponse]()},
		{Name: "AppBranding", Type: reflect.TypeFor[AppBranding]()},
		{Name: "AppConfig", Type: reflect.TypeFor[AppConfig]()},
		{Name: "AppInfo", Type: reflect.TypeFor[AppInfo]()},
		{Name: "AppListUpdatedNotification", Type: reflect.TypeFor[AppListUpdatedNotification]()},
		{Name: "AppMetadata", Type: reflect.TypeFor[AppMetadata]()},
		{Name: "AppReview", Type: reflect.TypeFor[AppReview]()},
		{Name: "AppScreenshot", Type: reflect.TypeFor[AppScreenshot]()},
		{Name: "AppSummary", Type: reflect.TypeFor[AppSummary]()},
		{Name: "AppTemplateSummary", Type: reflect.TypeFor[AppTemplateSummary]()},
		{Name: "AppTemplateUnavailableReason", Type: reflect.TypeFor[AppTemplateUnavailableReason]()},
		{Name: "AppToolApproval", Type: reflect.TypeFor[AppToolApproval]()},
		{Name: "AppToolConfig", Type: reflect.TypeFor[AppToolConfig]()},
		{Name: "AppToolsConfig", Type: reflect.TypeFor[AppToolsConfig]()},
		{Name: "AppsInstalledParams", Type: reflect.TypeFor[AppsInstalledParams]()},
		{Name: "AppsReadParams", Type: reflect.TypeFor[AppsReadParams]()},
		{Name: "AppsConfig", Type: reflect.TypeFor[AppsConfig]()},
		{Name: "AppsDefaultConfig", Type: reflect.TypeFor[AppsDefaultConfig]()},
		{Name: "AppsListParams", Type: reflect.TypeFor[AppsListParams]()},
		{Name: "AppsListResponse", Type: reflect.TypeFor[AppsListResponse]()},
		{Name: "ApplyPatchApprovalParams", Type: reflect.TypeFor[ApplyPatchApprovalParams]()},
		{Name: "ApplyPatchApprovalResponse", Type: reflect.TypeFor[ApplyPatchApprovalResponse]()},
		{Name: "AskForApproval", Type: reflect.TypeFor[AskForApproval]()},
		{Name: "AutoCompactTokenLimitScope", Type: reflect.TypeFor[AutoCompactTokenLimitScope]()},
		{Name: "AutoReviewDecisionSource", Type: reflect.TypeFor[AutoReviewDecisionSource]()},
		{Name: "ByteRange", Type: reflect.TypeFor[ByteRange]()},
		{Name: "CacheEvent", Type: reflect.TypeFor[CacheEvent]()},
		{Name: "CacheEventType", Type: reflect.TypeFor[CacheEventType]()},
		{Name: "CacheProviderStats", Type: reflect.TypeFor[CacheProviderStats]()},
		{Name: "CacheStatsResponse", Type: reflect.TypeFor[CacheStatsResponse]()},
		{Name: "CancelLoginAccountParams", Type: reflect.TypeFor[CancelLoginAccountParams]()},
		{Name: "CancelLoginAccountResponse", Type: reflect.TypeFor[CancelLoginAccountResponse]()},
		{Name: "CancelLoginAccountStatus", Type: reflect.TypeFor[CancelLoginAccountStatus]()},
		{Name: "CapabilityRootLocation", Type: reflect.TypeFor[CapabilityRootLocation]()},
		{Name: "ChatgptAuthTokensRefreshParams", Type: reflect.TypeFor[ChatgptAuthTokensRefreshParams]()},
		{Name: "ChatgptAuthTokensRefreshReason", Type: reflect.TypeFor[ChatgptAuthTokensRefreshReason]()},
		{Name: "ChatgptAuthTokensRefreshResponse", Type: reflect.TypeFor[ChatgptAuthTokensRefreshResponse]()},
		{Name: "ClientInfo", Type: reflect.TypeFor[ClientInfo]()},
		{Name: "ClientNotification", Type: reflect.TypeFor[ClientNotification]()},
		{Name: "ClientRequest", Type: reflect.TypeFor[ClientRequest]()},
		{Name: "CodexErrorInfo", Type: reflect.TypeFor[CodexErrorInfo]()},
		{Name: "CollabAgentState", Type: reflect.TypeFor[CollabAgentState]()},
		{Name: "CollabAgentStatus", Type: reflect.TypeFor[CollabAgentStatus]()},
		{Name: "CollabAgentTool", Type: reflect.TypeFor[CollabAgentTool]()},
		{Name: "CollabAgentToolCallStatus", Type: reflect.TypeFor[CollabAgentToolCallStatus]()},
		{Name: "CollaborationMode", Type: reflect.TypeFor[CollaborationMode]()},
		{Name: "CollaborationModeMask", Type: reflect.TypeFor[CollaborationModeMask]()},
		{Name: "CommandAction", Type: reflect.TypeFor[CommandAction]()},
		{Name: "CommandExecOutputDeltaNotification", Type: reflect.TypeFor[CommandExecOutputDeltaNotification]()},
		{Name: "CommandExecOutputStream", Type: reflect.TypeFor[CommandExecOutputStream]()},
		{Name: "CommandExecParams", Type: reflect.TypeFor[CommandExecParams]()},
		{Name: "CommandExecResizeParams", Type: reflect.TypeFor[CommandExecResizeParams]()},
		{Name: "CommandExecResizeResponse", Type: reflect.TypeFor[CommandExecResizeResponse]()},
		{Name: "CommandExecResponse", Type: reflect.TypeFor[CommandExecResponse]()},
		{Name: "CommandExecTerminalSize", Type: reflect.TypeFor[CommandExecTerminalSize]()},
		{Name: "CommandExecTerminateParams", Type: reflect.TypeFor[CommandExecTerminateParams]()},
		{Name: "CommandExecTerminateResponse", Type: reflect.TypeFor[CommandExecTerminateResponse]()},
		{Name: "CommandExecWriteParams", Type: reflect.TypeFor[CommandExecWriteParams]()},
		{Name: "CommandExecWriteResponse", Type: reflect.TypeFor[CommandExecWriteResponse]()},
		{Name: "CommandExecutionAction", Type: reflect.TypeFor[CommandExecutionAction]()},
		{Name: "CommandExecutionApprovalDecision", Type: reflect.TypeFor[CommandExecutionApprovalDecision]()},
		{Name: "CommandExecutionApprovalRequestParams", Type: reflect.TypeFor[CommandExecutionApprovalRequestParams]()},
		{Name: "CommandExecutionRequestApprovalParams", Type: reflect.TypeFor[CommandExecutionRequestApprovalParams]()},
		{Name: "CommandExecutionRequestApprovalResponse", Type: reflect.TypeFor[CommandExecutionRequestApprovalResponse]()},
		{Name: "CommandExecutionItem", Type: reflect.TypeFor[CommandExecutionItem]()},
		{Name: "CommandExecutionItemCompletedNotificationParams", Type: reflect.TypeFor[CommandExecutionItemCompletedNotificationParams]()},
		{Name: "CommandExecutionItemStartedNotificationParams", Type: reflect.TypeFor[CommandExecutionItemStartedNotificationParams]()},
		{Name: "CommandExecutionOutputDeltaNotificationParams", Type: reflect.TypeFor[CommandExecutionOutputDeltaNotificationParams]()},
		{Name: "CommandExecutionSource", Type: reflect.TypeFor[CommandExecutionSource]()},
		{Name: "ComputerUseRequirements", Type: reflect.TypeFor[ComputerUseRequirements]()},
		{Name: "Config", Type: reflect.TypeFor[Config]()},
		{Name: "ConfigBatchWriteParams", Type: reflect.TypeFor[ConfigBatchWriteParams]()},
		{Name: "ConfigEdit", Type: reflect.TypeFor[ConfigEdit]()},
		{Name: "ConfigLayer", Type: reflect.TypeFor[ConfigLayer]()},
		{Name: "ConfigLayerMetadata", Type: reflect.TypeFor[ConfigLayerMetadata]()},
		{Name: "ConfigLayerSource", Type: reflect.TypeFor[ConfigLayerSource]()},
		{Name: "ConfigReadParams", Type: reflect.TypeFor[ConfigReadParams]()},
		{Name: "ConfigReadResponse", Type: reflect.TypeFor[ConfigReadResponse]()},
		{Name: "ConfigRequirements", Type: reflect.TypeFor[ConfigRequirements]()},
		{Name: "ConfigRequirementsReadResponse", Type: reflect.TypeFor[ConfigRequirementsReadResponse]()},
		{Name: "ConfigValueWriteParams", Type: reflect.TypeFor[ConfigValueWriteParams]()},
		{Name: "ConfigWarningNotification", Type: reflect.TypeFor[ConfigWarningNotification]()},
		{Name: "ConfigWriteResponse", Type: reflect.TypeFor[ConfigWriteResponse]()},
		{Name: "ConfiguredHookHandler", Type: reflect.TypeFor[ConfiguredHookHandler]()},
		{Name: "ConfiguredHookMatcherGroup", Type: reflect.TypeFor[ConfiguredHookMatcherGroup]()},
		{Name: "ContentItem", Type: reflect.TypeFor[ContentItem]()},
		{Name: "ConversationTextRole", Type: reflect.TypeFor[ConversationTextRole]()},
		{Name: "CurrentTimeReadParams", Type: reflect.TypeFor[CurrentTimeReadParams]()},
		{Name: "CurrentTimeReadResponse", Type: reflect.TypeFor[CurrentTimeReadResponse]()},
		{Name: "ConsumeAccountRateLimitResetCreditOutcome", Type: reflect.TypeFor[ConsumeAccountRateLimitResetCreditOutcome]()},
		{Name: "ConsumeAccountRateLimitResetCreditParams", Type: reflect.TypeFor[ConsumeAccountRateLimitResetCreditParams]()},
		{Name: "ConsumeAccountRateLimitResetCreditResponse", Type: reflect.TypeFor[ConsumeAccountRateLimitResetCreditResponse]()},
		{Name: "ContextCompactionItem", Type: reflect.TypeFor[ContextCompactionItem]()},
		{Name: "CreditsSnapshot", Type: reflect.TypeFor[CreditsSnapshot]()},
		{Name: "DaemonShutdownParams", Type: reflect.TypeFor[DaemonShutdownParams]()},
		{Name: "DaemonShutdownState", Type: reflect.TypeFor[DaemonShutdownState]()},
		{Name: "DaemonStartResult", Type: reflect.TypeFor[DaemonStartResult]()},
		{Name: "DaemonStatus", Type: reflect.TypeFor[DaemonStatus]()},
		{Name: "DaemonStopResult", Type: reflect.TypeFor[DaemonStopResult]()},
		{Name: "DaemonVersion", Type: reflect.TypeFor[DaemonVersion]()},
		{Name: "DeprecationNoticeNotification", Type: reflect.TypeFor[DeprecationNoticeNotification]()},
		{Name: "DynamicToolCallContentItem", Type: reflect.TypeFor[DynamicToolCallContentItem]()},
		{Name: "DynamicToolCallOutputContentItem", Type: reflect.TypeFor[DynamicToolCallOutputContentItem]()},
		{Name: "DynamicToolCallParams", Type: reflect.TypeFor[DynamicToolCallParams]()},
		{Name: "DynamicToolCallResponse", Type: reflect.TypeFor[DynamicToolCallResponse]()},
		{Name: "DynamicToolCallItem", Type: reflect.TypeFor[DynamicToolCallItem]()},
		{Name: "DynamicToolCallItemCompletedNotificationParams", Type: reflect.TypeFor[DynamicToolCallItemCompletedNotificationParams]()},
		{Name: "DynamicToolCallItemStartedNotificationParams", Type: reflect.TypeFor[DynamicToolCallItemStartedNotificationParams]()},
		{Name: "DynamicToolFunctionSpec", Type: reflect.TypeFor[DynamicToolFunctionSpec]()},
		{Name: "DynamicToolNamespaceSpec", Type: reflect.TypeFor[DynamicToolNamespaceSpec]()},
		{Name: "DynamicToolNamespaceTool", Type: reflect.TypeFor[DynamicToolNamespaceTool]()},
		{Name: "DynamicToolSpec", Type: reflect.TypeFor[DynamicToolSpec]()},
		{Name: "ErrorNotification", Type: reflect.TypeFor[ErrorNotification]()},
		{Name: "ExecPolicyAmendment", Type: reflect.TypeFor[ExecPolicyAmendment]()},
		{Name: "ExperimentalFeature", Type: reflect.TypeFor[ExperimentalFeature]()},
		{Name: "ExperimentalFeatureEnablementSetParams", Type: reflect.TypeFor[ExperimentalFeatureEnablementSetParams]()},
		{Name: "ExperimentalFeatureEnablementSetResponse", Type: reflect.TypeFor[ExperimentalFeatureEnablementSetResponse]()},
		{Name: "ExperimentalFeatureListParams", Type: reflect.TypeFor[ExperimentalFeatureListParams]()},
		{Name: "ExperimentalFeatureListResponse", Type: reflect.TypeFor[ExperimentalFeatureListResponse]()},
		{Name: "ExperimentalFeatureStage", Type: reflect.TypeFor[ExperimentalFeatureStage]()},
		{Name: "FeedbackUploadParams", Type: reflect.TypeFor[FeedbackUploadParams]()},
		{Name: "FileChange", Type: reflect.TypeFor[FileChange]()},
		{Name: "FileChangeOutputDeltaNotification", Type: reflect.TypeFor[FileChangeOutputDeltaNotification]()},
		{Name: "FileChangeApprovalRequestParams", Type: reflect.TypeFor[FileChangeApprovalRequestParams]()},
		{Name: "FileChangeApprovalDecision", Type: reflect.TypeFor[FileChangeApprovalDecision]()},
		{Name: "FileChangeRequestApprovalParams", Type: reflect.TypeFor[FileChangeRequestApprovalParams]()},
		{Name: "FileChangeRequestApprovalResponse", Type: reflect.TypeFor[FileChangeRequestApprovalResponse]()},
		{Name: "FileChangeArtifactEvidence", Type: reflect.TypeFor[FileChangeArtifactEvidence]()},
		{Name: "FileChangeRevertParams", Type: reflect.TypeFor[FileChangeRevertParams]()},
		{Name: "FileChangeRevertResult", Type: reflect.TypeFor[FileChangeRevertResult]()},
		{Name: "FileChangeItem", Type: reflect.TypeFor[FileChangeItem]()},
		{Name: "FileChangeItemCompletedNotificationParams", Type: reflect.TypeFor[FileChangeItemCompletedNotificationParams]()},
		{Name: "FileChangeItemStartedNotificationParams", Type: reflect.TypeFor[FileChangeItemStartedNotificationParams]()},
		{Name: "FileChangePatchUpdatedNotificationParams", Type: reflect.TypeFor[FileChangePatchUpdatedNotificationParams]()},
		{Name: "FileUpdateChange", Type: reflect.TypeFor[FileUpdateChange]()},
		{Name: "FileSystemAccessMode", Type: reflect.TypeFor[FileSystemAccessMode]()},
		{Name: "FileSystemPath", Type: reflect.TypeFor[FileSystemPath]()},
		{Name: "FileSystemSandboxEntry", Type: reflect.TypeFor[FileSystemSandboxEntry]()},
		{Name: "FileSystemSpecialPath", Type: reflect.TypeFor[FileSystemSpecialPath]()},
		{Name: "FileChangedNotification", Type: reflect.TypeFor[FileChangedNotification]()},
		{Name: "ForcedChatgptWorkspaceIds", Type: reflect.TypeFor[ForcedChatgptWorkspaceIds]()},
		{Name: "ForcedLoginMethod", Type: reflect.TypeFor[ForcedLoginMethod]()},
		{Name: "FsChangedNotification", Type: reflect.TypeFor[FsChangedNotification]()},
		{Name: "FsCopyParams", Type: reflect.TypeFor[FsCopyParams]()},
		{Name: "FsCopyResponse", Type: reflect.TypeFor[FsCopyResponse]()},
		{Name: "FsCreateDirectoryParams", Type: reflect.TypeFor[FsCreateDirectoryParams]()},
		{Name: "FsCreateDirectoryResponse", Type: reflect.TypeFor[FsCreateDirectoryResponse]()},
		{Name: "FsGetMetadataParams", Type: reflect.TypeFor[FsGetMetadataParams]()},
		{Name: "FsGetMetadataResponse", Type: reflect.TypeFor[FsGetMetadataResponse]()},
		{Name: "GetAccountParams", Type: reflect.TypeFor[GetAccountParams]()},
		{Name: "GetAccountRateLimitsResponse", Type: reflect.TypeFor[GetAccountRateLimitsResponse]()},
		{Name: "GetAccountResponse", Type: reflect.TypeFor[GetAccountResponse]()},
		{Name: "GetAccountTokenUsageParams", Type: reflect.TypeFor[GetAccountTokenUsageParams]()},
		{Name: "GetAccountTokenUsageResponse", Type: reflect.TypeFor[GetAccountTokenUsageResponse]()},
		{Name: "GetWorkspaceMessagesResponse", Type: reflect.TypeFor[GetWorkspaceMessagesResponse]()},
		{Name: "FsReadDirectoryEntry", Type: reflect.TypeFor[FsReadDirectoryEntry]()},
		{Name: "FsReadDirectoryParams", Type: reflect.TypeFor[FsReadDirectoryParams]()},
		{Name: "FsReadDirectoryResponse", Type: reflect.TypeFor[FsReadDirectoryResponse]()},
		{Name: "FsReadFileParams", Type: reflect.TypeFor[FsReadFileParams]()},
		{Name: "FsReadFileResponse", Type: reflect.TypeFor[FsReadFileResponse]()},
		{Name: "FsRemoveParams", Type: reflect.TypeFor[FsRemoveParams]()},
		{Name: "FsRemoveResponse", Type: reflect.TypeFor[FsRemoveResponse]()},
		{Name: "FsUnwatchParams", Type: reflect.TypeFor[FsUnwatchParams]()},
		{Name: "FsUnwatchResponse", Type: reflect.TypeFor[FsUnwatchResponse]()},
		{Name: "FsWatchParams", Type: reflect.TypeFor[FsWatchParams]()},
		{Name: "FsWatchResponse", Type: reflect.TypeFor[FsWatchResponse]()},
		{Name: "FsWriteFileParams", Type: reflect.TypeFor[FsWriteFileParams]()},
		{Name: "FsWriteFileResponse", Type: reflect.TypeFor[FsWriteFileResponse]()},
		{Name: "FunctionCallOutputBody", Type: reflect.TypeFor[FunctionCallOutputBody]()},
		{Name: "FunctionCallOutputContentItem", Type: reflect.TypeFor[FunctionCallOutputContentItem]()},
		{Name: "GitInfo", Type: reflect.TypeFor[GitInfo]()},
		{Name: "GrantedPermissionProfile", Type: reflect.TypeFor[GrantedPermissionProfile]()},
		{Name: "GuardianWarningNotification", Type: reflect.TypeFor[GuardianWarningNotification]()},
		{Name: "HookPromptFragment", Type: reflect.TypeFor[HookPromptFragment]()},
		{Name: "ImageDetail", Type: reflect.TypeFor[ImageDetail]()},
		{Name: "ImageGenerationItem", Type: reflect.TypeFor[ImageGenerationItem]()},
		{Name: "ImplementationInfo", Type: reflect.TypeFor[ImplementationInfo]()},
		{Name: "InitializeCapabilities", Type: reflect.TypeFor[InitializeCapabilities]()},
		{Name: "InitializeParams", Type: reflect.TypeFor[InitializeParams]()},
		{Name: "InitializeResponse", Type: reflect.TypeFor[InitializeResponse]()},
		{Name: "InputModality", Type: reflect.TypeFor[InputModality]()},
		{Name: "InternalChatMessageMetadataPassthrough", Type: reflect.TypeFor[InternalChatMessageMetadataPassthrough]()},
		{Name: "ItemCompletedNotification", Type: reflect.TypeFor[ItemCompletedNotification]()},
		{Name: "JsonValue", Type: reflect.TypeFor[JsonValue]()},
		{Name: "JSONRPCError", Type: reflect.TypeFor[JSONRPCError]()},
		{Name: "JSONRPCErrorError", Type: reflect.TypeFor[JSONRPCErrorError]()},
		{Name: "JSONRPCMessage", Type: reflect.TypeFor[JSONRPCMessage]()},
		{Name: "JSONRPCNotification", Type: reflect.TypeFor[JSONRPCNotification]()},
		{Name: "JSONRPCRequest", Type: reflect.TypeFor[JSONRPCRequest]()},
		{Name: "JSONRPCResponse", Type: reflect.TypeFor[JSONRPCResponse]()},
		{Name: "ItemLifecycleNotificationParams", Type: reflect.TypeFor[ItemLifecycleNotificationParams]()},
		{Name: "ItemStartedNotification", Type: reflect.TypeFor[ItemStartedNotification]()},
		{Name: "LegacyAppPathString", Type: reflect.TypeFor[LegacyAppPathString]()},
		{Name: "ListMcpServerStatusParams", Type: reflect.TypeFor[ListMcpServerStatusParams]()},
		{Name: "ListMcpServerStatusResponse", Type: reflect.TypeFor[ListMcpServerStatusResponse]()},
		{Name: "LoginAccountParams", Type: reflect.TypeFor[LoginAccountParams]()},
		{Name: "LoginAccountResponse", Type: reflect.TypeFor[LoginAccountResponse]()},
		{Name: "LoginAppBrand", Type: reflect.TypeFor[LoginAppBrand]()},
		{Name: "LogoutAccountResponse", Type: reflect.TypeFor[LogoutAccountResponse]()},
		{Name: "LocalShellAction", Type: reflect.TypeFor[LocalShellAction]()},
		{Name: "LocalShellStatus", Type: reflect.TypeFor[LocalShellStatus]()},
		{Name: "MarketplaceAddParams", Type: reflect.TypeFor[MarketplaceAddParams]()},
		{Name: "MarketplaceRemoveParams", Type: reflect.TypeFor[MarketplaceRemoveParams]()},
		{Name: "MarketplaceUpgradeParams", Type: reflect.TypeFor[MarketplaceUpgradeParams]()},
		{Name: "ManagedHooksRequirements", Type: reflect.TypeFor[ManagedHooksRequirements]()},
		{Name: "MCPContent", Type: reflect.TypeFor[MCPContent]()},
		{Name: "MCPToolCallError", Type: reflect.TypeFor[MCPToolCallError]()},
		{Name: "MCPToolCallItem", Type: reflect.TypeFor[MCPToolCallItem]()},
		{Name: "MCPToolCallItemCompletedNotificationParams", Type: reflect.TypeFor[MCPToolCallItemCompletedNotificationParams]()},
		{Name: "MCPToolCallItemStartedNotificationParams", Type: reflect.TypeFor[MCPToolCallItemStartedNotificationParams]()},
		{Name: "MCPToolCallProgressNotificationParams", Type: reflect.TypeFor[MCPToolCallProgressNotificationParams]()},
		{Name: "MCPToolCallResult", Type: reflect.TypeFor[MCPToolCallResult]()},
		{Name: "McpAuthStatus", Type: reflect.TypeFor[McpAuthStatus]()},
		{Name: "McpResourceReadParams", Type: reflect.TypeFor[McpResourceReadParams]()},
		{Name: "McpResourceReadResponse", Type: reflect.TypeFor[McpResourceReadResponse]()},
		{Name: "McpServerInfo", Type: reflect.TypeFor[McpServerInfo]()},
		{Name: "McpServerMigration", Type: reflect.TypeFor[McpServerMigration]()},
		{Name: "McpServerOauthLoginCompletedNotification", Type: reflect.TypeFor[McpServerOauthLoginCompletedNotification]()},
		{Name: "McpServerOauthLoginParams", Type: reflect.TypeFor[McpServerOauthLoginParams]()},
		{Name: "McpServerOauthLoginResponse", Type: reflect.TypeFor[McpServerOauthLoginResponse]()},
		{Name: "McpServerRefreshResponse", Type: reflect.TypeFor[McpServerRefreshResponse]()},
		{Name: "McpServerToolCallParams", Type: reflect.TypeFor[McpServerToolCallParams]()},
		{Name: "McpServerToolCallResponse", Type: reflect.TypeFor[McpServerToolCallResponse]()},
		{Name: "McpServerStartupFailureReason", Type: reflect.TypeFor[McpServerStartupFailureReason]()},
		{Name: "McpServerStartupState", Type: reflect.TypeFor[McpServerStartupState]()},
		{Name: "McpServerStatus", Type: reflect.TypeFor[McpServerStatus]()},
		{Name: "McpServerStatusDetail", Type: reflect.TypeFor[McpServerStatusDetail]()},
		{Name: "McpServerStatusUpdatedNotification", Type: reflect.TypeFor[McpServerStatusUpdatedNotification]()},
		{Name: "McpToolCallAppContext", Type: reflect.TypeFor[McpToolCallAppContext]()},
		{Name: "McpToolCallResult", Type: reflect.TypeFor[McpToolCallResult]()},
		{Name: "MemoryCitation", Type: reflect.TypeFor[MemoryCitation]()},
		{Name: "MemoryCitationEntry", Type: reflect.TypeFor[MemoryCitationEntry]()},
		{Name: "MergeStrategy", Type: reflect.TypeFor[MergeStrategy]()},
		{Name: "MessagePhase", Type: reflect.TypeFor[MessagePhase]()},
		{Name: "Model", Type: reflect.TypeFor[Model]()},
		{Name: "ModelAvailabilityNux", Type: reflect.TypeFor[ModelAvailabilityNux]()},
		{Name: "ModelCatalogAvailabilityNux", Type: reflect.TypeFor[ModelCatalogAvailabilityNux]()},
		{Name: "ModelCatalogCapabilities", Type: reflect.TypeFor[ModelCatalogCapabilities]()},
		{Name: "ModelCatalogEntry", Type: reflect.TypeFor[ModelCatalogEntry]()},
		{Name: "ModelCatalogListParams", Type: reflect.TypeFor[ModelCatalogListParams]()},
		{Name: "ModelCatalogListResponse", Type: reflect.TypeFor[ModelCatalogListResponse]()},
		{Name: "ModelCatalogReasoningEffortOption", Type: reflect.TypeFor[ModelCatalogReasoningEffortOption]()},
		{Name: "ModelCatalogServiceTier", Type: reflect.TypeFor[ModelCatalogServiceTier]()},
		{Name: "ModelCatalogUpgradeInfo", Type: reflect.TypeFor[ModelCatalogUpgradeInfo]()},
		{Name: "ModelListParams", Type: reflect.TypeFor[ModelListParams]()},
		{Name: "ModelListResponse", Type: reflect.TypeFor[ModelListResponse]()},
		{Name: "ModelProviderCapabilitiesReadParams", Type: reflect.TypeFor[ModelProviderCapabilitiesReadParams]()},
		{Name: "ModelProviderCapabilitiesReadResponse", Type: reflect.TypeFor[ModelProviderCapabilitiesReadResponse]()},
		{Name: "ModelRerouteReason", Type: reflect.TypeFor[ModelRerouteReason]()},
		{Name: "ModelReroutedNotification", Type: reflect.TypeFor[ModelReroutedNotification]()},
		{Name: "ModelSafetyBufferingUpdatedNotification", Type: reflect.TypeFor[ModelSafetyBufferingUpdatedNotification]()},
		{Name: "ModelServiceTier", Type: reflect.TypeFor[ModelServiceTier]()},
		{Name: "ModelUpgradeInfo", Type: reflect.TypeFor[ModelUpgradeInfo]()},
		{Name: "ModelVerification", Type: reflect.TypeFor[ModelVerification]()},
		{Name: "ModelVerificationNotification", Type: reflect.TypeFor[ModelVerificationNotification]()},
		{Name: "ModelsRequirements", Type: reflect.TypeFor[ModelsRequirements]()},
		{Name: "ModeKind", Type: reflect.TypeFor[ModeKind]()},
		{Name: "MultiAgentMode", Type: reflect.TypeFor[MultiAgentMode]()},
		{Name: "NetworkDomainPermission", Type: reflect.TypeFor[NetworkDomainPermission]()},
		{Name: "NetworkRequirements", Type: reflect.TypeFor[NetworkRequirements]()},
		{Name: "NetworkUnixSocketPermission", Type: reflect.TypeFor[NetworkUnixSocketPermission]()},
		{Name: "NewThreadModelDefaults", Type: reflect.TypeFor[NewThreadModelDefaults]()},
		{Name: "NonSteerableTurnKind", Type: reflect.TypeFor[NonSteerableTurnKind]()},
		{Name: "OperationalListParams", Type: reflect.TypeFor[OperationalListParams]()},
		{Name: "McpElicitationArrayType", Type: reflect.TypeFor[McpElicitationArrayType]()},
		{Name: "McpElicitationBooleanSchema", Type: reflect.TypeFor[McpElicitationBooleanSchema]()},
		{Name: "McpElicitationBooleanType", Type: reflect.TypeFor[McpElicitationBooleanType]()},
		{Name: "McpElicitationConstOption", Type: reflect.TypeFor[McpElicitationConstOption]()},
		{Name: "McpElicitationEnumSchema", Type: reflect.TypeFor[McpElicitationEnumSchema]()},
		{Name: "McpElicitationLegacyTitledEnumSchema", Type: reflect.TypeFor[McpElicitationLegacyTitledEnumSchema]()},
		{Name: "McpElicitationMultiSelectEnumSchema", Type: reflect.TypeFor[McpElicitationMultiSelectEnumSchema]()},
		{Name: "McpElicitationNumberSchema", Type: reflect.TypeFor[McpElicitationNumberSchema]()},
		{Name: "McpElicitationNumberType", Type: reflect.TypeFor[McpElicitationNumberType]()},
		{Name: "McpElicitationObjectType", Type: reflect.TypeFor[McpElicitationObjectType]()},
		{Name: "McpElicitationPrimitiveSchema", Type: reflect.TypeFor[McpElicitationPrimitiveSchema]()},
		{Name: "McpElicitationSchema", Type: reflect.TypeFor[McpElicitationSchema]()},
		{Name: "McpElicitationSingleSelectEnumSchema", Type: reflect.TypeFor[McpElicitationSingleSelectEnumSchema]()},
		{Name: "McpElicitationStringFormat", Type: reflect.TypeFor[McpElicitationStringFormat]()},
		{Name: "McpElicitationStringSchema", Type: reflect.TypeFor[McpElicitationStringSchema]()},
		{Name: "McpElicitationStringType", Type: reflect.TypeFor[McpElicitationStringType]()},
		{Name: "McpElicitationTitledEnumItems", Type: reflect.TypeFor[McpElicitationTitledEnumItems]()},
		{Name: "McpElicitationTitledMultiSelectEnumSchema", Type: reflect.TypeFor[McpElicitationTitledMultiSelectEnumSchema]()},
		{Name: "McpElicitationTitledSingleSelectEnumSchema", Type: reflect.TypeFor[McpElicitationTitledSingleSelectEnumSchema]()},
		{Name: "McpElicitationUntitledEnumItems", Type: reflect.TypeFor[McpElicitationUntitledEnumItems]()},
		{Name: "McpElicitationUntitledMultiSelectEnumSchema", Type: reflect.TypeFor[McpElicitationUntitledMultiSelectEnumSchema]()},
		{Name: "McpElicitationUntitledSingleSelectEnumSchema", Type: reflect.TypeFor[McpElicitationUntitledSingleSelectEnumSchema]()},
		{Name: "McpServerElicitationAction", Type: reflect.TypeFor[McpServerElicitationAction]()},
		{Name: "McpServerElicitationRequestParams", Type: reflect.TypeFor[McpServerElicitationRequestParams]()},
		{Name: "McpServerElicitationRequestResponse", Type: reflect.TypeFor[McpServerElicitationRequestResponse]()},
		{Name: "MethodInfo", Type: reflect.TypeFor[MethodInfo]()},
		{Name: "MethodState", Type: reflect.TypeFor[MethodState]()},
		{Name: "NetworkAccess", Type: reflect.TypeFor[NetworkAccess]()},
		{Name: "NetworkPolicyAmendment", Type: reflect.TypeFor[NetworkPolicyAmendment]()},
		{Name: "NetworkPolicyRuleAction", Type: reflect.TypeFor[NetworkPolicyRuleAction]()},
		{Name: "OverriddenMetadata", Type: reflect.TypeFor[OverriddenMetadata]()},
		{Name: "PatchApplyStatus", Type: reflect.TypeFor[PatchApplyStatus]()},
		{Name: "PatchChangeKind", Type: reflect.TypeFor[PatchChangeKind]()},
		{Name: "Personality", Type: reflect.TypeFor[Personality]()},
		{Name: "PlanDeltaNotification", Type: reflect.TypeFor[PlanDeltaNotification]()},
		{Name: "PlanType", Type: reflect.TypeFor[PlanType]()},
		{Name: "PluginShareCheckoutParams", Type: reflect.TypeFor[PluginShareCheckoutParams]()},
		{Name: "PluginShareDeleteParams", Type: reflect.TypeFor[PluginShareDeleteParams]()},
		{Name: "PluginShareListParams", Type: reflect.TypeFor[PluginShareListParams]()},
		{Name: "PluginShareDiscoverability", Type: reflect.TypeFor[PluginShareDiscoverability]()},
		{Name: "PluginSharePrincipalType", Type: reflect.TypeFor[PluginSharePrincipalType]()},
		{Name: "PluginShareTargetRole", Type: reflect.TypeFor[PluginShareTargetRole]()},
		{Name: "PluginShareTarget", Type: reflect.TypeFor[PluginShareTarget]()},
		{Name: "PluginShareSaveParams", Type: reflect.TypeFor[PluginShareSaveParams]()},
		{Name: "PluginShareUpdateDiscoverability", Type: reflect.TypeFor[PluginShareUpdateDiscoverability]()},
		{Name: "PluginShareUpdateTargetsParams", Type: reflect.TypeFor[PluginShareUpdateTargetsParams]()},
		{Name: "PluginListMarketplaceKind", Type: reflect.TypeFor[PluginListMarketplaceKind]()},
		{Name: "PluginListParams", Type: reflect.TypeFor[PluginListParams]()},
		{Name: "PluginReadParams", Type: reflect.TypeFor[PluginReadParams]()},
		{Name: "PluginInstallParams", Type: reflect.TypeFor[PluginInstallParams]()},
		{Name: "PluginInstalledParams", Type: reflect.TypeFor[PluginInstalledParams]()},
		{Name: "PluginSkillReadParams", Type: reflect.TypeFor[PluginSkillReadParams]()},
		{Name: "PluginUninstallParams", Type: reflect.TypeFor[PluginUninstallParams]()},
		{Name: "PluginsMigration", Type: reflect.TypeFor[PluginsMigration]()},
		{Name: "ProcessExitedNotification", Type: reflect.TypeFor[ProcessExitedNotification]()},
		{Name: "ProcessOutputDeltaNotification", Type: reflect.TypeFor[ProcessOutputDeltaNotification]()},
		{Name: "ProcessOutputStream", Type: reflect.TypeFor[ProcessOutputStream]()},
		{Name: "ProcessTerminalSize", Type: reflect.TypeFor[ProcessTerminalSize]()},
		{Name: "RateLimitReachedType", Type: reflect.TypeFor[RateLimitReachedType]()},
		{Name: "RateLimitResetCredit", Type: reflect.TypeFor[RateLimitResetCredit]()},
		{Name: "RateLimitResetCreditsSummary", Type: reflect.TypeFor[RateLimitResetCreditsSummary]()},
		{Name: "RateLimitResetCreditStatus", Type: reflect.TypeFor[RateLimitResetCreditStatus]()},
		{Name: "RateLimitResetType", Type: reflect.TypeFor[RateLimitResetType]()},
		{Name: "RateLimitSnapshot", Type: reflect.TypeFor[RateLimitSnapshot]()},
		{Name: "RateLimitWindow", Type: reflect.TypeFor[RateLimitWindow]()},
		{Name: "SkillMigration", Type: reflect.TypeFor[SkillMigration]()},
		{Name: "SkillsConfigWriteParams", Type: reflect.TypeFor[SkillsConfigWriteParams]()},
		{Name: "SkillsExtraRootsSetParams", Type: reflect.TypeFor[SkillsExtraRootsSetParams]()},
		{Name: "SkillsListParams", Type: reflect.TypeFor[SkillsListParams]()},
		{Name: "SpendControlLimitSnapshot", Type: reflect.TypeFor[SpendControlLimitSnapshot]()},
		{Name: "SessionMigration", Type: reflect.TypeFor[SessionMigration]()},
		{Name: "HookMigration", Type: reflect.TypeFor[HookMigration]()},
		{Name: "SubagentMigration", Type: reflect.TypeFor[SubagentMigration]()},
		{Name: "CommandMigration", Type: reflect.TypeFor[CommandMigration]()},
		{Name: "MigrationDetails", Type: reflect.TypeFor[MigrationDetails]()},
		{Name: "ExternalAgentConfigMigrationItemType", Type: reflect.TypeFor[ExternalAgentConfigMigrationItemType]()},
		{Name: "ExternalAgentConfigMigrationItem", Type: reflect.TypeFor[ExternalAgentConfigMigrationItem]()},
		{Name: "ExternalAgentConfigDetectParams", Type: reflect.TypeFor[ExternalAgentConfigDetectParams]()},
		{Name: "ExternalAgentConfigDetectResponse", Type: reflect.TypeFor[ExternalAgentConfigDetectResponse]()},
		{Name: "ExternalAgentConfigImportParams", Type: reflect.TypeFor[ExternalAgentConfigImportParams]()},
		{Name: "ExternalAgentConfigImportResponse", Type: reflect.TypeFor[ExternalAgentConfigImportResponse]()},
		{Name: "ExternalAgentConfigImportItemTypeFailure", Type: reflect.TypeFor[ExternalAgentConfigImportItemTypeFailure]()},
		{Name: "ExternalAgentConfigImportItemTypeSuccess", Type: reflect.TypeFor[ExternalAgentConfigImportItemTypeSuccess]()},
		{Name: "ExternalAgentConfigImportTypeResult", Type: reflect.TypeFor[ExternalAgentConfigImportTypeResult]()},
		{Name: "ExternalAgentConfigImportHistoryRecordSuccessParams", Type: reflect.TypeFor[ExternalAgentConfigImportHistoryRecordSuccessParams]()},
		{Name: "ExternalAgentConfigImportHistoryRecordTypeResultParams", Type: reflect.TypeFor[ExternalAgentConfigImportHistoryRecordTypeResultParams]()},
		{Name: "ExternalAgentConfigImportHistoryRecordParams", Type: reflect.TypeFor[ExternalAgentConfigImportHistoryRecordParams]()},
		{Name: "ExternalAgentConfigImportHistory", Type: reflect.TypeFor[ExternalAgentConfigImportHistory]()},
		{Name: "ExternalAgentConfigImportHistoriesReadResponse", Type: reflect.TypeFor[ExternalAgentConfigImportHistoriesReadResponse]()},
		{Name: "ExternalAgentConfigImportProgressNotification", Type: reflect.TypeFor[ExternalAgentConfigImportProgressNotification]()},
		{Name: "ExternalAgentConfigImportCompletedNotification", Type: reflect.TypeFor[ExternalAgentConfigImportCompletedNotification]()},
		{Name: "ExecCommandApprovalParams", Type: reflect.TypeFor[ExecCommandApprovalParams]()},
		{Name: "ExecCommandApprovalResponse", Type: reflect.TypeFor[ExecCommandApprovalResponse]()},
		{Name: "FuzzyFileSearchMatchType", Type: reflect.TypeFor[FuzzyFileSearchMatchType]()},
		{Name: "FuzzyFileSearchParams", Type: reflect.TypeFor[FuzzyFileSearchParams]()},
		{Name: "FuzzyFileSearchResult", Type: reflect.TypeFor[FuzzyFileSearchResult]()},
		{Name: "FuzzyFileSearchResponse", Type: reflect.TypeFor[FuzzyFileSearchResponse]()},
		{Name: "FuzzyFileSearchSessionUpdatedNotification", Type: reflect.TypeFor[FuzzyFileSearchSessionUpdatedNotification]()},
		{Name: "FuzzyFileSearchSessionCompletedNotification", Type: reflect.TypeFor[FuzzyFileSearchSessionCompletedNotification]()},
		{Name: "GitStatusResponse", Type: reflect.TypeFor[GitStatusResponse]()},
		{Name: "GitStatusEntry", Type: reflect.TypeFor[GitStatusEntry]()},
		{Name: "GitStatusSnapshot", Type: reflect.TypeFor[GitStatusSnapshot]()},
		{Name: "GitWorktree", Type: reflect.TypeFor[GitWorktree]()},
		{Name: "GitWorktreeListResponse", Type: reflect.TypeFor[GitWorktreeListResponse]()},
		{Name: "GuardianApprovalReview", Type: reflect.TypeFor[GuardianApprovalReview]()},
		{Name: "GuardianApprovalReviewAction", Type: reflect.TypeFor[GuardianApprovalReviewAction]()},
		{Name: "GuardianApprovalReviewStatus", Type: reflect.TypeFor[GuardianApprovalReviewStatus]()},
		{Name: "GuardianCommandSource", Type: reflect.TypeFor[GuardianCommandSource]()},
		{Name: "GuardianRiskLevel", Type: reflect.TypeFor[GuardianRiskLevel]()},
		{Name: "GuardianUserAuthorization", Type: reflect.TypeFor[GuardianUserAuthorization]()},
		{Name: "HookEventName", Type: reflect.TypeFor[HookEventName]()},
		{Name: "HookExecutionMode", Type: reflect.TypeFor[HookExecutionMode]()},
		{Name: "HookHandlerType", Type: reflect.TypeFor[HookHandlerType]()},
		{Name: "HookOutputEntryKind", Type: reflect.TypeFor[HookOutputEntryKind]()},
		{Name: "HookRunStatus", Type: reflect.TypeFor[HookRunStatus]()},
		{Name: "HookScope", Type: reflect.TypeFor[HookScope]()},
		{Name: "HookSource", Type: reflect.TypeFor[HookSource]()},
		{Name: "HookTrustStatus", Type: reflect.TypeFor[HookTrustStatus]()},
		{Name: "HookOutputEntry", Type: reflect.TypeFor[HookOutputEntry]()},
		{Name: "HookRunSummary", Type: reflect.TypeFor[HookRunSummary]()},
		{Name: "HookStartedNotification", Type: reflect.TypeFor[HookStartedNotification]()},
		{Name: "HookCompletedNotification", Type: reflect.TypeFor[HookCompletedNotification]()},
		{Name: "HookErrorInfo", Type: reflect.TypeFor[HookErrorInfo]()},
		{Name: "HookMetadata", Type: reflect.TypeFor[HookMetadata]()},
		{Name: "HooksListEntry", Type: reflect.TypeFor[HooksListEntry]()},
		{Name: "HooksListParams", Type: reflect.TypeFor[HooksListParams]()},
		{Name: "HooksListResponse", Type: reflect.TypeFor[HooksListResponse]()},
		{Name: "ItemGuardianApprovalReviewCompletedNotification", Type: reflect.TypeFor[ItemGuardianApprovalReviewCompletedNotification]()},
		{Name: "ItemGuardianApprovalReviewStartedNotification", Type: reflect.TypeFor[ItemGuardianApprovalReviewStartedNotification]()},
		{Name: "NetworkApprovalContext", Type: reflect.TypeFor[NetworkApprovalContext]()},
		{Name: "NetworkApprovalProtocol", Type: reflect.TypeFor[NetworkApprovalProtocol]()},
		{Name: "ParsedCommand", Type: reflect.TypeFor[ParsedCommand]()},
		{Name: "PermissionProfileListParams", Type: reflect.TypeFor[PermissionProfileListParams]()},
		{Name: "PermissionProfileListResponse", Type: reflect.TypeFor[PermissionProfileListResponse]()},
		{Name: "PermissionProfileSummary", Type: reflect.TypeFor[PermissionProfileSummary]()},
		{Name: "PermissionGrantScope", Type: reflect.TypeFor[PermissionGrantScope]()},
		{Name: "PermissionsApprovalRequestParams", Type: reflect.TypeFor[PermissionsApprovalRequestParams]()},
		{Name: "PermissionsRequestApprovalParams", Type: reflect.TypeFor[PermissionsRequestApprovalParams]()},
		{Name: "PermissionsRequestApprovalResponse", Type: reflect.TypeFor[PermissionsRequestApprovalResponse]()},
		{Name: "ProviderCatalogCapabilities", Type: reflect.TypeFor[ProviderCatalogCapabilities]()},
		{Name: "ProviderCatalogEntry", Type: reflect.TypeFor[ProviderCatalogEntry]()},
		{Name: "ProviderHealthProbeParams", Type: reflect.TypeFor[ProviderHealthProbeParams]()},
		{Name: "ProviderHealthProbeResponse", Type: reflect.TypeFor[ProviderHealthProbeResponse]()},
		{Name: "ProviderHealthProbeStatus", Type: reflect.TypeFor[ProviderHealthProbeStatus]()},
		{Name: "ProviderListParams", Type: reflect.TypeFor[ProviderListParams]()},
		{Name: "ProviderListResponse", Type: reflect.TypeFor[ProviderListResponse]()},
		{Name: "RequestPermissionProfile", Type: reflect.TypeFor[RequestPermissionProfile]()},
		{Name: "ReasoningEffort", Type: reflect.TypeFor[ReasoningEffort]()},
		{Name: "ReasoningEffortOption", Type: reflect.TypeFor[ReasoningEffortOption]()},
		{Name: "ReasoningSummary", Type: reflect.TypeFor[ReasoningSummary]()},
		{Name: "ReasoningSummaryPartAddedNotification", Type: reflect.TypeFor[ReasoningSummaryPartAddedNotification]()},
		{Name: "ReasoningSummaryTextDeltaNotification", Type: reflect.TypeFor[ReasoningSummaryTextDeltaNotification]()},
		{Name: "ReasoningTextDeltaNotification", Type: reflect.TypeFor[ReasoningTextDeltaNotification]()},
		{Name: "ReasoningItemContent", Type: reflect.TypeFor[ReasoningItemContent]()},
		{Name: "ReasoningItemReasoningSummary", Type: reflect.TypeFor[ReasoningItemReasoningSummary]()},
		{Name: "ResidencyRequirement", Type: reflect.TypeFor[ResidencyRequirement]()},
		{Name: "RawResponseItemCompletedNotification", Type: reflect.TypeFor[RawResponseItemCompletedNotification]()},
		{Name: "Resource", Type: reflect.TypeFor[Resource]()},
		{Name: "ResourceContent", Type: reflect.TypeFor[ResourceContent]()},
		{Name: "ResourceTemplate", Type: reflect.TypeFor[ResourceTemplate]()},
		{Name: "ResponseItem", Type: reflect.TypeFor[ResponseItem]()},
		{Name: "ReviewDelivery", Type: reflect.TypeFor[ReviewDelivery]()},
		{Name: "ReviewDecision", Type: reflect.TypeFor[ReviewDecision]()},
		{Name: "ReviewStartParams", Type: reflect.TypeFor[ReviewStartParams]()},
		{Name: "ReviewStartResponse", Type: reflect.TypeFor[ReviewStartResponse]()},
		{Name: "ReviewTarget", Type: reflect.TypeFor[ReviewTarget]()},
		{Name: "ResponsesApiWebSearchAction", Type: reflect.TypeFor[ResponsesApiWebSearchAction]()},
		{Name: "RuntimeDeltaNotification", Type: reflect.TypeFor[RuntimeDeltaNotification]()},
		{Name: "RuntimeModelParams", Type: reflect.TypeFor[RuntimeModelParams]()},
		{Name: "RuntimeThreadNotification", Type: reflect.TypeFor[RuntimeThreadNotification]()},
		{Name: "RuntimeTurnNotification", Type: reflect.TypeFor[RuntimeTurnNotification]()},
		{Name: "SandboxMode", Type: reflect.TypeFor[SandboxMode]()},
		{Name: "SandboxPolicy", Type: reflect.TypeFor[SandboxPolicy]()},
		{Name: "SandboxWorkspaceWrite", Type: reflect.TypeFor[SandboxWorkspaceWrite]()},
		{Name: "SelectedCapabilityRoot", Type: reflect.TypeFor[SelectedCapabilityRoot]()},
		{Name: "SendAddCreditsNudgeEmailParams", Type: reflect.TypeFor[SendAddCreditsNudgeEmailParams]()},
		{Name: "SendAddCreditsNudgeEmailResponse", Type: reflect.TypeFor[SendAddCreditsNudgeEmailResponse]()},
		{Name: "ServerRequest", Type: reflect.TypeFor[ServerRequest]()},
		{Name: "ServerNotification", Type: reflect.TypeFor[ServerNotification]()},
		{Name: "ServerRequestResolvedNotificationParams", Type: reflect.TypeFor[ServerRequestResolvedNotificationParams]()},
		{Name: "ServerCapabilities", Type: reflect.TypeFor[ServerCapabilities]()},
		{Name: "SessionSource", Type: reflect.TypeFor[SessionSource]()},
		{Name: "Settings", Type: reflect.TypeFor[Settings]()},
		{Name: "Surface", Type: reflect.TypeFor[Surface]()},
		{Name: "SortDirection", Type: reflect.TypeFor[SortDirection]()},
		{Name: "SubAgentActivityKind", Type: reflect.TypeFor[SubAgentActivityKind]()},
		{Name: "SubAgentSource", Type: reflect.TypeFor[SubAgentSource]()},
		{Name: "TerminalInteractionNotification", Type: reflect.TypeFor[TerminalInteractionNotification]()},
		{Name: "Thread", Type: reflect.TypeFor[Thread]()},
		{Name: "ThreadActiveFlag", Type: reflect.TypeFor[ThreadActiveFlag]()},
		{Name: "ThreadApproveGuardianDeniedActionParams", Type: reflect.TypeFor[ThreadApproveGuardianDeniedActionParams]()},
		{Name: "ThreadApproveGuardianDeniedActionResponse", Type: reflect.TypeFor[ThreadApproveGuardianDeniedActionResponse]()},
		{Name: "ThreadArchiveParams", Type: reflect.TypeFor[ThreadArchiveParams]()},
		{Name: "ThreadArchiveResponse", Type: reflect.TypeFor[ThreadArchiveResponse]()},
		{Name: "ThreadArchivedNotification", Type: reflect.TypeFor[ThreadArchivedNotification]()},
		{Name: "ThreadClosedNotification", Type: reflect.TypeFor[ThreadClosedNotification]()},
		{Name: "ThreadCompactStartParams", Type: reflect.TypeFor[ThreadCompactStartParams]()},
		{Name: "ThreadCompactStartResponse", Type: reflect.TypeFor[ThreadCompactStartResponse]()},
		{Name: "ThreadCompactedNotificationParams", Type: reflect.TypeFor[ThreadCompactedNotificationParams]()},
		{Name: "ThreadDeleteParams", Type: reflect.TypeFor[ThreadDeleteParams]()},
		{Name: "ThreadDeleteResponse", Type: reflect.TypeFor[ThreadDeleteResponse]()},
		{Name: "ThreadDeletedNotification", Type: reflect.TypeFor[ThreadDeletedNotification]()},
		{Name: "ThreadExtra", Type: reflect.TypeFor[ThreadExtra]()},
		{Name: "ThreadForkParams", Type: reflect.TypeFor[ThreadForkParams]()},
		{Name: "ThreadForkResponse", Type: reflect.TypeFor[ThreadForkResponse]()},
		{Name: "ThreadGoal", Type: reflect.TypeFor[ThreadGoal]()},
		{Name: "ThreadGoalClearParams", Type: reflect.TypeFor[ThreadGoalClearParams]()},
		{Name: "ThreadGoalClearResponse", Type: reflect.TypeFor[ThreadGoalClearResponse]()},
		{Name: "ThreadGoalClearedNotification", Type: reflect.TypeFor[ThreadGoalClearedNotification]()},
		{Name: "ThreadGoalGetParams", Type: reflect.TypeFor[ThreadGoalGetParams]()},
		{Name: "ThreadGoalGetResponse", Type: reflect.TypeFor[ThreadGoalGetResponse]()},
		{Name: "ThreadGoalSetParams", Type: reflect.TypeFor[ThreadGoalSetParams]()},
		{Name: "ThreadGoalSetResponse", Type: reflect.TypeFor[ThreadGoalSetResponse]()},
		{Name: "ThreadGoalStatus", Type: reflect.TypeFor[ThreadGoalStatus]()},
		{Name: "ThreadGoalUpdatedNotification", Type: reflect.TypeFor[ThreadGoalUpdatedNotification]()},
		{Name: "ThreadHistoryForkParams", Type: reflect.TypeFor[ThreadHistoryForkParams]()},
		{Name: "ThreadHistoryForkResult", Type: reflect.TypeFor[ThreadHistoryForkResult]()},
		{Name: "ThreadHistoryMode", Type: reflect.TypeFor[ThreadHistoryMode]()},
		{Name: "ThreadHistoryRollbackParams", Type: reflect.TypeFor[ThreadHistoryRollbackParams]()},
		{Name: "ThreadHistoryRollbackRecord", Type: reflect.TypeFor[ThreadHistoryRollbackRecord]()},
		{Name: "ThreadHistoryRollbackResult", Type: reflect.TypeFor[ThreadHistoryRollbackResult]()},
		{Name: "ThreadInjectItemsParams", Type: reflect.TypeFor[ThreadInjectItemsParams]()},
		{Name: "ThreadInjectItemsResponse", Type: reflect.TypeFor[ThreadInjectItemsResponse]()},
		{Name: "ThreadRollbackParams", Type: reflect.TypeFor[ThreadRollbackParams]()},
		{Name: "ThreadRollbackResponse", Type: reflect.TypeFor[ThreadRollbackResponse]()},
		{Name: "ThreadSearchResult", Type: reflect.TypeFor[ThreadSearchResult]()},
		{Name: "ThreadSectionAppearance", Type: reflect.TypeFor[ThreadSectionAppearance]()},
		{Name: "ThreadSectionCreateParams", Type: reflect.TypeFor[ThreadSectionCreateParams]()},
		{Name: "ThreadSectionDeleteParams", Type: reflect.TypeFor[ThreadSectionDeleteParams]()},
		{Name: "ThreadSectionListParams", Type: reflect.TypeFor[ThreadSectionListParams]()},
		{Name: "ThreadSectionMoveParams", Type: reflect.TypeFor[ThreadSectionMoveParams]()},
		{Name: "ThreadSectionUpdateParams", Type: reflect.TypeFor[ThreadSectionUpdateParams]()},
		{Name: "ThreadShellCommandParams", Type: reflect.TypeFor[ThreadShellCommandParams]()},
		{Name: "ThreadShellCommandResponse", Type: reflect.TypeFor[ThreadShellCommandResponse]()},
		{Name: "ThreadId", Type: reflect.TypeFor[ThreadId]()},
		{Name: "ThreadLifecycleStatus", Type: reflect.TypeFor[ThreadLifecycleStatus]()},
		{Name: "ThreadListCwdFilter", Type: reflect.TypeFor[ThreadListCwdFilter]()},
		{Name: "ThreadListParams", Type: reflect.TypeFor[ThreadListParams]()},
		{Name: "ThreadListResponse", Type: reflect.TypeFor[ThreadListResponse]()},
		{Name: "ThreadListResult", Type: reflect.TypeFor[ThreadListResult]()},
		{Name: "ThreadLoadedListParams", Type: reflect.TypeFor[ThreadLoadedListParams]()},
		{Name: "ThreadLoadedListResponse", Type: reflect.TypeFor[ThreadLoadedListResponse]()},
		{Name: "ThreadMemoryMode", Type: reflect.TypeFor[ThreadMemoryMode]()},
		{Name: "ThreadMemoryModeSetParams", Type: reflect.TypeFor[ThreadMemoryModeSetParams]()},
		{Name: "ThreadMemoryModeSetResponse", Type: reflect.TypeFor[ThreadMemoryModeSetResponse]()},
		{Name: "ThreadMetadataGitInfoUpdateParams", Type: reflect.TypeFor[ThreadMetadataGitInfoUpdateParams]()},
		{Name: "ThreadMetadataUpdateParams", Type: reflect.TypeFor[ThreadMetadataUpdateParams]()},
		{Name: "ThreadMetadataUpdateResponse", Type: reflect.TypeFor[ThreadMetadataUpdateResponse]()},
		{Name: "ThreadMetadataUpdateResult", Type: reflect.TypeFor[ThreadMetadataUpdateResult]()},
		{Name: "ThreadNameUpdatedNotification", Type: reflect.TypeFor[ThreadNameUpdatedNotification]()},
		{Name: "ThreadReadParams", Type: reflect.TypeFor[ThreadReadParams]()},
		{Name: "ThreadReadResponse", Type: reflect.TypeFor[ThreadReadResponse]()},
		{Name: "ThreadReadResult", Type: reflect.TypeFor[ThreadReadResult]()},
		{Name: "ThreadRecord", Type: reflect.TypeFor[ThreadRecord]()},
		{Name: "ThreadRunStartParams", Type: reflect.TypeFor[ThreadRunStartParams]()},
		{Name: "ThreadRunStartResult", Type: reflect.TypeFor[ThreadRunStartResult]()},
		{Name: "ThreadResumeParams", Type: reflect.TypeFor[ThreadResumeParams]()},
		{Name: "ThreadResumeInitialTurnsPageParams", Type: reflect.TypeFor[ThreadResumeInitialTurnsPageParams]()},
		{Name: "ThreadResumeResponse", Type: reflect.TypeFor[ThreadResumeResponse]()},
		{Name: "ThreadSetNameParams", Type: reflect.TypeFor[ThreadSetNameParams]()},
		{Name: "ThreadSetNameResponse", Type: reflect.TypeFor[ThreadSetNameResponse]()},
		{Name: "ThreadSettings", Type: reflect.TypeFor[ThreadSettings]()},
		{Name: "ThreadSettingsUpdatedNotification", Type: reflect.TypeFor[ThreadSettingsUpdatedNotification]()},
		{Name: "ThreadSortKey", Type: reflect.TypeFor[ThreadSortKey]()},
		{Name: "ThreadSource", Type: reflect.TypeFor[ThreadSource]()},
		{Name: "ThreadSourceKind", Type: reflect.TypeFor[ThreadSourceKind]()},
		{Name: "ThreadStartParams", Type: reflect.TypeFor[ThreadStartParams]()},
		{Name: "ThreadStartResponse", Type: reflect.TypeFor[ThreadStartResponse]()},
		{Name: "ThreadStartSource", Type: reflect.TypeFor[ThreadStartSource]()},
		{Name: "ThreadStartedNotification", Type: reflect.TypeFor[ThreadStartedNotification]()},
		{Name: "ThreadStatus", Type: reflect.TypeFor[ThreadStatus]()},
		{Name: "ThreadStatusChangedNotification", Type: reflect.TypeFor[ThreadStatusChangedNotification]()},
		{Name: "ThreadItem", Type: reflect.TypeFor[ThreadItem]()},
		{Name: "ThreadTokenUsageUpdatedNotificationParams", Type: reflect.TypeFor[ThreadTokenUsageUpdatedNotificationParams]()},
		{Name: "ThreadUnarchiveParams", Type: reflect.TypeFor[ThreadUnarchiveParams]()},
		{Name: "ThreadUnarchiveResponse", Type: reflect.TypeFor[ThreadUnarchiveResponse]()},
		{Name: "ThreadUnarchiveResult", Type: reflect.TypeFor[ThreadUnarchiveResult]()},
		{Name: "ThreadUnarchivedNotification", Type: reflect.TypeFor[ThreadUnarchivedNotification]()},
		{Name: "ThreadUnsubscribeParams", Type: reflect.TypeFor[ThreadUnsubscribeParams]()},
		{Name: "ThreadUnsubscribeResponse", Type: reflect.TypeFor[ThreadUnsubscribeResponse]()},
		{Name: "ThreadUnsubscribeStatus", Type: reflect.TypeFor[ThreadUnsubscribeStatus]()},
		{Name: "TextElement", Type: reflect.TypeFor[TextElement]()},
		{Name: "TextPosition", Type: reflect.TypeFor[TextPosition]()},
		{Name: "TextRange", Type: reflect.TypeFor[TextRange]()},
		{Name: "TimelineItem", Type: reflect.TypeFor[TimelineItem]()},
		{Name: "TokenUsage", Type: reflect.TypeFor[TokenUsage]()},
		{Name: "TokenUsageBreakdown", Type: reflect.TypeFor[TokenUsageBreakdown]()},
		{Name: "Tool", Type: reflect.TypeFor[Tool]()},
		{Name: "ToolPayloadSummary", Type: reflect.TypeFor[ToolPayloadSummary]()},
		{Name: "ToolRequestUserInputAnswer", Type: reflect.TypeFor[ToolRequestUserInputAnswer]()},
		{Name: "ToolRequestUserInputOption", Type: reflect.TypeFor[ToolRequestUserInputOption]()},
		{Name: "ToolRequestUserInputParams", Type: reflect.TypeFor[ToolRequestUserInputParams]()},
		{Name: "ToolRequestUserInputQuestion", Type: reflect.TypeFor[ToolRequestUserInputQuestion]()},
		{Name: "ToolRequestUserInputResponse", Type: reflect.TypeFor[ToolRequestUserInputResponse]()},
		{Name: "ToolsV2", Type: reflect.TypeFor[ToolsV2]()},
		{Name: "Turn", Type: reflect.TypeFor[Turn]()},
		{Name: "TurnCompletedNotification", Type: reflect.TypeFor[TurnCompletedNotification]()},
		{Name: "TurnDiffUpdatedNotificationParams", Type: reflect.TypeFor[TurnDiffUpdatedNotificationParams]()},
		{Name: "TurnEnvironmentParams", Type: reflect.TypeFor[TurnEnvironmentParams]()},
		{Name: "TurnError", Type: reflect.TypeFor[TurnError]()},
		{Name: "TurnItemsView", Type: reflect.TypeFor[TurnItemsView]()},
		{Name: "TurnInterruptParams", Type: reflect.TypeFor[TurnInterruptParams]()},
		{Name: "TurnInterruptResponse", Type: reflect.TypeFor[TurnInterruptResponse]()},
		{Name: "TurnModerationMetadataNotification", Type: reflect.TypeFor[TurnModerationMetadataNotification]()},
		{Name: "TurnLifecycleStatus", Type: reflect.TypeFor[TurnLifecycleStatus]()},
		{Name: "TurnPlanStep", Type: reflect.TypeFor[TurnPlanStep]()},
		{Name: "TurnPlanStepStatus", Type: reflect.TypeFor[TurnPlanStepStatus]()},
		{Name: "TurnPlanUpdatedNotification", Type: reflect.TypeFor[TurnPlanUpdatedNotification]()},
		{Name: "TurnRecord", Type: reflect.TypeFor[TurnRecord]()},
		{Name: "TurnRunInterruptParams", Type: reflect.TypeFor[TurnRunInterruptParams]()},
		{Name: "TurnRunInterruptResult", Type: reflect.TypeFor[TurnRunInterruptResult]()},
		{Name: "TurnRunRetryParams", Type: reflect.TypeFor[TurnRunRetryParams]()},
		{Name: "TurnRunRetryResult", Type: reflect.TypeFor[TurnRunRetryResult]()},
		{Name: "TurnRunStartParams", Type: reflect.TypeFor[TurnRunStartParams]()},
		{Name: "TurnRunStartResult", Type: reflect.TypeFor[TurnRunStartResult]()},
		{Name: "TurnStartParams", Type: reflect.TypeFor[TurnStartParams]()},
		{Name: "TurnStartResponse", Type: reflect.TypeFor[TurnStartResponse]()},
		{Name: "TurnStatus", Type: reflect.TypeFor[TurnStatus]()},
		{Name: "TurnSteerParams", Type: reflect.TypeFor[TurnSteerParams]()},
		{Name: "TurnSteerResponse", Type: reflect.TypeFor[TurnSteerResponse]()},
		{Name: "TurnStartedNotification", Type: reflect.TypeFor[TurnStartedNotification]()},
		{Name: "TurnsPage", Type: reflect.TypeFor[TurnsPage]()},
		{Name: "UserInput", Type: reflect.TypeFor[UserInput]()},
		{Name: "Verbosity", Type: reflect.TypeFor[Verbosity]()},
		{Name: "WebSearchAction", Type: reflect.TypeFor[WebSearchAction]()},
		{Name: "WebSearchContextSize", Type: reflect.TypeFor[WebSearchContextSize]()},
		{Name: "WebSearchItem", Type: reflect.TypeFor[WebSearchItem]()},
		{Name: "WebSearchLocation", Type: reflect.TypeFor[WebSearchLocation]()},
		{Name: "WebSearchMode", Type: reflect.TypeFor[WebSearchMode]()},
		{Name: "WebSearchToolConfig", Type: reflect.TypeFor[WebSearchToolConfig]()},
		{Name: "WarningNotification", Type: reflect.TypeFor[WarningNotification]()},
		{Name: "W3cTraceContext", Type: reflect.TypeFor[W3cTraceContext]()},
		{Name: "WindowsSandboxSetupMode", Type: reflect.TypeFor[WindowsSandboxSetupMode]()},
		{Name: "WindowsSandboxSetupStartParams", Type: reflect.TypeFor[WindowsSandboxSetupStartParams]()},
		{Name: "WorkspaceMessage", Type: reflect.TypeFor[WorkspaceMessage]()},
		{Name: "WorkspaceMessageType", Type: reflect.TypeFor[WorkspaceMessageType]()},
		{Name: "WriteStatus", Type: reflect.TypeFor[WriteStatus]()},
		{Name: "EnvironmentConnectionNotification", Type: reflect.TypeFor[EnvironmentConnectionNotification]()},
		{Name: "RemoteControlConnectionStatus", Type: reflect.TypeFor[RemoteControlConnectionStatus]()},
		{Name: "RemoteControlStatusChangedNotification", Type: reflect.TypeFor[RemoteControlStatusChangedNotification]()},
		{Name: "SkillsChangedNotification", Type: reflect.TypeFor[SkillsChangedNotification]()},
		{Name: "ThreadQueueChangedNotification", Type: reflect.TypeFor[ThreadQueueChangedNotification]()},
		{Name: "ThreadRevertedNotification", Type: reflect.TypeFor[ThreadRevertedNotification]()},
		{Name: "WindowsSandboxSetupCompletedNotification", Type: reflect.TypeFor[WindowsSandboxSetupCompletedNotification]()},
		{Name: "WindowsWorldWritableWarningNotification", Type: reflect.TypeFor[WindowsWorldWritableWarningNotification]()},
		{Name: "RealtimeConversationVersion", Type: reflect.TypeFor[RealtimeConversationVersion]()},
		{Name: "ThreadRealtimeAudioChunk", Type: reflect.TypeFor[ThreadRealtimeAudioChunk]()},
		{Name: "ThreadRealtimeClosedNotification", Type: reflect.TypeFor[ThreadRealtimeClosedNotification]()},
		{Name: "ThreadRealtimeErrorNotification", Type: reflect.TypeFor[ThreadRealtimeErrorNotification]()},
		{Name: "ThreadRealtimeItemAddedNotification", Type: reflect.TypeFor[ThreadRealtimeItemAddedNotification]()},
		{Name: "ThreadRealtimeOutputAudioDeltaNotification", Type: reflect.TypeFor[ThreadRealtimeOutputAudioDeltaNotification]()},
		{Name: "ThreadRealtimeSdpNotification", Type: reflect.TypeFor[ThreadRealtimeSdpNotification]()},
		{Name: "ThreadRealtimeStartedNotification", Type: reflect.TypeFor[ThreadRealtimeStartedNotification]()},
		{Name: "ThreadRealtimeTranscriptDeltaNotification", Type: reflect.TypeFor[ThreadRealtimeTranscriptDeltaNotification]()},
		{Name: "ThreadRealtimeTranscriptDoneNotification", Type: reflect.TypeFor[ThreadRealtimeTranscriptDoneNotification]()},
		// Register exact public names after their aliases so nested schemas refer
		// to the public names. JSON and TypeScript output remain key-sorted.
		{Name: "ContextCompactedNotification", Type: reflect.TypeFor[ContextCompactedNotification]()},
		{Name: "RequestId", Type: reflect.TypeFor[RequestId]()},
		{Name: "CommandExecutionOutputDeltaNotification", Type: reflect.TypeFor[CommandExecutionOutputDeltaNotification]()},
		{Name: "CommandExecutionStatus", Type: reflect.TypeFor[CommandExecutionStatus]()},
		{Name: "DynamicToolCallStatus", Type: reflect.TypeFor[DynamicToolCallStatus]()},
		{Name: "FileChangePatchUpdatedNotification", Type: reflect.TypeFor[FileChangePatchUpdatedNotification]()},
		{Name: "McpToolCallError", Type: reflect.TypeFor[McpToolCallError]()},
		{Name: "McpToolCallProgressNotification", Type: reflect.TypeFor[McpToolCallProgressNotification]()},
		{Name: "McpToolCallStatus", Type: reflect.TypeFor[McpToolCallStatus]()},
		{Name: "ServerRequestResolvedNotification", Type: reflect.TypeFor[ServerRequestResolvedNotification]()},
		{Name: "ThreadTokenUsage", Type: reflect.TypeFor[ThreadTokenUsage]()},
		{Name: "ThreadTokenUsageUpdatedNotification", Type: reflect.TypeFor[ThreadTokenUsageUpdatedNotification]()},
		{Name: "TurnDiffUpdatedNotification", Type: reflect.TypeFor[TurnDiffUpdatedNotification]()},
	}
}

func wireSchemasForDefinitions(definitions []wireSchemaDefinition) Schema {
	names := make(map[reflect.Type]string, len(definitions))
	for _, definition := range definitions {
		names[definition.Type] = definition.Name
	}
	schemas := make(Schema, len(definitions))
	for _, definition := range definitions {
		schemas[definition.Name] = schemaForDefinition(definition.Type, names)
	}
	schemas["MethodState"] = stringEnumSchema(
		string(MethodImplemented),
		string(MethodBlocked),
		string(MethodDeferredStub),
		string(MethodRenamedEquivalent),
		string(MethodNotApplicable),
	)
	schemas["Surface"] = stringEnumSchema(
		string(SurfaceClientRequest),
		string(SurfaceServerNotification),
		string(SurfaceServerRequest),
		string(SurfaceClientNotification),
		string(SurfaceGollemExtension),
	)
	schemas["SortDirection"] = stringEnumSchema(string(SortDirectionAsc), string(SortDirectionDesc))
	schemas["InputModality"] = stringEnumSchema(string(InputModalityText), string(InputModalityImage))
	schemas["AdditionalContextKind"] = stringEnumSchema(
		string(AdditionalContextKindUntrusted), string(AdditionalContextKindApplication),
	)
	schemas["AdditionalContextEntry"] = additionalContextEntrySchema()
	schemas["AccountLoginCompletedNotification"] = closedThreadSessionParamSchema(Schema{
		"loginId": nullableStringSchema(),
		"success": Schema{"type": "boolean"},
		"error":   nullableStringSchema(),
	}, []string{"success"})
	schemas["ClientNotification"] = clientNotificationSchema()
	schemas["ClientRequest"] = clientRequestSchema()
	for name, schema := range jsonRPCErrorSchemas() {
		schemas[name] = schema
	}
	for name, schema := range jsonRPCEnvelopeSchemas() {
		schemas[name] = schema
	}
	schemas["JSONRPCMessage"] = jsonRPCMessageSchema()
	for name, schema := range serverRequestApprovalParamSchemas() {
		schemas[name] = schema
	}
	schemas["AccountTokenUsageDailyBucket"] = closedThreadSessionParamSchema(Schema{
		"startDate": Schema{"type": "string"},
		"tokens":    Schema{"type": "integer", "format": "int64"},
	}, []string{"startDate", "tokens"})
	schemas["AccountTokenUsageSummary"] = closedThreadSessionParamSchema(Schema{
		"lifetimeTokens":        Schema{"type": []any{"integer", "null"}, "format": "int64"},
		"peakDailyTokens":       Schema{"type": []any{"integer", "null"}, "format": "int64"},
		"longestRunningTurnSec": Schema{"type": []any{"integer", "null"}, "format": "int64"},
		"currentStreakDays":     Schema{"type": []any{"integer", "null"}, "format": "int64"},
		"longestStreakDays":     Schema{"type": []any{"integer", "null"}, "format": "int64"},
	}, nil)
	schemas["AppBranding"] = closedThreadSessionParamSchema(Schema{
		"category":          nullableStringSchema(),
		"developer":         nullableStringSchema(),
		"website":           nullableStringSchema(),
		"privacyPolicy":     nullableStringSchema(),
		"termsOfService":    nullableStringSchema(),
		"isDiscoverableApp": Schema{"type": "boolean"},
	}, []string{"isDiscoverableApp"})
	schemas["AppInfo"] = closedThreadSessionParamSchema(Schema{
		"id":                  Schema{"type": "string"},
		"name":                Schema{"type": "string"},
		"description":         nullableStringSchema(),
		"logoUrl":             nullableStringSchema(),
		"logoUrlDark":         nullableStringSchema(),
		"iconAssets":          nullableAppInfoStringMapSchema(),
		"iconDarkAssets":      nullableAppInfoStringMapSchema(),
		"distributionChannel": nullableStringSchema(),
		"branding": Schema{"anyOf": []any{
			Schema{"$ref": "#/$defs/AppBranding"}, Schema{"type": "null"},
		}},
		"appMetadata": Schema{"anyOf": []any{
			Schema{"$ref": "#/$defs/AppMetadata"}, Schema{"type": "null"},
		}},
		"labels":     nullableAppInfoStringMapSchema(),
		"installUrl": nullableStringSchema(),
		"isAccessible": Schema{
			"type": "boolean", "default": false,
		},
		"isEnabled": Schema{
			"type": "boolean", "default": true,
			"description": "Whether this app is enabled in config.toml. Example: ```toml [apps.bad_app] enabled = false ```",
		},
		"pluginDisplayNames": Schema{
			"type": "array", "items": Schema{"type": "string"}, "default": []any{},
		},
	}, []string{"id", "name"})
	schemas["AppInfo"].(Schema)["description"] = "EXPERIMENTAL - app metadata returned by app-list APIs."
	schemas["AppListUpdatedNotification"] = closedThreadSessionParamSchema(Schema{
		"data": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/AppInfo"}},
	}, []string{"data"})
	schemas["AppListUpdatedNotification"].(Schema)["description"] =
		"EXPERIMENTAL - notification emitted when the app list changes."
	schemas["AppMetadata"] = closedThreadSessionParamSchema(Schema{
		"review": Schema{"anyOf": []any{
			Schema{"$ref": "#/$defs/AppReview"}, Schema{"type": "null"},
		}},
		"categories": Schema{"anyOf": []any{
			Schema{"type": "array", "items": Schema{"type": "string"}}, Schema{"type": "null"},
		}},
		"subCategories": Schema{"anyOf": []any{
			Schema{"type": "array", "items": Schema{"type": "string"}}, Schema{"type": "null"},
		}},
		"seoDescription": nullableStringSchema(),
		"screenshots": Schema{"anyOf": []any{
			Schema{"type": "array", "items": Schema{"$ref": "#/$defs/AppScreenshot"}},
			Schema{"type": "null"},
		}},
		"developer":      nullableStringSchema(),
		"version":        nullableStringSchema(),
		"versionId":      nullableStringSchema(),
		"versionNotes":   nullableStringSchema(),
		"firstPartyType": nullableStringSchema(),
		"firstPartyRequiresInstall": Schema{"anyOf": []any{
			Schema{"type": "boolean"}, Schema{"type": "null"},
		}},
		"showInComposerWhenUnlinked": Schema{"anyOf": []any{
			Schema{"type": "boolean"}, Schema{"type": "null"},
		}},
	}, nil)
	schemas["AppReview"] = closedThreadSessionParamSchema(Schema{
		"status": Schema{"type": "string"},
	}, []string{"status"})
	schemas["AppScreenshot"] = closedThreadSessionParamSchema(Schema{
		"url":        nullableStringSchema(),
		"fileId":     nullableStringSchema(),
		"userPrompt": Schema{"type": "string"},
	}, []string{"userPrompt"})
	schemas["AppSummary"] = closedThreadSessionParamSchema(Schema{
		"id":          Schema{"type": "string"},
		"name":        Schema{"type": "string"},
		"description": nullableStringSchema(),
		"installUrl":  nullableStringSchema(),
		"category":    nullableStringSchema(),
	}, []string{"id", "name"})
	schemas["AppSummary"].(Schema)["description"] =
		"EXPERIMENTAL - app metadata summary for plugin responses."
	schemas["AppTemplateSummary"] = closedThreadSessionParamSchema(Schema{
		"templateId":           Schema{"type": "string"},
		"name":                 Schema{"type": "string"},
		"description":          nullableStringSchema(),
		"category":             nullableStringSchema(),
		"canonicalConnectorId": nullableStringSchema(),
		"logoUrl":              nullableStringSchema(),
		"logoUrlDark":          nullableStringSchema(),
		"materializedAppIds":   Schema{"type": "array", "items": Schema{"type": "string"}},
		"reason":               nullableSchemaRef("AppTemplateUnavailableReason"),
	}, []string{"templateId", "name", "materializedAppIds"})
	schemas["AppTemplateUnavailableReason"] = stringEnumSchema(
		string(AppTemplateUnavailableReasonNotConfiguredForWorkspace),
		string(AppTemplateUnavailableReasonNoActiveWorkspace),
	)
	schemas["AppToolApproval"] = stringEnumSchema(
		string(AppToolApprovalAuto),
		string(AppToolApprovalPrompt),
		string(AppToolApprovalWrites),
		string(AppToolApprovalApprove),
	)
	schemas["AppToolConfig"] = Schema{
		"type": "object",
		"properties": Schema{
			"approval_mode": nullableSchemaRef("AppToolApproval"),
			"enabled":       Schema{"type": []any{"boolean", "null"}},
		},
	}
	schemas["AppToolsConfig"] = Schema{"type": "object"}
	for name, schema := range appsDiscoveryParamSchemas() {
		schemas[name] = schema
	}
	schemas["FeedbackUploadParams"] = feedbackUploadParamSchema()
	schemas["AppConfig"] = Schema{
		"type": "object",
		"properties": Schema{
			"approvals_reviewer":          nullableSchemaRef("ApprovalsReviewer"),
			"default_tools_approval_mode": nullableSchemaRef("AppToolApproval"),
			"default_tools_enabled":       Schema{"type": []any{"boolean", "null"}},
			"destructive_enabled":         Schema{"type": []any{"boolean", "null"}},
			"enabled":                     Schema{"default": true, "type": "boolean"},
			"open_world_enabled":          Schema{"type": []any{"boolean", "null"}},
			"tools":                       nullableSchemaRef("AppToolsConfig"),
		},
	}
	schemas["AppsDefaultConfig"] = Schema{
		"type": "object",
		"properties": Schema{
			"approvals_reviewer":          nullableSchemaRef("ApprovalsReviewer"),
			"default_tools_approval_mode": nullableSchemaRef("AppToolApproval"),
			"destructive_enabled":         Schema{"default": true, "type": "boolean"},
			"enabled":                     Schema{"default": true, "type": "boolean"},
			"open_world_enabled":          Schema{"default": true, "type": "boolean"},
		},
	}
	schemas["AppsConfig"] = Schema{
		"type": "object",
		"properties": Schema{
			"_default": Schema{
				"anyOf":   []any{Schema{"$ref": "#/$defs/AppsDefaultConfig"}, Schema{"type": "null"}},
				"default": nil,
			},
		},
	}
	schemas["AppsListParams"] = Schema{
		"type":        "object",
		"description": "EXPERIMENTAL - list available apps/connectors.",
		"properties": Schema{
			"cursor": Schema{
				"description": "Opaque pagination cursor returned by a previous call.",
				"type":        []any{"string", "null"},
			},
			"forceRefetch": Schema{
				"description": "When true, bypass app caches and fetch the latest data from sources.",
				"type":        "boolean",
			},
			"limit": Schema{
				"description": "Optional page size; defaults to a reasonable server-side value.",
				"format":      "uint32", "minimum": float64(0), "type": []any{"integer", "null"},
			},
			"threadId": Schema{
				"description": "Optional thread id used to evaluate app feature gating from that thread's config.",
				"type":        []any{"string", "null"},
			},
		},
	}
	schemas["AppsListResponse"] = Schema{
		"type":        "object",
		"description": "EXPERIMENTAL - app list response.",
		"properties": Schema{
			"data": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/AppInfo"}},
			"nextCursor": Schema{
				"description": "Opaque cursor to pass to the next call to continue after the last item. If None, there are no more items to return.",
				"type":        []any{"string", "null"},
			},
		},
		"required": []string{"data"},
	}
	schemas["AddCreditsNudgeCreditType"] = stringEnumSchema(
		string(AddCreditsNudgeCreditTypeCredits),
		string(AddCreditsNudgeCreditTypeUsageLimit),
	)
	schemas["AddCreditsNudgeEmailStatus"] = stringEnumSchema(
		string(AddCreditsNudgeEmailStatusSent),
		string(AddCreditsNudgeEmailStatusCooldownActive),
	)
	schemas["AgentPath"] = Schema{"type": "string"}
	schemas["AttestationGenerateParams"] = Schema{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title":   "AttestationGenerateParams",
		"type":    "object",
	}
	schemas["CurrentTimeReadParams"] = currentTimeReadParamsSchema()
	schemas["CurrentTimeReadResponse"] = currentTimeReadResponseSchema()
	schemas["AmazonBedrockCredentialSource"] = stringEnumSchema(
		string(AmazonBedrockCredentialSourceCodexManaged),
		string(AmazonBedrockCredentialSourceAWSManaged),
	)
	schemas["AuthMode"] = stringEnumSchema(
		string(AuthModeAPIKey),
		string(AuthModeChatGPT),
		string(AuthModeChatGPTAuthTokens),
		string(AuthModeHeaders),
		string(AuthModeAgentIdentity),
		string(AuthModePersonalAccessToken),
		string(AuthModeBedrockAPIKey),
	)
	schemas["AutoReviewDecisionSource"] = stringEnumSchema(
		string(AutoReviewDecisionSourceAgent),
	)
	schemas["CancelLoginAccountStatus"] = stringEnumSchema(
		string(CancelLoginAccountStatusCanceled),
		string(CancelLoginAccountStatusNotFound),
	)
	schemas["ChatgptAuthTokensRefreshReason"] = Schema{
		"oneOf": []any{Schema{
			"description": "Codex attempted a backend request and received `401 Unauthorized`.",
			"enum":        []any{string(ChatgptAuthTokensRefreshReasonUnauthorized)},
			"type":        "string",
		}},
	}
	schemas["ChatgptAuthTokensRefreshParams"] = Schema{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"properties": Schema{
			"reason": Schema{"$ref": "#/$defs/ChatgptAuthTokensRefreshReason"},
			"previousAccountId": Schema{
				"description": "Workspace/account identifier that Codex was previously using.\n\n" +
					"Clients that manage multiple accounts/workspaces can use this as a hint to " +
					"refresh the token for the correct workspace.\n\n" +
					"This may be `null` when the prior auth state did not include a workspace " +
					"identifier (`chatgpt_account_id`).",
				"type": []any{"string", "null"},
			},
		},
		"required": []string{"reason"},
		"title":    "ChatgptAuthTokensRefreshParams",
		"type":     "object",
	}
	schemas["ChatgptAuthTokensRefreshResponse"] = closedThreadSessionParamSchema(Schema{
		"accessToken":      Schema{"type": "string"},
		"chatgptAccountId": Schema{"type": "string"},
		"chatgptPlanType":  nullableStringSchema(),
	}, []string{"accessToken", "chatgptAccountId"})
	schemas["LoginAppBrand"] = stringEnumSchema(
		string(LoginAppBrandCodex),
		string(LoginAppBrandChatGPT),
	)
	schemas["LoginAccountParams"] = loginAccountParamsSchema()
	schemas["LoginAccountResponse"] = loginAccountResponseSchema()
	schemas["MarketplaceAddParams"] = marketplaceAddParamSchema()
	schemas["MarketplaceRemoveParams"] = marketplaceRemoveParamSchema()
	schemas["MarketplaceUpgradeParams"] = marketplaceUpgradeParamSchema()
	schemas["McpAuthStatus"] = stringEnumSchema(
		string(McpAuthStatusUnsupported), string(McpAuthStatusNotLoggedIn),
		string(McpAuthStatusBearerToken), string(McpAuthStatusOAuth),
	)
	schemas["McpServerStartupFailureReason"] = stringEnumSchema(
		string(McpServerStartupFailureReasonReauthenticationRequired),
	)
	schemas["McpServerStartupState"] = stringEnumSchema(
		string(McpServerStartupStateStarting), string(McpServerStartupStateReady),
		string(McpServerStartupStateFailed), string(McpServerStartupStateCancelled),
	)
	schemas["McpServerStatusDetail"] = stringEnumSchema(
		string(McpServerStatusDetailFull), string(McpServerStatusDetailToolsAndAuthOnly),
	)
	schemas["ListMcpServerStatusParams"] = listMcpServerStatusParamsSchema()
	schemas["ListMcpServerStatusResponse"] = listMcpServerStatusResponseSchema()
	schemas["McpResourceReadParams"] = mcpResourceReadParamsSchema()
	schemas["McpResourceReadResponse"] = mcpResourceReadResponseSchema()
	schemas["McpServerInfo"] = mcpServerInfoSchema()
	for name, schema := range mcpServerOauthLoginSchemas() {
		schemas[name] = schema
	}
	schemas["McpServerStatus"] = mcpServerStatusSchema()
	schemas["Resource"] = mcpResourceSchema()
	schemas["ResourceTemplate"] = mcpResourceTemplateSchema()
	schemas["Tool"] = mcpToolSchema()
	schemas["McpServerMigration"] = mcpServerMigrationSchema()
	schemas["PluginShareCheckoutParams"] = pluginShareCheckoutParamSchema()
	schemas["PluginShareDeleteParams"] = pluginShareDeleteParamSchema()
	schemas["PluginShareListParams"] = pluginShareListParamSchema()
	schemas["PluginShareDiscoverability"] = pluginShareDiscoverabilitySchema()
	schemas["PluginSharePrincipalType"] = pluginSharePrincipalTypeSchema()
	schemas["PluginShareTargetRole"] = pluginShareTargetRoleSchema()
	schemas["PluginShareTarget"] = pluginShareTargetSchema()
	schemas["PluginShareSaveParams"] = pluginShareSaveParamsSchema()
	schemas["PluginShareUpdateDiscoverability"] = pluginShareUpdateDiscoverabilitySchema()
	schemas["PluginShareUpdateTargetsParams"] = pluginShareUpdateTargetsParamsSchema()
	schemas["PluginListMarketplaceKind"] = pluginListMarketplaceKindSchema()
	schemas["PluginListParams"] = pluginListParamSchema()
	schemas["PluginReadParams"] = pluginReadParamSchema()
	schemas["PluginInstallParams"] = pluginInstallParamSchema()
	schemas["PluginInstalledParams"] = pluginInstalledParamSchema()
	schemas["PluginSkillReadParams"] = pluginSkillReadParamSchema()
	schemas["PluginUninstallParams"] = pluginUninstallParamSchema()
	schemas["PluginsMigration"] = pluginsMigrationSchema()
	schemas["SkillMigration"] = skillMigrationSchema()
	schemas["SkillsConfigWriteParams"] = skillsConfigWriteParamSchema()
	schemas["SkillsExtraRootsSetParams"] = skillsExtraRootsSetParamSchema()
	schemas["SkillsListParams"] = skillsListParamSchema()
	schemas["SessionMigration"] = sessionMigrationSchema()
	schemas["HookMigration"] = hookMigrationSchema()
	schemas["SubagentMigration"] = subagentMigrationSchema()
	schemas["CommandMigration"] = commandMigrationSchema()
	schemas["MigrationDetails"] = migrationDetailsSchema()
	schemas["ExternalAgentConfigMigrationItemType"] = stringEnumSchema(
		string(ExternalAgentConfigMigrationItemTypeAgentsMD),
		string(ExternalAgentConfigMigrationItemTypeConfig),
		string(ExternalAgentConfigMigrationItemTypeSkills),
		string(ExternalAgentConfigMigrationItemTypePlugins),
		string(ExternalAgentConfigMigrationItemTypeMCPServerConfig),
		string(ExternalAgentConfigMigrationItemTypeSubagents),
		string(ExternalAgentConfigMigrationItemTypeHooks),
		string(ExternalAgentConfigMigrationItemTypeCommands),
		string(ExternalAgentConfigMigrationItemTypeSessions),
	)
	schemas["ExternalAgentConfigMigrationItem"] = externalAgentConfigMigrationItemSchema()
	schemas["ExternalAgentConfigDetectParams"] = externalAgentConfigDetectParamsSchema()
	schemas["ExternalAgentConfigDetectResponse"] = externalAgentConfigDetectResponseSchema()
	schemas["ExternalAgentConfigImportParams"] = externalAgentConfigImportParamsSchema()
	schemas["ExternalAgentConfigImportResponse"] = externalAgentConfigImportResponseSchema()
	schemas["ExternalAgentConfigImportItemTypeFailure"] = externalAgentConfigImportItemTypeFailureSchema()
	schemas["ExternalAgentConfigImportItemTypeSuccess"] = externalAgentConfigImportItemTypeSuccessSchema()
	schemas["ExternalAgentConfigImportTypeResult"] = externalAgentConfigImportTypeResultSchema()
	schemas["ExternalAgentConfigImportHistoryRecordSuccessParams"] = externalAgentConfigImportHistoryRecordSuccessParamsSchema()
	schemas["ExternalAgentConfigImportHistoryRecordTypeResultParams"] = externalAgentConfigImportHistoryRecordTypeResultParamsSchema()
	schemas["ExternalAgentConfigImportHistoryRecordParams"] = externalAgentConfigImportHistoryRecordParamsSchema()
	schemas["ExternalAgentConfigImportHistory"] = externalAgentConfigImportHistorySchema()
	schemas["ExternalAgentConfigImportHistoriesReadResponse"] = externalAgentConfigImportHistoriesReadResponseSchema()
	schemas["ExternalAgentConfigImportProgressNotification"] = externalAgentConfigImportNotificationSchema()
	schemas["ExternalAgentConfigImportCompletedNotification"] = externalAgentConfigImportNotificationSchema()
	schemas["FuzzyFileSearchMatchType"] = stringEnumSchema(
		string(FuzzyFileSearchMatchTypeFile), string(FuzzyFileSearchMatchTypeDirectory),
	)
	schemas["FuzzyFileSearchParams"] = fuzzyFileSearchParamsSchema()
	schemas["FuzzyFileSearchResult"] = fuzzyFileSearchResultSchema()
	schemas["FuzzyFileSearchResponse"] = fuzzyFileSearchResponseSchema()
	schemas["FuzzyFileSearchSessionUpdatedNotification"] = fuzzyFileSearchSessionUpdatedNotificationSchema()
	schemas["FuzzyFileSearchSessionCompletedNotification"] = fuzzyFileSearchSessionCompletedNotificationSchema()
	schemas["GuardianApprovalReview"] = guardianApprovalReviewSchema()
	schemas["GuardianApprovalReviewAction"] = guardianApprovalReviewActionSchema()
	schemas["GuardianApprovalReviewStatus"] = stringEnumSchema(
		string(GuardianApprovalReviewStatusInProgress), string(GuardianApprovalReviewStatusApproved),
		string(GuardianApprovalReviewStatusDenied), string(GuardianApprovalReviewStatusTimedOut),
		string(GuardianApprovalReviewStatusAborted),
	)
	schemas["GuardianCommandSource"] = stringEnumSchema(
		string(GuardianCommandSourceShell), string(GuardianCommandSourceUnifiedExec),
	)
	schemas["GuardianRiskLevel"] = stringEnumSchema(
		string(GuardianRiskLevelLow), string(GuardianRiskLevelMedium),
		string(GuardianRiskLevelHigh), string(GuardianRiskLevelCritical),
	)
	schemas["GuardianUserAuthorization"] = stringEnumSchema(
		string(GuardianUserAuthorizationUnknown), string(GuardianUserAuthorizationLow),
		string(GuardianUserAuthorizationMedium), string(GuardianUserAuthorizationHigh),
	)
	schemas["HookEventName"] = stringEnumSchema(
		string(HookEventNamePreToolUse), string(HookEventNamePermissionRequest),
		string(HookEventNamePostToolUse), string(HookEventNamePreCompact),
		string(HookEventNamePostCompact), string(HookEventNameSessionStart),
		string(HookEventNameUserPromptSubmit), string(HookEventNameSubagentStart),
		string(HookEventNameSubagentStop), string(HookEventNameStop),
	)
	schemas["HookExecutionMode"] = stringEnumSchema(
		string(HookExecutionModeSync), string(HookExecutionModeAsync),
	)
	schemas["HookHandlerType"] = stringEnumSchema(
		string(HookHandlerTypeCommand), string(HookHandlerTypePrompt), string(HookHandlerTypeAgent),
	)
	schemas["HookOutputEntryKind"] = stringEnumSchema(
		string(HookOutputEntryKindWarning), string(HookOutputEntryKindStop),
		string(HookOutputEntryKindFeedback), string(HookOutputEntryKindContext),
		string(HookOutputEntryKindError),
	)
	schemas["HookRunStatus"] = stringEnumSchema(
		string(HookRunStatusRunning), string(HookRunStatusCompleted),
		string(HookRunStatusFailed), string(HookRunStatusBlocked), string(HookRunStatusStopped),
	)
	schemas["HookScope"] = stringEnumSchema(string(HookScopeThread), string(HookScopeTurn))
	schemas["HookSource"] = stringEnumSchema(
		string(HookSourceSystem), string(HookSourceUser), string(HookSourceProject),
		string(HookSourceMDM), string(HookSourceSessionFlags), string(HookSourcePlugin),
		string(HookSourceCloudRequirements), string(HookSourceCloudManagedConfig),
		string(HookSourceLegacyManagedConfigFile), string(HookSourceLegacyManagedConfigMDM),
		string(HookSourceUnknown),
	)
	schemas["HookTrustStatus"] = stringEnumSchema(
		string(HookTrustStatusManaged), string(HookTrustStatusUntrusted),
		string(HookTrustStatusTrusted), string(HookTrustStatusModified),
	)
	schemas["HookOutputEntry"] = closedThreadSessionParamSchema(Schema{
		"kind": Schema{"$ref": "#/$defs/HookOutputEntryKind"},
		"text": Schema{"type": "string"},
	}, []string{"kind", "text"})
	schemas["HookRunSummary"] = hookRunSummarySchema()
	schemas["HookStartedNotification"] = hookRunNotificationSchema()
	schemas["HookCompletedNotification"] = hookRunNotificationSchema()
	schemas["HookErrorInfo"] = hookErrorInfoSchema()
	schemas["HookMetadata"] = hookMetadataSchema()
	schemas["HooksListEntry"] = hooksListEntrySchema()
	schemas["HooksListParams"] = hooksListParamsSchema()
	schemas["HooksListResponse"] = hooksListResponseSchema()
	schemas["ItemGuardianApprovalReviewCompletedNotification"] = guardianApprovalReviewCompletedNotificationSchema()
	schemas["ItemGuardianApprovalReviewStartedNotification"] = guardianApprovalReviewStartedNotificationSchema()
	schemas["ApplyPatchApprovalParams"] = closedThreadSessionParamSchema(Schema{
		"conversationId": Schema{"$ref": "#/$defs/ThreadId"},
		"callId": Schema{
			"type": "string",
			"description": "Use to correlate this with [codex_protocol::protocol::PatchApplyBeginEvent] " +
				"and [codex_protocol::protocol::PatchApplyEndEvent].",
		},
		"fileChanges": Schema{
			"type":                 "object",
			"additionalProperties": Schema{"$ref": "#/$defs/FileChange"},
		},
		"reason": Schema{
			"anyOf":       []any{Schema{"type": "string"}, Schema{"type": "null"}},
			"description": "Optional explanatory reason (e.g. request for extra write access).",
		},
		"grantRoot": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
			"description": "When set, the agent is asking the user to allow writes under this root " +
				"for the remainder of the session (unclear if this is honored today).",
		},
	}, []string{"callId", "conversationId", "fileChanges"})
	schemas["ExecCommandApprovalParams"] = closedThreadSessionParamSchema(Schema{
		"conversationId": Schema{"$ref": "#/$defs/ThreadId"},
		"callId": Schema{
			"type": "string",
			"description": "Use to correlate this with [codex_protocol::protocol::ExecCommandBeginEvent] " +
				"and [codex_protocol::protocol::ExecCommandEndEvent].",
		},
		"approvalId": Schema{
			"anyOf":       []any{Schema{"type": "string"}, Schema{"type": "null"}},
			"description": "Identifier for this specific approval callback.",
		},
		"command": Schema{"type": "array", "items": Schema{"type": "string"}},
		"cwd":     Schema{"type": "string"},
		"reason":  nullableStringSchema(),
		"parsedCmd": Schema{
			"type":  "array",
			"items": Schema{"$ref": "#/$defs/ParsedCommand"},
		},
	}, []string{"callId", "command", "conversationId", "cwd", "parsedCmd"})
	// Preserve the raw source schemas for standalone legacy approval records.
	// Their closed TypeScript records are normalized only in the renderer.
	schemas["ApplyPatchApprovalParams"] = sourceApprovalParamsSchema("ApplyPatchApprovalParams", Schema{
		"conversationId": Schema{"$ref": "#/$defs/ThreadId"},
		"callId":         Schema{"type": "string", "description": "Use to correlate this with [codex_protocol::protocol::PatchApplyBeginEvent] and [codex_protocol::protocol::PatchApplyEndEvent]."},
		"fileChanges":    Schema{"type": "object", "additionalProperties": Schema{"$ref": "#/$defs/FileChange"}},
		"reason":         Schema{"description": "Optional explanatory reason (e.g. request for extra write access).", "type": []any{"string", "null"}},
		"grantRoot":      Schema{"description": "When set, the agent is asking the user to allow writes under this root for the remainder of the session (unclear if this is honored today).", "type": []any{"string", "null"}},
	}, []string{"callId", "conversationId", "fileChanges"})
	schemas["ExecCommandApprovalParams"] = sourceApprovalParamsSchema("ExecCommandApprovalParams", Schema{
		"conversationId": Schema{"$ref": "#/$defs/ThreadId"},
		"callId":         Schema{"type": "string", "description": "Use to correlate this with [codex_protocol::protocol::ExecCommandBeginEvent] and [codex_protocol::protocol::ExecCommandEndEvent]."},
		"approvalId":     Schema{"description": "Identifier for this specific approval callback.", "type": []any{"string", "null"}},
		"command":        Schema{"type": "array", "items": Schema{"type": "string"}},
		"cwd":            Schema{"type": "string"},
		"reason":         Schema{"type": []any{"string", "null"}},
		"parsedCmd":      Schema{"type": "array", "items": Schema{"$ref": "#/$defs/ParsedCommand"}},
	}, []string{"callId", "command", "conversationId", "cwd", "parsedCmd"})
	schemas["PermissionsRequestApprovalParams"] = sourceApprovalParamsSchema("PermissionsRequestApprovalParams", Schema{
		"cwd":           Schema{"$ref": "#/$defs/AbsolutePathBuf"},
		"environmentId": Schema{"default": nil, "type": []any{"string", "null"}},
		"itemId":        Schema{"type": "string"},
		"permissions":   Schema{"$ref": "#/$defs/RequestPermissionProfile"},
		"reason":        Schema{"type": []any{"string", "null"}},
		"startedAtMs": Schema{
			"description": "Unix timestamp (in milliseconds) when this approval request started.",
			"format":      "int64",
			"type":        "integer",
		},
		"threadId": Schema{"type": "string"},
		"turnId":   Schema{"type": "string"},
	}, []string{"cwd", "itemId", "permissions", "startedAtMs", "threadId", "turnId"})
	// Preserve the raw source schemas for request_user_input. TypeScript closes
	// these serde-open records in the renderer only.
	schemas["ToolRequestUserInputOption"] = Schema{
		"description": "EXPERIMENTAL. Defines a single selectable option for request_user_input.",
		"properties": Schema{
			"description": Schema{"type": "string"},
			"label":       Schema{"type": "string"},
		},
		"required": []string{"description", "label"},
		"type":     "object",
	}
	schemas["ToolRequestUserInputQuestion"] = Schema{
		"description": "EXPERIMENTAL. Represents one request_user_input question and its required options.",
		"properties": Schema{
			"header":   Schema{"type": "string"},
			"id":       Schema{"type": "string"},
			"isOther":  Schema{"default": false, "type": "boolean"},
			"isSecret": Schema{"default": false, "type": "boolean"},
			"options": Schema{
				"items": Schema{"$ref": "#/$defs/ToolRequestUserInputOption"},
				"type":  []any{"array", "null"},
			},
			"question": Schema{"type": "string"},
		},
		"required": []string{"header", "id", "question"},
		"type":     "object",
	}
	schemas["ToolRequestUserInputParams"] = Schema{
		"$schema":     "http://json-schema.org/draft-07/schema#",
		"description": "EXPERIMENTAL. Params sent with a request_user_input event.",
		"properties": Schema{
			"autoResolutionMs": Schema{
				"default":     nil,
				"description": "@deprecated Use `isBlocking` to decide whether the request should block.",
				"format":      "uint64",
				"minimum":     json.Number("0.0"),
				"type":        []any{"integer", "null"},
			},
			"isBlocking": Schema{"type": "boolean"},
			"itemId":     Schema{"type": "string"},
			"questions":  Schema{"items": Schema{"$ref": "#/$defs/ToolRequestUserInputQuestion"}, "type": "array"},
			"threadId":   Schema{"type": "string"},
			"turnId":     Schema{"type": "string"},
		},
		"required": []string{"isBlocking", "itemId", "questions", "threadId", "turnId"},
		"title":    "ToolRequestUserInputParams",
		"type":     "object",
	}
	schemas["ToolRequestUserInputAnswer"] = Schema{
		"description": "EXPERIMENTAL. Captures a user's answer to a request_user_input question.",
		"properties":  Schema{"answers": Schema{"items": Schema{"type": "string"}, "type": "array"}},
		"required":    []string{"answers"},
		"type":        "object",
	}
	schemas["ToolRequestUserInputResponse"] = Schema{
		"$schema":     "http://json-schema.org/draft-07/schema#",
		"description": "EXPERIMENTAL. Response payload mapping question ids to answers.",
		"properties":  Schema{"answers": Schema{"additionalProperties": Schema{"$ref": "#/$defs/ToolRequestUserInputAnswer"}, "type": "object"}},
		"required":    []string{"answers"},
		"title":       "ToolRequestUserInputResponse",
		"type":        "object",
	}
	schemas["NetworkApprovalContext"] = closedThreadSessionParamSchema(Schema{
		"host":     Schema{"type": "string"},
		"protocol": Schema{"$ref": "#/$defs/NetworkApprovalProtocol"},
	}, []string{"host", "protocol"})
	schemas["NetworkApprovalProtocol"] = stringEnumSchema(
		string(NetworkApprovalProtocolHTTP), string(NetworkApprovalProtocolHTTPS),
		string(NetworkApprovalProtocolSocks5TCP), string(NetworkApprovalProtocolSocks5UDP),
	)
	schemas["ParsedCommand"] = parsedCommandSchema()
	for name, schema := range permissionProfileListSchemas() {
		schemas[name] = schema
	}
	for name, schema := range dynamicToolSpecSchemas() {
		schemas[name] = schema
	}
	for name, schema := range collaborationCapabilitySchemas() {
		schemas[name] = schema
	}
	for name, schema := range providerHealthProbeSchemas() {
		schemas[name] = schema
	}
	schemas["ProcessExitedNotification"] = processExitedNotificationSchema()
	schemas["ProcessOutputDeltaNotification"] = processOutputDeltaNotificationSchema()
	schemas["ProcessOutputStream"] = processOutputStreamSchema()
	schemas["ProcessTerminalSize"] = processTerminalSizeSchema()
	schemas["BackgroundTerminalStatus"] = stringEnumSchema(
		string(BackgroundTerminalStatusRunning),
		string(BackgroundTerminalStatusCompleted),
		string(BackgroundTerminalStatusFailed),
		string(BackgroundTerminalStatusKilled),
		string(BackgroundTerminalStatusTimedOut),
		string(BackgroundTerminalStatusOwnerLost),
	)
	for name, schema := range accountRateLimitSchemas() {
		schemas[name] = schema
	}
	for name, schema := range accountEnvelopeSchemas() {
		schemas[name] = schema
	}
	for name, schema := range operationalNotificationLeafSchemas() {
		schemas[name] = schema
	}
	for name, schema := range realtimeNotificationSchemas() {
		schemas[name] = schema
	}
	for name, schema := range workspaceMessageSchemas() {
		schemas[name] = schema
	}
	for name, schema := range experimentalFeatureSchemas() {
		schemas[name] = schema
	}
	schemas["McpServerToolCallParams"] = mcpServerToolCallParamsSchema()
	schemas["McpServerToolCallResponse"] = mcpServerToolCallResponseSchema()
	schemas["ResourceContent"] = resourceContentSchema()
	schemas["ReasoningEffort"] = Schema{"type": "string", "minLength": 1}
	schemas["ResidencyRequirement"] = stringEnumSchema(string(ResidencyRequirementUS))
	schemas["WebSearchMode"] = stringEnumSchema(
		string(WebSearchModeDisabled), string(WebSearchModeCached),
		string(WebSearchModeIndexed), string(WebSearchModeLive),
	)
	schemas["WindowsSandboxSetupMode"] = stringEnumSchema(
		string(WindowsSandboxSetupModeElevated), string(WindowsSandboxSetupModeUnelevated),
	)
	schemas["WindowsSandboxSetupStartParams"] = windowsSandboxSetupStartParamSchema()
	schemas["NetworkDomainPermission"] = stringEnumSchema(
		string(NetworkDomainPermissionAllow), string(NetworkDomainPermissionDeny),
	)
	schemas["NetworkUnixSocketPermission"] = stringEnumSchema(
		string(NetworkUnixSocketPermissionAllow), string(NetworkUnixSocketPermissionDeny),
	)
	schemas["MergeStrategy"] = stringEnumSchema(
		string(MergeStrategyReplace), string(MergeStrategyUpsert),
	)
	schemas["WriteStatus"] = stringEnumSchema(
		string(WriteStatusOK), string(WriteStatusOKOverridden),
	)
	for name, schema := range configPrerequisiteSchemas() {
		schemas[name] = schema
	}
	schemas["Config"] = publicConfigSchema()
	schemas["ConfigReadResponse"] = configReadResponseSchema()
	schemas["ConfigLayerSource"] = configLayerSourceSchema()
	configReadProperties := schemas["ConfigReadParams"].(Schema)["properties"].(Schema)
	configReadProperties["cwd"].(Schema)["description"] = configReadCWDDescription
	const configFilePathDescription = "Path to the config file to write; defaults to the user's `config.toml` when omitted."
	for _, definition := range []string{"ConfigValueWriteParams", "ConfigBatchWriteParams"} {
		properties := schemas[definition].(Schema)["properties"].(Schema)
		properties["filePath"].(Schema)["description"] = configFilePathDescription
	}
	configBatchProperties := schemas["ConfigBatchWriteParams"].(Schema)["properties"].(Schema)
	configBatchProperties["reloadUserConfig"].(Schema)["description"] =
		"When true, hot-reload the updated user config into all loaded threads after writing."
	configWriteResponseProperties := schemas["ConfigWriteResponse"].(Schema)["properties"].(Schema)
	configWriteResponseProperties["filePath"].(Schema)["description"] =
		"Canonical path to the config file that was written."
	schemas["ConfiguredHookHandler"] = configuredHookHandlerSchema()
	configRequirementsProperties := schemas["ConfigRequirements"].(Schema)["properties"].(Schema)
	for _, name := range []string{"allowedPermissionProfiles", "featureRequirements"} {
		variants := configRequirementsProperties[name].(Schema)["anyOf"].([]any)
		variants[0].(Schema)["x-gollem-typescript-optional-map"] = true
	}
	networkRequirementsProperties := schemas["NetworkRequirements"].(Schema)["properties"].(Schema)
	for _, name := range []string{"httpPort", "socksPort"} {
		variants := networkRequirementsProperties[name].(Schema)["anyOf"].([]any)
		variants[0].(Schema)["minimum"] = 0
		variants[0].(Schema)["maximum"] = 65535
	}
	for _, name := range []string{"domains", "unixSockets"} {
		variants := networkRequirementsProperties[name].(Schema)["anyOf"].([]any)
		variants[0].(Schema)["x-gollem-typescript-optional-map"] = true
	}
	schemas["ModelListParams"] = modelListParamsSchema()
	modelProperties := schemas["Model"].(Schema)["properties"].(Schema)
	modelProperties["additionalSpeedTiers"].(Schema)["description"] = "Deprecated: use `serviceTiers` instead."
	modelProperties["defaultServiceTier"].(Schema)["description"] = "Catalog default service tier id for this model, when one is configured."
	modelListResponseProperties := schemas["ModelListResponse"].(Schema)["properties"].(Schema)
	modelListResponseProperties["nextCursor"].(Schema)["description"] = "Opaque cursor to pass to the next call to continue after the last item. If None, there are no more items to return."
	schemas["ReasoningSummary"] = stringEnumSchema(
		string(ReasoningSummaryAuto), string(ReasoningSummaryConcise),
		string(ReasoningSummaryDetailed), string(ReasoningSummaryNone),
	)
	schemas["CollabAgentStatus"] = stringEnumSchema(
		string(CollabAgentStatusPendingInit), string(CollabAgentStatusRunning),
		string(CollabAgentStatusInterrupted), string(CollabAgentStatusCompleted),
		string(CollabAgentStatusErrored), string(CollabAgentStatusShutdown),
		string(CollabAgentStatusNotFound),
	)
	schemas["CollabAgentTool"] = stringEnumSchema(
		string(CollabAgentToolSpawnAgent), string(CollabAgentToolSendInput),
		string(CollabAgentToolResumeAgent), string(CollabAgentToolWait),
		string(CollabAgentToolCloseAgent),
	)
	schemas["CollabAgentToolCallStatus"] = stringEnumSchema(
		string(CollabAgentToolCallStatusInProgress), string(CollabAgentToolCallStatusCompleted),
		string(CollabAgentToolCallStatusFailed),
	)
	schemas["SubAgentActivityKind"] = stringEnumSchema(
		string(SubAgentActivityStarted), string(SubAgentActivityInteracted),
		string(SubAgentActivityInterrupted),
	)
	schemas["CollabAgentState"] = collabAgentStateSchema()
	schemas["AgentMessageInputContent"] = rawResponseContentSchema(agentMessageInputContentVariants)
	schemas["ReasoningItemContent"] = rawResponseContentSchema(reasoningItemContentVariants)
	schemas["ReasoningItemReasoningSummary"] = rawResponseContentSchema(reasoningItemSummaryVariants)
	schemas["ReviewDecision"] = reviewDecisionSchema()
	for name, schema := range reviewStartSchemas() {
		schemas[name] = schema
	}
	for name, schema := range threadSettingsSchemas() {
		schemas[name] = schema
	}
	for name, schema := range threadInjectItemsSchemas() {
		schemas[name] = schema
	}
	for name, schema := range threadRollbackSchemas() {
		schemas[name] = schema
	}
	schemas["ThreadSearchResult"] = threadSearchResultSchema()
	for name, schema := range threadSectionParamSchemas() {
		schemas[name] = schema
	}
	for name, schema := range threadShellCommandSchemas() {
		schemas[name] = schema
	}
	schemas["ResponsesApiWebSearchAction"] = responsesAPIWebSearchActionSchema()
	schemas["ContentItem"] = contentItemSchema(contentItemVariants)
	schemas["FunctionCallOutputContentItem"] = contentItemSchema(functionCallOutputContentItemVariants)
	schemas["FunctionCallOutputBody"] = functionCallOutputBodySchema()
	schemas["ConversationTextRole"] = stringEnumSchema(
		string(ConversationTextRoleUser), string(ConversationTextRoleDeveloper), string(ConversationTextRoleAssistant),
	)
	schemas["InternalChatMessageMetadataPassthrough"] = internalChatMessageMetadataSchema()
	schemas["LocalShellStatus"] = stringEnumSchema(
		string(LocalShellStatusCompleted), string(LocalShellStatusInProgress), string(LocalShellStatusIncomplete),
	)
	schemas["LocalShellAction"] = localShellActionSchema()
	schemas["ResponseItem"] = responseItemSchema()
	schemas["ThreadItem"] = threadItemSchema()
	schemas["ThreadStartParams"] = threadStartParamsSchema()
	schemas["ThreadResumeParams"] = threadResumeParamsSchema()
	schemas["ThreadResumeInitialTurnsPageParams"] = threadResumeInitialTurnsPageParamsSchema()
	schemas["ThreadForkParams"] = threadForkParamsSchema()
	schemas["TurnStartParams"] = turnStartParamsSchema()
	schemas["TurnStartResponse"] = turnStartResponseSchema()
	schemas["TurnEnvironmentParams"] = turnEnvironmentParamsSchema()
	schemas["TerminalInteractionNotification"] = terminalInteractionNotificationSchema()
	for name, schema := range threadApproveGuardianDeniedActionSchemas() {
		schemas[name] = schema
	}
	schemas["W3cTraceContext"] = w3cTraceContextSchema()
	schemas["JsonValue"] = jsonValueSchema()
	setSchemaIntegerMinimum(schemas["ByteRange"].(Schema), 0, "start", "end")
	setSchemaIntegerMinimum(schemas["TextPosition"].(Schema), 0, "line", "column")
	schemas["ImageDetail"] = stringEnumSchema(
		string(ImageDetailAuto), string(ImageDetailLow), string(ImageDetailHigh), string(ImageDetailOriginal),
	)
	setSchemaIntegerMinimum(schemas["MemoryCitationEntry"].(Schema), 0, "lineStart", "lineEnd")
	schemas["MessagePhase"] = stringEnumSchema(
		string(MessagePhaseCommentary), string(MessagePhaseFinalAnswer),
	)
	schemas["ModelRerouteReason"] = stringEnumSchema(string(ModelRerouteReasonHighRiskCyberActivity))
	schemas["ModelVerification"] = stringEnumSchema(string(ModelVerificationTrustedAccessForCyber))
	schemas["UserInput"] = userInputSchema()
	schemas["ThreadLifecycleStatus"] = stringEnumSchema(
		string(ThreadLifecycleActive), string(ThreadLifecycleArchived), string(ThreadLifecycleDeleted),
	)
	setSchemaIntegerMinimum(schemas["ThreadHistoryRollbackParams"].(Schema), 1, "numTurns")
	schemas["ThreadGoalStatus"] = stringEnumSchema(
		string(ThreadGoalActive), string(ThreadGoalPaused), string(ThreadGoalBlocked),
		string(ThreadGoalUsageLimited), string(ThreadGoalBudgetLimited), string(ThreadGoalComplete),
	)
	schemas["ThreadMemoryMode"] = stringEnumSchema(
		string(ThreadMemoryModeEnabled), string(ThreadMemoryModeDisabled),
	)
	schemas["CommandExecOutputStream"] = stringEnumSchema(
		string(CommandExecOutputStdout), string(CommandExecOutputStderr),
	)
	schemas["CommandExecutionApprovalDecision"] = commandExecutionApprovalDecisionSchema()
	schemas["CommandAction"] = commandActionSchema()
	schemas["WebSearchAction"] = webSearchActionSchema()
	schemas["AbsolutePathBuf"] = Schema{
		"type":        "string",
		"description": "An absolute, lexically normalized local path.",
	}
	schemas["ApprovalsReviewer"] = stringEnumSchema(
		string(ApprovalsReviewerUser),
		string(ApprovalsReviewerAutoReview),
		string(ApprovalsReviewerGuardianSubagent),
	)
	schemas["AskForApproval"] = askForApprovalSchema()
	schemas["FileSystemAccessMode"] = stringEnumSchema(
		string(FileSystemAccessRead), string(FileSystemAccessWrite), string(FileSystemAccessDeny),
	)
	schemas["FileSystemPath"] = fileSystemPathSchema()
	schemas["FileSystemSpecialPath"] = fileSystemSpecialPathSchema()
	schemas["LegacyAppPathString"] = Schema{"type": "string"}
	schemas["Personality"] = stringEnumSchema(
		string(PersonalityNone), string(PersonalityFriendly), string(PersonalityPragmatic),
	)
	schemas["PermissionGrantScope"] = stringEnumSchema(
		string(PermissionGrantTurn), string(PermissionGrantSession),
	)
	for alias, canonical := range map[string]string{
		"CommandExecutionOutputDeltaNotificationParams": "CommandExecutionOutputDeltaNotification",
		"FileChangePatchUpdatedNotificationParams":      "FileChangePatchUpdatedNotification",
		"MCPToolCallError":                              "McpToolCallError",
		"MCPToolCallProgressNotificationParams":         "McpToolCallProgressNotification",
		"ServerRequestResolvedNotificationParams":       "ServerRequestResolvedNotification",
		"ThreadCompactedNotificationParams":             "ContextCompactedNotification",
		"ThreadTokenUsageUpdatedNotificationParams":     "ThreadTokenUsageUpdatedNotification",
		"TokenUsage":                        "ThreadTokenUsage",
		"TurnDiffUpdatedNotificationParams": "TurnDiffUpdatedNotification",
	} {
		schemas[alias] = Schema{"$ref": "#/$defs/" + canonical}
	}
	schemas["RequestId"] = Schema{"$ref": "#/$defs/RequestID"}
	schemas["ServerRequest"] = serverRequestSchema()
	schemas["ServerNotification"] = serverNotificationSchema()
	schemas["SandboxMode"] = stringEnumSchema(
		string(SandboxModeReadOnly), string(SandboxModeWorkspaceWrite), string(SandboxModeDangerFullAccess),
	)
	schemas["ThreadStartSource"] = stringEnumSchema(
		string(ThreadStartSourceStartup), string(ThreadStartSourceClear),
	)
	schemas["CommandExecutionStatus"] = stringEnumSchema(
		string(CommandExecutionStatusInProgress), string(CommandExecutionStatusCompleted),
		string(CommandExecutionStatusFailed), string(CommandExecutionStatusDeclined),
	)
	schemas["CommandExecutionSource"] = stringEnumSchema(
		string(CommandExecutionSourceAgent), string(CommandExecutionSourceUserShell),
		string(CommandExecutionSourceUnifiedExecStartup), string(CommandExecutionSourceUnifiedExecInteraction),
	)
	schemas["DynamicToolCallStatus"] = stringEnumSchema(
		string(DynamicToolCallStatusInProgress), string(DynamicToolCallStatusCompleted),
		string(DynamicToolCallStatusFailed),
	)
	schemas["McpToolCallStatus"] = stringEnumSchema(
		string(McpToolCallStatusInProgress), string(McpToolCallStatusCompleted),
		string(McpToolCallStatusFailed),
	)
	for definition, status := range map[string]string{
		"CommandExecutionItem": "CommandExecutionStatus",
		"DynamicToolCallItem":  "DynamicToolCallStatus",
		"MCPToolCallItem":      "McpToolCallStatus",
	} {
		properties := schemas[definition].(Schema)["properties"].(Schema)
		properties["status"] = Schema{"$ref": "#/$defs/" + status}
	}
	setSchemaIntegerMinimum(schemas["AdditionalFileSystemPermissions"].(Schema), 1, "globScanMaxDepth")
	if response, ok := schemas["PermissionsRequestApprovalResponse"].(Schema); ok {
		if properties, ok := response["properties"].(Schema); ok {
			if scope, ok := properties["scope"].(Schema); ok {
				scope["default"] = string(PermissionGrantTurn)
			}
		}
	}
	schemas["FileChangeApprovalDecision"] = stringEnumSchema(
		string(FileChangeApprovalAccept),
		string(FileChangeApprovalAcceptForSession),
		string(FileChangeApprovalDecline),
		string(FileChangeApprovalCancel),
	)
	schemas["FileChange"] = fileChangeSchema()
	schemas["PatchApplyStatus"] = stringEnumSchema(
		string(PatchApplyStatusInProgress), string(PatchApplyStatusCompleted),
		string(PatchApplyStatusFailed), string(PatchApplyStatusDeclined),
	)
	schemas["PatchChangeKind"] = patchChangeKindSchema()
	schemas["DynamicToolCallParams"] = dynamicToolCallParamsSchema()
	schemas["DynamicToolCallResponse"] = dynamicToolCallResponseSchema()
	schemas["DynamicToolCallOutputContentItem"] = dynamicToolCallOutputContentItemSchema()
	schemas["McpElicitationArrayType"] = stringEnumSchema("array")
	schemas["McpElicitationBooleanType"] = stringEnumSchema("boolean")
	schemas["McpElicitationEnumSchema"] = schemaRefUnion(
		"McpElicitationSingleSelectEnumSchema",
		"McpElicitationMultiSelectEnumSchema",
		"McpElicitationLegacyTitledEnumSchema",
	)
	schemas["McpElicitationMultiSelectEnumSchema"] = schemaRefUnion(
		"McpElicitationUntitledMultiSelectEnumSchema",
		"McpElicitationTitledMultiSelectEnumSchema",
	)
	schemas["McpElicitationNumberType"] = stringEnumSchema("number", "integer")
	schemas["McpElicitationObjectType"] = stringEnumSchema("object")
	schemas["McpElicitationPrimitiveSchema"] = schemaRefUnion(
		"McpElicitationEnumSchema",
		"McpElicitationStringSchema",
		"McpElicitationNumberSchema",
		"McpElicitationBooleanSchema",
	)
	schemas["McpElicitationSingleSelectEnumSchema"] = schemaRefUnion(
		"McpElicitationUntitledSingleSelectEnumSchema",
		"McpElicitationTitledSingleSelectEnumSchema",
	)
	schemas["McpElicitationStringFormat"] = stringEnumSchema("email", "uri", "date", "date-time")
	schemas["McpElicitationStringType"] = stringEnumSchema("string")
	setSchemaIntegerMinimum(schemas["McpElicitationStringSchema"].(Schema), 0, "minLength", "maxLength")
	setSchemaIntegerMinimum(schemas["McpElicitationTitledMultiSelectEnumSchema"].(Schema), 0, "minItems", "maxItems")
	setSchemaIntegerMinimum(schemas["McpElicitationUntitledMultiSelectEnumSchema"].(Schema), 0, "minItems", "maxItems")
	schemas["McpServerElicitationAction"] = stringEnumSchema("accept", "decline", "cancel")
	schemas["McpServerElicitationRequestParams"] = mcpServerElicitationRequestParamsSchema()
	schemas["McpServerElicitationRequestResponse"] = mcpServerElicitationRequestResponseSchema()
	schemas["NetworkPolicyRuleAction"] = stringEnumSchema(
		string(NetworkPolicyRuleAllow), string(NetworkPolicyRuleDeny),
	)
	schemas["NetworkAccess"] = stringEnumSchema(
		string(NetworkAccessRestricted), string(NetworkAccessEnabled),
	)
	schemas["SandboxPolicy"] = sandboxPolicySchema()
	for name, schema := range commandExecContractSchemas() {
		schemas[name] = schema
	}
	schemas["CommandExecWriteParams"] = schemaWithRequiredFieldAlternatives(
		schemas["CommandExecWriteParams"].(Schema),
		[]string{"processId"},
		[]string{"id"},
	)
	schemas["CommandExecTerminateParams"] = schemaWithRequiredFieldAlternatives(
		schemas["CommandExecTerminateParams"].(Schema),
		[]string{"processId"},
		[]string{"id"},
	)
	schemas["CommandExecResizeParams"] = schemaWithRequiredFieldAlternatives(
		schemas["CommandExecResizeParams"].(Schema),
		[]string{"processId", "size"},
		[]string{"processId", "cols", "rows"},
		[]string{"id", "cols", "rows"},
	)
	for _, name := range []string{"ThreadGoalSetParams", "ThreadGoalGetParams", "ThreadGoalClearParams"} {
		schemas[name] = schemaWithRequiredIDAlternative(schemas[name].(Schema))
	}
	schemas["ThreadMetadataUpdateParams"] = schemaWithRequiredIDAlternative(
		schemas["ThreadMetadataUpdateParams"].(Schema),
	)
	schemas["ThreadMemoryModeSetParams"] = schemaWithRequiredFieldAlternatives(
		schemas["ThreadMemoryModeSetParams"].(Schema),
		[]string{"threadId", "mode"},
		[]string{"id", "mode"},
		[]string{"threadId", "memoryMode"},
		[]string{"id", "memoryMode"},
	)
	schemas["ThreadListCwdFilter"] = Schema{"oneOf": []any{
		Schema{"type": "string"},
		Schema{"type": "array", "items": Schema{"type": "string"}},
	}}
	schemas["ThreadSortKey"] = stringEnumSchema(
		string(ThreadSortCreatedAt), string(ThreadSortUpdatedAt), string(ThreadSortRecencyAt),
	)
	schemas["ThreadSourceKind"] = stringEnumSchema(
		string(ThreadSourceCLI), string(ThreadSourceVSCode), string(ThreadSourceExec),
		string(ThreadSourceAppServer), string(ThreadSourceSubAgent), string(ThreadSourceSubAgentReview),
		string(ThreadSourceSubAgentCompact), string(ThreadSourceSubAgentSpawn),
		string(ThreadSourceSubAgentOther), string(ThreadSourceUnknown),
	)
	schemas["NonSteerableTurnKind"] = stringEnumSchema(
		string(NonSteerableTurnKindReview), string(NonSteerableTurnKindCompact),
	)
	schemas["ThreadActiveFlag"] = stringEnumSchema(
		string(ThreadActiveFlagWaitingOnApproval), string(ThreadActiveFlagWaitingOnUserInput),
	)
	schemas["TurnItemsView"] = stringEnumSchema(
		string(TurnItemsViewNotLoaded), string(TurnItemsViewSummary), string(TurnItemsViewFull),
	)
	schemas["TurnPlanStepStatus"] = stringEnumSchema(
		string(TurnPlanStepStatusPending), string(TurnPlanStepStatusInProgress),
		string(TurnPlanStepStatusCompleted),
	)
	schemas["TurnStatus"] = stringEnumSchema(
		string(TurnStatusCompleted), string(TurnStatusInterrupted), string(TurnStatusFailed),
		string(TurnStatusInProgress),
	)
	schemas["CodexErrorInfo"] = codexErrorInfoSchema()
	schemas["ThreadStatus"] = threadStatusSchema()
	schemas["SubAgentSource"] = subAgentSourceSchema()
	schemas["SessionSource"] = sessionSourceSchema()
	schemas["ThreadUnsubscribeStatus"] = stringEnumSchema(
		string(ThreadUnsubscribeNotLoaded), string(ThreadUnsubscribeNotSubscribed),
		string(ThreadUnsubscribeUnsubscribed),
	)
	schemas["TurnLifecycleStatus"] = stringEnumSchema(
		string(TurnLifecycleQueued), string(TurnLifecycleRunning), string(TurnLifecycleCompleted),
		string(TurnLifecycleFailed), string(TurnLifecycleInterrupted),
	)
	return schemas
}

func loginAccountParamsSchema() Schema {
	return Schema{"oneOf": []any{
		loginAccountVariantSchema(
			"apiKey",
			[]string{"apiKey", "type"},
			Schema{"apiKey": Schema{"type": "string"}},
		),
		loginAccountVariantSchema(
			"chatgpt",
			[]string{"type"},
			Schema{
				"codexStreamlinedLogin":     Schema{"type": "boolean"},
				"useHostedLoginSuccessPage": Schema{"type": "boolean"},
				"appBrand": Schema{
					"anyOf": []any{
						Schema{"$ref": "#/$defs/LoginAppBrand"},
						Schema{"type": "null"},
					},
					"default": nil,
				},
			},
		),
		loginAccountVariantSchema("chatgptDeviceCode", []string{"type"}, nil),
		loginAccountVariantSchema(
			"chatgptAuthTokens",
			[]string{"accessToken", "chatgptAccountId", "type"},
			Schema{
				"accessToken": Schema{
					"type": "string",
					"description": "Access token (JWT) supplied by the client. " +
						"This token is used for backend API requests and email extraction.",
				},
				"chatgptAccountId": Schema{
					"type":        "string",
					"description": "Workspace/account identifier supplied by the client.",
				},
				"chatgptPlanType": Schema{
					"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
					"description": "Optional plan type supplied by the client.\n\n" +
						"When `null`, Codex attempts to derive the plan type from access-token " +
						"claims. If unavailable, the plan defaults to `unknown`.",
				},
			},
			"[UNSTABLE] FOR OPENAI INTERNAL USE ONLY - DO NOT USE. "+
				"The access token must contain the same scopes that Codex-managed ChatGPT auth tokens have.",
		),
		loginAccountVariantSchema(
			"amazonBedrock",
			[]string{"apiKey", "region", "type"},
			Schema{
				"apiKey": Schema{"type": "string"},
				"region": Schema{"type": "string"},
			},
			"[UNSTABLE] Managed Amazon Bedrock login is experimental.",
		),
	}}
}

func loginAccountResponseSchema() Schema {
	return Schema{"oneOf": []any{
		loginAccountVariantSchema("apiKey", []string{"type"}, nil),
		loginAccountVariantSchema(
			"chatgpt",
			[]string{"authUrl", "loginId", "type"},
			Schema{
				"loginId": Schema{"type": "string"},
				"authUrl": Schema{
					"type":        "string",
					"description": "URL the client should open in a browser to initiate the OAuth flow.",
				},
			},
		),
		loginAccountVariantSchema(
			"chatgptDeviceCode",
			[]string{"loginId", "type", "userCode", "verificationUrl"},
			Schema{
				"loginId": Schema{"type": "string"},
				"verificationUrl": Schema{
					"type": "string",
					"description": "URL the client should open in a browser to complete " +
						"device code authorization.",
				},
				"userCode": Schema{
					"type":        "string",
					"description": "One-time code the user must enter after signing in.",
				},
			},
		),
		loginAccountVariantSchema("chatgptAuthTokens", []string{"type"}, nil),
		loginAccountVariantSchema("amazonBedrock", []string{"type"}, nil),
	}}
}

func loginAccountVariantSchema(
	typeName string,
	required []string,
	fields Schema,
	description ...string,
) Schema {
	properties := Schema{
		"type": Schema{"type": "string", "enum": []any{typeName}},
	}
	for name, schema := range fields {
		properties[name] = schema
	}
	variant := Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
	if len(description) > 0 {
		variant["description"] = description[0]
	}
	return variant
}

func codexErrorInfoSchema() Schema {
	variants := []any{stringEnumSchema(codexErrorInfoStrings...)}
	for _, name := range codexErrorHTTPVariants {
		variants = append(variants, threadTurnDependencyObjectVariant(name, Schema{
			"type": "object",
			"properties": Schema{
				"httpStatusCode": Schema{"anyOf": []any{
					Schema{"type": "integer", "minimum": 0, "maximum": 65535},
					Schema{"type": "null"},
				}},
			},
			"required":             []string{"httpStatusCode"},
			"additionalProperties": false,
		}))
	}
	variants = append(variants, threadTurnDependencyObjectVariant("activeTurnNotSteerable", Schema{
		"type": "object",
		"properties": Schema{
			"turnKind": Schema{"$ref": "#/$defs/NonSteerableTurnKind"},
		},
		"required":             []string{"turnKind"},
		"additionalProperties": false,
	}))
	return Schema{"oneOf": variants}
}

func threadStatusSchema() Schema {
	variants := make([]any, 0, 4)
	for _, statusType := range []string{"notLoaded", "idle", "systemError"} {
		variants = append(variants, Schema{
			"type": "object",
			"properties": Schema{
				"type": stringEnumSchema(statusType),
			},
			"required":             []string{"type"},
			"additionalProperties": false,
		})
	}
	variants = append(variants, Schema{
		"type": "object",
		"properties": Schema{
			"type": stringEnumSchema("active"),
			"activeFlags": Schema{
				"type":  "array",
				"items": Schema{"$ref": "#/$defs/ThreadActiveFlag"},
			},
		},
		"required":             []string{"type", "activeFlags"},
		"additionalProperties": false,
	})
	return Schema{"oneOf": variants}
}

func subAgentSourceSchema() Schema {
	return Schema{"oneOf": []any{
		stringEnumSchema("review", "compact", "memory_consolidation"),
		threadTurnDependencyObjectVariant("thread_spawn", Schema{
			"type": "object",
			"properties": Schema{
				"parent_thread_id": Schema{"$ref": "#/$defs/ThreadId"},
				"depth": Schema{
					"type": "integer", "minimum": -2147483648, "maximum": 2147483647,
				},
				"agent_path":     nullableSchemaRef("AgentPath"),
				"agent_nickname": nullableStringSchema(),
				"agent_role":     nullableStringSchema(),
			},
			"required": []string{
				"parent_thread_id", "depth", "agent_path", "agent_nickname", "agent_role",
			},
			"additionalProperties": false,
		}),
		threadTurnDependencyObjectVariant("other", Schema{"type": "string"}),
	}}
}

func sessionSourceSchema() Schema {
	return Schema{"oneOf": []any{
		stringEnumSchema("cli", "vscode", "exec", "appServer", "unknown"),
		threadTurnDependencyObjectVariant("custom", Schema{"type": "string"}),
		threadTurnDependencyObjectVariant("subAgent", Schema{"$ref": "#/$defs/SubAgentSource"}),
	}}
}

func threadTurnDependencyObjectVariant(name string, value Schema) Schema {
	return Schema{
		"type":                 "object",
		"properties":           Schema{name: value},
		"required":             []string{name},
		"additionalProperties": false,
	}
}

func commandActionSchema() Schema {
	return Schema{"oneOf": []any{
		commandActionVariantSchema("read", []string{"command", "name", "path"}, Schema{
			"command": Schema{"type": "string"},
			"name":    Schema{"type": "string"},
			"path":    Schema{"$ref": "#/$defs/AbsolutePathBuf"},
		}),
		commandActionVariantSchema("listFiles", []string{"command", "path"}, Schema{
			"command": Schema{"type": "string"},
			"path":    nullableStringSchema(),
		}),
		commandActionVariantSchema("search", []string{"command", "query", "path"}, Schema{
			"command": Schema{"type": "string"},
			"query":   nullableStringSchema(),
			"path":    nullableStringSchema(),
		}),
		commandActionVariantSchema("unknown", []string{"command"}, Schema{
			"command": Schema{"type": "string"},
		}),
	}}
}

func parsedCommandSchema() Schema {
	return Schema{"oneOf": []any{
		parsedCommandVariantSchema("read", []string{"cmd", "name", "path"}, Schema{
			"cmd":  Schema{"type": "string"},
			"name": Schema{"type": "string"},
			"path": Schema{
				"type": "string",
				"description": "(Best effort) Path to the file being read by the command. When " +
					"possible, this is an absolute path, though when relative, it should " +
					"be resolved against the `cwd`` that will be used to run the command " +
					"to derive the absolute path.",
			},
		}),
		parsedCommandVariantSchema("list_files", []string{"cmd"}, Schema{
			"cmd": Schema{"type": "string"}, "path": nullableStringSchema(),
		}),
		parsedCommandVariantSchema("search", []string{"cmd"}, Schema{
			"cmd": Schema{"type": "string"}, "query": nullableStringSchema(), "path": nullableStringSchema(),
		}),
		parsedCommandVariantSchema("unknown", []string{"cmd"}, Schema{
			"cmd": Schema{"type": "string"},
		}),
	}}
}

func parsedCommandVariantSchema(commandType string, requiredFields []string, fields Schema) Schema {
	properties := Schema{
		"type": Schema{"type": "string", "enum": []any{commandType}},
	}
	for name, schema := range fields {
		properties[name] = schema
	}
	required := []string{"type"}
	required = append(required, requiredFields...)
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func collabAgentStateSchema() Schema {
	return Schema{
		"type": "object",
		"properties": Schema{
			"status":  Schema{"$ref": "#/$defs/CollabAgentStatus"},
			"message": nullableStringSchema(),
		},
		"required":             []string{"status", "message"},
		"additionalProperties": false,
	}
}

func rawResponseContentSchema(variants []rawResponseContentVariant) Schema {
	oneOf := make([]any, 0, len(variants))
	for _, variant := range variants {
		oneOf = append(oneOf, rawResponseContentVariantSchema(variant))
	}
	return Schema{"oneOf": oneOf}
}

func rawResponseContentVariantSchema(variant rawResponseContentVariant) Schema {
	return Schema{
		"type": "object",
		"properties": Schema{
			"type":        Schema{"type": "string", "enum": []any{variant.contentType}},
			variant.field: Schema{"type": "string"},
		},
		"required":             []string{"type", variant.field},
		"additionalProperties": false,
	}
}

func responsesAPIWebSearchActionSchema() Schema {
	return Schema{"oneOf": []any{
		responsesAPIWebSearchActionVariantSchema("search", Schema{
			"query": Schema{"type": "string"},
			"queries": Schema{
				"type": "array", "items": Schema{"type": "string"},
			},
		}),
		responsesAPIWebSearchActionVariantSchema("open_page", Schema{
			"url": Schema{"type": "string"},
		}),
		responsesAPIWebSearchActionVariantSchema("find_in_page", Schema{
			"url":     Schema{"type": "string"},
			"pattern": Schema{"type": "string"},
		}),
		responsesAPIWebSearchActionVariantSchema("other", nil),
	}}
}

func responsesAPIWebSearchActionVariantSchema(actionType string, fields Schema) Schema {
	properties := Schema{
		"type": Schema{"type": "string", "enum": []any{actionType}},
	}
	for name, schema := range fields {
		properties[name] = schema
	}
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             []string{"type"},
		"additionalProperties": false,
	}
}

func contentItemSchema(variants []contentItemVariant) Schema {
	items := make([]any, 0, len(variants))
	for _, variant := range variants {
		properties := Schema{
			"type":        Schema{"type": "string", "enum": []any{variant.itemType}},
			variant.field: Schema{"type": "string"},
		}
		if variant.optionalDetail {
			properties["detail"] = Schema{"$ref": "#/$defs/ImageDetail"}
		}
		items = append(items, Schema{
			"type":                 "object",
			"properties":           properties,
			"required":             []string{"type", variant.field},
			"additionalProperties": false,
		})
	}
	return Schema{"oneOf": items}
}

func functionCallOutputBodySchema() Schema {
	return Schema{"anyOf": []any{
		Schema{"type": "string"},
		Schema{"type": "array", "items": Schema{"$ref": "#/$defs/FunctionCallOutputContentItem"}},
	}}
}

func internalChatMessageMetadataSchema() Schema {
	return Schema{
		"type": "object",
		"properties": Schema{
			"turn_id": Schema{"type": "string"},
		},
		"additionalProperties": false,
	}
}

func localShellActionSchema() Schema {
	return Schema{"oneOf": []any{Schema{
		"type": "object",
		"properties": Schema{
			"type":              Schema{"type": "string", "enum": []any{"exec"}},
			"command":           Schema{"type": "array", "items": Schema{"type": "string"}},
			"timeout_ms":        nullableUnsignedIntegerSchema(),
			"working_directory": nullableStringSchema(),
			"env": Schema{"anyOf": []any{
				Schema{"type": "object", "additionalProperties": Schema{"type": "string"}},
				Schema{"type": "null"},
			}},
			"user": nullableStringSchema(),
		},
		"required":             []string{"type", "command", "timeout_ms", "working_directory", "env", "user"},
		"additionalProperties": false,
	}}}
}

func nullableUnsignedIntegerSchema() Schema {
	return Schema{"anyOf": []any{Schema{"type": "integer", "minimum": 0}, Schema{"type": "null"}}}
}

func jsonValueSchema() Schema {
	return Schema{"anyOf": []any{
		Schema{"type": "number"},
		Schema{"type": "string"},
		Schema{"type": "boolean"},
		Schema{"type": "array", "items": Schema{"$ref": "#/$defs/JsonValue"}},
		Schema{
			"type": "object", "additionalProperties": Schema{"$ref": "#/$defs/JsonValue"},
			"x-gollem-typescript-recursive-map": true,
		},
		Schema{"type": "null"},
	}}
}

func responseItemSchema() Schema {
	return Schema{"oneOf": []any{
		responseItemVariantSchema("message", []string{"role", "content"}, Schema{
			"role":    Schema{"type": "string"},
			"content": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/ContentItem"}},
			"phase":   Schema{"$ref": "#/$defs/MessagePhase"},
		}, true),
		responseItemVariantSchema("agent_message", []string{"author", "recipient", "content"}, Schema{
			"author":    Schema{"type": "string"},
			"recipient": Schema{"type": "string"},
			"content":   Schema{"type": "array", "items": Schema{"$ref": "#/$defs/AgentMessageInputContent"}},
		}, true),
		responseItemVariantSchema("reasoning", []string{"summary", "encrypted_content"}, Schema{
			"summary":           Schema{"type": "array", "items": Schema{"$ref": "#/$defs/ReasoningItemReasoningSummary"}},
			"content":           Schema{"type": "array", "items": Schema{"$ref": "#/$defs/ReasoningItemContent"}},
			"encrypted_content": nullableStringSchema(),
		}, true),
		responseItemVariantSchema("local_shell_call", []string{"call_id", "status", "action"}, Schema{
			"call_id": nullableStringSchema(),
			"status":  Schema{"$ref": "#/$defs/LocalShellStatus"},
			"action":  Schema{"$ref": "#/$defs/LocalShellAction"},
		}, true),
		responseItemVariantSchema("function_call", []string{"name", "arguments", "call_id"}, Schema{
			"name":      Schema{"type": "string"},
			"namespace": Schema{"type": "string"},
			"arguments": Schema{"type": "string"},
			"call_id":   Schema{"type": "string"},
		}, true),
		responseItemVariantSchema("tool_search_call", []string{"call_id", "execution", "arguments"}, Schema{
			"call_id":   nullableStringSchema(),
			"status":    Schema{"type": "string"},
			"execution": Schema{"type": "string"},
			"arguments": Schema{},
		}, true),
		responseItemVariantSchema("function_call_output", []string{"call_id", "output"}, Schema{
			"call_id": Schema{"type": "string"},
			"output":  Schema{"$ref": "#/$defs/FunctionCallOutputBody"},
		}, true),
		responseItemVariantSchema("custom_tool_call", []string{"call_id", "name", "input"}, Schema{
			"status":    Schema{"type": "string"},
			"call_id":   Schema{"type": "string"},
			"name":      Schema{"type": "string"},
			"namespace": Schema{"type": "string"},
			"input":     Schema{"type": "string"},
		}, true),
		responseItemVariantSchema("custom_tool_call_output", []string{"call_id", "output"}, Schema{
			"call_id": Schema{"type": "string"},
			"name":    Schema{"type": "string"},
			"output":  Schema{"$ref": "#/$defs/FunctionCallOutputBody"},
		}, true),
		responseItemVariantSchema("tool_search_output", []string{"call_id", "status", "execution", "tools"}, Schema{
			"call_id":   nullableStringSchema(),
			"status":    Schema{"type": "string"},
			"execution": Schema{"type": "string"},
			"tools":     Schema{"type": "array", "items": Schema{}},
		}, true),
		responseItemVariantSchema("web_search_call", nil, Schema{
			"status": Schema{"type": "string"},
			"action": Schema{"$ref": "#/$defs/WebSearchAction"},
		}, true),
		responseItemVariantSchema("image_generation_call", []string{"status", "result"}, Schema{
			"status":         Schema{"type": "string"},
			"revised_prompt": Schema{"type": "string"},
			"result":         Schema{"type": "string"},
		}, true),
		responseItemVariantSchema("compaction", []string{"encrypted_content"}, Schema{
			"encrypted_content": Schema{"type": "string"},
		}, true),
		responseItemVariantSchema("compaction_trigger", nil, nil, false),
		responseItemVariantSchema("context_compaction", nil, Schema{
			"encrypted_content": Schema{"type": "string"},
		}, true),
		responseItemVariantSchema("other", nil, nil, false),
	}}
}

func threadItemSchema() Schema {
	return Schema{"oneOf": []any{
		threadItemVariantSchema("userMessage", []string{"id", "clientId", "content"}, Schema{
			"id":       Schema{"type": "string"},
			"clientId": nullableStringSchema(),
			"content":  schemaArrayRef("UserInput"),
		}),
		threadItemVariantSchema("hookPrompt", []string{"id", "fragments"}, Schema{
			"id":        Schema{"type": "string"},
			"fragments": schemaArrayRef("HookPromptFragment"),
		}),
		threadItemVariantSchema("agentMessage", []string{"id", "text", "phase", "memoryCitation"}, Schema{
			"id":             Schema{"type": "string"},
			"text":           Schema{"type": "string"},
			"phase":          nullableSchemaRef("MessagePhase"),
			"memoryCitation": nullableSchemaRef("MemoryCitation"),
		}),
		threadItemVariantSchema("plan", []string{"id", "text"}, Schema{
			"id":   Schema{"type": "string"},
			"text": Schema{"type": "string"},
		}),
		threadItemVariantSchema("reasoning", []string{"id", "summary", "content"}, Schema{
			"id":      Schema{"type": "string"},
			"summary": Schema{"type": "array", "items": Schema{"type": "string"}},
			"content": Schema{"type": "array", "items": Schema{"type": "string"}},
		}),
		threadItemVariantSchema(
			"commandExecution",
			[]string{
				"id", "command", "cwd", "processId", "source", "status", "commandActions",
				"aggregatedOutput", "exitCode", "durationMs",
			},
			Schema{
				"id":               Schema{"type": "string"},
				"command":          Schema{"type": "string"},
				"cwd":              Schema{"$ref": "#/$defs/LegacyAppPathString"},
				"processId":        nullableStringSchema(),
				"source":           Schema{"$ref": "#/$defs/CommandExecutionSource"},
				"status":           Schema{"$ref": "#/$defs/CommandExecutionStatus"},
				"commandActions":   schemaArrayRef("CommandAction"),
				"aggregatedOutput": nullableStringSchema(),
				"exitCode":         nullableIntegerSchema(),
				"durationMs":       nullableIntegerSchema(),
			},
		),
		threadItemVariantSchema("fileChange", []string{"id", "changes", "status"}, Schema{
			"id":      Schema{"type": "string"},
			"changes": schemaArrayRef("FileUpdateChange"),
			"status":  Schema{"$ref": "#/$defs/PatchApplyStatus"},
		}),
		threadItemVariantSchema(
			"mcpToolCall",
			[]string{
				"id", "server", "tool", "status", "arguments", "appContext", "pluginId",
				"result", "error", "durationMs",
			},
			Schema{
				"id":                Schema{"type": "string"},
				"server":            Schema{"type": "string"},
				"tool":              Schema{"type": "string"},
				"status":            Schema{"$ref": "#/$defs/McpToolCallStatus"},
				"arguments":         Schema{"$ref": "#/$defs/JsonValue"},
				"appContext":        nullableSchemaRef("McpToolCallAppContext"),
				"mcpAppResourceUri": Schema{"type": "string"},
				"pluginId":          nullableStringSchema(),
				"result":            nullableSchemaRef("McpToolCallResult"),
				"error":             nullableSchemaRef("McpToolCallError"),
				"durationMs":        nullableIntegerSchema(),
			},
		),
		threadItemVariantSchema(
			"dynamicToolCall",
			[]string{"id", "namespace", "tool", "arguments", "status", "contentItems", "success", "durationMs"},
			Schema{
				"id":           Schema{"type": "string"},
				"namespace":    nullableStringSchema(),
				"tool":         Schema{"type": "string"},
				"arguments":    Schema{"$ref": "#/$defs/JsonValue"},
				"status":       Schema{"$ref": "#/$defs/DynamicToolCallStatus"},
				"contentItems": nullableArrayRef("DynamicToolCallOutputContentItem"),
				"success":      nullableBooleanSchema(),
				"durationMs":   nullableIntegerSchema(),
			},
		),
		threadItemVariantSchema(
			"collabAgentToolCall",
			[]string{
				"id", "tool", "status", "senderThreadId", "receiverThreadIds", "prompt", "model",
				"reasoningEffort", "agentsStates",
			},
			Schema{
				"id":                Schema{"type": "string"},
				"tool":              Schema{"$ref": "#/$defs/CollabAgentTool"},
				"status":            Schema{"$ref": "#/$defs/CollabAgentToolCallStatus"},
				"senderThreadId":    Schema{"type": "string"},
				"receiverThreadIds": Schema{"type": "array", "items": Schema{"type": "string"}},
				"prompt":            nullableStringSchema(),
				"model":             nullableStringSchema(),
				"reasoningEffort":   nullableSchemaRef("ReasoningEffort"),
				"agentsStates": Schema{
					"type": "object", "additionalProperties": Schema{"$ref": "#/$defs/CollabAgentState"},
					"x-gollem-typescript-optional-map": true,
				},
			},
		),
		threadItemVariantSchema("subAgentActivity", []string{"id", "kind", "agentThreadId", "agentPath"}, Schema{
			"id":            Schema{"type": "string"},
			"kind":          Schema{"$ref": "#/$defs/SubAgentActivityKind"},
			"agentThreadId": Schema{"type": "string"},
			"agentPath":     Schema{"type": "string"},
		}),
		threadItemVariantSchema("webSearch", []string{"id", "query", "action"}, Schema{
			"id":     Schema{"type": "string"},
			"query":  Schema{"type": "string"},
			"action": nullableSchemaRef("WebSearchAction"),
		}),
		threadItemVariantSchema("imageView", []string{"id", "path"}, Schema{
			"id":   Schema{"type": "string"},
			"path": Schema{"$ref": "#/$defs/LegacyAppPathString"},
		}),
		threadItemVariantSchema("sleep", []string{"id", "durationMs"}, Schema{
			"id":         Schema{"type": "string"},
			"durationMs": Schema{"type": "integer", "minimum": 0},
		}),
		threadItemVariantSchema("imageGeneration", []string{"id", "status", "revisedPrompt", "result"}, Schema{
			"id":            Schema{"type": "string"},
			"status":        Schema{"type": "string"},
			"revisedPrompt": nullableStringSchema(),
			"result":        Schema{"type": "string"},
			"savedPath":     Schema{"$ref": "#/$defs/AbsolutePathBuf"},
		}),
		threadItemVariantSchema("enteredReviewMode", []string{"id", "review"}, Schema{
			"id":     Schema{"type": "string"},
			"review": Schema{"type": "string"},
		}),
		threadItemVariantSchema("exitedReviewMode", []string{"id", "review"}, Schema{
			"id":     Schema{"type": "string"},
			"review": Schema{"type": "string"},
		}),
		threadItemVariantSchema("contextCompaction", []string{"id"}, Schema{
			"id": Schema{"type": "string"},
		}),
	}}
}

func threadItemVariantSchema(itemType string, requiredFields []string, fields Schema) Schema {
	properties := Schema{
		"type": Schema{"type": "string", "enum": []any{itemType}},
	}
	for name, schema := range fields {
		properties[name] = schema
	}
	required := []string{"type"}
	required = append(required, requiredFields...)
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func schemaArrayRef(name string) Schema {
	return Schema{"type": "array", "items": Schema{"$ref": "#/$defs/" + name}}
}

func nullableArrayRef(name string) Schema {
	return Schema{"anyOf": []any{schemaArrayRef(name), Schema{"type": "null"}}}
}

func nullableSchemaRef(name string) Schema {
	return Schema{"anyOf": []any{Schema{"$ref": "#/$defs/" + name}, Schema{"type": "null"}}}
}

func nullableIntegerSchema() Schema {
	return Schema{"anyOf": []any{Schema{"type": "integer"}, Schema{"type": "null"}}}
}

func nullableBooleanSchema() Schema {
	return Schema{"anyOf": []any{Schema{"type": "boolean"}, Schema{"type": "null"}}}
}

func responseItemVariantSchema(itemType string, requiredFields []string, fields Schema, common bool) Schema {
	properties := Schema{
		"type": Schema{"type": "string", "enum": []any{itemType}},
	}
	if common {
		properties["id"] = Schema{"type": "string"}
		properties[responseItemMetadataField] = Schema{"$ref": "#/$defs/InternalChatMessageMetadataPassthrough"}
	}
	for name, schema := range fields {
		properties[name] = schema
	}
	required := []string{"type"}
	required = append(required, requiredFields...)
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func commandActionVariantSchema(actionType string, requiredFields []string, fields Schema) Schema {
	properties := Schema{
		"type": Schema{"type": "string", "enum": []any{actionType}},
	}
	for name, schema := range fields {
		properties[name] = schema
	}
	required := []string{"type"}
	required = append(required, requiredFields...)
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func webSearchActionSchema() Schema {
	return Schema{"oneOf": []any{
		webSearchActionVariantSchema("search", []string{"query", "queries"}, Schema{
			"query": nullableStringSchema(),
			"queries": Schema{"oneOf": []any{
				Schema{"type": "array", "items": Schema{"type": "string"}},
				Schema{"type": "null"},
			}},
		}),
		webSearchActionVariantSchema("openPage", []string{"url"}, Schema{
			"url": nullableStringSchema(),
		}),
		webSearchActionVariantSchema("findInPage", []string{"url", "pattern"}, Schema{
			"url":     nullableStringSchema(),
			"pattern": nullableStringSchema(),
		}),
		webSearchActionVariantSchema("other", nil, nil),
	}}
}

func webSearchActionVariantSchema(actionType string, requiredFields []string, fields Schema) Schema {
	properties := Schema{
		"type": Schema{"type": "string", "enum": []any{actionType}},
	}
	for name, schema := range fields {
		properties[name] = schema
	}
	required := []string{"type"}
	required = append(required, requiredFields...)
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func configuredHookHandlerSchema() Schema {
	return Schema{"oneOf": []any{
		configuredHookHandlerVariantSchema("command", []string{
			"command", "commandWindows", "timeoutSec", "async", "statusMessage",
		}, Schema{
			"command":        Schema{"type": "string"},
			"commandWindows": nullableStringSchema(),
			"timeoutSec": Schema{"anyOf": []any{
				Schema{"type": "integer", "minimum": 0},
				Schema{"type": "null"},
			}},
			"async":         Schema{"type": "boolean"},
			"statusMessage": nullableStringSchema(),
		}),
		configuredHookHandlerVariantSchema("prompt", nil, nil),
		configuredHookHandlerVariantSchema("agent", nil, nil),
	}}
}

func configPrerequisiteSchemas() map[string]Schema {
	nullable := func(value Schema) Schema {
		return Schema{"anyOf": []any{value, Schema{"type": "null"}}}
	}
	return map[string]Schema{
		"AnalyticsConfig": {
			"type": "object",
			"properties": Schema{
				"enabled": nullable(Schema{"type": "boolean"}),
			},
			"required":                         []string{"enabled"},
			"additionalProperties":             Schema{"$ref": "#/$defs/JsonValue"},
			"x-gollem-typescript-optional-map": true,
		},
		"AutoCompactTokenLimitScope": {
			"description": "Selects which part of the active context is charged against `model_auto_compact_token_limit`.",
			"oneOf": []any{
				Schema{
					"type":        "string",
					"enum":        []any{"total"},
					"description": "Count the full active context against the limit.",
				},
				Schema{
					"type":        "string",
					"enum":        []any{"body_after_prefix"},
					"description": "Count sampled output and later growth after the carried window prefix.",
				},
			},
		},
		"ForcedChatgptWorkspaceIds": {
			"anyOf": []any{
				Schema{"type": "string"},
				Schema{"type": "array", "items": Schema{"type": "string"}},
			},
			"description": "Backward-compatible API shape for ChatGPT workspace login restrictions.",
		},
		"ForcedLoginMethod": stringEnumSchema("chatgpt", "api"),
		"SandboxWorkspaceWrite": {
			"type": "object",
			"properties": Schema{
				"writable_roots": Schema{
					"type": "array", "items": Schema{"type": "string"}, "default": []any{},
				},
				"network_access":         Schema{"type": "boolean", "default": false},
				"exclude_tmpdir_env_var": Schema{"type": "boolean", "default": false},
				"exclude_slash_tmp":      Schema{"type": "boolean", "default": false},
			},
			"required": []string{
				"writable_roots", "network_access", "exclude_tmpdir_env_var", "exclude_slash_tmp",
			},
			"additionalProperties":                             true,
			"x-gollem-typescript-ignore-additional-properties": true,
		},
		"ToolsV2": {
			"type": "object",
			"properties": Schema{
				"web_search": nullable(Schema{"$ref": "#/$defs/WebSearchToolConfig"}),
			},
			"required":             []string{"web_search"},
			"additionalProperties": true,
			"x-gollem-typescript-ignore-additional-properties": true,
		},
		"Verbosity": {
			"type":        "string",
			"enum":        []any{"low", "medium", "high"},
			"description": "Controls output length/detail on GPT-5 models via the Responses API. Serialized with lowercase values to match the OpenAI API.",
		},
		"WebSearchContextSize": stringEnumSchema("low", "medium", "high"),
		"WebSearchLocation": {
			"type": "object",
			"properties": Schema{
				"country":  nullable(Schema{"type": "string"}),
				"region":   nullable(Schema{"type": "string"}),
				"city":     nullable(Schema{"type": "string"}),
				"timezone": nullable(Schema{"type": "string"}),
			},
			"required":             []string{"country", "region", "city", "timezone"},
			"additionalProperties": false,
		},
		"WebSearchToolConfig": {
			"type": "object",
			"properties": Schema{
				"context_size": nullable(Schema{"$ref": "#/$defs/WebSearchContextSize"}),
				"allowed_domains": nullable(Schema{
					"type": "array", "items": Schema{"type": "string"},
				}),
				"location": nullable(Schema{"$ref": "#/$defs/WebSearchLocation"}),
			},
			"required":             []string{"context_size", "allowed_domains", "location"},
			"additionalProperties": false,
		},
	}
}

func publicConfigSchema() Schema {
	nullable := func(value Schema) Schema {
		return Schema{"anyOf": []any{value, Schema{"type": "null"}}}
	}
	approvalsReviewer := nullable(Schema{"$ref": "#/$defs/ApprovalsReviewer"})
	approvalsReviewer["description"] =
		"[UNSTABLE] Optional default for where approval requests are routed for review."
	return Schema{
		"type": "object",
		"properties": Schema{
			"model":                                nullable(Schema{"type": "string"}),
			"review_model":                         nullable(Schema{"type": "string"}),
			"model_context_window":                 nullable(Schema{"type": "integer"}),
			"model_auto_compact_token_limit":       nullable(Schema{"type": "integer"}),
			"model_auto_compact_token_limit_scope": nullable(Schema{"$ref": "#/$defs/AutoCompactTokenLimitScope"}),
			"model_provider":                       nullable(Schema{"type": "string"}),
			"approval_policy":                      nullable(Schema{"$ref": "#/$defs/AskForApproval"}),
			"approvals_reviewer":                   approvalsReviewer,
			"sandbox_mode":                         nullable(Schema{"$ref": "#/$defs/SandboxMode"}),
			"sandbox_workspace_write":              nullable(Schema{"$ref": "#/$defs/SandboxWorkspaceWrite"}),
			"forced_chatgpt_workspace_id":          nullable(Schema{"$ref": "#/$defs/ForcedChatgptWorkspaceIds"}),
			"forced_login_method":                  nullable(Schema{"$ref": "#/$defs/ForcedLoginMethod"}),
			"web_search":                           nullable(Schema{"$ref": "#/$defs/WebSearchMode"}),
			"tools":                                nullable(Schema{"$ref": "#/$defs/ToolsV2"}),
			"instructions":                         nullable(Schema{"type": "string"}),
			"developer_instructions":               nullable(Schema{"type": "string"}),
			"compact_prompt":                       nullable(Schema{"type": "string"}),
			"model_reasoning_effort":               nullable(Schema{"$ref": "#/$defs/ReasoningEffort"}),
			"model_reasoning_summary":              nullable(Schema{"$ref": "#/$defs/ReasoningSummary"}),
			"model_verbosity":                      nullable(Schema{"$ref": "#/$defs/Verbosity"}),
			"service_tier":                         nullable(Schema{"type": "string"}),
			"analytics":                            nullable(Schema{"$ref": "#/$defs/AnalyticsConfig"}),
			"desktop": nullable(Schema{
				"type":                             "object",
				"additionalProperties":             Schema{"$ref": "#/$defs/JsonValue"},
				"x-gollem-typescript-optional-map": true,
			}),
		},
		"required":                         append([]string(nil), publicConfigKnownFields...),
		"additionalProperties":             Schema{"$ref": "#/$defs/JsonValue"},
		"x-gollem-typescript-optional-map": true,
	}
}

func configReadResponseSchema() Schema {
	nullable := func(value Schema) Schema {
		return Schema{"anyOf": []any{value, Schema{"type": "null"}}}
	}
	return Schema{
		"type": "object",
		"properties": Schema{
			"config": Schema{"$ref": "#/$defs/Config"},
			"origins": Schema{
				"type":                             "object",
				"additionalProperties":             Schema{"$ref": "#/$defs/ConfigLayerMetadata"},
				"x-gollem-typescript-optional-map": true,
			},
			"layers": nullable(Schema{
				"type":  "array",
				"items": Schema{"$ref": "#/$defs/ConfigLayer"},
			}),
		},
		"required":             []string{"config", "origins", "layers"},
		"additionalProperties": true,
		"x-gollem-typescript-ignore-additional-properties": true,
	}
}

func additionalContextEntrySchema() Schema {
	return Schema{
		"type": "object",
		"properties": Schema{
			"value": Schema{"type": "string"},
			"kind":  Schema{"$ref": "#/$defs/AdditionalContextKind"},
		},
		"required":             []string{"kind", "value"},
		"additionalProperties": true,
		"x-gollem-typescript-ignore-additional-properties": true,
	}
}

func configLayerSourceSchema() Schema {
	absolutePath := Schema{"$ref": "#/$defs/AbsolutePathBuf"}
	return Schema{"oneOf": []any{
		configLayerSourceVariant("mdm", []string{"domain", "key"}, Schema{
			"domain": Schema{"type": "string"},
			"key":    Schema{"type": "string"},
		}),
		configLayerSourceVariant("system", []string{"file"}, Schema{
			"file": absolutePath,
		}),
		configLayerSourceVariant("enterpriseManaged", []string{"id", "name"}, Schema{
			"id":   Schema{"type": "string"},
			"name": Schema{"type": "string"},
		}),
		configLayerSourceVariant("user", []string{"file", "profile"}, Schema{
			"file": absolutePath,
			"profile": Schema{"anyOf": []any{
				Schema{"type": "string"},
				Schema{"type": "null"},
			}},
		}),
		configLayerSourceVariant("project", []string{"dotCodexFolder"}, Schema{
			"dotCodexFolder": absolutePath,
		}),
		configLayerSourceVariant("sessionFlags", nil, nil),
		configLayerSourceVariant("legacyManagedConfigTomlFromFile", []string{"file"}, Schema{
			"file": absolutePath,
		}),
		configLayerSourceVariant("legacyManagedConfigTomlFromMdm", nil, nil),
	}}
}

func configLayerSourceVariant(typeName string, requiredFields []string, fields Schema) Schema {
	properties := Schema{"type": stringEnumSchema(typeName)}
	for name, schema := range fields {
		properties[name] = schema
	}
	required := []string{"type"}
	required = append(required, requiredFields...)
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func configuredHookHandlerVariantSchema(hookType string, requiredFields []string, fields Schema) Schema {
	properties := Schema{
		"type": Schema{"type": "string", "enum": []any{hookType}},
	}
	for name, schema := range fields {
		properties[name] = schema
	}
	required := []string{"type"}
	required = append(required, requiredFields...)
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func userInputSchema() Schema {
	return Schema{"oneOf": []any{
		userInputVariantSchema("text", []string{"text", "text_elements"}, Schema{
			"text": Schema{"type": "string"},
			"text_elements": Schema{
				"type":  "array",
				"items": Schema{"$ref": "#/$defs/TextElement"},
			},
		}),
		userInputVariantSchema("image", []string{"url"}, Schema{
			"detail": Schema{"$ref": "#/$defs/ImageDetail"},
			"url":    Schema{"type": "string"},
		}),
		userInputVariantSchema("localImage", []string{"path"}, Schema{
			"detail": Schema{"$ref": "#/$defs/ImageDetail"},
			"path":   Schema{"type": "string"},
		}),
		userInputVariantSchema("skill", []string{"name", "path"}, Schema{
			"name": Schema{"type": "string"},
			"path": Schema{"type": "string"},
		}),
		userInputVariantSchema("mention", []string{"name", "path"}, Schema{
			"name": Schema{"type": "string"},
			"path": Schema{"type": "string"},
		}),
	}}
}

func userInputVariantSchema(inputType string, requiredFields []string, fields Schema) Schema {
	properties := Schema{
		"type": Schema{"type": "string", "enum": []any{inputType}},
	}
	for name, schema := range fields {
		properties[name] = schema
	}
	required := []string{"type"}
	required = append(required, requiredFields...)
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func fileSystemSpecialPathSchema() Schema {
	return Schema{"oneOf": []any{
		fileSystemSpecialPathVariantSchema("root", false, false),
		fileSystemSpecialPathVariantSchema("minimal", false, false),
		fileSystemSpecialPathVariantSchema("project_roots", false, true),
		fileSystemSpecialPathVariantSchema("tmpdir", false, false),
		fileSystemSpecialPathVariantSchema("slash_tmp", false, false),
		fileSystemSpecialPathVariantSchema("unknown", true, true),
	}}
}

func fileSystemSpecialPathVariantSchema(kind string, includePath, includeSubpath bool) Schema {
	properties := Schema{
		"kind": Schema{"type": "string", "enum": []any{kind}},
	}
	required := []string{"kind"}
	if includePath {
		properties["path"] = Schema{"type": "string"}
		required = append(required, "path")
	}
	if includeSubpath {
		properties["subpath"] = nullableStringSchema()
		required = append(required, "subpath")
	}
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func fileSystemPathSchema() Schema {
	return Schema{"oneOf": []any{
		fileSystemPathVariantSchema("path", "path", Schema{"$ref": "#/$defs/LegacyAppPathString"}),
		fileSystemPathVariantSchema("glob_pattern", "pattern", Schema{"type": "string"}),
		fileSystemPathVariantSchema("special", "value", Schema{"$ref": "#/$defs/FileSystemSpecialPath"}),
	}}
}

func fileSystemPathVariantSchema(pathType, valueField string, valueSchema Schema) Schema {
	return Schema{
		"type": "object",
		"properties": Schema{
			"type":     Schema{"type": "string", "enum": []any{pathType}},
			valueField: valueSchema,
		},
		"required":             []string{"type", valueField},
		"additionalProperties": false,
	}
}

func patchChangeKindSchema() Schema {
	return Schema{"oneOf": []any{
		patchChangeKindVariantSchema("add", ""),
		patchChangeKindVariantSchema("delete", ""),
		patchChangeKindVariantSchema("update", "move_path"),
		patchChangeKindVariantSchema("update", "movePath"),
	}}
}

func fileChangeSchema() Schema {
	return Schema{"oneOf": []any{
		fileChangeVariantSchema("add", []string{"content", "type"}, Schema{
			"content": Schema{"type": "string"},
		}),
		fileChangeVariantSchema("delete", []string{"content", "type"}, Schema{
			"content": Schema{"type": "string"},
		}),
		fileChangeVariantSchema("update", []string{"type", "unified_diff"}, Schema{
			"unified_diff": Schema{"type": "string"},
			"move_path":    Schema{"type": []any{"string", "null"}},
		}),
	}}
}

func fileChangeVariantSchema(changeType string, requiredFields []string, fields Schema) Schema {
	properties := Schema{"type": Schema{"type": "string", "enum": []any{changeType}}}
	for name, schema := range fields {
		properties[name] = schema
	}
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             append([]string(nil), requiredFields...),
		"additionalProperties": false,
	}
}

func dynamicToolCallOutputContentItemSchema() Schema {
	return Schema{"oneOf": []any{
		dynamicToolCallOutputContentItemVariantSchema("inputText", "text"),
		dynamicToolCallOutputContentItemVariantSchema("inputImage", "imageUrl"),
		dynamicToolCallOutputContentItemVariantSchema("inputAudio", "audioUrl"),
	}}
}

func serverRequestSchema() Schema {
	variants := make([]any, 0, len(serverRequestVariants))
	for _, variant := range serverRequestVariants {
		properties := Schema{
			"id":     Schema{"$ref": "#/$defs/RequestId"},
			"method": Schema{"enum": []any{variant.Method}, "title": variant.Title + "Method", "type": "string"},
			"params": Schema{"$ref": "#/$defs/" + variant.ParamType},
		}
		item := Schema{
			"properties": properties,
			"required":   []string{"id", "method", "params"},
			"title":      variant.Title,
			"type":       "object",
		}
		if variant.Description != "" {
			item["description"] = variant.Description
		}
		variants = append(variants, item)
	}
	return Schema{"oneOf": variants, "title": "ServerRequest"}
}

func dynamicToolCallParamsSchema() Schema {
	return Schema{
		"type":  "object",
		"title": "DynamicToolCallParams",
		"properties": Schema{
			"threadId":  Schema{"type": "string"},
			"turnId":    Schema{"type": "string"},
			"callId":    Schema{"type": "string"},
			"namespace": Schema{"type": []any{"string", "null"}},
			"tool":      Schema{"type": "string"},
			"arguments": true,
		},
		"required": []string{"arguments", "callId", "threadId", "tool", "turnId"},
	}
}

func dynamicToolCallResponseSchema() Schema {
	return Schema{
		"type":  "object",
		"title": "DynamicToolCallResponse",
		"properties": Schema{
			"contentItems": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/DynamicToolCallOutputContentItem"}},
			"success":      Schema{"type": "boolean"},
		},
		"required": []string{"contentItems", "success"},
	}
}

func dynamicToolCallOutputContentItemVariantSchema(contentType, valueField string) Schema {
	variantName := ""
	typeName := ""
	switch contentType {
	case "inputText":
		variantName, typeName = "InputTextDynamicToolCallOutputContentItem", "InputTextDynamicToolCallOutputContentItemType"
	case "inputImage":
		variantName, typeName = "InputImageDynamicToolCallOutputContentItem", "InputImageDynamicToolCallOutputContentItemType"
	case "inputAudio":
		variantName, typeName = "InputAudioDynamicToolCallOutputContentItem", "InputAudioDynamicToolCallOutputContentItemType"
	}
	return Schema{
		"type": "object",
		"properties": Schema{
			"type":     Schema{"type": "string", "enum": []any{contentType}, "title": typeName},
			valueField: Schema{"type": "string"},
		},
		"required": []string{valueField, "type"},
		"title":    variantName,
	}
}

func threadStartParamsSchema() Schema {
	properties := threadSessionParamCommonSchemaProperties()
	properties["serviceName"] = nullableThreadSessionParamSchema(Schema{"type": "string"})
	properties["personality"] = nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/Personality"})
	properties["ephemeral"] = nullableThreadSessionParamSchema(Schema{"type": "boolean"})
	properties["sessionStartSource"] = nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/ThreadStartSource"})
	properties["threadSource"] = nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/ThreadSource"})
	return closedThreadSessionParamSchema(properties, nil)
}

func threadResumeParamsSchema() Schema {
	properties := threadSessionParamCommonSchemaProperties()
	properties["threadId"] = Schema{"type": "string"}
	properties["personality"] = nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/Personality"})
	return closedThreadSessionParamSchema(properties, []string{"threadId"})
}

func threadResumeInitialTurnsPageParamsSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"limit": nullableThreadSessionParamSchema(Schema{"type": "integer", "minimum": 0}),
		"sortDirection": nullableThreadSessionParamSchema(
			Schema{"$ref": "#/$defs/SortDirection"},
		),
		"itemsView": nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/TurnItemsView"}),
	}, nil)
}

func modelListParamsSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"cursor": Schema{
			"description": "Opaque pagination cursor returned by a previous call.",
			"anyOf":       []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
		"limit": Schema{
			"description": "Optional page size; defaults to a reasonable server-side value.",
			"anyOf": []any{
				Schema{"type": "integer", "minimum": 0, "maximum": 4294967295},
				Schema{"type": "null"},
			},
		},
		"includeHidden": Schema{
			"description": "When true, include models that are hidden from the default picker list.",
			"anyOf":       []any{Schema{"type": "boolean"}, Schema{"type": "null"}},
		},
	}, nil)
}

func listMcpServerStatusParamsSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"cursor": Schema{
			"description": "Opaque pagination cursor returned by a previous call.",
			"anyOf":       []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
		"limit": Schema{
			"description": "Optional page size; defaults to a server-defined value.",
			"anyOf": []any{
				Schema{"type": "integer", "minimum": 0, "maximum": 4294967295},
				Schema{"type": "null"},
			},
		},
		"detail": Schema{
			"description": "Controls how much MCP inventory data to fetch for each server. Defaults to `Full` when omitted.",
			"anyOf": []any{
				Schema{"$ref": "#/$defs/McpServerStatusDetail"},
				Schema{"type": "null"},
			},
		},
		"threadId": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
	}, nil)
}

func mcpResourceReadParamsSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"threadId": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
		"server": Schema{"type": "string"},
		"uri":    Schema{"type": "string"},
	}, []string{"server", "uri"})
}

func mcpResourceReadResponseSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"contents": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/ResourceContent"},
		},
	}, []string{"contents"})
}

func mcpServerInfoSchema() Schema {
	schema := closedThreadSessionParamSchema(Schema{
		"name": Schema{"type": "string"},
		"title": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
		"version": Schema{"type": "string"},
		"description": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
		"icons": Schema{
			"anyOf": []any{
				Schema{"type": "array", "items": Schema{"$ref": "#/$defs/JsonValue"}},
				Schema{"type": "null"},
			},
		},
		"websiteUrl": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
	}, []string{"name", "title", "version", "description", "icons", "websiteUrl"})
	schema["description"] = "Presentation metadata advertised by an initialized MCP server."
	return schema
}

func mcpServerMigrationSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"name": Schema{"type": "string"},
	}, []string{"name"})
}

func pluginsMigrationSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"marketplaceName": Schema{"type": "string"},
		"pluginNames": Schema{
			"type": "array", "items": Schema{"type": "string"},
		},
	}, []string{"marketplaceName", "pluginNames"})
}

func skillMigrationSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"name": Schema{"type": "string"},
	}, []string{"name"})
}

func hookMigrationSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"name": Schema{"type": "string"},
	}, []string{"name"})
}

func subagentMigrationSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"name": Schema{"type": "string"},
	}, []string{"name"})
}

func commandMigrationSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"name": Schema{"type": "string"},
	}, []string{"name"})
}

func migrationDetailsSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"plugins":    migrationDetailsArraySchema("PluginsMigration"),
		"skills":     migrationDetailsArraySchema("SkillMigration"),
		"sessions":   migrationDetailsArraySchema("SessionMigration"),
		"mcpServers": migrationDetailsArraySchema("McpServerMigration"),
		"hooks":      migrationDetailsArraySchema("HookMigration"),
		"subagents":  migrationDetailsArraySchema("SubagentMigration"),
		"commands":   migrationDetailsArraySchema("CommandMigration"),
	}, nil)
}

func migrationDetailsArraySchema(typeName string) Schema {
	return Schema{
		"type":    "array",
		"items":   Schema{"$ref": "#/$defs/" + typeName},
		"default": []any{},
	}
}

func sessionMigrationSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"path": Schema{"type": "string"},
		"cwd":  Schema{"type": "string"},
		"title": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
	}, []string{"path", "cwd", "title"})
}

func mcpServerToolCallParamsSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"threadId":  Schema{"type": "string"},
		"server":    Schema{"type": "string"},
		"tool":      Schema{"type": "string"},
		"arguments": Schema{"$ref": "#/$defs/JsonValue"},
		"_meta":     Schema{"$ref": "#/$defs/JsonValue"},
	}, []string{"server", "threadId", "tool"})
}

func mcpResourceSchema() Schema {
	return Schema{
		"type":        "object",
		"description": "A known resource that the server is capable of reading.",
		"properties": Schema{
			"_meta":       Schema{"$ref": "#/$defs/JsonValue"},
			"annotations": Schema{"$ref": "#/$defs/JsonValue"},
			"description": Schema{"type": []any{"string", "null"}},
			"icons": Schema{
				"type": []any{"array", "null"}, "items": Schema{"$ref": "#/$defs/JsonValue"},
			},
			"mimeType": Schema{"type": []any{"string", "null"}},
			"name":     Schema{"type": "string"},
			"size":     Schema{"type": []any{"integer", "null"}, "format": "int64"},
			"title":    Schema{"type": []any{"string", "null"}},
			"uri":      Schema{"type": "string"},
		},
		"required": []string{"name", "uri"},
	}
}

func mcpResourceTemplateSchema() Schema {
	return Schema{
		"type":        "object",
		"description": "A template description for resources available on the server.",
		"properties": Schema{
			"annotations": Schema{"$ref": "#/$defs/JsonValue"},
			"description": Schema{"type": []any{"string", "null"}},
			"mimeType":    Schema{"type": []any{"string", "null"}},
			"name":        Schema{"type": "string"},
			"title":       Schema{"type": []any{"string", "null"}},
			"uriTemplate": Schema{"type": "string"},
		},
		"required": []string{"name", "uriTemplate"},
	}
}

func mcpToolSchema() Schema {
	return Schema{
		"type":        "object",
		"description": "Definition for a tool the client can call.",
		"properties": Schema{
			"_meta":        Schema{"$ref": "#/$defs/JsonValue"},
			"annotations":  Schema{"$ref": "#/$defs/JsonValue"},
			"description":  Schema{"type": []any{"string", "null"}},
			"icons":        Schema{"type": []any{"array", "null"}, "items": Schema{"$ref": "#/$defs/JsonValue"}},
			"inputSchema":  Schema{"$ref": "#/$defs/JsonValue"},
			"name":         Schema{"type": "string"},
			"outputSchema": Schema{"$ref": "#/$defs/JsonValue"},
			"title":        Schema{"type": []any{"string", "null"}},
		},
		"required": []string{"inputSchema", "name"},
	}
}

func mcpServerStatusSchema() Schema {
	return Schema{
		"type": "object",
		"properties": Schema{
			"authStatus": Schema{"$ref": "#/$defs/McpAuthStatus"},
			"name":       Schema{"type": "string"},
			"resourceTemplates": Schema{
				"type": "array", "items": Schema{"$ref": "#/$defs/ResourceTemplate"},
			},
			"resources": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/Resource"}},
			"serverInfo": Schema{"anyOf": []any{
				Schema{"$ref": "#/$defs/McpServerInfo"}, Schema{"type": "null"},
			}},
			"tools": Schema{
				"type": "object", "additionalProperties": Schema{"$ref": "#/$defs/Tool"},
			},
		},
		"required": []string{"authStatus", "name", "resourceTemplates", "resources", "tools"},
	}
}

func listMcpServerStatusResponseSchema() Schema {
	return Schema{
		"type": "object",
		"properties": Schema{
			"data": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/McpServerStatus"}},
			"nextCursor": Schema{
				"description": "Opaque cursor to pass to the next call to continue after the last item. If None, there are no more items to return.",
				"type":        []any{"string", "null"},
			},
		},
		"required": []string{"data"},
	}
}

func mcpServerToolCallResponseSchema() Schema {
	return closedThreadSessionParamSchema(Schema{
		"content": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/JsonValue"},
		},
		"structuredContent": Schema{"$ref": "#/$defs/JsonValue"},
		"isError": Schema{
			"anyOf":                          []any{Schema{"type": "boolean"}, Schema{"type": "null"}},
			typeScriptOptionalNonNullKeyword: true,
		},
		"_meta": Schema{"$ref": "#/$defs/JsonValue"},
	}, []string{"content"})
}

func resourceContentSchema() Schema {
	return Schema{"oneOf": []any{
		resourceContentVariantSchema("text"),
		resourceContentVariantSchema("blob"),
	}}
}

func resourceContentVariantSchema(contentField string) Schema {
	properties := Schema{
		"uri": Schema{"type": "string"},
		"mimeType": Schema{
			"anyOf":                          []any{Schema{"type": "string"}, Schema{"type": "null"}},
			typeScriptOptionalNonNullKeyword: true,
		},
		"_meta": Schema{"$ref": "#/$defs/JsonValue"},
	}
	properties[contentField] = Schema{"type": "string"}
	return closedThreadSessionParamSchema(properties, []string{contentField, "uri"})
}

func threadForkParamsSchema() Schema {
	properties := threadSessionParamCommonSchemaProperties()
	properties["threadId"] = Schema{"type": "string"}
	properties["lastTurnId"] = nullableThreadSessionParamSchema(Schema{"type": "string"})
	properties["ephemeral"] = Schema{"type": "boolean"}
	properties["threadSource"] = nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/ThreadSource"})
	return closedThreadSessionParamSchema(properties, []string{"threadId"})
}

func threadSessionParamCommonSchemaProperties() Schema {
	configMap := Schema{
		"type": "object", "additionalProperties": Schema{"$ref": "#/$defs/JsonValue"},
		"x-gollem-typescript-optional-map": true,
	}
	return Schema{
		"model":                 nullableThreadSessionParamSchema(Schema{"type": "string"}),
		"modelProvider":         nullableThreadSessionParamSchema(Schema{"type": "string"}),
		"serviceTier":           nullableThreadSessionParamSchema(Schema{"type": "string"}),
		"cwd":                   nullableThreadSessionParamSchema(Schema{"type": "string"}),
		"approvalPolicy":        nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/AskForApproval"}),
		"approvalsReviewer":     nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/ApprovalsReviewer"}),
		"sandbox":               nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/SandboxMode"}),
		"config":                nullableThreadSessionParamSchema(configMap),
		"baseInstructions":      nullableThreadSessionParamSchema(Schema{"type": "string"}),
		"developerInstructions": nullableThreadSessionParamSchema(Schema{"type": "string"}),
	}
}

func closedThreadSessionParamSchema(properties Schema, required []string) Schema {
	schema := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func sourceApprovalParamsSchema(title string, properties Schema, required []string) Schema {
	return Schema{"$schema": "http://json-schema.org/draft-07/schema#", "properties": properties, "required": required, "title": title, "type": "object"}
}

func nullableThreadSessionParamSchema(value Schema) Schema {
	return Schema{"anyOf": []any{value, Schema{"type": "null"}}}
}

func turnStartParamsSchema() Schema {
	properties := Schema{
		"threadId":            Schema{"type": "string"},
		"clientUserMessageId": nullableThreadSessionParamSchema(Schema{"type": "string"}),
		"input": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/UserInput"},
		},
		"cwd":               nullableThreadSessionParamSchema(Schema{"type": "string"}),
		"approvalPolicy":    nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/AskForApproval"}),
		"approvalsReviewer": nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/ApprovalsReviewer"}),
		"sandboxPolicy":     nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/SandboxPolicy"}),
		"model":             nullableThreadSessionParamSchema(Schema{"type": "string"}),
		"serviceTier":       nullableThreadSessionParamSchema(Schema{"type": "string"}),
		"effort":            nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/ReasoningEffort"}),
		"summary":           nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/ReasoningSummary"}),
		"personality":       nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/Personality"}),
		"outputSchema":      nullableThreadSessionParamSchema(Schema{"$ref": "#/$defs/JsonValue"}),
	}
	return closedThreadSessionParamSchema(properties, []string{"threadId", "input"})
}

func turnStartResponseSchema() Schema {
	return Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           Schema{"turn": Schema{"$ref": "#/$defs/Turn"}},
		"required":             []string{"turn"},
	}
}

func commandExecutionApprovalDecisionSchema() Schema {
	execPolicyVariant := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": Schema{
			CommandExecutionApprovalAcceptWithExecpolicyAmendment: Schema{
				"type":                 "object",
				"additionalProperties": false,
				"properties": Schema{
					"execpolicy_amendment": Schema{"$ref": "#/$defs/ExecPolicyAmendment"},
				},
				"required": []any{"execpolicy_amendment"},
			},
		},
		"required": []any{CommandExecutionApprovalAcceptWithExecpolicyAmendment},
	}
	networkPolicyVariant := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": Schema{
			CommandExecutionApprovalApplyNetworkPolicyAmendment: Schema{
				"type":                 "object",
				"additionalProperties": false,
				"properties": Schema{
					"network_policy_amendment": Schema{"$ref": "#/$defs/NetworkPolicyAmendment"},
				},
				"required": []any{"network_policy_amendment"},
			},
		},
		"required": []any{CommandExecutionApprovalApplyNetworkPolicyAmendment},
	}
	return Schema{"oneOf": []any{
		stringEnumSchema(CommandExecutionApprovalAccept),
		stringEnumSchema(CommandExecutionApprovalAcceptForSession),
		execPolicyVariant,
		networkPolicyVariant,
		stringEnumSchema(CommandExecutionApprovalDecline),
		stringEnumSchema(CommandExecutionApprovalCancel),
	}}
}

func schemaRefUnion(names ...string) Schema {
	variants := make([]any, 0, len(names))
	for _, name := range names {
		variants = append(variants, Schema{"$ref": "#/$defs/" + name})
	}
	return Schema{"anyOf": variants}
}

func askForApprovalSchema() Schema {
	granular := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": Schema{
			"sandbox_approval":    Schema{"type": "boolean"},
			"rules":               Schema{"type": "boolean"},
			"skill_approval":      Schema{"type": "boolean"},
			"request_permissions": Schema{"type": "boolean"},
			"mcp_elicitations":    Schema{"type": "boolean"},
		},
		"required": []any{
			"sandbox_approval", "rules", "skill_approval",
			"request_permissions", "mcp_elicitations",
		},
	}
	return Schema{"oneOf": []any{
		stringEnumSchema("untrusted"),
		stringEnumSchema("on-request"),
		Schema{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           Schema{"granular": granular},
			"required":             []any{"granular"},
		},
		stringEnumSchema("never"),
	}}
}

func sandboxPolicySchema() Schema {
	return Schema{"oneOf": []any{
		Schema{"properties": Schema{"type": Schema{"enum": []any{"dangerFullAccess"}, "title": "DangerFullAccessSandboxPolicyType", "type": "string"}}, "required": []string{"type"}, "title": "DangerFullAccessSandboxPolicy", "type": "object"},
		Schema{"properties": Schema{"networkAccess": Schema{"default": false, "type": "boolean"}, "type": Schema{"enum": []any{"readOnly"}, "title": "ReadOnlySandboxPolicyType", "type": "string"}}, "required": []string{"type"}, "title": "ReadOnlySandboxPolicy", "type": "object"},
		Schema{"properties": Schema{"networkAccess": Schema{"allOf": []any{Schema{"$ref": "#/$defs/NetworkAccess"}}, "default": "restricted"}, "type": Schema{"enum": []any{"externalSandbox"}, "title": "ExternalSandboxSandboxPolicyType", "type": "string"}}, "required": []string{"type"}, "title": "ExternalSandboxSandboxPolicy", "type": "object"},
		Schema{"properties": Schema{"excludeSlashTmp": Schema{"default": false, "type": "boolean"}, "excludeTmpdirEnvVar": Schema{"default": false, "type": "boolean"}, "networkAccess": Schema{"default": false, "type": "boolean"}, "type": Schema{"enum": []any{"workspaceWrite"}, "title": "WorkspaceWriteSandboxPolicyType", "type": "string"}, "writableRoots": Schema{"default": []any{}, "items": Schema{"$ref": "#/$defs/AbsolutePathBuf"}, "type": "array"}}, "required": []string{"type"}, "title": "WorkspaceWriteSandboxPolicy", "type": "object"},
	}}
}

func mcpServerElicitationRequestParamsSchema() Schema {
	return Schema{
		"type":  "object",
		"title": "McpServerElicitationRequestParams",
		"properties": Schema{
			"threadId": Schema{"type": "string"},
			"turnId": Schema{
				"description": "Active Codex turn when this elicitation was observed, if app-server could correlate one.\n\nThis is nullable because MCP models elicitation as a standalone server-to-client request identified by the MCP server request id. It may be triggered during a turn, but turn context is app-server correlation rather than part of the protocol identity of the elicitation itself.",
				"type":        []any{"string", "null"},
			},
			"serverName": Schema{"type": "string"},
		},
		"required": []string{"serverName", "threadId"},
		"oneOf": []any{
			mcpServerElicitationRequestVariantSchema("form"),
			mcpServerElicitationRequestVariantSchema("openai/form"),
			mcpServerElicitationRequestVariantSchema("url"),
		},
	}
}

func mcpServerElicitationRequestVariantSchema(mode string) Schema {
	properties := Schema{
		"mode":    Schema{"type": "string", "enum": []any{mode}},
		"_meta":   true,
		"message": Schema{"type": "string"},
	}
	required := []string{"message", "mode"}
	switch mode {
	case "form":
		properties["requestedSchema"] = Schema{"$ref": "#/$defs/McpElicitationSchema"}
		required = append(required, "requestedSchema")
	case "openai/form":
		properties["requestedSchema"] = true
		required = append(required, "requestedSchema")
	case "url":
		properties["url"] = Schema{"type": "string"}
		properties["elicitationId"] = Schema{"type": "string"}
		required = []string{"elicitationId", "message", "mode", "url"}
	}
	return Schema{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

func mcpServerElicitationRequestResponseSchema() Schema {
	return Schema{
		"type":  "object",
		"title": "McpServerElicitationRequestResponse",
		"properties": Schema{
			"action": Schema{"$ref": "#/$defs/McpServerElicitationAction"},
			"content": Schema{
				"description": "Structured user input for accepted elicitations, mirroring RMCP `CreateElicitationResult`.\n\nThis is nullable because decline/cancel responses have no content.",
			},
			"_meta": Schema{
				"description": "Optional client metadata for form-mode action handling.",
			},
		},
		"required": []string{"action"},
	}
}

func setSchemaIntegerMinimum(schema Schema, minimum int, names ...string) {
	properties, _ := schema["properties"].(Schema)
	for _, name := range names {
		if property, ok := properties[name].(Schema); ok {
			property["minimum"] = minimum
		}
	}
}

func patchChangeKindVariantSchema(kind, requiredMovePath string) Schema {
	properties := Schema{
		"type": Schema{"type": "string", "enum": []any{kind}},
	}
	required := []string{"type"}
	if kind == "update" {
		properties["move_path"] = nullableStringSchema()
		properties["movePath"] = nullableStringSchema()
		required = append(required, requiredMovePath)
	}
	return Schema{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func nullableStringSchema() Schema {
	return Schema{"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}}}
}

func nullableAppInfoStringMapSchema() Schema {
	return Schema{"anyOf": []any{
		Schema{"type": "object", "additionalProperties": Schema{"type": "string"}},
		Schema{"type": "null"},
	}}
}

func schemaWithRequiredIDAlternative(base Schema) Schema {
	return schemaWithRequiredFieldAlternatives(base, []string{"threadId"}, []string{"id"})
}

func schemaWithRequiredFieldAlternatives(base Schema, requiredFields ...[]string) Schema {
	variants := make([]any, 0, len(requiredFields))
	for _, required := range requiredFields {
		variant := make(Schema, len(base))
		for key, value := range base {
			variant[key] = value
		}
		variant["required"] = append([]string(nil), required...)
		variants = append(variants, variant)
	}
	return Schema{"anyOf": variants}
}

type wireSchemaDefinition struct {
	Name string
	Type reflect.Type
}

func MarshalJSONSchema() ([]byte, error) {
	data, err := json.MarshalIndent(JSONSchema(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func methodEnumSchema(surfaces ...Surface) Schema {
	allowed := make(map[Surface]bool, len(surfaces))
	for _, surface := range surfaces {
		allowed[surface] = true
	}
	var enum []any
	for _, info := range methodRegistry {
		if allowed[info.Surface] {
			enum = append(enum, info.Method)
		}
	}
	return Schema{"type": "string", "enum": enum}
}

func stringEnumSchema(values ...string) Schema {
	enum := make([]any, len(values))
	for i, value := range values {
		enum[i] = value
	}
	return Schema{"type": "string", "enum": enum}
}

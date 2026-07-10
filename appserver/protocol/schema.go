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
	for name, schema := range runtimeSchemaDefinitions() {
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

func runtimeSchemaDefinitions() Schema {
	definitions := []runtimeSchemaDefinition{
		{Name: "ApprovalRequestBase", Type: reflect.TypeFor[ApprovalRequestBase]()},
		{Name: "ApprovalRespondParams", Type: reflect.TypeFor[ApprovalRespondParams]()},
		{Name: "ApprovalRespondResult", Type: reflect.TypeFor[ApprovalRespondResult]()},
		{Name: "CommandExecutionAction", Type: reflect.TypeFor[CommandExecutionAction]()},
		{Name: "CommandExecutionApprovalRequestParams", Type: reflect.TypeFor[CommandExecutionApprovalRequestParams]()},
		{Name: "CommandExecutionItem", Type: reflect.TypeFor[CommandExecutionItem]()},
		{Name: "CommandExecutionItemCompletedNotificationParams", Type: reflect.TypeFor[CommandExecutionItemCompletedNotificationParams]()},
		{Name: "CommandExecutionItemStartedNotificationParams", Type: reflect.TypeFor[CommandExecutionItemStartedNotificationParams]()},
		{Name: "CommandExecutionOutputDeltaNotificationParams", Type: reflect.TypeFor[CommandExecutionOutputDeltaNotificationParams]()},
		{Name: "ContextCompactionItem", Type: reflect.TypeFor[ContextCompactionItem]()},
		{Name: "DaemonShutdownParams", Type: reflect.TypeFor[DaemonShutdownParams]()},
		{Name: "DaemonShutdownState", Type: reflect.TypeFor[DaemonShutdownState]()},
		{Name: "DaemonStartResult", Type: reflect.TypeFor[DaemonStartResult]()},
		{Name: "DaemonStatus", Type: reflect.TypeFor[DaemonStatus]()},
		{Name: "DaemonStopResult", Type: reflect.TypeFor[DaemonStopResult]()},
		{Name: "DaemonVersion", Type: reflect.TypeFor[DaemonVersion]()},
		{Name: "DynamicToolCallContentItem", Type: reflect.TypeFor[DynamicToolCallContentItem]()},
		{Name: "DynamicToolCallItem", Type: reflect.TypeFor[DynamicToolCallItem]()},
		{Name: "DynamicToolCallItemCompletedNotificationParams", Type: reflect.TypeFor[DynamicToolCallItemCompletedNotificationParams]()},
		{Name: "DynamicToolCallItemStartedNotificationParams", Type: reflect.TypeFor[DynamicToolCallItemStartedNotificationParams]()},
		{Name: "FileChangeApprovalRequestParams", Type: reflect.TypeFor[FileChangeApprovalRequestParams]()},
		{Name: "FileChangeArtifactEvidence", Type: reflect.TypeFor[FileChangeArtifactEvidence]()},
		{Name: "FileChangeItem", Type: reflect.TypeFor[FileChangeItem]()},
		{Name: "FileChangeItemCompletedNotificationParams", Type: reflect.TypeFor[FileChangeItemCompletedNotificationParams]()},
		{Name: "FileChangeItemStartedNotificationParams", Type: reflect.TypeFor[FileChangeItemStartedNotificationParams]()},
		{Name: "FileChangePatchUpdatedNotificationParams", Type: reflect.TypeFor[FileChangePatchUpdatedNotificationParams]()},
		{Name: "FileUpdateChange", Type: reflect.TypeFor[FileUpdateChange]()},
		{Name: "ItemLifecycleNotificationParams", Type: reflect.TypeFor[ItemLifecycleNotificationParams]()},
		{Name: "MCPContent", Type: reflect.TypeFor[MCPContent]()},
		{Name: "MCPToolCallError", Type: reflect.TypeFor[MCPToolCallError]()},
		{Name: "MCPToolCallItem", Type: reflect.TypeFor[MCPToolCallItem]()},
		{Name: "MCPToolCallItemCompletedNotificationParams", Type: reflect.TypeFor[MCPToolCallItemCompletedNotificationParams]()},
		{Name: "MCPToolCallItemStartedNotificationParams", Type: reflect.TypeFor[MCPToolCallItemStartedNotificationParams]()},
		{Name: "MCPToolCallProgressNotificationParams", Type: reflect.TypeFor[MCPToolCallProgressNotificationParams]()},
		{Name: "MCPToolCallResult", Type: reflect.TypeFor[MCPToolCallResult]()},
		{Name: "PatchChangeKind", Type: reflect.TypeFor[PatchChangeKind]()},
		{Name: "PermissionsApprovalRequestParams", Type: reflect.TypeFor[PermissionsApprovalRequestParams]()},
		{Name: "ServerRequestResolvedNotificationParams", Type: reflect.TypeFor[ServerRequestResolvedNotificationParams]()},
		{Name: "ThreadCompactStartParams", Type: reflect.TypeFor[ThreadCompactStartParams]()},
		{Name: "ThreadCompactStartResponse", Type: reflect.TypeFor[ThreadCompactStartResponse]()},
		{Name: "ThreadCompactedNotificationParams", Type: reflect.TypeFor[ThreadCompactedNotificationParams]()},
		{Name: "ThreadTokenUsageUpdatedNotificationParams", Type: reflect.TypeFor[ThreadTokenUsageUpdatedNotificationParams]()},
		{Name: "TimelineItem", Type: reflect.TypeFor[TimelineItem]()},
		{Name: "TokenUsage", Type: reflect.TypeFor[TokenUsage]()},
		{Name: "TokenUsageBreakdown", Type: reflect.TypeFor[TokenUsageBreakdown]()},
		{Name: "ToolPayloadSummary", Type: reflect.TypeFor[ToolPayloadSummary]()},
		{Name: "TurnDiffUpdatedNotificationParams", Type: reflect.TypeFor[TurnDiffUpdatedNotificationParams]()},
	}
	names := make(map[reflect.Type]string, len(definitions))
	for _, definition := range definitions {
		names[definition.Type] = definition.Name
	}
	schemas := make(Schema, len(definitions))
	for _, definition := range definitions {
		schemas[definition.Name] = schemaForDefinition(definition.Type, names)
	}
	return schemas
}

type runtimeSchemaDefinition struct {
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

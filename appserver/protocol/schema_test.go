package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONSchemaContainsEnvelopeAndMethodInventory(t *testing.T) {
	schema := JSONSchema()
	if schema["x-gollem-protocol-version"] != ProtocolVersion {
		t.Fatalf("protocol version = %v", schema["x-gollem-protocol-version"])
	}
	if schema["x-gollem-schema-version"] != SchemaVersion {
		t.Fatalf("schema version = %v", schema["x-gollem-schema-version"])
	}
	if _, err := json.Marshal(schema); err != nil {
		t.Fatalf("schema must be JSON serializable: %v", err)
	}

	methods, ok := schema["x-gollem-methods"].([]MethodInfo)
	if !ok {
		t.Fatalf("x-gollem-methods = %T", schema["x-gollem-methods"])
	}
	if len(methods) != 224 {
		t.Fatalf("schema method inventory has %d rows, want 224", len(methods))
	}

	defs := schema["$defs"].(Schema)
	request := defs["Request"].(Schema)
	props := request["properties"].(Schema)
	if _, exists := props["jsonrpc"]; exists {
		t.Fatal("request schema should not expose jsonrpc")
	}
	methodSchema := props["method"].(Schema)
	enum := methodSchema["enum"].([]any)
	assertEnumContains(t, enum, "initialize")
	assertEnumContains(t, enum, "turn/retry")

	notification := defs["Notification"].(Schema)
	notificationProps := notification["properties"].(Schema)
	notificationMethods := notificationProps["method"].(Schema)["enum"].([]any)
	assertEnumContains(t, notificationMethods, "initialized")
	assertEnumContains(t, notificationMethods, "thread/started")
}

func TestJSONSchemaExportsRuntimeDefinitionsAndBindings(t *testing.T) {
	schema := JSONSchema()
	defs := schema["$defs"].(Schema)
	for _, name := range []string{
		"DynamicToolCallItem",
		"CommandExecutionItem",
		"FileChangeItem",
		"MCPToolCallItem",
		"ContextCompactionItem",
		"ThreadTokenUsageUpdatedNotificationParams",
		"TurnDiffUpdatedNotificationParams",
		"FileChangeApprovalRequestParams",
		"DaemonStatus",
	} {
		if _, ok := defs[name]; !ok {
			t.Errorf("$defs missing %s", name)
		}
	}

	bindings, ok := schema["x-gollem-type-bindings"].([]WireTypeBinding)
	if !ok {
		t.Fatalf("bindings = %T", schema["x-gollem-type-bindings"])
	}
	assertBinding(t, bindings, "item/started", SurfaceServerNotification, "DynamicToolCallItemStartedNotificationParams")
	assertBinding(t, bindings, "item/completed", SurfaceServerNotification, "MCPToolCallItemCompletedNotificationParams")
	assertBinding(t, bindings, "thread/tokenUsage/updated", SurfaceServerNotification, "ThreadTokenUsageUpdatedNotificationParams")
	assertBinding(t, bindings, "item/fileChange/requestApproval", SurfaceServerRequest, "FileChangeApprovalRequestParams")
	assertBinding(t, bindings, "daemon/status", SurfaceGollemExtension, "DaemonStatus")

	for _, binding := range bindings {
		info, ok := LookupMethod(binding.Method)
		if !ok {
			t.Errorf("binding references unknown method %q", binding.Method)
		} else if info.Surface != binding.Surface {
			t.Errorf("binding %s surface = %s, registry has %s", binding.Method, binding.Surface, info.Surface)
		}
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if _, ok := defs[name]; !ok {
				t.Errorf("binding %s references missing definition %s", binding.Method, name)
			}
		}
	}
	started := defs["DynamicToolCallItemStartedNotificationParams"].(Schema)
	startedItem := started["properties"].(Schema)["item"].(Schema)
	if startedItem["$ref"] != "#/$defs/DynamicToolCallItem" {
		t.Fatalf("dynamic tool notification item schema = %v", startedItem)
	}
	assertSchemaRefsResolve(t, schema, defs)

	payloadBindings, ok := schema["x-gollem-item-payload-bindings"].([]ItemPayloadBinding)
	if !ok {
		t.Fatalf("item payload bindings = %T", schema["x-gollem-item-payload-bindings"])
	}
	wantPayloads := map[string]string{
		ItemTypeDynamicToolCall:   "DynamicToolCallItem",
		ItemTypeCommandExecution:  "CommandExecutionItem",
		ItemTypeFileChange:        "FileChangeItem",
		ItemTypeMCPToolCall:       "MCPToolCallItem",
		ItemTypeContextCompaction: "ContextCompactionItem",
	}
	for _, binding := range payloadBindings {
		if want := wantPayloads[binding.Kind]; want == "" {
			t.Errorf("unexpected item payload binding kind %q", binding.Kind)
		} else if want != binding.Type {
			t.Errorf("item payload binding %s = %s, want %s", binding.Kind, binding.Type, want)
		}
		if _, ok := defs[binding.Type]; !ok {
			t.Errorf("item payload binding %s references missing definition %s", binding.Kind, binding.Type)
		}
		delete(wantPayloads, binding.Kind)
	}
	if len(wantPayloads) > 0 {
		t.Fatalf("missing item payload bindings: %v", wantPayloads)
	}
}

func TestProtocolBindingsReturnIndependentCopies(t *testing.T) {
	bindings := WireTypeBindings()
	bindings[0].Method = "changed"
	bindings[0].Params[0] = "Changed"
	if fresh := WireTypeBindings(); fresh[0].Method == "changed" || fresh[0].Params[0] == "Changed" {
		t.Fatal("WireTypeBindings returned mutable registry storage")
	}

	payloads := ItemPayloadBindings()
	payloads[0].Kind = "changed"
	if fresh := ItemPayloadBindings(); fresh[0].Kind == "changed" {
		t.Fatal("ItemPayloadBindings returned mutable registry storage")
	}
}

func TestJSONSchemaGoldenIsDeterministic(t *testing.T) {
	generated, err := MarshalJSONSchema()
	if err != nil {
		t.Fatalf("MarshalJSONSchema: %v", err)
	}
	second, err := MarshalJSONSchema()
	if err != nil {
		t.Fatalf("MarshalJSONSchema second call: %v", err)
	}
	if !bytes.Equal(generated, second) {
		t.Fatal("schema generation is nondeterministic")
	}
	golden, err := os.ReadFile(filepath.Join("schema.json"))
	if err != nil {
		t.Fatalf("read schema golden: %v", err)
	}
	if !bytes.Equal(generated, golden) {
		t.Fatal("schema.json is stale; run go generate ./appserver/protocol")
	}
}

func TestRuntimeSchemaCompatibilityFloor(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	wantFields := map[string][]string{
		"DynamicToolCallItem":  {"type", "namespace", "tool", "arguments", "status", "contentItems", "success", "durationMs"},
		"CommandExecutionItem": {"type", "command", "cwd", "processId", "source", "status", "commandActions", "aggregatedOutput", "exitCode", "durationMs", "startedAt", "completedAt"},
		"FileChangeItem":       {"type", "changes", "status"},
		"MCPToolCallItem":      {"type", "server", "tool", "status", "arguments", "appContext", "mcpAppResourceUri", "pluginId", "result", "error", "durationMs"},
		"DaemonStatus":         {"status", "name", "version", "protocolVersion", "pid", "startedAt", "uptimeMillis", "shutdownRequested", "restartRequested"},
	}
	for definition, fields := range wantFields {
		def, ok := defs[definition].(Schema)
		if !ok {
			t.Fatalf("definition %s = %T", definition, defs[definition])
		}
		properties, ok := def["properties"].(Schema)
		if !ok {
			t.Fatalf("definition %s properties = %T", definition, def["properties"])
		}
		for _, field := range fields {
			if _, ok := properties[field]; !ok {
				t.Errorf("definition %s removed field %s", definition, field)
			}
		}
	}
}

func assertBinding(t *testing.T, bindings []WireTypeBinding, method string, surface Surface, typeName string) {
	t.Helper()
	for _, binding := range bindings {
		if binding.Method != method || binding.Surface != surface {
			continue
		}
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == typeName {
				return
			}
		}
	}
	t.Fatalf("binding %s/%s missing type %s", surface, method, typeName)
}

func assertEnumContains(t *testing.T, enum []any, want string) {
	t.Helper()
	for _, value := range enum {
		if value == want {
			return
		}
	}
	t.Fatalf("enum missing %q", want)
}

func assertSchemaRefsResolve(t *testing.T, value any, defs Schema) {
	t.Helper()
	switch typed := value.(type) {
	case Schema:
		if ref, ok := typed["$ref"].(string); ok {
			const prefix = "#/$defs/"
			if !strings.HasPrefix(ref, prefix) {
				t.Errorf("external or malformed schema ref %q", ref)
			} else if _, ok := defs[strings.TrimPrefix(ref, prefix)]; !ok {
				t.Errorf("schema ref %q does not resolve", ref)
			}
		}
		for _, child := range typed {
			assertSchemaRefsResolve(t, child, defs)
		}
	case []any:
		for _, child := range typed {
			assertSchemaRefsResolve(t, child, defs)
		}
	}
}

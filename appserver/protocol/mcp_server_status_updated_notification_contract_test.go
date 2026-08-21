package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMcpServerStatusUpdatedNotificationSchemaIsExact(t *testing.T) {
	definition, ok := JSONSchema()["$defs"].(Schema)["McpServerStatusUpdatedNotification"].(Schema)
	if !ok {
		t.Fatal("$defs missing McpServerStatusUpdatedNotification")
	}
	if definition["type"] != "object" || definition["additionalProperties"] != false {
		t.Fatalf("McpServerStatusUpdatedNotification is not a closed object: %#v", definition)
	}
	if got := schemaRequiredNames(definition); !slices.Equal(got, []string{
		"threadId", "name", "status", "error", "failureReason",
	}) {
		t.Fatalf("McpServerStatusUpdatedNotification required = %v", got)
	}
	wantProperties := Schema{
		"threadId": Schema{"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}}},
		"name":     Schema{"type": "string"},
		"status":   Schema{"$ref": "#/$defs/McpServerStartupState"},
		"error":    Schema{"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}}},
		"failureReason": Schema{"anyOf": []any{
			Schema{"$ref": "#/$defs/McpServerStartupFailureReason"},
			Schema{"type": "null"},
		}},
	}
	if got := definition["properties"].(Schema); !reflect.DeepEqual(got, wantProperties) {
		t.Fatalf("McpServerStatusUpdatedNotification properties = %#v, want %#v", got, wantProperties)
	}
}

func TestMcpServerStatusUpdatedNotificationAcceptsCanonicalAndCompatibleForms(t *testing.T) {
	valid := []struct {
		input string
		want  string
	}{
		{
			input: `{"name":"","status":"starting"}`,
			want:  `{"threadId":null,"name":"","status":"starting","error":null,"failureReason":null}`,
		},
		{
			input: `{"threadId":null,"name":"server","status":"ready","error":null,"failureReason":null}`,
			want:  `{"threadId":null,"name":"server","status":"ready","error":null,"failureReason":null}`,
		},
		{
			input: `{"threadId":"","name":"server","status":"failed",` +
				`"error":"","failureReason":"reauthenticationRequired"}`,
			want: `{"threadId":"","name":"server","status":"failed",` +
				`"error":"","failureReason":"reauthenticationRequired"}`,
		},
		{
			input: `{"name":"server","status":"cancelled","error":"cancelled"}`,
			want:  `{"threadId":null,"name":"server","status":"cancelled","error":"cancelled","failureReason":null}`,
		},
	}
	for _, tc := range valid {
		var notification McpServerStatusUpdatedNotification
		if err := json.Unmarshal([]byte(tc.input), &notification); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(notification)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}
}

func TestMcpServerStatusUpdatedNotificationRejectsMalformedForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `{}`,
		`{"status":"starting"}`,
		`{"name":"server"}`,
		`{"name":null,"status":"starting"}`,
		`{"name":1,"status":"starting"}`,
		`{"name":"server","status":null}`,
		`{"name":"server","status":"other"}`,
		`{"threadId":1,"name":"server","status":"starting"}`,
		`{"threadId":{},"name":"server","status":"starting"}`,
		`{"name":"server","status":"starting","error":1}`,
		`{"name":"server","status":"starting","error":{}}`,
		`{"name":"server","status":"starting","failureReason":"other"}`,
		`{"name":"server","status":"starting","failureReason":1}`,
		`{"name":"server","status":"starting","failure_reason":"reauthenticationRequired"}`,
		`{"name":"server","status":"starting","extra":true}`,
		`{"name":"server","status":"starting"} {}`,
	}
	for _, input := range invalid {
		var notification McpServerStatusUpdatedNotification
		if err := json.Unmarshal([]byte(input), &notification); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	if _, err := json.Marshal(McpServerStatusUpdatedNotification{Name: "server"}); err == nil {
		t.Fatal("notification with zero startup status marshaled")
	}
	badReason := McpServerStartupFailureReason("other")
	if _, err := json.Marshal(McpServerStatusUpdatedNotification{
		Name: "server", Status: McpServerStartupStateFailed, FailureReason: &badReason,
	}); err == nil {
		t.Fatal("notification with invalid failure reason marshaled")
	}
	var nilNotification *McpServerStatusUpdatedNotification
	if err := nilNotification.UnmarshalJSON([]byte(`{"name":"server","status":"ready"}`)); err == nil {
		t.Fatal("nil McpServerStatusUpdatedNotification receiver succeeded")
	}
}

func TestMcpServerStatusUpdatedNotificationRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "McpServerStatusUpdatedNotification") ||
			slices.Contains(binding.Result, "McpServerStatusUpdatedNotification") {
			t.Fatalf("McpServerStatusUpdatedNotification unexpectedly bound to %s", binding.Method)
		}
	}
	info, ok := LookupMethod("mcpServer/startupStatus/updated")
	if !ok || info.State != MethodBlocked {
		t.Fatalf("mcpServer/startupStatus/updated = %#v, %v; want blocked", info, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestMcpServerStatusUpdatedNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type McpServerStatusUpdatedNotification = {`,
		`"error": string | null;`,
		`"failureReason": McpServerStartupFailureReason | null;`,
		`"name": string;`,
		`"status": McpServerStartupState;`,
		`"threadId": string | null;`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = McpServerStatusUpdatedNotification{}
	_ json.Unmarshaler = (*McpServerStatusUpdatedNotification)(nil)
)

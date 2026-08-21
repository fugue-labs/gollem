package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExactItemLifecycleNotificationSchemas(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	tests := []struct {
		name      string
		timestamp string
	}{
		{name: "ItemStartedNotification", timestamp: "startedAtMs"},
		{name: "ItemCompletedNotification", timestamp: "completedAtMs"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			schema, ok := defs[testCase.name].(Schema)
			if !ok {
				t.Fatalf("$defs missing %s", testCase.name)
			}
			if schema["additionalProperties"] != false {
				t.Fatalf("%s allows extra fields: %#v", testCase.name, schema)
			}
			wantRequired := []string{"item", "threadId", "turnId", testCase.timestamp}
			if !slices.Equal(schemaRequiredNames(schema), wantRequired) {
				t.Fatalf("%s required = %v, want %v", testCase.name, schemaRequiredNames(schema), wantRequired)
			}
			properties := schema["properties"].(Schema)
			if !reflect.DeepEqual(properties["item"], Schema{"$ref": "#/$defs/ThreadItem"}) {
				t.Fatalf("%s item = %#v", testCase.name, properties["item"])
			}
			for _, name := range []string{"threadId", "turnId"} {
				if !reflect.DeepEqual(properties[name], Schema{"type": "string"}) {
					t.Fatalf("%s %s = %#v", testCase.name, name, properties[name])
				}
			}
			if !reflect.DeepEqual(properties[testCase.timestamp], Schema{"type": "integer"}) {
				t.Fatalf("%s %s = %#v", testCase.name, testCase.timestamp, properties[testCase.timestamp])
			}
		})
	}
}

func TestExactItemLifecycleNotificationWireValidation(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		target any
		want   string
	}{
		{
			name:   "started",
			input:  `{"startedAtMs":-1,"turnId":"turn-1","item":{"id":"item-1","type":"contextCompaction"},"threadId":"thread-1"}`,
			target: new(ItemStartedNotification),
			want:   `{"item":{"type":"contextCompaction","id":"item-1"},"threadId":"thread-1","turnId":"turn-1","startedAtMs":-1}`,
		},
		{
			name:   "completed",
			input:  `{"completedAtMs":9223372036854775807,"turnId":"turn-2","threadId":"thread-1","item":{"text":"done","id":"item-2","type":"plan"}}`,
			target: new(ItemCompletedNotification),
			want:   `{"item":{"type":"plan","id":"item-2","text":"done"},"threadId":"thread-1","turnId":"turn-2","completedAtMs":9223372036854775807}`,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(testCase.input), testCase.target); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(testCase.target)
			if err != nil || string(encoded) != testCase.want {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, testCase.want)
			}
		})
	}
}

func TestExactItemLifecycleNotificationRejectsMalformedWire(t *testing.T) {
	validItem := `{"type":"contextCompaction","id":"item-1"}`
	started := []string{
		`null`, `[]`, `{}`,
		`{"threadId":"thread-1","turnId":"turn-1","startedAtMs":1}`,
		`{"item":` + validItem + `,"turnId":"turn-1","startedAtMs":1}`,
		`{"item":` + validItem + `,"threadId":"thread-1","startedAtMs":1}`,
		`{"item":` + validItem + `,"threadId":"thread-1","turnId":"turn-1"}`,
		`{"item":null,"threadId":"thread-1","turnId":"turn-1","startedAtMs":1}`,
		`{"item":` + validItem + `,"threadId":null,"turnId":"turn-1","startedAtMs":1}`,
		`{"item":` + validItem + `,"threadId":"thread-1","turnId":null,"startedAtMs":1}`,
		`{"item":` + validItem + `,"threadId":"thread-1","turnId":"turn-1","startedAtMs":null}`,
		`{"item":` + validItem + `,"threadId":"thread-1","turnId":"turn-1","startedAtMs":1.5}`,
		`{"item":` + validItem + `,"threadId":"thread-1","turnId":"turn-1","startedAtMs":"1"}`,
		`{"item":` + validItem + `,"threadId":"thread-1","turnId":"turn-1","completedAtMs":1}`,
		`{"item":` + validItem + `,"threadId":"thread-1","turnId":"turn-1","startedAtMs":1,"extra":true}`,
		`{"item":{"type":"contextCompaction","id":"item-1","extra":true},"threadId":"thread-1","turnId":"turn-1","startedAtMs":1}`,
	}
	for _, input := range started {
		var notification ItemStartedNotification
		if err := json.Unmarshal([]byte(input), &notification); err == nil {
			t.Errorf("ItemStartedNotification Unmarshal(%s) succeeded", input)
		}
	}

	completed := []string{
		`{"item":` + validItem + `,"threadId":"thread-1","turnId":"turn-1"}`,
		`{"item":` + validItem + `,"threadId":"thread-1","turnId":"turn-1","completedAtMs":null}`,
		`{"item":` + validItem + `,"threadId":"thread-1","turnId":"turn-1","completedAtMs":9223372036854775808}`,
		`{"item":` + validItem + `,"threadId":"thread-1","turnId":"turn-1","startedAtMs":1}`,
	}
	for _, input := range completed {
		var notification ItemCompletedNotification
		if err := json.Unmarshal([]byte(input), &notification); err == nil {
			t.Errorf("ItemCompletedNotification Unmarshal(%s) succeeded", input)
		}
	}
}

func TestExactItemLifecycleNotificationMarshalAndNilReceiversFailClosed(t *testing.T) {
	if _, err := json.Marshal(ItemStartedNotification{}); err == nil {
		t.Fatal("zero ItemStartedNotification marshal succeeded")
	}
	if _, err := json.Marshal(ItemCompletedNotification{}); err == nil {
		t.Fatal("zero ItemCompletedNotification marshal succeeded")
	}
	var started *ItemStartedNotification
	if err := started.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ItemStartedNotification receiver succeeded")
	}
	var completed *ItemCompletedNotification
	if err := completed.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ItemCompletedNotification receiver succeeded")
	}
}

func TestExactItemLifecycleNotificationBindingsPreserveCompatibility(t *testing.T) {
	want := map[string][]string{
		"item/started": {
			"ItemStartedNotification", "ItemLifecycleNotificationParams",
			"DynamicToolCallItemStartedNotificationParams", "CommandExecutionItemStartedNotificationParams",
			"FileChangeItemStartedNotificationParams", "MCPToolCallItemStartedNotificationParams",
		},
		"item/completed": {
			"ItemCompletedNotification", "ItemLifecycleNotificationParams",
			"DynamicToolCallItemCompletedNotificationParams", "CommandExecutionItemCompletedNotificationParams",
			"FileChangeItemCompletedNotificationParams", "MCPToolCallItemCompletedNotificationParams",
		},
	}
	for _, binding := range WireTypeBindings() {
		if expected, ok := want[binding.Method]; ok {
			if binding.Surface != SurfaceServerNotification || !slices.Equal(binding.Params, expected) {
				t.Errorf("%s binding = %#v, want params %v", binding.Method, binding, expected)
			}
			delete(want, binding.Method)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing lifecycle bindings: %v", want)
	}
	if len(JSONSchema()["$defs"].(Schema)) != 675 {
		t.Fatalf("definition count = %d, want 675", len(JSONSchema()["$defs"].(Schema)))
	}
	if len(WireTypeBindings()) != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", len(WireTypeBindings()), len(ItemPayloadBindings()))
	}

	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, wantSource := range []string{
		"export type ItemStartedNotification = {",
		`"item": ThreadItem;`,
		`"startedAtMs": number;`,
		"export type ItemCompletedNotification = {",
		`"completedAtMs": number;`,
		`"item/started": ItemStartedNotification | ItemLifecycleNotificationParams | DynamicToolCallItemStartedNotificationParams`,
		`"item/completed": ItemCompletedNotification | ItemLifecycleNotificationParams | DynamicToolCallItemCompletedNotificationParams`,
	} {
		if !strings.Contains(source, wantSource) {
			t.Errorf("generated TypeScript missing %q", wantSource)
		}
	}
}

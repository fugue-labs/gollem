package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestClientNotificationSchemaIsExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	if got := definitions["ClientNotification"]; !reflect.DeepEqual(got, clientNotificationSchema()) {
		t.Fatalf("ClientNotification schema = %#v, want %#v", got, clientNotificationSchema())
	}
}

func TestClientNotificationPreservesSerdeWireForms(t *testing.T) {
	for _, input := range []string{
		`{"method":"initialized"}`,
		`{"future":true,"method":"initialized","params":{}}`,
	} {
		var notification ClientNotification
		if err := json.Unmarshal([]byte(input), &notification); err != nil {
			t.Errorf("unmarshal %s: %v", input, err)
			continue
		}
		encoded, err := json.Marshal(notification)
		if err != nil || string(encoded) != `{"method":"initialized"}` {
			t.Errorf("round trip %s = %s, %v; want initialized notification", input, encoded, err)
		}
	}
}

func TestClientNotificationRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"method":null}`, `{"method":1}`, `{"method":"other"}`,
		`{"method":"initialized","method":"initialized"}`,
		`{"method":"initialized"} {}`,
	} {
		assertJSONRejects[ClientNotification](t, input)
	}
}

func TestClientNotificationRemainsStandaloneFromInitializedBinding(t *testing.T) {
	var notification *ClientNotification
	if err := notification.UnmarshalJSON([]byte(`{"method":"initialized"}`)); err == nil {
		t.Fatal("nil client notification receiver succeeded")
	}

	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ClientNotification") || slices.Contains(binding.Result, "ClientNotification") {
			t.Fatalf("ClientNotification unexpectedly bound to %s", binding.Method)
		}
	}
	info, ok := LookupMethod("initialized")
	if !ok || info.Surface != SurfaceClientNotification || info.State != MethodImplemented {
		t.Fatalf("initialized method = %#v, %v; want implemented client notification", info, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestClientNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type ClientNotification = {\n" +
		"  \"method\": \"initialized\";\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

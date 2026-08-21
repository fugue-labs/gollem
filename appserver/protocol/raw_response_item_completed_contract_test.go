package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestRawResponseItemCompletedNotificationSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	notification, ok := defs["RawResponseItemCompletedNotification"].(Schema)
	if !ok {
		t.Fatal("$defs missing RawResponseItemCompletedNotification")
	}
	if notification["additionalProperties"] != false {
		t.Fatalf("RawResponseItemCompletedNotification allows extra fields: %#v", notification)
	}
	if !slices.Equal(schemaRequiredNames(notification), []string{"threadId", "turnId", "item"}) {
		t.Fatalf("RawResponseItemCompletedNotification required = %v", schemaRequiredNames(notification))
	}
	properties := notification["properties"].(Schema)
	for _, name := range []string{"threadId", "turnId"} {
		if !reflect.DeepEqual(properties[name], Schema{"type": "string"}) {
			t.Fatalf("RawResponseItemCompletedNotification %s = %#v", name, properties[name])
		}
	}
	if !reflect.DeepEqual(properties["item"], Schema{"$ref": "#/$defs/ResponseItem"}) {
		t.Fatalf("RawResponseItemCompletedNotification item = %#v", properties["item"])
	}
}

func TestRawResponseItemCompletedNotificationWireValidation(t *testing.T) {
	input := `{
		"item":{"type":"tool_search_call","execution":"search","arguments":{"large":18446744073709551616},"call_id":null,"status":"completed"},
		"turnId":"turn-1",
		"threadId":"thread-1"
	}`
	var notification RawResponseItemCompletedNotification
	if err := json.Unmarshal([]byte(input), &notification); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	encoded, err := json.Marshal(notification)
	want := `{"threadId":"thread-1","turnId":"turn-1","item":{"type":"tool_search_call","call_id":null,"status":"completed","execution":"search","arguments":{"large":18446744073709551616}}}`
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip = %s, %v; want %s", encoded, err, want)
	}
}

func TestRawResponseItemCompletedNotificationRejectsMalformedWire(t *testing.T) {
	validItem := `{"type":"other"}`
	invalid := []string{
		`null`, `[]`, `{}`,
		`{"turnId":"turn-1","item":` + validItem + `}`,
		`{"threadId":"thread-1","item":` + validItem + `}`,
		`{"threadId":"thread-1","turnId":"turn-1"}`,
		`{"threadId":null,"turnId":"turn-1","item":` + validItem + `}`,
		`{"threadId":"thread-1","turnId":null,"item":` + validItem + `}`,
		`{"threadId":"thread-1","turnId":"turn-1","item":null}`,
		`{"threadId":1,"turnId":"turn-1","item":` + validItem + `}`,
		`{"threadId":"thread-1","turnId":1,"item":` + validItem + `}`,
		`{"threadId":"thread-1","turnId":"turn-1","item":[]}`,
		`{"threadId":"thread-1","turnId":"turn-1","item":{"type":"other","id":"crossed"}}`,
		`{"threadId":"thread-1","turnId":"turn-1","item":` + validItem + `,"extra":true}`,
	}
	for _, input := range invalid {
		var notification RawResponseItemCompletedNotification
		if err := json.Unmarshal([]byte(input), &notification); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
}

func TestRawResponseItemCompletedNotificationEmptyAndNilReceiverFailClosed(t *testing.T) {
	if _, err := json.Marshal(RawResponseItemCompletedNotification{}); err == nil {
		t.Fatal("zero RawResponseItemCompletedNotification marshal succeeded")
	}
	var notification *RawResponseItemCompletedNotification
	if err := notification.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil RawResponseItemCompletedNotification receiver succeeded")
	}
}

func TestRawResponseItemCompletedNotificationRemainsStandalone(t *testing.T) {
	info, ok := LookupMethod("rawResponseItem/completed")
	if !ok || info.Surface != SurfaceServerNotification || info.State != MethodBlocked {
		t.Fatalf("rawResponseItem/completed method = %#v, %v", info, ok)
	}
	for _, binding := range WireTypeBindings() {
		if binding.Method == "rawResponseItem/completed" ||
			slices.Contains(binding.Params, "RawResponseItemCompletedNotification") ||
			slices.Contains(binding.Result, "RawResponseItemCompletedNotification") {
			t.Fatalf("raw response notification unexpectedly bound: %#v", binding)
		}
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
		"export type RawResponseItemCompletedNotification = {",
		`"item": ResponseItem;`,
		`"threadId": string;`,
		`"turnId": string;`,
	} {
		if !strings.Contains(source, wantSource) {
			t.Errorf("generated TypeScript missing %q", wantSource)
		}
	}
	if strings.Contains(source, `"rawResponseItem/completed": RawResponseItemCompletedNotification`) {
		t.Fatal("rawResponseItem/completed unexpectedly appears in MethodParamsByName")
	}
}

package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAccountLoginCompletedNotificationSchemaIsExact(t *testing.T) {
	want := closedThreadSessionParamSchema(Schema{
		"loginId": nullableStringSchema(),
		"success": Schema{"type": "boolean"},
		"error":   nullableStringSchema(),
	}, []string{"success"})
	got := JSONSchema()["$defs"].(Schema)["AccountLoginCompletedNotification"]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AccountLoginCompletedNotification = %#v, want %#v", got, want)
	}
}

func TestAccountLoginCompletedNotificationAcceptsSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{"success":true}`, `{"error":null,"loginId":null,"success":true}`},
		{`{"loginId":null,"success":false,"error":null}`, `{"error":null,"loginId":null,"success":false}`},
		{`{"loginId":"","success":true,"error":""}`, `{"error":"","loginId":"","success":true}`},
		{`{"future":true,"loginId":" 550e8400-e29b-41d4-a716-446655440000 ","success":false,"error":" denied "}`, `{"error":" denied ","loginId":" 550e8400-e29b-41d4-a716-446655440000 ","success":false}`},
	} {
		var notification AccountLoginCompletedNotification
		if err := json.Unmarshal([]byte(tc.input), &notification); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(notification)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestAccountLoginCompletedNotificationRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"success":null}`, `{"success":"true"}`, `{"success":1}`,
		`{"loginId":1,"success":true}`, `{"loginId":true,"success":true}`,
		`{"success":true,"error":1}`, `{"success":true,"error":false}`,
		`{"success":true,"success":false}`,
		`{"loginId":null,"loginId":"id","success":true}`,
		`{"success":true,"error":null,"error":"failure"}`,
		`{"success":true} {}`, `{"success":true} x`,
	} {
		assertJSONRejects[AccountLoginCompletedNotification](t, input)
	}
}

func TestAccountLoginCompletedNotificationNilReceiverFailsClosed(t *testing.T) {
	var notification *AccountLoginCompletedNotification
	if err := notification.UnmarshalJSON([]byte(`{"success":true}`)); err == nil {
		t.Fatal("nil notification receiver succeeded")
	}
}

func TestAccountLoginCompletedNotificationRemainsStandaloneAndDeferred(t *testing.T) {
	const name = "AccountLoginCompletedNotification"
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
			t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == name {
			t.Fatalf("%s unexpectedly bound to item %s", name, binding.Kind)
		}
	}
	var found bool
	for _, method := range Methods() {
		if method.Method == "account/login/completed" {
			found = true
			if method.Surface != SurfaceServerNotification || method.State != MethodDeferredStub {
				t.Fatalf("login completion method = %#v, want deferred server notification", method)
			}
		}
	}
	if !found {
		t.Fatal("account/login/completed method inventory entry missing")
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

func TestAccountLoginCompletedNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type AccountLoginCompletedNotification = {\n" +
		"  \"error\": string | null;\n" +
		"  \"loginId\": string | null;\n" +
		"  \"success\": boolean;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}

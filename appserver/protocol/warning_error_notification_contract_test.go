package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestWarningErrorNotificationSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	warning := defs["WarningNotification"].(Schema)
	assertClosedObjectSchema(t, warning, "threadId", "message")
	warningProperties := warning["properties"].(Schema)
	assertNullableStringSchema(t, warningProperties["threadId"])
	if !reflect.DeepEqual(warningProperties["message"], Schema{"type": "string"}) {
		t.Fatalf("WarningNotification.message = %#v", warningProperties["message"])
	}

	guardian := defs["GuardianWarningNotification"].(Schema)
	assertClosedObjectSchema(t, guardian, "threadId", "message")
	guardianProperties := guardian["properties"].(Schema)
	for _, field := range []string{"threadId", "message"} {
		if !reflect.DeepEqual(guardianProperties[field], Schema{"type": "string"}) {
			t.Errorf("GuardianWarningNotification.%s = %#v", field, guardianProperties[field])
		}
	}

	errorNotification := defs["ErrorNotification"].(Schema)
	assertClosedObjectSchema(t, errorNotification, "error", "willRetry", "threadId", "turnId")
	errorProperties := errorNotification["properties"].(Schema)
	if !reflect.DeepEqual(errorProperties["error"], Schema{"$ref": "#/$defs/TurnError"}) {
		t.Fatalf("ErrorNotification.error = %#v", errorProperties["error"])
	}
	if !reflect.DeepEqual(errorProperties["willRetry"], Schema{"type": "boolean"}) {
		t.Fatalf("ErrorNotification.willRetry = %#v", errorProperties["willRetry"])
	}
	for _, field := range []string{"threadId", "turnId"} {
		if !reflect.DeepEqual(errorProperties[field], Schema{"type": "string"}) {
			t.Errorf("ErrorNotification.%s = %#v", field, errorProperties[field])
		}
	}
}

func TestWarningErrorNotificationsAcceptExactWireValues(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
		value func() any
	}{
		{"warning omitted thread", `{"message":"warn"}`, `{"threadId":null,"message":"warn"}`, func() any { return new(WarningNotification) }},
		{"warning null thread", `{"threadId":null,"message":""}`, `{"threadId":null,"message":""}`, func() any { return new(WarningNotification) }},
		{"warning thread", `{"threadId":"thread","message":"warn"}`, `{"threadId":"thread","message":"warn"}`, func() any { return new(WarningNotification) }},
		{"guardian", `{"threadId":"","message":""}`, `{"threadId":"","message":""}`, func() any { return new(GuardianWarningNotification) }},
		{"error", `{"error":{"message":"boom","codexErrorInfo":null,"additionalDetails":null},"willRetry":false,"threadId":"thread","turnId":"turn"}`, `{"error":{"message":"boom","codexErrorInfo":null,"additionalDetails":null},"willRetry":false,"threadId":"thread","turnId":"turn"}`, func() any { return new(ErrorNotification) }},
		{"retrying error", `{"error":{"message":"","codexErrorInfo":"serverOverloaded","additionalDetails":"retrying"},"willRetry":true,"threadId":"","turnId":""}`, `{"error":{"message":"","codexErrorInfo":"serverOverloaded","additionalDetails":"retrying"},"willRetry":true,"threadId":"","turnId":""}`, func() any { return new(ErrorNotification) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.value()
			if err := json.Unmarshal([]byte(tc.input), value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != tc.want {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, tc.want)
			}
		})
	}
}

func TestWarningErrorNotificationsRejectMalformedWireValues(t *testing.T) {
	warningInvalid := []string{
		`null`, `[]`, `"warning"`, `{}`,
		`{"threadId":null}`, `{"threadId":null,"message":null}`,
		`{"threadId":1,"message":"warn"}`, `{"threadId":null,"message":false}`,
		`{"threadId":null,"message":"warn","details":null}`,
		`{"threadId":null,"message":"warn"} {}`,
	}
	for _, input := range warningInvalid {
		var value WarningNotification
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("WarningNotification accepted %s", input)
		}
	}

	guardianInvalid := []string{
		`null`, `{}`, `{"message":"warn"}`, `{"threadId":"thread"}`,
		`{"threadId":null,"message":"warn"}`, `{"threadId":"thread","message":null}`,
		`{"threadId":"thread","message":"warn","willRetry":false}`,
		`{"threadId":"thread","message":"warn"} {}`,
	}
	for _, input := range guardianInvalid {
		var value GuardianWarningNotification
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("GuardianWarningNotification accepted %s", input)
		}
	}

	errorInvalid := []string{
		`null`, `{}`,
		`{"willRetry":false,"threadId":"thread","turnId":"turn"}`,
		`{"error":{"message":"boom","codexErrorInfo":null,"additionalDetails":null},"threadId":"thread","turnId":"turn"}`,
		`{"error":{"message":"boom","codexErrorInfo":null,"additionalDetails":null},"willRetry":false,"turnId":"turn"}`,
		`{"error":{"message":"boom","codexErrorInfo":null,"additionalDetails":null},"willRetry":false,"threadId":"thread"}`,
		`{"error":null,"willRetry":false,"threadId":"thread","turnId":"turn"}`,
		`{"error":{"message":"boom"},"willRetry":false,"threadId":"thread","turnId":"turn"}`,
		`{"error":{"message":"boom","codexErrorInfo":null,"additionalDetails":null},"willRetry":"false","threadId":"thread","turnId":"turn"}`,
		`{"error":{"message":"boom","codexErrorInfo":null,"additionalDetails":null},"willRetry":false,"threadId":null,"turnId":"turn"}`,
		`{"error":{"message":"boom","codexErrorInfo":null,"additionalDetails":null},"willRetry":false,"threadId":"thread","turnId":1}`,
		`{"error":{"message":"boom","codexErrorInfo":null,"additionalDetails":null},"willRetry":false,"threadId":"thread","turnId":"turn","at":"now"}`,
		`{"error":{"message":"boom","codexErrorInfo":null,"additionalDetails":null},"willRetry":false,"threadId":"thread","turnId":"turn"} {}`,
	}
	for _, input := range errorInvalid {
		var value ErrorNotification
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ErrorNotification accepted %s", input)
		}
	}
}

func TestWarningErrorNotificationNilReceiversAndDistinctTypes(t *testing.T) {
	var warning *WarningNotification
	var guardian *GuardianWarningNotification
	var errorNotification *ErrorNotification
	for name, decode := range map[string]func() error{
		"warning":  func() error { return warning.UnmarshalJSON([]byte(`{}`)) },
		"guardian": func() error { return guardian.UnmarshalJSON([]byte(`{}`)) },
		"error":    func() error { return errorNotification.UnmarshalJSON([]byte(`{}`)) },
	} {
		if err := decode(); err == nil {
			t.Errorf("nil %s receiver succeeded", name)
		}
	}
	types := []reflect.Type{
		reflect.TypeFor[WarningNotification](),
		reflect.TypeFor[GuardianWarningNotification](),
		reflect.TypeFor[ErrorNotification](),
	}
	for i := range types {
		for j := i + 1; j < len(types); j++ {
			if types[i] == types[j] {
				t.Fatalf("types %d and %d unexpectedly alias", i, j)
			}
		}
	}
}

func TestWarningErrorNotificationContractsRemainStandalone(t *testing.T) {
	names := []string{"WarningNotification", "GuardianWarningNotification", "ErrorNotification"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	for _, method := range []string{"warning", "guardianWarning"} {
		info, ok := LookupMethod(method)
		if !ok || info.State != MethodBlocked {
			t.Errorf("%s = %#v, %v; want blocked", method, info, ok)
		}
	}
	if info, ok := LookupMethod("error"); !ok || info.State != MethodImplemented {
		t.Fatalf("error method = %#v, found=%v; want implemented", info, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestWarningErrorNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type WarningNotification = {`,
		`"threadId": string | null;`,
		`"message": string;`,
		`export type GuardianWarningNotification = {`,
		`export type ErrorNotification = {`,
		`"error": TurnError;`,
		`"willRetry": boolean;`,
		`"turnId": string;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

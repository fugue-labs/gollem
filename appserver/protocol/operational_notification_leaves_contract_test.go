package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestOperationalNotificationLeafSchemasAreExact(t *testing.T) {
	object := func(title string, properties Schema, required ...string) Schema {
		out := Schema{"$schema": "http://json-schema.org/draft-07/schema#", "properties": properties, "title": title, "type": "object"}
		if len(required) != 0 {
			out["required"] = required
		}
		return out
	}
	want := map[string]Schema{
		"EnvironmentConnectionNotification": object("EnvironmentConnectionNotification", Schema{
			"environmentId": Schema{"type": "string"}, "threadId": Schema{"type": "string"},
		}, "environmentId", "threadId"),
		"RemoteControlConnectionStatus": stringEnumSchema("disabled", "connecting", "connected", "errored"),
		"RemoteControlStatusChangedNotification": {
			"$schema":     "http://json-schema.org/draft-07/schema#",
			"description": "Current remote-control connection status and remote identity exposed to clients.",
			"properties": Schema{
				"environmentId":  Schema{"type": []any{"string", "null"}},
				"installationId": Schema{"type": "string"},
				"serverName":     Schema{"type": "string"},
				"status":         Schema{"$ref": "#/$defs/RemoteControlConnectionStatus"},
			},
			"required": []string{"installationId", "serverName", "status"},
			"title":    "RemoteControlStatusChangedNotification",
			"type":     "object",
		},
		"SkillsChangedNotification": {
			"$schema": "http://json-schema.org/draft-07/schema#",
			"description": "Notification emitted when watched local skill files change.\n\n" +
				"Treat this as an invalidation signal and re-run `skills/list` with the client's current parameters when refreshed skill metadata is needed.",
			"title": "SkillsChangedNotification",
			"type":  "object",
		},
		"ThreadQueueChangedNotification": object("ThreadQueueChangedNotification", Schema{"threadId": Schema{"type": "string"}}, "threadId"),
		"ThreadRevertedNotification":     object("ThreadRevertedNotification", Schema{"threadId": Schema{"type": "string"}}, "threadId"),
		"WindowsSandboxSetupCompletedNotification": object("WindowsSandboxSetupCompletedNotification", Schema{
			"error": Schema{"type": []any{"string", "null"}}, "mode": Schema{"$ref": "#/$defs/WindowsSandboxSetupMode"}, "success": Schema{"type": "boolean"},
		}, "mode", "success"),
		"WindowsWorldWritableWarningNotification": object("WindowsWorldWritableWarningNotification", Schema{
			"extraCount":  Schema{"format": "uint", "minimum": 0, "type": "integer"},
			"failedScan":  Schema{"type": "boolean"},
			"samplePaths": Schema{"items": Schema{"type": "string"}, "type": "array"},
		}, "extraCount", "failedScan", "samplePaths"),
	}
	definitions := JSONSchema()["$defs"].(Schema)
	for name, expected := range want {
		if got := definitions[name]; !reflect.DeepEqual(got, expected) {
			t.Errorf("%s = %#v, want %#v", name, got, expected)
		}
	}
}

func TestOperationalNotificationLeavesPreserveSerdeWireForms(t *testing.T) {
	fixtures := []struct {
		name  string
		input string
		want  string
		value func() any
	}{
		{"environment", `{"future":1,"threadId":"thread","environmentId":"environment"}`, `{"threadId":"thread","environmentId":"environment"}`, func() any { return new(EnvironmentConnectionNotification) }},
		{"skills empty", `{"future":1,"future":2}`, `{}`, func() any { return new(SkillsChangedNotification) }},
		{"queue", `{"future":1,"threadId":"thread"}`, `{"threadId":"thread"}`, func() any { return new(ThreadQueueChangedNotification) }},
		{"reverted", `{"future":1,"threadId":"thread"}`, `{"threadId":"thread"}`, func() any { return new(ThreadRevertedNotification) }},
		{"remote absent environment", `{"future":1,"status":"connected","serverName":"server","installationId":"install"}`, `{"status":"connected","serverName":"server","installationId":"install","environmentId":null}`, func() any { return new(RemoteControlStatusChangedNotification) }},
		{"remote environment", `{"status":"errored","serverName":"","installationId":"","environmentId":"environment"}`, `{"status":"errored","serverName":"","installationId":"","environmentId":"environment"}`, func() any { return new(RemoteControlStatusChangedNotification) }},
		{"windows setup absent error", `{"future":1,"mode":"elevated","success":false}`, `{"mode":"elevated","success":false,"error":null}`, func() any { return new(WindowsSandboxSetupCompletedNotification) }},
		{"windows setup error", `{"mode":"unelevated","success":true,"error":"message"}`, `{"mode":"unelevated","success":true,"error":"message"}`, func() any { return new(WindowsSandboxSetupCompletedNotification) }},
		{"world writable", `{"future":1,"samplePaths":["","path"],"extraCount":18446744073709551615,"failedScan":true}`, `{"samplePaths":["","path"],"extraCount":18446744073709551615,"failedScan":true}`, func() any { return new(WindowsWorldWritableWarningNotification) }},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			value := fixture.value()
			if err := json.Unmarshal([]byte(fixture.input), value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != fixture.want {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, fixture.want)
			}
		})
	}

	for _, value := range []RemoteControlConnectionStatus{
		RemoteControlConnectionStatusDisabled,
		RemoteControlConnectionStatusConnecting,
		RemoteControlConnectionStatusConnected,
		RemoteControlConnectionStatusErrored,
	} {
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != `"`+string(value)+`"` {
			t.Errorf("remote status %q = %s, %v", value, encoded, err)
		}
	}
	var warning WindowsWorldWritableWarningNotification
	if encoded, err := json.Marshal(warning); err != nil || string(encoded) != `{"samplePaths":[],"extraCount":0,"failedScan":false}` {
		t.Fatalf("zero world writable warning = %s, %v", encoded, err)
	}
	if uint64(math.MaxUint64) != ^uint64(0) {
		t.Fatal("unexpected uint64 maximum")
	}
}

func TestOperationalNotificationLeavesRejectMalformedWireForms(t *testing.T) {
	assertOperationalLeafRejects[EnvironmentConnectionNotification](t,
		``, `null`, `[]`, `{}`, `{"threadId":null,"environmentId":"environment"}`,
		`{"threadId":"thread","environmentId":1}`, `{"threadId":"one","threadId":"two","environmentId":"environment"}`, `{} {}`)
	assertOperationalLeafRejects[SkillsChangedNotification](t, ``, `null`, `[]`, `"value"`, `{`, `{} {}`, `{} x`)
	assertOperationalLeafRejects[ThreadQueueChangedNotification](t,
		``, `null`, `[]`, `{}`, `{"threadId":null}`, `{"threadId":1}`, `{"threadId":"one","threadId":"two"}`, `{} {}`)
	assertOperationalLeafRejects[ThreadRevertedNotification](t,
		``, `null`, `[]`, `{}`, `{"threadId":null}`, `{"threadId":1}`, `{"threadId":"one","threadId":"two"}`, `{} {}`)
	assertOperationalLeafRejects[RemoteControlStatusChangedNotification](t,
		``, `null`, `[]`, `{}`, `{"status":"connected","serverName":"server"}`,
		`{"status":"future","serverName":"server","installationId":"install"}`,
		`{"status":null,"serverName":"server","installationId":"install"}`,
		`{"status":"connected","serverName":"server","installationId":"install","environmentId":1}`,
		`{"status":"connected","status":"errored","serverName":"server","installationId":"install"}`, `{} {}`)
	assertOperationalLeafRejects[WindowsSandboxSetupCompletedNotification](t,
		``, `null`, `[]`, `{}`, `{"success":true}`, `{"mode":"elevated"}`,
		`{"mode":"future","success":true}`, `{"mode":"elevated","success":null}`,
		`{"mode":"elevated","success":true,"error":false}`,
		`{"mode":"elevated","mode":"unelevated","success":true}`, `{} {}`)
	assertOperationalLeafRejects[WindowsWorldWritableWarningNotification](t,
		``, `null`, `[]`, `{}`, `{"samplePaths":[],"extraCount":0}`,
		`{"samplePaths":null,"extraCount":0,"failedScan":false}`,
		`{"samplePaths":[null],"extraCount":0,"failedScan":false}`,
		`{"samplePaths":[],"extraCount":-1,"failedScan":false}`,
		`{"samplePaths":[],"extraCount":18446744073709551616,"failedScan":false}`,
		`{"samplePaths":[],"extraCount":0,"failedScan":0}`,
		`{"samplePaths":[],"extraCount":0,"extraCount":1,"failedScan":false}`, `{} {}`)
	assertOperationalLeafRejects[RemoteControlConnectionStatus](t,
		``, `null`, `[]`, `{}`, `"future"`, `"connected" {}`)
}

func TestOperationalNotificationLeavesFailClosedAndRemainStandalone(t *testing.T) {
	checks := map[string]func() error{
		"environment": func() error { var value *EnvironmentConnectionNotification; return value.UnmarshalJSON([]byte(`{}`)) },
		"skills":      func() error { var value *SkillsChangedNotification; return value.UnmarshalJSON([]byte(`{}`)) },
		"queue":       func() error { var value *ThreadQueueChangedNotification; return value.UnmarshalJSON([]byte(`{}`)) },
		"reverted":    func() error { var value *ThreadRevertedNotification; return value.UnmarshalJSON([]byte(`{}`)) },
		"remote": func() error {
			var value *RemoteControlStatusChangedNotification
			return value.UnmarshalJSON([]byte(`{}`))
		},
		"windows setup": func() error {
			var value *WindowsSandboxSetupCompletedNotification
			return value.UnmarshalJSON([]byte(`{}`))
		},
		"world writable": func() error {
			var value *WindowsWorldWritableWarningNotification
			return value.UnmarshalJSON([]byte(`{}`))
		},
		"remote status": func() error {
			var value *RemoteControlConnectionStatus
			return value.UnmarshalJSON([]byte(`"connected"`))
		},
	}
	for name, check := range checks {
		if err := check(); err == nil {
			t.Errorf("nil %s receiver succeeded", name)
		}
	}

	names := []string{
		"EnvironmentConnectionNotification", "SkillsChangedNotification", "ThreadQueueChangedNotification", "ThreadRevertedNotification",
		"RemoteControlConnectionStatus", "RemoteControlStatusChangedNotification", "WindowsSandboxSetupCompletedNotification", "WindowsWorldWritableWarningNotification",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if slices.Contains(names, binding.Type) {
			t.Fatalf("%s unexpectedly bound to item %s", binding.Type, binding.Kind)
		}
	}
	for _, methodName := range []string{"skills/changed", "remoteControl/status/changed", "windows/worldWritableWarning", "windowsSandbox/setupCompleted"} {
		method, ok := LookupMethod(methodName)
		if !ok || method.Surface != SurfaceServerNotification || method.State != MethodDeferredStub {
			t.Errorf("%s = %#v, %v; want deferred notification", methodName, method, ok)
		}
	}
	for _, methodName := range []string{"thread/environment/connected", "thread/environment/disconnected", "thread/queue/changed", "thread/reverted"} {
		if method, ok := LookupMethod(methodName); ok {
			t.Errorf("%s = %#v; want no experimental method registration", methodName, method)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if len(Methods()) != 229 || len(WireTypeBindings()) != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("counts = %d methods/%d method bindings/%d item bindings; want 229/86/5", len(Methods()), len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
}

func TestOperationalNotificationLeavesTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type EnvironmentConnectionNotification = {\n  \"environmentId\": string;\n  \"threadId\": string;\n};",
		`export type RemoteControlConnectionStatus = "disabled" | "connecting" | "connected" | "errored";`,
		"export type RemoteControlStatusChangedNotification = {\n  \"environmentId\": string | null;\n  \"installationId\": string;\n  \"serverName\": string;\n  \"status\": RemoteControlConnectionStatus;\n};",
		`export type SkillsChangedNotification = Record<string, never>;`,
		"export type ThreadQueueChangedNotification = {\n  \"threadId\": string;\n};",
		"export type ThreadRevertedNotification = {\n  \"threadId\": string;\n};",
		"export type WindowsSandboxSetupCompletedNotification = {\n  \"error\": string | null;\n  \"mode\": WindowsSandboxSetupMode;\n  \"success\": boolean;\n};",
		"export type WindowsWorldWritableWarningNotification = {\n  \"extraCount\": number;\n  \"failedScan\": boolean;\n  \"samplePaths\": Array<string>;\n};",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertOperationalLeafRejects[T any](t *testing.T, inputs ...string) {
	t.Helper()
	for _, input := range inputs {
		assertJSONRejects[T](t, input)
	}
}

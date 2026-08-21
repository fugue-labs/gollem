package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExternalAgentConfigImportNotificationSchemasAreExact(t *testing.T) {
	want := closedThreadSessionParamSchema(Schema{
		"importId": Schema{"type": "string"},
		"itemTypeResults": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/ExternalAgentConfigImportTypeResult"},
		},
	}, []string{"importId", "itemTypeResults"})
	for _, name := range []string{
		"ExternalAgentConfigImportProgressNotification",
		"ExternalAgentConfigImportCompletedNotification",
	} {
		got, ok := JSONSchema()["$defs"].(Schema)[name].(Schema)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s schema = %#v, %v; want %#v", name, got, ok, want)
		}
	}
}

func TestExternalAgentConfigImportNotificationsAcceptRustWireForms(t *testing.T) {
	inputs := []struct {
		name      string
		input     string
		canonical string
	}{
		{
			name:      "empty values and unknown",
			input:     `{"importId":"","itemTypeResults":[],"future":true}`,
			canonical: `{"importId":"","itemTypeResults":[]}`,
		},
		{
			name: "ordered duplicate strict results",
			input: `{"importId":" import-1 ","itemTypeResults":[` +
				`{"itemType":"CONFIG","successes":[],"failures":[]},` +
				`{"itemType":"HOOKS","successes":[{"itemType":"SKILLS","cwd":"repo/../repo","source":" Claude Code ","target":""}],"failures":[{"itemType":"HOOKS","failureStage":"","message":""}]},` +
				`{"itemType":"CONFIG","successes":[],"failures":[]}` +
				`]}`,
			canonical: `{"importId":" import-1 ","itemTypeResults":[` +
				`{"itemType":"CONFIG","successes":[],"failures":[]},` +
				`{"itemType":"HOOKS","successes":[{"itemType":"SKILLS","cwd":"repo/../repo","source":" Claude Code ","target":""}],"failures":[{"itemType":"HOOKS","errorType":null,"failureStage":"","message":"","cwd":null,"source":null}]},` +
				`{"itemType":"CONFIG","successes":[],"failures":[]}` +
				`]}`,
		},
	}

	for _, tc := range inputs {
		t.Run(tc.name+"/progress", func(t *testing.T) {
			var got ExternalAgentConfigImportProgressNotification
			assertExternalAgentConfigImportNotificationRoundTrip(t, tc.input, tc.canonical, &got)
		})
		t.Run(tc.name+"/completed", func(t *testing.T) {
			var got ExternalAgentConfigImportCompletedNotification
			assertExternalAgentConfigImportNotificationRoundTrip(t, tc.input, tc.canonical, &got)
		})
	}

	progress, err := json.Marshal(ExternalAgentConfigImportProgressNotification{})
	if err != nil || string(progress) != `{"importId":"","itemTypeResults":[]}` {
		t.Fatalf("progress nil slice = %s, %v; want non-null empty array", progress, err)
	}
	completed, err := json.Marshal(ExternalAgentConfigImportCompletedNotification{})
	if err != nil || string(completed) != `{"importId":"","itemTypeResults":[]}` {
		t.Fatalf("completed nil slice = %s, %v; want non-null empty array", completed, err)
	}
}

func assertExternalAgentConfigImportNotificationRoundTrip(
	t *testing.T,
	input string,
	canonical string,
	value interface {
		json.Marshaler
		json.Unmarshaler
	},
) {
	t.Helper()
	if err := value.UnmarshalJSON([]byte(input)); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	encoded, err := value.MarshalJSON()
	if err != nil || string(encoded) != canonical {
		t.Fatalf("canonical = %s, %v; want %s", encoded, err, canonical)
	}
}

func TestExternalAgentConfigImportNotificationsRejectMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"itemTypeResults":[]}`,
		`{"importId":null,"itemTypeResults":[]}`,
		`{"importId":1,"itemTypeResults":[]}`,
		`{"importId":"id"}`,
		`{"importId":"id","itemTypeResults":null}`,
		`{"importId":"id","itemTypeResults":{}}`,
		`{"importId":"id","itemTypeResults":[null]}`,
		`{"importId":"id","itemTypeResults":[{}]}`,
		`{"importId":"id","itemTypeResults":[{"itemType":"OTHER","successes":[],"failures":[]}]}`,
		`{"importId":"id","itemTypeResults":[{"itemType":"CONFIG","successes":[{}],"failures":[]}]}`,
		`{"importId":"id","itemTypeResults":[{"itemType":"HOOKS","successes":[],"failures":[{"itemType":"HOOKS","message":"missing stage"}]}]}`,
		`{"importId":"a","importId":"b","itemTypeResults":[]}`,
		`{"importId":"id","itemTypeResults":[],"itemTypeResults":[]}`,
		`{"importId":"id","itemTypeResults":[]`,
		`{"importId":"id","itemTypeResults":[]} {}`,
	}
	for _, input := range invalid {
		var progress ExternalAgentConfigImportProgressNotification
		if err := json.Unmarshal([]byte(input), &progress); err == nil {
			t.Errorf("progress Unmarshal(%s) succeeded", input)
		}
		var completed ExternalAgentConfigImportCompletedNotification
		if err := json.Unmarshal([]byte(input), &completed); err == nil {
			t.Errorf("completed Unmarshal(%s) succeeded", input)
		}
	}

	var progress *ExternalAgentConfigImportProgressNotification
	if err := progress.UnmarshalJSON([]byte(`{"importId":"id","itemTypeResults":[]}`)); err == nil {
		t.Fatal("nil progress receiver succeeded")
	}
	var completed *ExternalAgentConfigImportCompletedNotification
	if err := completed.UnmarshalJSON([]byte(`{"importId":"id","itemTypeResults":[]}`)); err == nil {
		t.Fatal("nil completed receiver succeeded")
	}
	bad := []ExternalAgentConfigImportTypeResult{{}}
	if _, err := json.Marshal(ExternalAgentConfigImportProgressNotification{ItemTypeResults: bad}); err == nil {
		t.Fatal("progress with invalid nested result marshaled")
	}
	if _, err := json.Marshal(ExternalAgentConfigImportCompletedNotification{ItemTypeResults: bad}); err == nil {
		t.Fatal("completed with invalid nested result marshaled")
	}
}

func TestExternalAgentConfigImportNotificationsRemainStandalone(t *testing.T) {
	names := []string{
		"ExternalAgentConfigImportProgressNotification",
		"ExternalAgentConfigImportCompletedNotification",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound: %#v", name, binding)
			}
		}
	}
	for _, method := range []string{
		"externalAgentConfig/import",
		"externalAgentConfig/import/readHistories",
		"externalAgentConfig/import/progress",
		"externalAgentConfig/import/completed",
	} {
		info, ok := LookupMethod(method)
		if !ok || info.State != MethodDeferredStub {
			t.Fatalf("%s = %#v, %v; want deferred stub", method, info, ok)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestExternalAgentConfigImportNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, name := range []string{
		"ExternalAgentConfigImportProgressNotification",
		"ExternalAgentConfigImportCompletedNotification",
	} {
		want := "export type " + name + " = {\n" +
			"  \"importId\": string;\n" +
			"  \"itemTypeResults\": Array<ExternalAgentConfigImportTypeResult>;\n" +
			"};"
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = ExternalAgentConfigImportProgressNotification{}
	_ json.Unmarshaler = (*ExternalAgentConfigImportProgressNotification)(nil)
	_ json.Marshaler   = ExternalAgentConfigImportCompletedNotification{}
	_ json.Unmarshaler = (*ExternalAgentConfigImportCompletedNotification)(nil)
)

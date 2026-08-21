package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadSettingsSchemasAreExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	wants := map[string]Schema{
		"ThreadExtra": {
			"description": "Extra app-server data for a thread.",
			"type":        "object",
		},
		"ThreadHistoryMode": stringEnumSchema("legacy", "paginated"),
		"ThreadSettings": {
			"properties": Schema{
				"activePermissionProfile": nullableSchemaRef("ActivePermissionProfile"),
				"approvalPolicy":          Schema{"$ref": "#/$defs/AskForApproval"},
				"approvalsReviewer":       Schema{"$ref": "#/$defs/ApprovalsReviewer"},
				"collaborationMode":       Schema{"$ref": "#/$defs/CollaborationMode"},
				"cwd":                     Schema{"$ref": "#/$defs/AbsolutePathBuf"},
				"effort":                  nullableSchemaRef("ReasoningEffort"),
				"model":                   Schema{"type": "string"},
				"modelProvider":           Schema{"type": "string"},
				"personality":             nullableSchemaRef("Personality"),
				"sandboxPolicy":           Schema{"$ref": "#/$defs/SandboxPolicy"},
				"serviceTier":             Schema{"type": []any{"string", "null"}},
				"summary":                 nullableSchemaRef("ReasoningSummary"),
			},
			"required": []string{
				"approvalPolicy", "approvalsReviewer", "collaborationMode", "cwd",
				"model", "modelProvider", "sandboxPolicy",
			},
			"type": "object",
		},
		"ThreadSettingsUpdatedNotification": {
			"$schema": "http://json-schema.org/draft-07/schema#",
			"properties": Schema{
				"threadId":       Schema{"type": "string"},
				"threadSettings": Schema{"$ref": "#/$defs/ThreadSettings"},
			},
			"required": []string{"threadId", "threadSettings"},
			"title":    "ThreadSettingsUpdatedNotification",
			"type":     "object",
		},
	}
	for name, want := range wants {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestThreadSettingsContractsPreserveSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`"legacy"`, `"legacy"`},
		{`"paginated"`, `"paginated"`},
	} {
		var mode ThreadHistoryMode
		if err := json.Unmarshal([]byte(tc.input), &mode); err != nil {
			t.Errorf("unmarshal mode %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(mode)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("mode round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}

	var extra ThreadExtra
	if err := json.Unmarshal([]byte(`{"future":true}`), &extra); err != nil {
		t.Fatalf("unmarshal extra: %v", err)
	}
	if encoded, err := json.Marshal(extra); err != nil || string(encoded) != `{}` {
		t.Fatalf("extra round trip = %s, %v; want {}", encoded, err)
	}

	const settings = `{"future":true,"cwd":"/workspace/./project","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"dangerFullAccess"},"model":"gpt-5","modelProvider":"openai","collaborationMode":{"mode":"default","settings":{"model":"gpt-5"}}}`
	const canonicalSettings = `{"cwd":"/workspace/project","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"dangerFullAccess"},"activePermissionProfile":null,"model":"gpt-5","modelProvider":"openai","serviceTier":null,"effort":null,"summary":null,"collaborationMode":{"mode":"default","settings":{"model":"gpt-5","reasoning_effort":null,"developer_instructions":null}},"personality":null}`
	var decoded ThreadSettings
	if err := json.Unmarshal([]byte(settings), &decoded); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil || string(encoded) != canonicalSettings {
		t.Fatalf("settings round trip = %s, %v; want %s", encoded, err, canonicalSettings)
	}

	var notification ThreadSettingsUpdatedNotification
	if err := json.Unmarshal([]byte(`{"future":true,"threadId":"thread","threadSettings":`+settings+`}`), &notification); err != nil {
		t.Fatalf("unmarshal notification: %v", err)
	}
	encoded, err = json.Marshal(notification)
	wantNotification := `{"threadId":"thread","threadSettings":` + canonicalSettings + `}`
	if err != nil || string(encoded) != wantNotification {
		t.Fatalf("notification round trip = %s, %v; want %s", encoded, err, wantNotification)
	}
}

func TestThreadSettingsContractsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `"other"`, `"legacy" {}`,
	} {
		assertJSONRejects[ThreadHistoryMode](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}` + ` {}`,
	} {
		assertJSONRejects[ThreadExtra](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"cwd":"relative","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"dangerFullAccess"},"model":"gpt-5","modelProvider":"openai","collaborationMode":{"mode":"default","settings":{"model":"gpt-5"}}}`,
		`{"cwd":"/workspace","approvalPolicy":"other","approvalsReviewer":"user","sandboxPolicy":{"type":"dangerFullAccess"},"model":"gpt-5","modelProvider":"openai","collaborationMode":{"mode":"default","settings":{"model":"gpt-5"}}}`,
		`{"cwd":"/workspace","approvalPolicy":"never","approvalsReviewer":"other","sandboxPolicy":{"type":"dangerFullAccess"},"model":"gpt-5","modelProvider":"openai","collaborationMode":{"mode":"default","settings":{"model":"gpt-5"}}}`,
		`{"cwd":"/workspace","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"dangerFullAccess"},"model":null,"modelProvider":"openai","collaborationMode":{"mode":"default","settings":{"model":"gpt-5"}}}`,
		`{"cwd":"/workspace","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"dangerFullAccess"},"model":"gpt-5","modelProvider":"openai","collaborationMode":null}`,
		`{"cwd":"/workspace","approvalPolicy":"never","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"dangerFullAccess"},"model":"gpt-5","modelProvider":"openai","collaborationMode":{"mode":"default","settings":{"model":"gpt-5"}}}`,
	} {
		assertJSONRejects[ThreadSettings](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`, `{"threadId":"thread"}`,
		`{"threadSettings":{"cwd":"/workspace","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"dangerFullAccess"},"model":"gpt-5","modelProvider":"openai","collaborationMode":{"mode":"default","settings":{"model":"gpt-5"}}}}`,
		`{"threadId":null,"threadSettings":{}}`,
		`{"threadId":"a","threadId":"b","threadSettings":{}}`,
	} {
		assertJSONRejects[ThreadSettingsUpdatedNotification](t, input)
	}
}

func TestThreadSettingsContractsFailClosedAndRemainStandalone(t *testing.T) {
	var mode *ThreadHistoryMode
	if err := mode.UnmarshalJSON([]byte(`"legacy"`)); err == nil {
		t.Fatal("nil history-mode receiver succeeded")
	}
	var extra *ThreadExtra
	if err := extra.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil extra receiver succeeded")
	}
	var settings *ThreadSettings
	if err := settings.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil settings receiver succeeded")
	}
	var notification *ThreadSettingsUpdatedNotification
	if err := notification.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil notification receiver succeeded")
	}
	if _, err := json.Marshal(ThreadHistoryMode("other")); err == nil {
		t.Fatal("invalid history mode marshaled")
	}

	names := []string{"ThreadExtra", "ThreadHistoryMode", "ThreadSettings", "ThreadSettingsUpdatedNotification"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestThreadSettingsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type ThreadExtra = Record<string, never>;`,
		`export type ThreadHistoryMode = "legacy" | "paginated";`,
		"export type ThreadSettings = {\n" +
			"  \"activePermissionProfile\"?: ActivePermissionProfile | null;\n" +
			"  \"approvalPolicy\": AskForApproval;\n" +
			"  \"approvalsReviewer\": ApprovalsReviewer;\n" +
			"  \"collaborationMode\": CollaborationMode;\n" +
			"  \"cwd\": AbsolutePathBuf;\n" +
			"  \"effort\"?: ReasoningEffort | null;\n" +
			"  \"model\": string;\n" +
			"  \"modelProvider\": string;\n" +
			"  \"personality\"?: Personality | null;\n" +
			"  \"sandboxPolicy\": SandboxPolicy;\n" +
			"  \"serviceTier\"?: string | null;\n" +
			"  \"summary\"?: ReasoningSummary | null;\n" +
			"};",
		"export type ThreadSettingsUpdatedNotification = {\n" +
			"  \"threadId\": string;\n" +
			"  \"threadSettings\": ThreadSettings;\n" +
			"};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

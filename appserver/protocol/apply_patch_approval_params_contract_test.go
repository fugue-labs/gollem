package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestApplyPatchApprovalParamsSchemaIsExact(t *testing.T) {
	definition, ok := JSONSchema()["$defs"].(Schema)["ApplyPatchApprovalParams"].(Schema)
	if !ok {
		t.Fatal("$defs missing ApplyPatchApprovalParams")
	}
	want := sourceApprovalParamsSchema("ApplyPatchApprovalParams", Schema{
		"conversationId": Schema{"$ref": "#/$defs/ThreadId"},
		"callId": Schema{
			"type": "string",
			"description": "Use to correlate this with [codex_protocol::protocol::PatchApplyBeginEvent] " +
				"and [codex_protocol::protocol::PatchApplyEndEvent].",
		},
		"fileChanges": Schema{
			"type":                 "object",
			"additionalProperties": Schema{"$ref": "#/$defs/FileChange"},
		},
		"reason": Schema{
			"type":        []any{"string", "null"},
			"description": "Optional explanatory reason (e.g. request for extra write access).",
		},
		"grantRoot": Schema{
			"type": []any{"string", "null"},
			"description": "When set, the agent is asking the user to allow writes under this root " +
				"for the remainder of the session (unclear if this is honored today).",
		},
	}, []string{"callId", "conversationId", "fileChanges"})
	if !reflect.DeepEqual(definition, want) {
		t.Fatalf("ApplyPatchApprovalParams = %#v, want %#v", definition, want)
	}
}

func TestApplyPatchApprovalParamsAcceptsSerdeWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{
			input: `{"conversationId":"","callId":"","fileChanges":{}}`,
			want:  `{"conversationId":"","callId":"","fileChanges":{},"reason":null,"grantRoot":null}`,
		},
		{
			input: `{"conversationId":"thread","callId":"call","fileChanges":{"relative path":{"type":"add","content":""},"/absolute":{"type":"delete","content":"old"}},"reason":null,"grantRoot":null}`,
			want:  `{"conversationId":"thread","callId":"call","fileChanges":{"/absolute":{"type":"delete","content":"old"},"relative path":{"type":"add","content":""}},"reason":null,"grantRoot":null}`,
		},
		{
			input: `{"conversationId":"thread-2","callId":"call-2","fileChanges":{"":{"type":"update","unified_diff":"diff","move_path":"next path"}},"reason":"","grantRoot":"relative/root"}`,
			want:  `{"conversationId":"thread-2","callId":"call-2","fileChanges":{"":{"type":"update","unified_diff":"diff","move_path":"next path"}},"reason":"","grantRoot":"relative/root"}`,
		},
		{
			input: `{"future":1,"future":2,"conversationId":"thread","callId":"call","fileChanges":{"same":{"type":"add","content":"first"},"same":{"type":"delete","content":"last"}},"other":{"ignored":true}}`,
			want:  `{"conversationId":"thread","callId":"call","fileChanges":{"same":{"type":"delete","content":"last"}},"reason":null,"grantRoot":null}`,
		},
	} {
		var params ApplyPatchApprovalParams
		if err := json.Unmarshal([]byte(test.input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", test.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != test.want {
			t.Errorf("round trip %s = %s, %v; want %s", test.input, encoded, err, test.want)
		}
	}

	encoded, err := json.Marshal(ApplyPatchApprovalParams{})
	want := `{"conversationId":"","callId":"","fileChanges":{},"reason":null,"grantRoot":null}`
	if err != nil || string(encoded) != want {
		t.Fatalf("marshal zero params = %s, %v; want %s", encoded, err, want)
	}
}

func TestApplyPatchApprovalParamsRejectsMalformedWireForms(t *testing.T) {
	valid := `"conversationId":"thread","callId":"call","fileChanges":{}`
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"callId":"call","fileChanges":{}}`,
		`{"conversationId":"thread","fileChanges":{}}`,
		`{"conversationId":"thread","callId":"call"}`,
		`{"conversationId":null,"callId":"call","fileChanges":{}}`,
		`{"conversationId":1,"callId":"call","fileChanges":{}}`,
		`{"conversationId":"thread","callId":null,"fileChanges":{}}`,
		`{"conversationId":"thread","callId":1,"fileChanges":{}}`,
		`{"conversationId":"thread","callId":"call","fileChanges":null}`,
		`{"conversationId":"thread","callId":"call","fileChanges":[]}`,
		`{"conversationId":"thread","callId":"call","fileChanges":{"path":null}}`,
		`{"conversationId":"thread","callId":"call","fileChanges":{"path":{"type":"add"}}}`,
		`{"conversationId":"thread","callId":"call","fileChanges":{},"reason":1}`,
		`{"conversationId":"thread","callId":"call","fileChanges":{},"grantRoot":1}`,
		`{"conversationId":"one","conversationId":"two","callId":"call","fileChanges":{}}`,
		`{"conversationId":"thread","callId":"one","callId":"two","fileChanges":{}}`,
		`{"conversationId":"thread","callId":"call","fileChanges":{},"fileChanges":{}}`,
		`{"conversationId":"thread","callId":"call","fileChanges":{},"reason":null,"reason":"why"}`,
		`{"conversationId":"thread","callId":"call","fileChanges":{},"grantRoot":null,"grantRoot":"root"}`,
		`{` + valid + `} {}`,
		`{` + valid + `} x`,
	} {
		assertJSONRejects[ApplyPatchApprovalParams](t, input)
	}

	var params *ApplyPatchApprovalParams
	if err := params.UnmarshalJSON([]byte(`{` + valid + `}`)); err == nil {
		t.Fatal("nil ApplyPatchApprovalParams receiver succeeded")
	}
	if _, err := json.Marshal(ApplyPatchApprovalParams{
		FileChanges: map[string]FileChange{"path": {}},
	}); err == nil {
		t.Fatal("invalid nested FileChange marshaled")
	}
}

func TestApplyPatchApprovalParamsRemainsStandalone(t *testing.T) {
	if reflect.TypeFor[ApplyPatchApprovalParams]() == reflect.TypeFor[FileChangeApprovalRequestParams]() {
		t.Fatal("legacy apply-patch params alias live file-change approval params")
	}
	defs := JSONSchema()["$defs"].(Schema)
	for _, name := range []string{"ThreadId", "FileChange", "ApplyPatchApprovalResponse"} {
		if _, ok := defs[name]; !ok {
			t.Fatalf("dependency-complete %s missing", name)
		}
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ApplyPatchApprovalParams") ||
			slices.Contains(binding.Result, "ApplyPatchApprovalParams") {
			t.Fatalf("ApplyPatchApprovalParams unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "ApplyPatchApprovalParams" {
			t.Fatalf("ApplyPatchApprovalParams unexpectedly bound to item %s", binding.Kind)
		}
	}
	if got := len(defs); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestApplyPatchApprovalParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type ApplyPatchApprovalParams = {\n" +
		"  \"callId\": string;\n" +
		"  \"conversationId\": ThreadId;\n" +
		"  \"fileChanges\": { [key in string]?: FileChange };\n" +
		"  \"grantRoot\": string | null;\n" +
		"  \"reason\": string | null;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}

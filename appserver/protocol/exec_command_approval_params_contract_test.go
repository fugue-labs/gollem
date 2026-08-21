package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExecCommandApprovalParamsSchemaIsExact(t *testing.T) {
	definition, ok := JSONSchema()["$defs"].(Schema)["ExecCommandApprovalParams"].(Schema)
	if !ok {
		t.Fatal("$defs missing ExecCommandApprovalParams")
	}
	want := sourceApprovalParamsSchema("ExecCommandApprovalParams", Schema{
		"conversationId": Schema{"$ref": "#/$defs/ThreadId"},
		"callId": Schema{
			"type": "string",
			"description": "Use to correlate this with [codex_protocol::protocol::ExecCommandBeginEvent] " +
				"and [codex_protocol::protocol::ExecCommandEndEvent].",
		},
		"approvalId": Schema{
			"type":        []any{"string", "null"},
			"description": "Identifier for this specific approval callback.",
		},
		"command": Schema{"type": "array", "items": Schema{"type": "string"}},
		"cwd":     Schema{"type": "string"},
		"reason":  Schema{"type": []any{"string", "null"}},
		"parsedCmd": Schema{
			"type":  "array",
			"items": Schema{"$ref": "#/$defs/ParsedCommand"},
		},
	}, []string{"callId", "command", "conversationId", "cwd", "parsedCmd"})
	if !reflect.DeepEqual(definition, want) {
		t.Fatalf("ExecCommandApprovalParams = %#v, want %#v", definition, want)
	}
}

func TestExecCommandApprovalParamsAcceptsSerdeWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{
			input: `{"conversationId":"","callId":"","command":[],"cwd":"","parsedCmd":[]}`,
			want:  `{"conversationId":"","callId":"","approvalId":null,"command":[],"cwd":"","reason":null,"parsedCmd":[]}`,
		},
		{
			input: `{"conversationId":"thread-1","callId":"call-1","approvalId":null,"command":["sh","-lc","pwd"],"cwd":"relative/worktree","reason":null,"parsedCmd":[{"type":"unknown","cmd":"pwd"}]}`,
			want:  `{"conversationId":"thread-1","callId":"call-1","approvalId":null,"command":["sh","-lc","pwd"],"cwd":"relative/worktree","reason":null,"parsedCmd":[{"type":"unknown","cmd":"pwd"}]}`,
		},
		{
			input: `{"conversationId":"thread-2","callId":"call-2","approvalId":"approval-2","command":["rg","q","q"],"cwd":"/workspace","reason":"inspect","parsedCmd":[{"type":"read","cmd":"cat f","name":"cat","path":"relative/f"},{"type":"list_files","cmd":"ls"},{"type":"search","cmd":"rg q","query":"q","path":null}]}`,
			want:  `{"conversationId":"thread-2","callId":"call-2","approvalId":"approval-2","command":["rg","q","q"],"cwd":"/workspace","reason":"inspect","parsedCmd":[{"type":"read","cmd":"cat f","name":"cat","path":"relative/f"},{"type":"list_files","cmd":"ls","path":null},{"type":"search","cmd":"rg q","query":"q","path":null}]}`,
		},
		{
			input: `{"future":1,"future":2,"conversationId":"thread","callId":"call","command":["echo"],"cwd":".","parsedCmd":[],"other":{"ignored":true}}`,
			want:  `{"conversationId":"thread","callId":"call","approvalId":null,"command":["echo"],"cwd":".","reason":null,"parsedCmd":[]}`,
		},
	} {
		var params ExecCommandApprovalParams
		if err := json.Unmarshal([]byte(test.input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", test.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != test.want {
			t.Errorf("round trip %s = %s, %v; want %s", test.input, encoded, err, test.want)
		}
	}

	encoded, err := json.Marshal(ExecCommandApprovalParams{})
	want := `{"conversationId":"","callId":"","approvalId":null,"command":[],"cwd":"","reason":null,"parsedCmd":[]}`
	if err != nil || string(encoded) != want {
		t.Fatalf("marshal zero params = %s, %v; want %s", encoded, err, want)
	}
}

func TestExecCommandApprovalParamsRejectsMalformedWireForms(t *testing.T) {
	valid := `"conversationId":"thread","callId":"call","command":[],"cwd":".","parsedCmd":[]`
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"callId":"call","command":[],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","command":[],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":[],"parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":[],"cwd":"."}`,
		`{"conversationId":null,"callId":"call","command":[],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":1,"callId":"call","command":[],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":null,"command":[],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":1,"command":[],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","approvalId":1,"command":[],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":null,"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":{},"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":[1],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":[],"cwd":null,"parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":[],"cwd":1,"parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":[],"cwd":".","reason":1,"parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":[],"cwd":".","parsedCmd":null}`,
		`{"conversationId":"thread","callId":"call","command":[],"cwd":".","parsedCmd":{}}`,
		`{"conversationId":"thread","callId":"call","command":[],"cwd":".","parsedCmd":[null]}`,
		`{"conversationId":"thread","callId":"call","command":[],"cwd":".","parsedCmd":[{"type":"unknown"}]}`,
		`{"conversationId":"one","conversationId":"two","callId":"call","command":[],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"one","callId":"two","command":[],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","approvalId":null,"approvalId":"id","command":[],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":[],"command":[],"cwd":".","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":[],"cwd":"one","cwd":"two","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":[],"cwd":".","reason":null,"reason":"why","parsedCmd":[]}`,
		`{"conversationId":"thread","callId":"call","command":[],"cwd":".","parsedCmd":[],"parsedCmd":[]}`,
		`{` + valid + `} {}`,
		`{` + valid + `} x`,
	} {
		assertJSONRejects[ExecCommandApprovalParams](t, input)
	}

	var params *ExecCommandApprovalParams
	if err := params.UnmarshalJSON([]byte(`{` + valid + `}`)); err == nil {
		t.Fatal("nil ExecCommandApprovalParams receiver succeeded")
	}
}

func TestExecCommandApprovalParamsRemainsStandalone(t *testing.T) {
	if reflect.TypeFor[ExecCommandApprovalParams]() == reflect.TypeFor[CommandExecutionApprovalRequestParams]() {
		t.Fatal("legacy exec-command params alias live command-execution approval params")
	}
	defs := JSONSchema()["$defs"].(Schema)
	if _, ok := defs["ExecCommandApprovalResponse"]; !ok {
		t.Fatal("dependency-complete ExecCommandApprovalResponse missing")
	}
	if _, ok := defs["ReviewDecision"]; !ok {
		t.Fatal("dependency-complete ReviewDecision missing")
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ExecCommandApprovalParams") ||
			slices.Contains(binding.Result, "ExecCommandApprovalParams") {
			t.Fatalf("ExecCommandApprovalParams unexpectedly bound to %s", binding.Method)
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

func TestExecCommandApprovalParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type ExecCommandApprovalParams = {\n" +
		"  \"approvalId\": string | null;\n" +
		"  \"callId\": string;\n" +
		"  \"command\": Array<string>;\n" +
		"  \"conversationId\": ThreadId;\n" +
		"  \"cwd\": string;\n" +
		"  \"parsedCmd\": Array<ParsedCommand>;\n" +
		"  \"reason\": string | null;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}

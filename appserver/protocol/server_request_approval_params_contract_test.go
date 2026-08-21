package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestServerRequestApprovalParamSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range serverRequestApprovalParamSchemas() {
		got, ok := defs[name].(Schema)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s schema = %#v, %v; want %#v", name, got, ok, want)
		}
	}
}

func TestCommandExecutionRequestApprovalParamsPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{
			input: `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"command":"pwd","future":true}`,
			want:  `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"environmentId":null,"command":"pwd"}`,
		},
		{
			input: `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":2,"approvalId":"approval","environmentId":"environment","reason":"reason","networkApprovalContext":{"host":"example.com","protocol":"https"},"command":"curl","cwd":"relative/path","commandActions":[{"type":"unknown","command":"curl"}],"proposedExecpolicyAmendment":["curl *"],"proposedNetworkPolicyAmendments":[{"host":"example.com","action":"allow"}]}`,
			want:  `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":2,"approvalId":"approval","environmentId":"environment","reason":"reason","networkApprovalContext":{"host":"example.com","protocol":"https"},"command":"curl","cwd":"relative/path","commandActions":[{"type":"unknown","command":"curl"}],"proposedExecpolicyAmendment":["curl *"],"proposedNetworkPolicyAmendments":[{"host":"example.com","action":"allow"}]}`,
		},
		{
			input: `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":3,"approvalId":null,"environmentId":null,"reason":null,"networkApprovalContext":null,"command":null,"cwd":null,"commandActions":null,"proposedExecpolicyAmendment":null,"proposedNetworkPolicyAmendments":null,"future":{"ignored":true}}`,
			want:  `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":3,"environmentId":null}`,
		},
	} {
		var params CommandExecutionRequestApprovalParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}

	encoded, err := json.Marshal(CommandExecutionRequestApprovalParams{})
	if err != nil || string(encoded) != `{"threadId":"","turnId":"","itemId":"","startedAtMs":0,"environmentId":null}` {
		t.Fatalf("zero params = %s, %v", encoded, err)
	}
}

func TestFileChangeRequestApprovalParamsPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{
			input: `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"future":true}`,
			want:  `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"reason":null,"grantRoot":null}`,
		},
		{
			input: `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":2,"reason":"reason","grantRoot":"relative/root"}`,
			want:  `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":2,"reason":"reason","grantRoot":"relative/root"}`,
		},
		{
			input: `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":3,"reason":null,"grantRoot":null,"future":{"ignored":true}}`,
			want:  `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":3,"reason":null,"grantRoot":null}`,
		},
	} {
		var params FileChangeRequestApprovalParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestServerRequestApprovalParamsRejectMalformedWireForms(t *testing.T) {
	commandBase := `"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1`
	fileBase := commandBase
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"turnId":"turn","itemId":"item","startedAtMs":1}`,
		`{"threadId":"thread","itemId":"item","startedAtMs":1}`,
		`{"threadId":"thread","turnId":"turn","startedAtMs":1}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item"}`,
		`{"threadId":null,"turnId":"turn","itemId":"item","startedAtMs":1}`,
		`{"threadId":"thread","turnId":1,"itemId":"item","startedAtMs":1}`,
		`{"threadId":"thread","turnId":"turn","itemId":null,"startedAtMs":1}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":null}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":"one"}`,
		`{"threadId":"thread","threadId":"duplicate","turnId":"turn","itemId":"item","startedAtMs":1}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"environmentId":null,"environmentId":"duplicate"}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"command":1}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"networkApprovalContext":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"commandActions":[{}]}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"proposedExecpolicyAmendment":[1]}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"proposedNetworkPolicyAmendments":[{}]}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"proposedNetworkPolicyAmendments":[{"host":"example.com","action":"prompt"}]}`,
		`{` + commandBase + `} {}`,
		`{` + commandBase + `} x`,
	} {
		assertJSONRejects[CommandExecutionRequestApprovalParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"turnId":"turn","itemId":"item","startedAtMs":1}`,
		`{"threadId":"thread","itemId":"item","startedAtMs":1}`,
		`{"threadId":"thread","turnId":"turn","startedAtMs":1}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item"}`,
		`{"threadId":null,"turnId":"turn","itemId":"item","startedAtMs":1}`,
		`{"threadId":"thread","turnId":1,"itemId":"item","startedAtMs":1}`,
		`{"threadId":"thread","turnId":"turn","itemId":null,"startedAtMs":1}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":null}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":"one"}`,
		`{"threadId":"thread","threadId":"duplicate","turnId":"turn","itemId":"item","startedAtMs":1}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"reason":null,"reason":"duplicate"}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"grantRoot":1}`,
		`{` + fileBase + `} {}`,
		`{` + fileBase + `} x`,
	} {
		assertJSONRejects[FileChangeRequestApprovalParams](t, input)
	}

	var command *CommandExecutionRequestApprovalParams
	if err := command.UnmarshalJSON([]byte(`{` + commandBase + `}`)); err == nil {
		t.Fatal("nil CommandExecutionRequestApprovalParams receiver succeeded")
	}
	var fileChange *FileChangeRequestApprovalParams
	if err := fileChange.UnmarshalJSON([]byte(`{` + fileBase + `}`)); err == nil {
		t.Fatal("nil FileChangeRequestApprovalParams receiver succeeded")
	}
}

func TestServerRequestApprovalParamsRemainStandalone(t *testing.T) {
	if reflect.TypeFor[CommandExecutionRequestApprovalParams]() == reflect.TypeFor[CommandExecutionApprovalRequestParams]() {
		t.Fatal("command request approval params alias the live compatibility params")
	}
	if reflect.TypeFor[FileChangeRequestApprovalParams]() == reflect.TypeFor[FileChangeApprovalRequestParams]() {
		t.Fatal("file-change request approval params alias the live compatibility params")
	}
	defs := JSONSchema()["$defs"].(Schema)
	for _, name := range []string{"CommandAction", "LegacyAppPathString", "NetworkApprovalContext", "NetworkPolicyAmendment", "ExecPolicyAmendment"} {
		if _, ok := defs[name]; !ok {
			t.Fatalf("dependency-complete %s missing", name)
		}
	}
	if _, ok := defs["ServerRequest"]; !ok {
		t.Fatal("ServerRequest missing after every referenced parameter became source-exact")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range []string{"CommandExecutionRequestApprovalParams", "FileChangeRequestApprovalParams"} {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	if got := len(defs); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestServerRequestApprovalParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type CommandExecutionRequestApprovalParams = {\n" +
			"  \"approvalId\"?: string | null;\n" +
			"  \"command\"?: string | null;\n" +
			"  \"commandActions\"?: Array<CommandAction> | null;\n" +
			"  \"cwd\"?: LegacyAppPathString | null;\n" +
			"  \"environmentId\": string | null;\n" +
			"  \"itemId\": string;\n" +
			"  \"networkApprovalContext\"?: NetworkApprovalContext | null;\n" +
			"  \"proposedExecpolicyAmendment\"?: Array<string> | null;\n" +
			"  \"proposedNetworkPolicyAmendments\"?: Array<NetworkPolicyAmendment> | null;\n" +
			"  \"reason\"?: string | null;\n" +
			"  \"startedAtMs\": number;\n" +
			"  \"threadId\": string;\n" +
			"  \"turnId\": string;\n" +
			"};",
		"export type FileChangeRequestApprovalParams = {\n" +
			"  \"grantRoot\"?: string | null;\n" +
			"  \"itemId\": string;\n" +
			"  \"reason\"?: string | null;\n" +
			"  \"startedAtMs\": number;\n" +
			"  \"threadId\": string;\n" +
			"  \"turnId\": string;\n" +
			"};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Fatalf("generated TypeScript missing %q", want)
		}
	}
}

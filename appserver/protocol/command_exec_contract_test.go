package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestCommandExecContractSchemasAreExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	want := commandExecContractSchemas()
	for name, expected := range want {
		if got := definitions[name]; !reflect.DeepEqual(got, expected) {
			t.Errorf("%s = %#v, want %#v", name, got, expected)
		}
	}
}

func TestCommandExecParamsPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{
			`{"future":true,"command":["printf","ok"],"processId":null,"cwd":null,"env":{"KEEP":"yes","REMOVE":null},"size":null,"sandboxPolicy":null,"outputBytesCap":null,"timeoutMs":null}`,
			`{"command":["printf","ok"],"processId":null,"outputBytesCap":null,"timeoutMs":null,"cwd":null,"env":{"KEEP":"yes","REMOVE":null},"size":null,"sandboxPolicy":null}`,
		},
		{
			`{"command":["sh","-c","printf ok"],"processId":"command-1","tty":true,"streamStdin":true,"streamStdoutStderr":true,"outputBytesCap":0,"disableOutputCap":true,"disableTimeout":true,"timeoutMs":123,"cwd":"/workspace","env":{},"size":{"rows":24,"cols":80},"sandboxPolicy":{"type":"readOnly","networkAccess":false}}`,
			`{"command":["sh","-c","printf ok"],"processId":"command-1","tty":true,"streamStdin":true,"streamStdoutStderr":true,"outputBytesCap":0,"disableOutputCap":true,"disableTimeout":true,"timeoutMs":123,"cwd":"/workspace","env":{},"size":{"rows":24,"cols":80},"sandboxPolicy":{"type":"readOnly","networkAccess":false}}`,
		},
	} {
		var params CommandExecParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("unmarshal params %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("params round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestCommandExecContractsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"command":null}`, `{"command":"printf"}`, `{"command":[null]}`,
		`{"command":[],"processId":1}`, `{"command":[],"tty":null}`,
		`{"command":[],"env":{"PATH":1}}`, `{"command":[],"size":null,"size":null}`,
		`{"command":[],"sandboxPolicy":{"type":"unknown"}}`, `{"command":[]} {}`,
	} {
		assertJSONRejects[CommandExecParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"exitCode":0,"stdout":""}`, `{"exitCode":0,"stdout":"","stderr":null}`,
		`{"exitCode":2147483648,"stdout":"","stderr":""}`,
		`{"exitCode":0,"stdout":"","stderr":"","exitCode":0}`,
		`{"exitCode":0,"stdout":"","stderr":""} {}`,
	} {
		assertJSONRejects[CommandExecResponse](t, input)
	}
}

func TestCommandExecContractsFailClosedAndRemainStandalone(t *testing.T) {
	if _, err := json.Marshal(CommandExecParams{}); err == nil {
		t.Fatal("nil command serialized")
	}
	var params *CommandExecParams
	if err := params.UnmarshalJSON([]byte(`{"command":[]}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	var response *CommandExecResponse
	if err := response.UnmarshalJSON([]byte(`{"exitCode":0,"stdout":"","stderr":""}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "CommandExecParams") || slices.Contains(binding.Result, "CommandExecResponse") {
			t.Fatalf("public command-exec contract unexpectedly bound to %s", binding.Method)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestCommandExecContractTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type CommandExecParams = {\n" +
			"  \"command\": Array<string>;\n" +
			"  \"cwd\"?: string | null;\n" +
			"  \"disableOutputCap\"?: boolean;\n" +
			"  \"disableTimeout\"?: boolean;\n" +
			"  \"env\"?: { [key in string]?: string | null } | null;\n" +
			"  \"outputBytesCap\"?: number | null;\n" +
			"  \"processId\"?: string | null;\n" +
			"  \"sandboxPolicy\"?: SandboxPolicy | null;\n" +
			"  \"size\"?: CommandExecTerminalSize | null;\n" +
			"  \"streamStdin\"?: boolean;\n" +
			"  \"streamStdoutStderr\"?: boolean;\n" +
			"  \"timeoutMs\"?: number | null;\n" +
			"  \"tty\"?: boolean;\n" +
			"};",
		"export type CommandExecResponse = {\n" +
			"  \"exitCode\": number;\n" +
			"  \"stderr\": string;\n" +
			"  \"stdout\": string;\n" +
			"};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadShellCommandSchemasAreExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	wants := map[string]Schema{
		"ThreadShellCommandParams": {
			"$schema": "http://json-schema.org/draft-07/schema#",
			"properties": Schema{
				"command": Schema{
					"description": "Shell command string evaluated by the thread's configured shell. Unlike `command/exec`, this intentionally preserves shell syntax such as pipes, redirects, and quoting. This runs unsandboxed with full access rather than inheriting the thread sandbox policy.",
					"type":        "string",
				},
				"threadId": Schema{"type": "string"},
			},
			"required": []string{"command", "threadId"},
			"title":    "ThreadShellCommandParams",
			"type":     "object",
		},
		"ThreadShellCommandResponse": {
			"$schema": "http://json-schema.org/draft-07/schema#",
			"title":   "ThreadShellCommandResponse",
			"type":    "object",
		},
	}
	for name, want := range wants {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestThreadShellCommandContractsPreserveSerdeWireForms(t *testing.T) {
	const paramsInput = `{"future":true,"threadId":"thread","command":"printf 'hello' | sed 's/hello/world/'"}`
	const canonicalParams = `{"threadId":"thread","command":"printf 'hello' | sed 's/hello/world/'"}`
	var params ThreadShellCommandParams
	if err := json.Unmarshal([]byte(paramsInput), &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	encoded, err := json.Marshal(params)
	if err != nil || string(encoded) != canonicalParams {
		t.Fatalf("params round trip = %s, %v; want %s", encoded, err, canonicalParams)
	}

	var response ThreadShellCommandResponse
	if err := json.Unmarshal([]byte(`{"future":true}`), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	encoded, err = json.Marshal(response)
	if err != nil || string(encoded) != `{}` {
		t.Fatalf("response round trip = %s, %v; want {}", encoded, err)
	}
}

func TestThreadShellCommandContractsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"threadId":"thread"}`, `{"command":"pwd"}`,
		`{"threadId":null,"command":"pwd"}`, `{"threadId":"thread","command":null}`,
		`{"threadId":"thread","command":[]}`,
		`{"threadId":"a","threadId":"b","command":"pwd"}`,
		`{"threadId":"thread","command":"pwd"} {}`,
	} {
		assertJSONRejects[ThreadShellCommandParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}` + ` {}`,
	} {
		assertJSONRejects[ThreadShellCommandResponse](t, input)
	}
}

func TestThreadShellCommandContractsFailClosedAndRemainStandalone(t *testing.T) {
	var params *ThreadShellCommandParams
	if err := params.UnmarshalJSON([]byte(`{"threadId":"thread","command":"pwd"}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	var response *ThreadShellCommandResponse
	if err := response.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}

	names := []string{"ThreadShellCommandParams", "ThreadShellCommandResponse"}
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

func TestThreadShellCommandTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type ThreadShellCommandParams = {\n" +
			"  \"command\": string;\n" +
			"  \"threadId\": string;\n" +
			"};",
		`export type ThreadShellCommandResponse = Record<string, never>;`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

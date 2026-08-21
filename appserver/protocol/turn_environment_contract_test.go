package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTurnEnvironmentParamsSchemaIsExact(t *testing.T) {
	definition := JSONSchema()["$defs"].(Schema)["TurnEnvironmentParams"]
	want := Schema{
		"properties": Schema{
			"cwd":           Schema{"$ref": "#/$defs/LegacyAppPathString"},
			"environmentId": Schema{"type": "string"},
			"runtimeWorkspaceRoots": Schema{
				"description": "Environment-native runtime workspace roots. Omitted defaults to `cwd`.",
				"items":       Schema{"$ref": "#/$defs/LegacyAppPathString"},
				"type":        []string{"array", "null"},
			},
		},
		"required": []string{"cwd", "environmentId"},
		"type":     "object",
	}
	if !reflect.DeepEqual(definition, want) {
		t.Fatalf("TurnEnvironmentParams = %#v, want %#v", definition, want)
	}
}

func TestTurnEnvironmentParamsPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{
			`{"future":true,"environmentId":"local","cwd":"/workspace","runtimeWorkspaceRoots":null}`,
			`{"environmentId":"local","cwd":"/workspace","runtimeWorkspaceRoots":null}`,
		},
		{
			`{"environmentId":"local","cwd":"/workspace","runtimeWorkspaceRoots":["/workspace","/scratch"]}`,
			`{"environmentId":"local","cwd":"/workspace","runtimeWorkspaceRoots":["/workspace","/scratch"]}`,
		},
		{
			`{"environmentId":"local","cwd":"/workspace"}`,
			`{"environmentId":"local","cwd":"/workspace","runtimeWorkspaceRoots":null}`,
		},
	} {
		var params TurnEnvironmentParams
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

func TestTurnEnvironmentParamsRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"environmentId":"local"}`, `{"cwd":"/workspace"}`,
		`{"environmentId":null,"cwd":"/workspace"}`, `{"environmentId":"local","cwd":null}`,
		`{"environmentId":"local","cwd":"/workspace","runtimeWorkspaceRoots":{}}`,
		`{"environmentId":"local","cwd":"/workspace","runtimeWorkspaceRoots":[null]}`,
		`{"environmentId":"a","environmentId":"b","cwd":"/workspace"}`,
		`{"environmentId":"local","cwd":"/workspace"} {}`,
	} {
		assertJSONRejects[TurnEnvironmentParams](t, input)
	}
}

func TestTurnEnvironmentParamsFailsClosedAndRemainsStandalone(t *testing.T) {
	var params *TurnEnvironmentParams
	if err := params.UnmarshalJSON([]byte(`{"environmentId":"local","cwd":"/workspace"}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "TurnEnvironmentParams") || slices.Contains(binding.Result, "TurnEnvironmentParams") {
			t.Fatalf("TurnEnvironmentParams unexpectedly bound to %s", binding.Method)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestTurnEnvironmentParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type TurnEnvironmentParams = {\n" +
		"  \"cwd\": LegacyAppPathString;\n" +
		"  \"environmentId\": string;\n" +
		"  \"runtimeWorkspaceRoots\"?: Array<LegacyAppPathString> | null;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

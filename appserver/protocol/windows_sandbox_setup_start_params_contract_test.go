package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestWindowsSandboxSetupStartParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["WindowsSandboxSetupStartParams"]
	want := Schema{
		"properties": Schema{
			"cwd": Schema{"anyOf": []any{
				Schema{"$ref": "#/$defs/AbsolutePathBuf"},
				Schema{"type": "null"},
			}},
			"mode": Schema{"$ref": "#/$defs/WindowsSandboxSetupMode"},
		},
		"required": []string{"mode"},
		"type":     "object",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WindowsSandboxSetupStartParams = %#v, want %#v", got, want)
	}
}

func TestWindowsSandboxSetupStartParamsPreserveSerdeWireForms(t *testing.T) {
	assertWindowsSandboxSetupStartRoundTrip(
		t,
		`{"mode":"elevated","future":true}`,
		`{"mode":"elevated","cwd":null}`,
	)
	assertWindowsSandboxSetupStartRoundTrip(
		t,
		`{"mode":"unelevated","cwd":"/workspace/project","future":true}`,
		`{"mode":"unelevated","cwd":"/workspace/project"}`,
	)
}

func TestWindowsSandboxSetupStartParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"mode":null}`, `{"mode":"admin"}`, `{"mode":1}`,
		`{"mode":"elevated","cwd":1}`, `{"mode":"elevated","cwd":"relative"}`,
		`{"mode":"elevated","mode":"unelevated"}`, `{"mode":"elevated"} {}`,
	} {
		assertJSONRejects[WindowsSandboxSetupStartParams](t, input)
	}
}

func TestWindowsSandboxSetupStartParamsRemainStandalone(t *testing.T) {
	var nilParams *WindowsSandboxSetupStartParams
	if err := nilParams.UnmarshalJSON([]byte(`{"mode":"elevated"}`)); err == nil {
		t.Fatal("nil WindowsSandboxSetupStartParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "WindowsSandboxSetupStartParams" {
				t.Fatalf("WindowsSandboxSetupStartParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestWindowsSandboxSetupStartParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type WindowsSandboxSetupStartParams = { mode: WindowsSandboxSetupMode, cwd?: AbsolutePathBuf | null, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertWindowsSandboxSetupStartRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value WindowsSandboxSetupStartParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

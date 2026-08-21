package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPluginUninstallParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["PluginUninstallParams"]
	want := Schema{
		"properties": Schema{
			"pluginId": Schema{"type": "string"},
		},
		"required": []string{"pluginId"},
		"type":     "object",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PluginUninstallParams = %#v, want %#v", got, want)
	}
}

func TestPluginUninstallParamsPreserveSerdeWireForms(t *testing.T) {
	assertPluginUninstallRoundTrip(
		t,
		`{"pluginId":" plugin-1 ","future":true}`,
		`{"pluginId":" plugin-1 "}`,
	)
}

func TestPluginUninstallParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"pluginId":null}`, `{"pluginId":1}`, `{"pluginId":true}`,
		`{"pluginId":"one","pluginId":"two"}`, `{"pluginId":"one"} {}`,
	} {
		assertJSONRejects[PluginUninstallParams](t, input)
	}
}

func TestPluginUninstallParamsRemainStandalone(t *testing.T) {
	var nilParams *PluginUninstallParams
	if err := nilParams.UnmarshalJSON([]byte(`{"pluginId":"plugin-1"}`)); err == nil {
		t.Fatal("nil PluginUninstallParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "PluginUninstallParams" {
				t.Fatalf("PluginUninstallParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestPluginUninstallParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type PluginUninstallParams = { pluginId: string, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertPluginUninstallRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value PluginUninstallParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPluginShareListParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["PluginShareListParams"]
	want := Schema{"type": "object"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PluginShareListParams = %#v, want %#v", got, want)
	}
}

func TestPluginShareListParamsPreserveSerdeWireForms(t *testing.T) {
	assertPluginShareListRoundTrip(t, `{}`, `{}`)
	assertPluginShareListRoundTrip(t, `{"future":true,"future":false}`, `{}`)
}

func TestPluginShareListParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{} {}`,
	} {
		assertJSONRejects[PluginShareListParams](t, input)
	}
}

func TestPluginShareListParamsRemainStandalone(t *testing.T) {
	var nilParams *PluginShareListParams
	if err := nilParams.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil PluginShareListParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "PluginShareListParams" {
				t.Fatalf("PluginShareListParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestPluginShareListParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type PluginShareListParams = Record<string, never>;`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertPluginShareListRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value PluginShareListParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPluginShareCheckoutParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["PluginShareCheckoutParams"]
	want := Schema{
		"properties": Schema{
			"remotePluginId": Schema{"type": "string"},
		},
		"required": []string{"remotePluginId"},
		"type":     "object",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PluginShareCheckoutParams = %#v, want %#v", got, want)
	}
}

func TestPluginShareCheckoutParamsPreserveSerdeWireForms(t *testing.T) {
	assertPluginShareCheckoutRoundTrip(
		t,
		`{"remotePluginId":" plugin-1 ","future":true}`,
		`{"remotePluginId":" plugin-1 "}`,
	)
}

func TestPluginShareCheckoutParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"remotePluginId":null}`, `{"remotePluginId":1}`, `{"remotePluginId":true}`,
		`{"remotePluginId":"one","remotePluginId":"two"}`, `{"remotePluginId":"one"} {}`,
	} {
		assertJSONRejects[PluginShareCheckoutParams](t, input)
	}
}

func TestPluginShareCheckoutParamsRemainStandalone(t *testing.T) {
	var nilParams *PluginShareCheckoutParams
	if err := nilParams.UnmarshalJSON([]byte(`{"remotePluginId":"plugin-1"}`)); err == nil {
		t.Fatal("nil PluginShareCheckoutParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "PluginShareCheckoutParams" {
				t.Fatalf("PluginShareCheckoutParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestPluginShareCheckoutParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type PluginShareCheckoutParams = { remotePluginId: string, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertPluginShareCheckoutRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value PluginShareCheckoutParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

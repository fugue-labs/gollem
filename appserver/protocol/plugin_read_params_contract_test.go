package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPluginReadParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["PluginReadParams"]
	want := Schema{"properties": Schema{
		"marketplacePath": Schema{"anyOf": []any{Schema{"$ref": "#/$defs/AbsolutePathBuf"}, Schema{"type": "null"}}},
		"pluginName":      Schema{"type": "string"}, "remoteMarketplaceName": Schema{"type": []any{"string", "null"}},
	}, "required": []string{"pluginName"}, "type": "object"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PluginReadParams = %#v, want %#v", got, want)
	}
}
func TestPluginReadParamsPreserveSerdeWireForms(t *testing.T) {
	assertPluginReadRoundTrip(t, `{"pluginName":" plugin ","future":true}`, `{"marketplacePath":null,"remoteMarketplaceName":null,"pluginName":" plugin "}`)
	assertPluginReadRoundTrip(t, `{"marketplacePath":null,"remoteMarketplaceName":null,"pluginName":"plugin"}`, `{"marketplacePath":null,"remoteMarketplaceName":null,"pluginName":"plugin"}`)
	assertPluginReadRoundTrip(t, `{"marketplacePath":"/repo","remoteMarketplaceName":" marketplace ","pluginName":"plugin"}`, `{"marketplacePath":"/repo","remoteMarketplaceName":" marketplace ","pluginName":"plugin"}`)
}
func TestPluginReadParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{``, `null`, `[]`, `"value"`, `1`, `true`, `{}`, `{"pluginName":null}`, `{"pluginName":1}`, `{"marketplacePath":1,"pluginName":"plugin"}`, `{"remoteMarketplaceName":true,"pluginName":"plugin"}`, `{"pluginName":"one","pluginName":"two"}`, `{"pluginName":"plugin"} {}`} {
		assertJSONRejects[PluginReadParams](t, input)
	}
}
func TestPluginReadParamsRemainStandalone(t *testing.T) {
	var nilParams *PluginReadParams
	if err := nilParams.UnmarshalJSON([]byte(`{"pluginName":"plugin"}`)); err == nil {
		t.Fatal("nil PluginReadParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "PluginReadParams" {
				t.Fatalf("PluginReadParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}
func TestPluginReadParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type PluginReadParams = { marketplacePath?: AbsolutePathBuf | null, remoteMarketplaceName?: string | null, pluginName: string, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}
func assertPluginReadRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value PluginReadParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

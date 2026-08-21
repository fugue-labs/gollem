package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPluginInstalledParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["PluginInstalledParams"]
	want := pluginInstalledParamSchema()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PluginInstalledParams = %#v, want %#v", got, want)
	}
}
func TestPluginInstalledParamsPreserveSerdeWireForms(t *testing.T) {
	assertPluginInstalledRoundTrip(t, `{"future":true}`, `{"cwds":null,"installSuggestionPluginNames":null}`)
	assertPluginInstalledRoundTrip(t, `{"cwds":["/repo"],"installSuggestionPluginNames":["plugin"],"future":true}`, `{"cwds":["/repo"],"installSuggestionPluginNames":["plugin"]}`)
}
func TestPluginInstalledParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{"cwds":{}}`, `{"cwds":[null]}`, `{"cwds":["relative"]}`, `{"installSuggestionPluginNames":{}}`, `{"installSuggestionPluginNames":[null]}`, `{"installSuggestionPluginNames":[1]}`, `{"cwds":[],"cwds":[]}`, `{} {}`} {
		assertJSONRejects[PluginInstalledParams](t, input)
	}
}
func TestPluginInstalledParamsRemainStandalone(t *testing.T) {
	var nilParams *PluginInstalledParams
	if err := nilParams.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil PluginInstalledParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "PluginInstalledParams" {
				t.Fatalf("PluginInstalledParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}
func TestPluginInstalledParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type PluginInstalledParams = {
/**
 * Optional working directories used to discover repo marketplaces.
 */
cwds?: Array<AbsolutePathBuf> | null,
/**
 * Additional uninstalled plugin names that should be returned when present locally.
 * This is used by mention surfaces that intentionally expose install entrypoints.
 */
installSuggestionPluginNames?: Array<string> | null, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}
func assertPluginInstalledRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value PluginInstalledParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

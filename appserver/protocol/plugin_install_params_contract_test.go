package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPluginInstallParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["PluginInstallParams"]
	want := pluginInstallParamSchema()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PluginInstallParams = %#v, want %#v", got, want)
	}
}
func TestPluginInstallParamsPreserveSerdeWireForms(t *testing.T) {
	assertPluginInstallRoundTrip(t, `{"pluginName":" plugin ","future":true}`, `{"marketplacePath":null,"remoteMarketplaceName":null,"installAttemptId":null,"pluginName":" plugin "}`)
	assertPluginInstallRoundTrip(t, `{"marketplacePath":"/repo","remoteMarketplaceName":" market ","installAttemptId":" attempt ","pluginName":"plugin"}`, `{"marketplacePath":"/repo","remoteMarketplaceName":" market ","installAttemptId":" attempt ","pluginName":"plugin"}`)
}
func TestPluginInstallParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{``, `null`, `[]`, `"value"`, `1`, `true`, `{}`, `{"pluginName":null}`, `{"marketplacePath":1,"pluginName":"plugin"}`, `{"remoteMarketplaceName":true,"pluginName":"plugin"}`, `{"installAttemptId":false,"pluginName":"plugin"}`, `{"pluginName":"one","pluginName":"two"}`, `{"pluginName":"plugin"} {}`} {
		assertJSONRejects[PluginInstallParams](t, input)
	}
}
func TestPluginInstallParamsRemainStandalone(t *testing.T) {
	var nilParams *PluginInstallParams
	if err := nilParams.UnmarshalJSON([]byte(`{"pluginName":"plugin"}`)); err == nil {
		t.Fatal("nil PluginInstallParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "PluginInstallParams" {
				t.Fatalf("PluginInstallParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}
func TestPluginInstallParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type PluginInstallParams = { marketplacePath?: AbsolutePathBuf | null, remoteMarketplaceName?: string | null,
/**
 * Client-generated identifier used to correlate one installation attempt.
 */
installAttemptId?: string | null, pluginName: string, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}
func assertPluginInstallRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value PluginInstallParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

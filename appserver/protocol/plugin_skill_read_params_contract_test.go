package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPluginSkillReadParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["PluginSkillReadParams"]
	want := Schema{
		"properties": Schema{
			"remoteMarketplaceName": Schema{"type": "string"},
			"remotePluginId":        Schema{"type": "string"},
			"skillName":             Schema{"type": "string"},
		},
		"required": []string{"remoteMarketplaceName", "remotePluginId", "skillName"},
		"type":     "object",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PluginSkillReadParams = %#v, want %#v", got, want)
	}
}

func TestPluginSkillReadParamsPreserveSerdeWireForms(t *testing.T) {
	assertPluginSkillReadRoundTrip(
		t,
		`{"remoteMarketplaceName":" marketplace ","remotePluginId":" plugin ","skillName":" skill ","future":true}`,
		`{"remoteMarketplaceName":" marketplace ","remotePluginId":" plugin ","skillName":" skill "}`,
	)
}

func TestPluginSkillReadParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"remotePluginId":"plugin","skillName":"skill"}`,
		`{"remoteMarketplaceName":null,"remotePluginId":"plugin","skillName":"skill"}`,
		`{"remoteMarketplaceName":"marketplace","remotePluginId":1,"skillName":"skill"}`,
		`{"remoteMarketplaceName":"marketplace","remotePluginId":"plugin","skillName":true}`,
		`{"remoteMarketplaceName":"one","remoteMarketplaceName":"two","remotePluginId":"plugin","skillName":"skill"}`,
		`{"remoteMarketplaceName":"marketplace","remotePluginId":"one","remotePluginId":"two","skillName":"skill"}`,
		`{"remoteMarketplaceName":"marketplace","remotePluginId":"plugin","skillName":"one","skillName":"two"}`,
		`{"remoteMarketplaceName":"marketplace","remotePluginId":"plugin","skillName":"skill"} {}`,
	} {
		assertJSONRejects[PluginSkillReadParams](t, input)
	}
}

func TestPluginSkillReadParamsRemainStandalone(t *testing.T) {
	var nilParams *PluginSkillReadParams
	if err := nilParams.UnmarshalJSON([]byte(`{"remoteMarketplaceName":"marketplace","remotePluginId":"plugin","skillName":"skill"}`)); err == nil {
		t.Fatal("nil PluginSkillReadParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "PluginSkillReadParams" {
				t.Fatalf("PluginSkillReadParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestPluginSkillReadParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type PluginSkillReadParams = { remoteMarketplaceName: string, remotePluginId: string, skillName: string, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertPluginSkillReadRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value PluginSkillReadParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSkillsListParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["SkillsListParams"]
	want := Schema{"properties": Schema{
		"cwds":        Schema{"description": "When empty, defaults to the current session working directory.", "items": Schema{"type": "string"}, "type": "array"},
		"forceReload": Schema{"description": "When true, bypass the skills cache and re-scan skills from disk.", "type": "boolean"},
	}, "type": "object"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SkillsListParams = %#v, want %#v", got, want)
	}
}

func TestSkillsListParamsPreserveSerdeWireForms(t *testing.T) {
	assertSkillsListRoundTrip(t, `{"future":true}`, `{}`)
	assertSkillsListRoundTrip(t, `{"cwds":[],"forceReload":false,"future":true}`, `{}`)
	assertSkillsListRoundTrip(t, `{"cwds":["relative","/workspace"],"forceReload":true,"future":true}`, `{"cwds":["relative","/workspace"],"forceReload":true}`)
}

func TestSkillsListParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{"cwds":null}`,
		`{"cwds":"workspace"}`, `{"cwds":[1]}`, `{"forceReload":null}`, `{"forceReload":"true"}`,
		`{"cwds":[],"cwds":[]}`, `{} {}`,
	} {
		assertJSONRejects[SkillsListParams](t, input)
	}
}

func TestSkillsListParamsRemainStandalone(t *testing.T) {
	var nilParams *SkillsListParams
	if err := nilParams.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil SkillsListParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "SkillsListParams" {
				t.Fatalf("SkillsListParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestSkillsListParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type SkillsListParams = {\n/**\n * When empty, defaults to the current session working directory.\n */\ncwds?: Array<string>,\n/**\n * When true, bypass the skills cache and re-scan skills from disk.\n */\nforceReload?: boolean, };"
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertSkillsListRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value SkillsListParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

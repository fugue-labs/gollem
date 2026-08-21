package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSkillsExtraRootsSetParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["SkillsExtraRootsSetParams"]
	want := Schema{
		"properties": Schema{
			"extraRoots": Schema{
				"items": Schema{"$ref": "#/$defs/AbsolutePathBuf"},
				"type":  "array",
			},
		},
		"required": []string{"extraRoots"},
		"type":     "object",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SkillsExtraRootsSetParams = %#v, want %#v", got, want)
	}
}

func TestSkillsExtraRootsSetParamsPreserveSerdeWireForms(t *testing.T) {
	assertSkillsExtraRootsSetRoundTrip(t, `{"extraRoots":[],"future":true}`, `{"extraRoots":[]}`)
	assertSkillsExtraRootsSetRoundTrip(
		t,
		`{"extraRoots":["/workspace/../workspace","/tmp/skills"],"future":true}`,
		`{"extraRoots":["/workspace","/tmp/skills"]}`,
	)
	encoded, err := json.Marshal(SkillsExtraRootsSetParams{})
	if err != nil || string(encoded) != `{"extraRoots":[]}` {
		t.Fatalf("nil extra roots = %s, %v; want empty array", encoded, err)
	}
}

func TestSkillsExtraRootsSetParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"extraRoots":null}`, `{"extraRoots":{}}`, `{"extraRoots":[null]}`,
		`{"extraRoots":[1]}`, `{"extraRoots":["relative/path"]}`,
		`{"extraRoots":[],"extraRoots":[]}`, `{"extraRoots":[]} {}`,
	} {
		assertJSONRejects[SkillsExtraRootsSetParams](t, input)
	}
}

func TestSkillsExtraRootsSetParamsRemainStandalone(t *testing.T) {
	var nilParams *SkillsExtraRootsSetParams
	if err := nilParams.UnmarshalJSON([]byte(`{"extraRoots":[]}`)); err == nil {
		t.Fatal("nil SkillsExtraRootsSetParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "SkillsExtraRootsSetParams" {
				t.Fatalf("SkillsExtraRootsSetParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestSkillsExtraRootsSetParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type SkillsExtraRootsSetParams = { extraRoots: Array<AbsolutePathBuf>, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertSkillsExtraRootsSetRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value SkillsExtraRootsSetParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

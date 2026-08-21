package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSkillsConfigWriteParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["SkillsConfigWriteParams"]
	want := Schema{
		"properties": Schema{
			"enabled": Schema{"type": "boolean"},
			"name": Schema{
				"description": "Name-based selector.",
				"type":        []any{"string", "null"},
			},
			"path": Schema{
				"anyOf": []any{
					Schema{"$ref": "#/$defs/AbsolutePathBuf"},
					Schema{"type": "null"},
				},
				"description": "Path-based selector.",
			},
		},
		"required": []string{"enabled"},
		"type":     "object",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SkillsConfigWriteParams = %#v, want %#v", got, want)
	}
}

func TestSkillsConfigWriteParamsPreserveSerdeWireForms(t *testing.T) {
	assertSkillsConfigWriteRoundTrip(t, `{"enabled":false,"future":true}`, `{"path":null,"name":null,"enabled":false}`)
	assertSkillsConfigWriteRoundTrip(
		t,
		`{"path":"/workspace/../workspace","name":"","enabled":true,"future":true}`,
		`{"path":"/workspace","name":"","enabled":true}`,
	)
}

func TestSkillsConfigWriteParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"enabled":null}`, `{"enabled":"true"}`, `{"path":1,"enabled":true}`,
		`{"path":"relative/path","enabled":true}`, `{"name":1,"enabled":true}`,
		`{"enabled":true,"enabled":false}`, `{"enabled":true} {}`,
	} {
		assertJSONRejects[SkillsConfigWriteParams](t, input)
	}
}

func TestSkillsConfigWriteParamsRemainStandalone(t *testing.T) {
	var nilParams *SkillsConfigWriteParams
	if err := nilParams.UnmarshalJSON([]byte(`{"enabled":true}`)); err == nil {
		t.Fatal("nil SkillsConfigWriteParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "SkillsConfigWriteParams" {
				t.Fatalf("SkillsConfigWriteParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestSkillsConfigWriteParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type SkillsConfigWriteParams = {\n/**\n * Path-based selector.\n */\npath?: AbsolutePathBuf | null,\n/**\n * Name-based selector.\n */\nname?: string | null, enabled: boolean, };"
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertSkillsConfigWriteRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value SkillsConfigWriteParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

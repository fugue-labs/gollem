package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSkillMigrationSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["SkillMigration"].(Schema)
	if !ok {
		t.Fatal("$defs missing SkillMigration")
	}
	want := closedThreadSessionParamSchema(Schema{
		"name": Schema{"type": "string"},
	}, []string{"name"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SkillMigration = %#v, want %#v", got, want)
	}
}

func TestSkillMigrationAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: `{"name":""}`, want: `{"name":""}`},
		{input: `{"name":"skill"}`, want: `{"name":"skill"}`},
		{
			input: `{"name":"skill","skill_name":"ignored","future":{"nested":true}}`,
			want:  `{"name":"skill"}`,
		},
	}
	for _, tc := range cases {
		var migration SkillMigration
		if err := json.Unmarshal([]byte(tc.input), &migration); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(migration)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}
}

func TestSkillMigrationRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"future":true}`,
		`{"name":null}`,
		`{"name":1}`,
		`{"name":true}`,
		`{"name":{}}`,
		`{"name":[]}`,
		`{"name":"first","name":"second"}`,
		`{"name":"skill"`,
		`{"name":"skill"} {}`,
	}
	for _, input := range invalid {
		var migration SkillMigration
		if err := json.Unmarshal([]byte(input), &migration); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var migration *SkillMigration
	if err := migration.UnmarshalJSON([]byte(`{"name":"skill"}`)); err == nil {
		t.Fatal("nil SkillMigration receiver succeeded")
	}
}

func TestDecodeSkillMigrationObjectRejectsMalformedJSON(t *testing.T) {
	invalid := []string{
		``,
		`{"unterminated`,
		`{"name":`,
		`{"name":"skill"`,
		`{} {}`,
		`{} trailing`,
	}
	for _, input := range invalid {
		if _, err := decodeSkillMigrationObject([]byte(input)); err == nil {
			t.Errorf("decodeSkillMigrationObject(%q) succeeded", input)
		}
	}
}

func TestSkillMigrationRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "SkillMigration") ||
			slices.Contains(binding.Result, "SkillMigration") {
			t.Fatalf("SkillMigration unexpectedly bound: %#v", binding)
		}
	}
	for _, method := range []string{"externalAgentConfig/detect", "externalAgentConfig/import"} {
		info, ok := LookupMethod(method)
		if !ok || info.State != MethodDeferredStub {
			t.Errorf("%s = %#v, %v; want deferred stub", method, info, ok)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestSkillMigrationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	want := "export type SkillMigration = {\n  \"name\": string;\n};"
	if !strings.Contains(source, want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var _ json.Unmarshaler = (*SkillMigration)(nil)

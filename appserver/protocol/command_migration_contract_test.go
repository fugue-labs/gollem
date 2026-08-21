package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestCommandMigrationSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["CommandMigration"].(Schema)
	if !ok {
		t.Fatal("$defs missing CommandMigration")
	}
	want := closedThreadSessionParamSchema(Schema{
		"name": Schema{"type": "string"},
	}, []string{"name"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CommandMigration = %#v, want %#v", got, want)
	}
}

func TestCommandMigrationAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: `{"name":""}`, want: `{"name":""}`},
		{input: `{"name":"command"}`, want: `{"name":"command"}`},
		{
			input: `{"name":"command","command_name":"ignored","future":{"nested":true}}`,
			want:  `{"name":"command"}`,
		},
	}
	for _, tc := range cases {
		var migration CommandMigration
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

func TestCommandMigrationRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"future":true}`,
		`{"name":null}`,
		`{"name":1}`,
		`{"name":true}`,
		`{"name":{}}`,
		`{"name":[]}`,
		`{"name":"first","name":"second"}`,
		`{"name":"command"`,
		`{"name":"command"} {}`,
	}
	for _, input := range invalid {
		var migration CommandMigration
		if err := json.Unmarshal([]byte(input), &migration); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var migration *CommandMigration
	if err := migration.UnmarshalJSON([]byte(`{"name":"command"}`)); err == nil {
		t.Fatal("nil CommandMigration receiver succeeded")
	}
}

func TestDecodeCommandMigrationObjectRejectsMalformedJSON(t *testing.T) {
	invalid := []string{
		``,
		`{"unterminated`,
		`{"name":`,
		`{"name":"command"`,
		`{} {}`,
		`{} trailing`,
	}
	for _, input := range invalid {
		if _, err := decodeCommandMigrationObject([]byte(input)); err == nil {
			t.Errorf("decodeCommandMigrationObject(%q) succeeded", input)
		}
	}
}

func TestCommandMigrationRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "CommandMigration") ||
			slices.Contains(binding.Result, "CommandMigration") {
			t.Fatalf("CommandMigration unexpectedly bound: %#v", binding)
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

func TestCommandMigrationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	want := "export type CommandMigration = {\n  \"name\": string;\n};"
	if !strings.Contains(source, want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var _ json.Unmarshaler = (*CommandMigration)(nil)

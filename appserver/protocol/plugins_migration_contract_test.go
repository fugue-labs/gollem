package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPluginsMigrationSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["PluginsMigration"].(Schema)
	if !ok {
		t.Fatal("$defs missing PluginsMigration")
	}
	want := closedThreadSessionParamSchema(Schema{
		"marketplaceName": Schema{"type": "string"},
		"pluginNames": Schema{
			"type":  "array",
			"items": Schema{"type": "string"},
		},
	}, []string{"marketplaceName", "pluginNames"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PluginsMigration = %#v, want %#v", got, want)
	}
}

func TestPluginsMigrationAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: `{"marketplaceName":"","pluginNames":[]}`,
			want:  `{"marketplaceName":"","pluginNames":[]}`,
		},
		{
			input: `{"marketplaceName":"market","pluginNames":["one","","one"]}`,
			want:  `{"marketplaceName":"market","pluginNames":["one","","one"]}`,
		},
		{
			input: `{"marketplaceName":"market","pluginNames":["one"],"marketplace_name":"ignored","future":{"nested":true}}`,
			want:  `{"marketplaceName":"market","pluginNames":["one"]}`,
		},
	}
	for _, tc := range cases {
		var migration PluginsMigration
		if err := json.Unmarshal([]byte(tc.input), &migration); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(migration)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}

	encoded, err := json.Marshal(PluginsMigration{})
	if err != nil || string(encoded) != `{"marketplaceName":"","pluginNames":[]}` {
		t.Errorf("zero-value canonical = %s, %v", encoded, err)
	}
}

func TestPluginsMigrationRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"marketplaceName":"market"}`,
		`{"pluginNames":[]}`,
		`{"marketplaceName":null,"pluginNames":[]}`,
		`{"marketplaceName":1,"pluginNames":[]}`,
		`{"marketplaceName":true,"pluginNames":[]}`,
		`{"marketplaceName":{},"pluginNames":[]}`,
		`{"marketplaceName":[],"pluginNames":[]}`,
		`{"marketplaceName":"market","pluginNames":null}`,
		`{"marketplaceName":"market","pluginNames":"plugin"}`,
		`{"marketplaceName":"market","pluginNames":{}}`,
		`{"marketplaceName":"market","pluginNames":[null]}`,
		`{"marketplaceName":"market","pluginNames":[1]}`,
		`{"marketplaceName":"market","pluginNames":[true]}`,
		`{"marketplaceName":"market","pluginNames":[{}]}`,
		`{"marketplaceName":"market","pluginNames":[[]]}`,
		`{"marketplaceName":"first","marketplaceName":"second","pluginNames":[]}`,
		`{"marketplaceName":"market","pluginNames":[],"pluginNames":[]}`,
		`{"marketplaceName":"market","pluginNames":[]`,
		`{"marketplaceName":"market","pluginNames":[]} {}`,
	}
	for _, input := range invalid {
		var migration PluginsMigration
		if err := json.Unmarshal([]byte(input), &migration); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var migration *PluginsMigration
	if err := migration.UnmarshalJSON([]byte(`{"marketplaceName":"market","pluginNames":[]}`)); err == nil {
		t.Fatal("nil PluginsMigration receiver succeeded")
	}
}

func TestDecodePluginsMigrationObjectRejectsMalformedJSON(t *testing.T) {
	invalid := []string{
		``,
		`{"unterminated`,
		`{"marketplaceName":`,
		`{"marketplaceName":"market","pluginNames":[]`,
		`{} {}`,
		`{} trailing`,
	}
	for _, input := range invalid {
		if _, err := decodePluginsMigrationObject([]byte(input)); err == nil {
			t.Errorf("decodePluginsMigrationObject(%q) succeeded", input)
		}
	}
}

func TestPluginsMigrationRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "PluginsMigration") ||
			slices.Contains(binding.Result, "PluginsMigration") {
			t.Fatalf("PluginsMigration unexpectedly bound: %#v", binding)
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
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestPluginsMigrationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	want := "export type PluginsMigration = {\n  \"marketplaceName\": string;\n  \"pluginNames\": Array<string>;\n};"
	if !strings.Contains(source, want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = PluginsMigration{}
	_ json.Unmarshaler = (*PluginsMigration)(nil)
)

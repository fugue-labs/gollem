package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMcpServerMigrationSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["McpServerMigration"].(Schema)
	if !ok {
		t.Fatal("$defs missing McpServerMigration")
	}
	want := closedThreadSessionParamSchema(Schema{
		"name": Schema{"type": "string"},
	}, []string{"name"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("McpServerMigration = %#v, want %#v", got, want)
	}
}

func TestMcpServerMigrationAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: `{"name":""}`, want: `{"name":""}`},
		{input: `{"name":"server"}`, want: `{"name":"server"}`},
		{
			input: `{"name":"server","server_name":"ignored","future":{"nested":true}}`,
			want:  `{"name":"server"}`,
		},
	}
	for _, tc := range cases {
		var migration McpServerMigration
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

func TestMcpServerMigrationRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"future":true}`,
		`{"name":null}`,
		`{"name":1}`,
		`{"name":true}`,
		`{"name":{}}`,
		`{"name":[]}`,
		`{"name":"first","name":"second"}`,
		`{"name":"server"`,
		`{"name":"server"} {}`,
	}
	for _, input := range invalid {
		var migration McpServerMigration
		if err := json.Unmarshal([]byte(input), &migration); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var migration *McpServerMigration
	if err := migration.UnmarshalJSON([]byte(`{"name":"server"}`)); err == nil {
		t.Fatal("nil McpServerMigration receiver succeeded")
	}
}

func TestDecodeMcpServerMigrationObjectRejectsMalformedJSON(t *testing.T) {
	invalid := []string{
		``,
		`{"unterminated`,
		`{"name":`,
		`{"name":"server"`,
		`{} {}`,
		`{} trailing`,
	}
	for _, input := range invalid {
		if _, err := decodeMcpServerMigrationObject([]byte(input)); err == nil {
			t.Errorf("decodeMcpServerMigrationObject(%q) succeeded", input)
		}
	}
}

func TestMcpServerMigrationRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "McpServerMigration") ||
			slices.Contains(binding.Result, "McpServerMigration") {
			t.Fatalf("McpServerMigration unexpectedly bound: %#v", binding)
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

func TestMcpServerMigrationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	want := "export type McpServerMigration = {\n  \"name\": string;\n};"
	if !strings.Contains(source, want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var _ json.Unmarshaler = (*McpServerMigration)(nil)

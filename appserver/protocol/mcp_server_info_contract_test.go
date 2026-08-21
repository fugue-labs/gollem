package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMcpServerInfoSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["McpServerInfo"].(Schema)
	if !ok {
		t.Fatal("$defs missing McpServerInfo")
	}
	want := closedThreadSessionParamSchema(Schema{
		"name": Schema{"type": "string"},
		"title": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
		"version": Schema{"type": "string"},
		"description": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
		"icons": Schema{
			"anyOf": []any{
				Schema{"type": "array", "items": Schema{"$ref": "#/$defs/JsonValue"}},
				Schema{"type": "null"},
			},
		},
		"websiteUrl": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
	}, []string{"name", "title", "version", "description", "icons", "websiteUrl"})
	want["description"] = "Presentation metadata advertised by an initialized MCP server."
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("McpServerInfo = %#v, want %#v", got, want)
	}
}

func TestMcpServerInfoAcceptsRustWireFormsAndCanonicalizesOptions(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: `{"name":"","version":""}`,
			want:  `{"name":"","title":null,"version":"","description":null,"icons":null,"websiteUrl":null}`,
		},
		{
			input: `{"name":"server","title":null,"version":"1.0.0","description":null,"icons":null,"websiteUrl":null}`,
			want:  `{"name":"server","title":null,"version":"1.0.0","description":null,"icons":null,"websiteUrl":null}`,
		},
		{
			input: `{"name":"server","title":"Title","version":"1.0.0","description":"Description",` +
				`"icons":[null,true,"icon",9007199254740993,{"z":2,"a":[false,null]}],` +
				`"websiteUrl":"https://example.com"}`,
			want: `{"name":"server","title":"Title","version":"1.0.0","description":"Description",` +
				`"icons":[null,true,"icon",9007199254740993,{"a":[false,null],"z":2}],` +
				`"websiteUrl":"https://example.com"}`,
		},
		{
			input: `{"name":"server","version":"1.0.0","icons":[],` +
				`"website_url":"https://ignored.example","future":{"nested":true}}`,
			want: `{"name":"server","title":null,"version":"1.0.0",` +
				`"description":null,"icons":[],"websiteUrl":null}`,
		},
	}
	for _, tc := range cases {
		var info McpServerInfo
		if err := json.Unmarshal([]byte(tc.input), &info); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(info)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}
}

func TestMcpServerInfoRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"version":"1.0.0"}`,
		`{"name":"server"}`,
		`{"name":null,"version":"1.0.0"}`,
		`{"name":1,"version":"1.0.0"}`,
		`{"name":"server","version":null}`,
		`{"name":"server","version":1}`,
		`{"name":"server","version":"1.0.0","title":1}`,
		`{"name":"server","version":"1.0.0","description":{}}`,
		`{"name":"server","version":"1.0.0","icons":{}}`,
		`{"name":"server","version":"1.0.0","icons":"value"}`,
		`{"name":"server","version":"1.0.0","websiteUrl":1}`,
		`{"name":"first","name":"second","version":"1.0.0"}`,
		`{"name":"server","version":"1","version":"2"}`,
		`{"name":"server","title":null,"title":null,"version":"1.0.0"}`,
		`{"name":"server","version":"1.0.0","description":null,"description":null}`,
		`{"name":"server","version":"1.0.0","icons":null,"icons":[]}`,
		`{"name":"server","version":"1.0.0","websiteUrl":null,"websiteUrl":""}`,
		`{"name":"server","version":"1.0.0"`,
		`{"name":"server","version":"1.0.0"} {}`,
	}
	for _, input := range invalid {
		var info McpServerInfo
		if err := json.Unmarshal([]byte(input), &info); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var info *McpServerInfo
	if err := info.UnmarshalJSON([]byte(`{"name":"server","version":"1.0.0"}`)); err == nil {
		t.Fatal("nil McpServerInfo receiver succeeded")
	}
	if _, err := json.Marshal(McpServerInfo{
		Name: "server", Version: "1.0.0", Icons: []JsonValue{{}},
	}); err == nil {
		t.Fatal("McpServerInfo with invalid constructed icon JSON marshaled")
	}
}

func TestDecodeMcpServerInfoObjectRejectsMalformedJSON(t *testing.T) {
	invalid := []string{
		``,
		`{"unterminated`,
		`{"name":`,
		`{"name":"server"`,
		`{} {}`,
		`{} trailing`,
	}
	for _, input := range invalid {
		if _, err := decodeMcpServerInfoObject([]byte(input)); err == nil {
			t.Errorf("decodeMcpServerInfoObject(%q) succeeded", input)
		}
	}
}

func TestMcpServerInfoRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "McpServerInfo") ||
			slices.Contains(binding.Result, "McpServerInfo") {
			t.Fatalf("McpServerInfo unexpectedly bound: %#v", binding)
		}
	}
	info, ok := LookupMethod("mcpServerStatus/list")
	if !ok || info.State != MethodImplemented {
		t.Fatalf("mcpServerStatus/list = %#v, %v; want implemented", info, ok)
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

func TestMcpServerInfoTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type McpServerInfo = {`,
		`"description": string | null;`,
		`"icons": Array<JsonValue> | null;`,
		`"name": string;`,
		`"title": string | null;`,
		`"version": string;`,
		`"websiteUrl": string | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var _ json.Unmarshaler = (*McpServerInfo)(nil)

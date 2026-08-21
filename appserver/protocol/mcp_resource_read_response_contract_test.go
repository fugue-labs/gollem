package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMcpResourceReadResponseSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["McpResourceReadResponse"].(Schema)
	if !ok {
		t.Fatal("$defs missing McpResourceReadResponse")
	}
	want := closedThreadSessionParamSchema(Schema{
		"contents": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/ResourceContent"},
		},
	}, []string{"contents"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("McpResourceReadResponse = %#v, want %#v", got, want)
	}
}

func TestMcpResourceReadResponseAcceptsRustWireFormsAndCanonicalizesContents(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: `{"contents":[]}`, want: `{"contents":[]}`},
		{
			input: `{"contents":[{"uri":"resource://text","text":"hello"}]}`,
			want:  `{"contents":[{"uri":"resource://text","text":"hello"}]}`,
		},
		{
			input: `{"contents":[{"uri":"resource://text","mimeType":"text/plain","text":"hello","_meta":{"z":1,"a":2}},{"uri":"resource://blob","blob":"AA=="}]}`,
			want:  `{"contents":[{"uri":"resource://text","mimeType":"text/plain","text":"hello","_meta":{"a":2,"z":1}},{"uri":"resource://blob","blob":"AA=="}]}`,
		},
		{
			input: `{"contents":[{"uri":"resource://crossed","text":"text wins","blob":"ignored"}],"future":{"nested":true}}`,
			want:  `{"contents":[{"uri":"resource://crossed","text":"text wins"}]}`,
		},
	}
	for _, tc := range cases {
		var response McpResourceReadResponse
		if err := json.Unmarshal([]byte(tc.input), &response); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(response)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}

	encoded, err := json.Marshal(McpResourceReadResponse{})
	if want := `{"contents":[]}`; err != nil || string(encoded) != want {
		t.Fatalf("marshal nil contents = %s, %v; want %s", encoded, err, want)
	}
}

func TestMcpResourceReadResponseRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"contents":null}`,
		`{"contents":{}}`,
		`{"contents":"value"}`,
		`{"contents":1}`,
		`{"contents":true}`,
		`{"contents":[null]}`,
		`{"contents":[1]}`,
		`{"contents":[{}]}`,
		`{"contents":[{"text":"missing uri"}]}`,
		`{"contents":[{"uri":"resource://missing"}]}`,
		`{"contents":[{"uri":"resource://text","text":null}]}`,
		`{"content":[]}`,
		`{"contents":[],"contents":[]}`,
		`{"contents":[]`,
		`{"contents":[]} {}`,
	}
	for _, input := range invalid {
		var response McpResourceReadResponse
		if err := json.Unmarshal([]byte(input), &response); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var response *McpResourceReadResponse
	if err := response.UnmarshalJSON([]byte(`{"contents":[]}`)); err == nil {
		t.Fatal("nil McpResourceReadResponse receiver succeeded")
	}
	for name, response := range map[string]McpResourceReadResponse{
		"empty content": {Contents: []ResourceContent{{}}},
		"invalid content": {
			Contents: []ResourceContent{{raw: json.RawMessage(`{"uri":"resource://text","text":null}`)}},
		},
	} {
		if _, err := json.Marshal(response); err == nil {
			t.Errorf("marshal invalid constructed %s succeeded", name)
		}
	}
}

func TestDecodeMcpResourceReadResponseObjectRejectsMalformedJSON(t *testing.T) {
	invalid := []string{
		``,
		`{"unterminated`,
		`{"contents":`,
		`{"contents":[]`,
		`{} {}`,
		`{} trailing`,
	}
	for _, input := range invalid {
		if _, err := decodeMcpResourceReadResponseObject([]byte(input)); err == nil {
			t.Errorf("decodeMcpResourceReadResponseObject(%q) succeeded", input)
		}
	}
}

func TestMcpResourceReadResponseRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "McpResourceReadResponse") ||
			slices.Contains(binding.Result, "McpResourceReadResponse") {
			t.Fatalf("McpResourceReadResponse unexpectedly bound: %#v", binding)
		}
	}
	info, ok := LookupMethod("mcpServer/resource/read")
	if !ok || info.State != MethodImplemented {
		t.Fatalf("mcpServer/resource/read = %#v, %v; want implemented", info, ok)
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

func TestMcpResourceReadResponseTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type McpResourceReadResponse = {`,
		`"contents": Array<ResourceContent>;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = McpResourceReadResponse{}
	_ json.Unmarshaler = (*McpResourceReadResponse)(nil)
)

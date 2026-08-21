package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestResourceContentSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["ResourceContent"].(Schema)
	if !ok {
		t.Fatal("$defs missing ResourceContent")
	}
	optionalString := Schema{
		"anyOf":                                []any{Schema{"type": "string"}, Schema{"type": "null"}},
		"x-gollem-typescript-optional-nonnull": true,
	}
	want := Schema{"oneOf": []any{
		closedThreadSessionParamSchema(Schema{
			"uri": Schema{"type": "string"}, "mimeType": optionalString,
			"text": Schema{"type": "string"}, "_meta": Schema{"$ref": "#/$defs/JsonValue"},
		}, []string{"text", "uri"}),
		closedThreadSessionParamSchema(Schema{
			"uri": Schema{"type": "string"}, "mimeType": optionalString,
			"blob": Schema{"type": "string"}, "_meta": Schema{"$ref": "#/$defs/JsonValue"},
		}, []string{"blob", "uri"}),
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResourceContent = %#v, want %#v", got, want)
	}
}

func TestResourceContentAcceptsRustWireFormsAndCanonicalizesVariants(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: `{"uri":"","text":""}`, want: `{"uri":"","text":""}`},
		{input: `{"uri":"resource://blob","blob":"AA=="}`, want: `{"uri":"resource://blob","blob":"AA=="}`},
		{
			input: `{"uri":"resource://text","mimeType":"text/plain","text":"hello","_meta":{"z":1,"a":[true,null]}}`,
			want:  `{"uri":"resource://text","mimeType":"text/plain","text":"hello","_meta":{"a":[true,null],"z":1}}`,
		},
		{
			input: `{"uri":"resource://blob","mimeType":"application/octet-stream","blob":"AA==","_meta":["source",9007199254740993123456789]}`,
			want:  `{"uri":"resource://blob","mimeType":"application/octet-stream","blob":"AA==","_meta":["source",9007199254740993123456789]}`,
		},
		{
			input: `{"uri":"resource://nulls","mimeType":null,"text":"value","_meta":null}`,
			want:  `{"uri":"resource://nulls","text":"value"}`,
		},
		{
			input: `{"uri":"resource://future","text":"value","future":{"nested":true},"mime_type":"ignored"}`,
			want:  `{"uri":"resource://future","text":"value"}`,
		},
		{
			input: `{"uri":"resource://crossed","text":"text wins","blob":"ignored"}`,
			want:  `{"uri":"resource://crossed","text":"text wins"}`,
		},
		{
			input: `{"uri":"resource://fallback","text":1,"blob":"blob wins"}`,
			want:  `{"uri":"resource://fallback","blob":"blob wins"}`,
		},
		{
			input: `{"uri":"resource://text","text":"text wins","blob":1}`,
			want:  `{"uri":"resource://text","text":"text wins"}`,
		},
		{
			input: `{"uri":"resource://duplicates","text":"text wins","blob":"one","blob":"two"}`,
			want:  `{"uri":"resource://duplicates","text":"text wins"}`,
		},
		{
			input: `{"uri":"resource://fallback","text":"one","text":"two","blob":"blob wins"}`,
			want:  `{"uri":"resource://fallback","blob":"blob wins"}`,
		},
	}
	for _, tc := range cases {
		var content ResourceContent
		if err := json.Unmarshal([]byte(tc.input), &content); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(content)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}
}

func TestResourceContentRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"text":"value"}`,
		`{"blob":"AA=="}`,
		`{"uri":"resource://only"}`,
		`{"uri":null,"text":"value"}`,
		`{"uri":1,"text":"value"}`,
		`{"uri":"resource://text","text":null}`,
		`{"uri":"resource://text","text":1}`,
		`{"uri":"resource://blob","blob":null}`,
		`{"uri":"resource://blob","blob":1}`,
		`{"uri":"resource://mime","mimeType":1,"text":"value"}`,
		`{"uri":"resource://meta","text":"value","_meta":undefined}`,
		`{"uri":"resource://duplicate","uri":"other","text":"value"}`,
		`{"uri":"resource://duplicate","mimeType":"a","mimeType":"b","text":"value"}`,
		`{"uri":"resource://duplicate","_meta":1,"_meta":2,"text":"value"}`,
		`{"uri":"resource://both","text":1,"blob":2}`,
		`{"uri":"resource://text","text":"value"`,
		`{"uri":"resource://text","text":"value"} {}`,
	}
	for _, input := range invalid {
		var content ResourceContent
		if err := json.Unmarshal([]byte(input), &content); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var content *ResourceContent
	if err := content.UnmarshalJSON([]byte(`{"uri":"resource://text","text":"value"}`)); err == nil {
		t.Fatal("nil ResourceContent receiver succeeded")
	}
	for name, content := range map[string]ResourceContent{
		"empty":        {},
		"missing uri":  {raw: json.RawMessage(`{"text":"value"}`)},
		"invalid meta": {raw: json.RawMessage(`{"uri":"resource://text","text":"value","_meta":undefined}`)},
	} {
		if _, err := json.Marshal(content); err == nil {
			t.Errorf("marshal invalid constructed %s succeeded", name)
		}
	}
}

func TestDecodeResourceContentVariantObjectRejectsMalformedJSON(t *testing.T) {
	invalid := []string{
		``,
		`{"unterminated`,
		`{"uri":"resource://text"`,
		`{} {}`,
		`{} trailing`,
	}
	for _, input := range invalid {
		if _, err := decodeResourceContentVariantObject([]byte(input), "resource content", "uri"); err == nil {
			t.Errorf("decodeResourceContentVariantObject(%q) succeeded", input)
		}
	}
}

func TestResourceContentRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ResourceContent") ||
			slices.Contains(binding.Result, "ResourceContent") {
			t.Fatalf("ResourceContent unexpectedly bound: %#v", binding)
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

func TestResourceContentTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ResourceContent = {`,
		`"_meta"?: JsonValue;`,
		`"blob": string;`,
		`"mimeType"?: string;`,
		`"text": string;`,
		`"uri": string;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = ResourceContent{}
	_ json.Unmarshaler = (*ResourceContent)(nil)
)

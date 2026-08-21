package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMcpServerToolCallResponseSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	response, ok := defs["McpServerToolCallResponse"].(Schema)
	if !ok {
		t.Fatal("$defs missing McpServerToolCallResponse")
	}
	if response["type"] != "object" || response["additionalProperties"] != false {
		t.Fatalf("McpServerToolCallResponse is not a closed object: %#v", response)
	}
	if got, want := schemaRequiredNames(response), []string{"content"}; !slices.Equal(got, want) {
		t.Fatalf("McpServerToolCallResponse required = %v, want %v", got, want)
	}
	wantProperties := Schema{
		"content": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/JsonValue"},
		},
		"structuredContent": Schema{"$ref": "#/$defs/JsonValue"},
		"isError": Schema{
			"anyOf":                                []any{Schema{"type": "boolean"}, Schema{"type": "null"}},
			"x-gollem-typescript-optional-nonnull": true,
		},
		"_meta": Schema{"$ref": "#/$defs/JsonValue"},
	}
	if got := response["properties"].(Schema); !reflect.DeepEqual(got, wantProperties) {
		t.Fatalf("McpServerToolCallResponse properties = %#v, want %#v", got, wantProperties)
	}
}

func TestMcpServerToolCallResponseAcceptsExactWireFormsAndCanonicalizesOptions(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: `{"content":[]}`, want: `{"content":[]}`},
		{
			input: `{"content":[],"structuredContent":null,"isError":null,"_meta":null}`,
			want:  `{"content":[]}`,
		},
		{
			input: `{"content":[null,"text",true,9007199254740993123456789,{"z":1,"a":2}],"structuredContent":{"nested":[1,false]},"isError":false,"_meta":["source",{"ok":true}]}`,
			want:  `{"content":[null,"text",true,9007199254740993123456789,{"a":2,"z":1}],"structuredContent":{"nested":[1,false]},"isError":false,"_meta":["source",{"ok":true}]}`,
		},
		{
			input: `{"content":[-1.25e100],"isError":true}`,
			want:  `{"content":[-1.25e100],"isError":true}`,
		},
	}
	for _, tc := range cases {
		var response McpServerToolCallResponse
		if err := json.Unmarshal([]byte(tc.input), &response); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(response)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}

	structuredContent := JsonValue{raw: json.RawMessage(`null`)}
	meta := JsonValue{raw: json.RawMessage(`{"source":"client"}`)}
	isError := false
	encoded, err := json.Marshal(McpServerToolCallResponse{
		StructuredContent: &structuredContent,
		IsError:           &isError,
		Meta:              &meta,
	})
	want := `{"content":[],"structuredContent":null,"isError":false,"_meta":{"source":"client"}}`
	if err != nil || string(encoded) != want {
		t.Fatalf("marshal populated response = %s, %v; want %s", encoded, err, want)
	}
}

func TestMcpServerToolCallResponseRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"content":null}`,
		`{"content":{}}`,
		`{"content":"value"}`,
		`{"content":1}`,
		`{"content":true}`,
		`{"content":[undefined]}`,
		`{"content":[],"isError":"false"}`,
		`{"content":[],"isError":1}`,
		`{"content":[],"isError":{}}`,
		`{"content":[],"structured_content":null}`,
		`{"content":[],"is_error":false}`,
		`{"content":[],"meta":null}`,
		`{"content":[],"serverId":"repo"}`,
		`{"content":[],"serverName":"repo"}`,
		`{"content":[],"toolName":"echo"}`,
		`{"content":[],"result":{}}`,
		`{"content":[],"text":"pong"}`,
		`{"content":[],"unknown":null}`,
		`{"content":[]`,
		`{"content":[]} {}`,
	}
	for _, input := range invalid {
		var response McpServerToolCallResponse
		if err := json.Unmarshal([]byte(input), &response); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var response *McpServerToolCallResponse
	if err := response.UnmarshalJSON([]byte(`{"content":[]}`)); err == nil {
		t.Fatal("nil McpServerToolCallResponse receiver succeeded")
	}
	for name, response := range map[string]McpServerToolCallResponse{
		"content":            {Content: []JsonValue{{}}},
		"structured content": {StructuredContent: &JsonValue{}},
		"metadata":           {Meta: &JsonValue{}},
	} {
		if _, err := json.Marshal(response); err == nil {
			t.Errorf("marshal invalid constructed %s succeeded", name)
		}
	}
}

func TestMcpServerToolCallResponseRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "McpServerToolCallResponse") ||
			slices.Contains(binding.Result, "McpServerToolCallResponse") {
			t.Fatalf("McpServerToolCallResponse unexpectedly bound: %#v", binding)
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

func TestMcpServerToolCallResponseTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type McpServerToolCallResponse = {`,
		`"_meta"?: JsonValue;`,
		`"content": Array<JsonValue>;`,
		`"isError"?: boolean;`,
		`"structuredContent"?: JsonValue;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func TestTypeScriptOptionalNonNullHintRejectsMalformedSchemas(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema Schema
		want   string
	}{
		{name: "missing anyOf", schema: Schema{}, want: "requires anyOf"},
		{
			name: "only null",
			schema: Schema{"anyOf": []any{
				Schema{"type": "null"},
			}},
			want: "no non-null variant",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := typeScriptOptionalNonNullType(tc.schema, 0); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("typeScriptOptionalNonNullType error = %v, want %q", err, tc.want)
			}
		})
	}

	got, err := typeScriptOptionalNonNullType(Schema{"anyOf": []any{
		Schema{"type": "boolean"},
		Schema{"type": "string"},
		Schema{"type": "null"},
	}}, 0)
	if err != nil || got != "boolean | string" {
		t.Fatalf("multi-variant optional non-null type = %q, %v", got, err)
	}
}

var (
	_ json.Marshaler   = McpServerToolCallResponse{}
	_ json.Unmarshaler = (*McpServerToolCallResponse)(nil)
)

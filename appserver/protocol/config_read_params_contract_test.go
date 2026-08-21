package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestConfigReadParamsSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	schema := defs["ConfigReadParams"].(Schema)
	assertConfigWriteClosedSchemaRequired(t, schema, nil, "cwd", "includeLayers")
	properties := schema["properties"].(Schema)
	assertConfigWriteSchemaProperty(t, properties, "includeLayers", Schema{"type": "boolean"})
	assertConfigNullableSchema(t, properties["cwd"], Schema{"type": "string"})
	assertConfigWriteSchemaDescription(t, properties, "cwd", configReadCWDDescription)
}

func TestConfigReadParamsAcceptsExactWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{`{}`, `{"cwd":null}`},
		{`{"includeLayers":false}`, `{"cwd":null}`},
		{`{"includeLayers":true}`, `{"includeLayers":true,"cwd":null}`},
		{`{"cwd":null}`, `{"cwd":null}`},
		{`{"cwd":""}`, `{"cwd":""}`},
		{`{"includeLayers":true,"cwd":"relative/project"}`, `{"includeLayers":true,"cwd":"relative/project"}`},
		{`{"includeLayers":false,"cwd":"/workspace/project"}`, `{"cwd":"/workspace/project"}`},
	} {
		var value ConfigReadParams
		assertConfigWriteRoundTrip(t, test.input, test.want, &value)
	}
}

func TestConfigReadParamsRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`,
		`{"includeLayers":null}`,
		`{"includeLayers":"false"}`,
		`{"includeLayers":0}`,
		`{"cwd":false}`,
		`{"cwd":1}`,
		`{"include_layers":true}`,
		`{"keys":[]}`,
		`{"includeValues":true}`,
		`{"extra":true}`,
		`{} {}`,
	} {
		assertJSONRejects[ConfigReadParams](t, input)
	}
}

func TestConfigReadParamsNilReceiver(t *testing.T) {
	var params *ConfigReadParams
	if err := params.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ConfigReadParams receiver succeeded")
	}
}

func TestConfigReadParamsRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ConfigReadParams") || slices.Contains(binding.Result, "ConfigReadParams") {
			t.Fatalf("ConfigReadParams unexpectedly bound to %s", binding.Method)
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

func TestConfigReadParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	if want := `export type ConfigReadParams = {`; !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
	want := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": Schema{
			"cwd": Schema{
				"anyOf":       []any{Schema{"type": "string"}, Schema{"type": "null"}},
				"description": configReadCWDDescription,
			},
			"includeLayers": Schema{"type": "boolean"},
		},
	}
	if got := JSONSchema()["$defs"].(Schema)["ConfigReadParams"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ConfigReadParams schema = %#v, want %#v", got, want)
	}
}

var _ json.Unmarshaler = (*ConfigReadParams)(nil)

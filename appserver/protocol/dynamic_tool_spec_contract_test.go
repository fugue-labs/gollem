package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestDynamicToolSpecSchemasAreExact(t *testing.T) {
	functionProperties := Schema{
		"deferLoading": Schema{"type": "boolean"},
		"description":  Schema{"type": "string"},
		"inputSchema":  true,
		"name":         Schema{"type": "string"},
	}
	functionVariant := Schema{
		"properties": mergeDynamicToolSpecSchemaProperties(functionProperties, Schema{
			"type": Schema{
				"enum":  []any{"function"},
				"title": "FunctionDynamicToolSpecType",
				"type":  "string",
			},
		}),
		"required": []string{"description", "inputSchema", "name", "type"},
		"title":    "FunctionDynamicToolSpec",
		"type":     "object",
	}
	namespaceToolFunctionVariant := Schema{
		"properties": mergeDynamicToolSpecSchemaProperties(functionProperties, Schema{
			"type": Schema{
				"enum":  []any{"function"},
				"title": "FunctionDynamicToolNamespaceToolType",
				"type":  "string",
			},
		}),
		"required": []string{"description", "inputSchema", "name", "type"},
		"title":    "FunctionDynamicToolNamespaceTool",
		"type":     "object",
	}
	namespaceProperties := Schema{
		"description": Schema{"type": "string"},
		"name":        Schema{"type": "string"},
		"tools": Schema{
			"items": Schema{"$ref": "#/$defs/DynamicToolNamespaceTool"},
			"type":  "array",
		},
	}
	namespaceVariant := Schema{
		"properties": mergeDynamicToolSpecSchemaProperties(namespaceProperties, Schema{
			"type": Schema{
				"enum":  []any{"namespace"},
				"title": "NamespaceDynamicToolSpecType",
				"type":  "string",
			},
		}),
		"required": []string{"description", "name", "tools", "type"},
		"title":    "NamespaceDynamicToolSpec",
		"type":     "object",
	}
	wants := map[string]Schema{
		"DynamicToolFunctionSpec": {
			"type": "object",
			"properties": Schema{
				"deferLoading": Schema{"type": "boolean"},
				"description":  Schema{"type": "string"},
				"inputSchema":  Schema{"$ref": "#/$defs/JsonValue"},
				"name":         Schema{"type": "string"},
			},
			"required": []string{"description", "inputSchema", "name"},
		},
		"DynamicToolNamespaceTool": {"oneOf": []any{namespaceToolFunctionVariant}},
		"DynamicToolNamespaceSpec": {
			"type":       "object",
			"properties": namespaceProperties,
			"required":   []string{"description", "name", "tools"},
		},
		"DynamicToolSpec": {"oneOf": []any{functionVariant, namespaceVariant}},
	}
	definitions := JSONSchema()["$defs"].(Schema)
	for name, want := range wants {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestDynamicToolFunctionSpecPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{
			`{"name":"","description":"","inputSchema":null}`,
			`{"name":"","description":"","inputSchema":null}`,
		},
		{
			`{"future":1,"future":2,"name":" tool ","description":" description ","inputSchema":{"z":1,"a":18446744073709551616},"deferLoading":false}`,
			`{"name":" tool ","description":" description ","inputSchema":{"a":18446744073709551616,"z":1}}`,
		},
		{
			`{"name":"tool","description":"description","inputSchema":[null,true,1e100],"deferLoading":true}`,
			`{"name":"tool","description":"description","inputSchema":[null,true,1e100],"deferLoading":true}`,
		},
	} {
		var value DynamicToolFunctionSpec
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestDynamicToolFunctionSpecRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"description":"description","inputSchema":{}}`,
		`{"name":"name","inputSchema":{}}`,
		`{"name":"name","description":"description"}`,
		`{"name":null,"description":"description","inputSchema":{}}`,
		`{"name":"name","description":null,"inputSchema":{}}`,
		`{"name":"name","description":"description","deferLoading":null,"inputSchema":{}}`,
		`{"name":"name","description":"description","deferLoading":1,"inputSchema":{}}`,
		`{"name":"a","name":"b","description":"description","inputSchema":{}}`,
		`{"name":"name","description":"a","description":"b","inputSchema":{}}`,
		`{"name":"name","description":"description","inputSchema":{},"inputSchema":[]}`,
		`{"name":"name","description":"description","inputSchema":{},"deferLoading":false,"deferLoading":true}`,
		`{"name":"name","description":"description","inputSchema":{}} {}`,
		`{"name":"name","description":"description","inputSchema":{}} x`,
	} {
		assertJSONRejects[DynamicToolFunctionSpec](t, input)
	}
}

func TestDynamicToolNamespaceToolPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{
			`{"type":"function","name":"","description":"","inputSchema":null}`,
			`{"type":"function","name":"","description":"","inputSchema":null}`,
		},
		{
			`{"future":true,"type":"function","name":" tool ","description":" description ","inputSchema":{"z":1,"a":2},"deferLoading":true,"tools":[]}`,
			`{"type":"function","name":" tool ","description":" description ","inputSchema":{"a":2,"z":1},"deferLoading":true}`,
		},
	} {
		var value DynamicToolNamespaceTool
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestDynamicToolNamespaceToolRejectsMalformedWireForms(t *testing.T) {
	validFunction := `"name":"name","description":"description","inputSchema":{}`
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{` + validFunction + `}`,
		`{"type":null,` + validFunction + `}`,
		`{"type":"namespace",` + validFunction + `}`,
		`{"type":"function","name":"name","description":"description"}`,
		`{"type":"function","name":"a","name":"b","description":"description","inputSchema":{}}`,
		`{"type":"function","type":"function",` + validFunction + `}`,
		`{"type":"function",` + validFunction + `} {}`,
		`{"type":"function",` + validFunction + `} x`,
	} {
		assertJSONRejects[DynamicToolNamespaceTool](t, input)
	}
}

func TestDynamicToolNamespaceSpecPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{
			`{"name":"","description":"","tools":[]}`,
			`{"name":"","description":"","tools":[]}`,
		},
		{
			`{"future":1,"name":" namespace ","description":" description ","tools":[{"type":"function","name":"a","description":"","inputSchema":null},{"type":"function","name":"a","description":"","inputSchema":null}]}`,
			`{"name":" namespace ","description":" description ","tools":[{"type":"function","name":"a","description":"","inputSchema":null},{"type":"function","name":"a","description":"","inputSchema":null}]}`,
		},
	} {
		var value DynamicToolNamespaceSpec
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestDynamicToolNamespaceSpecRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"description":"description","tools":[]}`,
		`{"name":"name","tools":[]}`,
		`{"name":"name","description":"description"}`,
		`{"name":null,"description":"description","tools":[]}`,
		`{"name":"name","description":null,"tools":[]}`,
		`{"name":"name","description":"description","tools":null}`,
		`{"name":"name","description":"description","tools":{}}`,
		`{"name":"name","description":"description","tools":[null]}`,
		`{"name":"name","description":"description","tools":[{"type":"namespace","name":"nested","description":"","tools":[]}]}`,
		`{"name":"a","name":"b","description":"description","tools":[]}`,
		`{"name":"name","description":"description","tools":[],"tools":[]}`,
		`{"name":"name","description":"description","tools":[]} {}`,
		`{"name":"name","description":"description","tools":[]} x`,
	} {
		assertJSONRejects[DynamicToolNamespaceSpec](t, input)
	}
}

func TestDynamicToolSpecPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{
			`{"type":"function","name":"","description":"","inputSchema":null}`,
			`{"type":"function","name":"","description":"","inputSchema":null}`,
		},
		{
			`{"type":"function","name":"tool","description":"description","inputSchema":{},"deferLoading":false,"tools":[]}`,
			`{"type":"function","name":"tool","description":"description","inputSchema":{}}`,
		},
		{
			`{"type":"namespace","name":"","description":"","tools":[]}`,
			`{"type":"namespace","name":"","description":"","tools":[]}`,
		},
		{
			`{"future":true,"type":"namespace","name":"namespace","description":"description","inputSchema":{},"deferLoading":true,"tools":[{"type":"function","name":"tool","description":"","inputSchema":[],"deferLoading":true}]}`,
			`{"type":"namespace","name":"namespace","description":"description","tools":[{"type":"function","name":"tool","description":"","inputSchema":[],"deferLoading":true}]}`,
		},
	} {
		var value DynamicToolSpec
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestDynamicToolSpecRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"name":"name","description":"description","inputSchema":{}}`,
		`{"type":null,"name":"name","description":"description","inputSchema":{}}`,
		`{"type":"other","name":"name","description":"description","inputSchema":{}}`,
		`{"type":"function","name":"name","description":"description"}`,
		`{"type":"function","name":"a","name":"b","description":"description","inputSchema":{}}`,
		`{"type":"namespace","name":"name","description":"description"}`,
		`{"type":"namespace","name":"name","description":"description","tools":[],"tools":[]}`,
		`{"type":"namespace","name":"name","description":"description","tools":[{"type":"namespace","name":"nested","description":"","tools":[]}]}`,
		`{"type":"function","type":"namespace","name":"name","description":"description","tools":[]}`,
		`{"type":"namespace","name":"name","description":"description","tools":[]} {}`,
		`{"type":"namespace","name":"name","description":"description","tools":[]} x`,
	} {
		assertJSONRejects[DynamicToolSpec](t, input)
	}
}

func TestDynamicToolSpecNilReceiversAndInvalidMarshalFailClosed(t *testing.T) {
	var function *DynamicToolFunctionSpec
	if err := function.UnmarshalJSON([]byte(`{"name":"","description":"","inputSchema":null}`)); err == nil {
		t.Fatal("nil DynamicToolFunctionSpec receiver succeeded")
	}
	var namespaceTool *DynamicToolNamespaceTool
	if err := namespaceTool.UnmarshalJSON([]byte(`{"type":"function","name":"","description":"","inputSchema":null}`)); err == nil {
		t.Fatal("nil DynamicToolNamespaceTool receiver succeeded")
	}
	var namespace *DynamicToolNamespaceSpec
	if err := namespace.UnmarshalJSON([]byte(`{"name":"","description":"","tools":[]}`)); err == nil {
		t.Fatal("nil DynamicToolNamespaceSpec receiver succeeded")
	}
	var spec *DynamicToolSpec
	if err := spec.UnmarshalJSON([]byte(`{"type":"function","name":"","description":"","inputSchema":null}`)); err == nil {
		t.Fatal("nil DynamicToolSpec receiver succeeded")
	}
	for name, value := range map[string]any{
		"empty function":       DynamicToolFunctionSpec{},
		"nil namespace tools":  DynamicToolNamespaceSpec{},
		"empty namespace tool": DynamicToolNamespaceTool{},
		"empty spec":           DynamicToolSpec{},
		"ambiguous spec": DynamicToolSpec{
			Function:  &DynamicToolFunctionSpec{},
			Namespace: &DynamicToolNamespaceSpec{Tools: []DynamicToolNamespaceTool{}},
		},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("%s marshaled", name)
		}
	}
}

func TestDynamicToolSpecContractsRemainStandalone(t *testing.T) {
	names := []string{
		"DynamicToolFunctionSpec", "DynamicToolNamespaceTool",
		"DynamicToolNamespaceSpec", "DynamicToolSpec",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if slices.Contains(names, binding.Type) {
			t.Fatalf("%s unexpectedly bound to item %s", binding.Type, binding.Kind)
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

func TestDynamicToolSpecTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type DynamicToolFunctionSpec = {\n",
		`  "deferLoading"?: boolean;`,
		`  "description": string;`,
		`  "inputSchema": JsonValue;`,
		`  "name": string;`,
		`export type DynamicToolNamespaceTool = { "type": "function" } & DynamicToolFunctionSpec;`,
		"export type DynamicToolNamespaceSpec = {\n",
		`  "description": string;`,
		`  "name": string;`,
		`  "tools": Array<DynamicToolNamespaceTool>;`,
		`export type DynamicToolSpec = { "type": "function" } & DynamicToolFunctionSpec | { "type": "namespace" } & DynamicToolNamespaceSpec;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func mergeDynamicToolSpecSchemaProperties(base, extra Schema) Schema {
	merged := make(Schema, len(base)+len(extra))
	for name, value := range base {
		merged[name] = value
	}
	for name, value := range extra {
		merged[name] = value
	}
	return merged
}

var (
	_ json.Marshaler   = DynamicToolFunctionSpec{}
	_ json.Unmarshaler = (*DynamicToolFunctionSpec)(nil)
	_ json.Marshaler   = DynamicToolNamespaceTool{}
	_ json.Unmarshaler = (*DynamicToolNamespaceTool)(nil)
	_ json.Marshaler   = DynamicToolNamespaceSpec{}
	_ json.Unmarshaler = (*DynamicToolNamespaceSpec)(nil)
	_ json.Marshaler   = DynamicToolSpec{}
	_ json.Unmarshaler = (*DynamicToolSpec)(nil)
)

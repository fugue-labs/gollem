package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMcpServerStatusSchemasAreExact(t *testing.T) {
	jsonValue := Schema{"$ref": "#/$defs/JsonValue"}
	optionalString := Schema{"type": []any{"string", "null"}}
	wants := map[string]Schema{
		"Resource": {
			"type": "object", "description": "A known resource that the server is capable of reading.",
			"properties": Schema{
				"_meta": jsonValue, "annotations": jsonValue, "description": optionalString,
				"icons":    Schema{"type": []any{"array", "null"}, "items": jsonValue},
				"mimeType": optionalString, "name": Schema{"type": "string"},
				"size":  Schema{"type": []any{"integer", "null"}, "format": "int64"},
				"title": optionalString, "uri": Schema{"type": "string"},
			},
			"required": []string{"name", "uri"},
		},
		"ResourceTemplate": {
			"type": "object", "description": "A template description for resources available on the server.",
			"properties": Schema{
				"annotations": jsonValue, "description": optionalString, "mimeType": optionalString,
				"name": Schema{"type": "string"}, "title": optionalString,
				"uriTemplate": Schema{"type": "string"},
			},
			"required": []string{"name", "uriTemplate"},
		},
		"Tool": {
			"type": "object", "description": "Definition for a tool the client can call.",
			"properties": Schema{
				"_meta": jsonValue, "annotations": jsonValue, "description": optionalString,
				"icons":       Schema{"type": []any{"array", "null"}, "items": jsonValue},
				"inputSchema": jsonValue, "name": Schema{"type": "string"},
				"outputSchema": jsonValue, "title": optionalString,
			},
			"required": []string{"inputSchema", "name"},
		},
		"McpServerStatus": {
			"type": "object",
			"properties": Schema{
				"authStatus":        Schema{"$ref": "#/$defs/McpAuthStatus"},
				"name":              Schema{"type": "string"},
				"resourceTemplates": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/ResourceTemplate"}},
				"resources":         Schema{"type": "array", "items": Schema{"$ref": "#/$defs/Resource"}},
				"serverInfo": Schema{"anyOf": []any{
					Schema{"$ref": "#/$defs/McpServerInfo"}, Schema{"type": "null"},
				}},
				"tools": Schema{"type": "object", "additionalProperties": Schema{"$ref": "#/$defs/Tool"}},
			},
			"required": []string{"authStatus", "name", "resourceTemplates", "resources", "tools"},
		},
		"ListMcpServerStatusResponse": {
			"type": "object",
			"properties": Schema{
				"data": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/McpServerStatus"}},
				"nextCursor": Schema{
					"description": "Opaque cursor to pass to the next call to continue after the last item. If None, there are no more items to return.",
					"type":        []any{"string", "null"},
				},
			},
			"required": []string{"data"},
		},
	}
	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range wants {
		if got := defs[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestMcpResourcePreservesSerdeWireForms(t *testing.T) {
	roundTripMcpStatus[Resource](t,
		`{"name":"","uri":""}`,
		`{"name":"","uri":""}`,
	)
	roundTripMcpStatus[Resource](t,
		`{"future":1,"name":" resource ","title":"","description":" desc ","mimeType":"text/plain",`+
			`"size":9223372036854775807,"uri":"scheme://value","annotations":{"z":2,"a":1},`+
			`"icons":[],"_meta":[true,null]}`,
		`{"annotations":{"a":1,"z":2},"description":" desc ","mimeType":"text/plain","name":" resource ",`+
			`"size":9223372036854775807,"title":"","uri":"scheme://value","icons":[],"_meta":[true,null]}`,
	)
	roundTripMcpStatus[Resource](t,
		`{"name":"r","uri":"u","title":null,"description":null,"mimeType":null,"size":null,`+
			`"annotations":null,"icons":null,"_meta":null}`,
		`{"name":"r","uri":"u"}`,
	)
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`, `{"name":"r"}`, `{"uri":"u"}`,
		`{"name":null,"uri":"u"}`, `{"name":"r","uri":null}`, `{"name":"r","uri":"u","size":1.5}`,
		`{"name":"r","uri":"u","size":9223372036854775808}`, `{"name":"r","uri":"u","icons":{}}`,
		`{"name":"r","uri":"u","annotations":}`, `{"name":"a","name":"b","uri":"u"}`,
		`{"name":"r","uri":"a","uri":"b"}`, `{"name":"r","uri":"u"} {}`,
	} {
		assertJSONRejects[Resource](t, input)
	}
}

func TestMcpResourceTemplatePreservesSerdeWireForms(t *testing.T) {
	roundTripMcpStatus[ResourceTemplate](t,
		`{"name":"","uriTemplate":""}`,
		`{"uriTemplate":"","name":""}`,
	)
	roundTripMcpStatus[ResourceTemplate](t,
		`{"future":true,"annotations":{"b":2,"a":1},"uriTemplate":"file:///{path}","name":"template",`+
			`"title":"Title","description":"Description","mimeType":"application/json"}`,
		`{"annotations":{"a":1,"b":2},"uriTemplate":"file:///{path}","name":"template",`+
			`"title":"Title","description":"Description","mimeType":"application/json"}`,
	)
	roundTripMcpStatus[ResourceTemplate](t,
		`{"annotations":null,"uriTemplate":"u","name":"n","title":null,"description":null,"mimeType":null}`,
		`{"uriTemplate":"u","name":"n"}`,
	)
	for _, input := range []string{
		`null`, `{}`, `{"name":"n"}`, `{"uriTemplate":"u"}`, `{"name":null,"uriTemplate":"u"}`,
		`{"name":"n","uriTemplate":null}`, `{"name":"n","uri_template":"u"}`,
		`{"name":"n","uriTemplate":"u","annotations":}`, `{"name":"a","name":"b","uriTemplate":"u"}`,
	} {
		assertJSONRejects[ResourceTemplate](t, input)
	}
}

func TestMcpToolPreservesSerdeWireForms(t *testing.T) {
	roundTripMcpStatus[Tool](t,
		`{"name":"","inputSchema":null}`,
		`{"name":"","inputSchema":null}`,
	)
	roundTripMcpStatus[Tool](t,
		`{"future":1,"name":"tool","title":"Title","description":"Description",`+
			`"inputSchema":{"type":"object","properties":{"x":{"type":"string"}}},`+
			`"outputSchema":{},"annotations":{"readOnlyHint":true},"icons":[],"_meta":{"z":2,"a":1}}`,
		`{"name":"tool","title":"Title","description":"Description",`+
			`"inputSchema":{"properties":{"x":{"type":"string"}},"type":"object"},`+
			`"outputSchema":{},"annotations":{"readOnlyHint":true},"icons":[],"_meta":{"a":1,"z":2}}`,
	)
	roundTripMcpStatus[Tool](t,
		`{"name":"tool","title":null,"description":null,"inputSchema":{},"outputSchema":null,`+
			`"annotations":null,"icons":null,"_meta":null}`,
		`{"name":"tool","inputSchema":{}}`,
	)
	for _, input := range []string{
		`null`, `{}`, `{"name":"tool"}`, `{"inputSchema":{}}`, `{"name":null,"inputSchema":{}}`,
		`{"name":"tool","inputSchema":}`, `{"name":"tool","input_schema":{}}`,
		`{"name":"a","name":"b","inputSchema":{}}`, `{"name":"tool","inputSchema":{},"inputSchema":{}}`,
	} {
		assertJSONRejects[Tool](t, input)
	}
}

func TestMcpServerStatusAndListResponsePreserveSerdeWireForms(t *testing.T) {
	minimalStatus := `{"name":"server","tools":{},"resources":[],"resourceTemplates":[],"authStatus":"unsupported"}`
	roundTripMcpStatus[McpServerStatus](t, minimalStatus,
		`{"name":"server","serverInfo":null,"tools":{},"resources":[],"resourceTemplates":[],"authStatus":"unsupported"}`,
	)
	roundTripMcpStatus[McpServerStatus](t,
		`{"future":true,"name":"server","serverInfo":null,"tools":{"z":{"name":"old","inputSchema":{}},`+
			`"a":{"name":"a","inputSchema":null}},"resources":[{"name":"r","uri":"u"}],`+
			`"resourceTemplates":[{"name":"t","uriTemplate":"x/{id}"}],"authStatus":"oAuth"}`,
		`{"name":"server","serverInfo":null,"tools":{"a":{"name":"a","inputSchema":null},`+
			`"z":{"name":"old","inputSchema":{}}},"resources":[{"name":"r","uri":"u"}],`+
			`"resourceTemplates":[{"uriTemplate":"x/{id}","name":"t"}],"authStatus":"oAuth"}`,
	)
	roundTripMcpStatus[ListMcpServerStatusResponse](t,
		`{"data":[`+minimalStatus+`],"future":1}`,
		`{"data":[{"name":"server","serverInfo":null,"tools":{},"resources":[],"resourceTemplates":[],`+
			`"authStatus":"unsupported"}],"nextCursor":null}`,
	)
	roundTripMcpStatus[ListMcpServerStatusResponse](t,
		`{"data":[],"nextCursor":" next "}`,
		`{"data":[],"nextCursor":" next "}`,
	)
	for _, input := range []string{
		`null`, `{}`, `{"name":"server","tools":{},"resources":[],"resourceTemplates":[]}`,
		`{"name":"server","tools":null,"resources":[],"resourceTemplates":[],"authStatus":"unsupported"}`,
		`{"name":"server","tools":{},"resources":null,"resourceTemplates":[],"authStatus":"unsupported"}`,
		`{"name":"server","tools":{},"resources":[],"resourceTemplates":null,"authStatus":"unsupported"}`,
		`{"name":"server","tools":{"x":null},"resources":[],"resourceTemplates":[],"authStatus":"unsupported"}`,
		`{"name":"server","tools":{},"resources":[null],"resourceTemplates":[],"authStatus":"unsupported"}`,
		`{"name":"server","tools":{},"resources":[],"resourceTemplates":[],"authStatus":"unknown"}`,
		`{"name":"a","name":"b","tools":{},"resources":[],"resourceTemplates":[],"authStatus":"unsupported"}`,
	} {
		assertJSONRejects[McpServerStatus](t, input)
	}
	for _, input := range []string{
		`null`, `{}`, `{"data":null}`, `{"data":{}}`, `{"data":[null]}`, `{"data":[],"nextCursor":1}`,
		`{"data":[],"data":[]}`, `{"data":[]} {}`,
	} {
		assertJSONRejects[ListMcpServerStatusResponse](t, input)
	}
}

func TestMcpServerStatusToolMapUsesSerdeLastWins(t *testing.T) {
	input := `{"name":"server","tools":{"same":{"name":"first","inputSchema":{}},` +
		`"same":{"name":"second","inputSchema":null}},"resources":[],"resourceTemplates":[],"authStatus":"bearerToken"}`
	var status McpServerStatus
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got := status.Tools["same"].Name; got != "second" {
		t.Fatalf("duplicate map key name = %q, want second", got)
	}
}

func TestMcpServerStatusRejectsInvalidConstructedValues(t *testing.T) {
	for name, value := range map[string]any{
		"resource JSON": Resource{Name: "r", URI: "u", Annotations: &JsonValue{}},
		"template JSON": ResourceTemplate{Name: "t", URITemplate: "u", Annotations: &JsonValue{}},
		"tool JSON":     Tool{Name: "t", InputSchema: JsonValue{}},
		"nil tools":     McpServerStatus{Name: "s", Resources: []Resource{}, ResourceTemplates: []ResourceTemplate{}, AuthStatus: McpAuthStatusUnsupported},
		"nil resources": McpServerStatus{Name: "s", Tools: map[string]Tool{}, ResourceTemplates: []ResourceTemplate{}, AuthStatus: McpAuthStatusUnsupported},
		"nil templates": McpServerStatus{Name: "s", Tools: map[string]Tool{}, Resources: []Resource{}, AuthStatus: McpAuthStatusUnsupported},
		"nil data":      ListMcpServerStatusResponse{},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("%s marshaled", name)
		}
	}
	var nilIcons []JsonValue
	if encoded, err := json.Marshal(Resource{Name: "r", URI: "u", Icons: &nilIcons}); err != nil ||
		string(encoded) != `{"name":"r","uri":"u","icons":[]}` {
		t.Errorf("resource nil icon slice = %s, %v", encoded, err)
	}
	inputSchema := mustMcpStatusJSONValue(t, `{}`)
	if encoded, err := json.Marshal(Tool{Name: "t", InputSchema: inputSchema, Icons: &nilIcons}); err != nil ||
		string(encoded) != `{"name":"t","inputSchema":{},"icons":[]}` {
		t.Errorf("tool nil icon slice = %s, %v", encoded, err)
	}
	var resource *Resource
	if err := resource.UnmarshalJSON([]byte(`{"name":"r","uri":"u"}`)); err == nil {
		t.Error("nil Resource receiver succeeded")
	}
	var template *ResourceTemplate
	if err := template.UnmarshalJSON([]byte(`{"name":"t","uriTemplate":"u"}`)); err == nil {
		t.Error("nil ResourceTemplate receiver succeeded")
	}
	var tool *Tool
	if err := tool.UnmarshalJSON([]byte(`{"name":"t","inputSchema":{}}`)); err == nil {
		t.Error("nil Tool receiver succeeded")
	}
	var status *McpServerStatus
	if err := status.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Error("nil McpServerStatus receiver succeeded")
	}
	var response *ListMcpServerStatusResponse
	if err := response.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Error("nil ListMcpServerStatusResponse receiver succeeded")
	}
}

func mustMcpStatusJSONValue(t *testing.T, input string) JsonValue {
	t.Helper()
	var value JsonValue
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("decode JSON value: %v", err)
	}
	return value
}

func TestMcpServerStatusContractsRemainStandalone(t *testing.T) {
	names := []string{"Resource", "ResourceTemplate", "Tool", "McpServerStatus", "ListMcpServerStatusResponse"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	method, ok := LookupMethod("mcpServerStatus/list")
	if !ok || method.State != MethodImplemented {
		t.Fatalf("mcpServerStatus/list = %#v, %v; want implemented", method, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if len(Methods()) != 229 || len(WireTypeBindings()) != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("counts = %d/%d/%d, want 229/85/5", len(Methods()), len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
}

func TestMcpServerStatusTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type Resource = {`, `"annotations"?: JsonValue;`, `"icons"?: Array<JsonValue>;`,
		`"size"?: number;`, `"uri": string;`, `export type ResourceTemplate = {`,
		`"uriTemplate": string;`, `export type Tool = {`, `"inputSchema": JsonValue;`,
		`"outputSchema"?: JsonValue;`, `export type McpServerStatus = {`,
		`"serverInfo": McpServerInfo | null;`, `"tools": { [key in string]?: Tool };`,
		`export type ListMcpServerStatusResponse = {`, `"data": Array<McpServerStatus>;`,
		`"nextCursor": string | null;`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func roundTripMcpStatus[T any](t *testing.T, input, want string) {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

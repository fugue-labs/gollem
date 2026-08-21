package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPermissionProfileListSchemasAreExact(t *testing.T) {
	wants := map[string]Schema{
		"PermissionProfileListParams": {
			"type": "object",
			"properties": Schema{
				"cursor": Schema{
					"description": "Opaque pagination cursor returned by a previous call.",
					"type":        []any{"string", "null"},
				},
				"cwd": Schema{
					"description": "Optional working directory to resolve project config layers.",
					"type":        []any{"string", "null"},
				},
				"limit": Schema{
					"description": "Optional page size; defaults to the full result set.",
					"format":      "uint32",
					"minimum":     float64(0),
					"type":        []any{"integer", "null"},
				},
			},
		},
		"PermissionProfileSummary": {
			"type": "object",
			"properties": Schema{
				"allowed": Schema{
					"description": "Whether the effective requirements allow selecting this profile.",
					"type":        "boolean",
				},
				"description": Schema{
					"description": "Optional user-facing description for display in clients.",
					"type":        []any{"string", "null"},
				},
				"id": Schema{
					"description": "Available permission profile identifier.",
					"type":        "string",
				},
			},
			"required": []string{"allowed", "id"},
		},
		"PermissionProfileListResponse": {
			"type": "object",
			"properties": Schema{
				"data": Schema{
					"items": Schema{"$ref": "#/$defs/PermissionProfileSummary"},
					"type":  "array",
				},
				"nextCursor": Schema{
					"description": "Opaque cursor to pass to the next call to continue after the last item. If None, there are no more items to return.",
					"type":        []any{"string", "null"},
				},
			},
			"required": []string{"data"},
		},
	}
	definitions := JSONSchema()["$defs"].(Schema)
	for name, want := range wants {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestPermissionProfileListParamsPreserveSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  PermissionProfileListParams
		json  string
	}{
		{`{}`, PermissionProfileListParams{}, `{"cursor":null,"limit":null,"cwd":null}`},
		{
			`{"cursor":null,"limit":null,"cwd":null}`,
			PermissionProfileListParams{},
			`{"cursor":null,"limit":null,"cwd":null}`,
		},
		{
			`{"future":1,"future":2,"cursor":"","limit":0,"cwd":" "}`,
			PermissionProfileListParams{
				Cursor: stringPointer(""), Limit: uint32Pointer(0), CWD: stringPointer(" "),
			},
			`{"cursor":"","limit":0,"cwd":" "}`,
		},
		{
			`{"cursor":" next ","limit":4294967295,"cwd":" /workspace "}`,
			PermissionProfileListParams{
				Cursor: stringPointer(" next "),
				Limit:  uint32Pointer(math.MaxUint32),
				CWD:    stringPointer(" /workspace "),
			},
			`{"cursor":" next ","limit":4294967295,"cwd":" /workspace "}`,
		},
	} {
		var params PermissionProfileListParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		if !reflect.DeepEqual(params, tc.want) {
			t.Errorf("params %s = %#v, want %#v", tc.input, params, tc.want)
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.json {
			t.Errorf("marshal %#v = %s, %v; want %s", params, encoded, err, tc.json)
		}
	}
}

func TestPermissionProfileListParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"cursor":1}`, `{"cursor":true}`, `{"cwd":1}`, `{"cwd":false}`,
		`{"limit":-1}`, `{"limit":0.5}`, `{"limit":1e3}`,
		`{"limit":4294967296}`, `{"limit":"1"}`, `{"limit":true}`,
		`{"cursor":null,"cursor":"next"}`, `{"limit":null,"limit":0}`,
		`{"cwd":null,"cwd":"path"}`, `{} {}`, `{} x`,
	} {
		assertJSONRejects[PermissionProfileListParams](t, input)
	}
}

func TestPermissionProfileSummaryPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  PermissionProfileSummary
		json  string
	}{
		{
			`{"id":"","allowed":false}`,
			PermissionProfileSummary{},
			`{"id":"","description":null,"allowed":false}`,
		},
		{
			`{"id":"profile","description":null,"allowed":true}`,
			PermissionProfileSummary{ID: "profile", Allowed: true},
			`{"id":"profile","description":null,"allowed":true}`,
		},
		{
			`{"future":1,"future":2,"id":" profile ","description":" description ","allowed":false}`,
			PermissionProfileSummary{
				ID: " profile ", Description: stringPointer(" description "), Allowed: false,
			},
			`{"id":" profile ","description":" description ","allowed":false}`,
		},
	} {
		var summary PermissionProfileSummary
		if err := json.Unmarshal([]byte(tc.input), &summary); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		if !reflect.DeepEqual(summary, tc.want) {
			t.Errorf("summary %s = %#v, want %#v", tc.input, summary, tc.want)
		}
		encoded, err := json.Marshal(summary)
		if err != nil || string(encoded) != tc.json {
			t.Errorf("marshal %#v = %s, %v; want %s", summary, encoded, err, tc.json)
		}
	}
}

func TestPermissionProfileSummaryRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"allowed":true}`, `{"id":"profile"}`,
		`{"id":null,"allowed":true}`, `{"id":1,"allowed":true}`,
		`{"id":"profile","allowed":null}`, `{"id":"profile","allowed":"true"}`,
		`{"id":"profile","description":1,"allowed":true}`,
		`{"id":"a","id":"b","allowed":true}`,
		`{"id":"profile","description":null,"description":"text","allowed":true}`,
		`{"id":"profile","allowed":true,"allowed":false}`,
		`{"id":"profile","allowed":true} {}`, `{"id":"profile","allowed":true} x`,
	} {
		assertJSONRejects[PermissionProfileSummary](t, input)
	}
}

func TestPermissionProfileListResponsePreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"data":[]}`, `{"data":[],"nextCursor":null}`},
		{`{"future":true,"data":[],"nextCursor":null}`, `{"data":[],"nextCursor":null}`},
		{
			`{"data":[{"id":"a","allowed":true},{"id":"a","description":"","allowed":false}],"nextCursor":" next "}`,
			`{"data":[{"id":"a","description":null,"allowed":true},{"id":"a","description":"","allowed":false}],"nextCursor":" next "}`,
		},
	} {
		var response PermissionProfileListResponse
		if err := json.Unmarshal([]byte(tc.input), &response); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(response)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestPermissionProfileListResponseRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"data":null}`, `{"data":{}}`, `{"data":[null]}`, `{"data":[{}]}`,
		`{"data":[{"id":"profile"}]}`, `{"data":[{"allowed":true}]}`,
		`{"data":[],"nextCursor":1}`, `{"data":[],"nextCursor":false}`,
		`{"data":[],"data":[]}`, `{"data":[],"nextCursor":null,"nextCursor":"next"}`,
		`{"data":[]} {}`, `{"data":[]} x`,
	} {
		assertJSONRejects[PermissionProfileListResponse](t, input)
	}
}

func TestPermissionProfileListNilReceiversAndInvalidMarshalFailClosed(t *testing.T) {
	var params *PermissionProfileListParams
	if err := params.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	var summary *PermissionProfileSummary
	if err := summary.UnmarshalJSON([]byte(`{"id":"profile","allowed":true}`)); err == nil {
		t.Fatal("nil summary receiver succeeded")
	}
	var response *PermissionProfileListResponse
	if err := response.UnmarshalJSON([]byte(`{"data":[]}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}
	if _, err := json.Marshal(PermissionProfileListResponse{}); err == nil {
		t.Fatal("nil response data marshaled")
	}
}

func TestPermissionProfileListContractsRemainStandalone(t *testing.T) {
	names := []string{
		"PermissionProfileListParams", "PermissionProfileSummary", "PermissionProfileListResponse",
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
	method, ok := LookupMethod("permissionProfile/list")
	if !ok || method.Surface != SurfaceClientRequest || method.State != MethodImplemented {
		t.Fatalf("permissionProfile/list = %#v, %v; want existing implemented client request", method, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if len(Methods()) != 229 || len(WireTypeBindings()) != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("counts = %d/%d/%d, want 229/86/5", len(Methods()), len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
}

func TestPermissionProfileListTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type PermissionProfileListParams = {\n" +
			"  \"cursor\"?: string | null;\n" +
			"  \"cwd\"?: string | null;\n" +
			"  \"limit\"?: number | null;\n" +
			"};",
		"export type PermissionProfileSummary = {\n" +
			"  \"allowed\": boolean;\n" +
			"  \"description\": string | null;\n" +
			"  \"id\": string;\n" +
			"};",
		"export type PermissionProfileListResponse = {\n" +
			"  \"data\": Array<PermissionProfileSummary>;\n" +
			"  \"nextCursor\": string | null;\n" +
			"};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func uint32Pointer(value uint32) *uint32 { return &value }

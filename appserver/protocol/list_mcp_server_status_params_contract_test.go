package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestListMcpServerStatusParamsSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	params, ok := defs["ListMcpServerStatusParams"].(Schema)
	if !ok {
		t.Fatal("$defs missing ListMcpServerStatusParams")
	}
	if params["type"] != "object" || params["additionalProperties"] != false {
		t.Fatalf("ListMcpServerStatusParams is not a closed object: %#v", params)
	}
	if got := schemaRequiredNames(params); len(got) != 0 {
		t.Fatalf("ListMcpServerStatusParams required = %v, want none", got)
	}
	wantProperties := Schema{
		"cursor": Schema{
			"description": "Opaque pagination cursor returned by a previous call.",
			"anyOf":       []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
		"limit": Schema{
			"description": "Optional page size; defaults to a server-defined value.",
			"anyOf": []any{
				Schema{"type": "integer", "minimum": 0, "maximum": 4294967295},
				Schema{"type": "null"},
			},
		},
		"detail": Schema{
			"description": "Controls how much MCP inventory data to fetch for each server. Defaults to `Full` when omitted.",
			"anyOf": []any{
				Schema{"$ref": "#/$defs/McpServerStatusDetail"},
				Schema{"type": "null"},
			},
		},
		"threadId": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
	}
	if got := params["properties"].(Schema); !reflect.DeepEqual(got, wantProperties) {
		t.Fatalf("ListMcpServerStatusParams properties = %#v, want %#v", got, wantProperties)
	}
}

func TestListMcpServerStatusParamsAcceptsExactWireFormsAndCanonicalizesOptions(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: `{}`,
			want:  `{"cursor":null,"limit":null,"detail":null,"threadId":null}`,
		},
		{
			input: `{"cursor":null,"limit":null,"detail":null,"threadId":null}`,
			want:  `{"cursor":null,"limit":null,"detail":null,"threadId":null}`,
		},
		{
			input: `{"cursor":"","limit":0,"detail":"full","threadId":""}`,
			want:  `{"cursor":"","limit":0,"detail":"full","threadId":""}`,
		},
		{
			input: `{"cursor":"next","limit":4294967295,"detail":"toolsAndAuthOnly","threadId":"thread-1"}`,
			want:  `{"cursor":"next","limit":4294967295,"detail":"toolsAndAuthOnly","threadId":"thread-1"}`,
		},
	}
	for _, tc := range cases {
		var params ListMcpServerStatusParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}

	cursor := "next"
	limit := uint32(math.MaxUint32)
	detail := McpServerStatusDetailToolsAndAuthOnly
	threadID := "thread-1"
	encoded, err := json.Marshal(ListMcpServerStatusParams{
		Cursor: &cursor, Limit: &limit, Detail: &detail, ThreadID: &threadID,
	})
	want := `{"cursor":"next","limit":4294967295,"detail":"toolsAndAuthOnly","threadId":"thread-1"}`
	if err != nil || string(encoded) != want {
		t.Fatalf("marshal populated params = %s, %v; want %s", encoded, err, want)
	}
}

func TestListMcpServerStatusParamsRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`,
		`{"cursor":1}`, `{"cursor":false}`,
		`{"limit":-1}`, `{"limit":1.5}`, `{"limit":4294967296}`, `{"limit":"1"}`,
		`{"detail":""}`, `{"detail":"Full"}`, `{"detail":"tools_and_auth_only"}`,
		`{"detail":"other"}`, `{"detail":1}`, `{"detail":false}`, `{"detail":{}}`,
		`{"threadId":1}`, `{"threadId":false}`,
		`{"thread_id":"thread-1"}`, `{"serverId":"server-1"}`, `{"serverName":"server"}`,
		`{"name":"server"}`, `{"servers":[]}`, `{"unknown":null}`,
		`{"limit":1} {}`,
	}
	for _, input := range invalid {
		var params ListMcpServerStatusParams
		if err := json.Unmarshal([]byte(input), &params); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	var params *ListMcpServerStatusParams
	if err := params.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ListMcpServerStatusParams receiver succeeded")
	}
}

func TestListMcpServerStatusParamsRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ListMcpServerStatusParams") ||
			slices.Contains(binding.Result, "ListMcpServerStatusParams") {
			t.Fatalf("ListMcpServerStatusParams unexpectedly bound: %#v", binding)
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

func TestListMcpServerStatusParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ListMcpServerStatusParams = {`,
		`"cursor"?: string | null;`,
		`"detail"?: McpServerStatusDetail | null;`,
		`"limit"?: number | null;`,
		`"threadId"?: string | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = ListMcpServerStatusParams{}
	_ json.Unmarshaler = (*ListMcpServerStatusParams)(nil)
)

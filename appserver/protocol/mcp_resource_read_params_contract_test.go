package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMcpResourceReadParamsSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	params, ok := defs["McpResourceReadParams"].(Schema)
	if !ok {
		t.Fatal("$defs missing McpResourceReadParams")
	}
	if params["type"] != "object" || params["additionalProperties"] != false {
		t.Fatalf("McpResourceReadParams is not a closed object: %#v", params)
	}
	if got, want := schemaRequiredNames(params), []string{"server", "uri"}; !slices.Equal(got, want) {
		t.Fatalf("McpResourceReadParams required = %v, want %v", got, want)
	}
	wantProperties := Schema{
		"threadId": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
		"server": Schema{"type": "string"},
		"uri":    Schema{"type": "string"},
	}
	if got := params["properties"].(Schema); !reflect.DeepEqual(got, wantProperties) {
		t.Fatalf("McpResourceReadParams properties = %#v, want %#v", got, wantProperties)
	}
}

func TestMcpResourceReadParamsAcceptsExactWireFormsAndCanonicalizesThread(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: `{"server":"","uri":""}`,
			want:  `{"threadId":null,"server":"","uri":""}`,
		},
		{
			input: `{"threadId":null,"server":"repo","uri":"file:///workspace/README.md"}`,
			want:  `{"threadId":null,"server":"repo","uri":"file:///workspace/README.md"}`,
		},
		{
			input: `{"threadId":"thread-1","server":"repo","uri":"file:///workspace/README.md"}`,
			want:  `{"threadId":"thread-1","server":"repo","uri":"file:///workspace/README.md"}`,
		},
	}
	for _, tc := range cases {
		var params McpResourceReadParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}

	threadID := "thread-1"
	encoded, err := json.Marshal(McpResourceReadParams{
		ThreadID: &threadID,
		Server:   "repo",
		URI:      "file:///workspace/README.md",
	})
	want := `{"threadId":"thread-1","server":"repo","uri":"file:///workspace/README.md"}`
	if err != nil || string(encoded) != want {
		t.Fatalf("marshal populated params = %s, %v; want %s", encoded, err, want)
	}
}

func TestMcpResourceReadParamsRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`,
		`{}`, `{"threadId":null}`, `{"server":"repo"}`, `{"uri":"resource"}`,
		`{"server":null,"uri":"resource"}`, `{"server":1,"uri":"resource"}`,
		`{"server":false,"uri":"resource"}`, `{"server":{},"uri":"resource"}`,
		`{"server":"repo","uri":null}`, `{"server":"repo","uri":1}`,
		`{"server":"repo","uri":false}`, `{"server":"repo","uri":{}}`,
		`{"threadId":1,"server":"repo","uri":"resource"}`,
		`{"threadId":false,"server":"repo","uri":"resource"}`,
		`{"thread_id":"thread-1","server":"repo","uri":"resource"}`,
		`{"serverId":"repo","server":"repo","uri":"resource"}`,
		`{"serverName":"repo","server":"repo","uri":"resource"}`,
		`{"name":"repo","server":"repo","uri":"resource"}`,
		`{"mcpServerId":"repo","server":"repo","uri":"resource"}`,
		`{"mcpServer":"repo","server":"repo","uri":"resource"}`,
		`{"resourceUri":"resource","server":"repo","uri":"resource"}`,
		`{"resourceURI":"resource","server":"repo","uri":"resource"}`,
		`{"unknown":null,"server":"repo","uri":"resource"}`,
		`{"server":"repo","uri":"resource"} {}`,
	}
	for _, input := range invalid {
		var params McpResourceReadParams
		if err := json.Unmarshal([]byte(input), &params); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	var params *McpResourceReadParams
	if err := params.UnmarshalJSON([]byte(`{"server":"repo","uri":"resource"}`)); err == nil {
		t.Fatal("nil McpResourceReadParams receiver succeeded")
	}
}

func TestMcpResourceReadParamsRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "McpResourceReadParams") ||
			slices.Contains(binding.Result, "McpResourceReadParams") {
			t.Fatalf("McpResourceReadParams unexpectedly bound: %#v", binding)
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

func TestMcpResourceReadParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type McpResourceReadParams = {`,
		`"server": string;`,
		`"threadId"?: string | null;`,
		`"uri": string;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = McpResourceReadParams{}
	_ json.Unmarshaler = (*McpResourceReadParams)(nil)
)

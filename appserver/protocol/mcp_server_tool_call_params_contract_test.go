package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMcpServerToolCallParamsSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	params, ok := defs["McpServerToolCallParams"].(Schema)
	if !ok {
		t.Fatal("$defs missing McpServerToolCallParams")
	}
	if params["type"] != "object" || params["additionalProperties"] != false {
		t.Fatalf("McpServerToolCallParams is not a closed object: %#v", params)
	}
	if got, want := schemaRequiredNames(params), []string{"server", "threadId", "tool"}; !slices.Equal(got, want) {
		t.Fatalf("McpServerToolCallParams required = %v, want %v", got, want)
	}
	wantProperties := Schema{
		"threadId":  Schema{"type": "string"},
		"server":    Schema{"type": "string"},
		"tool":      Schema{"type": "string"},
		"arguments": Schema{"$ref": "#/$defs/JsonValue"},
		"_meta":     Schema{"$ref": "#/$defs/JsonValue"},
	}
	if got := params["properties"].(Schema); !reflect.DeepEqual(got, wantProperties) {
		t.Fatalf("McpServerToolCallParams properties = %#v, want %#v", got, wantProperties)
	}
}

func TestMcpServerToolCallParamsAcceptsExactWireFormsAndCanonicalizesOptions(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: `{"threadId":"","server":"","tool":""}`,
			want:  `{"threadId":"","server":"","tool":""}`,
		},
		{
			input: `{"threadId":"thread-1","server":"repo","tool":"echo","arguments":null,"_meta":null}`,
			want:  `{"threadId":"thread-1","server":"repo","tool":"echo"}`,
		},
		{
			input: `{"threadId":"thread-1","server":"repo","tool":"echo","arguments":"value","_meta":false}`,
			want:  `{"threadId":"thread-1","server":"repo","tool":"echo","arguments":"value","_meta":false}`,
		},
		{
			input: `{"threadId":"thread-1","server":"repo","tool":"echo","arguments":{"big":9007199254740993123456789,"items":[true,null,"x"]},"_meta":[1,{"ok":true}]}`,
			want:  `{"threadId":"thread-1","server":"repo","tool":"echo","arguments":{"big":9007199254740993123456789,"items":[true,null,"x"]},"_meta":[1,{"ok":true}]}`,
		},
	}
	for _, tc := range cases {
		var params McpServerToolCallParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}

	arguments := JsonValue{raw: json.RawMessage(`{"n":9007199254740993123456789}`)}
	meta := JsonValue{raw: json.RawMessage(`{"source":"client"}`)}
	encoded, err := json.Marshal(McpServerToolCallParams{
		ThreadID:  "thread-1",
		Server:    "repo",
		Tool:      "echo",
		Arguments: &arguments,
		Meta:      &meta,
	})
	want := `{"threadId":"thread-1","server":"repo","tool":"echo","arguments":{"n":9007199254740993123456789},"_meta":{"source":"client"}}`
	if err != nil || string(encoded) != want {
		t.Fatalf("marshal populated params = %s, %v; want %s", encoded, err, want)
	}

	if _, err := json.Marshal(McpServerToolCallParams{
		ThreadID: "thread-1", Server: "repo", Tool: "echo", Arguments: &JsonValue{},
	}); err == nil {
		t.Fatal("marshal invalid constructed arguments succeeded")
	}
	if _, err := json.Marshal(McpServerToolCallParams{
		ThreadID: "thread-1", Server: "repo", Tool: "echo", Meta: &JsonValue{},
	}); err == nil {
		t.Fatal("marshal invalid constructed metadata succeeded")
	}
}

func TestMcpServerToolCallParamsRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`,
		`{}`, `{"threadId":"thread-1"}`, `{"server":"repo"}`, `{"tool":"echo"}`,
		`{"threadId":"thread-1","server":"repo"}`,
		`{"threadId":"thread-1","tool":"echo"}`,
		`{"server":"repo","tool":"echo"}`,
		`{"threadId":null,"server":"repo","tool":"echo"}`,
		`{"threadId":1,"server":"repo","tool":"echo"}`,
		`{"threadId":false,"server":"repo","tool":"echo"}`,
		`{"threadId":{},"server":"repo","tool":"echo"}`,
		`{"threadId":"thread-1","server":null,"tool":"echo"}`,
		`{"threadId":"thread-1","server":1,"tool":"echo"}`,
		`{"threadId":"thread-1","server":false,"tool":"echo"}`,
		`{"threadId":"thread-1","server":{},"tool":"echo"}`,
		`{"threadId":"thread-1","server":"repo","tool":null}`,
		`{"threadId":"thread-1","server":"repo","tool":1}`,
		`{"threadId":"thread-1","server":"repo","tool":false}`,
		`{"threadId":"thread-1","server":"repo","tool":{}}`,
		`{"thread_id":"thread-1","threadId":"thread-1","server":"repo","tool":"echo"}`,
		`{"serverName":"repo","threadId":"thread-1","server":"repo","tool":"echo"}`,
		`{"toolName":"echo","threadId":"thread-1","server":"repo","tool":"echo"}`,
		`{"args":{},"threadId":"thread-1","server":"repo","tool":"echo"}`,
		`{"input":{},"threadId":"thread-1","server":"repo","tool":"echo"}`,
		`{"meta":{},"threadId":"thread-1","server":"repo","tool":"echo"}`,
		`{"unknown":null,"threadId":"thread-1","server":"repo","tool":"echo"}`,
		`{"threadId":"thread-1","server":"repo","tool":"echo","arguments":}`,
		`{"threadId":"thread-1","server":"repo","tool":"echo"} {}`,
	}
	for _, input := range invalid {
		var params McpServerToolCallParams
		if err := json.Unmarshal([]byte(input), &params); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	var params *McpServerToolCallParams
	if err := params.UnmarshalJSON([]byte(`{"threadId":"thread-1","server":"repo","tool":"echo"}`)); err == nil {
		t.Fatal("nil McpServerToolCallParams receiver succeeded")
	}
}

func TestMcpServerToolCallParamsRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "McpServerToolCallParams") ||
			slices.Contains(binding.Result, "McpServerToolCallParams") {
			t.Fatalf("McpServerToolCallParams unexpectedly bound: %#v", binding)
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

func TestMcpServerToolCallParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type McpServerToolCallParams = {`,
		`"_meta"?: JsonValue;`,
		`"arguments"?: JsonValue;`,
		`"server": string;`,
		`"threadId": string;`,
		`"tool": string;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = McpServerToolCallParams{}
	_ json.Unmarshaler = (*McpServerToolCallParams)(nil)
)

package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestJSONRPCEnvelopeSchemasAreExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	for name, want := range jsonRPCEnvelopeSchemas() {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestJSONRPCEnvelopesPreserveSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		value any
		want  string
	}{
		{`{"future":true,"method":"notice","params":null}`, &JSONRPCNotification{}, `{"method":"notice"}`},
		{`{"method":"notice","params":{"z":1,"a":[null,true]}}`, &JSONRPCNotification{}, `{"method":"notice","params":{"a":[null,true],"z":1}}`},
		{`{"future":true,"id":"req-1","method":"run","params":{"z":1,"a":[null,true]},"trace":{"tracestate":"vendor=value","traceparent":null,"unknown":true}}`, &JSONRPCRequest{}, `{"id":"req-1","method":"run","params":{"a":[null,true],"z":1},"trace":{"tracestate":"vendor=value"}}`},
		{`{"id":-4,"result":null,"future":true}`, &JSONRPCResponse{}, `{"id":-4,"result":null}`},
		{`{"id":"req-2","result":{"z":1,"a":[true,null]}}`, &JSONRPCResponse{}, `{"id":"req-2","result":{"a":[true,null],"z":1}}`},
	} {
		if err := json.Unmarshal([]byte(tc.input), tc.value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(tc.value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestJSONRPCEnvelopesRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"params":{}}`, `{"method":null}`, `{"method":1}`,
		`{"method":"notice","method":"other"}`, `{"method":"notice"} {}`,
	} {
		assertJSONRejects[JSONRPCNotification](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"method":"run"}`, `{"id":1}`, `{"id":null,"method":"run"}`,
		`{"id":1,"method":null}`, `{"id":1,"method":"run","trace":true}`,
		`{"id":1,"method":"run","id":2}`, `{"id":1,"method":"run","params":{},"params":{}}`,
		`{"id":1,"method":"run"} {}`,
	} {
		assertJSONRejects[JSONRPCRequest](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"id":1}`, `{"result":{}}`, `{"id":null,"result":null}`,
		`{"id":1,"id":2,"result":null}`, `{"id":1,"result":{},"result":null}`,
		`{"id":1,"result":null} {}`,
	} {
		assertJSONRejects[JSONRPCResponse](t, input)
	}
}

func TestJSONRPCEnvelopesRemainStandalone(t *testing.T) {
	var notification *JSONRPCNotification
	if err := notification.UnmarshalJSON([]byte(`{"method":"notice"}`)); err == nil {
		t.Fatal("nil JSON-RPC notification receiver succeeded")
	}
	var request *JSONRPCRequest
	if err := request.UnmarshalJSON([]byte(`{"id":1,"method":"run"}`)); err == nil {
		t.Fatal("nil JSON-RPC request receiver succeeded")
	}
	var response *JSONRPCResponse
	if err := response.UnmarshalJSON([]byte(`{"id":1,"result":null}`)); err == nil {
		t.Fatal("nil JSON-RPC response receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range []string{"JSONRPCNotification", "JSONRPCRequest", "JSONRPCResponse"} {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestJSONRPCEnvelopesTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type JSONRPCNotification = {\n  \"method\": string;\n  \"params\"?: JsonValue;\n};",
		"export type JSONRPCRequest = {\n  \"id\": RequestId;\n  \"method\": string;\n  \"params\"?: JsonValue;\n  \"trace\"?: W3cTraceContext;\n};",
		"export type JSONRPCResponse = {\n  \"id\": RequestId;\n  \"result\": JsonValue;\n};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

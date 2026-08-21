package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestJSONRPCErrorSchemasAreExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	for name, want := range jsonRPCErrorSchemas() {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestJSONRPCErrorsPreserveSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"id":1,"error":{"code":-32601,"message":"missing"}}`, `{"error":{"code":-32601,"message":"missing"},"id":1}`},
		{`{"future":true,"id":"req-1","error":{"message":"failed","data":null,"code":-32001,"extra":true}}`, `{"error":{"code":-32001,"message":"failed"},"id":"req-1"}`},
		{`{"id":-4,"error":{"code":7,"data":{"retry":true,"reasons":["a"]},"message":"failed"}}`, `{"error":{"code":7,"data":{"reasons":["a"],"retry":true},"message":"failed"},"id":-4}`},
	} {
		var value JSONRPCError
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

func TestJSONRPCErrorsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"id":1}`, `{"error":{"code":1,"message":"x"}}`,
		`{"id":null,"error":{"code":1,"message":"x"}}`,
		`{"id":1,"error":{"code":1}}`, `{"id":1,"error":{"message":"x"}}`,
		`{"id":1,"error":{"code":1.5,"message":"x"}}`,
		`{"id":1,"error":{"code":1,"message":null}}`,
		`{"id":1,"id":2,"error":{"code":1,"message":"x"}}`,
		`{"id":1,"error":{"code":1,"code":2,"message":"x"}}`,
		`{"id":1,"error":{"code":1,"message":"x"}} {}`,
	} {
		assertJSONRejects[JSONRPCError](t, input)
	}
}

func TestJSONRPCErrorsRemainStandalone(t *testing.T) {
	var envelope *JSONRPCError
	if err := envelope.UnmarshalJSON([]byte(`{"id":1,"error":{"code":1,"message":"x"}}`)); err == nil {
		t.Fatal("nil JSON-RPC error receiver succeeded")
	}
	var payload *JSONRPCErrorError
	if err := payload.UnmarshalJSON([]byte(`{"code":1,"message":"x"}`)); err == nil {
		t.Fatal("nil JSON-RPC error payload receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range []string{"JSONRPCError", "JSONRPCErrorError"} {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestJSONRPCErrorsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type JSONRPCError = {\n  \"error\": JSONRPCErrorError;\n  \"id\": RequestId;\n};",
		"export type JSONRPCErrorError = {\n  \"code\": number;\n  \"data\"?: JsonValue;\n  \"message\": string;\n};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestJSONRPCMessageSchemaIsExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	if got := definitions["JSONRPCMessage"]; !reflect.DeepEqual(got, jsonRPCMessageSchema()) {
		t.Fatalf("JSONRPCMessage schema = %#v, want %#v", got, jsonRPCMessageSchema())
	}
}

func TestJSONRPCMessagePreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input, variant, want string
	}{
		{`{"id":1,"method":"run","params":{"z":1,"a":[null,true]}}`, "request", `{"id":1,"method":"run","params":{"a":[null,true],"z":1}}`},
		{`{"method":"notice","params":null,"future":true}`, "notification", `{"method":"notice"}`},
		{`{"id":"response","result":{"z":1,"a":[true,null]},"future":true}`, "response", `{"id":"response","result":{"a":[true,null],"z":1}}`},
		{`{"id":-4,"error":{"message":"missing","code":-32601,"future":true},"future":true}`, "error", `{"error":{"code":-32601,"message":"missing"},"id":-4}`},
		{`{"id":1,"method":"run","result":{"ignored":true}}`, "request", `{"id":1,"method":"run"}`},
		{`{"id":1,"id":2,"method":"notice"}`, "notification", `{"method":"notice"}`},
	} {
		var value JSONRPCMessage
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		if got := jsonRPCMessageVariant(value); got != tc.variant {
			t.Errorf("variant for %s = %s, want %s", tc.input, got, tc.variant)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestJSONRPCMessageRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"id":1}`, `{"result":null}`, `{"error":{"code":1,"message":"x"}}`,
		`{"method":null}`, `{"method":"one","method":"two"}`,
		`{"id":1,"method":"run"} {}`,
	} {
		assertJSONRejects[JSONRPCMessage](t, input)
	}
}

func TestJSONRPCMessageRejectsInvalidConstructedVariants(t *testing.T) {
	if _, err := json.Marshal(JSONRPCMessage{}); err == nil {
		t.Fatal("empty JSON-RPC message marshaled")
	}
	if _, err := json.Marshal(JSONRPCMessage{
		Request:      &JSONRPCRequest{ID: NewNumberID(1), Method: "run"},
		Notification: &JSONRPCNotification{Method: "notice"},
	}); err == nil {
		t.Fatal("multi-variant JSON-RPC message marshaled")
	}
}

func TestJSONRPCMessageRemainsStandalone(t *testing.T) {
	var message *JSONRPCMessage
	if err := message.UnmarshalJSON([]byte(`{"method":"notice"}`)); err == nil {
		t.Fatal("nil JSON-RPC message receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "JSONRPCMessage") || slices.Contains(binding.Result, "JSONRPCMessage") {
			t.Fatalf("JSONRPCMessage unexpectedly bound to %s", binding.Method)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestJSONRPCMessageTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type JSONRPCMessage = JSONRPCRequest | JSONRPCNotification | JSONRPCResponse | JSONRPCError;"
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func jsonRPCMessageVariant(message JSONRPCMessage) string {
	switch {
	case message.Request != nil:
		return "request"
	case message.Notification != nil:
		return "notification"
	case message.Response != nil:
		return "response"
	case message.Error != nil:
		return "error"
	default:
		return ""
	}
}

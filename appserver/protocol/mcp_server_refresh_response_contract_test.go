package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMcpServerRefreshResponseSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["McpServerRefreshResponse"].(Schema)
	if !ok {
		t.Fatal("$defs missing McpServerRefreshResponse")
	}
	want := Schema{
		"type":                 "object",
		"additionalProperties": false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("McpServerRefreshResponse = %#v, want %#v", got, want)
	}
}

func TestMcpServerRefreshResponseAcceptsAndCanonicalizesObjects(t *testing.T) {
	for _, input := range []string{
		`{}`,
		`{"future":true}`,
		`{"nested":{"value":[1,null,"two"]}}`,
		`{"future":1,"future":2}`,
	} {
		var response McpServerRefreshResponse
		if err := json.Unmarshal([]byte(input), &response); err != nil {
			t.Errorf("Unmarshal(%s): %v", input, err)
			continue
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Errorf("Marshal(%s): %v", input, err)
			continue
		}
		if string(encoded) != `{}` {
			t.Errorf("round trip %s = %s, want {}", input, encoded)
		}
	}
}

func TestMcpServerRefreshResponseRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{`, `{} {}`,
	} {
		assertJSONRejects[McpServerRefreshResponse](t, input)
	}

	var response *McpServerRefreshResponse
	if err := response.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil McpServerRefreshResponse receiver succeeded")
	}
}

func TestMcpServerRefreshResponseRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "McpServerRefreshResponse") ||
			slices.Contains(binding.Result, "McpServerRefreshResponse") {
			t.Fatalf("McpServerRefreshResponse unexpectedly bound to %s", binding.Method)
		}
	}
	info, ok := LookupMethod("config/mcpServer/reload")
	if !ok || info.State != MethodImplemented {
		t.Fatalf("config/mcpServer/reload = %#v, %v; want implemented", info, ok)
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

func TestMcpServerRefreshResponseTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type McpServerRefreshResponse = Record<string, never>;`
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}

var _ json.Unmarshaler = (*McpServerRefreshResponse)(nil)

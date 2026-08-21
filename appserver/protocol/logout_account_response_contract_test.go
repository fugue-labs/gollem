package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestLogoutAccountResponseSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["LogoutAccountResponse"].(Schema)
	if !ok {
		t.Fatal("$defs missing LogoutAccountResponse")
	}
	want := Schema{
		"type":                 "object",
		"additionalProperties": false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LogoutAccountResponse = %#v, want %#v", got, want)
	}
}

func TestLogoutAccountResponseAcceptsAndCanonicalizesObjects(t *testing.T) {
	for _, input := range []string{
		`{}`,
		`{"future":true}`,
		`{"nested":{"value":[1,null,"two"]}}`,
		`{"future":1,"future":2}`,
	} {
		var response LogoutAccountResponse
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

func TestLogoutAccountResponseRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{`, `{} {}`,
	} {
		assertJSONRejects[LogoutAccountResponse](t, input)
	}

	var response *LogoutAccountResponse
	if err := response.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil LogoutAccountResponse receiver succeeded")
	}
}

func TestLogoutAccountResponseRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "LogoutAccountResponse") ||
			slices.Contains(binding.Result, "LogoutAccountResponse") {
			t.Fatalf("LogoutAccountResponse unexpectedly bound to %s", binding.Method)
		}
	}
	info, ok := LookupMethod("account/logout")
	if !ok || info.State != MethodDeferredStub {
		t.Fatalf("account/logout = %#v, %v; want deferred stub", info, ok)
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

func TestLogoutAccountResponseTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type LogoutAccountResponse = Record<string, never>;`
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}

var _ json.Unmarshaler = (*LogoutAccountResponse)(nil)

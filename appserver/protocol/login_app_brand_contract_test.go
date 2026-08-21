package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestLoginAppBrandSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(t, defs["LoginAppBrand"], "codex", "chatgpt")
}

func TestLoginAppBrandAcceptsExactValuesAndDefault(t *testing.T) {
	for _, input := range []string{`"codex"`, `"chatgpt"`} {
		var brand LoginAppBrand
		if err := json.Unmarshal([]byte(input), &brand); err != nil {
			t.Fatalf("unmarshal LoginAppBrand %s: %v", input, err)
		}
		encoded, err := json.Marshal(brand)
		if err != nil {
			t.Fatalf("marshal LoginAppBrand %s: %v", input, err)
		}
		if got := string(encoded); got != input {
			t.Fatalf("LoginAppBrand round trip = %s, want %s", got, input)
		}
	}
	if LoginAppBrandDefault != LoginAppBrandCodex {
		t.Fatalf("LoginAppBrandDefault = %q, want %q", LoginAppBrandDefault, LoginAppBrandCodex)
	}
}

func TestLoginAppBrandRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"Codex"`, `"ChatGPT"`,
		`1`, `true`, `{}`, `[]`, `"codex" {}`, `"chatgpt" x`,
	} {
		assertJSONRejects[LoginAppBrand](t, input)
	}
}

func TestLoginAppBrandNilReceiverAndInvalidMarshalFailClosed(t *testing.T) {
	var brand *LoginAppBrand
	if err := brand.UnmarshalJSON([]byte(`"codex"`)); err == nil {
		t.Fatal("nil LoginAppBrand receiver succeeded")
	}
	if _, err := json.Marshal(LoginAppBrand("other")); err == nil {
		t.Fatal("invalid LoginAppBrand marshaled")
	}
}

func TestLoginAppBrandRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "LoginAppBrand") ||
			slices.Contains(binding.Result, "LoginAppBrand") {
			t.Fatalf("LoginAppBrand unexpectedly bound to %s", binding.Method)
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

func TestLoginAppBrandTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type LoginAppBrand = "codex" | "chatgpt";`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = LoginAppBrand("")
	_ json.Unmarshaler = (*LoginAppBrand)(nil)
)

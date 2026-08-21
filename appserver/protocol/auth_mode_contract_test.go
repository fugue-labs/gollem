package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAuthModeSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(
		t,
		defs["AuthMode"],
		"apikey",
		"chatgpt",
		"chatgptAuthTokens",
		"headers",
		"agentIdentity",
		"personalAccessToken",
		"bedrockApiKey",
	)
}

func TestAuthModeAcceptsExactValues(t *testing.T) {
	for _, input := range []string{
		`"apikey"`,
		`"chatgpt"`,
		`"chatgptAuthTokens"`,
		`"headers"`,
		`"agentIdentity"`,
		`"personalAccessToken"`,
		`"bedrockApiKey"`,
	} {
		var mode AuthMode
		if err := json.Unmarshal([]byte(input), &mode); err != nil {
			t.Fatalf("unmarshal AuthMode %s: %v", input, err)
		}
		encoded, err := json.Marshal(mode)
		if err != nil {
			t.Fatalf("marshal AuthMode %s: %v", input, err)
		}
		if got := string(encoded); got != input {
			t.Fatalf("AuthMode round trip = %s, want %s", got, input)
		}
	}
}

func TestAuthModeRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"apiKey"`, `"ChatGPT"`,
		`"chatgptauthtokens"`, `"agentidentity"`, `"personalaccesstoken"`,
		`"bedrockapikey"`, `1`, `true`, `{}`, `[]`, `"apikey" {}`,
		`"bedrockApiKey" x`,
	} {
		assertJSONRejects[AuthMode](t, input)
	}
}

func TestAuthModeNilReceiverAndInvalidMarshalFailClosed(t *testing.T) {
	var mode *AuthMode
	if err := mode.UnmarshalJSON([]byte(`"apikey"`)); err == nil {
		t.Fatal("nil AuthMode receiver succeeded")
	}
	if _, err := json.Marshal(AuthMode("other")); err == nil {
		t.Fatal("invalid AuthMode marshaled")
	}
}

func TestAuthModeRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "AuthMode") ||
			slices.Contains(binding.Result, "AuthMode") {
			t.Fatalf("AuthMode unexpectedly bound to %s", binding.Method)
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

func TestAuthModeTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type AuthMode = "apikey" | "chatgpt" | "chatgptAuthTokens" | "headers" | "agentIdentity" | "personalAccessToken" | "bedrockApiKey";`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = AuthMode("")
	_ json.Unmarshaler = (*AuthMode)(nil)
)

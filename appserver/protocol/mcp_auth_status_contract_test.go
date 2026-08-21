package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestMcpAuthStatusSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(
		t,
		defs["McpAuthStatus"],
		"unsupported",
		"notLoggedIn",
		"bearerToken",
		"oAuth",
	)
}

func TestMcpAuthStatusAcceptsExactValues(t *testing.T) {
	tests := map[string]McpAuthStatus{
		`"unsupported"`: McpAuthStatusUnsupported,
		`"notLoggedIn"`: McpAuthStatusNotLoggedIn,
		`"bearerToken"`: McpAuthStatusBearerToken,
		`"oAuth"`:       McpAuthStatusOAuth,
	}
	for input, want := range tests {
		var status McpAuthStatus
		if err := json.Unmarshal([]byte(input), &status); err != nil {
			t.Fatalf("unmarshal McpAuthStatus %s: %v", input, err)
		}
		if status != want {
			t.Fatalf("McpAuthStatus = %q, want %q", status, want)
		}
		encoded, err := json.Marshal(status)
		if err != nil {
			t.Fatalf("marshal McpAuthStatus %s: %v", input, err)
		}
		if got := string(encoded); got != input {
			t.Fatalf("McpAuthStatus round trip = %s, want %s", got, input)
		}
	}
}

func TestMcpAuthStatusRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"Unsupported"`, `"not_logged_in"`,
		`"bearer_token"`, `"oauth"`, `"OAuth"`, `1`, `true`, `{}`, `[]`,
		`"unsupported" {}`, `"oAuth" x`,
	} {
		assertJSONRejects[McpAuthStatus](t, input)
	}
}

func TestMcpAuthStatusNilReceiverAndInvalidMarshalFailClosed(t *testing.T) {
	var status *McpAuthStatus
	if err := status.UnmarshalJSON([]byte(`"unsupported"`)); err == nil {
		t.Fatal("nil McpAuthStatus receiver succeeded")
	}
	if _, err := json.Marshal(McpAuthStatus("")); err == nil {
		t.Fatal("zero McpAuthStatus marshaled; upstream v2 has no default")
	}
	if _, err := json.Marshal(McpAuthStatus("other")); err == nil {
		t.Fatal("invalid McpAuthStatus marshaled")
	}
}

func TestMcpAuthStatusRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "McpAuthStatus") ||
			slices.Contains(binding.Result, "McpAuthStatus") {
			t.Fatalf("McpAuthStatus unexpectedly bound to %s", binding.Method)
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

func TestMcpAuthStatusTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type McpAuthStatus = "unsupported" | "notLoggedIn" | "bearerToken" | "oAuth";`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = McpAuthStatus("")
	_ json.Unmarshaler = (*McpAuthStatus)(nil)
)

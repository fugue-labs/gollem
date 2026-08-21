package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAppTemplateUnavailableReasonSchemaIsExact(t *testing.T) {
	assertStringEnum(
		t,
		JSONSchema()["$defs"].(Schema)["AppTemplateUnavailableReason"],
		"NOT_CONFIGURED_FOR_WORKSPACE",
		"NO_ACTIVE_WORKSPACE",
	)
}

func TestAppTemplateUnavailableReasonAcceptsExactValues(t *testing.T) {
	values := map[string]AppTemplateUnavailableReason{
		`"NOT_CONFIGURED_FOR_WORKSPACE"`: AppTemplateUnavailableReasonNotConfiguredForWorkspace,
		`"NO_ACTIVE_WORKSPACE"`:          AppTemplateUnavailableReasonNoActiveWorkspace,
	}
	for input, want := range values {
		var value AppTemplateUnavailableReason
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Fatalf("unmarshal %s: %v", input, err)
		}
		if value != want {
			t.Fatalf("AppTemplateUnavailableReason = %q, want %q", value, want)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != input {
			t.Fatalf("round trip %s = %s, %v", input, encoded, err)
		}
	}
}

func TestAppTemplateUnavailableReasonRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"not_configured_for_workspace"`,
		`"NotConfiguredForWorkspace"`, `"NOT_CONFIGURED_FOR_WORKSPACE "`,
		`" NO_ACTIVE_WORKSPACE"`, `"NO_ACTIVE_WORKSPACE\n"`, `1`, `true`, `{}`, `[]`,
		`"NOT_CONFIGURED_FOR_WORKSPACE" {}`, `"NO_ACTIVE_WORKSPACE" x`,
	} {
		assertJSONRejects[AppTemplateUnavailableReason](t, input)
	}
}

func TestAppTemplateUnavailableReasonNilReceiverAndInvalidMarshalFailClosed(t *testing.T) {
	var reason *AppTemplateUnavailableReason
	if err := reason.UnmarshalJSON([]byte(`"NO_ACTIVE_WORKSPACE"`)); err == nil {
		t.Fatal("nil AppTemplateUnavailableReason receiver succeeded")
	}
	for _, value := range []AppTemplateUnavailableReason{"", "other"} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatalf("invalid AppTemplateUnavailableReason %q marshaled", value)
		}
	}
}

func TestAppTemplateUnavailableReasonRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "AppTemplateUnavailableReason") ||
			slices.Contains(binding.Result, "AppTemplateUnavailableReason") {
			t.Fatalf("AppTemplateUnavailableReason unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "AppTemplateUnavailableReason" {
			t.Fatalf("AppTemplateUnavailableReason unexpectedly bound to item %s", binding.Kind)
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

func TestAppTemplateUnavailableReasonTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type AppTemplateUnavailableReason = "NOT_CONFIGURED_FOR_WORKSPACE" | "NO_ACTIVE_WORKSPACE";`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = AppTemplateUnavailableReason("")
	_ json.Unmarshaler = (*AppTemplateUnavailableReason)(nil)
)

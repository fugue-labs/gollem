package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAppToolApprovalSchemaIsExact(t *testing.T) {
	assertStringEnum(
		t,
		JSONSchema()["$defs"].(Schema)["AppToolApproval"],
		"auto",
		"prompt",
		"writes",
		"approve",
	)
}

func TestAppToolApprovalAcceptsExactValues(t *testing.T) {
	values := map[string]AppToolApproval{
		`"auto"`:    AppToolApprovalAuto,
		`"prompt"`:  AppToolApprovalPrompt,
		`"writes"`:  AppToolApprovalWrites,
		`"approve"`: AppToolApprovalApprove,
	}
	for input, want := range values {
		var value AppToolApproval
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Fatalf("unmarshal %s: %v", input, err)
		}
		if value != want {
			t.Fatalf("AppToolApproval = %q, want %q", value, want)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != input {
			t.Fatalf("round trip %s = %s, %v", input, encoded, err)
		}
	}
}

func TestAppToolApprovalRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"AUTO"`, `"Prompt"`, `"auto_approve"`,
		`"write"`, `"writes "`, `" approve"`, `"approve\n"`, `1`, `true`, `{}`, `[]`,
		`"auto" {}`, `"prompt" x`,
	} {
		assertJSONRejects[AppToolApproval](t, input)
	}
}

func TestAppToolApprovalNilReceiverAndInvalidMarshalFailClosed(t *testing.T) {
	var approval *AppToolApproval
	if err := approval.UnmarshalJSON([]byte(`"auto"`)); err == nil {
		t.Fatal("nil AppToolApproval receiver succeeded")
	}
	for _, value := range []AppToolApproval{"", "other", "AUTO"} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatalf("invalid AppToolApproval %q marshaled", value)
		}
	}
}

func TestAppToolApprovalRemainsStandaloneAndUnbound(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "AppToolApproval") || slices.Contains(binding.Result, "AppToolApproval") {
			t.Fatalf("AppToolApproval unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "AppToolApproval" {
			t.Fatalf("AppToolApproval unexpectedly bound to item %s", binding.Kind)
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

func TestAppToolApprovalTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type AppToolApproval = "auto" | "prompt" | "writes" | "approve";`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = AppToolApproval("")
	_ json.Unmarshaler = (*AppToolApproval)(nil)
)

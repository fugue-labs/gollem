package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAccountCreditNudgeValueSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(
		t,
		defs["AddCreditsNudgeCreditType"],
		"credits",
		"usage_limit",
	)
	assertStringEnum(
		t,
		defs["AddCreditsNudgeEmailStatus"],
		"sent",
		"cooldown_active",
	)
}

func TestAccountCreditNudgeValuesAcceptExactLiterals(t *testing.T) {
	creditTypes := map[string]AddCreditsNudgeCreditType{
		`"credits"`:     AddCreditsNudgeCreditTypeCredits,
		`"usage_limit"`: AddCreditsNudgeCreditTypeUsageLimit,
	}
	for input, want := range creditTypes {
		var value AddCreditsNudgeCreditType
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Fatalf("unmarshal AddCreditsNudgeCreditType %s: %v", input, err)
		}
		if value != want {
			t.Fatalf("AddCreditsNudgeCreditType = %q, want %q", value, want)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != input {
			t.Fatalf("AddCreditsNudgeCreditType round trip = %s, %v; want %s", encoded, err, input)
		}
	}

	statuses := map[string]AddCreditsNudgeEmailStatus{
		`"sent"`:            AddCreditsNudgeEmailStatusSent,
		`"cooldown_active"`: AddCreditsNudgeEmailStatusCooldownActive,
	}
	for input, want := range statuses {
		var value AddCreditsNudgeEmailStatus
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Fatalf("unmarshal AddCreditsNudgeEmailStatus %s: %v", input, err)
		}
		if value != want {
			t.Fatalf("AddCreditsNudgeEmailStatus = %q, want %q", value, want)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != input {
			t.Fatalf("AddCreditsNudgeEmailStatus round trip = %s, %v; want %s", encoded, err, input)
		}
	}
}

func TestAccountCreditNudgeValuesRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"sent"`, `"Credits"`, `"usageLimit"`,
		`"USAGE_LIMIT"`, `1`, `true`, `{}`, `[]`, `"credits" {}`,
		`"usage_limit" x`,
	} {
		assertJSONRejects[AddCreditsNudgeCreditType](t, input)
	}

	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"credits"`, `"Sent"`, `"cooldownActive"`,
		`"COOLDOWN_ACTIVE"`, `1`, `true`, `{}`, `[]`, `"sent" {}`,
		`"cooldown_active" x`,
	} {
		assertJSONRejects[AddCreditsNudgeEmailStatus](t, input)
	}
}

func TestAccountCreditNudgeValuesNilReceiversAndInvalidMarshalFailClosed(t *testing.T) {
	var creditType *AddCreditsNudgeCreditType
	if err := creditType.UnmarshalJSON([]byte(`"credits"`)); err == nil {
		t.Fatal("nil AddCreditsNudgeCreditType receiver succeeded")
	}
	for _, value := range []AddCreditsNudgeCreditType{"", "other"} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatalf("invalid AddCreditsNudgeCreditType %q marshaled", value)
		}
	}

	var status *AddCreditsNudgeEmailStatus
	if err := status.UnmarshalJSON([]byte(`"sent"`)); err == nil {
		t.Fatal("nil AddCreditsNudgeEmailStatus receiver succeeded")
	}
	for _, value := range []AddCreditsNudgeEmailStatus{"", "other"} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatalf("invalid AddCreditsNudgeEmailStatus %q marshaled", value)
		}
	}
}

func TestAccountCreditNudgeValuesRemainStandaloneAndDeferred(t *testing.T) {
	names := []string{"AddCreditsNudgeCreditType", "AddCreditsNudgeEmailStatus"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if slices.Contains(names, binding.Type) {
			t.Fatalf("%s unexpectedly bound to item %s", binding.Type, binding.Kind)
		}
	}
	method, ok := LookupMethod("account/sendAddCreditsNudgeEmail")
	if !ok || method.Surface != SurfaceClientRequest || method.State != MethodDeferredStub {
		t.Fatalf("account/sendAddCreditsNudgeEmail = %#v, %v; want deferred client request", method, ok)
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

func TestAccountCreditNudgeValuesTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type AddCreditsNudgeCreditType = "credits" | "usage_limit";`,
		`export type AddCreditsNudgeEmailStatus = "sent" | "cooldown_active";`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = AddCreditsNudgeCreditType("")
	_ json.Unmarshaler = (*AddCreditsNudgeCreditType)(nil)
	_ json.Marshaler   = AddCreditsNudgeEmailStatus("")
	_ json.Unmarshaler = (*AddCreditsNudgeEmailStatus)(nil)
)

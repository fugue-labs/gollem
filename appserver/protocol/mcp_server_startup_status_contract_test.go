package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestMcpServerStartupStatusSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(
		t,
		defs["McpServerStartupFailureReason"],
		"reauthenticationRequired",
	)
	assertStringEnum(
		t,
		defs["McpServerStartupState"],
		"starting",
		"ready",
		"failed",
		"cancelled",
	)
}

func TestMcpServerStartupFailureReasonAcceptsExactValue(t *testing.T) {
	const input = `"reauthenticationRequired"`
	var reason McpServerStartupFailureReason
	if err := json.Unmarshal([]byte(input), &reason); err != nil {
		t.Fatalf("unmarshal McpServerStartupFailureReason: %v", err)
	}
	if reason != McpServerStartupFailureReasonReauthenticationRequired {
		t.Fatalf("McpServerStartupFailureReason = %q", reason)
	}
	encoded, err := json.Marshal(reason)
	if err != nil {
		t.Fatalf("marshal McpServerStartupFailureReason: %v", err)
	}
	if got := string(encoded); got != input {
		t.Fatalf("McpServerStartupFailureReason round trip = %s, want %s", got, input)
	}
}

func TestMcpServerStartupStateAcceptsExactValues(t *testing.T) {
	tests := map[string]McpServerStartupState{
		`"starting"`:  McpServerStartupStateStarting,
		`"ready"`:     McpServerStartupStateReady,
		`"failed"`:    McpServerStartupStateFailed,
		`"cancelled"`: McpServerStartupStateCancelled,
	}
	for input, want := range tests {
		var state McpServerStartupState
		if err := json.Unmarshal([]byte(input), &state); err != nil {
			t.Fatalf("unmarshal McpServerStartupState %s: %v", input, err)
		}
		if state != want {
			t.Fatalf("McpServerStartupState = %q, want %q", state, want)
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("marshal McpServerStartupState %s: %v", input, err)
		}
		if got := string(encoded); got != input {
			t.Fatalf("McpServerStartupState round trip = %s, want %s", got, input)
		}
	}
}

func TestMcpServerStartupStatusRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"ReauthenticationRequired"`,
		`"reauthentication_required"`, `1`, `true`, `{}`, `[]`,
		`"reauthenticationRequired" {}`, `"reauthenticationRequired" x`,
	} {
		assertJSONRejects[McpServerStartupFailureReason](t, input)
	}

	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"Starting"`, `"inProgress"`,
		`"canceled"`, `"CANCELLED"`, `1`, `true`, `{}`, `[]`,
		`"starting" {}`, `"cancelled" x`,
	} {
		assertJSONRejects[McpServerStartupState](t, input)
	}
}

func TestMcpServerStartupStatusNilReceiversAndInvalidMarshalFailClosed(t *testing.T) {
	var reason *McpServerStartupFailureReason
	if err := reason.UnmarshalJSON([]byte(`"reauthenticationRequired"`)); err == nil {
		t.Fatal("nil McpServerStartupFailureReason receiver succeeded")
	}
	for _, value := range []McpServerStartupFailureReason{"", "other"} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatalf("invalid McpServerStartupFailureReason %q marshaled", value)
		}
	}

	var state *McpServerStartupState
	if err := state.UnmarshalJSON([]byte(`"starting"`)); err == nil {
		t.Fatal("nil McpServerStartupState receiver succeeded")
	}
	for _, value := range []McpServerStartupState{"", "other"} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatalf("invalid McpServerStartupState %q marshaled", value)
		}
	}
}

func TestMcpServerStartupStatusRemainsStandalone(t *testing.T) {
	names := []string{"McpServerStartupFailureReason", "McpServerStartupState"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
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

func TestMcpServerStartupStatusTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type McpServerStartupFailureReason = "reauthenticationRequired";`,
		`export type McpServerStartupState = "starting" | "ready" | "failed" | "cancelled";`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = McpServerStartupFailureReason("")
	_ json.Unmarshaler = (*McpServerStartupFailureReason)(nil)
	_ json.Marshaler   = McpServerStartupState("")
	_ json.Unmarshaler = (*McpServerStartupState)(nil)
)

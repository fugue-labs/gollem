package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestGuardianEnumFoundationSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(t, defs["GuardianApprovalReviewStatus"],
		"inProgress", "approved", "denied", "timedOut", "aborted")
	assertStringEnum(t, defs["GuardianCommandSource"], "shell", "unifiedExec")
	assertStringEnum(t, defs["GuardianRiskLevel"], "low", "medium", "high", "critical")
	assertStringEnum(t, defs["GuardianUserAuthorization"], "unknown", "low", "medium", "high")
	assertStringEnum(t, defs["NetworkApprovalProtocol"], "http", "https", "socks5Tcp", "socks5Udp")
}

func TestGuardianEnumFoundationAcceptsExactValues(t *testing.T) {
	assertEnumRoundTrips(t, []GuardianApprovalReviewStatus{
		GuardianApprovalReviewStatusInProgress, GuardianApprovalReviewStatusApproved,
		GuardianApprovalReviewStatusDenied, GuardianApprovalReviewStatusTimedOut,
		GuardianApprovalReviewStatusAborted,
	})
	assertEnumRoundTrips(t, []GuardianCommandSource{
		GuardianCommandSourceShell, GuardianCommandSourceUnifiedExec,
	})
	assertEnumRoundTrips(t, []GuardianRiskLevel{
		GuardianRiskLevelLow, GuardianRiskLevelMedium,
		GuardianRiskLevelHigh, GuardianRiskLevelCritical,
	})
	assertEnumRoundTrips(t, []GuardianUserAuthorization{
		GuardianUserAuthorizationUnknown, GuardianUserAuthorizationLow,
		GuardianUserAuthorizationMedium, GuardianUserAuthorizationHigh,
	})
	assertEnumRoundTrips(t, []NetworkApprovalProtocol{
		NetworkApprovalProtocolHTTP, NetworkApprovalProtocolHTTPS,
		NetworkApprovalProtocolSocks5TCP, NetworkApprovalProtocolSocks5UDP,
	})
}

func TestGuardianEnumFoundationRejectsMalformedWireForms(t *testing.T) {
	common := []string{``, `null`, `""`, `1`, `true`, `{}`, `[]`, `"low" {}`, `"low" x`}
	assertEnumRejects[GuardianApprovalReviewStatus](t,
		append(common, `"in_progress"`, `"InProgress"`, `"other"`)...)
	assertEnumRejects[GuardianCommandSource](t,
		append(common, `"unified_exec"`, `"UnifiedExec"`, `"other"`)...)
	assertEnumRejects[GuardianRiskLevel](t,
		append(common, `"Low"`, `"other"`)...)
	assertEnumRejects[GuardianUserAuthorization](t,
		append(common, `"Unknown"`, `"other"`)...)
	assertEnumRejects[NetworkApprovalProtocol](t,
		append(common, `"socks5_tcp"`, `"Socks5Tcp"`, `"other"`)...)
}

func TestGuardianEnumFoundationNilReceiversAndInvalidValuesFailClosed(t *testing.T) {
	receivers := []json.Unmarshaler{
		(*GuardianApprovalReviewStatus)(nil), (*GuardianCommandSource)(nil),
		(*GuardianRiskLevel)(nil), (*GuardianUserAuthorization)(nil),
		(*NetworkApprovalProtocol)(nil),
	}
	for _, receiver := range receivers {
		if err := receiver.UnmarshalJSON([]byte(`"low"`)); err == nil {
			t.Fatalf("nil %T receiver succeeded", receiver)
		}
	}
	invalid := []json.Marshaler{
		GuardianApprovalReviewStatus("other"), GuardianCommandSource("other"),
		GuardianRiskLevel("other"), GuardianUserAuthorization("other"),
		NetworkApprovalProtocol("other"),
	}
	for _, value := range invalid {
		if _, err := value.MarshalJSON(); err == nil {
			t.Fatalf("invalid %T marshaled", value)
		}
	}
}

func TestGuardianEnumFoundationRemainsStandalone(t *testing.T) {
	names := []string{
		"GuardianApprovalReviewStatus", "GuardianCommandSource", "GuardianRiskLevel",
		"GuardianUserAuthorization", "NetworkApprovalProtocol",
	}
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

func TestGuardianEnumFoundationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type GuardianApprovalReviewStatus = "inProgress" | "approved" | "denied" | "timedOut" | "aborted";`,
		`export type GuardianCommandSource = "shell" | "unifiedExec";`,
		`export type GuardianRiskLevel = "low" | "medium" | "high" | "critical";`,
		`export type GuardianUserAuthorization = "unknown" | "low" | "medium" | "high";`,
		`export type NetworkApprovalProtocol = "http" | "https" | "socks5Tcp" | "socks5Udp";`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertEnumRoundTrips[T ~string](t *testing.T, values []T) {
	t.Helper()
	for _, want := range values {
		data, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("marshal %T(%q): %v", want, want, err)
		}
		var got T
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal %T(%q): %v", want, want, err)
		}
		if got != want {
			t.Fatalf("round trip %T = %q, want %q", want, got, want)
		}
	}
}

func assertEnumRejects[T ~string](t *testing.T, inputs ...string) {
	t.Helper()
	for _, input := range inputs {
		assertJSONRejects[T](t, input)
	}
}

var (
	_ json.Marshaler   = GuardianApprovalReviewStatus("")
	_ json.Unmarshaler = (*GuardianApprovalReviewStatus)(nil)
	_ json.Marshaler   = GuardianCommandSource("")
	_ json.Unmarshaler = (*GuardianCommandSource)(nil)
	_ json.Marshaler   = GuardianRiskLevel("")
	_ json.Unmarshaler = (*GuardianRiskLevel)(nil)
	_ json.Marshaler   = GuardianUserAuthorization("")
	_ json.Unmarshaler = (*GuardianUserAuthorization)(nil)
	_ json.Marshaler   = NetworkApprovalProtocol("")
	_ json.Unmarshaler = (*NetworkApprovalProtocol)(nil)
)

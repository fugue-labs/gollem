package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestHookValueFoundationSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(t, defs["HookEventName"],
		"preToolUse", "permissionRequest", "postToolUse", "preCompact", "postCompact",
		"sessionStart", "userPromptSubmit", "subagentStart", "subagentStop", "stop",
	)
	assertStringEnum(t, defs["HookExecutionMode"], "sync", "async")
	assertStringEnum(t, defs["HookHandlerType"], "command", "prompt", "agent")
	assertStringEnum(t, defs["HookOutputEntryKind"], "warning", "stop", "feedback", "context", "error")
	assertStringEnum(t, defs["HookRunStatus"], "running", "completed", "failed", "blocked", "stopped")
	assertStringEnum(t, defs["HookScope"], "thread", "turn")
	assertStringEnum(t, defs["HookSource"],
		"system", "user", "project", "mdm", "sessionFlags", "plugin",
		"cloudRequirements", "cloudManagedConfig", "legacyManagedConfigFile",
		"legacyManagedConfigMdm", "unknown",
	)
	assertStringEnum(t, defs["HookTrustStatus"], "managed", "untrusted", "trusted", "modified")

	wantEntry := closedThreadSessionParamSchema(Schema{
		"kind": Schema{"$ref": "#/$defs/HookOutputEntryKind"},
		"text": Schema{"type": "string"},
	}, []string{"kind", "text"})
	if got, ok := defs["HookOutputEntry"].(Schema); !ok || !reflect.DeepEqual(got, wantEntry) {
		t.Fatalf("HookOutputEntry schema = %#v, %v; want %#v", got, ok, wantEntry)
	}
}

func TestHookValueFoundationEnumsAcceptExactValues(t *testing.T) {
	assertHookEnumValues(t, []HookEventName{
		HookEventNamePreToolUse, HookEventNamePermissionRequest, HookEventNamePostToolUse,
		HookEventNamePreCompact, HookEventNamePostCompact, HookEventNameSessionStart,
		HookEventNameUserPromptSubmit, HookEventNameSubagentStart,
		HookEventNameSubagentStop, HookEventNameStop,
	})
	assertHookEnumValues(t, []HookExecutionMode{HookExecutionModeSync, HookExecutionModeAsync})
	assertHookEnumValues(t, []HookHandlerType{HookHandlerTypeCommand, HookHandlerTypePrompt, HookHandlerTypeAgent})
	assertHookEnumValues(t, []HookOutputEntryKind{
		HookOutputEntryKindWarning, HookOutputEntryKindStop, HookOutputEntryKindFeedback,
		HookOutputEntryKindContext, HookOutputEntryKindError,
	})
	assertHookEnumValues(t, []HookRunStatus{
		HookRunStatusRunning, HookRunStatusCompleted, HookRunStatusFailed,
		HookRunStatusBlocked, HookRunStatusStopped,
	})
	assertHookEnumValues(t, []HookScope{HookScopeThread, HookScopeTurn})
	assertHookEnumValues(t, []HookSource{
		HookSourceSystem, HookSourceUser, HookSourceProject, HookSourceMDM,
		HookSourceSessionFlags, HookSourcePlugin, HookSourceCloudRequirements,
		HookSourceCloudManagedConfig, HookSourceLegacyManagedConfigFile,
		HookSourceLegacyManagedConfigMDM, HookSourceUnknown,
	})
	assertHookEnumValues(t, []HookTrustStatus{
		HookTrustStatusManaged, HookTrustStatusUntrusted,
		HookTrustStatusTrusted, HookTrustStatusModified,
	})
}

func TestHookValueFoundationEnumsRejectMalformedValues(t *testing.T) {
	common := []string{``, `null`, `""`, `1`, `true`, `{}`, `[]`, `"value" {}`}
	assertHookEnumRejects[HookEventName](t, append(common, `"PreToolUse"`, `"pre_tool_use"`, `"other"`)...)
	assertHookEnumRejects[HookExecutionMode](t, append(common, `"Sync"`, `"other"`)...)
	assertHookEnumRejects[HookHandlerType](t, append(common, `"Command"`, `"other"`)...)
	assertHookEnumRejects[HookOutputEntryKind](t, append(common, `"Warning"`, `"other"`)...)
	assertHookEnumRejects[HookRunStatus](t, append(common, `"Running"`, `"other"`)...)
	assertHookEnumRejects[HookScope](t, append(common, `"Thread"`, `"other"`)...)
	assertHookEnumRejects[HookSource](t, append(common, `"session_flags"`, `"SessionFlags"`, `"other"`)...)
	assertHookEnumRejects[HookTrustStatus](t, append(common, `"Managed"`, `"other"`)...)
}

func TestHookValueFoundationNilReceiversAndInvalidMarshalFailClosed(t *testing.T) {
	nilReceivers := []json.Unmarshaler{
		(*HookEventName)(nil), (*HookExecutionMode)(nil), (*HookHandlerType)(nil),
		(*HookOutputEntryKind)(nil), (*HookRunStatus)(nil), (*HookScope)(nil),
		(*HookSource)(nil), (*HookTrustStatus)(nil),
	}
	for _, receiver := range nilReceivers {
		if err := receiver.UnmarshalJSON([]byte(`"value"`)); err == nil {
			t.Errorf("nil %T receiver succeeded", receiver)
		}
	}
	invalid := []any{
		HookEventName(""), HookEventName("other"),
		HookExecutionMode(""), HookExecutionMode("other"),
		HookHandlerType(""), HookHandlerType("other"),
		HookOutputEntryKind(""), HookOutputEntryKind("other"),
		HookRunStatus(""), HookRunStatus("other"),
		HookScope(""), HookScope("other"),
		HookSource(""), HookSource("other"),
		HookTrustStatus(""), HookTrustStatus("other"),
	}
	for _, value := range invalid {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid %T %q marshaled", value, value)
		}
	}
}

func TestHookOutputEntryAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		input string
		want  HookOutputEntry
		wire  string
	}{
		{`{"kind":"warning","text":"","future":true}`, HookOutputEntry{Kind: HookOutputEntryKindWarning, Text: ""}, `{"kind":"warning","text":""}`},
		{`{"kind":"stop","text":" stop "}`, HookOutputEntry{Kind: HookOutputEntryKindStop, Text: " stop "}, `{"kind":"stop","text":" stop "}`},
		{`{"kind":"feedback","text":"same"}`, HookOutputEntry{Kind: HookOutputEntryKindFeedback, Text: "same"}, `{"kind":"feedback","text":"same"}`},
		{`{"kind":"context","text":"same"}`, HookOutputEntry{Kind: HookOutputEntryKindContext, Text: "same"}, `{"kind":"context","text":"same"}`},
		{`{"kind":"error","text":" error "}`, HookOutputEntry{Kind: HookOutputEntryKindError, Text: " error "}, `{"kind":"error","text":" error "}`},
	}
	for _, tc := range cases {
		var got HookOutputEntry
		if err := json.Unmarshal([]byte(tc.input), &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("entry = %#v, want %#v", got, tc.want)
		}
		encoded, err := json.Marshal(got)
		if err != nil || string(encoded) != tc.wire {
			t.Fatalf("Marshal(%#v) = %s, %v; want %s", got, encoded, err, tc.wire)
		}
	}
}

func TestHookOutputEntryRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"text":"x"}`, `{"kind":"warning"}`,
		`{"kind":null,"text":"x"}`, `{"kind":"other","text":"x"}`,
		`{"kind":"warning","text":null}`, `{"kind":"warning","text":1}`,
		`{"Kind":"warning","text":"x"}`, `{"kind":"warning","Text":"x"}`,
		`{"kind":"warning","kind":"error","text":"x"}`,
		`{"kind":"warning","text":"x","text":"y"}`,
		`{"kind":"warning","text":"x"`, `{"kind":"warning","text":"x"} {}`,
	} {
		assertJSONRejects[HookOutputEntry](t, input)
	}
	var entry *HookOutputEntry
	if err := entry.UnmarshalJSON([]byte(`{"kind":"warning","text":"x"}`)); err == nil {
		t.Fatal("nil HookOutputEntry receiver succeeded")
	}
	if _, err := json.Marshal(HookOutputEntry{}); err == nil {
		t.Fatal("zero HookOutputEntry marshaled")
	}
}

func TestHookValueFoundationRemainsStandalone(t *testing.T) {
	names := []string{
		"HookEventName", "HookExecutionMode", "HookHandlerType",
		"HookOutputEntryKind", "HookRunStatus", "HookScope", "HookSource",
		"HookTrustStatus", "HookOutputEntry",
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
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestHookValueFoundationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type HookEventName = "preToolUse" | "permissionRequest" | "postToolUse" | "preCompact" | "postCompact" | "sessionStart" | "userPromptSubmit" | "subagentStart" | "subagentStop" | "stop";`,
		`export type HookExecutionMode = "sync" | "async";`,
		`export type HookHandlerType = "command" | "prompt" | "agent";`,
		`export type HookOutputEntryKind = "warning" | "stop" | "feedback" | "context" | "error";`,
		`export type HookRunStatus = "running" | "completed" | "failed" | "blocked" | "stopped";`,
		`export type HookScope = "thread" | "turn";`,
		`export type HookSource = "system" | "user" | "project" | "mdm" | "sessionFlags" | "plugin" | "cloudRequirements" | "cloudManagedConfig" | "legacyManagedConfigFile" | "legacyManagedConfigMdm" | "unknown";`,
		`export type HookTrustStatus = "managed" | "untrusted" | "trusted" | "modified";`,
		"export type HookOutputEntry = {\n  \"kind\": HookOutputEntryKind;\n  \"text\": string;\n};",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertHookEnumValues[T ~string](t *testing.T, values []T) {
	t.Helper()
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("Marshal(%T(%q)): %v", value, value, err)
		}
		var got T
		if err := json.Unmarshal(encoded, &got); err != nil || got != value {
			t.Fatalf("round trip %T(%q) = %q, %v", value, value, got, err)
		}
	}
}

func assertHookEnumRejects[T ~string](t *testing.T, inputs ...string) {
	t.Helper()
	for _, input := range inputs {
		var value T
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("%T Unmarshal(%s) succeeded", value, input)
		}
	}
}

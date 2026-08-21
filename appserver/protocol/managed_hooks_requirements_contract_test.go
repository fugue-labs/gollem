package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestManagedHooksRequirementSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	handler := defs["ConfiguredHookHandler"].(Schema)
	variants, ok := handler["oneOf"].([]any)
	if !ok || len(variants) != 3 {
		t.Fatalf("ConfiguredHookHandler variants = %#v", handler["oneOf"])
	}
	wantVariants := []struct {
		hookType string
		fields   []string
	}{
		{hookType: "command", fields: []string{"type", "command", "commandWindows", "timeoutSec", "async", "statusMessage"}},
		{hookType: "prompt", fields: []string{"type"}},
		{hookType: "agent", fields: []string{"type"}},
	}
	for index, want := range wantVariants {
		variant := variants[index].(Schema)
		if variant["additionalProperties"] != false {
			t.Fatalf("%s handler allows extra fields", want.hookType)
		}
		properties := variant["properties"].(Schema)
		if !reflect.DeepEqual(properties["type"].(Schema)["enum"], []any{want.hookType}) {
			t.Fatalf("handler variant %d type = %#v", index, properties["type"])
		}
		if !slices.Equal(schemaRequiredNames(variant), want.fields) {
			t.Fatalf("%s handler required = %v, want %v", want.hookType, schemaRequiredNames(variant), want.fields)
		}
	}
	command := variants[0].(Schema)["properties"].(Schema)
	if !reflect.DeepEqual(command["command"], Schema{"type": "string"}) ||
		!reflect.DeepEqual(command["async"], Schema{"type": "boolean"}) {
		t.Fatalf("command handler required fields = %#v", command)
	}
	assertConfigNullableSchema(t, command["commandWindows"], Schema{"type": "string"})
	assertConfigNullableSchema(t, command["timeoutSec"], Schema{"type": "integer", "minimum": 0})
	assertConfigNullableSchema(t, command["statusMessage"], Schema{"type": "string"})

	group := defs["ConfiguredHookMatcherGroup"].(Schema)
	assertClosedObjectSchema(t, group, "matcher", "hooks")
	groupProperties := group["properties"].(Schema)
	assertConfigNullableSchema(t, groupProperties["matcher"], Schema{"type": "string"})
	if !reflect.DeepEqual(groupProperties["hooks"], Schema{
		"type": "array", "items": Schema{"$ref": "#/$defs/ConfiguredHookHandler"},
	}) {
		t.Fatalf("matcher hooks schema = %#v", groupProperties["hooks"])
	}

	requirements := defs["ManagedHooksRequirements"].(Schema)
	fields := []string{
		"managedDir", "windowsManagedDir", "PreToolUse", "PermissionRequest",
		"PostToolUse", "PreCompact", "PostCompact", "SessionStart",
		"UserPromptSubmit", "SubagentStart", "SubagentStop", "Stop",
	}
	assertClosedObjectSchema(t, requirements, fields...)
	properties := requirements["properties"].(Schema)
	assertConfigNullableSchema(t, properties["managedDir"], Schema{"type": "string"})
	assertConfigNullableSchema(t, properties["windowsManagedDir"], Schema{"type": "string"})
	for _, field := range fields[2:] {
		if !reflect.DeepEqual(properties[field], Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/ConfiguredHookMatcherGroup"},
		}) {
			t.Fatalf("%s schema = %#v", field, properties[field])
		}
	}
}

func TestConfiguredHookHandlerAcceptsExactWireForms(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: `{"type":"command","command":"","async":false}`,
			want:  `{"type":"command","command":"","commandWindows":null,"timeoutSec":null,"async":false,"statusMessage":null}`,
		},
		{
			input: `{"type":"command","command":"run","commandWindows":"run.exe","timeoutSec":18446744073709551615,"async":true,"statusMessage":"working"}`,
			want:  `{"type":"command","command":"run","commandWindows":"run.exe","timeoutSec":18446744073709551615,"async":true,"statusMessage":"working"}`,
		},
		{input: `{"type":"prompt"}`, want: `{"type":"prompt"}`},
		{input: `{"type":"agent"}`, want: `{"type":"agent"}`},
	}
	for _, tc := range cases {
		var value ConfiguredHookHandler
		assertManagedHooksRoundTrip(t, tc.input, tc.want, &value)
	}
}

func TestConfiguredHookHandlerRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"type":"command","async":false}`,
		`{"type":"command","command":"run"}`,
		`{"type":"command","command":null,"async":false}`,
		`{"type":"command","command":"run","async":null}`,
		`{"type":"command","command":"run","async":false,"commandWindows":1}`,
		`{"type":"command","command":"run","async":false,"timeoutSec":-1}`,
		`{"type":"command","command":"run","async":false,"timeoutSec":1.5}`,
		`{"type":"command","command":"run","async":false,"timeoutSec":18446744073709551616}`,
		`{"type":"command","command":"run","async":false,"statusMessage":[]}`,
		`{"type":"command","command":"run","async":false,"extra":true}`,
		`{"type":"prompt","command":"run"}`,
		`{"type":"agent","async":false}`,
		`{"type":"other"}`,
		`{"type":1}`,
		`{"type":"prompt"} {}`,
	}
	for _, input := range invalid {
		assertJSONRejects[ConfiguredHookHandler](t, input)
	}
}

func TestConfiguredHookMatcherGroupAcceptsAndCanonicalizesRustOptions(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: `{"hooks":[]}`, want: `{"matcher":null,"hooks":[]}`},
		{input: `{"matcher":null,"hooks":[]}`, want: `{"matcher":null,"hooks":[]}`},
		{
			input: `{"matcher":"","hooks":[{"type":"prompt"},{"type":"command","command":"run","async":false},{"type":"agent"}]}`,
			want:  `{"matcher":"","hooks":[{"type":"prompt"},{"type":"command","command":"run","commandWindows":null,"timeoutSec":null,"async":false,"statusMessage":null},{"type":"agent"}]}`,
		},
	}
	for _, tc := range cases {
		var value ConfiguredHookMatcherGroup
		assertManagedHooksRoundTrip(t, tc.input, tc.want, &value)
	}
}

func TestConfiguredHookMatcherGroupRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"hooks":null}`,
		`{"hooks":{}}`,
		`{"hooks":[null]}`,
		`{"hooks":[{"type":"other"}]}`,
		`{"matcher":1,"hooks":[]}`,
		`{"matcher":null,"hooks":[],"extra":true}`,
		`{"hooks":[]} {}`,
	} {
		assertJSONRejects[ConfiguredHookMatcherGroup](t, input)
	}
}

func TestManagedHooksRequirementsAcceptExactWireForms(t *testing.T) {
	minimal := `{"PreToolUse":[],"PermissionRequest":[],"PostToolUse":[],"PreCompact":[],"PostCompact":[],"SessionStart":[],"UserPromptSubmit":[],"SubagentStart":[],"SubagentStop":[],"Stop":[]}`
	minimalCanonical := `{"managedDir":null,"windowsManagedDir":null,"PreToolUse":[],"PermissionRequest":[],"PostToolUse":[],"PreCompact":[],"PostCompact":[],"SessionStart":[],"UserPromptSubmit":[],"SubagentStart":[],"SubagentStop":[],"Stop":[]}`
	var omittedOptions ManagedHooksRequirements
	assertManagedHooksRoundTrip(t, minimal, minimalCanonical, &omittedOptions)

	full := `{"managedDir":"","windowsManagedDir":"C:\\\\hooks","PreToolUse":[{"matcher":"tool","hooks":[{"type":"command","command":"run","commandWindows":null,"timeoutSec":0,"async":false,"statusMessage":null}]}],"PermissionRequest":[{"matcher":null,"hooks":[{"type":"prompt"}]}],"PostToolUse":[{"matcher":"*","hooks":[{"type":"agent"},{"type":"agent"}]}],"PreCompact":[],"PostCompact":[],"SessionStart":[],"UserPromptSubmit":[],"SubagentStart":[],"SubagentStop":[],"Stop":[]}`
	var fullValue ManagedHooksRequirements
	assertManagedHooksRoundTrip(t, full, full, &fullValue)
}

func TestManagedHooksRequirementsRejectMalformedWireForms(t *testing.T) {
	base := map[string]any{
		"PreToolUse": []any{}, "PermissionRequest": []any{}, "PostToolUse": []any{},
		"PreCompact": []any{}, "PostCompact": []any{}, "SessionStart": []any{},
		"UserPromptSubmit": []any{}, "SubagentStart": []any{}, "SubagentStop": []any{}, "Stop": []any{},
	}
	for _, field := range []string{
		"PreToolUse", "PermissionRequest", "PostToolUse", "PreCompact", "PostCompact",
		"SessionStart", "UserPromptSubmit", "SubagentStart", "SubagentStop", "Stop",
	} {
		copy := cloneManagedHooksMap(base)
		delete(copy, field)
		assertJSONRejects[ManagedHooksRequirements](t, string(mustMarshalManagedHooks(t, copy)))
		copy = cloneManagedHooksMap(base)
		copy[field] = nil
		assertJSONRejects[ManagedHooksRequirements](t, string(mustMarshalManagedHooks(t, copy)))
	}
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"managedDir":1,"PreToolUse":[],"PermissionRequest":[],"PostToolUse":[],"PreCompact":[],"PostCompact":[],"SessionStart":[],"UserPromptSubmit":[],"SubagentStart":[],"SubagentStop":[],"Stop":[]}`,
		`{"windowsManagedDir":false,"PreToolUse":[],"PermissionRequest":[],"PostToolUse":[],"PreCompact":[],"PostCompact":[],"SessionStart":[],"UserPromptSubmit":[],"SubagentStart":[],"SubagentStop":[],"Stop":[]}`,
		`{"PreToolUse":[null],"PermissionRequest":[],"PostToolUse":[],"PreCompact":[],"PostCompact":[],"SessionStart":[],"UserPromptSubmit":[],"SubagentStart":[],"SubagentStop":[],"Stop":[]}`,
		`{"preToolUse":[],"PermissionRequest":[],"PostToolUse":[],"PreCompact":[],"PostCompact":[],"SessionStart":[],"UserPromptSubmit":[],"SubagentStart":[],"SubagentStop":[],"Stop":[]}`,
		`{"PreToolUse":[],"PermissionRequest":[],"PostToolUse":[],"PreCompact":[],"PostCompact":[],"SessionStart":[],"UserPromptSubmit":[],"SubagentStart":[],"SubagentStop":[],"Stop":[],"extra":true}`,
		`{"PreToolUse":[],"PermissionRequest":[],"PostToolUse":[],"PreCompact":[],"PostCompact":[],"SessionStart":[],"UserPromptSubmit":[],"SubagentStart":[],"SubagentStop":[],"Stop":[]} {}`,
	} {
		assertJSONRejects[ManagedHooksRequirements](t, input)
	}
}

func TestManagedHooksRequirementsNilReceiversAndInvalidMarshal(t *testing.T) {
	var handler *ConfiguredHookHandler
	if err := handler.UnmarshalJSON([]byte(`{"type":"prompt"}`)); err == nil {
		t.Fatal("nil ConfiguredHookHandler receiver succeeded")
	}
	var group *ConfiguredHookMatcherGroup
	if err := group.UnmarshalJSON([]byte(`{"hooks":[]}`)); err == nil {
		t.Fatal("nil ConfiguredHookMatcherGroup receiver succeeded")
	}
	var requirements *ManagedHooksRequirements
	if err := requirements.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ManagedHooksRequirements receiver succeeded")
	}
	if _, err := json.Marshal(ConfiguredHookHandler{}); err == nil {
		t.Fatal("empty ConfiguredHookHandler marshal succeeded")
	}
	if _, err := json.Marshal(ConfiguredHookMatcherGroup{}); err == nil {
		t.Fatal("nil matcher hooks marshal succeeded")
	}
	if _, err := json.Marshal(ManagedHooksRequirements{}); err == nil {
		t.Fatal("nil managed hook arrays marshal succeeded")
	}
}

func TestManagedHooksRequirementContractsRemainStandalone(t *testing.T) {
	names := []string{"ConfiguredHookHandler", "ConfiguredHookMatcherGroup", "ManagedHooksRequirements"}
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

func TestManagedHooksRequirementsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ConfiguredHookHandler = {`,
		`"type": "command";`,
		`"commandWindows": string | null;`,
		`"timeoutSec": number | null;`,
		`"statusMessage": string | null;`,
		`"type": "prompt";`,
		`"type": "agent";`,
		`export type ConfiguredHookMatcherGroup = {`,
		`"hooks": Array<ConfiguredHookHandler>;`,
		`"matcher": string | null;`,
		`export type ManagedHooksRequirements = {`,
		`"managedDir": string | null;`,
		`"PreToolUse": Array<ConfiguredHookMatcherGroup>;`,
		`"PermissionRequest": Array<ConfiguredHookMatcherGroup>;`,
		`"Stop": Array<ConfiguredHookMatcherGroup>;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertManagedHooksRoundTrip(t *testing.T, input, want string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(input), target); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded := mustMarshalManagedHooks(t, target)
	if string(encoded) != want {
		t.Fatalf("round trip %s = %s, want %s", input, encoded, want)
	}
}

func mustMarshalManagedHooks(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(%#v): %v", value, err)
	}
	return encoded
}

func cloneManagedHooksMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

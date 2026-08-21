package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestCollaborationCapabilitySchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	want := map[string]Schema{
		"ModeKind": {
			"description": "Initial collaboration mode to use when the TUI starts.",
			"enum":        []any{"plan", "default"},
			"type":        "string",
		},
		"MultiAgentMode": {
			"description": "Controls the effective multi-agent delegation instructions for a turn. `custom` means the configured mode hint defines the policy instead of a built-in policy.",
			"oneOf": []any{
				Schema{"enum": []any{"explicitRequestOnly", "proactive"}, "type": "string"},
				Schema{
					"additionalProperties": false,
					"properties":           Schema{"custom": Schema{"type": "string"}},
					"required":             []string{"custom"},
					"title":                "CustomMultiAgentMode",
					"type":                 "object",
				},
			},
		},
		"Settings": {
			"description": "Settings for a collaboration mode.",
			"properties": Schema{
				"developer_instructions": Schema{"type": []any{"string", "null"}},
				"model":                  Schema{"type": "string"},
				"reasoning_effort": nullableCollaborationSchema(
					Schema{"$ref": "#/$defs/ReasoningEffort"},
				),
			},
			"required": []string{"model"},
			"type":     "object",
		},
		"CollaborationMode": {
			"description": "Collaboration mode for a Codex session.",
			"properties": Schema{
				"mode":     Schema{"$ref": "#/$defs/ModeKind"},
				"settings": Schema{"$ref": "#/$defs/Settings"},
			},
			"required": []string{"mode", "settings"},
			"type":     "object",
		},
		"CollaborationModeMask": {
			"description": "EXPERIMENTAL - collaboration mode preset metadata for clients.",
			"properties": Schema{
				"mode":  nullableCollaborationSchema(Schema{"$ref": "#/$defs/ModeKind"}),
				"model": Schema{"type": []any{"string", "null"}},
				"name":  Schema{"type": "string"},
				"reasoning_effort": Schema{"anyOf": []any{
					nullableCollaborationSchema(Schema{"$ref": "#/$defs/ReasoningEffort"}),
					Schema{"type": "null"},
				}},
			},
			"required": []string{"name"},
			"type":     "object",
		},
		"CapabilityRootLocation": {
			"description": "Location used to resolve a selected capability root.",
			"oneOf": []any{Schema{
				"description": "A path owned by an execution environment.",
				"properties": Schema{
					"environmentId": Schema{"type": "string"},
					"path": Schema{
						"description": "Absolute path for the root in the selected environment.",
						"type":        "string",
					},
					"type": Schema{
						"enum":  []any{"environment"},
						"title": "EnvironmentCapabilityRootLocationType",
						"type":  "string",
					},
				},
				"required": []string{"environmentId", "path", "type"},
				"title":    "EnvironmentCapabilityRootLocation",
				"type":     "object",
			}},
		},
		"SelectedCapabilityRoot": {
			"description": "A user-selected root that can expose one or more runtime capabilities.",
			"properties": Schema{
				"id": Schema{
					"description": "Stable identifier supplied by the capability selection platform.",
					"type":        "string",
				},
				"location": Schema{
					"allOf":       []any{Schema{"$ref": "#/$defs/CapabilityRootLocation"}},
					"description": "Where the selected root can be resolved.",
				},
			},
			"required": []string{"id", "location"},
			"type":     "object",
		},
	}
	for name, expected := range want {
		if !reflect.DeepEqual(defs[name], expected) {
			t.Errorf("%s schema = %#v, want %#v", name, defs[name], expected)
		}
	}
}

func TestCollaborationCapabilityWireCanonicalization(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		value func() any
	}{
		{"mode plan", `"plan"`, `"plan"`, func() any { return new(ModeKind) }},
		{"mode legacy code", `"code"`, `"default"`, func() any { return new(ModeKind) }},
		{"mode legacy pair", `"pair_programming"`, `"default"`, func() any { return new(ModeKind) }},
		{"mode legacy execute", `"execute"`, `"default"`, func() any { return new(ModeKind) }},
		{"mode legacy custom", `"custom"`, `"default"`, func() any { return new(ModeKind) }},
		{"multi-agent explicit", `"explicitRequestOnly"`, `"explicitRequestOnly"`, func() any { return new(MultiAgentMode) }},
		{"multi-agent proactive", `"proactive"`, `"proactive"`, func() any { return new(MultiAgentMode) }},
		{"multi-agent custom empty", `{"custom":""}`, `{"custom":""}`, func() any { return new(MultiAgentMode) }},
		{"multi-agent legacy none", `"none"`, `{"custom":""}`, func() any { return new(MultiAgentMode) }},
		{
			"settings omitted options",
			`{"model":""}`,
			`{"model":"","reasoning_effort":null,"developer_instructions":null}`,
			func() any { return new(Settings) },
		},
		{
			"settings full and unknown",
			`{"model":"gpt","reasoning_effort":"provider-effort","developer_instructions":"","ignored":true}`,
			`{"model":"gpt","reasoning_effort":"provider-effort","developer_instructions":""}`,
			func() any { return new(Settings) },
		},
		{
			"collaboration mode",
			`{"mode":"code","settings":{"model":"gpt"},"ignored":true}`,
			`{"mode":"default","settings":{"model":"gpt","reasoning_effort":null,"developer_instructions":null}}`,
			func() any { return new(CollaborationMode) },
		},
		{
			"mask omitted options",
			`{"name":""}`,
			`{"name":"","mode":null,"model":null,"reasoning_effort":null}`,
			func() any { return new(CollaborationModeMask) },
		},
		{
			"mask full",
			`{"name":"plan","mode":"plan","model":"","reasoning_effort":"high","ignored":true}`,
			`{"name":"plan","mode":"plan","model":"","reasoning_effort":"high"}`,
			func() any { return new(CollaborationModeMask) },
		},
		{
			"foreign capability URI",
			`{"type":"environment","environmentId":"executor","path":"file:///C:/plugins/demo"}`,
			`{"type":"environment","environmentId":"executor","path":"file:///C:/plugins/demo"}`,
			func() any { return new(CapabilityRootLocation) },
		},
		{
			"legacy absolute capability path",
			`{"type":"environment","environmentId":"","path":"/workspace/plugins/../demo","ignored":true}`,
			`{"type":"environment","environmentId":"","path":"file:///workspace/demo"}`,
			func() any { return new(CapabilityRootLocation) },
		},
		{
			"selected capability root",
			`{"id":"","location":{"type":"environment","environmentId":"remote","path":"/workspace"},"ignored":true}`,
			`{"id":"","location":{"type":"environment","environmentId":"remote","path":"file:///workspace"}}`,
			func() any { return new(SelectedCapabilityRoot) },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.value()
			if err := json.Unmarshal([]byte(tc.input), value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != tc.want {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, tc.want)
			}
		})
	}
}

func TestCollaborationCapabilityRejectsMalformedWireValues(t *testing.T) {
	tests := []struct {
		name  string
		value func() any
		bad   []string
	}{
		{
			"mode", func() any { return new(ModeKind) },
			[]string{`null`, `""`, `"PLAN"`, `"pairProgramming"`, `0`, `{}`, `[]`, `"plan" {}`},
		},
		{
			"multi-agent", func() any { return new(MultiAgentMode) },
			[]string{`null`, `""`, `"unknown"`, `{"custom":null}`, `{"custom":0}`, `{"custom":"a","extra":true}`, `{"custom":"a","custom":"b"}`, `{}`, `[]`, `"proactive" {}`},
		},
		{
			"settings", func() any { return new(Settings) },
			[]string{`null`, `{}`, `{"model":null}`, `{"model":0}`, `{"model":"gpt","reasoning_effort":""}`, `{"model":"gpt","developer_instructions":0}`, `{"model":"a","model":"b"}`, `{"model":"gpt"} {}`},
		},
		{
			"collaboration mode", func() any { return new(CollaborationMode) },
			[]string{`null`, `{}`, `{"mode":"plan"}`, `{"settings":{"model":"gpt"}}`, `{"mode":null,"settings":{"model":"gpt"}}`, `{"mode":"plan","settings":null}`, `{"mode":"plan","settings":{"model":"gpt"},"mode":"default"}`, `{"mode":"plan","settings":{"model":"gpt"}} {}`},
		},
		{
			"mask", func() any { return new(CollaborationModeMask) },
			[]string{`null`, `{}`, `{"name":null}`, `{"name":0}`, `{"name":"x","mode":""}`, `{"name":"x","model":0}`, `{"name":"x","reasoning_effort":""}`, `{"name":"a","name":"b"}`, `{"name":"x"} {}`},
		},
		{
			"capability location", func() any { return new(CapabilityRootLocation) },
			[]string{`null`, `{}`, `{"type":"unknown","environmentId":"e","path":"/x"}`, `{"type":"environment","path":"/x"}`, `{"type":"environment","environmentId":"e"}`, `{"type":"environment","environmentId":null,"path":"/x"}`, `{"type":"environment","environmentId":"e","path":"relative"}`, `{"type":"environment","environmentId":"e","path":"https://example.com/x"}`, `{"type":"environment","environmentId":"e","path":"/x","type":"environment"}`, `{"type":"environment","environmentId":"e","path":"/x"} {}`},
		},
		{
			"selected root", func() any { return new(SelectedCapabilityRoot) },
			[]string{`null`, `{}`, `{"id":""}`, `{"location":{"type":"environment","environmentId":"e","path":"/x"}}`, `{"id":null,"location":{"type":"environment","environmentId":"e","path":"/x"}}`, `{"id":"","location":null}`, `{"id":"a","id":"b","location":{"type":"environment","environmentId":"e","path":"/x"}}`, `{"id":"","location":{"type":"environment","environmentId":"e","path":"relative"}}`, `{"id":"","location":{"type":"environment","environmentId":"e","path":"/x"}} {}`},
		},
	}
	for _, tc := range tests {
		for _, input := range tc.bad {
			value := tc.value()
			if err := json.Unmarshal([]byte(input), value); err == nil {
				t.Errorf("%s accepted %s", tc.name, input)
			}
		}
	}
}

func TestCollaborationCapabilityNilReceiversAndInvalidMarshal(t *testing.T) {
	nilReceivers := []json.Unmarshaler{
		(*ModeKind)(nil),
		(*MultiAgentMode)(nil),
		(*Settings)(nil),
		(*CollaborationMode)(nil),
		(*CollaborationModeMask)(nil),
		(*CapabilityRootLocation)(nil),
		(*SelectedCapabilityRoot)(nil),
	}
	for _, value := range nilReceivers {
		if err := value.UnmarshalJSON([]byte(`{}`)); err == nil {
			t.Errorf("%T nil receiver succeeded", value)
		}
	}

	emptyEffort := ReasoningEffort("")
	invalid := []any{
		ModeKind("invalid"),
		MultiAgentMode{},
		Settings{Model: "gpt", ReasoningEffort: &emptyEffort},
		CollaborationMode{Mode: ModeKind("invalid"), Settings: Settings{Model: "gpt"}},
		CollaborationModeMask{Name: "mask", ReasoningEffort: &emptyEffort},
		CapabilityRootLocation{},
		SelectedCapabilityRoot{ID: "root"},
	}
	for _, value := range invalid {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("marshal %T succeeded", value)
		}
	}
}

func TestCapabilityRootPathCanonicalization(t *testing.T) {
	opaque := opaqueCapabilityPathURI([]byte("/workspace/\x00/root"))
	valid := []struct {
		input string
		want  string
	}{
		{"/", "file:///"},
		{"/workspace/plugins/../root", "file:///workspace/root"},
		{"/workspace/./root", "file:///workspace/root"},
		{"/workspace/root/", "file:///workspace/root/"},
		{`C:\plugins\..\root`, "file:///C:/root"},
		{`C:\`, "file:///C:/"},
		{`C:\..\root`, "file:///C:/root"},
		{`\\server\share\plugins\..\root`, "file://server/share/root"},
		{`\\server\share\..\root`, "file://server/share/root"},
		{`\\server\share\`, "file://server/share/"},
		{`\\.\COM1`, "file:///%00/bad/path/XABcAC4AXABDAE8ATQAxAA"},
		{`\\bad host\share\x`, "file:///%00/bad/path/XABcAGIAYQBkACAAaABvAHMAdABcAHMAaABhAHIAZQBcAHgA"},
		{"C:\\root\x00bad", "file:///%00/bad/path/QwA6AFwAcgBvAG8AdAAAAGIAYQBkAA"},
		{"file://localhost/workspace", "file:///workspace"},
		{"file://localhost", "file:///"},
		{"file://SERVER/Share", "file://server/Share"},
		{"file://server/share/../root", "file://server/root"},
		{"file://server/share/..", "file://server/"},
		{"file:///a//b", "file:///a//b"},
		{"file:///tmp/a%2Fb", "file:///tmp/a%2Fb"},
		{"file:///tmp/a/../root/", "file:///tmp/root/"},
		{"file:///a/%2e%2e/root", "file:///root"},
		{"file:///a/%2e", "file:///a/"},
		{"file:///C:/..", "file:///C:/"},
		{"file:relative", "file:///relative"},
		{"file:", "file:///"},
		{"/workspace/\x00/root", opaque},
		{opaque, opaque},
	}
	for _, tc := range valid {
		got, err := canonicalCapabilityRootPath(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("canonicalCapabilityRootPath(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}

	invalid := []string{
		"",
		"x",
		"relative/path",
		"https://example.com/root",
		"file://user@server/root",
		"file://server:99/root",
		"file:///root?query=true",
		"file:///root#fragment",
		"file:///root%00bad",
		"file:///%00/bad/path/not*base64",
		"file:///%00/bad/path/YQ==",
		`\\server`,
		`\\\share`,
	}
	for _, input := range invalid {
		if got, err := canonicalCapabilityRootPath(input); err == nil {
			t.Errorf("canonicalCapabilityRootPath(%q) = %q without error", input, got)
		}
	}
}

func TestCollaborationCapabilityContractsRemainStandalone(t *testing.T) {
	names := []string{
		"CapabilityRootLocation", "CollaborationMode", "CollaborationModeMask",
		"ModeKind", "MultiAgentMode", "SelectedCapabilityRoot", "Settings",
	}
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

func TestCollaborationCapabilityTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type CapabilityRootLocation = {`,
		`"type": "environment";`,
		`"environmentId": string;`,
		`"path": string;`,
		`export type CollaborationMode = {`,
		`"mode": ModeKind;`,
		`"settings": Settings;`,
		`export type CollaborationModeMask = {`,
		`"mode": ModeKind | null;`,
		`"model": string | null;`,
		`"name": string;`,
		`"reasoning_effort": ReasoningEffort | null | null;`,
		`export type ModeKind = "plan" | "default";`,
		`export type MultiAgentMode = { "custom": string } | "explicitRequestOnly" | "proactive";`,
		`export type SelectedCapabilityRoot = {`,
		`"id": string;`,
		`"location": CapabilityRootLocation;`,
		`export type Settings = {`,
		`"developer_instructions": string | null;`,
		`"model": string;`,
		`"reasoning_effort": ReasoningEffort | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func nullableCollaborationSchema(value Schema) Schema {
	return Schema{"anyOf": []any{value, Schema{"type": "null"}}}
}

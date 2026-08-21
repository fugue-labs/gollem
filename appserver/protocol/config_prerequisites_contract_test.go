package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestConfigPrerequisiteSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	want := map[string]Schema{
		"AnalyticsConfig": {
			"type": "object",
			"properties": Schema{
				"enabled": nullableConfigPrerequisiteSchema(Schema{"type": "boolean"}),
			},
			"required":                         []string{"enabled"},
			"additionalProperties":             Schema{"$ref": "#/$defs/JsonValue"},
			"x-gollem-typescript-optional-map": true,
		},
		"AutoCompactTokenLimitScope": {
			"description": "Selects which part of the active context is charged against `model_auto_compact_token_limit`.",
			"oneOf": []any{
				Schema{
					"type":        "string",
					"enum":        []any{"total"},
					"description": "Count the full active context against the limit.",
				},
				Schema{
					"type":        "string",
					"enum":        []any{"body_after_prefix"},
					"description": "Count sampled output and later growth after the carried window prefix.",
				},
			},
		},
		"ForcedChatgptWorkspaceIds": {
			"anyOf": []any{
				Schema{"type": "string"},
				Schema{"type": "array", "items": Schema{"type": "string"}},
			},
			"description": "Backward-compatible API shape for ChatGPT workspace login restrictions.",
		},
		"ForcedLoginMethod": stringEnumSchema("chatgpt", "api"),
		"SandboxWorkspaceWrite": {
			"type": "object",
			"properties": Schema{
				"writable_roots": Schema{
					"type": "array", "items": Schema{"type": "string"}, "default": []any{},
				},
				"network_access":         Schema{"type": "boolean", "default": false},
				"exclude_tmpdir_env_var": Schema{"type": "boolean", "default": false},
				"exclude_slash_tmp":      Schema{"type": "boolean", "default": false},
			},
			"required": []string{
				"writable_roots", "network_access", "exclude_tmpdir_env_var", "exclude_slash_tmp",
			},
			"additionalProperties":                             true,
			"x-gollem-typescript-ignore-additional-properties": true,
		},
		"ToolsV2": {
			"type": "object",
			"properties": Schema{
				"web_search": nullableConfigPrerequisiteSchema(Schema{"$ref": "#/$defs/WebSearchToolConfig"}),
			},
			"required":             []string{"web_search"},
			"additionalProperties": true,
			"x-gollem-typescript-ignore-additional-properties": true,
		},
		"Verbosity": {
			"type":        "string",
			"enum":        []any{"low", "medium", "high"},
			"description": "Controls output length/detail on GPT-5 models via the Responses API. Serialized with lowercase values to match the OpenAI API.",
		},
		"WebSearchContextSize": stringEnumSchema("low", "medium", "high"),
		"WebSearchLocation": {
			"type": "object",
			"properties": Schema{
				"country":  nullableConfigPrerequisiteSchema(Schema{"type": "string"}),
				"region":   nullableConfigPrerequisiteSchema(Schema{"type": "string"}),
				"city":     nullableConfigPrerequisiteSchema(Schema{"type": "string"}),
				"timezone": nullableConfigPrerequisiteSchema(Schema{"type": "string"}),
			},
			"required":             []string{"country", "region", "city", "timezone"},
			"additionalProperties": false,
		},
		"WebSearchToolConfig": {
			"type": "object",
			"properties": Schema{
				"context_size": nullableConfigPrerequisiteSchema(Schema{"$ref": "#/$defs/WebSearchContextSize"}),
				"allowed_domains": nullableConfigPrerequisiteSchema(Schema{
					"type": "array", "items": Schema{"type": "string"},
				}),
				"location": nullableConfigPrerequisiteSchema(Schema{"$ref": "#/$defs/WebSearchLocation"}),
			},
			"required":             []string{"context_size", "allowed_domains", "location"},
			"additionalProperties": false,
		},
	}
	for name, expected := range want {
		if got := defs[name]; !reflect.DeepEqual(got, expected) {
			t.Errorf("%s schema = %#v, want %#v", name, got, expected)
		}
	}
}

func TestConfigPrerequisiteEnumsAcceptOnlyExactValues(t *testing.T) {
	assertConfigPrerequisiteEnum[AutoCompactTokenLimitScope](t, "total", "body_after_prefix")
	assertConfigPrerequisiteEnum[ForcedLoginMethod](t, "chatgpt", "api")
	assertConfigPrerequisiteEnum[Verbosity](t, "low", "medium", "high")
	assertConfigPrerequisiteEnum[WebSearchContextSize](t, "low", "medium", "high")
}

func TestForcedChatgptWorkspaceIdsAcceptsExactWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{`""`, `""`},
		{`"workspace"`, `"workspace"`},
		{`[]`, `[]`},
		{`["","workspace","workspace"]`, `["","workspace","workspace"]`},
	} {
		var value ForcedChatgptWorkspaceIds
		assertConfigPrerequisiteRoundTrip(t, test.input, test.want, &value)
	}
	for _, input := range []string{
		`null`, `true`, `1`, `{}`, `[null]`, `["workspace",1]`, `{} {}`,
	} {
		assertJSONRejects[ForcedChatgptWorkspaceIds](t, input)
	}
	var zero ForcedChatgptWorkspaceIds
	if _, err := json.Marshal(zero); err == nil {
		t.Fatal("empty ForcedChatgptWorkspaceIds marshaled")
	}
}

func TestAnalyticsConfigPreservesOpenPrecisionSafeValues(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{`{}`, `{"enabled":null}`},
		{`{"enabled":null}`, `{"enabled":null}`},
		{`{"enabled":false}`, `{"enabled":false}`},
		{`{"enabled":true,"nested":{"value":[9007199254740993,true,null]},"z":"last"}`, `{"enabled":true,"nested":{"value":[9007199254740993,true,null]},"z":"last"}`},
		{`{"empty":[],"enabled":null}`, `{"empty":[],"enabled":null}`},
	} {
		var value AnalyticsConfig
		assertConfigPrerequisiteRoundTrip(t, test.input, test.want, &value)
	}
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`,
		`{"enabled":"true"}`, `{"enabled":0}`, `{} {}`,
	} {
		assertJSONRejects[AnalyticsConfig](t, input)
	}
	invalid := AnalyticsConfig{Additional: map[string]JsonValue{"bad": {}}}
	if _, err := json.Marshal(invalid); err == nil {
		t.Fatal("AnalyticsConfig with invalid JsonValue marshaled")
	}
	conflicting := AnalyticsConfig{Additional: map[string]JsonValue{"enabled": {}}}
	if _, err := json.Marshal(conflicting); err == nil {
		t.Fatal("AnalyticsConfig with conflicting enabled field marshaled")
	}
}

func TestSandboxWorkspaceWriteDefaultsAndIgnoresAdditionalFields(t *testing.T) {
	const defaults = `{"writable_roots":[],"network_access":false,"exclude_tmpdir_env_var":false,"exclude_slash_tmp":false}`
	for _, test := range []struct {
		input string
		want  string
	}{
		{`{}`, defaults},
		{`{"writable_roots":[],"network_access":false,"exclude_tmpdir_env_var":false,"exclude_slash_tmp":false}`, defaults},
		{`{"writable_roots":["","relative","/absolute"],"network_access":true,"exclude_tmpdir_env_var":true,"exclude_slash_tmp":true}`, `{"writable_roots":["","relative","/absolute"],"network_access":true,"exclude_tmpdir_env_var":true,"exclude_slash_tmp":true}`},
		{`{"future":{"nested":true}}`, defaults},
	} {
		var value SandboxWorkspaceWrite
		assertConfigPrerequisiteRoundTrip(t, test.input, test.want, &value)
	}
	for _, input := range []string{
		`null`, `[]`, `"value"`,
		`{"writable_roots":null}`,
		`{"writable_roots":{}}`,
		`{"writable_roots":[null]}`,
		`{"writable_roots":[1]}`,
		`{"network_access":null}`,
		`{"network_access":"true"}`,
		`{"exclude_tmpdir_env_var":0}`,
		`{"exclude_slash_tmp":null}`,
		`{} {}`,
	} {
		assertJSONRejects[SandboxWorkspaceWrite](t, input)
	}
	encoded, err := json.Marshal(SandboxWorkspaceWrite{})
	if err != nil || string(encoded) != defaults {
		t.Fatalf("zero SandboxWorkspaceWrite = %s, %v", encoded, err)
	}
}

func TestWebSearchConfigGraphCanonicalizesNullableFields(t *testing.T) {
	const emptyLocation = `{"country":null,"region":null,"city":null,"timezone":null}`
	for _, test := range []struct {
		input string
		want  string
	}{
		{`{}`, emptyLocation},
		{`{"country":null,"region":"","city":"New York","timezone":"America/New_York"}`, `{"country":null,"region":"","city":"New York","timezone":"America/New_York"}`},
	} {
		var value WebSearchLocation
		assertConfigPrerequisiteRoundTrip(t, test.input, test.want, &value)
	}

	const emptyTool = `{"context_size":null,"allowed_domains":null,"location":null}`
	for _, test := range []struct {
		input string
		want  string
	}{
		{`{}`, emptyTool},
		{`{"context_size":null,"allowed_domains":null,"location":null}`, emptyTool},
		{`{"context_size":"high","allowed_domains":[],"location":{}}`, `{"context_size":"high","allowed_domains":[],"location":` + emptyLocation + `}`},
		{`{"context_size":"low","allowed_domains":["","example.com","example.com"],"location":{"country":"US"}}`, `{"context_size":"low","allowed_domains":["","example.com","example.com"],"location":{"country":"US","region":null,"city":null,"timezone":null}}`},
	} {
		var value WebSearchToolConfig
		assertConfigPrerequisiteRoundTrip(t, test.input, test.want, &value)
	}

	for _, test := range []struct {
		input string
		want  string
	}{
		{`{}`, `{"web_search":null}`},
		{`{"web_search":null}`, `{"web_search":null}`},
		{`{"web_search":{}}`, `{"web_search":` + emptyTool + `}`},
		{`{"future":true}`, `{"web_search":null}`},
	} {
		var value ToolsV2
		assertConfigPrerequisiteRoundTrip(t, test.input, test.want, &value)
	}
}

func TestWebSearchConfigGraphRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`,
		`{"country":1}`,
		`{"region":false}`,
		`{"city":[]}`,
		`{"timezone":{}}`,
		`{"extra":true}`,
		`{} {}`,
	} {
		assertJSONRejects[WebSearchLocation](t, input)
	}
	for _, input := range []string{
		`null`, `[]`,
		`{"context_size":"large"}`,
		`{"context_size":1}`,
		`{"allowed_domains":"example.com"}`,
		`{"allowed_domains":[null]}`,
		`{"allowed_domains":[1]}`,
		`{"location":false}`,
		`{"location":{"extra":true}}`,
		`{"extra":true}`,
		`{} {}`,
	} {
		assertJSONRejects[WebSearchToolConfig](t, input)
	}
	for _, input := range []string{
		`null`, `[]`, `{"web_search":false}`, `{"web_search":{"extra":true}}`, `{} {}`,
	} {
		assertJSONRejects[ToolsV2](t, input)
	}
}

func TestConfigPrerequisiteNilReceivers(t *testing.T) {
	var analytics *AnalyticsConfig
	if err := analytics.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil AnalyticsConfig receiver succeeded")
	}
	var workspace *ForcedChatgptWorkspaceIds
	if err := workspace.UnmarshalJSON([]byte(`"workspace"`)); err == nil {
		t.Fatal("nil ForcedChatgptWorkspaceIds receiver succeeded")
	}
	var sandbox *SandboxWorkspaceWrite
	if err := sandbox.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil SandboxWorkspaceWrite receiver succeeded")
	}
	var location *WebSearchLocation
	if err := location.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil WebSearchLocation receiver succeeded")
	}
	var tool *WebSearchToolConfig
	if err := tool.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil WebSearchToolConfig receiver succeeded")
	}
	var tools *ToolsV2
	if err := tools.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ToolsV2 receiver succeeded")
	}
}

func TestConfigPrerequisitesRemainStandalone(t *testing.T) {
	names := []string{
		"AnalyticsConfig",
		"AutoCompactTokenLimitScope",
		"ForcedChatgptWorkspaceIds",
		"ForcedLoginMethod",
		"SandboxWorkspaceWrite",
		"ToolsV2",
		"Verbosity",
		"WebSearchContextSize",
		"WebSearchLocation",
		"WebSearchToolConfig",
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

func TestConfigPrerequisiteTypeScriptExportsArePresent(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, name := range []string{
		"AnalyticsConfig",
		"AutoCompactTokenLimitScope",
		"ForcedChatgptWorkspaceIds",
		"ForcedLoginMethod",
		"SandboxWorkspaceWrite",
		"ToolsV2",
		"Verbosity",
		"WebSearchContextSize",
		"WebSearchLocation",
		"WebSearchToolConfig",
	} {
		if want := "export type " + name + " ="; !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func nullableConfigPrerequisiteSchema(value Schema) Schema {
	return Schema{"anyOf": []any{value, Schema{"type": "null"}}}
}

func assertConfigPrerequisiteEnum[T any](t *testing.T, values ...string) {
	t.Helper()
	for _, value := range values {
		var decoded T
		assertConfigPrerequisiteRoundTrip(t, `"`+value+`"`, `"`+value+`"`, &decoded)
	}
	for _, input := range []string{`null`, `true`, `1`, `""`, `"unknown"`, `{}`, `[]`, `"unknown" {}`} {
		assertJSONRejects[T](t, input)
	}
	var zero T
	if _, err := json.Marshal(zero); err == nil {
		t.Fatalf("zero %T marshaled", zero)
	}
}

func assertConfigPrerequisiteRoundTrip(t *testing.T, input, want string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(input), target); err != nil {
		t.Fatalf("unmarshal %s: %v", input, err)
	}
	got, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("marshal %s: %v", input, err)
	}
	if string(got) != want {
		t.Fatalf("round trip %s = %s, want %s", input, got, want)
	}
}

var (
	_ json.Unmarshaler = (*AnalyticsConfig)(nil)
	_ json.Unmarshaler = (*ForcedChatgptWorkspaceIds)(nil)
	_ json.Unmarshaler = (*SandboxWorkspaceWrite)(nil)
	_ json.Unmarshaler = (*WebSearchLocation)(nil)
	_ json.Unmarshaler = (*WebSearchToolConfig)(nil)
	_ json.Unmarshaler = (*ToolsV2)(nil)
)

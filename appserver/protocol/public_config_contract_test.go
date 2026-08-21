package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var publicConfigFieldNames = []string{
	"model",
	"review_model",
	"model_context_window",
	"model_auto_compact_token_limit",
	"model_auto_compact_token_limit_scope",
	"model_provider",
	"approval_policy",
	"approvals_reviewer",
	"sandbox_mode",
	"sandbox_workspace_write",
	"forced_chatgpt_workspace_id",
	"forced_login_method",
	"web_search",
	"tools",
	"instructions",
	"developer_instructions",
	"compact_prompt",
	"model_reasoning_effort",
	"model_reasoning_summary",
	"model_verbosity",
	"service_tier",
	"analytics",
	"desktop",
}

func TestPublicConfigSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	want := publicConfigContractSchema()
	if got := defs["Config"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("Config schema = %#v, want %#v", got, want)
	}
}

func TestPublicConfigCanonicalizesOmittedFieldsToNull(t *testing.T) {
	var value Config
	if err := json.Unmarshal([]byte(`{}`), &value); err != nil {
		t.Fatalf("unmarshal empty Config: %v", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal empty Config: %v", err)
	}
	if got, want := string(encoded), emptyPublicConfigJSON(); got != want {
		t.Fatalf("empty Config = %s, want %s", got, want)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode canonical Config: %v", err)
	}
	if len(fields) != len(publicConfigFieldNames) {
		t.Fatalf("canonical Config fields = %d, want %d", len(fields), len(publicConfigFieldNames))
	}
	for _, name := range publicConfigFieldNames {
		if got := string(fields[name]); got != "null" {
			t.Errorf("canonical Config %s = %q, want null", name, got)
		}
	}
}

func TestPublicConfigPreservesExactKnownAndOpenValues(t *testing.T) {
	input := `{
		"model":"gpt-5",
		"review_model":"reviewer",
		"model_context_window":-9223372036854775808,
		"model_auto_compact_token_limit":9223372036854775807,
		"model_auto_compact_token_limit_scope":"body_after_prefix",
		"model_provider":"provider",
		"approval_policy":{"granular":{"mcp_elicitations":true,"request_permissions":false,"rules":true,"sandbox_approval":false,"skill_approval":true}},
		"approvals_reviewer":"guardian_subagent",
		"sandbox_mode":"workspace-write",
		"sandbox_workspace_write":{"writable_roots":["","relative","/absolute"],"network_access":true,"exclude_tmpdir_env_var":false,"exclude_slash_tmp":true,"future":true},
		"forced_chatgpt_workspace_id":["","workspace","workspace"],
		"forced_login_method":"api",
		"web_search":"live",
		"tools":{"web_search":{"context_size":"high","allowed_domains":["","example.com","example.com"],"location":{"country":"US","region":null,"city":"New York","timezone":"America/New_York"}},"future":true},
		"instructions":"instructions",
		"developer_instructions":"developer",
		"compact_prompt":"compact",
		"model_reasoning_effort":"xhigh",
		"model_reasoning_summary":"detailed",
		"model_verbosity":"high",
		"service_tier":"priority",
		"analytics":{"enabled":true,"sample_rate":0.125,"nested":{"integer":9007199254740993}},
		"desktop":{"theme":"dark","nested":[9007199254740993,true,null]},
		"future_flag":false,
		"future_nested":{"integer":9007199254740993,"array":["value",null]}
	}`
	var value Config
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("unmarshal full Config: %v", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal full Config: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode full Config output: %v", err)
	}
	if got, want := len(fields), len(publicConfigFieldNames)+2; got != want {
		t.Fatalf("full Config fields = %d, want %d", got, want)
	}
	for name, want := range map[string]string{
		"model_context_window":           `-9223372036854775808`,
		"model_auto_compact_token_limit": `9223372036854775807`,
		"forced_chatgpt_workspace_id":    `["","workspace","workspace"]`,
		"sandbox_workspace_write":        `{"writable_roots":["","relative","/absolute"],"network_access":true,"exclude_tmpdir_env_var":false,"exclude_slash_tmp":true}`,
		"future_flag":                    `false`,
		"future_nested":                  `{"array":["value",null],"integer":9007199254740993}`,
	} {
		if got := string(fields[name]); got != want {
			t.Errorf("full Config %s = %s, want %s", name, got, want)
		}
	}
	if !strings.Contains(string(fields["analytics"]), `9007199254740993`) {
		t.Fatalf("analytics lost exact integer: %s", fields["analytics"])
	}
	if !strings.Contains(string(fields["desktop"]), `9007199254740993`) {
		t.Fatalf("desktop lost exact integer: %s", fields["desktop"])
	}

	var roundTripped Config
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("unmarshal canonical Config: %v", err)
	}
	reencoded, err := json.Marshal(roundTripped)
	if err != nil {
		t.Fatalf("remarshal canonical Config: %v", err)
	}
	if string(reencoded) != string(encoded) {
		t.Fatalf("Config canonical output changed:\nfirst: %s\nsecond: %s", encoded, reencoded)
	}
}

func TestPublicConfigAcceptsNullAndOpenAdditionalJSON(t *testing.T) {
	for _, input := range []string{
		`{"model":null}`,
		`{"desktop":{}}`,
		`{"analytics":null,"tools":null}`,
		`{"apps":{"untyped":true}}`,
		`{"future":null}`,
		`{"future":[9007199254740993,{"nested":true}]}`,
		`{"future":1,"future":2}`,
	} {
		var value Config
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Errorf("Config rejected %s: %v", input, err)
			continue
		}
		if _, err := json.Marshal(value); err != nil {
			t.Errorf("Config failed to marshal %s: %v", input, err)
		}
	}
}

func TestPublicConfigRejectsMalformedKnownFields(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{} {}`, `{} x`,
		`{"model":`, `{"future":1`,
		`{"model":1}`,
		`{"review_model":false}`,
		`{"model_context_window":9223372036854775808}`,
		`{"model_context_window":-9223372036854775809}`,
		`{"model_context_window":1.5}`,
		`{"model_context_window":1e3}`,
		`{"model_auto_compact_token_limit":"100"}`,
		`{"model_auto_compact_token_limit_scope":"bodyAfterPrefix"}`,
		`{"model_provider":[]}`,
		`{"approval_policy":"always"}`,
		`{"approvals_reviewer":"guardian"}`,
		`{"sandbox_mode":"workspace_write"}`,
		`{"sandbox_workspace_write":{"network_access":null}}`,
		`{"forced_chatgpt_workspace_id":["workspace",1]}`,
		`{"forced_login_method":"oauth"}`,
		`{"web_search":"enabled"}`,
		`{"tools":{"web_search":false}}`,
		`{"instructions":{}}`,
		`{"developer_instructions":[]}`,
		`{"compact_prompt":true}`,
		`{"model_reasoning_effort":""}`,
		`{"model_reasoning_summary":"brief"}`,
		`{"model_verbosity":"max"}`,
		`{"service_tier":1}`,
		`{"analytics":{"enabled":"true"}}`,
		`{"desktop":[]}`,
		`{"model":null,"model":"gpt-5"}`,
		`{"analytics":null,"analytics":{"enabled":true}}`,
	} {
		assertJSONRejects[Config](t, input)
	}
}

func TestPublicConfigDirectDecoderRejectsMalformedTokenStreams(t *testing.T) {
	for _, input := range []string{
		``,
		`{@`,
		`{"future"`,
		`{"model":`,
		`{"future":1`,
		`{} {}`,
		`{} x`,
	} {
		var value Config
		if err := value.UnmarshalJSON([]byte(input)); err == nil {
			t.Errorf("direct Config decoder accepted %q", input)
		}
	}
}

func TestPublicConfigMarshalRejectsInvalidAndConflictingValues(t *testing.T) {
	for name, value := range map[string]Config{
		"known additional field": {
			Additional: map[string]JsonValue{"model": {raw: json.RawMessage(`"shadow"`)}},
		},
		"invalid additional JSON": {
			Additional: map[string]JsonValue{"future": {}},
		},
		"invalid desktop JSON": {
			Desktop: map[string]JsonValue{"future": {}},
		},
		"invalid reasoning effort": {
			ModelReasoningEffort: func() *ReasoningEffort { value := ReasoningEffort(""); return &value }(),
		},
		"invalid verbosity": {
			ModelVerbosity: func() *Verbosity { value := Verbosity("max"); return &value }(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := json.Marshal(value); err == nil {
				t.Fatal("invalid Config marshaled")
			}
		})
	}
}

func TestPublicConfigNilReceiver(t *testing.T) {
	var value *Config
	if err := value.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil Config receiver succeeded")
	}
}

func TestPublicConfigRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "Config") || slices.Contains(binding.Result, "Config") {
			t.Fatalf("Config unexpectedly bound to %s", binding.Method)
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

func TestPublicConfigTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type Config = ({`,
		`"model_context_window": number | null;`,
		`"sandbox_workspace_write": SandboxWorkspaceWrite | null;`,
		`} & { [key in string]?: JsonValue });`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func publicConfigContractSchema() Schema {
	nullable := nullableConfigPrerequisiteSchema
	approvalsReviewer := nullable(Schema{"$ref": "#/$defs/ApprovalsReviewer"})
	approvalsReviewer["description"] = "[UNSTABLE] Optional default for where approval requests are routed for review."
	properties := Schema{
		"model":                                nullable(Schema{"type": "string"}),
		"review_model":                         nullable(Schema{"type": "string"}),
		"model_context_window":                 nullable(Schema{"type": "integer"}),
		"model_auto_compact_token_limit":       nullable(Schema{"type": "integer"}),
		"model_auto_compact_token_limit_scope": nullable(Schema{"$ref": "#/$defs/AutoCompactTokenLimitScope"}),
		"model_provider":                       nullable(Schema{"type": "string"}),
		"approval_policy":                      nullable(Schema{"$ref": "#/$defs/AskForApproval"}),
		"approvals_reviewer":                   approvalsReviewer,
		"sandbox_mode":                         nullable(Schema{"$ref": "#/$defs/SandboxMode"}),
		"sandbox_workspace_write":              nullable(Schema{"$ref": "#/$defs/SandboxWorkspaceWrite"}),
		"forced_chatgpt_workspace_id":          nullable(Schema{"$ref": "#/$defs/ForcedChatgptWorkspaceIds"}),
		"forced_login_method":                  nullable(Schema{"$ref": "#/$defs/ForcedLoginMethod"}),
		"web_search":                           nullable(Schema{"$ref": "#/$defs/WebSearchMode"}),
		"tools":                                nullable(Schema{"$ref": "#/$defs/ToolsV2"}),
		"instructions":                         nullable(Schema{"type": "string"}),
		"developer_instructions":               nullable(Schema{"type": "string"}),
		"compact_prompt":                       nullable(Schema{"type": "string"}),
		"model_reasoning_effort":               nullable(Schema{"$ref": "#/$defs/ReasoningEffort"}),
		"model_reasoning_summary":              nullable(Schema{"$ref": "#/$defs/ReasoningSummary"}),
		"model_verbosity":                      nullable(Schema{"$ref": "#/$defs/Verbosity"}),
		"service_tier":                         nullable(Schema{"type": "string"}),
		"analytics":                            nullable(Schema{"$ref": "#/$defs/AnalyticsConfig"}),
		"desktop": nullable(Schema{
			"type":                             "object",
			"additionalProperties":             Schema{"$ref": "#/$defs/JsonValue"},
			"x-gollem-typescript-optional-map": true,
		}),
	}
	return Schema{
		"type":                             "object",
		"properties":                       properties,
		"required":                         append([]string(nil), publicConfigFieldNames...),
		"additionalProperties":             Schema{"$ref": "#/$defs/JsonValue"},
		"x-gollem-typescript-optional-map": true,
	}
}

func emptyPublicConfigJSON() string {
	return `{"analytics":null,"approval_policy":null,"approvals_reviewer":null,"compact_prompt":null,"desktop":null,"developer_instructions":null,"forced_chatgpt_workspace_id":null,"forced_login_method":null,"instructions":null,"model":null,"model_auto_compact_token_limit":null,"model_auto_compact_token_limit_scope":null,"model_context_window":null,"model_provider":null,"model_reasoning_effort":null,"model_reasoning_summary":null,"model_verbosity":null,"review_model":null,"sandbox_mode":null,"sandbox_workspace_write":null,"service_tier":null,"tools":null,"web_search":null}`
}

var (
	_ json.Marshaler   = Config{}
	_ json.Unmarshaler = (*Config)(nil)
)

package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestConfigLayerSourceSchemaIsExact(t *testing.T) {
	definition := JSONSchema()["$defs"].(Schema)["ConfigLayerSource"].(Schema)
	variants, ok := definition["oneOf"].([]any)
	if !ok || len(variants) != 8 {
		t.Fatalf("ConfigLayerSource oneOf = %#v, want eight variants", definition["oneOf"])
	}
	want := map[string]struct {
		required []string
		refs     map[string]string
	}{
		"mdm":                             {required: []string{"type", "domain", "key"}},
		"system":                          {required: []string{"type", "file"}, refs: map[string]string{"file": "AbsolutePathBuf"}},
		"enterpriseManaged":               {required: []string{"type", "id", "name"}},
		"user":                            {required: []string{"type", "file", "profile"}, refs: map[string]string{"file": "AbsolutePathBuf"}},
		"project":                         {required: []string{"type", "dotCodexFolder"}, refs: map[string]string{"dotCodexFolder": "AbsolutePathBuf"}},
		"sessionFlags":                    {required: []string{"type"}},
		"legacyManagedConfigTomlFromFile": {required: []string{"type", "file"}, refs: map[string]string{"file": "AbsolutePathBuf"}},
		"legacyManagedConfigTomlFromMdm":  {required: []string{"type"}},
	}
	seen := map[string]bool{}
	for _, raw := range variants {
		variant := raw.(Schema)
		if variant["type"] != "object" || variant["additionalProperties"] != false {
			t.Fatalf("ConfigLayerSource variant is not a closed object: %#v", variant)
		}
		properties := variant["properties"].(Schema)
		typeSchema := properties["type"].(Schema)
		enums := typeSchema["enum"].([]any)
		if len(enums) != 1 {
			t.Fatalf("ConfigLayerSource discriminant = %#v", typeSchema)
		}
		kind := enums[0].(string)
		expected, found := want[kind]
		if !found {
			t.Fatalf("unexpected ConfigLayerSource variant %q", kind)
		}
		seen[kind] = true
		gotRequired := append([]string(nil), variant["required"].([]string)...)
		slices.Sort(gotRequired)
		wantRequired := append([]string(nil), expected.required...)
		slices.Sort(wantRequired)
		if !reflect.DeepEqual(gotRequired, wantRequired) {
			t.Errorf("%s required = %v, want %v", kind, gotRequired, wantRequired)
		}
		for field, ref := range expected.refs {
			if got := properties[field].(Schema)["$ref"]; got != "#/$defs/"+ref {
				t.Errorf("%s.%s ref = %v, want %s", kind, field, got, ref)
			}
		}
		if kind == "user" {
			assertConfigNullableSchema(t, properties["profile"], Schema{"type": "string"})
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("ConfigLayerSource variants = %v, want %v", seen, want)
	}
}

func TestConfigLayerSourceAcceptsExactWireForms(t *testing.T) {
	for _, test := range []struct {
		name      string
		input     string
		canonical string
		kind      string
	}{
		{"mdm", `{"type":"mdm","domain":"","key":""}`, `{"type":"mdm","domain":"","key":""}`, "mdm"},
		{"system", `{"type":"system","file":"/workspace/../workspace/system.toml"}`, `{"type":"system","file":"/workspace/system.toml"}`, "system"},
		{"enterprise", `{"type":"enterpriseManaged","id":"","name":"Managed"}`, `{"type":"enterpriseManaged","id":"","name":"Managed"}`, "enterpriseManaged"},
		{"user omitted profile", `{"type":"user","file":"/home/user/.codex/config.toml"}`, `{"type":"user","file":"/home/user/.codex/config.toml","profile":null}`, "user"},
		{"user null profile", `{"type":"user","file":"/home/user/.codex/config.toml","profile":null}`, `{"type":"user","file":"/home/user/.codex/config.toml","profile":null}`, "user"},
		{"user profile", `{"type":"user","file":"/home/user/.codex/config.toml","profile":""}`, `{"type":"user","file":"/home/user/.codex/config.toml","profile":""}`, "user"},
		{"project", `{"type":"project","dotCodexFolder":"/workspace/.codex"}`, `{"type":"project","dotCodexFolder":"/workspace/.codex"}`, "project"},
		{"session", `{"type":"sessionFlags"}`, `{"type":"sessionFlags"}`, "sessionFlags"},
		{"legacy file", `{"type":"legacyManagedConfigTomlFromFile","file":"/etc/codex/managed_config.toml"}`, `{"type":"legacyManagedConfigTomlFromFile","file":"/etc/codex/managed_config.toml"}`, "legacyManagedConfigTomlFromFile"},
		{"legacy MDM", `{"type":"legacyManagedConfigTomlFromMdm"}`, `{"type":"legacyManagedConfigTomlFromMdm"}`, "legacyManagedConfigTomlFromMdm"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value ConfigLayerSource
			if err := json.Unmarshal([]byte(test.input), &value); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			if value.Type() != test.kind {
				t.Errorf("Type() = %q, want %q", value.Type(), test.kind)
			}
			got, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			if string(got) != test.canonical {
				t.Errorf("canonical JSON = %s, want %s", got, test.canonical)
			}
		})
	}
}

func TestConfigLayerSourceRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"mdm"`, `1`, `true`, `{}`,
		`{"type":"unknown"}`,
		`{"type":"mdm","domain":"d"}`,
		`{"type":"mdm","domain":1,"key":"k"}`,
		`{"type":"mdm","domain":"d","key":1}`,
		`{"type":"mdm","domain":"d","key":"k","extra":true}`,
		`{"type":"system"}`,
		`{"type":"system","file":"relative.toml"}`,
		`{"type":"enterpriseManaged","id":"id"}`,
		`{"type":"enterpriseManaged","id":1,"name":"Managed"}`,
		`{"type":"enterpriseManaged","id":"id","name":null}`,
		`{"type":"user","file":null,"profile":null}`,
		`{"type":"user","file":"/config.toml","profile":1}`,
		`{"type":"user","file":"/config.toml","profile":null,"domain":"d"}`,
		`{"type":"project","dotCodexFolder":"relative"}`,
		`{"type":"sessionFlags","file":"/config.toml"}`,
		`{"type":"legacyManagedConfigTomlFromFile"}`,
		`{"type":"legacyManagedConfigTomlFromMdm","file":"/config.toml"}`,
		`{"type":"project","file":"/workspace/.codex"}`,
		`{"type":"sessionFlags"} {}`,
	} {
		assertJSONRejects[ConfigLayerSource](t, input)
	}
}

func TestConfigLayerSourceNilReceiverAndEmptyMarshal(t *testing.T) {
	var target *ConfigLayerSource
	if err := target.UnmarshalJSON([]byte(`{"type":"sessionFlags"}`)); err == nil {
		t.Fatal("nil ConfigLayerSource receiver succeeded")
	}
	if _, err := json.Marshal(ConfigLayerSource{}); err == nil {
		t.Fatal("empty ConfigLayerSource marshal succeeded")
	}
}

func TestConfigLayerSourceRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ConfigLayerSource") || slices.Contains(binding.Result, "ConfigLayerSource") {
			t.Fatalf("ConfigLayerSource unexpectedly bound to %s", binding.Method)
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

func TestConfigLayerSourceTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ConfigLayerSource =`,
		`"type": "mdm";`,
		`"type": "system";`,
		`"type": "enterpriseManaged";`,
		`"type": "user";`,
		`"profile": string | null;`,
		`"type": "project";`,
		`"dotCodexFolder": AbsolutePathBuf;`,
		`"type": "sessionFlags";`,
		`"type": "legacyManagedConfigTomlFromFile";`,
		`"type": "legacyManagedConfigTomlFromMdm";`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

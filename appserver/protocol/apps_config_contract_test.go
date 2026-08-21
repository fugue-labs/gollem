package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAppsConfigSchemasAreExact(t *testing.T) {
	wantDefault := Schema{
		"type": "object",
		"properties": Schema{
			"approvals_reviewer":          nullableSchemaRef("ApprovalsReviewer"),
			"default_tools_approval_mode": nullableSchemaRef("AppToolApproval"),
			"destructive_enabled":         Schema{"default": true, "type": "boolean"},
			"enabled":                     Schema{"default": true, "type": "boolean"},
			"open_world_enabled":          Schema{"default": true, "type": "boolean"},
		},
	}
	wantApps := Schema{
		"type": "object",
		"properties": Schema{
			"_default": Schema{
				"anyOf":   []any{Schema{"$ref": "#/$defs/AppsDefaultConfig"}, Schema{"type": "null"}},
				"default": nil,
			},
		},
	}
	definitions := JSONSchema()["$defs"].(Schema)
	for name, want := range map[string]Schema{
		"AppsDefaultConfig": wantDefault,
		"AppsConfig":        wantApps,
	} {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestAppsDefaultConfigAcceptsSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{
			`{}`,
			`{"approvals_reviewer":null,"default_tools_approval_mode":null,"destructive_enabled":true,"enabled":true,"open_world_enabled":true}`,
		},
		{
			`{"enabled":false,"approvals_reviewer":"auto_review","destructive_enabled":false,"open_world_enabled":false,"default_tools_approval_mode":"prompt"}`,
			`{"approvals_reviewer":"auto_review","default_tools_approval_mode":"prompt","destructive_enabled":false,"enabled":false,"open_world_enabled":false}`,
		},
		{
			`{"future":1,"future":2,"approvals_reviewer":null,"default_tools_approval_mode":null}`,
			`{"approvals_reviewer":null,"default_tools_approval_mode":null,"destructive_enabled":true,"enabled":true,"open_world_enabled":true}`,
		},
	} {
		var value AppsDefaultConfig
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestAppsConfigAcceptsFlattenedMapWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{}`, `{"_default":null}`},
		{`{"_default":null}`, `{"_default":null}`},
		{
			`{"_default":{"enabled":false},"":{"enabled":false}," ":{},"repos":{"tools":{"search":{"enabled":true}}}}`,
			`{"_default":{"approvals_reviewer":null,"default_tools_approval_mode":null,"destructive_enabled":true,"enabled":false,"open_world_enabled":true},"":{"approvals_reviewer":null,"default_tools_approval_mode":null,"default_tools_enabled":null,"destructive_enabled":null,"enabled":false,"open_world_enabled":null,"tools":null}," ":{"approvals_reviewer":null,"default_tools_approval_mode":null,"default_tools_enabled":null,"destructive_enabled":null,"enabled":true,"open_world_enabled":null,"tools":null},"repos":{"approvals_reviewer":null,"default_tools_approval_mode":null,"default_tools_enabled":null,"destructive_enabled":null,"enabled":true,"open_world_enabled":null,"tools":{"search":{"approval_mode":null,"enabled":true}}}}`,
		},
		{
			`{"app":{"enabled":false},"app":{"enabled":true,"approvals_reviewer":"user"}}`,
			`{"_default":null,"app":{"approvals_reviewer":"user","default_tools_approval_mode":null,"default_tools_enabled":null,"destructive_enabled":null,"enabled":true,"open_world_enabled":null,"tools":null}}`,
		},
	} {
		var value AppsConfig
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestAppsConfigsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"enabled":null}`, `{"destructive_enabled":null}`, `{"open_world_enabled":null}`,
		`{"enabled":0}`, `{"destructive_enabled":"true"}`, `{"open_world_enabled":0}`,
		`{"approvals_reviewer":"other"}`, `{"default_tools_approval_mode":"other"}`,
		`{"enabled":true,"enabled":false}`,
		`{"approvals_reviewer":null,"approvals_reviewer":"user"}`, `{} {}`, `{} x`,
	} {
		assertJSONRejects[AppsDefaultConfig](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"_default":[]}`, `{"_default":{"enabled":null}}`,
		`{"_default":null,"_default":null}`, `{"app":null}`, `{"app":[]}`,
		`{"app":{"enabled":null}}`, `{"app":{"approvals_reviewer":"other"}}`,
		`{"app":{"enabled":true,"enabled":false}}`, `{} {}`, `{} x`,
		`{"app":{"enabled":null},"app":{"enabled":true}}`,
	} {
		assertJSONRejects[AppsConfig](t, input)
	}
}

func TestAppsConfigsNilReceiversAndInvalidMarshal(t *testing.T) {
	var defaults *AppsDefaultConfig
	if err := defaults.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil AppsDefaultConfig receiver succeeded")
	}
	var apps *AppsConfig
	if err := apps.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil AppsConfig receiver succeeded")
	}
	invalidReviewer := ApprovalsReviewer("other")
	if _, err := json.Marshal(AppsDefaultConfig{ApprovalsReviewer: &invalidReviewer}); err == nil {
		t.Fatal("AppsDefaultConfig with invalid reviewer marshaled")
	}
	invalidApproval := AppToolApproval("other")
	if _, err := json.Marshal(AppsDefaultConfig{DefaultToolsApprovalMode: &invalidApproval}); err == nil {
		t.Fatal("AppsDefaultConfig with invalid approval mode marshaled")
	}
	if _, err := json.Marshal(AppsConfig{Apps: map[string]AppConfig{
		"app": {ApprovalsReviewer: &invalidReviewer},
	}}); err == nil {
		t.Fatal("AppsConfig with invalid nested app marshaled")
	}
	if _, err := json.Marshal(AppsConfig{Default: &AppsDefaultConfig{
		DefaultToolsApprovalMode: &invalidApproval,
	}}); err == nil {
		t.Fatal("AppsConfig with invalid default marshaled")
	}
}

func TestAppsConfigDirectDecoderErrors(t *testing.T) {
	for _, input := range []string{
		``, `{"`, `{"app":{}`, `{"_default":[]}`, `{} {}`, `{} x`,
	} {
		var value AppsConfig
		if err := value.UnmarshalJSON([]byte(input)); err == nil {
			t.Errorf("direct AppsConfig.UnmarshalJSON from %s succeeded", input)
		}
	}
}

func TestAppsConfigMarshalPreservesSerdeReservedKeyCollision(t *testing.T) {
	encoded, err := json.Marshal(AppsConfig{Apps: map[string]AppConfig{
		"_default": {Enabled: true},
	}})
	want := `{"_default":null,"_default":{"approvals_reviewer":null,"default_tools_approval_mode":null,"default_tools_enabled":null,"destructive_enabled":null,"enabled":true,"open_world_enabled":null,"tools":null}}`
	if err != nil || string(encoded) != want {
		t.Fatalf("reserved collision = %s, %v; want %s", encoded, err, want)
	}
	assertJSONRejects[AppsConfig](t, string(encoded))
}

func TestAppsConfigsRemainStandaloneAndUnbound(t *testing.T) {
	names := []string{"AppsDefaultConfig", "AppsConfig"}
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
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestAppsConfigsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	wants := []string{
		"export type AppsDefaultConfig = {\n" +
			"  \"approvals_reviewer\": ApprovalsReviewer | null;\n" +
			"  \"default_tools_approval_mode\": AppToolApproval | null;\n" +
			"  \"destructive_enabled\": boolean;\n" +
			"  \"enabled\": boolean;\n" +
			"  \"open_world_enabled\": boolean;\n" +
			"};",
		"export type AppsConfig = ({\n" +
			"  \"_default\": AppsDefaultConfig | null;\n" +
			"} & { [key in string]?: AppConfig });",
	}
	for _, want := range wants {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = AppsDefaultConfig{}
	_ json.Unmarshaler = (*AppsDefaultConfig)(nil)
	_ json.Marshaler   = AppsConfig{}
	_ json.Unmarshaler = (*AppsConfig)(nil)
)

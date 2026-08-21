package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAppConfigSchemaIsExact(t *testing.T) {
	want := Schema{
		"type": "object",
		"properties": Schema{
			"approvals_reviewer":          nullableSchemaRef("ApprovalsReviewer"),
			"default_tools_approval_mode": nullableSchemaRef("AppToolApproval"),
			"default_tools_enabled":       Schema{"type": []any{"boolean", "null"}},
			"destructive_enabled":         Schema{"type": []any{"boolean", "null"}},
			"enabled":                     Schema{"default": true, "type": "boolean"},
			"open_world_enabled":          Schema{"type": []any{"boolean", "null"}},
			"tools":                       nullableSchemaRef("AppToolsConfig"),
		},
	}
	got := JSONSchema()["$defs"].(Schema)["AppConfig"]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AppConfig = %#v, want %#v", got, want)
	}
}

func TestAppConfigAcceptsSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{
			`{}`,
			`{"approvals_reviewer":null,"default_tools_approval_mode":null,"default_tools_enabled":null,"destructive_enabled":null,"enabled":true,"open_world_enabled":null,"tools":null}`,
		},
		{
			`{"enabled":false,"approvals_reviewer":"guardian_subagent","destructive_enabled":true,"open_world_enabled":false,"default_tools_approval_mode":"writes","default_tools_enabled":true,"tools":{"repos/list":{"enabled":false,"approval_mode":"prompt"}}}`,
			`{"approvals_reviewer":"guardian_subagent","default_tools_approval_mode":"writes","default_tools_enabled":true,"destructive_enabled":true,"enabled":false,"open_world_enabled":false,"tools":{"repos/list":{"approval_mode":"prompt","enabled":false}}}`,
		},
		{
			`{"future":1,"future":2,"enabled":true,"approvals_reviewer":null,"destructive_enabled":null,"open_world_enabled":null,"default_tools_approval_mode":null,"default_tools_enabled":null,"tools":null}`,
			`{"approvals_reviewer":null,"default_tools_approval_mode":null,"default_tools_enabled":null,"destructive_enabled":null,"enabled":true,"open_world_enabled":null,"tools":null}`,
		},
		{
			`{"tools":{"":{"future":true}," ":{"enabled":true},"duplicate":{"enabled":false},"duplicate":{"approval_mode":"approve"}}}`,
			`{"approvals_reviewer":null,"default_tools_approval_mode":null,"default_tools_enabled":null,"destructive_enabled":null,"enabled":true,"open_world_enabled":null,"tools":{"":{"approval_mode":null,"enabled":null}," ":{"approval_mode":null,"enabled":true},"duplicate":{"approval_mode":"approve","enabled":null}}}`,
		},
	} {
		var value AppConfig
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

func TestAppConfigRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"enabled":null}`, `{"enabled":0}`, `{"enabled":"true"}`,
		`{"approvals_reviewer":"other"}`, `{"approvals_reviewer":true}`,
		`{"destructive_enabled":0}`, `{"open_world_enabled":"false"}`,
		`{"default_tools_approval_mode":"other"}`, `{"default_tools_approval_mode":true}`,
		`{"default_tools_enabled":0}`, `{"tools":[]}`, `{"tools":{"tool":null}}`,
		`{"tools":{"tool":{"enabled":0}}}`, `{"tools":{"tool":{"approval_mode":"other"}}}`,
		`{"enabled":true,"enabled":false}`,
		`{"approvals_reviewer":null,"approvals_reviewer":"user"}`,
		`{"tools":null,"tools":{}}`, `{} {}`, `{} x`,
	} {
		assertJSONRejects[AppConfig](t, input)
	}
}

func TestAppConfigNilReceiverAndInvalidMarshalFailClosed(t *testing.T) {
	var config *AppConfig
	if err := config.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil AppConfig receiver succeeded")
	}
	invalidReviewer := ApprovalsReviewer("other")
	if _, err := json.Marshal(AppConfig{Enabled: true, ApprovalsReviewer: &invalidReviewer}); err == nil {
		t.Fatal("AppConfig with invalid reviewer marshaled")
	}
	invalidApproval := AppToolApproval("other")
	if _, err := json.Marshal(AppConfig{Enabled: true, DefaultToolsApprovalMode: &invalidApproval}); err == nil {
		t.Fatal("AppConfig with invalid approval mode marshaled")
	}
	if _, err := json.Marshal(AppConfig{Enabled: true, Tools: &AppToolsConfig{Tools: map[string]AppToolConfig{
		"tool": {ApprovalMode: &invalidApproval},
	}}}); err == nil {
		t.Fatal("AppConfig with invalid nested tool config marshaled")
	}
}

func TestAppConfigRemainsStandaloneAndUnbound(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "AppConfig") || slices.Contains(binding.Result, "AppConfig") {
			t.Fatalf("AppConfig unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "AppConfig" {
			t.Fatalf("AppConfig unexpectedly bound to item %s", binding.Kind)
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

func TestAppConfigTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type AppConfig = {\n" +
		"  \"approvals_reviewer\": ApprovalsReviewer | null;\n" +
		"  \"default_tools_approval_mode\": AppToolApproval | null;\n" +
		"  \"default_tools_enabled\": boolean | null;\n" +
		"  \"destructive_enabled\": boolean | null;\n" +
		"  \"enabled\": boolean;\n" +
		"  \"open_world_enabled\": boolean | null;\n" +
		"  \"tools\": AppToolsConfig | null;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = AppConfig{}
	_ json.Unmarshaler = (*AppConfig)(nil)
)

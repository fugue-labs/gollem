package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAppToolConfigSchemasAreExact(t *testing.T) {
	wantTool := Schema{
		"type": "object",
		"properties": Schema{
			"approval_mode": nullableSchemaRef("AppToolApproval"),
			"enabled":       Schema{"type": []any{"boolean", "null"}},
		},
	}
	wantTools := Schema{"type": "object"}
	definitions := JSONSchema()["$defs"].(Schema)
	for name, want := range map[string]Schema{
		"AppToolConfig":  wantTool,
		"AppToolsConfig": wantTools,
	} {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestAppToolConfigAcceptsSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{}`, `{"approval_mode":null,"enabled":null}`},
		{`{"enabled":null,"approval_mode":null}`, `{"approval_mode":null,"enabled":null}`},
		{`{"enabled":true,"approval_mode":"writes"}`, `{"approval_mode":"writes","enabled":true}`},
		{`{"future":{"ignored":true},"enabled":false,"approval_mode":"approve"}`, `{"approval_mode":"approve","enabled":false}`},
	} {
		var value AppToolConfig
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

func TestAppToolsConfigAcceptsFlattenedMapWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{}`, `{}`},
		{`{"repos/list":{}}`, `{"repos/list":{"approval_mode":null,"enabled":null}}`},
		{`{"":{"enabled":true}," ":{"approval_mode":"prompt"},"opaque tool":{"enabled":null,"approval_mode":null}}`, `{"":{"approval_mode":null,"enabled":true}," ":{"approval_mode":"prompt","enabled":null},"opaque tool":{"approval_mode":null,"enabled":null}}`},
		{`{"tool":{"future":1,"future":2}}`, `{"tool":{"approval_mode":null,"enabled":null}}`},
		{`{"duplicate":{"enabled":false},"duplicate":{"enabled":true,"approval_mode":"auto"}}`, `{"duplicate":{"approval_mode":"auto","enabled":true}}`},
	} {
		var value AppToolsConfig
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

func TestAppToolConfigsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"enabled":0}`, `{"enabled":"true"}`, `{"approval_mode":"other"}`,
		`{"approval_mode":true}`, `{"enabled":true,"enabled":false}`,
		`{"approval_mode":null,"approval_mode":"auto"}`, `{} {}`, `{} x`,
	} {
		assertJSONRejects[AppToolConfig](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"tool":null}`, `{"tool":[]}`, `{"tool":"value"}`, `{"tool":1}`,
		`{"tool":{"enabled":0}}`, `{"tool":{"approval_mode":"other"}}`,
		`{"tool":{"enabled":true,"enabled":false}}`, `{} {}`, `{} x`,
		`{"tool":{"enabled":0},"tool":{"enabled":true}}`,
	} {
		assertJSONRejects[AppToolsConfig](t, input)
	}
}

func TestAppToolsConfigDirectDecoderErrors(t *testing.T) {
	for _, input := range []string{``, `{"`, `{"tool":{}`, `{} {}`, `{} x`} {
		var value AppToolsConfig
		if err := value.UnmarshalJSON([]byte(input)); err == nil {
			t.Errorf("direct AppToolsConfig.UnmarshalJSON from %s succeeded", input)
		}
	}
}

func TestAppToolConfigsNilReceiversAndNilMapCanonicalizeSafely(t *testing.T) {
	var tool *AppToolConfig
	if err := tool.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil AppToolConfig receiver succeeded")
	}
	var tools *AppToolsConfig
	if err := tools.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil AppToolsConfig receiver succeeded")
	}
	encoded, err := json.Marshal(AppToolsConfig{})
	if err != nil || string(encoded) != `{}` {
		t.Fatalf("nil tool map marshaled as %s, %v; want {}", encoded, err)
	}
	invalid := AppToolApproval("other")
	if _, err := json.Marshal(AppToolConfig{ApprovalMode: &invalid}); err == nil {
		t.Fatal("AppToolConfig with invalid approval mode marshaled")
	}
	if _, err := json.Marshal(AppToolsConfig{Tools: map[string]AppToolConfig{
		"tool": {ApprovalMode: &invalid},
	}}); err == nil {
		t.Fatal("AppToolsConfig with invalid approval mode marshaled")
	}
}

func TestAppToolConfigsRemainStandaloneAndUnbound(t *testing.T) {
	names := []string{"AppToolConfig", "AppToolsConfig"}
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

func TestAppToolConfigsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	wantTool := "export type AppToolConfig = {\n" +
		"  \"approval_mode\": AppToolApproval | null;\n" +
		"  \"enabled\": boolean | null;\n" +
		"};"
	wantTools := "export type AppToolsConfig = { [key in string]?: {\n" +
		"  \"approval_mode\": AppToolApproval | null;\n" +
		"  \"enabled\": boolean | null;\n" +
		"} };"
	for _, want := range []string{wantTool, wantTools} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = AppToolConfig{}
	_ json.Unmarshaler = (*AppToolConfig)(nil)
	_ json.Marshaler   = AppToolsConfig{}
	_ json.Unmarshaler = (*AppToolsConfig)(nil)
)

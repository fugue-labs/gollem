package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestConfigRequirementsSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range map[string][]any{
		"ResidencyRequirement":    {"us"},
		"WebSearchMode":           {"disabled", "cached", "indexed", "live"},
		"WindowsSandboxSetupMode": {"elevated", "unelevated"},
	} {
		if got := defs[name].(Schema); got["type"] != "string" || !reflect.DeepEqual(got["enum"], want) {
			t.Errorf("%s schema = %#v, want string enum %v", name, got, want)
		}
	}

	computerUse := defs["ComputerUseRequirements"].(Schema)
	assertClosedObjectSchema(t, computerUse, "allowLockedComputerUse")
	assertConfigNullableSchema(t, computerUse["properties"].(Schema)["allowLockedComputerUse"], Schema{"type": "boolean"})

	requirements := defs["ConfigRequirements"].(Schema)
	fields := []string{
		"allowedApprovalPolicies", "allowedSandboxModes", "allowedWindowsSandboxImplementations",
		"allowedPermissionProfiles", "defaultPermissions", "allowedWebSearchModes",
		"allowManagedHooksOnly", "allowAppshots", "allowRemoteControl", "computerUse",
		"featureRequirements", "enforceResidency", "models",
	}
	assertClosedObjectSchema(t, requirements, fields...)
	properties := requirements["properties"].(Schema)
	assertConfigNullableSchema(t, properties["allowedApprovalPolicies"], configRequirementsArraySchema("AskForApproval"))
	assertConfigNullableSchema(t, properties["allowedSandboxModes"], configRequirementsArraySchema("SandboxMode"))
	assertConfigNullableSchema(t, properties["allowedWindowsSandboxImplementations"], configRequirementsArraySchema("WindowsSandboxSetupMode"))
	assertConfigNullableSchema(t, properties["allowedPermissionProfiles"], configRequirementsBoolMapSchema())
	assertConfigNullableSchema(t, properties["defaultPermissions"], Schema{"type": "string"})
	assertConfigNullableSchema(t, properties["allowedWebSearchModes"], configRequirementsArraySchema("WebSearchMode"))
	for _, field := range []string{"allowManagedHooksOnly", "allowAppshots", "allowRemoteControl"} {
		assertConfigNullableSchema(t, properties[field], Schema{"type": "boolean"})
	}
	assertConfigNullableSchema(t, properties["computerUse"], Schema{"$ref": "#/$defs/ComputerUseRequirements"})
	assertConfigNullableSchema(t, properties["featureRequirements"], configRequirementsBoolMapSchema())
	assertConfigNullableSchema(t, properties["enforceResidency"], Schema{"$ref": "#/$defs/ResidencyRequirement"})
	assertConfigNullableSchema(t, properties["models"], Schema{"$ref": "#/$defs/ModelsRequirements"})

	response := defs["ConfigRequirementsReadResponse"].(Schema)
	assertClosedObjectSchema(t, response, "requirements")
	assertConfigNullableSchema(t, response["properties"].(Schema)["requirements"], Schema{"$ref": "#/$defs/ConfigRequirements"})
}

func TestConfigRequirementLeafValuesAreExact(t *testing.T) {
	for _, value := range []ResidencyRequirement{ResidencyRequirementUS} {
		assertConfigRequirementEnumRoundTrip(t, value)
	}
	for _, value := range []WebSearchMode{
		WebSearchModeDisabled, WebSearchModeCached, WebSearchModeIndexed, WebSearchModeLive,
	} {
		assertConfigRequirementEnumRoundTrip(t, value)
	}
	for _, value := range []WindowsSandboxSetupMode{
		WindowsSandboxSetupModeElevated, WindowsSandboxSetupModeUnelevated,
	} {
		assertConfigRequirementEnumRoundTrip(t, value)
	}

	for _, input := range []string{`null`, `"eu"`, `""`, `1`, `true`, `[]`, `"us" {}`} {
		assertJSONRejects[ResidencyRequirement](t, input)
	}
	for _, input := range []string{`null`, `"remote"`, `""`, `1`, `true`, `[]`, `"live" {}`} {
		assertJSONRejects[WebSearchMode](t, input)
	}
	for _, input := range []string{`null`, `"admin"`, `""`, `1`, `true`, `[]`, `"elevated" {}`} {
		assertJSONRejects[WindowsSandboxSetupMode](t, input)
	}
}

func TestComputerUseRequirementsAcceptAndCanonicalizeRustOptions(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`{}`, `{"allowLockedComputerUse":null}`},
		{`{"allowLockedComputerUse":null}`, `{"allowLockedComputerUse":null}`},
		{`{"allowLockedComputerUse":false}`, `{"allowLockedComputerUse":false}`},
		{`{"allowLockedComputerUse":true}`, `{"allowLockedComputerUse":true}`},
	}
	for _, tc := range cases {
		var value ComputerUseRequirements
		assertConfigRequirementsRoundTrip(t, tc.input, tc.want, &value)
	}
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`,
		`{"allowLockedComputerUse":0}`,
		`{"allowLockedComputerUse":"false"}`,
		`{"allowLockedComputerUse":null,"extra":true}`,
		`{"allowLockedComputerUse":null} {}`,
	} {
		assertJSONRejects[ComputerUseRequirements](t, input)
	}
}

func TestConfigRequirementsAcceptExactWireForms(t *testing.T) {
	nullCanonical := `{"allowedApprovalPolicies":null,"allowedSandboxModes":null,"allowedWindowsSandboxImplementations":null,"allowedPermissionProfiles":null,"defaultPermissions":null,"allowedWebSearchModes":null,"allowManagedHooksOnly":null,"allowAppshots":null,"allowRemoteControl":null,"computerUse":null,"featureRequirements":null,"enforceResidency":null,"models":null}`
	var omitted ConfigRequirements
	assertConfigRequirementsRoundTrip(t, `{}`, nullCanonical, &omitted)
	var explicitNull ConfigRequirements
	assertConfigRequirementsRoundTrip(t, nullCanonical, nullCanonical, &explicitNull)

	full := `{"allowedApprovalPolicies":["untrusted",{"granular":{"sandbox_approval":true,"rules":false,"skill_approval":true,"request_permissions":false,"mcp_elicitations":true}}],"allowedSandboxModes":["read-only","workspace-write","danger-full-access"],"allowedWindowsSandboxImplementations":["elevated","unelevated"],"allowedPermissionProfiles":{"":false,"strict":true},"defaultPermissions":"","allowedWebSearchModes":["disabled","cached","indexed","live"],"allowManagedHooksOnly":false,"allowAppshots":true,"allowRemoteControl":false,"computerUse":{"allowLockedComputerUse":false},"featureRequirements":{"":false,"feature":true},"enforceResidency":"us","models":{"newThread":{"model":"","modelReasoningEffort":"custom","serviceTier":""}}}`
	var fullValue ConfigRequirements
	assertConfigRequirementsRoundTrip(t, full, full, &fullValue)

	partialWant := `{"allowedApprovalPolicies":null,"allowedSandboxModes":null,"allowedWindowsSandboxImplementations":null,"allowedPermissionProfiles":null,"defaultPermissions":null,"allowedWebSearchModes":null,"allowManagedHooksOnly":null,"allowAppshots":null,"allowRemoteControl":null,"computerUse":{"allowLockedComputerUse":null},"featureRequirements":null,"enforceResidency":null,"models":{"newThread":null}}`
	var partial ConfigRequirements
	assertConfigRequirementsRoundTrip(t, `{"computerUse":{},"models":{}}`, partialWant, &partial)
}

func TestConfigRequirementsRejectMalformedWireForms(t *testing.T) {
	invalid := []string{
		`null`, `[]`, `"value"`, `1`, `true`,
		`{"allowedApprovalPolicies":{}}`,
		`{"allowedApprovalPolicies":[null]}`,
		`{"allowedApprovalPolicies":["always"]}`,
		`{"allowedSandboxModes":[null]}`,
		`{"allowedSandboxModes":["readOnly"]}`,
		`{"allowedWindowsSandboxImplementations":[null]}`,
		`{"allowedWindowsSandboxImplementations":["admin"]}`,
		`{"allowedPermissionProfiles":[]}`,
		`{"allowedPermissionProfiles":{"profile":null}}`,
		`{"allowedPermissionProfiles":{"profile":"true"}}`,
		`{"defaultPermissions":false}`,
		`{"allowedWebSearchModes":[null]}`,
		`{"allowedWebSearchModes":["remote"]}`,
		`{"allowManagedHooksOnly":0}`,
		`{"allowAppshots":"true"}`,
		`{"allowRemoteControl":[]}`,
		`{"computerUse":false}`,
		`{"computerUse":{"extra":true}}`,
		`{"featureRequirements":{"feature":null}}`,
		`{"enforceResidency":"eu"}`,
		`{"models":{"extra":true}}`,
		`{"extra":true}`,
		`{} {}`,
	}
	for _, input := range invalid {
		assertJSONRejects[ConfigRequirements](t, input)
	}
}

func TestConfigRequirementsReadResponseIsExact(t *testing.T) {
	var omitted ConfigRequirementsReadResponse
	assertConfigRequirementsRoundTrip(t, `{}`, `{"requirements":null}`, &omitted)
	var explicitNull ConfigRequirementsReadResponse
	assertConfigRequirementsRoundTrip(t, `{"requirements":null}`, `{"requirements":null}`, &explicitNull)
	var nested ConfigRequirementsReadResponse
	assertConfigRequirementsRoundTrip(
		t,
		`{"requirements":{"allowRemoteControl":false}}`,
		`{"requirements":{"allowedApprovalPolicies":null,"allowedSandboxModes":null,"allowedWindowsSandboxImplementations":null,"allowedPermissionProfiles":null,"defaultPermissions":null,"allowedWebSearchModes":null,"allowManagedHooksOnly":null,"allowAppshots":null,"allowRemoteControl":false,"computerUse":null,"featureRequirements":null,"enforceResidency":null,"models":null}}`,
		&nested,
	)
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`,
		`{"requirements":false}`,
		`{"requirements":{"extra":true}}`,
		`{"requirements":null,"extra":true}`,
		`{"requirements":null} {}`,
	} {
		assertJSONRejects[ConfigRequirementsReadResponse](t, input)
	}
}

func TestConfigRequirementsNilReceiversAndInvalidMarshal(t *testing.T) {
	var computerUse *ComputerUseRequirements
	if err := computerUse.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ComputerUseRequirements receiver succeeded")
	}
	var requirements *ConfigRequirements
	if err := requirements.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ConfigRequirements receiver succeeded")
	}
	var response *ConfigRequirementsReadResponse
	if err := response.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ConfigRequirementsReadResponse receiver succeeded")
	}
	var residency *ResidencyRequirement
	if err := residency.UnmarshalJSON([]byte(`"us"`)); err == nil {
		t.Fatal("nil ResidencyRequirement receiver succeeded")
	}
	var webSearch *WebSearchMode
	if err := webSearch.UnmarshalJSON([]byte(`"live"`)); err == nil {
		t.Fatal("nil WebSearchMode receiver succeeded")
	}
	var windows *WindowsSandboxSetupMode
	if err := windows.UnmarshalJSON([]byte(`"elevated"`)); err == nil {
		t.Fatal("nil WindowsSandboxSetupMode receiver succeeded")
	}

	invalidWebSearch := []WebSearchMode{"remote"}
	if _, err := json.Marshal(ConfigRequirements{AllowedWebSearchModes: &invalidWebSearch}); err == nil {
		t.Fatal("invalid nested web-search mode marshal succeeded")
	}
	if _, err := json.Marshal(ResidencyRequirement("eu")); err == nil {
		t.Fatal("invalid residency requirement marshal succeeded")
	}
	if _, err := json.Marshal(WindowsSandboxSetupMode("admin")); err == nil {
		t.Fatal("invalid Windows sandbox setup mode marshal succeeded")
	}
}

func TestConfigRequirementsContractsRemainStandalone(t *testing.T) {
	names := []string{
		"ComputerUseRequirements", "ConfigRequirements", "ConfigRequirementsReadResponse",
		"ResidencyRequirement", "WebSearchMode", "WindowsSandboxSetupMode",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	info, ok := LookupMethod("configRequirements/read")
	if !ok || info.State != MethodImplemented {
		t.Fatalf("configRequirements/read = %#v, %v; want implemented", info, ok)
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

func TestConfigRequirementsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ComputerUseRequirements = {`,
		`"allowLockedComputerUse": boolean | null;`,
		`export type ResidencyRequirement = "us";`,
		`export type WebSearchMode = "disabled" | "cached" | "indexed" | "live";`,
		`export type WindowsSandboxSetupMode = "elevated" | "unelevated";`,
		`export type ConfigRequirements = {`,
		`"allowedApprovalPolicies": Array<AskForApproval> | null;`,
		`"allowedPermissionProfiles": { [key in string]?: boolean } | null;`,
		`"featureRequirements": { [key in string]?: boolean } | null;`,
		`"models": ModelsRequirements | null;`,
		`export type ConfigRequirementsReadResponse = {`,
		`"requirements": ConfigRequirements | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertConfigNullableSchema(t *testing.T, raw any, want Schema) {
	t.Helper()
	schema, ok := raw.(Schema)
	if !ok {
		t.Fatalf("nullable schema = %#v", raw)
	}
	variants, ok := schema["anyOf"].([]any)
	if !ok || len(variants) != 2 || !reflect.DeepEqual(variants[0], want) ||
		!reflect.DeepEqual(variants[1], Schema{"type": "null"}) {
		t.Fatalf("nullable schema = %#v, want %#v/null", schema, want)
	}
}

func configRequirementsArraySchema(item string) Schema {
	return Schema{"type": "array", "items": Schema{"$ref": "#/$defs/" + item}}
}

func configRequirementsBoolMapSchema() Schema {
	return Schema{
		"type": "object", "additionalProperties": Schema{"type": "boolean"},
		"x-gollem-typescript-optional-map": true,
	}
}

func assertConfigRequirementEnumRoundTrip[T interface {
	~string
	json.Marshaler
}](t *testing.T, value T) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != `"`+string(value)+`"` {
		t.Fatalf("Marshal(%q) = %s, %v", value, encoded, err)
	}
	var decoded T
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != value {
		t.Fatalf("Unmarshal(%s) = %q, %v", encoded, decoded, err)
	}
}

func assertConfigRequirementsRoundTrip(t *testing.T, input, want string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(input), target); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(target)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

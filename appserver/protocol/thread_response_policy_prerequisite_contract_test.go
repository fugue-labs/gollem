package protocol

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadResponsePolicyPrerequisiteSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	for name, values := range map[string][]string{
		"ApprovalsReviewer": {"user", "auto_review", "guardian_subagent"},
		"NetworkAccess":     {"restricted", "enabled"},
	} {
		definition, ok := defs[name].(Schema)
		if !ok {
			t.Fatalf("$defs missing %s", name)
		}
		if want := stringEnumSchema(values...); !reflect.DeepEqual(definition, want) {
			t.Errorf("%s schema = %#v, want %#v", name, definition, want)
		}
	}

	granular := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": Schema{
			"sandbox_approval":    Schema{"type": "boolean"},
			"rules":               Schema{"type": "boolean"},
			"skill_approval":      Schema{"type": "boolean"},
			"request_permissions": Schema{"type": "boolean"},
			"mcp_elicitations":    Schema{"type": "boolean"},
		},
		"required": []any{
			"sandbox_approval", "rules", "skill_approval",
			"request_permissions", "mcp_elicitations",
		},
	}
	wantApproval := Schema{"oneOf": []any{
		stringEnumSchema("untrusted"),
		stringEnumSchema("on-request"),
		Schema{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           Schema{"granular": granular},
			"required":             []any{"granular"},
		},
		stringEnumSchema("never"),
	}}
	if got := defs["AskForApproval"]; !reflect.DeepEqual(got, wantApproval) {
		t.Errorf("AskForApproval schema = %#v, want %#v", got, wantApproval)
	}

	wantSandbox := Schema{"oneOf": []any{
		Schema{"properties": Schema{"type": Schema{"enum": []any{"dangerFullAccess"}, "title": "DangerFullAccessSandboxPolicyType", "type": "string"}}, "required": []string{"type"}, "title": "DangerFullAccessSandboxPolicy", "type": "object"},
		Schema{"properties": Schema{"networkAccess": Schema{"default": false, "type": "boolean"}, "type": Schema{"enum": []any{"readOnly"}, "title": "ReadOnlySandboxPolicyType", "type": "string"}}, "required": []string{"type"}, "title": "ReadOnlySandboxPolicy", "type": "object"},
		Schema{"properties": Schema{"networkAccess": Schema{"allOf": []any{Schema{"$ref": "#/$defs/NetworkAccess"}}, "default": "restricted"}, "type": Schema{"enum": []any{"externalSandbox"}, "title": "ExternalSandboxSandboxPolicyType", "type": "string"}}, "required": []string{"type"}, "title": "ExternalSandboxSandboxPolicy", "type": "object"},
		Schema{"properties": Schema{"excludeSlashTmp": Schema{"default": false, "type": "boolean"}, "excludeTmpdirEnvVar": Schema{"default": false, "type": "boolean"}, "networkAccess": Schema{"default": false, "type": "boolean"}, "type": Schema{"enum": []any{"workspaceWrite"}, "title": "WorkspaceWriteSandboxPolicyType", "type": "string"}, "writableRoots": Schema{"default": []any{}, "items": Schema{"$ref": "#/$defs/AbsolutePathBuf"}, "type": "array"}}, "required": []string{"type"}, "title": "WorkspaceWriteSandboxPolicy", "type": "object"},
	}}
	if got := defs["SandboxPolicy"]; !reflect.DeepEqual(got, wantSandbox) {
		t.Errorf("SandboxPolicy schema = %#v, want %#v", got, wantSandbox)
	}
}

func TestThreadResponsePolicyPrerequisiteWireValidation(t *testing.T) {
	for _, input := range []string{`"user"`, `"auto_review"`, `"guardian_subagent"`} {
		var value ApprovalsReviewer
		threadResponsePolicyRoundTrip(t, input, &value)
	}
	for _, input := range []string{`"restricted"`, `"enabled"`} {
		var value NetworkAccess
		threadResponsePolicyRoundTrip(t, input, &value)
	}
	for _, input := range []string{
		`"untrusted"`,
		`"on-request"`,
		`"never"`,
		`{"granular":{"sandbox_approval":true,"rules":false,"skill_approval":true,"request_permissions":false,"mcp_elicitations":true}}`,
	} {
		var value AskForApproval
		threadResponsePolicyRoundTrip(t, input, &value)
	}

	absoluteRoot := filepath.Join(string(filepath.Separator), "workspace", "root")
	rootJSON, err := json.Marshal(absoluteRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{"type":"dangerFullAccess"}`,
		`{"type":"readOnly","networkAccess":false}`,
		`{"type":"externalSandbox","networkAccess":"restricted"}`,
		`{"type":"externalSandbox","networkAccess":"enabled"}`,
		`{"type":"workspaceWrite","writableRoots":[],"networkAccess":true,"excludeTmpdirEnvVar":false,"excludeSlashTmp":true}`,
		`{"type":"workspaceWrite","writableRoots":[` + string(rootJSON) + `],"networkAccess":false,"excludeTmpdirEnvVar":true,"excludeSlashTmp":false}`,
	} {
		var value SandboxPolicy
		threadResponsePolicyRoundTrip(t, input, &value)
	}
	for _, testCase := range []struct{ input, want string }{
		{`{"type":"readOnly"}`, `{"type":"readOnly","networkAccess":false}`},
		{`{"type":"externalSandbox"}`, `{"type":"externalSandbox","networkAccess":"restricted"}`},
		{`{"type":"workspaceWrite"}`, `{"type":"workspaceWrite","writableRoots":[],"networkAccess":false,"excludeTmpdirEnvVar":false,"excludeSlashTmp":false}`},
		{`{"type":"dangerFullAccess","future":true}`, `{"type":"dangerFullAccess"}`},
	} {
		var value SandboxPolicy
		if err := json.Unmarshal([]byte(testCase.input), &value); err != nil {
			t.Errorf("SandboxPolicy Unmarshal(%s): %v", testCase.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != testCase.want {
			t.Errorf("SandboxPolicy canonical %s = %s, %v; want %s", testCase.input, encoded, err, testCase.want)
		}
	}
}

func TestThreadResponsePolicyPrerequisitesRejectMalformedWireValues(t *testing.T) {
	for _, input := range []string{
		`null`, `""`, `"autoReview"`, `"other"`, `{}`, `[]`, `"user" true`,
	} {
		var value ApprovalsReviewer
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ApprovalsReviewer Unmarshal(%s) succeeded", input)
		}
	}
	for _, input := range []string{
		`null`, `""`, `"unrestricted"`, `"other"`, `{}`, `[]`, `"enabled" false`,
	} {
		var value NetworkAccess
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("NetworkAccess Unmarshal(%s) succeeded", input)
		}
	}

	for _, input := range []string{
		`null`, `[]`, `{}`, `"onRequest"`, `"always"`,
		`{"granular":null}`,
		`{"granular":{}}`,
		`{"granular":{"sandbox_approval":true}}`,
		`{"granular":{"sandbox_approval":true,"rules":false}}`,
		`{"granular":{"sandbox_approval":true,"rules":false,"skill_approval":true}}`,
		`{"granular":{"sandbox_approval":true,"rules":false,"skill_approval":true,"request_permissions":false}}`,
		`{"granular":{"sandbox_approval":true,"rules":false,"skill_approval":true,"request_permissions":false,"mcp_elicitations":null}}`,
		`{"granular":{"sandbox_approval":true,"rules":false,"skill_approval":true,"request_permissions":false,"mcp_elicitations":true,"other":false}}`,
		`{"granular":{"sandbox_approval":true,"rules":false,"skill_approval":true,"request_permissions":false,"mcp_elicitations":true},"other":false}`,
		`{"untrusted":true}`,
		`{"granular":{"sandboxApproval":true,"rules":false,"skillApproval":true,"requestPermissions":false,"mcpElicitations":true}}`,
		`"never" {}`,
	} {
		var value AskForApproval
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("AskForApproval Unmarshal(%s) succeeded", input)
		}
	}

	for _, input := range []string{
		`null`, `[]`, `{}`, `{"type":null}`, `{"type":"unknown"}`,
		`{"type":"readOnly","networkAccess":null}`,
		`{"type":"readOnly","networkAccess":"restricted"}`,
		`{"type":"externalSandbox","networkAccess":null}`,
		`{"type":"externalSandbox","networkAccess":false}`,
		`{"type":"externalSandbox","networkAccess":"other"}`,
		`{"type":"workspaceWrite","writableRoots":null,"networkAccess":false,"excludeTmpdirEnvVar":false,"excludeSlashTmp":false}`,
		`{"type":"workspaceWrite","writableRoots":["relative"],"networkAccess":false,"excludeTmpdirEnvVar":false,"excludeSlashTmp":false}`,
		`{"type":"workspaceWrite","writableRoots":[],"networkAccess":null,"excludeTmpdirEnvVar":false,"excludeSlashTmp":false}`,
		`{"type":"workspaceWrite","writableRoots":[],"networkAccess":false,"excludeTmpdirEnvVar":null,"excludeSlashTmp":false}`,
		`{"type":"workspaceWrite","writableRoots":[],"networkAccess":false,"excludeTmpdirEnvVar":false,"excludeSlashTmp":null}`,
		`{"type":"dangerFullAccess"} {}`,
	} {
		var value SandboxPolicy
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("SandboxPolicy Unmarshal(%s) succeeded", input)
		}
	}
}

func TestThreadResponsePolicyPrerequisiteDiscriminants(t *testing.T) {
	for _, testCase := range []struct {
		input string
		want  string
	}{
		{`"untrusted"`, "untrusted"},
		{`"on-request"`, "on-request"},
		{`"never"`, "never"},
		{`{"granular":{"sandbox_approval":true,"rules":false,"skill_approval":true,"request_permissions":false,"mcp_elicitations":true}}`, "granular"},
	} {
		var value AskForApproval
		if err := json.Unmarshal([]byte(testCase.input), &value); err != nil {
			t.Fatalf("Unmarshal(%s): %v", testCase.input, err)
		}
		if got := value.Kind(); got != testCase.want {
			t.Errorf("AskForApproval.Kind(%s) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
	if got := (AskForApproval{}).Kind(); got != "" {
		t.Errorf("zero AskForApproval.Kind() = %q", got)
	}

	for _, typeName := range []string{"dangerFullAccess", "readOnly", "externalSandbox", "workspaceWrite"} {
		var input string
		switch typeName {
		case "dangerFullAccess":
			input = `{"type":"dangerFullAccess"}`
		case "readOnly":
			input = `{"type":"readOnly","networkAccess":false}`
		case "externalSandbox":
			input = `{"type":"externalSandbox","networkAccess":"restricted"}`
		case "workspaceWrite":
			input = `{"type":"workspaceWrite","writableRoots":[],"networkAccess":false,"excludeTmpdirEnvVar":false,"excludeSlashTmp":false}`
		}
		var value SandboxPolicy
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Fatalf("Unmarshal(%s): %v", input, err)
		}
		if got := value.Type(); got != typeName {
			t.Errorf("SandboxPolicy.Type(%s) = %q, want %q", input, got, typeName)
		}
	}
	if got := (SandboxPolicy{}).Type(); got != "" {
		t.Errorf("zero SandboxPolicy.Type() = %q", got)
	}
}

func TestThreadResponsePolicyPrerequisiteNilAndZeroValuesFailClosed(t *testing.T) {
	for index, value := range []any{
		ApprovalsReviewer(""), ApprovalsReviewer("other"),
		NetworkAccess(""), NetworkAccess("other"),
		AskForApproval{}, SandboxPolicy{},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid value %d marshaled", index)
		}
	}
	var reviewer *ApprovalsReviewer
	if err := reviewer.UnmarshalJSON([]byte(`"user"`)); err == nil {
		t.Fatal("nil ApprovalsReviewer receiver succeeded")
	}
	var access *NetworkAccess
	if err := access.UnmarshalJSON([]byte(`"enabled"`)); err == nil {
		t.Fatal("nil NetworkAccess receiver succeeded")
	}
	var approval *AskForApproval
	if err := approval.UnmarshalJSON([]byte(`"never"`)); err == nil {
		t.Fatal("nil AskForApproval receiver succeeded")
	}
	var sandbox *SandboxPolicy
	if err := sandbox.UnmarshalJSON([]byte(`{"type":"dangerFullAccess"}`)); err == nil {
		t.Fatal("nil SandboxPolicy receiver succeeded")
	}
}

func TestThreadResponsePolicyPrerequisitesRemainStandalone(t *testing.T) {
	standalone := []string{"ApprovalsReviewer", "AskForApproval", "NetworkAccess", "SandboxPolicy"}
	for _, binding := range WireTypeBindings() {
		for _, name := range standalone {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Errorf("%s unexpectedly binds standalone %s", binding.Method, name)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestThreadResponsePolicyPrerequisiteTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ApprovalsReviewer = "user" | "auto_review" | "guardian_subagent";`,
		`export type NetworkAccess = "restricted" | "enabled";`,
		`export type AskForApproval = "untrusted" | "on-request" | {`,
		`"granular": {`,
		`"mcp_elicitations": boolean;`,
		`"request_permissions": boolean;`,
		`"rules": boolean;`,
		`"sandbox_approval": boolean;`,
		`"skill_approval": boolean;`,
		`export type SandboxPolicy = {`,
		`"type": "dangerFullAccess";`,
		`"type": "readOnly";`,
		`"type": "externalSandbox";`,
		`"type": "workspaceWrite";`,
		`"writableRoots": Array<AbsolutePathBuf>;`,
		`"networkAccess": NetworkAccess;`,
		`"excludeTmpdirEnvVar": boolean;`,
		`"excludeSlashTmp": boolean;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func threadResponsePolicyRoundTrip(t *testing.T, input string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(input), target); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("Marshal(%s): %v", input, err)
	}
	var got any
	var want any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode encoded %s: %v", encoded, err)
	}
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatalf("decode input %s: %v", input, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip %s = %s", input, encoded)
	}
}

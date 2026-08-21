package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPluginShareSaveSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range map[string]Schema{
		"PluginShareDiscoverability": pluginShareDiscoverabilitySchema(),
		"PluginSharePrincipalType":   pluginSharePrincipalTypeSchema(),
		"PluginShareTargetRole":      pluginShareTargetRoleSchema(),
		"PluginShareTarget":          pluginShareTargetSchema(),
		"PluginShareSaveParams":      pluginShareSaveParamsSchema(),
	} {
		if got := defs[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestPluginShareSaveEnumsAreClosed(t *testing.T) {
	for _, values := range [][]any{
		{PluginShareDiscoverabilityListed, PluginShareDiscoverabilityUnlisted, PluginShareDiscoverabilityPrivate},
		{PluginSharePrincipalTypeUser, PluginSharePrincipalTypeGroup, PluginSharePrincipalTypeWorkspace},
		{PluginShareTargetRoleReader, PluginShareTargetRoleEditor},
	} {
		for _, value := range values {
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("Marshal(%T): %v", value, err)
			}
			switch value.(type) {
			case PluginShareDiscoverability:
				var decoded PluginShareDiscoverability
				if err := json.Unmarshal(encoded, &decoded); err != nil {
					t.Fatalf("Unmarshal discoverability: %v", err)
				}
			case PluginSharePrincipalType:
				var decoded PluginSharePrincipalType
				if err := json.Unmarshal(encoded, &decoded); err != nil {
					t.Fatalf("Unmarshal principal type: %v", err)
				}
			case PluginShareTargetRole:
				var decoded PluginShareTargetRole
				if err := json.Unmarshal(encoded, &decoded); err != nil {
					t.Fatalf("Unmarshal target role: %v", err)
				}
			}
		}
	}
	for _, input := range []string{`null`, `"listed"`, `"OTHER"`, `1`, `{}`, `[]`, `"LISTED" {}`} {
		var value PluginShareDiscoverability
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("discoverability Unmarshal(%s) succeeded", input)
		}
	}
	for _, input := range []string{`null`, `"team"`, `"OTHER"`, `1`, `{}`, `[]`, `"user" {}`} {
		var value PluginSharePrincipalType
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("principal type Unmarshal(%s) succeeded", input)
		}
	}
	for _, input := range []string{`null`, `"owner"`, `"OTHER"`, `1`, `{}`, `[]`, `"reader" {}`} {
		var value PluginShareTargetRole
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("target role Unmarshal(%s) succeeded", input)
		}
	}
	if _, err := json.Marshal(PluginShareDiscoverability("listed")); err == nil {
		t.Fatal("invalid discoverability marshaled")
	}
	if _, err := json.Marshal(PluginSharePrincipalType("team")); err == nil {
		t.Fatal("invalid principal type marshaled")
	}
	if _, err := json.Marshal(PluginShareTargetRole("owner")); err == nil {
		t.Fatal("invalid target role marshaled")
	}
	var discoverability *PluginShareDiscoverability
	if err := discoverability.UnmarshalJSON([]byte(`"LISTED"`)); err == nil {
		t.Fatal("nil discoverability receiver succeeded")
	}
	var principalType *PluginSharePrincipalType
	if err := principalType.UnmarshalJSON([]byte(`"user"`)); err == nil {
		t.Fatal("nil principal-type receiver succeeded")
	}
	var role *PluginShareTargetRole
	if err := role.UnmarshalJSON([]byte(`"reader"`)); err == nil {
		t.Fatal("nil target-role receiver succeeded")
	}
}

func TestPluginShareTargetPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"principalType":"user","principalId":" user ","role":"reader","future":true}`, `{"principalType":"user","principalId":" user ","role":"reader"}`},
		{`{"principalType":"workspace","principalId":"","role":"editor"}`, `{"principalType":"workspace","principalId":"","role":"editor"}`},
	} {
		var target PluginShareTarget
		if err := json.Unmarshal([]byte(tc.input), &target); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.input, err)
		}
		encoded, err := json.Marshal(target)
		if err != nil || string(encoded) != tc.want {
			t.Fatalf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestPluginShareSaveParamsPreserveSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"pluginPath":"/plugins/example","future":true}`, `{"pluginPath":"/plugins/example","remotePluginId":null,"discoverability":null,"shareTargets":null}`},
		{`{"pluginPath":"/plugins/example","remotePluginId":" remote ","discoverability":"PRIVATE","shareTargets":[{"principalType":"group","principalId":" group ","role":"editor"}]}`, `{"pluginPath":"/plugins/example","remotePluginId":" remote ","discoverability":"PRIVATE","shareTargets":[{"principalType":"group","principalId":" group ","role":"editor"}]}`},
	} {
		var value PluginShareSaveParams
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.input, err)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Fatalf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestPluginShareSaveRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"principalType":null,"principalId":"id","role":"reader"}`,
		`{"principalType":"user","role":"reader"}`,
		`{"principalType":"user","principalId":"id","role":"owner"}`,
		`{"principalType":"user","principalId":"id","role":"reader","role":"editor"}`,
	} {
		assertJSONRejects[PluginShareTarget](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"pluginPath":null}`, `{"pluginPath":"relative"}`, `{"remotePluginId":1,"pluginPath":"/plugins/example"}`,
		`{"discoverability":"listed","pluginPath":"/plugins/example"}`, `{"shareTargets":{},"pluginPath":"/plugins/example"}`,
		`{"shareTargets":[null],"pluginPath":"/plugins/example"}`, `{"shareTargets":[{"principalType":"user","principalId":"id","role":"owner"}],"pluginPath":"/plugins/example"}`,
		`{"pluginPath":"/plugins/example","pluginPath":"/plugins/other"}`, `{"pluginPath":"/plugins/example"} {}`,
	} {
		assertJSONRejects[PluginShareSaveParams](t, input)
	}
}

func TestPluginShareSaveRemainsStandalone(t *testing.T) {
	var target *PluginShareTarget
	if err := target.UnmarshalJSON([]byte(`{"principalType":"user","principalId":"id","role":"reader"}`)); err == nil {
		t.Fatal("nil target receiver succeeded")
	}
	var params *PluginShareSaveParams
	if err := params.UnmarshalJSON([]byte(`{"pluginPath":"/plugins/example"}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			switch name {
			case "PluginShareDiscoverability", "PluginSharePrincipalType", "PluginShareTargetRole", "PluginShareTarget", "PluginShareSaveParams":
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestPluginShareSaveTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type PluginShareDiscoverability = "LISTED" | "UNLISTED" | "PRIVATE";`,
		`export type PluginSharePrincipalType = "user" | "group" | "workspace";`,
		`export type PluginShareTargetRole = "reader" | "editor";`,
		`export type PluginShareTarget = { principalType: PluginSharePrincipalType, principalId: string, role: PluginShareTargetRole, };`,
		`export type PluginShareSaveParams = { pluginPath: AbsolutePathBuf, remotePluginId?: string | null, discoverability?: PluginShareDiscoverability | null, shareTargets?: Array<PluginShareTarget> | null, };`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

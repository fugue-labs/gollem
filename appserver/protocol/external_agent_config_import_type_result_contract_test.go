package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExternalAgentConfigImportTypeResultSchemaIsExact(t *testing.T) {
	want := closedThreadSessionParamSchema(Schema{
		"itemType": Schema{"$ref": "#/$defs/ExternalAgentConfigMigrationItemType"},
		"successes": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/ExternalAgentConfigImportItemTypeSuccess"},
		},
		"failures": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/ExternalAgentConfigImportItemTypeFailure"},
		},
	}, []string{"itemType", "successes", "failures"})
	got, ok := JSONSchema()["$defs"].(Schema)["ExternalAgentConfigImportTypeResult"].(Schema)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("ExternalAgentConfigImportTypeResult schema = %#v, %v; want %#v", got, ok, want)
	}
}

func TestExternalAgentConfigImportTypeResultAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		want      ExternalAgentConfigImportTypeResult
		canonical string
	}{
		{
			name: "empty arrays and unknown", input: `{"itemType":"CONFIG","successes":[],"failures":[],"future":true}`,
			want: ExternalAgentConfigImportTypeResult{
				ItemType:  ExternalAgentConfigMigrationItemTypeConfig,
				Successes: []ExternalAgentConfigImportItemTypeSuccess{},
				Failures:  []ExternalAgentConfigImportItemTypeFailure{},
			},
			canonical: `{"itemType":"CONFIG","successes":[],"failures":[]}`,
		},
		{
			name: "ordered duplicate strict results",
			input: `{"itemType":"HOOKS","successes":[` +
				`{"itemType":"CONFIG"},` +
				`{"itemType":"SKILLS","cwd":"repo/../repo","source":" Claude Code ","target":""},` +
				`{"itemType":"CONFIG"}` +
				`],"failures":[` +
				`{"itemType":"HOOKS","failureStage":"","message":""},` +
				`{"itemType":"SESSIONS","errorType":" IO ","failureStage":" import ","message":" failed ","cwd":"/tmp/../repo","source":""},` +
				`{"itemType":"HOOKS","failureStage":"","message":""}` +
				`]}`,
			want: ExternalAgentConfigImportTypeResult{
				ItemType: ExternalAgentConfigMigrationItemTypeHooks,
				Successes: []ExternalAgentConfigImportItemTypeSuccess{
					{ItemType: ExternalAgentConfigMigrationItemTypeConfig},
					{
						ItemType: ExternalAgentConfigMigrationItemTypeSkills,
						CWD:      stringPointer("repo/../repo"), Source: stringPointer(" Claude Code "), Target: stringPointer(""),
					},
					{ItemType: ExternalAgentConfigMigrationItemTypeConfig},
				},
				Failures: []ExternalAgentConfigImportItemTypeFailure{
					{ItemType: ExternalAgentConfigMigrationItemTypeHooks},
					{
						ItemType:  ExternalAgentConfigMigrationItemTypeSessions,
						ErrorType: stringPointer(" IO "), FailureStage: " import ", Message: " failed ",
						CWD: stringPointer("/tmp/../repo"), Source: stringPointer(""),
					},
					{ItemType: ExternalAgentConfigMigrationItemTypeHooks},
				},
			},
			canonical: `{"itemType":"HOOKS","successes":[` +
				`{"itemType":"CONFIG","cwd":null,"source":null,"target":null},` +
				`{"itemType":"SKILLS","cwd":"repo/../repo","source":" Claude Code ","target":""},` +
				`{"itemType":"CONFIG","cwd":null,"source":null,"target":null}` +
				`],"failures":[` +
				`{"itemType":"HOOKS","errorType":null,"failureStage":"","message":"","cwd":null,"source":null},` +
				`{"itemType":"SESSIONS","errorType":" IO ","failureStage":" import ","message":" failed ","cwd":"/tmp/../repo","source":""},` +
				`{"itemType":"HOOKS","errorType":null,"failureStage":"","message":"","cwd":null,"source":null}` +
				`]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var result ExternalAgentConfigImportTypeResult
			if err := json.Unmarshal([]byte(tc.input), &result); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(result, tc.want) {
				t.Fatalf("result = %#v, want %#v", result, tc.want)
			}
			encoded, err := json.Marshal(result)
			if err != nil || string(encoded) != tc.canonical {
				t.Fatalf("canonical = %s, %v; want %s", encoded, err, tc.canonical)
			}
			var roundTrip ExternalAgentConfigImportTypeResult
			if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, result) {
				t.Fatalf("round trip = %#v, %v; want %#v", roundTrip, err, result)
			}
		})
	}

	encoded, err := json.Marshal(ExternalAgentConfigImportTypeResult{
		ItemType: ExternalAgentConfigMigrationItemTypeConfig,
	})
	if err != nil || string(encoded) != `{"itemType":"CONFIG","successes":[],"failures":[]}` {
		t.Fatalf("nil slices = %s, %v; want non-null empty arrays", encoded, err)
	}
}

func TestExternalAgentConfigImportTypeResultRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"itemType":null,"successes":[],"failures":[]}`,
		`{"itemType":"OTHER","successes":[],"failures":[]}`,
		`{"itemType":"CONFIG","failures":[]}`,
		`{"itemType":"CONFIG","successes":[]}`,
		`{"itemType":"CONFIG","successes":null,"failures":[]}`,
		`{"itemType":"CONFIG","successes":[],"failures":null}`,
		`{"itemType":"CONFIG","successes":{},"failures":[]}`,
		`{"itemType":"CONFIG","successes":[],"failures":"failures"}`,
		`{"itemType":"CONFIG","successes":[null],"failures":[]}`,
		`{"itemType":"CONFIG","successes":[{}],"failures":[]}`,
		`{"itemType":"CONFIG","successes":[{"itemType":"OTHER"}],"failures":[]}`,
		`{"itemType":"CONFIG","successes":[{"itemType":"CONFIG","cwd":1}],"failures":[]}`,
		`{"itemType":"CONFIG","successes":[],"failures":[null]}`,
		`{"itemType":"CONFIG","successes":[],"failures":[{}]}`,
		`{"itemType":"CONFIG","successes":[],"failures":[{"itemType":"HOOKS","message":"missing stage"}]}`,
		`{"itemType":"CONFIG","successes":[],"failures":[{"itemType":"HOOKS","failureStage":"x","message":null}]}`,
		`{"itemType":"CONFIG","itemType":"SKILLS","successes":[],"failures":[]}`,
		`{"itemType":"CONFIG","successes":[],"successes":[],"failures":[]}`,
		`{"itemType":"CONFIG","successes":[],"failures":[],"failures":[]}`,
		`{"itemType":"CONFIG","successes":[],"failures":[]`,
		`{"itemType":"CONFIG","successes":[],"failures":[]} {}`,
	}
	for _, input := range invalid {
		var result ExternalAgentConfigImportTypeResult
		if err := json.Unmarshal([]byte(input), &result); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var result *ExternalAgentConfigImportTypeResult
	if err := result.UnmarshalJSON([]byte(`{"itemType":"CONFIG","successes":[],"failures":[]}`)); err == nil {
		t.Fatal("nil ExternalAgentConfigImportTypeResult receiver succeeded")
	}
	if _, err := json.Marshal(ExternalAgentConfigImportTypeResult{}); err == nil {
		t.Fatal("invalid zero result marshaled")
	}
	if _, err := json.Marshal(ExternalAgentConfigImportTypeResult{
		ItemType:  ExternalAgentConfigMigrationItemTypeConfig,
		Successes: []ExternalAgentConfigImportItemTypeSuccess{{}},
	}); err == nil {
		t.Fatal("invalid nested success marshaled")
	}
}

func TestExternalAgentConfigImportTypeResultRemainsStandalone(t *testing.T) {
	const name = "ExternalAgentConfigImportTypeResult"
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
			t.Fatalf("%s unexpectedly bound: %#v", name, binding)
		}
	}
	for _, method := range []string{
		"externalAgentConfig/import",
		"externalAgentConfig/import/readHistories",
		"externalAgentConfig/import/progress",
		"externalAgentConfig/import/completed",
	} {
		info, ok := LookupMethod(method)
		if !ok || info.State != MethodDeferredStub {
			t.Fatalf("%s = %#v, %v; want deferred stub", method, info, ok)
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

func TestExternalAgentConfigImportTypeResultTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type ExternalAgentConfigImportTypeResult = {\n" +
		"  \"failures\": Array<ExternalAgentConfigImportItemTypeFailure>;\n" +
		"  \"itemType\": ExternalAgentConfigMigrationItemType;\n" +
		"  \"successes\": Array<ExternalAgentConfigImportItemTypeSuccess>;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = ExternalAgentConfigImportTypeResult{}
	_ json.Unmarshaler = (*ExternalAgentConfigImportTypeResult)(nil)
)

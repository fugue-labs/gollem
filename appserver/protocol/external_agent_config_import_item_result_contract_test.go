package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExternalAgentConfigImportItemResultSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	nullableString := func() Schema {
		return Schema{"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}}}
	}
	wantSuccess := closedThreadSessionParamSchema(Schema{
		"itemType": Schema{"$ref": "#/$defs/ExternalAgentConfigMigrationItemType"},
		"cwd":      nullableString(),
		"source":   nullableString(),
		"target":   nullableString(),
	}, []string{"itemType"})
	wantFailure := closedThreadSessionParamSchema(Schema{
		"itemType":     Schema{"$ref": "#/$defs/ExternalAgentConfigMigrationItemType"},
		"errorType":    nullableString(),
		"failureStage": Schema{"type": "string"},
		"message":      Schema{"type": "string"},
		"cwd":          nullableString(),
		"source":       nullableString(),
	}, []string{"itemType", "failureStage", "message"})
	for name, want := range map[string]Schema{
		"ExternalAgentConfigImportItemTypeSuccess": wantSuccess,
		"ExternalAgentConfigImportItemTypeFailure": wantFailure,
	} {
		got, ok := defs[name].(Schema)
		if !ok {
			t.Errorf("$defs missing %s", name)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestExternalAgentConfigImportItemTypeSuccessAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		want      ExternalAgentConfigImportItemTypeSuccess
		canonical string
	}{
		{
			name: "options omitted", input: `{"itemType":"CONFIG"}`,
			want: ExternalAgentConfigImportItemTypeSuccess{
				ItemType: ExternalAgentConfigMigrationItemTypeConfig,
			},
			canonical: `{"itemType":"CONFIG","cwd":null,"source":null,"target":null}`,
		},
		{
			name: "explicit null and unknown", input: `{"itemType":"SKILLS","cwd":null,"source":null,"target":null,"future":true}`,
			want: ExternalAgentConfigImportItemTypeSuccess{
				ItemType: ExternalAgentConfigMigrationItemTypeSkills,
			},
			canonical: `{"itemType":"SKILLS","cwd":null,"source":null,"target":null}`,
		},
		{
			name: "opaque strings", input: `{"itemType":"PLUGINS","cwd":"repo/../repo","source":" Claude Code ","target":""}`,
			want: ExternalAgentConfigImportItemTypeSuccess{
				ItemType: ExternalAgentConfigMigrationItemTypePlugins,
				CWD:      stringPointer("repo/../repo"),
				Source:   stringPointer(" Claude Code "),
				Target:   stringPointer(""),
			},
			canonical: `{"itemType":"PLUGINS","cwd":"repo/../repo","source":" Claude Code ","target":""}`,
		},
		{
			name: "empty cwd", input: `{"itemType":"COMMANDS","cwd":"","source":"","target":"target"}`,
			want: ExternalAgentConfigImportItemTypeSuccess{
				ItemType: ExternalAgentConfigMigrationItemTypeCommands,
				CWD:      stringPointer(""), Source: stringPointer(""), Target: stringPointer("target"),
			},
			canonical: `{"itemType":"COMMANDS","cwd":"","source":"","target":"target"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var value ExternalAgentConfigImportItemTypeSuccess
			if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(value, tc.want) {
				t.Fatalf("value = %#v, want %#v", value, tc.want)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != tc.canonical {
				t.Fatalf("canonical = %s, %v; want %s", encoded, err, tc.canonical)
			}
			var roundTrip ExternalAgentConfigImportItemTypeSuccess
			if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, value) {
				t.Fatalf("round trip = %#v, %v; want %#v", roundTrip, err, value)
			}
		})
	}
}

func TestExternalAgentConfigImportItemTypeFailureAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		want      ExternalAgentConfigImportItemTypeFailure
		canonical string
	}{
		{
			name:  "options omitted and required strings empty",
			input: `{"itemType":"CONFIG","failureStage":"","message":""}`,
			want: ExternalAgentConfigImportItemTypeFailure{
				ItemType: ExternalAgentConfigMigrationItemTypeConfig,
			},
			canonical: `{"itemType":"CONFIG","errorType":null,"failureStage":"","message":"","cwd":null,"source":null}`,
		},
		{
			name:  "explicit null and unknown",
			input: `{"itemType":"HOOKS","errorType":null,"failureStage":"write","message":"failed","cwd":null,"source":null,"future":true}`,
			want: ExternalAgentConfigImportItemTypeFailure{
				ItemType:     ExternalAgentConfigMigrationItemTypeHooks,
				FailureStage: "write", Message: "failed",
			},
			canonical: `{"itemType":"HOOKS","errorType":null,"failureStage":"write","message":"failed","cwd":null,"source":null}`,
		},
		{
			name:  "opaque strings",
			input: `{"itemType":"SESSIONS","errorType":" IO ","failureStage":" import ","message":" message ","cwd":"/tmp/../repo","source":""}`,
			want: ExternalAgentConfigImportItemTypeFailure{
				ItemType:  ExternalAgentConfigMigrationItemTypeSessions,
				ErrorType: stringPointer(" IO "), FailureStage: " import ", Message: " message ",
				CWD: stringPointer("/tmp/../repo"), Source: stringPointer(""),
			},
			canonical: `{"itemType":"SESSIONS","errorType":" IO ","failureStage":" import ","message":" message ","cwd":"/tmp/../repo","source":""}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var value ExternalAgentConfigImportItemTypeFailure
			if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(value, tc.want) {
				t.Fatalf("value = %#v, want %#v", value, tc.want)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != tc.canonical {
				t.Fatalf("canonical = %s, %v; want %s", encoded, err, tc.canonical)
			}
			var roundTrip ExternalAgentConfigImportItemTypeFailure
			if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, value) {
				t.Fatalf("round trip = %#v, %v; want %#v", roundTrip, err, value)
			}
		})
	}
}

func TestExternalAgentConfigImportItemResultsRejectMalformedWireForms(t *testing.T) {
	successInvalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"itemType":null}`, `{"itemType":"OTHER"}`, `{"itemType":1}`,
		`{"itemType":"CONFIG","cwd":1}`, `{"itemType":"CONFIG","source":true}`,
		`{"itemType":"CONFIG","target":[]}`, `{"itemType":"CONFIG","itemType":"SKILLS"}`,
		`{"itemType":"CONFIG","cwd":null,"cwd":"repo"}`,
		`{"itemType":"CONFIG","source":null,"source":"source"}`,
		`{"itemType":"CONFIG","target":null,"target":"target"}`,
		`{"itemType":"CONFIG"`, `{"itemType":"CONFIG"} {}`,
	}
	for _, input := range successInvalid {
		var value ExternalAgentConfigImportItemTypeSuccess
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("success Unmarshal(%s) succeeded", input)
		}
	}
	var success *ExternalAgentConfigImportItemTypeSuccess
	if err := success.UnmarshalJSON([]byte(`{"itemType":"CONFIG"}`)); err == nil {
		t.Fatal("nil success receiver succeeded")
	}
	if _, err := json.Marshal(ExternalAgentConfigImportItemTypeSuccess{}); err == nil {
		t.Fatal("success with invalid zero item type marshaled")
	}

	failureInvalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"itemType":"CONFIG","message":"missing stage"}`,
		`{"itemType":"CONFIG","failureStage":"missing message"}`,
		`{"itemType":null,"failureStage":"x","message":"x"}`,
		`{"itemType":"OTHER","failureStage":"x","message":"x"}`,
		`{"itemType":"CONFIG","failureStage":null,"message":"x"}`,
		`{"itemType":"CONFIG","failureStage":"x","message":null}`,
		`{"itemType":"CONFIG","failureStage":1,"message":"x"}`,
		`{"itemType":"CONFIG","failureStage":"x","message":1}`,
		`{"itemType":"CONFIG","failureStage":"x","message":"x","errorType":1}`,
		`{"itemType":"CONFIG","failureStage":"x","message":"x","cwd":true}`,
		`{"itemType":"CONFIG","failureStage":"x","message":"x","source":[]}`,
		`{"itemType":"CONFIG","itemType":"SKILLS","failureStage":"x","message":"x"}`,
		`{"itemType":"CONFIG","errorType":null,"errorType":"error","failureStage":"x","message":"x"}`,
		`{"itemType":"CONFIG","failureStage":"x","failureStage":"y","message":"x"}`,
		`{"itemType":"CONFIG","failureStage":"x","message":"x","message":"y"}`,
		`{"itemType":"CONFIG","failureStage":"x","message":"x","cwd":null,"cwd":"repo"}`,
		`{"itemType":"CONFIG","failureStage":"x","message":"x","source":null,"source":"source"}`,
		`{"itemType":"CONFIG","failureStage":"x","message":"x"`,
		`{"itemType":"CONFIG","failureStage":"x","message":"x"} {}`,
	}
	for _, input := range failureInvalid {
		var value ExternalAgentConfigImportItemTypeFailure
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("failure Unmarshal(%s) succeeded", input)
		}
	}
	var failure *ExternalAgentConfigImportItemTypeFailure
	if err := failure.UnmarshalJSON([]byte(`{"itemType":"CONFIG","failureStage":"","message":""}`)); err == nil {
		t.Fatal("nil failure receiver succeeded")
	}
	if _, err := json.Marshal(ExternalAgentConfigImportItemTypeFailure{}); err == nil {
		t.Fatal("failure with invalid zero item type marshaled")
	}
}

func TestExternalAgentConfigImportItemResultsRemainStandalone(t *testing.T) {
	names := []string{
		"ExternalAgentConfigImportItemTypeSuccess",
		"ExternalAgentConfigImportItemTypeFailure",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound: %#v", name, binding)
			}
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
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestExternalAgentConfigImportItemResultsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type ExternalAgentConfigImportItemTypeSuccess = {\n" +
			"  \"cwd\": string | null;\n" +
			"  \"itemType\": ExternalAgentConfigMigrationItemType;\n" +
			"  \"source\": string | null;\n" +
			"  \"target\": string | null;\n" +
			"};",
		"export type ExternalAgentConfigImportItemTypeFailure = {\n" +
			"  \"cwd\": string | null;\n" +
			"  \"errorType\": string | null;\n" +
			"  \"failureStage\": string;\n" +
			"  \"itemType\": ExternalAgentConfigMigrationItemType;\n" +
			"  \"message\": string;\n" +
			"  \"source\": string | null;\n" +
			"};",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = ExternalAgentConfigImportItemTypeSuccess{}
	_ json.Unmarshaler = (*ExternalAgentConfigImportItemTypeSuccess)(nil)
	_ json.Marshaler   = ExternalAgentConfigImportItemTypeFailure{}
	_ json.Unmarshaler = (*ExternalAgentConfigImportItemTypeFailure)(nil)
)

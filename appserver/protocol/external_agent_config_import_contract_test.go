package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExternalAgentConfigImportSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	wantParams := closedThreadSessionParamSchema(Schema{
		"migrationItems": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/ExternalAgentConfigMigrationItem"},
		},
		"source": Schema{
			"anyOf":       []any{Schema{"type": "string"}, Schema{"type": "null"}},
			"description": "Source product that produced the migration items. Missing means unspecified.",
		},
	}, []string{"migrationItems"})
	wantResponse := closedThreadSessionParamSchema(Schema{
		"importId": Schema{"type": "string"},
	}, []string{"importId"})
	for name, want := range map[string]Schema{
		"ExternalAgentConfigImportParams":   wantParams,
		"ExternalAgentConfigImportResponse": wantResponse,
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

func TestExternalAgentConfigImportParamsAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		want      ExternalAgentConfigImportParams
		canonical string
	}{
		{
			name: "empty with source omitted", input: `{"migrationItems":[]}`,
			want:      ExternalAgentConfigImportParams{MigrationItems: []ExternalAgentConfigMigrationItem{}},
			canonical: `{"migrationItems":[],"source":null}`,
		},
		{
			name: "explicit null and unknown", input: `{"migrationItems":[],"source":null,"future":true}`,
			want:      ExternalAgentConfigImportParams{MigrationItems: []ExternalAgentConfigMigrationItem{}},
			canonical: `{"migrationItems":[],"source":null}`,
		},
		{
			name: "empty source", input: `{"migrationItems":[],"source":""}`,
			want: ExternalAgentConfigImportParams{
				MigrationItems: []ExternalAgentConfigMigrationItem{}, Source: stringPointer(""),
			},
			canonical: `{"migrationItems":[],"source":""}`,
		},
		{
			name: "ordered duplicate strict items and source",
			input: `{"migrationItems":[` +
				`{"itemType":"CONFIG","description":"config"},` +
				`{"itemType":"CONFIG","description":"config","cwd":"repo/../repo","details":{}},` +
				`{"itemType":"CONFIG","description":"config"}` +
				`],"source":" Claude Code "}`,
			want: ExternalAgentConfigImportParams{
				MigrationItems: []ExternalAgentConfigMigrationItem{
					{ItemType: ExternalAgentConfigMigrationItemTypeConfig, Description: "config"},
					{
						ItemType: ExternalAgentConfigMigrationItemTypeConfig, Description: "config",
						CWD: stringPointer("repo/../repo"), Details: &MigrationDetails{},
					},
					{ItemType: ExternalAgentConfigMigrationItemTypeConfig, Description: "config"},
				},
				Source: stringPointer(" Claude Code "),
			},
			canonical: `{"migrationItems":[` +
				`{"itemType":"CONFIG","description":"config","cwd":null,"details":null},` +
				`{"itemType":"CONFIG","description":"config","cwd":"repo/../repo","details":` +
				`{"plugins":[],"skills":[],"sessions":[],"mcpServers":[],"hooks":[],"subagents":[],"commands":[]}},` +
				`{"itemType":"CONFIG","description":"config","cwd":null,"details":null}` +
				`],"source":" Claude Code "}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var params ExternalAgentConfigImportParams
			if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(params, canonicalExternalAgentConfigImportParams(tc.want)) {
				t.Fatalf("params = %#v, want %#v", params, canonicalExternalAgentConfigImportParams(tc.want))
			}
			encoded, err := json.Marshal(params)
			if err != nil || string(encoded) != tc.canonical {
				t.Fatalf("canonical = %s, %v; want %s", encoded, err, tc.canonical)
			}
			var roundTrip ExternalAgentConfigImportParams
			if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, params) {
				t.Fatalf("round trip = %#v, %v; want %#v", roundTrip, err, params)
			}
		})
	}

	encoded, err := json.Marshal(ExternalAgentConfigImportParams{})
	if err != nil || string(encoded) != `{"migrationItems":[],"source":null}` {
		t.Fatalf("zero params = %s, %v; want non-null items and explicit-null source", encoded, err)
	}
}

func TestExternalAgentConfigImportResponseAcceptsRustWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: `{"importId":""}`, want: ""},
		{input: `{"importId":"import-1","future":{"nested":true}}`, want: "import-1"},
	} {
		var response ExternalAgentConfigImportResponse
		if err := json.Unmarshal([]byte(tc.input), &response); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.input, err)
		}
		if response.ImportID != tc.want {
			t.Fatalf("response = %#v, want import ID %q", response, tc.want)
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var roundTrip ExternalAgentConfigImportResponse
		if err := json.Unmarshal(encoded, &roundTrip); err != nil || roundTrip != response {
			t.Fatalf("round trip = %#v, %v; want %#v", roundTrip, err, response)
		}
	}

	encoded, err := json.Marshal(ExternalAgentConfigImportResponse{})
	if err != nil || string(encoded) != `{"importId":""}` {
		t.Fatalf("zero response = %s, %v; want empty import ID", encoded, err)
	}
}

func TestExternalAgentConfigImportParamsRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"migrationItems":null}`, `{"migrationItems":{}}`, `{"migrationItems":"items"}`,
		`{"migrationItems":1}`, `{"migrationItems":true}`,
		`{"migrationItems":[null]}`, `{"migrationItems":[{}]}`, `{"migrationItems":[1]}`,
		`{"migrationItems":[{"itemType":"OTHER","description":"bad"}]}`,
		`{"migrationItems":[{"itemType":"CONFIG","description":"config","details":{"skills":null}}]}`,
		`{"migrationItems":[],"source":1}`, `{"migrationItems":[],"source":true}`,
		`{"migrationItems":[],"source":[]}`, `{"migrationItems":[],"source":{}}`,
		`{"migrationItems":[],"migrationItems":[]}`,
		`{"migrationItems":[],"source":null,"source":"codex"}`,
		`{"migrationItems":[]`, `{"migrationItems":[]} {}`,
	}
	for _, input := range invalid {
		var params ExternalAgentConfigImportParams
		if err := json.Unmarshal([]byte(input), &params); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var params *ExternalAgentConfigImportParams
	if err := params.UnmarshalJSON([]byte(`{"migrationItems":[]}`)); err == nil {
		t.Fatal("nil ExternalAgentConfigImportParams receiver succeeded")
	}
}

func TestExternalAgentConfigImportResponseRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"importId":null}`, `{"importId":1}`, `{"importId":true}`,
		`{"importId":[]}`, `{"importId":{}}`,
		`{"importId":"one","importId":"two"}`, `{"importId":""`, `{"importId":""} {}`,
	}
	for _, input := range invalid {
		var response ExternalAgentConfigImportResponse
		if err := json.Unmarshal([]byte(input), &response); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var response *ExternalAgentConfigImportResponse
	if err := response.UnmarshalJSON([]byte(`{"importId":""}`)); err == nil {
		t.Fatal("nil ExternalAgentConfigImportResponse receiver succeeded")
	}
}

func TestExternalAgentConfigImportRecordsRemainStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		for _, name := range []string{"ExternalAgentConfigImportParams", "ExternalAgentConfigImportResponse"} {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound: %#v", name, binding)
			}
		}
	}
	info, ok := LookupMethod("externalAgentConfig/import")
	if !ok || info.State != MethodDeferredStub {
		t.Fatalf("externalAgentConfig/import = %#v, %v; want deferred stub", info, ok)
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

func TestExternalAgentConfigImportTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type ExternalAgentConfigImportParams = {\n" +
			"  \"migrationItems\": Array<ExternalAgentConfigMigrationItem>;\n" +
			"  \"source\"?: string | null;\n" +
			"};",
		"export type ExternalAgentConfigImportResponse = {\n" +
			"  \"importId\": string;\n" +
			"};",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func canonicalExternalAgentConfigImportParams(params ExternalAgentConfigImportParams) ExternalAgentConfigImportParams {
	encoded, err := json.Marshal(params)
	if err != nil {
		panic(err)
	}
	var canonical ExternalAgentConfigImportParams
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		panic(err)
	}
	return canonical
}

var (
	_ json.Marshaler   = ExternalAgentConfigImportParams{}
	_ json.Unmarshaler = (*ExternalAgentConfigImportParams)(nil)
	_ json.Marshaler   = ExternalAgentConfigImportResponse{}
	_ json.Unmarshaler = (*ExternalAgentConfigImportResponse)(nil)
)

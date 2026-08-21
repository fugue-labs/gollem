package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExternalAgentConfigDetectSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	wantParams := closedThreadSessionParamSchema(Schema{
		"includeHome": Schema{
			"type": "boolean", "description": externalAgentConfigDetectIncludeHomeDescription,
		},
		"cwds": Schema{
			"anyOf": []any{
				Schema{"type": "array", "items": Schema{"type": "string"}},
				Schema{"type": "null"},
			},
			"description": externalAgentConfigDetectCWDsDescription,
		},
	}, nil)
	wantResponse := closedThreadSessionParamSchema(Schema{
		"items": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/ExternalAgentConfigMigrationItem"},
		},
	}, []string{"items"})
	for name, want := range map[string]Schema{
		"ExternalAgentConfigDetectParams":   wantParams,
		"ExternalAgentConfigDetectResponse": wantResponse,
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

func TestExternalAgentConfigDetectParamsAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		want      ExternalAgentConfigDetectParams
		canonical string
	}{
		{
			name: "defaults omitted", input: `{}`,
			want: ExternalAgentConfigDetectParams{}, canonical: `{"cwds":null}`,
		},
		{
			name: "false null and unknown", input: `{"includeHome":false,"cwds":null,"future":true}`,
			want: ExternalAgentConfigDetectParams{}, canonical: `{"cwds":null}`,
		},
		{
			name: "home and empty cwd list", input: `{"includeHome":true,"cwds":[]}`,
			want:      ExternalAgentConfigDetectParams{IncludeHome: true, CWDs: []string{}},
			canonical: `{"includeHome":true,"cwds":[]}`,
		},
		{
			name:      "opaque ordered cwd list",
			input:     `{"cwds":["","repo/../repo","/tmp/repo",""]}`,
			want:      ExternalAgentConfigDetectParams{CWDs: []string{"", "repo/../repo", "/tmp/repo", ""}},
			canonical: `{"cwds":["","repo/../repo","/tmp/repo",""]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var params ExternalAgentConfigDetectParams
			if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(params, tc.want) {
				t.Fatalf("params = %#v, want %#v", params, tc.want)
			}
			encoded, err := json.Marshal(params)
			if err != nil || string(encoded) != tc.canonical {
				t.Fatalf("canonical = %s, %v; want %s", encoded, err, tc.canonical)
			}
			var roundTrip ExternalAgentConfigDetectParams
			if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, params) {
				t.Fatalf("round trip = %#v, %v; want %#v", roundTrip, err, params)
			}
		})
	}

	params := ExternalAgentConfigDetectParams{CWDs: nil}
	encoded, err := json.Marshal(params)
	if err != nil || string(encoded) != `{"cwds":null}` {
		t.Fatalf("nil cwd list = %s, %v; want explicit null", encoded, err)
	}
}

func TestExternalAgentConfigDetectResponseAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  ExternalAgentConfigDetectResponse
	}{
		{
			name: "empty and unknown", input: `{"items":[],"future":{"nested":true}}`,
			want: ExternalAgentConfigDetectResponse{Items: []ExternalAgentConfigMigrationItem{}},
		},
		{
			name: "ordered duplicate items",
			input: `{"items":[` +
				`{"itemType":"CONFIG","description":"config"},` +
				`{"itemType":"CONFIG","description":"config","cwd":"repo/../repo","details":{}},` +
				`{"itemType":"CONFIG","description":"config"}` +
				`]}`,
			want: ExternalAgentConfigDetectResponse{Items: []ExternalAgentConfigMigrationItem{
				{ItemType: ExternalAgentConfigMigrationItemTypeConfig, Description: "config"},
				{
					ItemType: ExternalAgentConfigMigrationItemTypeConfig, Description: "config",
					CWD: stringPointer("repo/../repo"), Details: &MigrationDetails{},
				},
				{ItemType: ExternalAgentConfigMigrationItemTypeConfig, Description: "config"},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var response ExternalAgentConfigDetectResponse
			if err := json.Unmarshal([]byte(tc.input), &response); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(response, canonicalExternalAgentConfigDetectResponse(tc.want)) {
				t.Fatalf("response = %#v, want %#v", response, canonicalExternalAgentConfigDetectResponse(tc.want))
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var roundTrip ExternalAgentConfigDetectResponse
			if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, response) {
				t.Fatalf("round trip = %#v, %v; want %#v", roundTrip, err, response)
			}
		})
	}

	encoded, err := json.Marshal(ExternalAgentConfigDetectResponse{})
	if err != nil || string(encoded) != `{"items":[]}` {
		t.Fatalf("zero response = %s, %v; want non-null empty items", encoded, err)
	}
}

func TestExternalAgentConfigDetectParamsRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`,
		`{"includeHome":null}`, `{"includeHome":0}`, `{"includeHome":"true"}`,
		`{"includeHome":[]}`, `{"includeHome":{}}`,
		`{"cwds":"repo"}`, `{"cwds":1}`, `{"cwds":true}`, `{"cwds":{}}`,
		`{"cwds":[null]}`, `{"cwds":[1]}`, `{"cwds":[true]}`, `{"cwds":[{}]}`, `{"cwds":[[]]}`,
		`{"includeHome":false,"includeHome":true}`,
		`{"cwds":null,"cwds":[]}`,
		`{"includeHome":true`, `{} {}`,
	}
	for _, input := range invalid {
		var params ExternalAgentConfigDetectParams
		if err := json.Unmarshal([]byte(input), &params); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var params *ExternalAgentConfigDetectParams
	if err := params.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ExternalAgentConfigDetectParams receiver succeeded")
	}
}

func TestExternalAgentConfigDetectResponseRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"items":null}`, `{"items":{}}`, `{"items":"items"}`, `{"items":1}`, `{"items":true}`,
		`{"items":[null]}`, `{"items":[{}]}`, `{"items":[1]}`,
		`{"items":[{"itemType":"OTHER","description":"bad"}]}`,
		`{"items":[],"items":[]}`, `{"items":[]`, `{"items":[]} {}`,
	}
	for _, input := range invalid {
		var response ExternalAgentConfigDetectResponse
		if err := json.Unmarshal([]byte(input), &response); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var response *ExternalAgentConfigDetectResponse
	if err := response.UnmarshalJSON([]byte(`{"items":[]}`)); err == nil {
		t.Fatal("nil ExternalAgentConfigDetectResponse receiver succeeded")
	}
}

func TestDecodeExternalAgentConfigObjectRejectsMalformedEnvelopes(t *testing.T) {
	invalid := []string{``, `null`, `{"`, `{"includeHome":}`, `{"unknown":1`, `{} {}`, `{} {`}
	for _, input := range invalid {
		if _, err := decodeExternalAgentConfigObject(
			[]byte(input), "external-agent config detect params", "includeHome", "cwds",
		); err == nil {
			t.Errorf("decodeExternalAgentConfigObject(%q) succeeded", input)
		}
	}
}

func TestExternalAgentConfigDetectRecordsRemainStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		for _, name := range []string{"ExternalAgentConfigDetectParams", "ExternalAgentConfigDetectResponse"} {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound: %#v", name, binding)
			}
		}
	}
	info, ok := LookupMethod("externalAgentConfig/detect")
	if !ok || info.State != MethodDeferredStub {
		t.Fatalf("externalAgentConfig/detect = %#v, %v; want deferred stub", info, ok)
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

func TestExternalAgentConfigDetectTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type ExternalAgentConfigDetectParams = {\n" +
			"  \"cwds\"?: Array<string> | null;\n" +
			"  \"includeHome\"?: boolean;\n" +
			"};",
		"export type ExternalAgentConfigDetectResponse = {\n" +
			"  \"items\": Array<ExternalAgentConfigMigrationItem>;\n" +
			"};",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func canonicalExternalAgentConfigDetectResponse(response ExternalAgentConfigDetectResponse) ExternalAgentConfigDetectResponse {
	encoded, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}
	var canonical ExternalAgentConfigDetectResponse
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		panic(err)
	}
	return canonical
}

var (
	_ json.Marshaler   = ExternalAgentConfigDetectParams{}
	_ json.Unmarshaler = (*ExternalAgentConfigDetectParams)(nil)
	_ json.Marshaler   = ExternalAgentConfigDetectResponse{}
	_ json.Unmarshaler = (*ExternalAgentConfigDetectResponse)(nil)
)

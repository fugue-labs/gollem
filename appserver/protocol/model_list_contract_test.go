package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	modelListMinimalModelWire = `{"id":"id","model":"model","displayName":"name","description":"","hidden":false,` +
		`"supportedReasoningEfforts":[],"defaultReasoningEffort":"low","isDefault":false}`
	modelListCanonicalModelWire = `{"id":"id","model":"model","upgrade":null,"upgradeInfo":null,"availabilityNux":null,` +
		`"displayName":"name","description":"","hidden":false,"supportedReasoningEfforts":[],` +
		`"defaultReasoningEffort":"low","inputModalities":["text","image"],"supportsPersonality":false,` +
		`"additionalSpeedTiers":[],"serviceTiers":[],"defaultServiceTier":null,"isDefault":false}`
)

func TestModelListSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	params, ok := defs["ModelListParams"].(Schema)
	if !ok {
		t.Fatal("$defs missing ModelListParams")
	}
	if params["type"] != "object" || params["additionalProperties"] != false {
		t.Fatalf("ModelListParams is not a closed object: %#v", params)
	}
	if got := schemaRequiredNames(params); len(got) != 0 {
		t.Fatalf("ModelListParams required = %v, want none", got)
	}
	wantParamsProperties := Schema{
		"cursor": Schema{
			"description": "Opaque pagination cursor returned by a previous call.",
			"anyOf":       []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
		"limit": Schema{
			"description": "Optional page size; defaults to a reasonable server-side value.",
			"anyOf": []any{
				Schema{"type": "integer", "minimum": 0, "maximum": 4294967295},
				Schema{"type": "null"},
			},
		},
		"includeHidden": Schema{
			"description": "When true, include models that are hidden from the default picker list.",
			"anyOf":       []any{Schema{"type": "boolean"}, Schema{"type": "null"}},
		},
	}
	if got := params["properties"].(Schema); !reflect.DeepEqual(got, wantParamsProperties) {
		t.Fatalf("ModelListParams properties = %#v, want %#v", got, wantParamsProperties)
	}

	response, ok := defs["ModelListResponse"].(Schema)
	if !ok {
		t.Fatal("$defs missing ModelListResponse")
	}
	if response["type"] != "object" || response["additionalProperties"] != false {
		t.Fatalf("ModelListResponse is not a closed object: %#v", response)
	}
	if got := schemaRequiredNames(response); !slices.Equal(got, []string{"data", "nextCursor"}) {
		t.Fatalf("ModelListResponse required = %v", got)
	}
	wantResponseProperties := Schema{
		"data": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/Model"}},
		"nextCursor": Schema{
			"description": "Opaque cursor to pass to the next call to continue after the last item. If None, there are no more items to return.",
			"anyOf":       []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
	}
	if got := response["properties"].(Schema); !reflect.DeepEqual(got, wantResponseProperties) {
		t.Fatalf("ModelListResponse properties = %#v, want %#v", got, wantResponseProperties)
	}
}

func TestModelListParamsAcceptExactWireFormsAndCanonicalizeOptions(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: `{}`, want: `{"cursor":null,"limit":null,"includeHidden":null}`},
		{
			input: `{"cursor":null,"limit":null,"includeHidden":null}`,
			want:  `{"cursor":null,"limit":null,"includeHidden":null}`,
		},
		{
			input: `{"cursor":"","limit":0,"includeHidden":false}`,
			want:  `{"cursor":"","limit":0,"includeHidden":false}`,
		},
		{
			input: `{"cursor":"next","limit":4294967295,"includeHidden":true}`,
			want:  `{"cursor":"next","limit":4294967295,"includeHidden":true}`,
		},
	}
	for _, tc := range cases {
		var params ModelListParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}

	cursor := "next"
	limit := uint32(math.MaxUint32)
	includeHidden := false
	encoded, err := json.Marshal(ModelListParams{
		Cursor: &cursor, Limit: &limit, IncludeHidden: &includeHidden,
	})
	if err != nil || string(encoded) != `{"cursor":"next","limit":4294967295,"includeHidden":false}` {
		t.Fatalf("marshal populated params = %s, %v", encoded, err)
	}
}

func TestModelListParamsRejectMalformedWireForms(t *testing.T) {
	invalid := []string{
		`null`, `[]`, `"value"`, `1`,
		`{"cursor":1}`, `{"cursor":false}`,
		`{"limit":-1}`, `{"limit":1.5}`, `{"limit":4294967296}`, `{"limit":"1"}`,
		`{"includeHidden":0}`, `{"includeHidden":"false"}`,
		`{"providerId":"openai"}`, `{"limit":1,"pageSize":1}`,
		`{"limit":1} {}`,
	}
	for _, input := range invalid {
		var params ModelListParams
		if err := json.Unmarshal([]byte(input), &params); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	var params *ModelListParams
	if err := params.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ModelListParams receiver succeeded")
	}
}

func TestModelListResponseAcceptsExactWireFormsAndCanonicalizesCursor(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: `{"data":[]}`, want: `{"data":[],"nextCursor":null}`},
		{input: `{"data":[],"nextCursor":null}`, want: `{"data":[],"nextCursor":null}`},
		{
			input: `{"data":[` + modelListMinimalModelWire + `],"nextCursor":""}`,
			want:  `{"data":[` + modelListCanonicalModelWire + `],"nextCursor":""}`,
		},
		{
			input: `{"data":[` + modelListMinimalModelWire + `,` + modelListMinimalModelWire + `],"nextCursor":"next"}`,
			want:  `{"data":[` + modelListCanonicalModelWire + `,` + modelListCanonicalModelWire + `],"nextCursor":"next"}`,
		},
	}
	for _, tc := range cases {
		var response ModelListResponse
		if err := json.Unmarshal([]byte(tc.input), &response); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(response)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}
}

func TestModelListResponseRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		`null`, `[]`, `"value"`, `1`, `{}`,
		`{"data":null}`, `{"data":{}}`, `{"data":[null]}`, `{"data":[{}]}`,
		`{"data":[],"nextCursor":1}`, `{"data":[],"nextCursor":false}`,
		`{"data":[],"cursor":"next"}`, `{"data":[],"providerId":"openai"}`,
		`{"data":[]} {}`,
	}
	for _, input := range invalid {
		var response ModelListResponse
		if err := json.Unmarshal([]byte(input), &response); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	if _, err := json.Marshal(ModelListResponse{}); err == nil {
		t.Fatal("nil model data marshaled")
	}
	if _, err := json.Marshal(ModelListResponse{Data: []Model{{}}}); err == nil {
		t.Fatal("invalid nested model marshaled")
	}
	var response *ModelListResponse
	if err := response.UnmarshalJSON([]byte(`{"data":[]}`)); err == nil {
		t.Fatal("nil ModelListResponse receiver succeeded")
	}
}

func TestModelListContractsRemainStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ModelListParams") ||
			slices.Contains(binding.Result, "ModelListParams") ||
			slices.Contains(binding.Params, "ModelListResponse") ||
			slices.Contains(binding.Result, "ModelListResponse") {
			t.Fatalf("model-list type unexpectedly bound: %#v", binding)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestModelListTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ModelListParams = {`,
		`"cursor"?: string | null;`,
		`"includeHidden"?: boolean | null;`,
		`"limit"?: number | null;`,
		`export type ModelListResponse = {`,
		`"data": Array<Model>;`,
		`"nextCursor": string | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

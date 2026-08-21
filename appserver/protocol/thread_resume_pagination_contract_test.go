package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const resumePaginationTurnWire = `{"id":"turn","items":[],"itemsView":"summary","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`

func TestThreadResumePaginationSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	params, ok := defs["ThreadResumeInitialTurnsPageParams"].(Schema)
	if !ok {
		t.Fatal("$defs missing ThreadResumeInitialTurnsPageParams")
	}
	if params["type"] != "object" || params["additionalProperties"] != false {
		t.Fatalf("ThreadResumeInitialTurnsPageParams is not a closed object: %#v", params)
	}
	if got := schemaRequiredNames(params); len(got) != 0 {
		t.Fatalf("ThreadResumeInitialTurnsPageParams required = %v, want none", got)
	}
	wantParamsProperties := Schema{
		"limit": Schema{"anyOf": []any{
			Schema{"type": "integer", "minimum": 0},
			Schema{"type": "null"},
		}},
		"sortDirection": nullableSchemaRef("SortDirection"),
		"itemsView":     nullableSchemaRef("TurnItemsView"),
	}
	if got := params["properties"].(Schema); !reflect.DeepEqual(got, wantParamsProperties) {
		t.Fatalf("ThreadResumeInitialTurnsPageParams properties = %#v, want %#v", got, wantParamsProperties)
	}

	page, ok := defs["TurnsPage"].(Schema)
	if !ok {
		t.Fatal("$defs missing TurnsPage")
	}
	if page["type"] != "object" || page["additionalProperties"] != false {
		t.Fatalf("TurnsPage is not a closed object: %#v", page)
	}
	if got := schemaRequiredNames(page); !slices.Equal(got, []string{"data", "nextCursor", "backwardsCursor"}) {
		t.Fatalf("TurnsPage required = %v", got)
	}
	wantPageProperties := Schema{
		"data":            Schema{"type": "array", "items": Schema{"$ref": "#/$defs/Turn"}},
		"nextCursor":      Schema{"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}}},
		"backwardsCursor": Schema{"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}}},
	}
	if got := page["properties"].(Schema); !reflect.DeepEqual(got, wantPageProperties) {
		t.Fatalf("TurnsPage properties = %#v, want %#v", got, wantPageProperties)
	}
}

func TestThreadResumeInitialTurnsPageParamsCanonicalWireContract(t *testing.T) {
	valid := []struct {
		input string
		want  string
	}{
		{input: `{}`, want: `{"limit":null,"sortDirection":null,"itemsView":null}`},
		{
			input: `{"limit":null,"sortDirection":null,"itemsView":null}`,
			want:  `{"limit":null,"sortDirection":null,"itemsView":null}`,
		},
		{
			input: `{"limit":0,"sortDirection":"asc","itemsView":"notLoaded"}`,
			want:  `{"limit":0,"sortDirection":"asc","itemsView":"notLoaded"}`,
		},
		{
			input: `{"limit":4294967295,"sortDirection":"desc","itemsView":"full"}`,
			want:  `{"limit":4294967295,"sortDirection":"desc","itemsView":"full"}`,
		},
	}
	for _, tc := range valid {
		var params ThreadResumeInitialTurnsPageParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}

	maximum := uint32(math.MaxUint32)
	direction := SortDirectionDesc
	itemsView := TurnItemsViewSummary
	encoded, err := json.Marshal(ThreadResumeInitialTurnsPageParams{
		Limit: &maximum, SortDirection: &direction, ItemsView: &itemsView,
	})
	if err != nil || string(encoded) != `{"limit":4294967295,"sortDirection":"desc","itemsView":"summary"}` {
		t.Fatalf("marshal populated params = %s, %v", encoded, err)
	}
}

func TestThreadResumeInitialTurnsPageParamsRejectsMalformedForms(t *testing.T) {
	invalid := []string{
		`null`, `[]`, `"value"`, `1`,
		`{"limit":-1}`, `{"limit":1.5}`, `{"limit":4294967296}`, `{"limit":"1"}`,
		`{"sortDirection":"ascending"}`, `{"sortDirection":1}`,
		`{"itemsView":"all"}`, `{"itemsView":1}`,
		`{"limit":1,"pageSize":1}`,
		`{"limit":1} {}`,
	}
	for _, input := range invalid {
		var params ThreadResumeInitialTurnsPageParams
		if err := json.Unmarshal([]byte(input), &params); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	invalidDirection := SortDirection("ascending")
	if _, err := json.Marshal(ThreadResumeInitialTurnsPageParams{SortDirection: &invalidDirection}); err == nil {
		t.Fatal("invalid sort direction marshaled")
	}
	invalidItemsView := TurnItemsView("all")
	if _, err := json.Marshal(ThreadResumeInitialTurnsPageParams{ItemsView: &invalidItemsView}); err == nil {
		t.Fatal("invalid items view marshaled")
	}
	var params *ThreadResumeInitialTurnsPageParams
	if err := params.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ThreadResumeInitialTurnsPageParams receiver succeeded")
	}
}

func TestTurnsPageAcceptsCanonicalAndCompatibleForms(t *testing.T) {
	valid := []struct {
		input string
		want  string
	}{
		{input: `{"data":[]}`, want: `{"data":[],"nextCursor":null,"backwardsCursor":null}`},
		{
			input: `{"data":[],"nextCursor":null,"backwardsCursor":null}`,
			want:  `{"data":[],"nextCursor":null,"backwardsCursor":null}`,
		},
		{
			input: `{"data":[` + resumePaginationTurnWire + `],"nextCursor":"next","backwardsCursor":"back"}`,
			want:  `{"data":[` + resumePaginationTurnWire + `],"nextCursor":"next","backwardsCursor":"back"}`,
		},
	}
	for _, tc := range valid {
		var page TurnsPage
		if err := json.Unmarshal([]byte(tc.input), &page); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(page)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}
}

func TestTurnsPageRejectsMalformedForms(t *testing.T) {
	invalid := []string{
		`null`, `[]`, `"value"`, `1`, `{}`,
		`{"data":null}`, `{"data":{}}`, `{"data":[null]}`, `{"data":[{}]}`,
		`{"data":[],"nextCursor":1}`, `{"data":[],"backwardsCursor":false}`,
		`{"data":[],"cursor":"next"}`,
		`{"data":[]} {}`,
	}
	for _, input := range invalid {
		var page TurnsPage
		if err := json.Unmarshal([]byte(input), &page); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	if _, err := json.Marshal(TurnsPage{}); err == nil {
		t.Fatal("nil data marshaled")
	}
	if _, err := json.Marshal(TurnsPage{Data: []Turn{{}}}); err == nil {
		t.Fatal("invalid nested turn marshaled")
	}
	var page *TurnsPage
	if err := page.UnmarshalJSON([]byte(`{"data":[]}`)); err == nil {
		t.Fatal("nil TurnsPage receiver succeeded")
	}
}

func TestThreadResumePaginationContractsRemainStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ThreadResumeInitialTurnsPageParams") ||
			slices.Contains(binding.Result, "ThreadResumeInitialTurnsPageParams") ||
			slices.Contains(binding.Params, "TurnsPage") || slices.Contains(binding.Result, "TurnsPage") {
			t.Fatalf("resume pagination type unexpectedly bound: %#v", binding)
		}
	}
	resume := JSONSchema()["$defs"].(Schema)["ThreadResumeParams"].(Schema)
	if _, ok := resume["properties"].(Schema)["initialTurnsPage"]; ok {
		t.Fatal("ThreadResumeParams unexpectedly gained initialTurnsPage")
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestThreadResumePaginationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ThreadResumeInitialTurnsPageParams = {`,
		`"itemsView"?: TurnItemsView | null;`,
		`"limit"?: number | null;`,
		`"sortDirection"?: SortDirection | null;`,
		`export type TurnsPage = {`,
		`"backwardsCursor": string | null;`,
		`"data": Array<Turn>;`,
		`"nextCursor": string | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
	if strings.Contains(source, `export type ThreadResumeParams = {`+"\n"+`  "initialTurnsPage"`) {
		t.Fatal("generated ThreadResumeParams unexpectedly gained initialTurnsPage")
	}
}

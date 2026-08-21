package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadSearchResultSchemaIsExact(t *testing.T) {
	definition := JSONSchema()["$defs"].(Schema)["ThreadSearchResult"]
	want := Schema{
		"properties": Schema{
			"snippet": Schema{"type": "string"},
			"thread":  Schema{"$ref": "#/$defs/Thread"},
		},
		"required": []string{"snippet", "thread"},
		"type":     "object",
	}
	if !reflect.DeepEqual(definition, want) {
		t.Fatalf("ThreadSearchResult = %#v, want %#v", definition, want)
	}
}

func TestThreadSearchResultPreservesSerdeWireForm(t *testing.T) {
	const snippet = "matching text"
	var result ThreadSearchResult
	if err := json.Unmarshal([]byte(`{"future":true,"thread":`+publicThreadWire+`,"snippet":"`+snippet+`"}`), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	encoded, err := json.Marshal(result)
	want := `{"thread":` + publicThreadWire + `,"snippet":"` + snippet + `"}`
	if err != nil || string(encoded) != want {
		t.Fatalf("result round trip = %s, %v; want %s", encoded, err, want)
	}
}

func TestThreadSearchResultRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"thread":` + publicThreadWire + `}`, `{"snippet":"text"}`,
		`{"thread":null,"snippet":"text"}`, `{"thread":{},"snippet":"text"}`,
		`{"thread":` + publicThreadWire + `,"snippet":null}`,
		`{"thread":` + publicThreadWire + `,"snippet":"a","snippet":"b"}`,
		`{"thread":` + publicThreadWire + `,"snippet":"text"} {}`,
	} {
		assertJSONRejects[ThreadSearchResult](t, input)
	}
}

func TestThreadSearchResultFailsClosedAndRemainsStandalone(t *testing.T) {
	var result *ThreadSearchResult
	if err := result.UnmarshalJSON([]byte(`{"thread":` + publicThreadWire + `,"snippet":"text"}`)); err == nil {
		t.Fatal("nil result receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ThreadSearchResult") || slices.Contains(binding.Result, "ThreadSearchResult") {
			t.Fatalf("ThreadSearchResult unexpectedly bound to %s", binding.Method)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestThreadSearchResultTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type ThreadSearchResult = {\n" +
		"  \"snippet\": string;\n" +
		"  \"thread\": Thread;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

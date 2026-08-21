package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadInjectItemsSchemasAreExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	wants := map[string]Schema{
		"ThreadInjectItemsParams": {
			"$schema": "http://json-schema.org/draft-07/schema#",
			"properties": Schema{
				"items": Schema{
					"description": "Raw Responses API items to append to the thread's model-visible history.",
					"items":       true,
					"type":        "array",
				},
				"threadId": Schema{"type": "string"},
			},
			"required": []string{"items", "threadId"},
			"title":    "ThreadInjectItemsParams",
			"type":     "object",
		},
		"ThreadInjectItemsResponse": {
			"$schema": "http://json-schema.org/draft-07/schema#",
			"title":   "ThreadInjectItemsResponse",
			"type":    "object",
		},
	}
	for name, want := range wants {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestThreadInjectItemsContractsPreserveSerdeWireForms(t *testing.T) {
	const paramsInput = `{"future":true,"threadId":"thread","items":[null,true,4,"text",["nested"],{"kind":"message","content":"hello"}]}`
	const canonicalParams = `{"threadId":"thread","items":[null,true,4,"text",["nested"],{"kind":"message","content":"hello"}]}`
	var params ThreadInjectItemsParams
	if err := json.Unmarshal([]byte(paramsInput), &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	encoded, err := json.Marshal(params)
	if err != nil || string(encoded) != canonicalParams {
		t.Fatalf("params round trip = %s, %v; want %s", encoded, err, canonicalParams)
	}

	var response ThreadInjectItemsResponse
	if err := json.Unmarshal([]byte(`{"future":true}`), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	encoded, err = json.Marshal(response)
	if err != nil || string(encoded) != `{}` {
		t.Fatalf("response round trip = %s, %v; want {}", encoded, err)
	}
}

func TestThreadInjectItemsContractsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"threadId":"thread"}`, `{"items":[]}`,
		`{"threadId":null,"items":[]}`, `{"threadId":"thread","items":null}`,
		`{"threadId":"thread","items":{}}`,
		`{"threadId":"a","threadId":"b","items":[]}`,
		`{"threadId":"thread","items":[]} {}`,
	} {
		assertJSONRejects[ThreadInjectItemsParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}` + ` {}`,
	} {
		assertJSONRejects[ThreadInjectItemsResponse](t, input)
	}
}

func TestThreadInjectItemsContractsFailClosedAndRemainStandalone(t *testing.T) {
	var params *ThreadInjectItemsParams
	if err := params.UnmarshalJSON([]byte(`{"threadId":"thread","items":[]}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	var response *ThreadInjectItemsResponse
	if err := response.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}

	names := []string{"ThreadInjectItemsParams", "ThreadInjectItemsResponse"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestThreadInjectItemsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type ThreadInjectItemsParams = {\n" +
			"  \"items\": Array<JsonValue>;\n" +
			"  \"threadId\": string;\n" +
			"};",
		`export type ThreadInjectItemsResponse = Record<string, never>;`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

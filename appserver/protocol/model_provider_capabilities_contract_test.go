package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestModelProviderCapabilitiesSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	params, ok := defs["ModelProviderCapabilitiesReadParams"].(Schema)
	if !ok {
		t.Fatal("$defs missing ModelProviderCapabilitiesReadParams")
	}
	wantParams := Schema{
		"type":                 "object",
		"additionalProperties": false,
	}
	if !reflect.DeepEqual(params, wantParams) {
		t.Fatalf("ModelProviderCapabilitiesReadParams = %#v, want %#v", params, wantParams)
	}

	response, ok := defs["ModelProviderCapabilitiesReadResponse"].(Schema)
	if !ok {
		t.Fatal("$defs missing ModelProviderCapabilitiesReadResponse")
	}
	wantResponse := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": Schema{
			"namespaceTools":  Schema{"type": "boolean"},
			"imageGeneration": Schema{"type": "boolean"},
			"webSearch":       Schema{"type": "boolean"},
		},
		"required": []string{"namespaceTools", "imageGeneration", "webSearch"},
	}
	if !reflect.DeepEqual(response, wantResponse) {
		t.Fatalf("ModelProviderCapabilitiesReadResponse = %#v, want %#v", response, wantResponse)
	}
}

func TestModelProviderCapabilitiesParamsWireContract(t *testing.T) {
	var params ModelProviderCapabilitiesReadParams
	if err := json.Unmarshal([]byte(`{}`), &params); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	encoded, err := json.Marshal(params)
	if err != nil || string(encoded) != `{}` {
		t.Fatalf("Marshal = %s, %v; want {}", encoded, err)
	}

	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`,
		`{"providerId":"openai"}`,
		`{"provider":"openai"}`,
		`{"modelProvider":"openai"}`,
		`{"extra":false}`,
		`{} {}`,
	} {
		var value ModelProviderCapabilitiesReadParams
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	var nilParams *ModelProviderCapabilitiesReadParams
	if err := nilParams.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ModelProviderCapabilitiesReadParams receiver succeeded")
	}
}

func TestModelProviderCapabilitiesResponseAcceptsEveryBooleanCombination(t *testing.T) {
	values := []bool{false, true}
	for _, namespaceTools := range values {
		for _, imageGeneration := range values {
			for _, webSearch := range values {
				want := fmt.Sprintf(
					`{"namespaceTools":%t,"imageGeneration":%t,"webSearch":%t}`,
					namespaceTools,
					imageGeneration,
					webSearch,
				)
				var response ModelProviderCapabilitiesReadResponse
				if err := json.Unmarshal([]byte(want), &response); err != nil {
					t.Errorf("Unmarshal(%s): %v", want, err)
					continue
				}
				encoded, err := json.Marshal(response)
				if err != nil || string(encoded) != want {
					t.Errorf("round trip = %s, %v; want %s", encoded, err, want)
				}
			}
		}
	}
}

func TestModelProviderCapabilitiesResponseRejectsMalformedWireForms(t *testing.T) {
	valid := `{"namespaceTools":false,"imageGeneration":false,"webSearch":false}`
	invalid := []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"imageGeneration":false,"webSearch":false}`,
		`{"namespaceTools":false,"webSearch":false}`,
		`{"namespaceTools":false,"imageGeneration":false}`,
		`{"namespaceTools":null,"imageGeneration":false,"webSearch":false}`,
		`{"namespaceTools":false,"imageGeneration":null,"webSearch":false}`,
		`{"namespaceTools":false,"imageGeneration":false,"webSearch":null}`,
		`{"namespaceTools":0,"imageGeneration":false,"webSearch":false}`,
		`{"namespaceTools":false,"imageGeneration":"false","webSearch":false}`,
		`{"namespaceTools":false,"imageGeneration":false,"webSearch":[]}`,
		`{"namespaceTools":false,"imageGeneration":false,"webSearch":false,"configured":false}`,
		`{"namespaceTools":false,"imageGeneration":false,"webSearch":false,"providerId":"openai"}`,
		valid + ` {}`,
	}
	for _, input := range invalid {
		var response ModelProviderCapabilitiesReadResponse
		if err := json.Unmarshal([]byte(input), &response); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	var nilResponse *ModelProviderCapabilitiesReadResponse
	if err := nilResponse.UnmarshalJSON([]byte(valid)); err == nil {
		t.Fatal("nil ModelProviderCapabilitiesReadResponse receiver succeeded")
	}
}

func TestModelProviderCapabilitiesContractsRemainStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		for _, name := range []string{
			"ModelProviderCapabilitiesReadParams",
			"ModelProviderCapabilitiesReadResponse",
		} {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("model-provider capability type unexpectedly bound: %#v", binding)
			}
		}
	}
	info, ok := LookupMethod("modelProvider/capabilities/read")
	if !ok || info.State != MethodImplemented {
		t.Fatalf("modelProvider/capabilities/read = %#v, %v; want implemented", info, ok)
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

func TestModelProviderCapabilitiesTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ModelProviderCapabilitiesReadParams = Record<string, never>;`,
		`export type ModelProviderCapabilitiesReadResponse = {`,
		`"imageGeneration": boolean;`,
		`"namespaceTools": boolean;`,
		`"webSearch": boolean;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

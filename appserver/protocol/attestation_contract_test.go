package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAttestationContractsSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	wantParams := Schema{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title":   "AttestationGenerateParams",
		"type":    "object",
	}
	if got := defs["AttestationGenerateParams"]; !reflect.DeepEqual(got, wantParams) {
		t.Fatalf("AttestationGenerateParams = %#v, want %#v", got, wantParams)
	}
	wantResponse := Schema{
		"type": "object",
		"properties": Schema{
			"token": Schema{
				"type":        "string",
				"description": "Opaque client attestation token.",
			},
		},
		"required":             []string{"token"},
		"additionalProperties": false,
	}
	if got := defs["AttestationGenerateResponse"]; !reflect.DeepEqual(got, wantResponse) {
		t.Fatalf("AttestationGenerateResponse = %#v, want %#v", got, wantResponse)
	}
}

func TestAttestationGenerateParamsAcceptsSerdeObjects(t *testing.T) {
	for _, input := range []string{
		`{}`,
		`{"future":true}`,
		`{"nested":{"value":[1,null,"two"]}}`,
		`{"future":1,"future":2}`,
	} {
		var params AttestationGenerateParams
		if err := json.Unmarshal([]byte(input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil {
			t.Errorf("Marshal(%s): %v", input, err)
			continue
		}
		if string(encoded) != `{}` {
			t.Errorf("round trip %s = %s, want {}", input, encoded)
		}
	}
}

func TestAttestationGenerateParamsRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{} {}`, `{} x`,
	} {
		assertJSONRejects[AttestationGenerateParams](t, input)
	}

	var params *AttestationGenerateParams
	if err := params.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil AttestationGenerateParams receiver succeeded")
	}
}

func TestAttestationGenerateResponseAcceptsSerdeWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: `{"token":""}`, want: `{"token":""}`},
		{input: `{"future":true,"token":"opaque token"}`, want: `{"token":"opaque token"}`},
		{input: `{"token":"line\nvalue","nested":{"value":1}}`, want: `{"token":"line\nvalue"}`},
		{input: `{"token":"value","future":1,"future":2}`, want: `{"token":"value"}`},
	} {
		var response AttestationGenerateResponse
		if err := json.Unmarshal([]byte(test.input), &response); err != nil {
			t.Errorf("Unmarshal(%s): %v", test.input, err)
			continue
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Errorf("Marshal(%s): %v", test.input, err)
			continue
		}
		if string(encoded) != test.want {
			t.Errorf("round trip %s = %s, want %s", test.input, encoded, test.want)
		}
	}
}

func TestAttestationGenerateResponseRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `{}`, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"token":null}`,
		`{"token":1}`,
		`{"token":true}`,
		`{"token":[]}`,
		`{"token":{}}`,
		`{"token":"one","token":"two"}`,
		`{"token":"value"} {}`,
		`{"token":"value"} x`,
	} {
		assertJSONRejects[AttestationGenerateResponse](t, input)
	}

	var response *AttestationGenerateResponse
	if err := response.UnmarshalJSON([]byte(`{"token":"value"}`)); err == nil {
		t.Fatal("nil AttestationGenerateResponse receiver succeeded")
	}
}

func TestAttestationContractsRemainStandalone(t *testing.T) {
	for _, typeName := range []string{"AttestationGenerateParams", "AttestationGenerateResponse"} {
		for _, binding := range WireTypeBindings() {
			if slices.Contains(binding.Params, typeName) || slices.Contains(binding.Result, typeName) {
				t.Fatalf("%s unexpectedly bound to %s", typeName, binding.Method)
			}
		}
		for _, binding := range ItemPayloadBindings() {
			if binding.Type == typeName {
				t.Fatalf("%s unexpectedly bound to item %s", typeName, binding.Kind)
			}
		}
	}
	info, ok := LookupMethod("attestation/generate")
	if !ok || info.State != MethodBlocked {
		t.Fatalf("attestation/generate = %#v, %v; want blocked", info, ok)
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

func TestAttestationContractsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type AttestationGenerateParams = Record<string, never>;`,
		"export type AttestationGenerateResponse = {\n  \"token\": string;\n};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Fatalf("generated TypeScript missing %q", want)
		}
	}
}

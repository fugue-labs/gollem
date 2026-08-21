package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTurnSteerSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	params, ok := defs["TurnSteerParams"].(Schema)
	if !ok {
		t.Fatal("$defs missing TurnSteerParams")
	}
	if params["type"] != "object" || params["additionalProperties"] != false {
		t.Fatalf("TurnSteerParams is not a closed object: %#v", params)
	}
	if got := schemaRequiredNames(params); !slices.Equal(got, []string{"threadId", "input", "expectedTurnId"}) {
		t.Fatalf("TurnSteerParams required = %v", got)
	}
	wantProperties := Schema{
		"threadId":            Schema{"type": "string"},
		"clientUserMessageId": Schema{"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}}},
		"input":               Schema{"type": "array", "items": Schema{"$ref": "#/$defs/UserInput"}},
		"expectedTurnId":      Schema{"type": "string"},
	}
	if got := params["properties"].(Schema); !reflect.DeepEqual(got, wantProperties) {
		t.Fatalf("TurnSteerParams properties = %#v, want %#v", got, wantProperties)
	}

	response, ok := defs["TurnSteerResponse"].(Schema)
	if !ok {
		t.Fatal("$defs missing TurnSteerResponse")
	}
	wantResponse := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           Schema{"turnId": Schema{"type": "string"}},
		"required":             []string{"turnId"},
	}
	if !reflect.DeepEqual(response, wantResponse) {
		t.Fatalf("TurnSteerResponse = %#v, want %#v", response, wantResponse)
	}
}

func TestTurnSteerParamsAcceptExactPositiveForms(t *testing.T) {
	valid := []string{
		`{"threadId":"","input":[],"expectedTurnId":""}`,
		`{"threadId":"thread","clientUserMessageId":null,"input":[],"expectedTurnId":"turn"}`,
		`{"threadId":"thread","clientUserMessageId":"message","input":[` +
			`{"type":"text","text":"hello","text_elements":[]},` +
			`{"type":"image","url":"image.png"}],"expectedTurnId":"turn"}`,
	}
	for _, input := range valid {
		var params TurnSteerParams
		if err := json.Unmarshal([]byte(input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil {
			t.Errorf("Marshal(%s): %v", input, err)
			continue
		}
		var roundTrip TurnSteerParams
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Errorf("decode canonical %s: %v", encoded, err)
		}
	}
}

func TestTurnSteerParamsRejectMalformedAndLiveFields(t *testing.T) {
	invalid := []string{
		`null`, `[]`, `"value"`, `1`, `{}`,
		`{"input":[],"expectedTurnId":"turn"}`,
		`{"threadId":"thread","expectedTurnId":"turn"}`,
		`{"threadId":"thread","input":[]}`,
		`{"threadId":null,"input":[],"expectedTurnId":"turn"}`,
		`{"threadId":"thread","input":null,"expectedTurnId":"turn"}`,
		`{"threadId":"thread","input":[],"expectedTurnId":null}`,
		`{"threadId":"thread","clientUserMessageId":1,"input":[],"expectedTurnId":"turn"}`,
		`{"threadId":"thread","input":[null],"expectedTurnId":"turn"}`,
		`{"threadId":"thread","input":[{"type":"text"}],"expectedTurnId":"turn"}`,
	}
	for _, input := range invalid {
		var params TurnSteerParams
		if err := json.Unmarshal([]byte(input), &params); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	for _, field := range []string{
		"id", "turnId", "prompt", "message", "text", "accepted", "reason", "item",
		"responsesapiClientMetadata", "additionalContext",
	} {
		input := `{"threadId":"thread","input":[],"expectedTurnId":"turn","` + field + `":null}`
		var params TurnSteerParams
		if err := json.Unmarshal([]byte(input), &params); err == nil {
			t.Errorf("accepted %s", field)
		}
	}
	var trailing TurnSteerParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread","input":[],"expectedTurnId":"turn"} {}`), &trailing); err == nil {
		t.Fatal("accepted trailing value")
	}
}

func TestTurnSteerParamsMarshalAndNilReceiverFailClosed(t *testing.T) {
	for name, value := range map[string]TurnSteerParams{
		"nil input":     {ThreadID: "thread", ExpectedTurnID: "turn"},
		"invalid input": {ThreadID: "thread", Input: []UserInput{{}}, ExpectedTurnID: "turn"},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("%s marshaled", name)
		}
	}
	var params *TurnSteerParams
	if err := params.UnmarshalJSON([]byte(`{"threadId":"thread","input":[],"expectedTurnId":"turn"}`)); err == nil {
		t.Fatal("nil TurnSteerParams receiver succeeded")
	}
}

func TestTurnSteerResponseWireContract(t *testing.T) {
	for _, input := range []string{`{"turnId":""}`, `{"turnId":"turn"}`} {
		var response TurnSteerResponse
		if err := json.Unmarshal([]byte(input), &response); err != nil {
			t.Errorf("Unmarshal(%s): %v", input, err)
			continue
		}
		encoded, err := json.Marshal(response)
		if err != nil || string(encoded) != input {
			t.Errorf("round trip = %s, %v; want %s", encoded, err, input)
		}
	}
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `{}`, `{"turnId":null}`, `{"turnId":1}`,
		`{"turnId":"turn","accepted":true}`, `{"turnId":"turn","reason":"queued"}`,
		`{"turnId":"turn","item":{}}`, `{"turnId":"turn"} {}`,
	} {
		var response TurnSteerResponse
		if err := json.Unmarshal([]byte(input), &response); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	var nilResponse *TurnSteerResponse
	if err := nilResponse.UnmarshalJSON([]byte(`{"turnId":"turn"}`)); err == nil {
		t.Fatal("nil TurnSteerResponse receiver succeeded")
	}
}

func TestTurnSteerContractsAreBoundToLiveRuntime(t *testing.T) {
	found := false
	for _, binding := range WireTypeBindings() {
		if binding.Method == "turn/steer" {
			found = true
			if !slices.Equal(binding.Params, []string{"TurnSteerParams"}) ||
				!slices.Equal(binding.Result, []string{"TurnSteerResponse"}) {
				t.Fatalf("turn/steer binding = %#v", binding)
			}
		}
	}
	if !found {
		t.Fatal("turn/steer runtime binding missing")
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestTurnSteerTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type TurnSteerParams = {`,
		`"clientUserMessageId"?: string | null;`,
		`"expectedTurnId": string;`,
		`"input": Array<UserInput>;`,
		`"threadId": string;`,
		`export type TurnSteerResponse = {`,
		`"turnId": string;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

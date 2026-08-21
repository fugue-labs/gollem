package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCurrentTimeReadContractsAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	wants := map[string]Schema{
		"CurrentTimeReadParams": {
			"$schema": "http://json-schema.org/draft-07/schema#",
			"title":   "CurrentTimeReadParams",
			"type":    "object",
			"properties": Schema{
				"threadId": Schema{"type": "string"},
			},
			"required": []string{"threadId"},
		},
		"CurrentTimeReadResponse": {
			"$schema": "http://json-schema.org/draft-07/schema#",
			"title":   "CurrentTimeReadResponse",
			"type":    "object",
			"properties": Schema{
				"currentTimeAt": Schema{
					"description": "Current time as whole Unix seconds.",
					"format":      "int64",
					"type":        "integer",
				},
			},
			"required": []string{"currentTimeAt"},
		},
	}
	for name, want := range wants {
		if got := defs[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}

	bindings := WireTypeBindings()
	assertBinding(t, bindings, "currentTime/read", SurfaceServerRequest, "CurrentTimeReadParams")
	assertBinding(t, bindings, "currentTime/read", SurfaceServerRequest, "CurrentTimeReadResponse")
	info, ok := LookupMethod("currentTime/read")
	if !ok || info.State != MethodImplemented || info.Surface != SurfaceServerRequest {
		t.Fatalf("currentTime/read = %#v, %v; want implemented server request", info, ok)
	}
}

func TestCurrentTimeReadContractsMatchSerdeWireForms(t *testing.T) {
	paramsCases := []struct {
		input string
		want  string
	}{
		{`{"threadId":"thread-1"}`, `{"threadId":"thread-1"}`},
		{`{"future":true,"threadId":"thread-2"}`, `{"threadId":"thread-2"}`},
	}
	for _, test := range paramsCases {
		var params CurrentTimeReadParams
		if err := json.Unmarshal([]byte(test.input), &params); err != nil {
			t.Errorf("Unmarshal params %s: %v", test.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != test.want {
			t.Errorf("round trip params %s = %s, %v; want %s", test.input, encoded, err, test.want)
		}
	}

	responseCases := []struct {
		input string
		want  string
	}{
		{`{"currentTimeAt":0}`, `{"currentTimeAt":0}`},
		{`{"future":true,"currentTimeAt":1781717655}`, `{"currentTimeAt":1781717655}`},
		{`{"currentTimeAt":-1}`, `{"currentTimeAt":-1}`},
	}
	for _, test := range responseCases {
		var response CurrentTimeReadResponse
		if err := json.Unmarshal([]byte(test.input), &response); err != nil {
			t.Errorf("Unmarshal response %s: %v", test.input, err)
			continue
		}
		encoded, err := json.Marshal(response)
		if err != nil || string(encoded) != test.want {
			t.Errorf("round trip response %s = %s, %v; want %s", test.input, encoded, err, test.want)
		}
	}
}

func TestCurrentTimeReadContractsRejectMalformedWire(t *testing.T) {
	for _, input := range []string{
		``, `{}`, `null`, `[]`, `{"threadId":null}`, `{"threadId":1}`,
		`{"threadId":"one","threadId":"two"}`, `{"threadId":"thread"} null`,
	} {
		assertJSONRejects[CurrentTimeReadParams](t, input)
	}
	var nilParams *CurrentTimeReadParams
	if err := nilParams.UnmarshalJSON([]byte(`{"threadId":"thread"}`)); err == nil {
		t.Fatal("nil CurrentTimeReadParams receiver succeeded")
	}

	for _, input := range []string{
		``, `{}`, `null`, `[]`, `{"currentTimeAt":null}`, `{"currentTimeAt":"1"}`,
		`{"currentTimeAt":1.5}`, `{"currentTimeAt":9223372036854775808}`,
		`{"currentTimeAt":1,"currentTimeAt":2}`, `{"currentTimeAt":1} null`,
	} {
		assertJSONRejects[CurrentTimeReadResponse](t, input)
	}
	var nilResponse *CurrentTimeReadResponse
	if err := nilResponse.UnmarshalJSON([]byte(`{"currentTimeAt":1}`)); err == nil {
		t.Fatal("nil CurrentTimeReadResponse receiver succeeded")
	}
}

func TestCurrentTimeReadServerRequestAndTypeScriptAreExact(t *testing.T) {
	var request ServerRequest
	if err := json.Unmarshal([]byte(`{"method":"currentTime/read","id":"clock-1","params":{"threadId":"thread-1","future":true},"future":true}`), &request); err != nil {
		t.Fatalf("Unmarshal server request: %v", err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal server request: %v", err)
	}
	if got, want := string(encoded), `{"method":"currentTime/read","id":"clock-1","params":{"threadId":"thread-1"}}`; got != want {
		t.Fatalf("server request = %s, want %s", got, want)
	}

	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type CurrentTimeReadParams = ({\n  \"threadId\": string;\n} & Record<string, unknown>);",
		"export type CurrentTimeReadResponse = ({\n  \"currentTimeAt\": number;\n} & Record<string, unknown>);",
		`{ "method": "currentTime/read"; "id": RequestId; "params": CurrentTimeReadParams; }`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Fatalf("generated TypeScript missing %q", want)
		}
	}
}

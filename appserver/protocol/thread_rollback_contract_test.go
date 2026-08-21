package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadRollbackSchemasAreExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	wants := map[string]Schema{
		"ThreadRollbackParams": {
			"$schema":     "http://json-schema.org/draft-07/schema#",
			"description": "DEPRECATED: `thread/rollback` will be removed soon.",
			"properties": Schema{
				"numTurns": Schema{
					"description": "The number of turns to drop from the end of the thread. Must be >= 1.\n\nThis only modifies the thread's history and does not revert local file changes that have been made by the agent. Clients are responsible for reverting these changes.",
					"format":      "uint32",
					"minimum":     0.0,
					"type":        "integer",
				},
				"threadId": Schema{"type": "string"},
			},
			"required": []string{"numTurns", "threadId"},
			"title":    "ThreadRollbackParams",
			"type":     "object",
		},
		"ThreadRollbackResponse": {
			"$schema": "http://json-schema.org/draft-07/schema#",
			"properties": Schema{
				"thread": Schema{
					"allOf":       []any{Schema{"$ref": "#/$defs/Thread"}},
					"description": "The updated thread after applying the rollback, with `turns` populated.\n\nThe ThreadItems stored in each Turn are lossy since we explicitly do not persist all agent interactions, such as command executions. This is the same behavior as `thread/resume`.",
				},
			},
			"required": []string{"thread"},
			"title":    "ThreadRollbackResponse",
			"type":     "object",
		},
	}
	for name, want := range wants {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestThreadRollbackContractsPreserveSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"threadId":"thread","numTurns":0}`, `{"threadId":"thread","numTurns":0}`},
		{`{"future":true,"threadId":"thread","numTurns":4294967295}`, `{"threadId":"thread","numTurns":4294967295}`},
	} {
		var params ThreadRollbackParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("unmarshal params %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("params round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}

	var response ThreadRollbackResponse
	if err := json.Unmarshal([]byte(`{"future":true,"thread":`+publicThreadWire+`}`), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	encoded, err := json.Marshal(response)
	wantResponse := `{"thread":` + publicThreadWire + `}`
	if err != nil || string(encoded) != wantResponse {
		t.Fatalf("response round trip = %s, %v; want %s", encoded, err, wantResponse)
	}
}

func TestThreadRollbackContractsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"threadId":"thread"}`, `{"numTurns":0}`,
		`{"threadId":null,"numTurns":0}`, `{"threadId":"thread","numTurns":null}`,
		`{"threadId":"thread","numTurns":-1}`, `{"threadId":"thread","numTurns":1.5}`,
		`{"threadId":"thread","numTurns":4294967296}`,
		`{"threadId":"a","threadId":"b","numTurns":0}`,
		`{"threadId":"thread","numTurns":0} {}`,
	} {
		assertJSONRejects[ThreadRollbackParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"thread":null}`, `{"thread":{}}`,
		`{"thread":` + publicThreadWire + `,"thread":` + publicThreadWire + `}`,
		`{"thread":` + publicThreadWire + `} {}`,
	} {
		assertJSONRejects[ThreadRollbackResponse](t, input)
	}
}

func TestThreadRollbackContractsFailClosedAndRemainStandalone(t *testing.T) {
	var params *ThreadRollbackParams
	if err := params.UnmarshalJSON([]byte(`{"threadId":"thread","numTurns":0}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	var response *ThreadRollbackResponse
	if err := response.UnmarshalJSON([]byte(`{"thread":` + publicThreadWire + `}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}

	names := []string{"ThreadRollbackParams", "ThreadRollbackResponse"}
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

func TestThreadRollbackTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type ThreadRollbackParams = {\n" +
			"  \"numTurns\": number;\n" +
			"  \"threadId\": string;\n" +
			"};",
		"export type ThreadRollbackResponse = {\n" +
			"  \"thread\": Thread;\n" +
			"};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

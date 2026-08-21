package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadApproveGuardianDeniedActionSchemasAreExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	for name, want := range threadApproveGuardianDeniedActionSchemas() {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestThreadApproveGuardianDeniedActionContractsPreserveSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{
			`{"threadId":"thread-1","event":null}`,
			`{"threadId":"thread-1","event":null}`,
		},
		{
			`{"future":true,"event":{"type":"guardian","risks":[1,true]},"threadId":"thread-2"}`,
			`{"threadId":"thread-2","event":{"risks":[1,true],"type":"guardian"}}`,
		},
	} {
		var params ThreadApproveGuardianDeniedActionParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("unmarshal params %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("params round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}

	var response ThreadApproveGuardianDeniedActionResponse
	if err := json.Unmarshal([]byte(`{"future":true}`), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	encoded, err := json.Marshal(response)
	if err != nil || string(encoded) != `{}` {
		t.Fatalf("response round trip = %s, %v; want {}", encoded, err)
	}
}

func TestThreadApproveGuardianDeniedActionContractsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"threadId":"thread"}`, `{"event":null}`,
		`{"threadId":null,"event":{}}`, `{"threadId":"thread","event":}`,
		`{"threadId":"thread","event":undefined}`,
		`{"threadId":"thread","threadId":"other","event":{}}`,
		`{"threadId":"thread","event":{}} {}`,
	} {
		assertJSONRejects[ThreadApproveGuardianDeniedActionParams](t, input)
	}
	for _, input := range []string{``, `null`, `[]`, `"value"`, `1`, `true`, `{}` + ` {}`} {
		assertJSONRejects[ThreadApproveGuardianDeniedActionResponse](t, input)
	}
}

func TestThreadApproveGuardianDeniedActionContractsRemainStandalone(t *testing.T) {
	var params *ThreadApproveGuardianDeniedActionParams
	if err := params.UnmarshalJSON([]byte(`{"threadId":"thread","event":null}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	var response *ThreadApproveGuardianDeniedActionResponse
	if err := response.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}

	names := []string{"ThreadApproveGuardianDeniedActionParams", "ThreadApproveGuardianDeniedActionResponse"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if binding.Method == "thread/approveGuardianDeniedAction" ||
				slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	info, ok := LookupMethod("thread/approveGuardianDeniedAction")
	if !ok || info.Surface != SurfaceClientRequest || info.State != MethodDeferredStub {
		t.Fatalf("guardian denied action method = %#v, %v; want deferred client request", info, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestThreadApproveGuardianDeniedActionTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type ThreadApproveGuardianDeniedActionParams = {\n" +
			"  \"event\": JsonValue;\n" +
			"  \"threadId\": string;\n" +
			"};",
		`export type ThreadApproveGuardianDeniedActionResponse = Record<string, never>;`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

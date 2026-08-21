package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestLegacyApprovalResponseSchemasAreExact(t *testing.T) {
	want := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": Schema{
			"decision": Schema{"$ref": "#/$defs/ReviewDecision"},
		},
		"required": []string{"decision"},
	}
	defs := JSONSchema()["$defs"].(Schema)
	for _, name := range []string{"ApplyPatchApprovalResponse", "ExecCommandApprovalResponse"} {
		if got, ok := defs[name].(Schema); !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, %v; want %#v", name, got, ok, want)
		}
	}
}

func TestLegacyApprovalResponsesAcceptExactWireForms(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"decision":"approved"}`, `{"decision":"approved"}`},
		{`{"decision":"approved_for_session"}`, `{"decision":"approved_for_session"}`},
		{`{"decision":"denied"}`, `{"decision":"denied"}`},
		{`{"decision":"timed_out"}`, `{"decision":"timed_out"}`},
		{`{"decision":"abort"}`, `{"decision":"abort"}`},
		{`{"decision":{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":["git","git",""]}}}`, `{"decision":{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":["git","git",""]}}}`},
		{`{"decision":{"network_policy_amendment":{"network_policy_amendment":{"host":"","action":"allow"}}}}`, `{"decision":{"network_policy_amendment":{"network_policy_amendment":{"host":"","action":"allow"}}}}`},
		{`{"future":true,"decision":{"network_policy_amendment":{"future":true,"network_policy_amendment":{"host":"example.invalid","action":"deny","future":1}}}}`, `{"decision":{"network_policy_amendment":{"network_policy_amendment":{"host":"example.invalid","action":"deny"}}}}`},
	}
	for _, test := range tests {
		assertLegacyApprovalResponseRoundTrip[ApplyPatchApprovalResponse](t, test.input, test.want)
		assertLegacyApprovalResponseRoundTrip[ExecCommandApprovalResponse](t, test.input, test.want)
	}
}

func TestLegacyApprovalResponsesRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"decision":null}`,
		`{"decision":"accept"}`,
		`{"decision":{}}`,
		`{"decision":{"approved_execpolicy_amendment":null}}`,
		`{"decision":{"network_policy_amendment":{"network_policy_amendment":{"host":"h","action":"prompt"}}}}`,
		`{"decision":"approved","decision":"denied"}`,
		`{"decision":"approved"} {}`,
		`{"decision":"approved"} x`,
	} {
		assertJSONRejects[ApplyPatchApprovalResponse](t, input)
		assertJSONRejects[ExecCommandApprovalResponse](t, input)
	}

	if _, err := json.Marshal(ApplyPatchApprovalResponse{}); err == nil {
		t.Fatal("zero ApplyPatchApprovalResponse marshal succeeded")
	}
	if _, err := json.Marshal(ExecCommandApprovalResponse{}); err == nil {
		t.Fatal("zero ExecCommandApprovalResponse marshal succeeded")
	}
	var apply *ApplyPatchApprovalResponse
	if err := apply.UnmarshalJSON([]byte(`{"decision":"approved"}`)); err == nil {
		t.Fatal("nil ApplyPatchApprovalResponse receiver succeeded")
	}
	var exec *ExecCommandApprovalResponse
	if err := exec.UnmarshalJSON([]byte(`{"decision":"approved"}`)); err == nil {
		t.Fatal("nil ExecCommandApprovalResponse receiver succeeded")
	}
}

func TestLegacyApprovalResponsesRemainStandaloneAndNominal(t *testing.T) {
	if reflect.TypeFor[ApplyPatchApprovalResponse]() == reflect.TypeFor[ExecCommandApprovalResponse]() {
		t.Fatal("legacy approval response types unexpectedly alias")
	}
	if reflect.TypeFor[ExecCommandApprovalResponse]() == reflect.TypeFor[CommandExecutionRequestApprovalResponse]() ||
		reflect.TypeFor[ApplyPatchApprovalResponse]() == reflect.TypeFor[FileChangeRequestApprovalResponse]() {
		t.Fatal("legacy approval response aliases a live response")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range []string{"ApplyPatchApprovalResponse", "ExecCommandApprovalResponse"} {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	for _, method := range []string{"applyPatchApproval", "execCommandApproval"} {
		info, ok := LookupMethod(method)
		if !ok || info.State != MethodDeferredStub {
			t.Fatalf("%s = %#v, %v; want deferred stub", method, info, ok)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestLegacyApprovalResponsesTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type ApplyPatchApprovalResponse = {
  "decision": ReviewDecision;
};`,
		`export type ExecCommandApprovalResponse = {
  "decision": ReviewDecision;
};`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertLegacyApprovalResponseRoundTrip[T any](t *testing.T, input, want string) {
	t.Helper()
	var response T
	if err := json.Unmarshal([]byte(input), &response); err != nil {
		t.Errorf("Unmarshal[%T](%s): %v", response, input, err)
		return
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Errorf("Marshal[%T](%s): %v", response, input, err)
		return
	}
	if string(encoded) != want {
		t.Errorf("round trip[%T](%s) = %s; want %s", response, input, encoded, want)
		return
	}
	var canonical T
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		t.Errorf("canonical Unmarshal[%T](%s): %v", response, encoded, err)
		return
	}
	reencoded, err := json.Marshal(canonical)
	if err != nil || string(reencoded) != string(encoded) {
		t.Errorf("canonical round trip[%T] = %s, %v; want %s", response, reencoded, err, encoded)
	}
}

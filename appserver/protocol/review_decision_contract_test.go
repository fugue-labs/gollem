package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestReviewDecisionSchemaIsExact(t *testing.T) {
	definition, ok := JSONSchema()["$defs"].(Schema)["ReviewDecision"].(Schema)
	if !ok {
		t.Fatal("$defs missing ReviewDecision")
	}
	if want := expectedReviewDecisionSchema(); !reflect.DeepEqual(definition, want) {
		t.Fatalf("ReviewDecision = %#v, want %#v", definition, want)
	}
}

func TestReviewDecisionAcceptsExactWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: `"approved"`, want: `"approved"`},
		{input: `"approved_for_session"`, want: `"approved_for_session"`},
		{input: `"denied"`, want: `"denied"`},
		{input: `"timed_out"`, want: `"timed_out"`},
		{input: `"abort"`, want: `"abort"`},
		{
			input: `{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":[]}}`,
			want:  `{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":[]}}`,
		},
		{
			input: `{"approved_execpolicy_amendment":{"future":true,"proposed_execpolicy_amendment":["git","git",""]}}`,
			want:  `{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":["git","git",""]}}`,
		},
		{
			input: `{"network_policy_amendment":{"future":true,"network_policy_amendment":{"future":1,"host":"","action":"allow"}}}`,
			want:  `{"network_policy_amendment":{"network_policy_amendment":{"host":"","action":"allow"}}}`,
		},
		{
			input: `{"network_policy_amendment":{"network_policy_amendment":{"host":"example.invalid","action":"deny"}}}`,
			want:  `{"network_policy_amendment":{"network_policy_amendment":{"host":"example.invalid","action":"deny"}}}`,
		},
	} {
		var decision ReviewDecision
		if err := json.Unmarshal([]byte(test.input), &decision); err != nil {
			t.Errorf("Unmarshal(%s): %v", test.input, err)
			continue
		}
		encoded, err := json.Marshal(decision)
		if err != nil || string(encoded) != test.want {
			t.Errorf("round trip %s = %s, %v; want %s", test.input, encoded, err, test.want)
		}
	}
}

func TestReviewDecisionRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `1`, `true`, `{}`, `{`,
		`"accept"`, `"Approved"`, `"approve"`, `""`,
		`{"approved":null}`,
		`{"approved_execpolicy_amendment":null}`,
		`{"approved_execpolicy_amendment":{}}`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":null}}`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":{}}}`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":[1]}}`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":[null]}}`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":[],"proposed_execpolicy_amendment":[]}}`,
		`{"network_policy_amendment":null}`,
		`{"network_policy_amendment":{}}`,
		`{"network_policy_amendment":{"network_policy_amendment":null}}`,
		`{"network_policy_amendment":{"network_policy_amendment":[]}}`,
		`{"network_policy_amendment":{"network_policy_amendment":{}}}`,
		`{"network_policy_amendment":{"network_policy_amendment":{"host":null,"action":"allow"}}}`,
		`{"network_policy_amendment":{"network_policy_amendment":{"host":1,"action":"allow"}}}`,
		`{"network_policy_amendment":{"network_policy_amendment":{"host":"host","action":null}}}`,
		`{"network_policy_amendment":{"network_policy_amendment":{"host":"host","action":"prompt"}}}`,
		`{"network_policy_amendment":{"network_policy_amendment":{"host":"one","host":"two","action":"allow"}}}`,
		`{"network_policy_amendment":{"network_policy_amendment":{"host":"host","action":"allow","action":"deny"}}}`,
		`{"network_policy_amendment":{"network_policy_amendment":{"host":"host","action":"allow"},"network_policy_amendment":{"host":"host","action":"deny"}}}`,
		`{"other":{}}`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":[]},"network_policy_amendment":{"network_policy_amendment":{"host":"host","action":"allow"}}}`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":[]},"other":true}`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":[]}} {}`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":[]}} x`,
	} {
		assertJSONRejects[ReviewDecision](t, input)
	}

	if _, err := json.Marshal(ReviewDecision{}); err == nil {
		t.Fatal("zero ReviewDecision marshal succeeded")
	}
	var decision *ReviewDecision
	if err := decision.UnmarshalJSON([]byte(`"approved"`)); err == nil {
		t.Fatal("nil ReviewDecision receiver succeeded")
	}
	if _, err := json.Marshal(ReviewDecision{raw: json.RawMessage(`"other"`)}); err == nil {
		t.Fatal("invalid internal ReviewDecision marshal succeeded")
	}
}

func TestReviewDecisionInternalDecoderErrors(t *testing.T) {
	for _, input := range []string{
		``,
		`{"`,
		`{"approved_execpolicy_amendment":`,
		`{"approved_execpolicy_amendment":{}`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":[]}} {}`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":[]}} x`,
	} {
		if _, _, err := decodeReviewDecisionVariant([]byte(input)); err == nil {
			t.Errorf("decodeReviewDecisionVariant(%q) succeeded", input)
		}
	}
}

func TestReviewDecisionRemainsStandalone(t *testing.T) {
	if reflect.TypeFor[ReviewDecision]() == reflect.TypeFor[CommandExecutionApprovalDecision]() {
		t.Fatal("legacy review decision aliases live command-execution approval decision")
	}
	defs := JSONSchema()["$defs"].(Schema)
	for _, name := range []string{"ExecCommandApprovalResponse", "ApplyPatchApprovalResponse"} {
		if _, ok := defs[name]; !ok {
			t.Fatalf("dependency-complete %s missing", name)
		}
	}
	if reflect.DeepEqual(defs["ReviewDecision"], defs["CommandExecutionApprovalDecision"]) {
		t.Fatal("legacy and live decision schemas unexpectedly match")
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ReviewDecision") ||
			slices.Contains(binding.Result, "ReviewDecision") {
			t.Fatalf("ReviewDecision unexpectedly bound to %s", binding.Method)
		}
	}
	if got := len(defs); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestReviewDecisionTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type ReviewDecision = "approved" | {
  "approved_execpolicy_amendment": {
    "proposed_execpolicy_amendment": ExecPolicyAmendment;
  };
} | "approved_for_session" | {
  "network_policy_amendment": {
    "network_policy_amendment": NetworkPolicyAmendment;
  };
} | "denied" | "timed_out" | "abort";`
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}

func expectedReviewDecisionSchema() Schema {
	return Schema{
		"description": "User's decision in response to an ExecApprovalRequest.",
		"oneOf": []any{
			reviewDecisionLiteralSchema(
				"approved",
				"User has approved this command and the agent should execute it.",
			),
			Schema{
				"type":                 "object",
				"title":                "ApprovedExecpolicyAmendmentReviewDecision",
				"description":          "User has approved this command and wants to apply the proposed execpolicy amendment so future matching commands are permitted.",
				"additionalProperties": false,
				"properties": Schema{
					"approved_execpolicy_amendment": Schema{
						"type":                 "object",
						"additionalProperties": false,
						"properties": Schema{
							"proposed_execpolicy_amendment": Schema{"$ref": "#/$defs/ExecPolicyAmendment"},
						},
						"required": []string{"proposed_execpolicy_amendment"},
					},
				},
				"required": []string{"approved_execpolicy_amendment"},
			},
			reviewDecisionLiteralSchema(
				"approved_for_session",
				"User has approved this request and wants future prompts in the same session-scoped approval cache to be automatically approved for the remainder of the session.",
			),
			Schema{
				"type":                 "object",
				"title":                "NetworkPolicyAmendmentReviewDecision",
				"description":          "User chose to persist a network policy rule (allow/deny) for future requests to the same host.",
				"additionalProperties": false,
				"properties": Schema{
					"network_policy_amendment": Schema{
						"type":                 "object",
						"additionalProperties": false,
						"properties": Schema{
							"network_policy_amendment": Schema{"$ref": "#/$defs/NetworkPolicyAmendment"},
						},
						"required": []string{"network_policy_amendment"},
					},
				},
				"required": []string{"network_policy_amendment"},
			},
			reviewDecisionLiteralSchema(
				"denied",
				"User has denied this command and the agent should not execute it, but it should continue the session and try something else.",
			),
			reviewDecisionLiteralSchema(
				"timed_out",
				"Automatic approval review timed out before reaching a decision.",
			),
			reviewDecisionLiteralSchema(
				"abort",
				"User has denied this command and the agent should not do anything until the user's next command.",
			),
		},
	}
}

func reviewDecisionLiteralSchema(value, description string) Schema {
	return Schema{
		"type":        "string",
		"enum":        []any{value},
		"description": description,
	}
}

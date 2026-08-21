package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestGuardianApprovalReviewSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	want := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": Schema{
			"status": Schema{"$ref": "#/$defs/GuardianApprovalReviewStatus"},
			"riskLevel": Schema{"anyOf": []any{
				Schema{"$ref": "#/$defs/GuardianRiskLevel"}, Schema{"type": "null"},
			}},
			"userAuthorization": Schema{"anyOf": []any{
				Schema{"$ref": "#/$defs/GuardianUserAuthorization"}, Schema{"type": "null"},
			}},
			"rationale": Schema{"type": []any{"string", "null"}},
		},
		"required": []string{"status"},
	}
	if got := defs["GuardianApprovalReview"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("GuardianApprovalReview schema = %#v, want %#v", got, want)
	}
}

func TestGuardianApprovalReviewAcceptsSerdeWireForms(t *testing.T) {
	risk := GuardianRiskLevelCritical
	authorization := GuardianUserAuthorizationUnknown
	rationale := "\nopaque rationale \u2603\n"
	tests := []struct {
		name      string
		input     string
		want      GuardianApprovalReview
		canonical string
	}{
		{
			name:      "omitted options become explicit null output",
			input:     `{"status":"inProgress"}`,
			want:      GuardianApprovalReview{Status: GuardianApprovalReviewStatusInProgress},
			canonical: `{"status":"inProgress","riskLevel":null,"userAuthorization":null,"rationale":null}`,
		},
		{
			name:      "explicit null options",
			input:     `{"status":"approved","riskLevel":null,"userAuthorization":null,"rationale":null}`,
			want:      GuardianApprovalReview{Status: GuardianApprovalReviewStatusApproved},
			canonical: `{"status":"approved","riskLevel":null,"userAuthorization":null,"rationale":null}`,
		},
		{
			name:  "all values and unknown fields",
			input: `{"unknown":{"nested":true},"status":"denied","riskLevel":"critical","userAuthorization":"unknown","rationale":"\nopaque rationale \u2603\n"}`,
			want: GuardianApprovalReview{
				Status: GuardianApprovalReviewStatusDenied, RiskLevel: &risk,
				UserAuthorization: &authorization, Rationale: &rationale,
			},
			canonical: `{"status":"denied","riskLevel":"critical","userAuthorization":"unknown","rationale":"\nopaque rationale ` + "\u2603" + `\n"}`,
		},
		{
			name:      "empty rationale",
			input:     `{"status":"aborted","rationale":""}`,
			want:      GuardianApprovalReview{Status: GuardianApprovalReviewStatusAborted, Rationale: ptr("")},
			canonical: `{"status":"aborted","riskLevel":null,"userAuthorization":null,"rationale":""}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got GuardianApprovalReview
			if err := json.Unmarshal([]byte(test.input), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("decoded = %#v, want %#v", got, test.want)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(encoded) != test.canonical {
				t.Fatalf("canonical = %s, want %s", encoded, test.canonical)
			}
		})
	}
}

func TestGuardianApprovalReviewRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `1`, `true`, `"value"`, `{`,
		`{}`, `{"riskLevel":"low"}`, `{"status":null}`,
		`{"status":"other"}`, `{"status":"InProgress"}`, `{"status":1}`,
		`{"status":"approved","riskLevel":"other"}`,
		`{"status":"approved","riskLevel":1}`,
		`{"status":"approved","userAuthorization":"other"}`,
		`{"status":"approved","userAuthorization":false}`,
		`{"status":"approved","rationale":1}`,
		`{"status":"approved","status":"denied"}`,
		`{"status":"approved","riskLevel":null,"riskLevel":"low"}`,
		`{"status":"approved","userAuthorization":null,"userAuthorization":"high"}`,
		`{"status":"approved","rationale":null,"rationale":"value"}`,
		`{"status":"approved"} {}`, `{"status":"approved"} x`,
	} {
		assertJSONRejects[GuardianApprovalReview](t, input)
	}
}

func TestGuardianApprovalReviewNilReceiverAndInvalidValuesFailClosed(t *testing.T) {
	var review *GuardianApprovalReview
	if err := review.UnmarshalJSON([]byte(`{"status":"approved"}`)); err == nil {
		t.Fatal("nil GuardianApprovalReview receiver succeeded")
	}
	invalidRisk := GuardianRiskLevel("other")
	invalidAuthorization := GuardianUserAuthorization("other")
	for _, review := range []GuardianApprovalReview{
		{},
		{Status: GuardianApprovalReviewStatus("other")},
		{Status: GuardianApprovalReviewStatusApproved, RiskLevel: &invalidRisk},
		{Status: GuardianApprovalReviewStatusApproved, UserAuthorization: &invalidAuthorization},
	} {
		if _, err := json.Marshal(review); err == nil {
			t.Fatalf("invalid review marshaled: %#v", review)
		}
	}
}

func TestGuardianApprovalReviewRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "GuardianApprovalReview") ||
			slices.Contains(binding.Result, "GuardianApprovalReview") {
			t.Fatalf("GuardianApprovalReview unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "GuardianApprovalReview" {
			t.Fatalf("GuardianApprovalReview unexpectedly bound to item %s", binding.Kind)
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

func TestGuardianApprovalReviewTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type GuardianApprovalReview = {
  "rationale": string | null;
  "riskLevel": GuardianRiskLevel | null;
  "status": GuardianApprovalReviewStatus;
  "userAuthorization": GuardianUserAuthorization | null;
};`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
	for _, forbidden := range []string{
		`"rationale"?:`, `"riskLevel"?:`, `"userAuthorization"?:`,
	} {
		if strings.Contains(string(generated), forbidden) {
			t.Errorf("generated TypeScript unexpectedly contains %q", forbidden)
		}
	}
}

func ptr[T any](value T) *T { return &value }

var (
	_ json.Marshaler   = GuardianApprovalReview{}
	_ json.Unmarshaler = (*GuardianApprovalReview)(nil)
)

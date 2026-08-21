package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestGuardianApprovalReviewCompletedNotificationSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	want := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": Schema{
			"threadId":       Schema{"type": "string"},
			"turnId":         Schema{"type": "string"},
			"startedAtMs":    Schema{"type": "integer"},
			"completedAtMs":  Schema{"type": "integer"},
			"reviewId":       Schema{"type": "string"},
			"targetItemId":   Schema{"type": []any{"string", "null"}},
			"decisionSource": Schema{"$ref": "#/$defs/AutoReviewDecisionSource"},
			"review":         Schema{"$ref": "#/$defs/GuardianApprovalReview"},
			"action":         Schema{"$ref": "#/$defs/GuardianApprovalReviewAction"},
		},
		"required": []string{
			"threadId", "turnId", "startedAtMs", "completedAtMs", "reviewId",
			"decisionSource", "review", "action",
		},
	}
	if got := defs["ItemGuardianApprovalReviewCompletedNotification"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("completed-notification schema = %#v, want %#v", got, want)
	}
}

func TestGuardianApprovalReviewCompletedNotificationAcceptsSerdeWireForms(t *testing.T) {
	validReview := `{"status":"approved"}`
	validAction := `{"type":"command","source":"shell","command":"","cwd":"/workspace"}`
	tests := []struct {
		name      string
		input     string
		want      ItemGuardianApprovalReviewCompletedNotification
		canonical string
	}{
		{
			name: "omitted target becomes explicit null",
			input: `{"threadId":"","turnId":"","startedAtMs":-9223372036854775808,` +
				`"completedAtMs":9223372036854775807,"reviewId":"","decisionSource":"agent",` +
				`"review":` + validReview + `,"action":` + validAction + `}`,
			want: ItemGuardianApprovalReviewCompletedNotification{
				StartedAtMS: math.MinInt64, CompletedAtMS: math.MaxInt64,
				DecisionSource: AutoReviewDecisionSourceAgent,
				Review:         GuardianApprovalReview{Status: GuardianApprovalReviewStatusApproved},
				Action:         mustGuardianApprovalReviewAction(t, validAction),
			},
			canonical: `{"threadId":"","turnId":"","startedAtMs":-9223372036854775808,` +
				`"completedAtMs":9223372036854775807,"reviewId":"","targetItemId":null,` +
				`"decisionSource":"agent",` +
				`"review":{"status":"approved","riskLevel":null,"userAuthorization":null,"rationale":null},` +
				`"action":{"type":"command","source":"shell","command":"","cwd":"/workspace"}}`,
		},
		{
			name: "target and unknown field",
			input: `{"unknown":true,"threadId":"thread","turnId":"turn","startedAtMs":2,` +
				`"completedAtMs":1,"reviewId":"review","targetItemId":"item",` +
				`"decisionSource":"agent","review":` + validReview + `,"action":` + validAction + `}`,
			want: ItemGuardianApprovalReviewCompletedNotification{
				ThreadID: "thread", TurnID: "turn", StartedAtMS: 2, CompletedAtMS: 1,
				ReviewID: "review", TargetItemID: ptr("item"),
				DecisionSource: AutoReviewDecisionSourceAgent,
				Review:         GuardianApprovalReview{Status: GuardianApprovalReviewStatusApproved},
				Action:         mustGuardianApprovalReviewAction(t, validAction),
			},
			canonical: `{"threadId":"thread","turnId":"turn","startedAtMs":2,"completedAtMs":1,` +
				`"reviewId":"review","targetItemId":"item","decisionSource":"agent",` +
				`"review":{"status":"approved","riskLevel":null,"userAuthorization":null,"rationale":null},` +
				`"action":{"type":"command","source":"shell","command":"","cwd":"/workspace"}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got ItemGuardianApprovalReviewCompletedNotification
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

func TestGuardianApprovalReviewCompletedNotificationRejectsMalformedWireForms(t *testing.T) {
	valid := `{"threadId":"thread","turnId":"turn","startedAtMs":1,"completedAtMs":2,` +
		`"reviewId":"review","decisionSource":"agent","review":{"status":"approved"},` +
		`"action":{"type":"applyPatch","cwd":"/workspace","files":[]}}`
	for _, input := range []string{
		``, `null`, `[]`, `1`, `true`, `"value"`, `{`, `{}`,
		strings.Replace(valid, `"threadId":"thread",`, ``, 1),
		strings.Replace(valid, `"turnId":"turn",`, ``, 1),
		strings.Replace(valid, `"startedAtMs":1,`, ``, 1),
		strings.Replace(valid, `"completedAtMs":2,`, ``, 1),
		strings.Replace(valid, `"reviewId":"review",`, ``, 1),
		strings.Replace(valid, `"decisionSource":"agent",`, ``, 1),
		strings.Replace(valid, `"review":{"status":"approved"},`, ``, 1),
		strings.Replace(valid, `,"action":{"type":"applyPatch","cwd":"/workspace","files":[]}`, ``, 1),
		strings.Replace(valid, `"threadId":"thread"`, `"threadId":null`, 1),
		strings.Replace(valid, `"startedAtMs":1`, `"startedAtMs":1.5`, 1),
		strings.Replace(valid, `"startedAtMs":1`, `"startedAtMs":9223372036854775808`, 1),
		strings.Replace(valid, `"completedAtMs":2`, `"completedAtMs":null`, 1),
		strings.Replace(valid, `"completedAtMs":2`, `"completedAtMs":-9223372036854775809`, 1),
		strings.Replace(valid, `"decisionSource":"agent"`, `"decisionSource":"other"`, 1),
		strings.Replace(valid, `"review":{"status":"approved"}`, `"review":null`, 1),
		strings.Replace(valid, `"action":{"type":"applyPatch","cwd":"/workspace","files":[]}`, `"action":null`, 1),
		strings.Replace(valid, `"reviewId":"review"`, `"targetItemId":1,"reviewId":"review"`, 1),
		strings.Replace(valid, `"threadId":"thread"`, `"threadId":"thread","threadId":"other"`, 1),
		strings.Replace(valid, `"completedAtMs":2`, `"completedAtMs":2,"completedAtMs":3`, 1),
		valid + ` {}`,
		valid + ` x`,
	} {
		assertJSONRejects[ItemGuardianApprovalReviewCompletedNotification](t, input)
	}
}

func TestGuardianApprovalReviewCompletedNotificationNilReceiverAndInvalidValuesFailClosed(t *testing.T) {
	var notification *ItemGuardianApprovalReviewCompletedNotification
	if err := notification.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil completed-notification receiver succeeded")
	}
	validAction := mustGuardianApprovalReviewAction(t,
		`{"type":"command","source":"shell","command":"","cwd":"/workspace"}`)
	for _, notification := range []ItemGuardianApprovalReviewCompletedNotification{
		{},
		{
			DecisionSource: AutoReviewDecisionSource("other"),
			Review:         GuardianApprovalReview{Status: GuardianApprovalReviewStatusApproved},
			Action:         validAction,
		},
	} {
		if _, err := json.Marshal(notification); err == nil {
			t.Fatalf("invalid notification marshaled: %#v", notification)
		}
	}
}

func TestGuardianApprovalReviewCompletedNotificationRemainsStandalone(t *testing.T) {
	const typeName = "ItemGuardianApprovalReviewCompletedNotification"
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, typeName) || slices.Contains(binding.Result, typeName) {
			t.Fatalf("completed notification unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == typeName {
			t.Fatalf("completed notification unexpectedly bound to item %s", binding.Kind)
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
	for _, method := range Methods() {
		if (method.Method == "item/autoApprovalReview/started" ||
			method.Method == "item/autoApprovalReview/completed") && method.State != MethodBlocked {
			t.Fatalf("%s state = %s, want blocked", method.Method, method.State)
		}
	}
}

func TestGuardianApprovalReviewCompletedNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type ItemGuardianApprovalReviewCompletedNotification = {
  "action": GuardianApprovalReviewAction;
  "completedAtMs": number;
  "decisionSource": AutoReviewDecisionSource;
  "review": GuardianApprovalReview;
  "reviewId": string;
  "startedAtMs": number;
  "targetItemId": string | null;
  "threadId": string;
  "turnId": string;
};`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
	for _, forbidden := range []string{`"targetItemId"?:`, `"startedAtMs": bigint`} {
		if strings.Contains(string(generated), forbidden) {
			t.Errorf("generated TypeScript unexpectedly contains %q", forbidden)
		}
	}
}

var (
	_ json.Marshaler   = ItemGuardianApprovalReviewCompletedNotification{}
	_ json.Unmarshaler = (*ItemGuardianApprovalReviewCompletedNotification)(nil)
)

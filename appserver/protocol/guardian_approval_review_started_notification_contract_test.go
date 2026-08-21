package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestGuardianApprovalReviewStartedNotificationSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	want := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": Schema{
			"threadId":     Schema{"type": "string"},
			"turnId":       Schema{"type": "string"},
			"startedAtMs":  Schema{"type": "integer"},
			"reviewId":     Schema{"type": "string"},
			"targetItemId": Schema{"type": []any{"string", "null"}},
			"review":       Schema{"$ref": "#/$defs/GuardianApprovalReview"},
			"action":       Schema{"$ref": "#/$defs/GuardianApprovalReviewAction"},
		},
		"required": []string{
			"threadId", "turnId", "startedAtMs", "reviewId", "review", "action",
		},
	}
	if got := defs["ItemGuardianApprovalReviewStartedNotification"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("started-notification schema = %#v, want %#v", got, want)
	}
}

func TestGuardianApprovalReviewStartedNotificationAcceptsSerdeWireForms(t *testing.T) {
	validReview := `{"status":"inProgress"}`
	validAction := `{"type":"command","source":"shell","command":"","cwd":"/workspace"}`
	tests := []struct {
		name      string
		input     string
		want      ItemGuardianApprovalReviewStartedNotification
		canonical string
	}{
		{
			name: "omitted target becomes explicit null",
			input: `{"threadId":"","turnId":"","startedAtMs":-9223372036854775808,` +
				`"reviewId":"","review":` + validReview + `,"action":` + validAction + `}`,
			want: ItemGuardianApprovalReviewStartedNotification{
				StartedAtMS: math.MinInt64,
				Review:      GuardianApprovalReview{Status: GuardianApprovalReviewStatusInProgress},
				Action:      mustGuardianApprovalReviewAction(t, validAction),
			},
			canonical: `{"threadId":"","turnId":"","startedAtMs":-9223372036854775808,` +
				`"reviewId":"","targetItemId":null,` +
				`"review":{"status":"inProgress","riskLevel":null,"userAuthorization":null,"rationale":null},` +
				`"action":{"type":"command","source":"shell","command":"","cwd":"/workspace"}}`,
		},
		{
			name: "explicit null target and unknown fields",
			input: `{"unknown":{"nested":true},"action":` + validAction + `,"review":` + validReview +
				`,"targetItemId":null,"reviewId":"review-1","startedAtMs":0,"turnId":"turn-1","threadId":"thread-1"}`,
			want: ItemGuardianApprovalReviewStartedNotification{
				ThreadID: "thread-1", TurnID: "turn-1", ReviewID: "review-1",
				Review: GuardianApprovalReview{Status: GuardianApprovalReviewStatusInProgress},
				Action: mustGuardianApprovalReviewAction(t, validAction),
			},
			canonical: `{"threadId":"thread-1","turnId":"turn-1","startedAtMs":0,` +
				`"reviewId":"review-1","targetItemId":null,` +
				`"review":{"status":"inProgress","riskLevel":null,"userAuthorization":null,"rationale":null},` +
				`"action":{"type":"command","source":"shell","command":"","cwd":"/workspace"}}`,
		},
		{
			name: "maximum timestamp and target",
			input: `{"threadId":"thread-2","turnId":"turn-2","startedAtMs":9223372036854775807,` +
				`"reviewId":"review-2","targetItemId":"item-2",` +
				`"review":{"status":"denied","riskLevel":"critical","userAuthorization":"unknown","rationale":"why"},` +
				`"action":{"type":"networkAccess","target":"","host":"","protocol":"https","port":65535}}`,
			want: ItemGuardianApprovalReviewStartedNotification{
				ThreadID: "thread-2", TurnID: "turn-2", StartedAtMS: math.MaxInt64,
				ReviewID: "review-2", TargetItemID: ptr("item-2"),
				Review: GuardianApprovalReview{
					Status:            GuardianApprovalReviewStatusDenied,
					RiskLevel:         ptr(GuardianRiskLevelCritical),
					UserAuthorization: ptr(GuardianUserAuthorizationUnknown), Rationale: ptr("why"),
				},
				Action: mustGuardianApprovalReviewAction(t,
					`{"type":"networkAccess","target":"","host":"","protocol":"https","port":65535}`),
			},
			canonical: `{"threadId":"thread-2","turnId":"turn-2","startedAtMs":9223372036854775807,` +
				`"reviewId":"review-2","targetItemId":"item-2",` +
				`"review":{"status":"denied","riskLevel":"critical","userAuthorization":"unknown","rationale":"why"},` +
				`"action":{"type":"networkAccess","target":"","host":"","protocol":"https","port":65535}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got ItemGuardianApprovalReviewStartedNotification
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

func TestGuardianApprovalReviewStartedNotificationRejectsMalformedWireForms(t *testing.T) {
	valid := `{"threadId":"thread","turnId":"turn","startedAtMs":1,"reviewId":"review",` +
		`"review":{"status":"approved"},` +
		`"action":{"type":"applyPatch","cwd":"/workspace","files":[]}}`
	for _, input := range []string{
		``, `null`, `[]`, `1`, `true`, `"value"`, `{`, `{}`,
		`{"turnId":"turn","startedAtMs":1,"reviewId":"review","review":{"status":"approved"},"action":{"type":"applyPatch","cwd":"/workspace","files":[]}}`,
		`{"threadId":"thread","startedAtMs":1,"reviewId":"review","review":{"status":"approved"},"action":{"type":"applyPatch","cwd":"/workspace","files":[]}}`,
		`{"threadId":"thread","turnId":"turn","reviewId":"review","review":{"status":"approved"},"action":{"type":"applyPatch","cwd":"/workspace","files":[]}}`,
		`{"threadId":"thread","turnId":"turn","startedAtMs":1,"review":{"status":"approved"},"action":{"type":"applyPatch","cwd":"/workspace","files":[]}}`,
		`{"threadId":"thread","turnId":"turn","startedAtMs":1,"reviewId":"review","action":{"type":"applyPatch","cwd":"/workspace","files":[]}}`,
		`{"threadId":"thread","turnId":"turn","startedAtMs":1,"reviewId":"review","review":{"status":"approved"}}`,
		strings.Replace(valid, `"threadId":"thread"`, `"threadId":null`, 1),
		strings.Replace(valid, `"turnId":"turn"`, `"turnId":null`, 1),
		strings.Replace(valid, `"startedAtMs":1`, `"startedAtMs":null`, 1),
		strings.Replace(valid, `"startedAtMs":1`, `"startedAtMs":1.5`, 1),
		strings.Replace(valid, `"startedAtMs":1`, `"startedAtMs":"1"`, 1),
		strings.Replace(valid, `"startedAtMs":1`, `"startedAtMs":9223372036854775808`, 1),
		strings.Replace(valid, `"startedAtMs":1`, `"startedAtMs":-9223372036854775809`, 1),
		strings.Replace(valid, `"reviewId":"review"`, `"reviewId":null`, 1),
		strings.Replace(valid, `"review":{"status":"approved"}`, `"review":null`, 1),
		strings.Replace(valid, `"review":{"status":"approved"}`, `"review":{"status":"other"}`, 1),
		strings.Replace(valid, `"action":{"type":"applyPatch","cwd":"/workspace","files":[]}`, `"action":null`, 1),
		strings.Replace(valid, `"action":{"type":"applyPatch","cwd":"/workspace","files":[]}`, `"action":{"type":"other"}`, 1),
		strings.Replace(valid, `"reviewId":"review"`, `"targetItemId":1,"reviewId":"review"`, 1),
		strings.Replace(valid, `"threadId":"thread"`, `"threadId":"thread","threadId":"other"`, 1),
		strings.Replace(valid, `"reviewId":"review"`, `"targetItemId":null,"targetItemId":"item","reviewId":"review"`, 1),
		valid + ` {}`,
		valid + ` x`,
	} {
		assertJSONRejects[ItemGuardianApprovalReviewStartedNotification](t, input)
	}
}

func TestGuardianApprovalReviewStartedNotificationNilReceiverAndInvalidValuesFailClosed(t *testing.T) {
	var notification *ItemGuardianApprovalReviewStartedNotification
	if err := notification.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil started-notification receiver succeeded")
	}
	validAction := mustGuardianApprovalReviewAction(t,
		`{"type":"command","source":"shell","command":"","cwd":"/workspace"}`)
	for _, notification := range []ItemGuardianApprovalReviewStartedNotification{
		{},
		{Review: GuardianApprovalReview{Status: GuardianApprovalReviewStatus("other")}, Action: validAction},
	} {
		if _, err := json.Marshal(notification); err == nil {
			t.Fatalf("invalid notification marshaled: %#v", notification)
		}
	}
}

func TestGuardianApprovalReviewStartedNotificationRemainsStandalone(t *testing.T) {
	const typeName = "ItemGuardianApprovalReviewStartedNotification"
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, typeName) || slices.Contains(binding.Result, typeName) {
			t.Fatalf("started notification unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == typeName {
			t.Fatalf("started notification unexpectedly bound to item %s", binding.Kind)
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
		if method.Method == "item/autoApprovalReview/started" && method.State != MethodBlocked {
			t.Fatalf("started method state = %s, want blocked", method.State)
		}
	}
}

func TestGuardianApprovalReviewStartedNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type ItemGuardianApprovalReviewStartedNotification = {
  "action": GuardianApprovalReviewAction;
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

func mustGuardianApprovalReviewAction(t *testing.T, input string) GuardianApprovalReviewAction {
	t.Helper()
	var action GuardianApprovalReviewAction
	if err := json.Unmarshal([]byte(input), &action); err != nil {
		t.Fatalf("decode action fixture: %v", err)
	}
	return action
}

var (
	_ json.Marshaler   = ItemGuardianApprovalReviewStartedNotification{}
	_ json.Unmarshaler = (*ItemGuardianApprovalReviewStartedNotification)(nil)
)

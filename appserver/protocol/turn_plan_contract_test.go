package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTurnPlanSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	status, ok := defs["TurnPlanStepStatus"].(Schema)
	if !ok {
		t.Fatal("$defs missing TurnPlanStepStatus")
	}
	wantStatus := Schema{"type": "string", "enum": []any{"pending", "inProgress", "completed"}}
	if !reflect.DeepEqual(status, wantStatus) {
		t.Fatalf("TurnPlanStepStatus = %#v, want %#v", status, wantStatus)
	}

	step, ok := defs["TurnPlanStep"].(Schema)
	if !ok {
		t.Fatal("$defs missing TurnPlanStep")
	}
	wantStep := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": Schema{
			"status": Schema{"$ref": "#/$defs/TurnPlanStepStatus"},
			"step":   Schema{"type": "string"},
		},
		"required": []string{"step", "status"},
	}
	if !reflect.DeepEqual(step, wantStep) {
		t.Fatalf("TurnPlanStep = %#v, want %#v", step, wantStep)
	}

	notification, ok := defs["TurnPlanUpdatedNotification"].(Schema)
	if !ok {
		t.Fatal("$defs missing TurnPlanUpdatedNotification")
	}
	if notification["type"] != "object" || notification["additionalProperties"] != false {
		t.Fatalf("TurnPlanUpdatedNotification is not a closed object: %#v", notification)
	}
	if got := schemaRequiredNames(notification); !slices.Equal(got, []string{"threadId", "turnId", "explanation", "plan"}) {
		t.Fatalf("TurnPlanUpdatedNotification required = %v", got)
	}
	wantProperties := Schema{
		"threadId":    Schema{"type": "string"},
		"turnId":      Schema{"type": "string"},
		"explanation": Schema{"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}}},
		"plan":        Schema{"type": "array", "items": Schema{"$ref": "#/$defs/TurnPlanStep"}},
	}
	if got := notification["properties"].(Schema); !reflect.DeepEqual(got, wantProperties) {
		t.Fatalf("TurnPlanUpdatedNotification properties = %#v, want %#v", got, wantProperties)
	}
}

func TestTurnPlanStepStatusWireContract(t *testing.T) {
	for _, value := range []TurnPlanStepStatus{
		TurnPlanStepStatusPending,
		TurnPlanStepStatusInProgress,
		TurnPlanStepStatusCompleted,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Errorf("Marshal(%q): %v", value, err)
			continue
		}
		var decoded TurnPlanStepStatus
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != value {
			t.Errorf("round trip %q = %q, %v", value, decoded, err)
		}
	}
	for _, input := range []string{`null`, `""`, `"in_progress"`, `"running"`, `"Completed"`, `1`, `{}`, `[]`} {
		var status TurnPlanStepStatus
		if err := json.Unmarshal([]byte(input), &status); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	if _, err := json.Marshal(TurnPlanStepStatus("unknown")); err == nil {
		t.Fatal("invalid status marshaled")
	}
	var nilStatus *TurnPlanStepStatus
	if err := nilStatus.UnmarshalJSON([]byte(`"pending"`)); err == nil {
		t.Fatal("nil TurnPlanStepStatus receiver succeeded")
	}
}

func TestTurnPlanStepWireContract(t *testing.T) {
	valid := []string{
		`{"step":"","status":"pending"}`,
		`{"step":"Implement","status":"inProgress"}`,
		`{"step":"Verify","status":"completed"}`,
	}
	for _, input := range valid {
		var step TurnPlanStep
		if err := json.Unmarshal([]byte(input), &step); err != nil {
			t.Errorf("Unmarshal(%s): %v", input, err)
			continue
		}
		encoded, err := json.Marshal(step)
		if err != nil || string(encoded) != input {
			t.Errorf("round trip = %s, %v; want %s", encoded, err, input)
		}
	}
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `{}`,
		`{"status":"pending"}`, `{"step":"x"}`,
		`{"step":null,"status":"pending"}`, `{"step":"x","status":null}`,
		`{"step":1,"status":"pending"}`, `{"step":"x","status":"running"}`,
		`{"step":"x","status":"pending","id":"extra"}`,
		`{"step":"x","status":"pending"} {}`,
	} {
		var step TurnPlanStep
		if err := json.Unmarshal([]byte(input), &step); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	if _, err := json.Marshal(TurnPlanStep{Step: "x"}); err == nil {
		t.Fatal("step with invalid status marshaled")
	}
	var nilStep *TurnPlanStep
	if err := nilStep.UnmarshalJSON([]byte(`{"step":"x","status":"pending"}`)); err == nil {
		t.Fatal("nil TurnPlanStep receiver succeeded")
	}
}

func TestTurnPlanUpdatedNotificationAcceptsCanonicalAndCompatibleForms(t *testing.T) {
	valid := []struct {
		input string
		want  string
	}{
		{
			input: `{"threadId":"","turnId":"","plan":[]}`,
			want:  `{"threadId":"","turnId":"","explanation":null,"plan":[]}`,
		},
		{
			input: `{"threadId":"thread","turnId":"turn","explanation":null,"plan":[]}`,
			want:  `{"threadId":"thread","turnId":"turn","explanation":null,"plan":[]}`,
		},
		{
			input: `{"threadId":"thread","turnId":"turn","explanation":"Ship safely","plan":[` +
				`{"step":"Inspect","status":"pending"},` +
				`{"step":"Implement","status":"inProgress"},` +
				`{"step":"Verify","status":"completed"}]}`,
			want: `{"threadId":"thread","turnId":"turn","explanation":"Ship safely","plan":[` +
				`{"step":"Inspect","status":"pending"},` +
				`{"step":"Implement","status":"inProgress"},` +
				`{"step":"Verify","status":"completed"}]}`,
		},
	}
	for _, tc := range valid {
		var notification TurnPlanUpdatedNotification
		if err := json.Unmarshal([]byte(tc.input), &notification); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(notification)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}
}

func TestTurnPlanUpdatedNotificationRejectsMalformedForms(t *testing.T) {
	invalid := []string{
		`null`, `[]`, `"value"`, `1`, `{}`,
		`{"turnId":"turn","explanation":null,"plan":[]}`,
		`{"threadId":"thread","explanation":null,"plan":[]}`,
		`{"threadId":"thread","turnId":"turn","explanation":null}`,
		`{"threadId":null,"turnId":"turn","explanation":null,"plan":[]}`,
		`{"threadId":"thread","turnId":null,"explanation":null,"plan":[]}`,
		`{"threadId":"thread","turnId":"turn","explanation":1,"plan":[]}`,
		`{"threadId":"thread","turnId":"turn","explanation":null,"plan":null}`,
		`{"threadId":"thread","turnId":"turn","explanation":null,"plan":[null]}`,
		`{"threadId":"thread","turnId":"turn","explanation":null,"plan":[{"step":"x"}]}`,
		`{"threadId":"thread","turnId":"turn","explanation":null,"plan":[],"steps":[]}`,
		`{"threadId":"thread","turnId":"turn","explanation":null,"plan":[]} {}`,
	}
	for _, input := range invalid {
		var notification TurnPlanUpdatedNotification
		if err := json.Unmarshal([]byte(input), &notification); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	if _, err := json.Marshal(TurnPlanUpdatedNotification{ThreadID: "thread", TurnID: "turn"}); err == nil {
		t.Fatal("nil plan marshaled")
	}
	if _, err := json.Marshal(TurnPlanUpdatedNotification{
		ThreadID: "thread", TurnID: "turn", Plan: []TurnPlanStep{{Step: "x"}},
	}); err == nil {
		t.Fatal("invalid nested status marshaled")
	}
	var nilNotification *TurnPlanUpdatedNotification
	if err := nilNotification.UnmarshalJSON([]byte(`{"threadId":"thread","turnId":"turn","plan":[]}`)); err == nil {
		t.Fatal("nil TurnPlanUpdatedNotification receiver succeeded")
	}
}

func TestTurnPlanContractsRemainStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if binding.Method == "turn/plan/updated" {
			t.Fatalf("turn/plan/updated unexpectedly bound: %#v", binding)
		}
	}
	info, ok := LookupMethod("turn/plan/updated")
	if !ok || info.State != MethodBlocked {
		t.Fatalf("turn/plan/updated method = %#v, %v; want blocked", info, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestTurnPlanTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type TurnPlanStepStatus = "pending" | "inProgress" | "completed";`,
		`export type TurnPlanStep = {`,
		`"status": TurnPlanStepStatus;`,
		`"step": string;`,
		`export type TurnPlanUpdatedNotification = {`,
		`"explanation": string | null;`,
		`"plan": Array<TurnPlanStep>;`,
		`"threadId": string;`,
		`"turnId": string;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTurnStartSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnumSchema(t, defs, "ReasoningSummary", []string{"auto", "concise", "detailed", "none"})

	params, ok := defs["TurnStartParams"].(Schema)
	if !ok {
		t.Fatal("$defs missing TurnStartParams")
	}
	if params["type"] != "object" || params["additionalProperties"] != false {
		t.Fatalf("TurnStartParams is not a closed object: %#v", params)
	}
	if got := schemaRequiredNames(params); !slices.Equal(got, []string{"threadId", "input"}) {
		t.Fatalf("TurnStartParams required = %v, want [threadId input]", got)
	}
	wantParams := Schema{
		"threadId":            Schema{"type": "string"},
		"clientUserMessageId": nullableTurnStartSchema(Schema{"type": "string"}),
		"input":               Schema{"type": "array", "items": Schema{"$ref": "#/$defs/UserInput"}},
		"cwd":                 nullableTurnStartSchema(Schema{"type": "string"}),
		"approvalPolicy":      nullableTurnStartSchema(Schema{"$ref": "#/$defs/AskForApproval"}),
		"approvalsReviewer":   nullableTurnStartSchema(Schema{"$ref": "#/$defs/ApprovalsReviewer"}),
		"sandboxPolicy":       nullableTurnStartSchema(Schema{"$ref": "#/$defs/SandboxPolicy"}),
		"model":               nullableTurnStartSchema(Schema{"type": "string"}),
		"serviceTier":         nullableTurnStartSchema(Schema{"type": "string"}),
		"effort":              nullableTurnStartSchema(Schema{"$ref": "#/$defs/ReasoningEffort"}),
		"summary":             nullableTurnStartSchema(Schema{"$ref": "#/$defs/ReasoningSummary"}),
		"personality":         nullableTurnStartSchema(Schema{"$ref": "#/$defs/Personality"}),
		"outputSchema":        nullableTurnStartSchema(Schema{"$ref": "#/$defs/JsonValue"}),
	}
	if got := params["properties"].(Schema); !reflect.DeepEqual(got, wantParams) {
		t.Fatalf("TurnStartParams properties = %#v, want %#v", got, wantParams)
	}

	response, ok := defs["TurnStartResponse"].(Schema)
	if !ok {
		t.Fatal("$defs missing TurnStartResponse")
	}
	wantResponse := Schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           Schema{"turn": Schema{"$ref": "#/$defs/Turn"}},
		"required":             []string{"turn"},
	}
	if !reflect.DeepEqual(response, wantResponse) {
		t.Fatalf("TurnStartResponse = %#v, want %#v", response, wantResponse)
	}
}

func TestReasoningSummaryWireContract(t *testing.T) {
	for _, value := range []ReasoningSummary{
		ReasoningSummaryAuto,
		ReasoningSummaryConcise,
		ReasoningSummaryDetailed,
		ReasoningSummaryNone,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Errorf("Marshal(%q): %v", value, err)
			continue
		}
		var decoded ReasoningSummary
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != value {
			t.Errorf("Unmarshal(%s) = %q, %v", encoded, decoded, err)
		}
	}
	for _, input := range []string{`null`, `""`, `"summary"`, `"AUTO"`, `1`, `true`, `{}`, `[]`, `"auto" {}`} {
		var value ReasoningSummary
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	if _, err := json.Marshal(ReasoningSummary("other")); err == nil {
		t.Fatal("invalid ReasoningSummary marshaled")
	}
	var nilSummary *ReasoningSummary
	if err := nilSummary.UnmarshalJSON([]byte(`"auto"`)); err == nil {
		t.Fatal("nil ReasoningSummary receiver succeeded")
	}
}

func TestTurnStartParamsAcceptExactPositiveForms(t *testing.T) {
	valid := []string{
		`{"threadId":"","input":[]}`,
		`{"threadId":"thread","clientUserMessageId":null,"input":[],"cwd":null,` +
			`"approvalPolicy":null,"approvalsReviewer":null,"sandboxPolicy":null,"model":null,` +
			`"serviceTier":null,"effort":null,"summary":null,"personality":null,"outputSchema":null}`,
		`{"threadId":"thread","clientUserMessageId":"","input":[` +
			`{"type":"text","text":"","text_elements":[]},` +
			`{"type":"image","url":"image.png"}],"cwd":"relative/or/empty",` +
			`"approvalPolicy":{"granular":{"sandbox_approval":true,"rules":false,` +
			`"skill_approval":true,"request_permissions":false,"mcp_elicitations":true}},` +
			`"approvalsReviewer":"guardian_subagent","sandboxPolicy":{"type":"workspaceWrite",` +
			`"writableRoots":["/workspace"],"networkAccess":true,"excludeTmpdirEnvVar":false,` +
			`"excludeSlashTmp":true},"model":"","serviceTier":"tier","effort":"ultra",` +
			`"summary":"detailed","personality":"friendly",` +
			`"outputSchema":{"count":9007199254740993,"nested":[null,true]}}`,
	}
	for _, input := range valid {
		var params TurnStartParams
		if err := json.Unmarshal([]byte(input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil {
			t.Errorf("Marshal(%s): %v", input, err)
			continue
		}
		var roundTrip TurnStartParams
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Errorf("decode canonical %s: %v", encoded, err)
		}
		if strings.Contains(input, "9007199254740993") && !strings.Contains(string(encoded), "9007199254740993") {
			t.Errorf("precision lost: %s", encoded)
		}
	}
}

func TestTurnStartParamsRejectMalformedRequiredAndOptionalFields(t *testing.T) {
	invalid := []map[string]any{
		{},
		{"input": []any{}},
		{"threadId": nil, "input": []any{}},
		{"threadId": 1, "input": []any{}},
		{"threadId": "thread"},
		{"threadId": "thread", "input": nil},
		{"threadId": "thread", "input": map[string]any{}},
		{"threadId": "thread", "input": []any{nil}},
		{"threadId": "thread", "input": []any{map[string]any{"type": "text"}}},
	}
	for _, value := range invalid {
		turnStartParamsRejects(t, value, "required field")
	}

	invalidOptional := map[string]any{
		"clientUserMessageId": 1,
		"cwd":                 []any{},
		"approvalPolicy":      "always",
		"approvalsReviewer":   "other",
		"sandboxPolicy":       "workspace-write",
		"model":               false,
		"serviceTier":         1,
		"effort":              "",
		"summary":             "summary",
		"personality":         "verbose",
	}
	for field, value := range invalidOptional {
		turnStartParamsRejects(t, map[string]any{"threadId": "thread", "input": []any{}, field: value}, field)
	}

	excluded := []string{
		"prompt", "message", "text", "id", "metadata", "provider", "providerId", "settings",
		"maxTokens", "temperature", "topP", "thinkingBudget", "adaptiveThinking", "reasoningEffort",
		"stopSequences", "responsesapiClientMetadata", "additionalContext", "environments",
		"runtimeWorkspaceRoots", "permissions", "collaborationMode", "multiAgentMode",
	}
	for _, field := range excluded {
		turnStartParamsRejects(t, map[string]any{"threadId": "thread", "input": []any{}, field: nil}, field)
	}

	for _, input := range []string{`null`, `[]`, `"value"`, `1`, `{}` + " {}"} {
		var params TurnStartParams
		if err := json.Unmarshal([]byte(input), &params); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
}

func TestTurnStartParamsMarshalAndNilReceiverFailClosed(t *testing.T) {
	invalidSummary := ReasoningSummary("other")
	emptyApproval := AskForApproval{}
	invalidSchema := JsonValue{}
	for name, value := range map[string]TurnStartParams{
		"nil input":        {ThreadID: "thread"},
		"invalid input":    {ThreadID: "thread", Input: []UserInput{{}}},
		"invalid approval": {ThreadID: "thread", Input: []UserInput{}, ApprovalPolicy: &emptyApproval},
		"invalid summary":  {ThreadID: "thread", Input: []UserInput{}, Summary: &invalidSummary},
		"invalid schema":   {ThreadID: "thread", Input: []UserInput{}, OutputSchema: &invalidSchema},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("%s marshaled", name)
		}
	}
	var params *TurnStartParams
	if err := params.UnmarshalJSON([]byte(`{"threadId":"thread","input":[]}`)); err == nil {
		t.Fatal("nil TurnStartParams receiver succeeded")
	}
}

func TestTurnStartResponseWireContract(t *testing.T) {
	valid := `{"turn":{"id":"","items":[],"itemsView":"notLoaded","status":"completed",` +
		`"error":null,"startedAt":null,"completedAt":null,"durationMs":null}}`
	var response TurnStartResponse
	if err := json.Unmarshal([]byte(valid), &response); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	encoded, err := json.Marshal(response)
	if err != nil || string(encoded) != valid {
		t.Fatalf("round trip = %s, %v", encoded, err)
	}
	for _, input := range []string{
		`null`, `{}`, `{"turn":null}`, `{"turn":{}}`,
		strings.TrimSuffix(valid, "}") + `,"threadId":"thread"}`,
		valid + ` {}`,
	} {
		var value TurnStartResponse
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	if _, err := json.Marshal(TurnStartResponse{}); err == nil {
		t.Fatal("zero TurnStartResponse marshaled")
	}
	var nilResponse *TurnStartResponse
	if err := nilResponse.UnmarshalJSON([]byte(valid)); err == nil {
		t.Fatal("nil TurnStartResponse receiver succeeded")
	}
}

func TestTurnStartContractsRemainStandalone(t *testing.T) {
	foundRuntimeBinding := false
	for _, binding := range WireTypeBindings() {
		if binding.Method == "turn/start" {
			foundRuntimeBinding = true
			if !slices.Equal(binding.Params, []string{"TurnRunStartParams"}) ||
				!slices.Equal(binding.Result, []string{"TurnRunStartResult"}) {
				t.Errorf("turn/start binding = %#v", binding)
			}
		}
	}
	if !foundRuntimeBinding {
		t.Error("turn/start runtime binding missing")
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestTurnStartTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ReasoningSummary = "auto" | "concise" | "detailed" | "none";`,
		`export type TurnStartParams = {`,
		`"threadId": string;`,
		`"input": Array<UserInput>;`,
		`"sandboxPolicy"?: SandboxPolicy | null;`,
		`"summary"?: ReasoningSummary | null;`,
		`"outputSchema"?: JsonValue | null;`,
		`export type TurnStartResponse = {`,
		`"turn": Turn;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func nullableTurnStartSchema(value Schema) Schema {
	return Schema{"anyOf": []any{value, Schema{"type": "null"}}}
}

func turnStartParamsRejects(t *testing.T, value map[string]any, label string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var params TurnStartParams
	if err := json.Unmarshal(encoded, &params); err == nil {
		t.Errorf("accepted %s: %s", label, encoded)
	}
}

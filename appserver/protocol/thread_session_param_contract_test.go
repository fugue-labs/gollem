package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadSessionParamSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	common := threadSessionParamCommonSchemaPropertiesForTest()

	startProperties := cloneThreadSessionParamSchemaProperties(common)
	startProperties["personality"] = nullableThreadSessionParamSchemaForTest(Schema{"$ref": "#/$defs/Personality"})
	startProperties["serviceName"] = nullableThreadSessionParamSchemaForTest(Schema{"type": "string"})
	startProperties["ephemeral"] = nullableThreadSessionParamSchemaForTest(Schema{"type": "boolean"})
	startProperties["sessionStartSource"] = nullableThreadSessionParamSchemaForTest(Schema{"$ref": "#/$defs/ThreadStartSource"})
	startProperties["threadSource"] = nullableThreadSessionParamSchemaForTest(Schema{"$ref": "#/$defs/ThreadSource"})
	assertThreadSessionParamSchema(t, defs, "ThreadStartParams", startProperties, nil)

	resumeProperties := cloneThreadSessionParamSchemaProperties(common)
	resumeProperties["threadId"] = Schema{"type": "string"}
	resumeProperties["personality"] = nullableThreadSessionParamSchemaForTest(Schema{"$ref": "#/$defs/Personality"})
	assertThreadSessionParamSchema(t, defs, "ThreadResumeParams", resumeProperties, []string{"threadId"})

	forkProperties := cloneThreadSessionParamSchemaProperties(common)
	forkProperties["threadId"] = Schema{"type": "string"}
	forkProperties["lastTurnId"] = nullableThreadSessionParamSchemaForTest(Schema{"type": "string"})
	forkProperties["ephemeral"] = Schema{"type": "boolean"}
	forkProperties["threadSource"] = nullableThreadSessionParamSchemaForTest(Schema{"$ref": "#/$defs/ThreadSource"})
	assertThreadSessionParamSchema(t, defs, "ThreadForkParams", forkProperties, []string{"threadId"})
}

func TestThreadSessionParamsAcceptExactPositiveForms(t *testing.T) {
	valid := []struct {
		name   string
		input  string
		target func() any
	}{
		{name: "empty start", input: `{}`, target: func() any { return new(ThreadStartParams) }},
		{
			name: "nullable start",
			input: `{"model":null,"modelProvider":null,"serviceTier":null,"cwd":null,` +
				`"approvalPolicy":null,"approvalsReviewer":null,"sandbox":null,"config":null,` +
				`"serviceName":null,"baseInstructions":null,"developerInstructions":null,` +
				`"personality":null,"ephemeral":null,"sessionStartSource":null,"threadSource":null}`,
			target: func() any { return new(ThreadStartParams) },
		},
		{
			name: "populated start",
			input: `{"model":"","modelProvider":"provider","serviceTier":"",` +
				`"cwd":"relative/or/empty","approvalPolicy":"never","approvalsReviewer":"user",` +
				`"sandbox":"workspace-write","config":{"null":null,"array":[1,true],"nested":{"value":"ok"}},` +
				`"serviceName":"service","baseInstructions":"base","developerInstructions":"developer",` +
				`"personality":"pragmatic","ephemeral":false,"sessionStartSource":"startup",` +
				`"threadSource":"custom/source"}`,
			target: func() any { return new(ThreadStartParams) },
		},
		{name: "minimal resume", input: `{"threadId":""}`, target: func() any { return new(ThreadResumeParams) }},
		{
			name: "nullable resume",
			input: `{"threadId":"thread","model":null,"modelProvider":null,"serviceTier":null,"cwd":null,` +
				`"approvalPolicy":null,"approvalsReviewer":null,"sandbox":null,"config":null,` +
				`"baseInstructions":null,"developerInstructions":null,"personality":null}`,
			target: func() any { return new(ThreadResumeParams) },
		},
		{
			name: "populated resume",
			input: `{"threadId":"thread","model":"model","modelProvider":"provider","serviceTier":"tier",` +
				`"cwd":"","approvalPolicy":{"granular":{"sandbox_approval":true,"rules":false,` +
				`"skill_approval":true,"request_permissions":false,"mcp_elicitations":true}},` +
				`"approvalsReviewer":"guardian_subagent","sandbox":"danger-full-access",` +
				`"config":{"count":9007199254740993},"baseInstructions":"",` +
				`"developerInstructions":"developer","personality":"friendly"}`,
			target: func() any { return new(ThreadResumeParams) },
		},
		{name: "minimal fork", input: `{"threadId":""}`, target: func() any { return new(ThreadForkParams) }},
		{
			name: "nullable fork",
			input: `{"threadId":"thread","lastTurnId":null,"model":null,"modelProvider":null,` +
				`"serviceTier":null,"cwd":null,"approvalPolicy":null,"approvalsReviewer":null,` +
				`"sandbox":null,"config":null,"baseInstructions":null,"developerInstructions":null,` +
				`"threadSource":null}`,
			target: func() any { return new(ThreadForkParams) },
		},
		{
			name: "populated fork",
			input: `{"threadId":"thread","lastTurnId":"turn","model":"model","modelProvider":"provider",` +
				`"serviceTier":"","cwd":"relative","approvalPolicy":"on-request",` +
				`"approvalsReviewer":"auto_review","sandbox":"read-only","config":{},` +
				`"baseInstructions":"base","developerInstructions":"developer","ephemeral":true,` +
				`"threadSource":""}`,
			target: func() any { return new(ThreadForkParams) },
		},
	}

	for _, testCase := range valid {
		t.Run(testCase.name, func(t *testing.T) {
			target := testCase.target()
			if err := json.Unmarshal([]byte(testCase.input), target); err != nil {
				t.Fatalf("Unmarshal(%s): %v", testCase.input, err)
			}
			encoded, err := json.Marshal(target)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if err := json.Unmarshal(encoded, testCase.target()); err != nil {
				t.Fatalf("decode canonical %s: %v", encoded, err)
			}
		})
	}
}

func TestThreadSessionParamsRejectMalformedFields(t *testing.T) {
	startInvalid := map[string]any{
		"model": 1, "modelProvider": false, "serviceTier": 1, "cwd": []any{},
		"approvalPolicy": "always", "approvalsReviewer": "other", "sandbox": "readOnly",
		"config": []any{}, "serviceName": 1, "baseInstructions": false,
		"developerInstructions": map[string]any{}, "personality": "verbose", "ephemeral": "false",
		"sessionStartSource": "resume", "threadSource": 1,
	}
	for field, value := range startInvalid {
		threadSessionParamRejectsObject(t, map[string]any{field: value}, func() any { return new(ThreadStartParams) }, "start "+field)
	}

	resumeInvalid := map[string]any{
		"model": 1, "modelProvider": false, "serviceTier": 1, "cwd": []any{},
		"approvalPolicy": "always", "approvalsReviewer": "other", "sandbox": "readOnly",
		"config": []any{}, "baseInstructions": false, "developerInstructions": map[string]any{},
		"personality": "verbose",
	}
	for field, value := range resumeInvalid {
		threadSessionParamRejectsObject(t, map[string]any{"threadId": "thread", field: value}, func() any { return new(ThreadResumeParams) }, "resume "+field)
	}

	forkInvalid := map[string]any{
		"lastTurnId": 1, "model": 1, "modelProvider": false, "serviceTier": 1, "cwd": []any{},
		"approvalPolicy": "always", "approvalsReviewer": "other", "sandbox": "readOnly",
		"config": []any{}, "baseInstructions": false, "developerInstructions": map[string]any{},
		"ephemeral": nil, "threadSource": 1,
	}
	for field, value := range forkInvalid {
		threadSessionParamRejectsObject(t, map[string]any{"threadId": "thread", field: value}, func() any { return new(ThreadForkParams) }, "fork "+field)
	}
	threadSessionParamRejectsObject(
		t,
		map[string]any{"threadId": "thread", "ephemeral": "false"},
		func() any { return new(ThreadForkParams) },
		"fork ephemeral type",
	)
}

func TestThreadSessionParamsRejectMissingRequiredCrossedAndExperimentalFields(t *testing.T) {
	for _, value := range []map[string]any{
		{}, {"threadId": nil}, {"threadId": 1}, {"threadId": false}, {"threadId": []any{}},
	} {
		threadSessionParamRejectsObject(t, value, func() any { return new(ThreadResumeParams) }, "resume threadId")
		threadSessionParamRejectsObject(t, value, func() any { return new(ThreadForkParams) }, "fork threadId")
	}

	startExcluded := []string{
		"threadId", "lastTurnId", "prompt", "input", "allowProviderModelFallback",
		"runtimeWorkspaceRoots", "permissions", "multiAgentMode", "historyMode", "environments",
		"dynamicTools", "selectedCapabilityRoots", "mockExperimentalField", "experimentalRawEvents",
	}
	for _, field := range startExcluded {
		threadSessionParamRejectsObject(t, map[string]any{field: nil}, func() any { return new(ThreadStartParams) }, "start "+field)
	}

	resumeExcluded := []string{
		"lastTurnId", "serviceName", "ephemeral", "sessionStartSource", "threadSource", "prompt", "input",
		"history", "path", "runtimeWorkspaceRoots", "permissions", "excludeTurns", "initialTurnsPage",
	}
	for _, field := range resumeExcluded {
		threadSessionParamRejectsObject(t, map[string]any{"threadId": "thread", field: nil}, func() any { return new(ThreadResumeParams) }, "resume "+field)
	}

	forkExcluded := []string{
		"serviceName", "personality", "sessionStartSource", "prompt", "input", "title", "metadata",
		"includeItems", "sourceThreadId", "path", "runtimeWorkspaceRoots", "permissions", "excludeTurns",
	}
	for _, field := range forkExcluded {
		threadSessionParamRejectsObject(t, map[string]any{"threadId": "thread", field: nil}, func() any { return new(ThreadForkParams) }, "fork "+field)
	}

	for _, input := range []string{`null`, `[]`, `"value"`, `1`, `{}` + " {}"} {
		for _, target := range []func() any{
			func() any { return new(ThreadStartParams) },
			func() any { return new(ThreadResumeParams) },
			func() any { return new(ThreadForkParams) },
		} {
			if err := json.Unmarshal([]byte(input), target()); err == nil {
				t.Errorf("%T Unmarshal(%s) succeeded", target(), input)
			}
		}
	}
}

func TestThreadSessionParamMarshalAndNilReceiversFailClosed(t *testing.T) {
	invalidPersonality := Personality("verbose")
	invalidSandbox := SandboxMode("readOnly")
	invalidReviewer := ApprovalsReviewer("other")
	emptyApproval := AskForApproval{}
	invalidConfig := map[string]JsonValue{"missing": {}}
	for name, value := range map[string]any{
		"start personality": ThreadStartParams{Personality: &invalidPersonality},
		"resume sandbox":    ThreadResumeParams{ThreadID: "thread", Sandbox: &invalidSandbox},
		"fork reviewer":     ThreadForkParams{ThreadID: "thread", ApprovalsReviewer: &invalidReviewer},
		"start approval":    ThreadStartParams{ApprovalPolicy: &emptyApproval},
		"resume config":     ThreadResumeParams{ThreadID: "thread", Config: &invalidConfig},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("%s marshaled", name)
		}
	}

	var start *ThreadStartParams
	if err := start.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ThreadStartParams receiver succeeded")
	}
	var resume *ThreadResumeParams
	if err := resume.UnmarshalJSON([]byte(`{"threadId":"thread"}`)); err == nil {
		t.Fatal("nil ThreadResumeParams receiver succeeded")
	}
	var fork *ThreadForkParams
	if err := fork.UnmarshalJSON([]byte(`{"threadId":"thread"}`)); err == nil {
		t.Fatal("nil ThreadForkParams receiver succeeded")
	}
}

func TestThreadSessionParamsRemainStandalone(t *testing.T) {
	foundRuntimeStart := false
	for _, binding := range WireTypeBindings() {
		switch binding.Method {
		case "thread/start":
			foundRuntimeStart = true
			if !slices.Equal(binding.Params, []string{"ThreadRunStartParams"}) ||
				!slices.Equal(binding.Result, []string{"ThreadRunStartResult"}) {
				t.Errorf("thread/start binding = %#v", binding)
			}
		case "thread/resume":
			t.Errorf("%s unexpectedly has a wire type binding: %#v", binding.Method, binding)
		case "thread/fork":
			if !slices.Equal(binding.Params, []string{"ThreadHistoryForkParams"}) ||
				!slices.Equal(binding.Result, []string{"ThreadHistoryForkResult"}) {
				t.Errorf("thread/fork binding = %#v", binding)
			}
		}
	}
	if !foundRuntimeStart {
		t.Error("thread/start runtime binding missing")
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestThreadSessionParamTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, name := range []string{"ThreadStartParams", "ThreadResumeParams", "ThreadForkParams"} {
		if !strings.Contains(source, "export type "+name+" = {") {
			t.Errorf("generated TypeScript missing %s", name)
		}
	}
	for _, want := range []string{
		`"approvalPolicy"?: AskForApproval | null;`,
		`"approvalsReviewer"?: ApprovalsReviewer | null;`,
		`"config"?: { [key in string]?: JsonValue } | null;`,
		`"cwd"?: string | null;`,
		`"sandbox"?: SandboxMode | null;`,
		`"threadId": string;`,
		`"ephemeral"?: boolean;`,
		`"sessionStartSource"?: ThreadStartSource | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func threadSessionParamCommonSchemaPropertiesForTest() Schema {
	configMap := Schema{
		"type": "object", "additionalProperties": Schema{"$ref": "#/$defs/JsonValue"},
		"x-gollem-typescript-optional-map": true,
	}
	return Schema{
		"model":                 nullableThreadSessionParamSchemaForTest(Schema{"type": "string"}),
		"modelProvider":         nullableThreadSessionParamSchemaForTest(Schema{"type": "string"}),
		"serviceTier":           nullableThreadSessionParamSchemaForTest(Schema{"type": "string"}),
		"cwd":                   nullableThreadSessionParamSchemaForTest(Schema{"type": "string"}),
		"approvalPolicy":        nullableThreadSessionParamSchemaForTest(Schema{"$ref": "#/$defs/AskForApproval"}),
		"approvalsReviewer":     nullableThreadSessionParamSchemaForTest(Schema{"$ref": "#/$defs/ApprovalsReviewer"}),
		"sandbox":               nullableThreadSessionParamSchemaForTest(Schema{"$ref": "#/$defs/SandboxMode"}),
		"config":                nullableThreadSessionParamSchemaForTest(configMap),
		"baseInstructions":      nullableThreadSessionParamSchemaForTest(Schema{"type": "string"}),
		"developerInstructions": nullableThreadSessionParamSchemaForTest(Schema{"type": "string"}),
	}
}

func nullableThreadSessionParamSchemaForTest(value Schema) Schema {
	return Schema{"anyOf": []any{value, Schema{"type": "null"}}}
}

func cloneThreadSessionParamSchemaProperties(properties Schema) Schema {
	clone := make(Schema, len(properties))
	for name, value := range properties {
		clone[name] = value
	}
	return clone
}

func assertThreadSessionParamSchema(t *testing.T, defs Schema, name string, properties Schema, required []string) {
	t.Helper()
	definition, ok := defs[name].(Schema)
	if !ok {
		t.Fatalf("$defs missing %s", name)
	}
	if definition["type"] != "object" || definition["additionalProperties"] != false {
		t.Errorf("%s is not a closed object: %#v", name, definition)
	}
	if got := schemaRequiredNames(definition); !slices.Equal(got, required) {
		t.Errorf("%s required = %v, want %v", name, got, required)
	}
	if got := definition["properties"].(Schema); !reflect.DeepEqual(got, properties) {
		t.Errorf("%s properties = %#v, want %#v", name, got, properties)
	}
}

func threadSessionParamRejectsObject(t *testing.T, value map[string]any, target func() any, label string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, target()); err == nil {
		t.Errorf("%T accepted %s: %s", target(), label, encoded)
	}
}

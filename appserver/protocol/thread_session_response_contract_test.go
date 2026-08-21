package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadSessionResponseSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	wantProperties := Schema{
		"thread":             Schema{"$ref": "#/$defs/Thread"},
		"model":              Schema{"type": "string"},
		"modelProvider":      Schema{"type": "string"},
		"serviceTier":        Schema{"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}}},
		"cwd":                Schema{"$ref": "#/$defs/AbsolutePathBuf"},
		"instructionSources": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/LegacyAppPathString"}},
		"approvalPolicy":     Schema{"$ref": "#/$defs/AskForApproval"},
		"approvalsReviewer":  Schema{"$ref": "#/$defs/ApprovalsReviewer"},
		"sandbox":            Schema{"$ref": "#/$defs/SandboxPolicy"},
		"reasoningEffort":    Schema{"anyOf": []any{Schema{"$ref": "#/$defs/ReasoningEffort"}, Schema{"type": "null"}}},
	}
	for _, name := range []string{"ThreadStartResponse", "ThreadResumeResponse", "ThreadForkResponse"} {
		definition, ok := defs[name].(Schema)
		if !ok {
			t.Fatalf("$defs missing %s", name)
		}
		if definition["type"] != "object" || definition["additionalProperties"] != false {
			t.Errorf("%s is not a closed object: %#v", name, definition)
		}
		if got := schemaRequiredNames(definition); !slices.Equal(got, threadSessionResponseRequiredFields) {
			t.Errorf("%s required = %v, want %v", name, got, threadSessionResponseRequiredFields)
		}
		if got := definition["properties"].(Schema); !reflect.DeepEqual(got, wantProperties) {
			t.Errorf("%s properties = %#v, want %#v", name, got, wantProperties)
		}
	}
}

func TestThreadSessionResponseWireValidation(t *testing.T) {
	valid := []string{
		threadSessionResponseWire(
			`""`, `""`, `null`, `[]`, `"never"`, `"user"`,
			`{"type":"dangerFullAccess"}`, `null`,
		),
		threadSessionResponseWire(
			`"model"`, `"provider"`, `""`, `["","relative.md"]`,
			`{"granular":{"sandbox_approval":true,"rules":false,"skill_approval":true,"request_permissions":false,"mcp_elicitations":true}}`,
			`"guardian_subagent"`,
			`{"type":"workspaceWrite","writableRoots":["/workspace"],"networkAccess":true,"excludeTmpdirEnvVar":false,"excludeSlashTmp":true}`,
			`"high"`,
		),
	}
	for _, input := range valid {
		for _, target := range threadSessionResponseTargets() {
			threadResponsePolicyRoundTrip(t, input, target())
		}
	}
}

func TestThreadSessionResponsesRejectMissingAndNullFields(t *testing.T) {
	base := threadSessionResponseObject(t)
	for _, field := range threadSessionResponseRequiredFields {
		missing := cloneThreadSessionResponseObject(base)
		delete(missing, field)
		threadSessionResponsesReject(t, missing, "missing "+field)
	}
	for _, field := range []string{
		"thread", "model", "modelProvider", "cwd", "instructionSources",
		"approvalPolicy", "approvalsReviewer", "sandbox",
	} {
		nullValue := cloneThreadSessionResponseObject(base)
		nullValue[field] = nil
		threadSessionResponsesReject(t, nullValue, "null "+field)
	}
}

func TestThreadSessionResponsesRejectMalformedAndCrossedValues(t *testing.T) {
	relativeCWD := threadSessionResponseObject(t)
	relativeCWD["cwd"] = "relative"
	threadSessionResponsesReject(t, relativeCWD, "relative response cwd")

	invalid := []string{
		`null`, `[]`, `{}`,
		threadSessionResponseWire(`1`, `"provider"`, `null`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`),
		threadSessionResponseWire(`"model"`, `false`, `null`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`),
		threadSessionResponseWire(`"model"`, `"provider"`, `1`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`),
		strings.Replace(threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`), `"cwd":"/workspace"`, `"cwd":"relative"`, 1),
		threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[null]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`),
		threadSessionResponseWire(`"model"`, `"provider"`, `null`, `{}`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`),
		threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[]`, `"always"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`),
		threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[]`, `"never"`, `"other"`, `{"type":"dangerFullAccess"}`, `null`),
		threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `""`),
		strings.Replace(threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`), publicThreadWire, `{}`, 1),
		strings.TrimSuffix(threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`), `}`) + `,"runtimeWorkspaceRoots":[]}`,
		strings.TrimSuffix(threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`), `}`) + `,"activePermissionProfile":null}`,
		strings.TrimSuffix(threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`), `}`) + `,"multiAgentMode":"explicitRequestOnly"}`,
		strings.TrimSuffix(threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`), `}`) + `,"initialTurnsPage":null}`,
		strings.TrimSuffix(threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`), `}`) + `,"turn":{}}`,
		threadSessionResponseWire(`"model"`, `"provider"`, `null`, `[]`, `"never"`, `"user"`, `{"type":"dangerFullAccess"}`, `null`) + ` {}`,
	}
	for _, input := range invalid {
		for _, target := range threadSessionResponseTargets() {
			if err := json.Unmarshal([]byte(input), target()); err == nil {
				t.Errorf("%T Unmarshal(%s) succeeded", target(), input)
			}
		}
	}
}

func TestThreadSessionResponseNilAndZeroValuesFailClosed(t *testing.T) {
	for index, value := range []any{
		ThreadStartResponse{}, ThreadResumeResponse{}, ThreadForkResponse{},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid response %d marshaled", index)
		}
	}
	var start *ThreadStartResponse
	if err := start.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ThreadStartResponse receiver succeeded")
	}
	var resume *ThreadResumeResponse
	if err := resume.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ThreadResumeResponse receiver succeeded")
	}
	var fork *ThreadForkResponse
	if err := fork.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ThreadForkResponse receiver succeeded")
	}
}

func TestThreadSessionResponsesRemainStandalone(t *testing.T) {
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

func TestThreadSessionResponseTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	wantFields := []string{
		`"approvalPolicy": AskForApproval;`,
		`"approvalsReviewer": ApprovalsReviewer;`,
		`"cwd": AbsolutePathBuf;`,
		`"instructionSources": Array<LegacyAppPathString>;`,
		`"model": string;`,
		`"modelProvider": string;`,
		`"reasoningEffort": ReasoningEffort | null;`,
		`"sandbox": SandboxPolicy;`,
		`"serviceTier": string | null;`,
		`"thread": Thread;`,
	}
	source := string(generated)
	for _, name := range []string{"ThreadStartResponse", "ThreadResumeResponse", "ThreadForkResponse"} {
		start := strings.Index(source, "export type "+name+" = {")
		if start < 0 {
			t.Errorf("generated TypeScript missing %s", name)
			continue
		}
		end := strings.Index(source[start:], "\n};")
		if end < 0 {
			t.Errorf("generated TypeScript %s is unterminated", name)
			continue
		}
		definition := source[start : start+end]
		for _, field := range wantFields {
			if !strings.Contains(definition, field) {
				t.Errorf("generated %s missing %q", name, field)
			}
		}
		for _, excluded := range []string{
			"runtimeWorkspaceRoots", "activePermissionProfile", "multiAgentMode", "initialTurnsPage",
		} {
			if strings.Contains(definition, excluded) {
				t.Errorf("generated %s unexpectedly contains %s", name, excluded)
			}
		}
	}
}

func threadSessionResponseWire(
	model, provider, serviceTier, instructionSources, approvalPolicy, reviewer, sandbox, reasoningEffort string,
) string {
	return `{"thread":` + publicThreadWire +
		`,"model":` + model +
		`,"modelProvider":` + provider +
		`,"serviceTier":` + serviceTier +
		`,"cwd":"/workspace"` +
		`,"instructionSources":` + instructionSources +
		`,"approvalPolicy":` + approvalPolicy +
		`,"approvalsReviewer":` + reviewer +
		`,"sandbox":` + sandbox +
		`,"reasoningEffort":` + reasoningEffort + `}`
}

func threadSessionResponseTargets() []func() any {
	return []func() any{
		func() any { return new(ThreadStartResponse) },
		func() any { return new(ThreadResumeResponse) },
		func() any { return new(ThreadForkResponse) },
	}
}

func threadSessionResponseObject(t *testing.T) map[string]any {
	t.Helper()
	var value map[string]any
	decoder := json.NewDecoder(strings.NewReader(threadSessionResponseWire(
		`"model"`, `"provider"`, `null`, `[]`, `"never"`, `"user"`,
		`{"type":"dangerFullAccess"}`, `null`,
	)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func cloneThreadSessionResponseObject(value map[string]any) map[string]any {
	clone := make(map[string]any, len(value))
	for name, field := range value {
		clone[name] = field
	}
	return clone
}

func threadSessionResponsesReject(t *testing.T, value map[string]any, label string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range threadSessionResponseTargets() {
		if err := json.Unmarshal(encoded, target()); err == nil {
			t.Errorf("%T accepted %s", target(), label)
		}
	}
}

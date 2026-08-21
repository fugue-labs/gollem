package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadTurnDependencySchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)

	codexError := defs["CodexErrorInfo"].(Schema)
	errorVariants := codexError["oneOf"].([]any)
	if len(errorVariants) != 6 {
		t.Fatalf("CodexErrorInfo variants = %d, want 6", len(errorVariants))
	}
	assertSchemaStringEnum(t, errorVariants[0].(Schema), []string{
		"contextWindowExceeded", "sessionBudgetExceeded", "usageLimitExceeded",
		"serverOverloaded", "cyberPolicy", "internalServerError", "unauthorized",
		"badRequest", "threadRollbackFailed", "sandboxError", "other",
	})
	for _, name := range []string{
		"httpConnectionFailed", "responseStreamConnectionFailed",
		"responseStreamDisconnected", "responseTooManyFailedAttempts",
	} {
		nested := assertSingleKeyObjectVariant(t, errorVariants, name)
		assertSchemaRequiredNames(t, nested, "httpStatusCode")
		status := nested["properties"].(Schema)["httpStatusCode"].(Schema)
		parts := status["anyOf"].([]any)
		if len(parts) != 2 || !reflect.DeepEqual(parts[0], Schema{
			"type": "integer", "minimum": 0, "maximum": 65535,
		}) || !reflect.DeepEqual(parts[1], Schema{"type": "null"}) {
			t.Fatalf("%s httpStatusCode = %#v", name, status)
		}
	}
	active := assertSingleKeyObjectVariant(t, errorVariants, "activeTurnNotSteerable")
	assertSchemaRequiredNames(t, active, "turnKind")
	if !reflect.DeepEqual(active["properties"].(Schema)["turnKind"], Schema{"$ref": "#/$defs/NonSteerableTurnKind"}) {
		t.Fatalf("active turn kind = %#v", active["properties"].(Schema)["turnKind"])
	}

	turnError := defs["TurnError"].(Schema)
	if turnError["additionalProperties"] != false {
		t.Fatalf("TurnError allows extra fields: %#v", turnError)
	}
	assertSchemaRequiredNames(t, turnError, "message", "codexErrorInfo", "additionalDetails")
	turnErrorProperties := turnError["properties"].(Schema)
	if !reflect.DeepEqual(turnErrorProperties["message"], Schema{"type": "string"}) {
		t.Fatalf("TurnError message = %#v", turnErrorProperties["message"])
	}
	assertDependencyNullableSchemaRef(t, turnErrorProperties["codexErrorInfo"], "CodexErrorInfo")
	assertNullableStringSchema(t, turnErrorProperties["additionalDetails"])

	threadStatus := defs["ThreadStatus"].(Schema)
	statusVariants := threadStatus["oneOf"].([]any)
	if len(statusVariants) != 4 {
		t.Fatalf("ThreadStatus variants = %d, want 4", len(statusVariants))
	}
	for index, name := range []string{"notLoaded", "idle", "systemError"} {
		variant := statusVariants[index].(Schema)
		assertClosedObjectSchema(t, variant, "type")
		assertSchemaStringEnum(t, variant["properties"].(Schema)["type"].(Schema), []string{name})
	}
	activeStatus := statusVariants[3].(Schema)
	assertClosedObjectSchema(t, activeStatus, "type", "activeFlags")
	activeProperties := activeStatus["properties"].(Schema)
	assertSchemaStringEnum(t, activeProperties["type"].(Schema), []string{"active"})
	if !reflect.DeepEqual(activeProperties["activeFlags"], Schema{
		"type": "array", "items": Schema{"$ref": "#/$defs/ThreadActiveFlag"},
	}) {
		t.Fatalf("active flags = %#v", activeProperties["activeFlags"])
	}

	subAgent := defs["SubAgentSource"].(Schema)
	subAgentVariants := subAgent["oneOf"].([]any)
	if len(subAgentVariants) != 3 {
		t.Fatalf("SubAgentSource variants = %d, want 3", len(subAgentVariants))
	}
	assertSchemaStringEnum(t, subAgentVariants[0].(Schema), []string{"review", "compact", "memory_consolidation"})
	spawn := assertSingleKeyObjectVariant(t, subAgentVariants, "thread_spawn")
	assertClosedObjectSchema(t, spawn, "parent_thread_id", "depth", "agent_path", "agent_nickname", "agent_role")
	spawnProperties := spawn["properties"].(Schema)
	if !reflect.DeepEqual(spawnProperties["parent_thread_id"], Schema{"$ref": "#/$defs/ThreadId"}) {
		t.Fatalf("spawn parent = %#v", spawnProperties["parent_thread_id"])
	}
	if !reflect.DeepEqual(spawnProperties["depth"], Schema{
		"type": "integer", "minimum": -2147483648, "maximum": 2147483647,
	}) {
		t.Fatalf("spawn depth = %#v", spawnProperties["depth"])
	}
	assertDependencyNullableSchemaRef(t, spawnProperties["agent_path"], "AgentPath")
	assertNullableStringSchema(t, spawnProperties["agent_nickname"])
	assertNullableStringSchema(t, spawnProperties["agent_role"])
	other := assertSingleKeyObjectVariant(t, subAgentVariants, "other")
	if !reflect.DeepEqual(other, Schema{"type": "string"}) {
		t.Fatalf("other source = %#v", other)
	}

	session := defs["SessionSource"].(Schema)
	sessionVariants := session["oneOf"].([]any)
	if len(sessionVariants) != 3 {
		t.Fatalf("SessionSource variants = %d, want 3", len(sessionVariants))
	}
	assertSchemaStringEnum(t, sessionVariants[0].(Schema), []string{"cli", "vscode", "exec", "appServer", "unknown"})
	custom := assertSingleKeyObjectVariant(t, sessionVariants, "custom")
	if !reflect.DeepEqual(custom, Schema{"type": "string"}) {
		t.Fatalf("custom session source = %#v", custom)
	}
	subAgentSession := assertSingleKeyObjectVariant(t, sessionVariants, "subAgent")
	if !reflect.DeepEqual(subAgentSession, Schema{"$ref": "#/$defs/SubAgentSource"}) {
		t.Fatalf("subAgent session source = %#v", subAgentSession)
	}
}

func TestCodexErrorInfoWireValidation(t *testing.T) {
	valid := []string{
		`"contextWindowExceeded"`, `"sessionBudgetExceeded"`, `"usageLimitExceeded"`,
		`"serverOverloaded"`, `"cyberPolicy"`, `"internalServerError"`,
		`"unauthorized"`, `"badRequest"`, `"threadRollbackFailed"`,
		`"sandboxError"`, `"other"`,
		`{"httpConnectionFailed":{"httpStatusCode":null}}`,
		`{"httpConnectionFailed":{"httpStatusCode":0}}`,
		`{"responseStreamConnectionFailed":{"httpStatusCode":65535}}`,
		`{"responseStreamDisconnected":{"httpStatusCode":502}}`,
		`{"responseTooManyFailedAttempts":{"httpStatusCode":429}}`,
		`{"activeTurnNotSteerable":{"turnKind":"review"}}`,
		`{"activeTurnNotSteerable":{"turnKind":"compact"}}`,
	}
	for _, input := range valid {
		assertJSONRoundTrip[CodexErrorInfo](t, input, input)
	}

	invalid := []string{
		`null`, `1`, `true`, `[]`, `{}`, `""`, `"unknown"`, `"ContextWindowExceeded"`,
		`{"httpConnectionFailed":null}`,
		`{"httpConnectionFailed":{}}`,
		`{"httpConnectionFailed":{"httpStatusCode":-1}}`,
		`{"httpConnectionFailed":{"httpStatusCode":65536}}`,
		`{"httpConnectionFailed":{"httpStatusCode":1.5}}`,
		`{"httpConnectionFailed":{"httpStatusCode":"500"}}`,
		`{"httpConnectionFailed":{"httpStatusCode":500,"extra":true}}`,
		`{"responseStreamConnectionFailed":{}}`,
		`{"responseStreamDisconnected":{"httpStatusCode":null},"other":true}`,
		`{"activeTurnNotSteerable":{}}`,
		`{"activeTurnNotSteerable":{"turnKind":null}}`,
		`{"activeTurnNotSteerable":{"turnKind":"other"}}`,
		`{"activeTurnNotSteerable":{"turnKind":"review","extra":true}}`,
		`{"other":"crossed"}`,
	}
	assertDependencyJSONRejects[CodexErrorInfo](t, invalid...)
	if _, err := json.Marshal(CodexErrorInfo{}); err == nil {
		t.Fatal("zero CodexErrorInfo marshal succeeded")
	}
	var info *CodexErrorInfo
	if err := info.UnmarshalJSON([]byte(`"other"`)); err == nil {
		t.Fatal("nil CodexErrorInfo receiver succeeded")
	}
}

func TestTurnErrorWireValidation(t *testing.T) {
	valid := []string{
		`{"message":"","codexErrorInfo":null,"additionalDetails":null}`,
		`{"message":"failed","codexErrorInfo":"sandboxError","additionalDetails":"details"}`,
		`{"message":"busy","codexErrorInfo":{"httpConnectionFailed":{"httpStatusCode":503}},"additionalDetails":""}`,
	}
	for _, input := range valid {
		assertJSONRoundTrip[TurnError](t, input, input)
	}
	assertDependencyJSONRejects[TurnError](t,
		`null`, `[]`, `{}`,
		`{"codexErrorInfo":null,"additionalDetails":null}`,
		`{"message":"failed","additionalDetails":null}`,
		`{"message":"failed","codexErrorInfo":null}`,
		`{"message":null,"codexErrorInfo":null,"additionalDetails":null}`,
		`{"message":"failed","codexErrorInfo":"unknown","additionalDetails":null}`,
		`{"message":"failed","codexErrorInfo":null,"additionalDetails":1}`,
		`{"message":"failed","codexErrorInfo":null,"additionalDetails":null,"extra":true}`,
	)
	var turnError *TurnError
	if err := turnError.UnmarshalJSON([]byte(valid[0])); err == nil {
		t.Fatal("nil TurnError receiver succeeded")
	}
}

func TestThreadStatusWireValidation(t *testing.T) {
	valid := []string{
		`{"type":"notLoaded"}`,
		`{"type":"idle"}`,
		`{"type":"systemError"}`,
		`{"type":"active","activeFlags":[]}`,
		`{"type":"active","activeFlags":["waitingOnApproval","waitingOnUserInput"]}`,
	}
	for _, input := range valid {
		assertJSONRoundTrip[ThreadStatus](t, input, input)
	}
	assertDependencyJSONRejects[ThreadStatus](t,
		`null`, `[]`, `{}`, `{"type":null}`, `{"type":"unknown"}`,
		`{"type":"idle","activeFlags":[]}`,
		`{"type":"active"}`,
		`{"type":"active","activeFlags":null}`,
		`{"type":"active","activeFlags":{}}`,
		`{"type":"active","activeFlags":[null]}`,
		`{"type":"active","activeFlags":["unknown"]}`,
		`{"type":"active","activeFlags":[],"extra":true}`,
	)
	if _, err := json.Marshal(ThreadStatus{}); err == nil {
		t.Fatal("zero ThreadStatus marshal succeeded")
	}
	var status *ThreadStatus
	if err := status.UnmarshalJSON([]byte(valid[0])); err == nil {
		t.Fatal("nil ThreadStatus receiver succeeded")
	}
}

func TestSubAgentSourceWireValidation(t *testing.T) {
	valid := []string{
		`"review"`, `"compact"`, `"memory_consolidation"`,
		`{"other":""}`, `{"other":"custom"}`,
		`{"thread_spawn":{"parent_thread_id":"","depth":-2147483648,"agent_path":null,"agent_nickname":null,"agent_role":null}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","depth":2147483647,"agent_path":"agent/path","agent_nickname":"helper","agent_role":"reviewer"}}`,
	}
	for _, input := range valid {
		assertJSONRoundTrip[SubAgentSource](t, input, input)
	}
	assertDependencyJSONRejects[SubAgentSource](t,
		`null`, `1`, `[]`, `{}`, `""`, `"unknown"`, `"memoryConsolidation"`,
		`{"other":null}`, `{"other":1}`, `{"other":"custom","extra":true}`,
		`{"thread_spawn":null}`,
		`{"thread_spawn":{"depth":0,"agent_path":null,"agent_nickname":null,"agent_role":null}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","agent_path":null,"agent_nickname":null,"agent_role":null}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","depth":0,"agent_nickname":null,"agent_role":null}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","depth":0,"agent_path":null,"agent_role":null}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","depth":0,"agent_path":null,"agent_nickname":null}}`,
		`{"thread_spawn":{"parent_thread_id":null,"depth":0,"agent_path":null,"agent_nickname":null,"agent_role":null}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","depth":null,"agent_path":null,"agent_nickname":null,"agent_role":null}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","depth":2147483648,"agent_path":null,"agent_nickname":null,"agent_role":null}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","depth":-2147483649,"agent_path":null,"agent_nickname":null,"agent_role":null}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","depth":1.5,"agent_path":null,"agent_nickname":null,"agent_role":null}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","depth":0,"agent_path":null,"agent_nickname":null,"agent_type":"reviewer"}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","depth":0,"agent_path":null,"agent_nickname":null,"agent_role":null,"extra":true}}`,
		`{"thread_spawn":{"parent_thread_id":"thread-1","depth":0,"agent_path":null,"agent_nickname":null,"agent_role":null},"other":"crossed"}`,
	)
	if _, err := json.Marshal(SubAgentSource{}); err == nil {
		t.Fatal("zero SubAgentSource marshal succeeded")
	}
	var source *SubAgentSource
	if err := source.UnmarshalJSON([]byte(`"review"`)); err == nil {
		t.Fatal("nil SubAgentSource receiver succeeded")
	}
}

func TestSessionSourceWireValidation(t *testing.T) {
	valid := []string{
		`"cli"`, `"vscode"`, `"exec"`, `"appServer"`, `"unknown"`,
		`{"custom":""}`, `{"custom":"desktop"}`,
		`{"subAgent":"review"}`,
		`{"subAgent":{"other":"custom"}}`,
		`{"subAgent":{"thread_spawn":{"parent_thread_id":"thread-1","depth":0,"agent_path":null,"agent_nickname":null,"agent_role":null}}}`,
	}
	for _, input := range valid {
		assertJSONRoundTrip[SessionSource](t, input, input)
	}
	assertDependencyJSONRejects[SessionSource](t,
		`null`, `1`, `[]`, `{}`, `""`, `"mcp"`, `"appserver"`,
		`{"custom":null}`, `{"custom":1}`, `{"custom":"desktop","extra":true}`,
		`{"subAgent":null}`, `{"subAgent":"unknown"}`, `{"subAgent":{}}`,
		`{"custom":"desktop","subAgent":"review"}`,
	)
	if _, err := json.Marshal(SessionSource{}); err == nil {
		t.Fatal("zero SessionSource marshal succeeded")
	}
	var source *SessionSource
	if err := source.UnmarshalJSON([]byte(`"cli"`)); err == nil {
		t.Fatal("nil SessionSource receiver succeeded")
	}
}

func TestThreadTurnDependencyMalformedStringSyntaxFailsClosed(t *testing.T) {
	validators := []struct {
		name     string
		validate func([]byte) (json.RawMessage, error)
	}{
		{name: "Codex error info", validate: validateCodexErrorInfoJSON},
		{name: "subagent source", validate: validateSubAgentSourceJSON},
		{name: "session source", validate: validateSessionSourceJSON},
	}
	for _, validator := range validators {
		if _, err := validator.validate([]byte(`"unterminated`)); err == nil {
			t.Errorf("%s accepted malformed string syntax", validator.name)
		}
	}
}

func TestThreadTurnDependenciesRemainStandalone(t *testing.T) {
	if reflect.TypeFor[ThreadStatus]() == reflect.TypeFor[ThreadLifecycleStatus]() ||
		reflect.TypeFor[SessionSource]() == reflect.TypeFor[ThreadSourceKind]() {
		t.Fatal("public dependency type aliases an incompatible Gollem type")
	}
	names := []string{"CodexErrorInfo", "TurnError", "ThreadStatus", "SubAgentSource", "SessionSource"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound by %#v", name, binding)
			}
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if slices.Contains(names, binding.Type) {
			t.Fatalf("%s unexpectedly bound by %#v", binding.Type, binding)
		}
	}
	if len(JSONSchema()["$defs"].(Schema)) != 675 {
		t.Fatalf("definition count = %d, want 675", len(JSONSchema()["$defs"].(Schema)))
	}
	if len(WireTypeBindings()) != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", len(WireTypeBindings()), len(ItemPayloadBindings()))
	}

	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type CodexErrorInfo =",
		`"contextWindowExceeded"`,
		`"httpConnectionFailed": {`,
		`"httpStatusCode": number | null;`,
		`"activeTurnNotSteerable": {`,
		`"turnKind": NonSteerableTurnKind;`,
		"export type TurnError = {",
		`"codexErrorInfo": CodexErrorInfo | null;`,
		`"additionalDetails": string | null;`,
		"export type ThreadStatus =",
		`"activeFlags": Array<ThreadActiveFlag>;`,
		"export type SubAgentSource =",
		`"thread_spawn": {`,
		`"parent_thread_id": ThreadId;`,
		`"agent_path": AgentPath | null;`,
		"export type SessionSource =",
		`"subAgent": SubAgentSource;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertJSONRoundTrip[T any](t *testing.T, input, want string) {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Errorf("Unmarshal %T from %s: %v", value, input, err)
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Errorf("round trip %T from %s = %s, %v; want %s", value, input, encoded, err, want)
	}
}

func assertDependencyJSONRejects[T any](t *testing.T, inputs ...string) {
	t.Helper()
	for _, input := range inputs {
		var value T
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("Unmarshal %T from %s succeeded", value, input)
		}
	}
}

func assertSchemaStringEnum(t *testing.T, schema Schema, want []string) {
	t.Helper()
	values := schema["enum"].([]any)
	got := make([]string, len(values))
	for index, value := range values {
		got[index] = value.(string)
	}
	if schema["type"] != "string" || !slices.Equal(got, want) {
		t.Fatalf("string enum = %#v, want %v", schema, want)
	}
}

func assertSingleKeyObjectVariant(t *testing.T, variants []any, key string) Schema {
	t.Helper()
	for _, raw := range variants[1:] {
		variant := raw.(Schema)
		properties, _ := variant["properties"].(Schema)
		nested, ok := properties[key].(Schema)
		if !ok {
			continue
		}
		assertClosedObjectSchema(t, variant, key)
		return nested
	}
	t.Fatalf("missing object variant %s", key)
	return nil
}

func assertClosedObjectSchema(t *testing.T, schema Schema, required ...string) {
	t.Helper()
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("object is not closed: %#v", schema)
	}
	assertSchemaRequiredNames(t, schema, required...)
}

func assertDependencyNullableSchemaRef(t *testing.T, raw any, name string) {
	t.Helper()
	schema := raw.(Schema)
	variants := schema["anyOf"].([]any)
	if len(variants) != 2 || !reflect.DeepEqual(variants[0], Schema{"$ref": "#/$defs/" + name}) ||
		!reflect.DeepEqual(variants[1], Schema{"type": "null"}) {
		t.Fatalf("nullable %s ref = %#v", name, schema)
	}
}

package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const publicThreadWire = `{"id":"","sessionId":"","forkedFromId":null,"parentThreadId":null,"preview":"","ephemeral":false,"modelProvider":"","createdAt":-9223372036854775808,"updatedAt":9223372036854775807,"recencyAt":null,"status":{"type":"idle"},"path":null,"cwd":"/workspace","cliVersion":"","source":"cli","threadSource":null,"agentNickname":null,"agentRole":null,"gitInfo":null,"name":null,"turns":[]}`

func TestPublicThreadSchemaIsExact(t *testing.T) {
	definition, ok := JSONSchema()["$defs"].(Schema)["Thread"].(Schema)
	if !ok {
		t.Fatal("$defs missing Thread")
	}
	if definition["type"] != "object" || definition["additionalProperties"] != false {
		t.Fatalf("Thread schema is not a closed object: %#v", definition)
	}
	wantRequired := []string{
		"id", "sessionId", "forkedFromId", "parentThreadId", "preview", "ephemeral",
		"modelProvider", "createdAt", "updatedAt", "recencyAt", "status", "path", "cwd",
		"cliVersion", "source", "threadSource", "agentNickname", "agentRole", "gitInfo", "name", "turns",
	}
	if got := schemaRequiredNames(definition); !slices.Equal(got, wantRequired) {
		t.Fatalf("Thread required = %v, want %v", got, wantRequired)
	}
	properties := definition["properties"].(Schema)
	wantProperties := Schema{
		"id":             Schema{"type": "string"},
		"sessionId":      Schema{"type": "string"},
		"forkedFromId":   nullableStringSchema(),
		"parentThreadId": nullableStringSchema(),
		"preview":        Schema{"type": "string"},
		"ephemeral":      Schema{"type": "boolean"},
		"modelProvider":  Schema{"type": "string"},
		"createdAt":      Schema{"type": "integer"},
		"updatedAt":      Schema{"type": "integer"},
		"recencyAt":      nullableIntegerSchema(),
		"status":         Schema{"$ref": "#/$defs/ThreadStatus"},
		"path":           nullableStringSchema(),
		"cwd":            Schema{"$ref": "#/$defs/AbsolutePathBuf"},
		"cliVersion":     Schema{"type": "string"},
		"source":         Schema{"$ref": "#/$defs/SessionSource"},
		"threadSource":   nullableSchemaRef("ThreadSource"),
		"agentNickname":  nullableStringSchema(),
		"agentRole":      nullableStringSchema(),
		"gitInfo":        nullableSchemaRef("GitInfo"),
		"name":           nullableStringSchema(),
		"turns":          Schema{"type": "array", "items": Schema{"$ref": "#/$defs/Turn"}},
	}
	if !reflect.DeepEqual(properties, wantProperties) {
		t.Fatalf("Thread properties = %#v, want %#v", properties, wantProperties)
	}
}

func TestPublicThreadWireValidation(t *testing.T) {
	valid := []string{
		publicThreadWire,
		`{"id":"not-a-uuid","sessionId":"session/tree","forkedFromId":"fork","parentThreadId":"parent","preview":"hello","ephemeral":true,"modelProvider":"openai","createdAt":-1,"updatedAt":0,"recencyAt":-2,"status":{"type":"active","activeFlags":["waitingOnApproval"]},"path":"relative/thread.jsonl","cwd":"/workspace/project","cliVersion":"1.2.3","source":{"subAgent":{"thread_spawn":{"parent_thread_id":"parent","depth":1,"agent_path":null,"agent_nickname":"nick","agent_role":"role"}}},"threadSource":"","agentNickname":"nick","agentRole":"role","gitInfo":{"sha":"abc","branch":"main","originUrl":"origin"},"name":"title","turns":[{"id":"turn-1","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}]}`,
	}
	for _, input := range valid {
		var thread Thread
		if err := json.Unmarshal([]byte(input), &thread); err != nil {
			t.Errorf("Unmarshal(%s): %v", input, err)
			continue
		}
		encoded, err := json.Marshal(thread)
		if err != nil || string(encoded) != input {
			t.Errorf("round trip %s = %s, %v", input, encoded, err)
		}
	}
}

func TestPublicThreadRequiresEveryGeneratedTypeScriptField(t *testing.T) {
	var base map[string]json.RawMessage
	if err := json.Unmarshal([]byte(publicThreadWire), &base); err != nil {
		t.Fatal(err)
	}
	for name := range base {
		copy := make(map[string]json.RawMessage, len(base)-1)
		for key, value := range base {
			if key != name {
				copy[key] = value
			}
		}
		encoded, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		var thread Thread
		if err := json.Unmarshal(encoded, &thread); err == nil {
			t.Errorf("Thread without %s succeeded", name)
		}
	}
}

func TestPublicThreadRejectsMalformedWireValues(t *testing.T) {
	invalid := []string{
		`null`,
		`[]`,
		`{}`,
		publicThreadWithReplacement(`"id":""`, `"id":null`),
		publicThreadWithReplacement(`"sessionId":""`, `"sessionId":1`),
		publicThreadWithReplacement(`"preview":""`, `"preview":null`),
		publicThreadWithReplacement(`"ephemeral":false`, `"ephemeral":"false"`),
		publicThreadWithReplacement(`"modelProvider":""`, `"modelProvider":null`),
		publicThreadWithReplacement(`"createdAt":-9223372036854775808`, `"createdAt":1.5`),
		publicThreadWithReplacement(`"createdAt":-9223372036854775808`, `"createdAt":-9223372036854775809`),
		publicThreadWithReplacement(`"updatedAt":9223372036854775807`, `"updatedAt":9223372036854775808`),
		publicThreadWithReplacement(`"recencyAt":null`, `"recencyAt":1.5`),
		publicThreadWithReplacement(`"status":{"type":"idle"}`, `"status":null`),
		publicThreadWithReplacement(`"status":{"type":"idle"}`, `"status":{"type":"active"}`),
		publicThreadWithReplacement(`"path":null`, `"path":1`),
		publicThreadWithReplacement(`"cwd":"/workspace"`, `"cwd":null`),
		publicThreadWithReplacement(`"cwd":"/workspace"`, `"cwd":"relative"`),
		publicThreadWithReplacement(`"cliVersion":""`, `"cliVersion":null`),
		publicThreadWithReplacement(`"source":"cli"`, `"source":null`),
		publicThreadWithReplacement(`"source":"cli"`, `"source":"mcp"`),
		publicThreadWithReplacement(`"threadSource":null`, `"threadSource":1`),
		publicThreadWithReplacement(`"agentNickname":null`, `"agentNickname":1`),
		publicThreadWithReplacement(`"agentRole":null`, `"agentRole":false`),
		publicThreadWithReplacement(`"gitInfo":null`, `"gitInfo":{}`),
		publicThreadWithReplacement(`"name":null`, `"name":[]`),
		publicThreadWithReplacement(`"turns":[]`, `"turns":null`),
		publicThreadWithReplacement(`"turns":[]`, `"turns":{}`),
		publicThreadWithReplacement(`"turns":[]`, `"turns":[null]`),
		publicThreadWithReplacement(`"turns":[]`, `"turns":[{"id":"turn"}]`),
		publicThreadWithExtra(`"extra":null`),
		publicThreadWithExtra(`"historyMode":"full"`),
		publicThreadWithExtra(`"workspace":"crossed"`),
		publicThreadWithExtra(`"metadata":{}`),
		publicThreadWithExtra(`"createdAtRFC3339":"crossed"`),
		publicThreadWithExtra(`"items":[]`),
	}
	assertRawJSONRejects[Thread](t, invalid)
}

func TestPublicThreadMarshalValidationAndStandaloneIdentity(t *testing.T) {
	minimum := int64(math.MinInt64)
	maximum := int64(math.MaxInt64)
	valid := Thread{
		ID: "", SessionID: "", Preview: "", Ephemeral: false, ModelProvider: "",
		CreatedAt: minimum, UpdatedAt: maximum, Status: mustThreadStatus(t, `{"type":"idle"}`),
		CWD: AbsolutePathBuf("/workspace"), CLIVersion: "", Source: mustSessionSource(t, `"cli"`),
		Turns: []Turn{},
	}
	if _, err := json.Marshal(valid); err != nil {
		t.Fatalf("marshal valid Thread: %v", err)
	}

	invalid := []Thread{
		{},
		{CWD: AbsolutePathBuf("/workspace"), Status: mustThreadStatus(t, `{"type":"idle"}`), Source: mustSessionSource(t, `"cli"`), Turns: nil},
		{CWD: AbsolutePathBuf("relative"), Status: mustThreadStatus(t, `{"type":"idle"}`), Source: mustSessionSource(t, `"cli"`), Turns: []Turn{}},
		{CWD: AbsolutePathBuf("/workspace"), Status: ThreadStatus{}, Source: mustSessionSource(t, `"cli"`), Turns: []Turn{}},
		{CWD: AbsolutePathBuf("/workspace"), Status: mustThreadStatus(t, `{"type":"idle"}`), Source: SessionSource{}, Turns: []Turn{}},
		{CWD: AbsolutePathBuf("/workspace"), Status: mustThreadStatus(t, `{"type":"idle"}`), Source: mustSessionSource(t, `"cli"`), Turns: []Turn{{}}},
	}
	for index, thread := range invalid {
		if _, err := json.Marshal(thread); err == nil {
			t.Errorf("invalid Thread %d marshaled", index)
		}
	}
	var thread *Thread
	if err := thread.UnmarshalJSON([]byte(publicThreadWire)); err == nil {
		t.Fatal("nil Thread receiver succeeded")
	}
	if reflect.TypeFor[Thread]() == reflect.TypeFor[ThreadRecord]() {
		t.Fatal("public Thread aliases durable ThreadRecord")
	}
}

func TestPublicThreadTypeScriptAndBindingsRemainStandalone(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type Thread = {
  "agentNickname": string | null;
  "agentRole": string | null;
  "cliVersion": string;
  "createdAt": number;
  "cwd": AbsolutePathBuf;
  "ephemeral": boolean;
  "forkedFromId": string | null;
  "gitInfo": GitInfo | null;
  "id": string;
  "modelProvider": string;
  "name": string | null;
  "parentThreadId": string | null;
  "path": string | null;
  "preview": string;
  "recencyAt": number | null;
  "sessionId": string;
  "source": SessionSource;
  "status": ThreadStatus;
  "threadSource": ThreadSource | null;
  "turns": Array<Turn>;
  "updatedAt": number;
};`
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing exact Thread:\n%s", generated)
	}
	if len(JSONSchema()["$defs"].(Schema)) != 675 {
		t.Fatalf("definition count = %d, want 675", len(JSONSchema()["$defs"].(Schema)))
	}
	if len(WireTypeBindings()) != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "Thread") || slices.Contains(binding.Result, "Thread") {
			t.Fatalf("Thread unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "Thread" {
			t.Fatalf("Thread unexpectedly bound to durable item %s", binding.Kind)
		}
	}
}

func publicThreadWithReplacement(old, replacement string) string {
	return strings.Replace(publicThreadWire, old, replacement, 1)
}

func publicThreadWithExtra(field string) string {
	return strings.TrimSuffix(publicThreadWire, "}") + "," + field + "}"
}

func mustThreadStatus(t *testing.T, input string) ThreadStatus {
	t.Helper()
	var value ThreadStatus
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func mustSessionSource(t *testing.T, input string) SessionSource {
	t.Helper()
	var value SessionSource
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

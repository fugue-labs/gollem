package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPublicParentLifecycleNotificationSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	tests := []struct {
		name       string
		required   []string
		properties Schema
	}{
		{
			name:       "ThreadStartedNotification",
			required:   []string{"thread"},
			properties: Schema{"thread": Schema{"$ref": "#/$defs/Thread"}},
		},
		{
			name:     "ThreadStatusChangedNotification",
			required: []string{"threadId", "status"},
			properties: Schema{
				"threadId": Schema{"type": "string"},
				"status":   Schema{"$ref": "#/$defs/ThreadStatus"},
			},
		},
		{
			name:     "TurnStartedNotification",
			required: []string{"threadId", "turn"},
			properties: Schema{
				"threadId": Schema{"type": "string"},
				"turn":     Schema{"$ref": "#/$defs/Turn"},
			},
		},
		{
			name:     "TurnCompletedNotification",
			required: []string{"threadId", "turn"},
			properties: Schema{
				"threadId": Schema{"type": "string"},
				"turn":     Schema{"$ref": "#/$defs/Turn"},
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			definition, ok := defs[testCase.name].(Schema)
			if !ok {
				t.Fatalf("$defs missing %s", testCase.name)
			}
			if definition["type"] != "object" || definition["additionalProperties"] != false {
				t.Fatalf("%s is not a closed object: %#v", testCase.name, definition)
			}
			if got := schemaRequiredNames(definition); !slices.Equal(got, testCase.required) {
				t.Fatalf("%s required = %v, want %v", testCase.name, got, testCase.required)
			}
			if got := definition["properties"].(Schema); !reflect.DeepEqual(got, testCase.properties) {
				t.Fatalf("%s properties = %#v, want %#v", testCase.name, got, testCase.properties)
			}
		})
	}
}

func TestPublicParentLifecycleNotificationWireValidation(t *testing.T) {
	turn := `{"id":"turn-1","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`
	valid := []struct {
		input string
		value any
	}{
		{`{"thread":` + publicThreadWire + `}`, new(ThreadStartedNotification)},
		{`{"threadId":"","status":{"type":"idle"}}`, new(ThreadStatusChangedNotification)},
		{`{"threadId":"","turn":` + turn + `}`, new(TurnStartedNotification)},
		{`{"threadId":"thread-1","turn":` + turn + `}`, new(TurnCompletedNotification)},
	}
	for _, testCase := range valid {
		if err := json.Unmarshal([]byte(testCase.input), testCase.value); err != nil {
			t.Errorf("Unmarshal(%s): %v", testCase.input, err)
			continue
		}
		encoded, err := json.Marshal(testCase.value)
		if err != nil || string(encoded) != testCase.input {
			t.Errorf("round trip %s = %s, %v", testCase.input, encoded, err)
		}
	}
}

func TestPublicParentLifecycleNotificationsRejectMalformedWireValues(t *testing.T) {
	tests := []struct {
		name    string
		decode  func([]byte) error
		invalid []string
	}{
		{
			name:   "thread started",
			decode: func(data []byte) error { var value ThreadStartedNotification; return json.Unmarshal(data, &value) },
			invalid: []string{
				`null`, `[]`, `{}`, `{"thread":null}`, `{"thread":{}}`,
				`{"thread":` + publicThreadWire + `,"threadId":"crossed"}`,
				`{"thread":` + publicThreadWire + `,"at":"crossed"}`,
			},
		},
		{
			name: "thread status changed",
			decode: func(data []byte) error {
				var value ThreadStatusChangedNotification
				return json.Unmarshal(data, &value)
			},
			invalid: []string{
				`null`, `[]`, `{}`, `{"status":{"type":"idle"}}`, `{"threadId":""}`,
				`{"threadId":null,"status":{"type":"idle"}}`,
				`{"threadId":"","status":null}`,
				`{"threadId":"","status":{"type":"active"}}`,
				`{"threadId":"","status":{"type":"idle"},"at":"crossed"}`,
				`{"threadId":"","status":{"type":"idle"},"thread":{}}`,
			},
		},
		{
			name:    "turn started",
			decode:  func(data []byte) error { var value TurnStartedNotification; return json.Unmarshal(data, &value) },
			invalid: publicTurnLifecycleNotificationInvalid(),
		},
		{
			name:    "turn completed",
			decode:  func(data []byte) error { var value TurnCompletedNotification; return json.Unmarshal(data, &value) },
			invalid: publicTurnLifecycleNotificationInvalid(),
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			for _, input := range testCase.invalid {
				if err := testCase.decode([]byte(input)); err == nil {
					t.Errorf("Unmarshal(%s) succeeded", input)
				}
			}
		})
	}
}

func TestPublicParentLifecycleNotificationNilReceiversAndMarshalValidation(t *testing.T) {
	validThread := mustPublicThread(t)
	validTurn := mustPublicTurn(t)
	validStatus := mustThreadStatus(t, `{"type":"idle"}`)
	for index, value := range []any{
		ThreadStartedNotification{Thread: validThread},
		ThreadStatusChangedNotification{Status: validStatus},
		TurnStartedNotification{Turn: validTurn},
		TurnCompletedNotification{Turn: validTurn},
	} {
		if _, err := json.Marshal(value); err != nil {
			t.Errorf("valid notification %d: %v", index, err)
		}
	}
	for index, value := range []any{
		ThreadStartedNotification{},
		ThreadStatusChangedNotification{},
		TurnStartedNotification{},
		TurnCompletedNotification{},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid notification %d marshaled", index)
		}
	}
	var threadStarted *ThreadStartedNotification
	if err := threadStarted.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ThreadStartedNotification receiver succeeded")
	}
	var statusChanged *ThreadStatusChangedNotification
	if err := statusChanged.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ThreadStatusChangedNotification receiver succeeded")
	}
	var turnStarted *TurnStartedNotification
	if err := turnStarted.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil TurnStartedNotification receiver succeeded")
	}
	var turnCompleted *TurnCompletedNotification
	if err := turnCompleted.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil TurnCompletedNotification receiver succeeded")
	}
}

func TestPublicParentLifecycleNotificationTypeScriptAndBindingsRemainStandalone(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type ThreadStartedNotification = {\n  \"thread\": Thread;\n};",
		"export type ThreadStatusChangedNotification = {\n  \"status\": ThreadStatus;\n  \"threadId\": string;\n};",
		"export type TurnStartedNotification = {\n  \"threadId\": string;\n  \"turn\": Turn;\n};",
		"export type TurnCompletedNotification = {\n  \"threadId\": string;\n  \"turn\": Turn;\n};",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
	if len(JSONSchema()["$defs"].(Schema)) != 675 {
		t.Fatalf("definition count = %d, want 675", len(JSONSchema()["$defs"].(Schema)))
	}
	if len(WireTypeBindings()) != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
	names := []string{
		"ThreadStartedNotification", "ThreadStatusChangedNotification",
		"TurnStartedNotification", "TurnCompletedNotification",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
}

func publicTurnLifecycleNotificationInvalid() []string {
	return []string{
		`null`, `[]`, `{}`, `{"turn":{}}`, `{"threadId":""}`,
		`{"threadId":null,"turn":{"id":"turn"}}`,
		`{"threadId":"","turn":null}`,
		`{"threadId":"","turn":{"id":"turn"}}`,
		`{"threadId":"","turn":{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null},"turnId":"crossed"}`,
		`{"threadId":"","turn":{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null},"at":"crossed"}`,
	}
}

func mustPublicThread(t *testing.T) Thread {
	t.Helper()
	var value Thread
	if err := json.Unmarshal([]byte(publicThreadWire), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func mustPublicTurn(t *testing.T) Turn {
	t.Helper()
	var value Turn
	if err := json.Unmarshal([]byte(`{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

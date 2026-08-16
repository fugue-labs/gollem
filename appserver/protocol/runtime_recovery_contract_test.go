package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTurnRunRetryContractIsGeneratedAndBound(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	params := defs["TurnRunRetryParams"].(Schema)
	assertClosedObjectSchema(t, params, "turnId", "idempotencyKey")
	assertRecoverySchemaKeys(t, params,
		"turnId",
		"idempotencyKey",
		"prompt",
		"metadata",
		"providerId",
		"provider",
		"model",
		"maxTokens",
		"temperature",
		"topP",
		"thinkingBudget",
		"adaptiveThinking",
		"reasoningEffort",
		"reasoningSummary",
		"promptCacheEnabled",
		"stopSequences",
		"settings",
		"input",
	)
	result := defs["TurnRunRetryResult"].(Schema)
	assertClosedObjectSchema(t, result, "turn", "sourceTurnId", "idempotencyKey", "reused")
	assertRecoverySchemaKeys(t, result,
		"turn",
		"sourceTurnId",
		"idempotencyKey",
		"reused",
	)
	turn := defs["TurnRecord"].(Schema)
	turnProperties := turn["properties"].(Schema)
	for _, key := range []string{"retryOfTurnId", "retryIdempotencyKey"} {
		if _, ok := turnProperties[key]; !ok {
			t.Fatalf("TurnRecord schema is missing %q: %#v", key, turnProperties)
		}
	}

	var binding WireTypeBinding
	for _, candidate := range WireTypeBindings() {
		if candidate.Method == "turn/retry" {
			binding = candidate
			break
		}
	}
	if binding.Method == "" {
		t.Fatal("turn/retry binding is missing")
	}
	if !reflect.DeepEqual(binding.Params, []string{"TurnRunRetryParams"}) ||
		!reflect.DeepEqual(binding.Result, []string{"TurnRunRetryResult"}) {
		t.Fatalf("turn/retry binding = %#v", binding)
	}

	valid := TurnRunRetryParams{
		TurnID:         "turn-1",
		IdempotencyKey: "retry-1",
		Prompt:         "try again",
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != `{"turnId":"turn-1","idempotencyKey":"retry-1","prompt":"try again"}` {
		t.Fatalf("encoded params = %s", encoded)
	}

	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	ts := string(generated)
	for _, want := range []string{
		`export type TurnRunRetryParams = {`,
		`"idempotencyKey": string;`,
		`"turnId": string;`,
		`export type TurnRunRetryResult = {`,
		`"reused": boolean;`,
		`"sourceTurnId": string;`,
		`"retryIdempotencyKey"?: string;`,
		`"retryOfTurnId"?: string;`,
		`"turn/retry": TurnRunRetryParams;`,
	} {
		if !strings.Contains(ts, want) {
			t.Fatalf("generated TypeScript missing %q", want)
		}
	}
}

func assertRecoverySchemaKeys(t *testing.T, schema Schema, keys ...string) {
	t.Helper()
	properties := schema["properties"].(Schema)
	if len(properties) != len(keys) {
		t.Fatalf("schema properties = %#v, want %v", properties, keys)
	}
	for _, key := range keys {
		if _, ok := properties[key]; !ok {
			t.Fatalf("schema is missing property %q: %#v", key, properties)
		}
	}
}

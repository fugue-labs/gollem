package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPublicTurnSchemaIsExact(t *testing.T) {
	definition, ok := JSONSchema()["$defs"].(Schema)["Turn"].(Schema)
	if !ok {
		t.Fatal("$defs missing Turn")
	}
	if definition["type"] != "object" || definition["additionalProperties"] != false {
		t.Fatalf("Turn schema is not a closed object: %#v", definition)
	}
	wantRequired := []string{
		"id", "items", "itemsView", "status", "error", "startedAt", "completedAt", "durationMs",
	}
	if got := schemaRequiredNames(definition); !slices.Equal(got, wantRequired) {
		t.Fatalf("Turn required = %v, want %v", got, wantRequired)
	}
	properties := definition["properties"].(Schema)
	wantProperties := Schema{
		"id":          Schema{"type": "string"},
		"items":       Schema{"type": "array", "items": Schema{"$ref": "#/$defs/ThreadItem"}},
		"itemsView":   Schema{"$ref": "#/$defs/TurnItemsView"},
		"status":      Schema{"$ref": "#/$defs/TurnStatus"},
		"error":       nullableSchemaRef("TurnError"),
		"startedAt":   nullableIntegerSchema(),
		"completedAt": nullableIntegerSchema(),
		"durationMs":  nullableIntegerSchema(),
	}
	if !reflect.DeepEqual(properties, wantProperties) {
		t.Fatalf("Turn properties = %#v, want %#v", properties, wantProperties)
	}
}

func TestPublicTurnWireValidation(t *testing.T) {
	valid := []string{
		`{"id":"","items":[],"itemsView":"notLoaded","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"not-a-uuid","items":[{"type":"contextCompaction","id":"item-1"}],"itemsView":"summary","status":"inProgress","error":null,"startedAt":-1,"completedAt":0,"durationMs":-2}`,
		`{"id":"turn-1","items":[],"itemsView":"full","status":"failed","error":{"message":"failed","codexErrorInfo":"sandboxError","additionalDetails":"details"},"startedAt":-9223372036854775808,"completedAt":9223372036854775807,"durationMs":0}`,
		`{"id":"turn-2","items":[],"itemsView":"full","status":"interrupted","error":{"message":"","codexErrorInfo":null,"additionalDetails":null},"startedAt":null,"completedAt":null,"durationMs":null}`,
	}
	for _, input := range valid {
		var turn Turn
		if err := json.Unmarshal([]byte(input), &turn); err != nil {
			t.Errorf("Unmarshal(%s): %v", input, err)
			continue
		}
		encoded, err := json.Marshal(turn)
		if err != nil || string(encoded) != input {
			t.Errorf("round trip %s = %s, %v", input, encoded, err)
		}
	}
}

func TestPublicTurnRejectsMalformedWireValues(t *testing.T) {
	invalid := []string{
		`null`,
		`[]`,
		`{}`,
		`{"items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"completed","startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null}`,
		`{"id":null,"items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":null,"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":{},"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[null],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[{"type":"contextCompaction"}],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":null,"status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"other","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","status":null,"error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"running","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"failed","error":{},"startedAt":null,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":1.5,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":9223372036854775808,"completedAt":null,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":-9223372036854775809,"durationMs":null}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":1.5}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null,"threadId":"crossed"}`,
		`{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null,"createdAt":"crossed"}`,
	}
	assertRawJSONRejects[Turn](t, invalid)
}

func TestPublicTurnMarshalValidationAndStandaloneIdentity(t *testing.T) {
	var item ThreadItem
	if err := json.Unmarshal([]byte(`{"type":"contextCompaction","id":"item-1"}`), &item); err != nil {
		t.Fatal(err)
	}
	minimum := int64(math.MinInt64)
	valid := Turn{
		ID: "", Items: []ThreadItem{item}, ItemsView: TurnItemsViewFull,
		Status: TurnStatusCompleted, StartedAt: &minimum,
	}
	if _, err := json.Marshal(valid); err != nil {
		t.Fatalf("marshal valid Turn: %v", err)
	}

	invalid := []Turn{
		{},
		{ID: "turn", Items: nil, ItemsView: TurnItemsViewFull, Status: TurnStatusCompleted},
		{ID: "turn", Items: []ThreadItem{}, ItemsView: TurnItemsView("other"), Status: TurnStatusCompleted},
		{ID: "turn", Items: []ThreadItem{}, ItemsView: TurnItemsViewFull, Status: TurnStatus("running")},
		{ID: "turn", Items: []ThreadItem{{}}, ItemsView: TurnItemsViewFull, Status: TurnStatusCompleted},
		{ID: "turn", Items: []ThreadItem{}, ItemsView: TurnItemsViewFull, Status: TurnStatusFailed, Error: &TurnError{CodexErrorInfo: &CodexErrorInfo{}}},
	}
	for index, turn := range invalid {
		if _, err := json.Marshal(turn); err == nil {
			t.Errorf("invalid Turn %d marshaled", index)
		}
	}
	var turn *Turn
	if err := turn.UnmarshalJSON([]byte(`{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`)); err == nil {
		t.Fatal("nil Turn receiver succeeded")
	}
	if reflect.TypeFor[Turn]() == reflect.TypeFor[TurnRecord]() {
		t.Fatal("public Turn aliases durable TurnRecord")
	}
}

func TestPublicTurnTypeScriptAndBindingsRemainStandalone(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type Turn = {
  "completedAt": number | null;
  "durationMs": number | null;
  "error": TurnError | null;
  "id": string;
  "items": Array<ThreadItem>;
  "itemsView": TurnItemsView;
  "startedAt": number | null;
  "status": TurnStatus;
};`
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing exact Turn:\n%s", generated)
	}
	if len(JSONSchema()["$defs"].(Schema)) != 675 {
		t.Fatalf("definition count = %d, want 675", len(JSONSchema()["$defs"].(Schema)))
	}
	if len(WireTypeBindings()) != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "Turn") || slices.Contains(binding.Result, "Turn") {
			t.Fatalf("Turn unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "Turn" {
			t.Fatalf("Turn unexpectedly bound to durable item %s", binding.Kind)
		}
	}
}

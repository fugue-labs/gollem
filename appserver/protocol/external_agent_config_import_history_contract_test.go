package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExternalAgentConfigImportHistorySchemasAreExact(t *testing.T) {
	wantHistory := closedThreadSessionParamSchema(Schema{
		"importId":      Schema{"type": "string"},
		"completedAtMs": Schema{"type": "integer", "format": "int64"},
		"successes": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/ExternalAgentConfigImportItemTypeSuccess"},
		},
		"failures": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/ExternalAgentConfigImportItemTypeFailure"},
		},
	}, []string{"importId", "completedAtMs", "successes", "failures"})
	wantResponse := closedThreadSessionParamSchema(Schema{
		"data": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/ExternalAgentConfigImportHistory"},
		},
	}, []string{"data"})

	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range map[string]Schema{
		"ExternalAgentConfigImportHistory":               wantHistory,
		"ExternalAgentConfigImportHistoriesReadResponse": wantResponse,
	} {
		got, ok := defs[name].(Schema)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s schema = %#v, %v; want %#v", name, got, ok, want)
		}
	}
}

func TestExternalAgentConfigImportHistoryAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		want      ExternalAgentConfigImportHistory
		canonical string
	}{
		{
			name:  "empty values minimum timestamp and unknown",
			input: `{"importId":"","completedAtMs":-9223372036854775808,"successes":[],"failures":[],"future":true}`,
			want: ExternalAgentConfigImportHistory{
				ImportID: "", CompletedAtMS: math.MinInt64,
				Successes: []ExternalAgentConfigImportItemTypeSuccess{},
				Failures:  []ExternalAgentConfigImportItemTypeFailure{},
			},
			canonical: `{"importId":"","completedAtMs":-9223372036854775808,"successes":[],"failures":[]}`,
		},
		{
			name: "ordered duplicate strict results and maximum timestamp",
			input: `{"importId":" import-1 ","completedAtMs":9223372036854775807,"successes":[` +
				`{"itemType":"CONFIG"},` +
				`{"itemType":"SKILLS","cwd":"repo/../repo","source":" Claude Code ","target":""},` +
				`{"itemType":"CONFIG"}` +
				`],"failures":[` +
				`{"itemType":"HOOKS","failureStage":"","message":""},` +
				`{"itemType":"SESSIONS","errorType":" IO ","failureStage":" import ","message":" failed ","cwd":"/tmp/../repo","source":""},` +
				`{"itemType":"HOOKS","failureStage":"","message":""}` +
				`]}`,
			want: ExternalAgentConfigImportHistory{
				ImportID: " import-1 ", CompletedAtMS: math.MaxInt64,
				Successes: []ExternalAgentConfigImportItemTypeSuccess{
					{ItemType: ExternalAgentConfigMigrationItemTypeConfig},
					{
						ItemType: ExternalAgentConfigMigrationItemTypeSkills,
						CWD:      stringPointer("repo/../repo"), Source: stringPointer(" Claude Code "), Target: stringPointer(""),
					},
					{ItemType: ExternalAgentConfigMigrationItemTypeConfig},
				},
				Failures: []ExternalAgentConfigImportItemTypeFailure{
					{ItemType: ExternalAgentConfigMigrationItemTypeHooks},
					{
						ItemType:  ExternalAgentConfigMigrationItemTypeSessions,
						ErrorType: stringPointer(" IO "), FailureStage: " import ", Message: " failed ",
						CWD: stringPointer("/tmp/../repo"), Source: stringPointer(""),
					},
					{ItemType: ExternalAgentConfigMigrationItemTypeHooks},
				},
			},
			canonical: `{"importId":" import-1 ","completedAtMs":9223372036854775807,"successes":[` +
				`{"itemType":"CONFIG","cwd":null,"source":null,"target":null},` +
				`{"itemType":"SKILLS","cwd":"repo/../repo","source":" Claude Code ","target":""},` +
				`{"itemType":"CONFIG","cwd":null,"source":null,"target":null}` +
				`],"failures":[` +
				`{"itemType":"HOOKS","errorType":null,"failureStage":"","message":"","cwd":null,"source":null},` +
				`{"itemType":"SESSIONS","errorType":" IO ","failureStage":" import ","message":" failed ","cwd":"/tmp/../repo","source":""},` +
				`{"itemType":"HOOKS","errorType":null,"failureStage":"","message":"","cwd":null,"source":null}` +
				`]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var history ExternalAgentConfigImportHistory
			if err := json.Unmarshal([]byte(tc.input), &history); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(history, tc.want) {
				t.Fatalf("history = %#v, want %#v", history, tc.want)
			}
			encoded, err := json.Marshal(history)
			if err != nil || string(encoded) != tc.canonical {
				t.Fatalf("canonical = %s, %v; want %s", encoded, err, tc.canonical)
			}
			var roundTrip ExternalAgentConfigImportHistory
			if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, history) {
				t.Fatalf("round trip = %#v, %v; want %#v", roundTrip, err, history)
			}
		})
	}

	encoded, err := json.Marshal(ExternalAgentConfigImportHistory{})
	if err != nil || string(encoded) != `{"importId":"","completedAtMs":0,"successes":[],"failures":[]}` {
		t.Fatalf("nil slices = %s, %v; want non-null empty arrays", encoded, err)
	}
}

func TestExternalAgentConfigImportHistoriesReadResponseAcceptsRustWireForms(t *testing.T) {
	var response ExternalAgentConfigImportHistoriesReadResponse
	input := `{"data":[` +
		`{"importId":"same","completedAtMs":-1,"successes":[],"failures":[]},` +
		`{"importId":"same","completedAtMs":0,"successes":[],"failures":[]},` +
		`{"importId":"same","completedAtMs":-1,"successes":[],"failures":[]}` +
		`],"future":{"ignored":true}}`
	if err := json.Unmarshal([]byte(input), &response); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got := len(response.Data); got != 3 {
		t.Fatalf("history count = %d, want 3", got)
	}
	if response.Data[0].CompletedAtMS != -1 || response.Data[1].CompletedAtMS != 0 ||
		!reflect.DeepEqual(response.Data[0], response.Data[2]) {
		t.Fatalf("history order/duplicates changed: %#v", response.Data)
	}
	encoded, err := json.Marshal(response)
	want := `{"data":[` +
		`{"importId":"same","completedAtMs":-1,"successes":[],"failures":[]},` +
		`{"importId":"same","completedAtMs":0,"successes":[],"failures":[]},` +
		`{"importId":"same","completedAtMs":-1,"successes":[],"failures":[]}` +
		`]}`
	if err != nil || string(encoded) != want {
		t.Fatalf("canonical = %s, %v; want %s", encoded, err, want)
	}

	empty, err := json.Marshal(ExternalAgentConfigImportHistoriesReadResponse{})
	if err != nil || string(empty) != `{"data":[]}` {
		t.Fatalf("nil data = %s, %v; want non-null empty array", empty, err)
	}
}

func TestExternalAgentConfigImportHistoryRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"completedAtMs":0,"successes":[],"failures":[]}`,
		`{"importId":null,"completedAtMs":0,"successes":[],"failures":[]}`,
		`{"importId":1,"completedAtMs":0,"successes":[],"failures":[]}`,
		`{"importId":"id","successes":[],"failures":[]}`,
		`{"importId":"id","completedAtMs":null,"successes":[],"failures":[]}`,
		`{"importId":"id","completedAtMs":"0","successes":[],"failures":[]}`,
		`{"importId":"id","completedAtMs":0.5,"successes":[],"failures":[]}`,
		`{"importId":"id","completedAtMs":1e3,"successes":[],"failures":[]}`,
		`{"importId":"id","completedAtMs":9223372036854775808,"successes":[],"failures":[]}`,
		`{"importId":"id","completedAtMs":-9223372036854775809,"successes":[],"failures":[]}`,
		`{"importId":"id","completedAtMs":0,"failures":[]}`,
		`{"importId":"id","completedAtMs":0,"successes":null,"failures":[]}`,
		`{"importId":"id","completedAtMs":0,"successes":{},"failures":[]}`,
		`{"importId":"id","completedAtMs":0,"successes":[null],"failures":[]}`,
		`{"importId":"id","completedAtMs":0,"successes":[{}],"failures":[]}`,
		`{"importId":"id","completedAtMs":0,"successes":[{"itemType":"OTHER"}],"failures":[]}`,
		`{"importId":"id","completedAtMs":0,"successes":[]}`,
		`{"importId":"id","completedAtMs":0,"successes":[],"failures":null}`,
		`{"importId":"id","completedAtMs":0,"successes":[],"failures":"failures"}`,
		`{"importId":"id","completedAtMs":0,"successes":[],"failures":[null]}`,
		`{"importId":"id","completedAtMs":0,"successes":[],"failures":[{}]}`,
		`{"importId":"id","completedAtMs":0,"successes":[],"failures":[{"itemType":"HOOKS","message":"missing stage"}]}`,
		`{"importId":"a","importId":"b","completedAtMs":0,"successes":[],"failures":[]}`,
		`{"importId":"id","completedAtMs":0,"completedAtMs":1,"successes":[],"failures":[]}`,
		`{"importId":"id","completedAtMs":0,"successes":[],"successes":[],"failures":[]}`,
		`{"importId":"id","completedAtMs":0,"successes":[],"failures":[],"failures":[]}`,
		`{"importId":"id","completedAtMs":0,"successes":[],"failures":[]`,
		`{"importId":"id","completedAtMs":0,"successes":[],"failures":[]} {}`,
	}
	for _, input := range invalid {
		var history ExternalAgentConfigImportHistory
		if err := json.Unmarshal([]byte(input), &history); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var history *ExternalAgentConfigImportHistory
	if err := history.UnmarshalJSON([]byte(`{"importId":"id","completedAtMs":0,"successes":[],"failures":[]}`)); err == nil {
		t.Fatal("nil history receiver succeeded")
	}
	if _, err := json.Marshal(ExternalAgentConfigImportHistory{
		Successes: []ExternalAgentConfigImportItemTypeSuccess{{}},
	}); err == nil {
		t.Fatal("history with invalid nested success marshaled")
	}
	if _, err := json.Marshal(ExternalAgentConfigImportHistory{
		Failures: []ExternalAgentConfigImportItemTypeFailure{{}},
	}); err == nil {
		t.Fatal("history with invalid nested failure marshaled")
	}
}

func TestExternalAgentConfigImportHistoriesReadResponseRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"data":null}`, `{"data":{}}`, `{"data":"histories"}`, `{"data":[null]}`, `{"data":[{}]}`,
		`{"data":[{"importId":"id","completedAtMs":0,"successes":[],"failures":[{}]}]}`,
		`{"data":[],"data":[]}`, `{"data":[]`, `{"data":[]} {}`,
	}
	for _, input := range invalid {
		var response ExternalAgentConfigImportHistoriesReadResponse
		if err := json.Unmarshal([]byte(input), &response); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var response *ExternalAgentConfigImportHistoriesReadResponse
	if err := response.UnmarshalJSON([]byte(`{"data":[]}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}
	if _, err := json.Marshal(ExternalAgentConfigImportHistoriesReadResponse{
		Data: []ExternalAgentConfigImportHistory{{Successes: []ExternalAgentConfigImportItemTypeSuccess{{}}}},
	}); err == nil {
		t.Fatal("response with invalid nested history marshaled")
	}
}

func TestExternalAgentConfigImportHistoryTypesRemainStandalone(t *testing.T) {
	names := []string{
		"ExternalAgentConfigImportHistory",
		"ExternalAgentConfigImportHistoriesReadResponse",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound: %#v", name, binding)
			}
		}
	}
	for _, method := range []string{
		"externalAgentConfig/import",
		"externalAgentConfig/import/readHistories",
		"externalAgentConfig/import/progress",
		"externalAgentConfig/import/completed",
	} {
		info, ok := LookupMethod(method)
		if !ok || info.State != MethodDeferredStub {
			t.Fatalf("%s = %#v, %v; want deferred stub", method, info, ok)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestExternalAgentConfigImportHistoryTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type ExternalAgentConfigImportHistory = {\n" +
			"  \"completedAtMs\": bigint;\n" +
			"  \"failures\": Array<ExternalAgentConfigImportItemTypeFailure>;\n" +
			"  \"importId\": string;\n" +
			"  \"successes\": Array<ExternalAgentConfigImportItemTypeSuccess>;\n" +
			"};",
		"export type ExternalAgentConfigImportHistoriesReadResponse = {\n" +
			"  \"data\": Array<ExternalAgentConfigImportHistory>;\n" +
			"};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}

	plainInt64, err := typeScriptType(Schema{"type": "integer", "format": "int64"}, 0)
	if err != nil || plainInt64 != "number" {
		t.Fatalf("plain int64 TypeScript = %q, %v; want number", plainInt64, err)
	}
}

var (
	_ json.Marshaler   = ExternalAgentConfigImportHistory{}
	_ json.Unmarshaler = (*ExternalAgentConfigImportHistory)(nil)
	_ json.Marshaler   = ExternalAgentConfigImportHistoriesReadResponse{}
	_ json.Unmarshaler = (*ExternalAgentConfigImportHistoriesReadResponse)(nil)
)

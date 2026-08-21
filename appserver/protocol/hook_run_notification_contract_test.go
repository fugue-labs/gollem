package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestHookRunNotificationSchemasAreExact(t *testing.T) {
	nullableString := Schema{"type": []any{"string", "null"}}
	nullableInt64 := Schema{"type": []any{"integer", "null"}, "format": "int64"}
	wantRun := closedThreadSessionParamSchema(Schema{
		"id":            Schema{"type": "string"},
		"eventName":     Schema{"$ref": "#/$defs/HookEventName"},
		"handlerType":   Schema{"$ref": "#/$defs/HookHandlerType"},
		"executionMode": Schema{"$ref": "#/$defs/HookExecutionMode"},
		"scope":         Schema{"$ref": "#/$defs/HookScope"},
		"sourcePath":    Schema{"$ref": "#/$defs/AbsolutePathBuf"},
		"source": Schema{
			"allOf":   []any{Schema{"$ref": "#/$defs/HookSource"}},
			"default": "unknown",
		},
		"displayOrder":  Schema{"type": "integer", "format": "int64"},
		"status":        Schema{"$ref": "#/$defs/HookRunStatus"},
		"statusMessage": nullableString,
		"startedAt":     Schema{"type": "integer", "format": "int64"},
		"completedAt":   nullableInt64,
		"durationMs":    nullableInt64,
		"entries": Schema{
			"type": "array", "items": Schema{"$ref": "#/$defs/HookOutputEntry"},
		},
	}, []string{
		"displayOrder", "entries", "eventName", "executionMode", "handlerType",
		"id", "scope", "sourcePath", "startedAt", "status",
	})
	wantNotification := closedThreadSessionParamSchema(Schema{
		"threadId": Schema{"type": "string"},
		"turnId":   nullableString,
		"run":      Schema{"$ref": "#/$defs/HookRunSummary"},
	}, []string{"run", "threadId"})

	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range map[string]Schema{
		"HookRunSummary":            wantRun,
		"HookStartedNotification":   wantNotification,
		"HookCompletedNotification": wantNotification,
	} {
		got, ok := defs[name].(Schema)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s schema = %#v, %v; want %#v", name, got, ok, want)
		}
	}
}

func TestHookRunSummaryAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		want      HookRunSummary
		canonical string
	}{
		{
			name: "omitted defaults nullable fields and minimum integers",
			input: `{"id":"","eventName":"preToolUse","handlerType":"command",` +
				`"executionMode":"sync","scope":"thread","sourcePath":"/hooks.json",` +
				`"displayOrder":-9223372036854775808,"status":"running",` +
				`"startedAt":-9223372036854775808,"entries":[],"future":true}`,
			want: HookRunSummary{
				ID: "", EventName: HookEventNamePreToolUse,
				HandlerType: HookHandlerTypeCommand, ExecutionMode: HookExecutionModeSync,
				Scope: HookScopeThread, SourcePath: AbsolutePathBuf("/hooks.json"),
				Source: HookSourceUnknown, DisplayOrder: math.MinInt64,
				Status: HookRunStatusRunning, StartedAt: math.MinInt64,
				Entries: []HookOutputEntry{},
			},
			canonical: `{"id":"","eventName":"preToolUse","handlerType":"command",` +
				`"executionMode":"sync","scope":"thread","sourcePath":"/hooks.json",` +
				`"source":"unknown","displayOrder":-9223372036854775808,"status":"running",` +
				`"statusMessage":null,"startedAt":-9223372036854775808,` +
				`"completedAt":null,"durationMs":null,"entries":[]}`,
		},
		{
			name: "explicit values maximum integers and duplicate entries",
			input: `{"id":" run ","eventName":"stop","handlerType":"agent",` +
				`"executionMode":"async","scope":"turn",` +
				`"sourcePath":"/workspace/../workspace/hooks.json","source":"project",` +
				`"displayOrder":9223372036854775807,"status":"completed",` +
				`"statusMessage":" done ","startedAt":9223372036854775807,` +
				`"completedAt":-9223372036854775808,"durationMs":0,` +
				`"entries":[{"kind":"warning","text":""},{"kind":"warning","text":""}]}`,
			want: HookRunSummary{
				ID: " run ", EventName: HookEventNameStop,
				HandlerType: HookHandlerTypeAgent, ExecutionMode: HookExecutionModeAsync,
				Scope: HookScopeTurn, SourcePath: AbsolutePathBuf("/workspace/hooks.json"),
				Source: HookSourceProject, DisplayOrder: math.MaxInt64,
				Status: HookRunStatusCompleted, StatusMessage: stringPointer(" done "),
				StartedAt: math.MaxInt64, CompletedAt: hookInt64Pointer(math.MinInt64),
				DurationMS: hookInt64Pointer(0),
				Entries: []HookOutputEntry{
					{Kind: HookOutputEntryKindWarning, Text: ""},
					{Kind: HookOutputEntryKindWarning, Text: ""},
				},
			},
			canonical: `{"id":" run ","eventName":"stop","handlerType":"agent",` +
				`"executionMode":"async","scope":"turn","sourcePath":"/workspace/hooks.json",` +
				`"source":"project","displayOrder":9223372036854775807,"status":"completed",` +
				`"statusMessage":" done ","startedAt":9223372036854775807,` +
				`"completedAt":-9223372036854775808,"durationMs":0,` +
				`"entries":[{"kind":"warning","text":""},{"kind":"warning","text":""}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got HookRunSummary
			if err := json.Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("summary = %#v, want %#v", got, tc.want)
			}
			encoded, err := json.Marshal(got)
			if err != nil || string(encoded) != tc.canonical {
				t.Fatalf("canonical = %s, %v; want %s", encoded, err, tc.canonical)
			}
		})
	}

	nilEntries := sampleHookRunSummaryForContract()
	nilEntries.Entries = nil
	encoded, err := json.Marshal(nilEntries)
	if err != nil || !strings.HasSuffix(string(encoded), `,"entries":[]}`) {
		t.Fatalf("nil entries canonical = %s, %v; want []", encoded, err)
	}
}

func TestHookRunSummaryRejectsMalformedWireForms(t *testing.T) {
	valid := `{"id":"run","eventName":"preToolUse","handlerType":"command",` +
		`"executionMode":"sync","scope":"thread","sourcePath":"/hooks.json",` +
		`"source":"unknown","displayOrder":0,"status":"running","statusMessage":null,` +
		`"startedAt":0,"completedAt":null,"durationMs":null,"entries":[]}`
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		strings.Replace(valid, `"id":"run",`, ``, 1),
		strings.Replace(valid, `"eventName":"preToolUse",`, ``, 1),
		strings.Replace(valid, `"handlerType":"command",`, ``, 1),
		strings.Replace(valid, `"executionMode":"sync",`, ``, 1),
		strings.Replace(valid, `"scope":"thread",`, ``, 1),
		strings.Replace(valid, `"sourcePath":"/hooks.json",`, ``, 1),
		strings.Replace(valid, `"displayOrder":0,`, ``, 1),
		strings.Replace(valid, `"status":"running",`, ``, 1),
		strings.Replace(valid, `"startedAt":0,`, ``, 1),
		strings.Replace(valid, `,"entries":[]`, ``, 1),
		strings.Replace(valid, `"id":"run"`, `"id":null`, 1),
		strings.Replace(valid, `"eventName":"preToolUse"`, `"eventName":"other"`, 1),
		strings.Replace(valid, `"handlerType":"command"`, `"handlerType":"other"`, 1),
		strings.Replace(valid, `"executionMode":"sync"`, `"executionMode":"other"`, 1),
		strings.Replace(valid, `"scope":"thread"`, `"scope":"other"`, 1),
		strings.Replace(valid, `"sourcePath":"/hooks.json"`, `"sourcePath":"relative"`, 1),
		strings.Replace(valid, `"source":"unknown"`, `"source":null`, 1),
		strings.Replace(valid, `"source":"unknown"`, `"source":"other"`, 1),
		strings.Replace(valid, `"displayOrder":0`, `"displayOrder":0.5`, 1),
		strings.Replace(valid, `"displayOrder":0`, `"displayOrder":9223372036854775808`, 1),
		strings.Replace(valid, `"status":"running"`, `"status":"other"`, 1),
		strings.Replace(valid, `"statusMessage":null`, `"statusMessage":1`, 1),
		strings.Replace(valid, `"startedAt":0`, `"startedAt":1e3`, 1),
		strings.Replace(valid, `"completedAt":null`, `"completedAt":-9223372036854775809`, 1),
		strings.Replace(valid, `"durationMs":null`, `"durationMs":"0"`, 1),
		strings.Replace(valid, `"entries":[]`, `"entries":null`, 1),
		strings.Replace(valid, `"entries":[]`, `"entries":[null]`, 1),
		strings.Replace(valid, `"entries":[]`, `"entries":[{"kind":"other","text":"x"}]`, 1),
		strings.Replace(valid, `"id":"run"`, `"id":"run","id":"other"`, 1),
		strings.TrimSuffix(valid, `}`), valid + ` {}`,
	}
	for _, input := range invalid {
		assertJSONRejects[HookRunSummary](t, input)
	}

	var summary *HookRunSummary
	if err := summary.UnmarshalJSON([]byte(valid)); err == nil {
		t.Fatal("nil HookRunSummary receiver succeeded")
	}
	for _, value := range []HookRunSummary{
		{},
		sampleHookRunSummaryForContract(),
	} {
		if value.ID != "" {
			value.SourcePath = AbsolutePathBuf("relative")
		}
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid summary %#v marshaled", value)
		}
	}
	invalidNested := sampleHookRunSummaryForContract()
	invalidNested.Entries = []HookOutputEntry{{Kind: HookOutputEntryKind("other"), Text: "x"}}
	if _, err := json.Marshal(invalidNested); err == nil {
		t.Fatal("summary with invalid nested entry marshaled")
	}
}

func TestHookNotificationsAcceptRustWireForms(t *testing.T) {
	run := sampleHookRunSummaryForContract()
	runJSON, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		input     string
		canonical string
		new       func() json.Unmarshaler
	}{
		{
			"started omitted turn",
			`{"threadId":"","run":` + string(runJSON) + `,"future":true}`,
			`{"threadId":"","turnId":null,"run":` + string(runJSON) + `}`,
			func() json.Unmarshaler { return new(HookStartedNotification) },
		},
		{
			"started null turn",
			`{"threadId":"thread","turnId":null,"run":` + string(runJSON) + `}`,
			`{"threadId":"thread","turnId":null,"run":` + string(runJSON) + `}`,
			func() json.Unmarshaler { return new(HookStartedNotification) },
		},
		{
			"completed turn",
			`{"threadId":"thread","turnId":" turn ","run":` + string(runJSON) + `}`,
			`{"threadId":"thread","turnId":" turn ","run":` + string(runJSON) + `}`,
			func() json.Unmarshaler { return new(HookCompletedNotification) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.new()
			if err := value.UnmarshalJSON([]byte(tc.input)); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != tc.canonical {
				t.Fatalf("canonical = %s, %v; want %s", encoded, err, tc.canonical)
			}
		})
	}
}

func TestHookNotificationsRejectMalformedWireForms(t *testing.T) {
	runJSON, err := json.Marshal(sampleHookRunSummaryForContract())
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"threadId":"thread","turnId":null,"run":` + string(runJSON) + `}`
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		strings.Replace(valid, `"threadId":"thread",`, ``, 1),
		strings.Replace(valid, `"threadId":"thread"`, `"threadId":null`, 1),
		strings.Replace(valid, `"turnId":null`, `"turnId":1`, 1),
		strings.Replace(valid, `,"run":`+string(runJSON), ``, 1),
		strings.Replace(valid, `"run":`+string(runJSON), `"run":null`, 1),
		strings.Replace(valid, `"run":`+string(runJSON), `"run":{}`, 1),
		strings.Replace(valid, `"threadId":"thread"`, `"threadId":"thread","threadId":"other"`, 1),
		strings.TrimSuffix(valid, `}`), valid + ` {}`,
	}
	for _, input := range invalid {
		assertJSONRejects[HookStartedNotification](t, input)
		assertJSONRejects[HookCompletedNotification](t, input)
	}
	var started *HookStartedNotification
	if err := started.UnmarshalJSON([]byte(valid)); err == nil {
		t.Fatal("nil HookStartedNotification receiver succeeded")
	}
	var completed *HookCompletedNotification
	if err := completed.UnmarshalJSON([]byte(valid)); err == nil {
		t.Fatal("nil HookCompletedNotification receiver succeeded")
	}
	if _, err := json.Marshal(HookStartedNotification{}); err == nil {
		t.Fatal("zero HookStartedNotification marshaled")
	}
	if _, err := json.Marshal(HookCompletedNotification{}); err == nil {
		t.Fatal("zero HookCompletedNotification marshaled")
	}
}

func TestHookRunNotificationsRemainStandalone(t *testing.T) {
	names := []string{"HookRunSummary", "HookStartedNotification", "HookCompletedNotification"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound: %#v", name, binding)
			}
		}
	}
	for _, method := range []string{"hook/started", "hook/completed"} {
		info, ok := LookupMethod(method)
		if !ok || info.State != MethodBlocked {
			t.Fatalf("%s = %#v, %v; want blocked", method, info, ok)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if len(Methods()) != 229 || len(WireTypeBindings()) != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("surface changed: %d methods, %d wire bindings, %d item bindings", len(Methods()), len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
}

func TestHookRunNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatal(err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type HookRunSummary = {\n  \"completedAt\": bigint | null;\n  \"displayOrder\": bigint;\n  \"durationMs\": bigint | null;\n  \"entries\": Array<HookOutputEntry>;\n  \"eventName\": HookEventName;\n  \"executionMode\": HookExecutionMode;\n  \"handlerType\": HookHandlerType;\n  \"id\": string;\n  \"scope\": HookScope;\n  \"source\": HookSource;\n  \"sourcePath\": AbsolutePathBuf;\n  \"startedAt\": bigint;\n  \"status\": HookRunStatus;\n  \"statusMessage\": string | null;\n};",
		"export type HookStartedNotification = {\n  \"run\": HookRunSummary;\n  \"threadId\": string;\n  \"turnId\": string | null;\n};",
		"export type HookCompletedNotification = {\n  \"run\": HookRunSummary;\n  \"threadId\": string;\n  \"turnId\": string | null;\n};",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func sampleHookRunSummaryForContract() HookRunSummary {
	return HookRunSummary{
		ID: "run", EventName: HookEventNamePreToolUse,
		HandlerType: HookHandlerTypeCommand, ExecutionMode: HookExecutionModeSync,
		Scope: HookScopeThread, SourcePath: AbsolutePathBuf("/hooks.json"),
		Source: HookSourceUnknown, Status: HookRunStatusRunning,
		Entries: []HookOutputEntry{},
	}
}

func hookInt64Pointer(value int64) *int64 { return &value }

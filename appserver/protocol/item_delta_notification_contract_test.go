package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestItemDeltaNotificationSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	cases := []struct {
		name       string
		indexField string
		withDelta  bool
		required   []string
	}{
		{"AgentMessageDeltaNotification", "", true, []string{"threadId", "turnId", "itemId", "delta"}},
		{"PlanDeltaNotification", "", true, []string{"threadId", "turnId", "itemId", "delta"}},
		{"ReasoningSummaryPartAddedNotification", "summaryIndex", false, []string{"threadId", "turnId", "itemId", "summaryIndex"}},
		{"ReasoningSummaryTextDeltaNotification", "summaryIndex", true, []string{"threadId", "turnId", "itemId", "delta", "summaryIndex"}},
		{"ReasoningTextDeltaNotification", "contentIndex", true, []string{"threadId", "turnId", "itemId", "delta", "contentIndex"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			definition, ok := defs[tc.name].(Schema)
			if !ok {
				t.Fatalf("$defs missing %s", tc.name)
			}
			if definition["type"] != "object" || definition["additionalProperties"] != false {
				t.Fatalf("%s is not a closed object: %#v", tc.name, definition)
			}
			if got := schemaRequiredNames(definition); !slices.Equal(got, tc.required) {
				t.Fatalf("required = %v, want %v", got, tc.required)
			}
			properties := Schema{
				"threadId": Schema{"type": "string"},
				"turnId":   Schema{"type": "string"},
				"itemId":   Schema{"type": "string"},
			}
			if tc.withDelta {
				properties["delta"] = Schema{"type": "string"}
			}
			if tc.indexField != "" {
				properties[tc.indexField] = Schema{"type": "integer"}
			}
			if got := definition["properties"].(Schema); !reflect.DeepEqual(got, properties) {
				t.Fatalf("properties = %#v, want %#v", got, properties)
			}
		})
	}
}

func TestItemDeltaNotificationsAcceptExactWireValues(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		target func() any
	}{
		{
			"agent empty strings",
			`{"threadId":"","turnId":"","itemId":"","delta":""}`,
			func() any { return new(AgentMessageDeltaNotification) },
		},
		{
			"plan strings",
			`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"step"}`,
			func() any { return new(PlanDeltaNotification) },
		},
		{
			"summary part minimum",
			`{"threadId":"thread","turnId":"turn","itemId":"item","summaryIndex":-9223372036854775808}`,
			func() any { return new(ReasoningSummaryPartAddedNotification) },
		},
		{
			"summary text maximum",
			`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"summary","summaryIndex":9223372036854775807}`,
			func() any { return new(ReasoningSummaryTextDeltaNotification) },
		},
		{
			"reasoning negative index",
			`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"reason","contentIndex":-1}`,
			func() any { return new(ReasoningTextDeltaNotification) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.target()
			if err := json.Unmarshal([]byte(tc.input), value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != tc.input {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, tc.input)
			}
		})
	}

	minimum := int64(math.MinInt64)
	maximum := int64(math.MaxInt64)
	values := []any{
		AgentMessageDeltaNotification{},
		PlanDeltaNotification{},
		ReasoningSummaryPartAddedNotification{SummaryIndex: minimum},
		ReasoningSummaryTextDeltaNotification{SummaryIndex: maximum},
		ReasoningTextDeltaNotification{ContentIndex: minimum},
	}
	for index, value := range values {
		if _, err := json.Marshal(value); err != nil {
			t.Errorf("marshal valid value %d: %v", index, err)
		}
	}
}

func TestItemDeltaNotificationsRejectMalformedWireValues(t *testing.T) {
	commonInvalid := []string{
		`null`, `[]`, `"value"`, `1`, `{}`,
		`{"turnId":"turn","itemId":"item","delta":"delta"}`,
		`{"threadId":"thread","itemId":"item","delta":"delta"}`,
		`{"threadId":"thread","turnId":"turn","delta":"delta"}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item"}`,
		`{"threadId":null,"turnId":"turn","itemId":"item","delta":"delta"}`,
		`{"threadId":"thread","turnId":1,"itemId":"item","delta":"delta"}`,
		`{"threadId":"thread","turnId":"turn","itemId":null,"delta":"delta"}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","delta":false}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"delta","index":0}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"delta"} {}`,
	}
	for _, target := range []func() any{
		func() any { return new(AgentMessageDeltaNotification) },
		func() any { return new(PlanDeltaNotification) },
	} {
		for _, input := range commonInvalid {
			if err := json.Unmarshal([]byte(input), target()); err == nil {
				t.Errorf("Unmarshal(%s) succeeded", input)
			}
		}
	}

	indexedInvalid := []struct {
		input  string
		target func() any
	}{
		{`{"threadId":"thread","turnId":"turn","itemId":"item"}`, func() any { return new(ReasoningSummaryPartAddedNotification) }},
		{`{"threadId":"thread","turnId":"turn","itemId":"item","summaryIndex":null}`, func() any { return new(ReasoningSummaryPartAddedNotification) }},
		{`{"threadId":"thread","turnId":"turn","itemId":"item","summaryIndex":1.5}`, func() any { return new(ReasoningSummaryPartAddedNotification) }},
		{`{"threadId":"thread","turnId":"turn","itemId":"item","summaryIndex":9223372036854775808}`, func() any { return new(ReasoningSummaryPartAddedNotification) }},
		{`{"threadId":"thread","turnId":"turn","itemId":"item","summaryIndex":-9223372036854775809}`, func() any { return new(ReasoningSummaryPartAddedNotification) }},
		{`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"summary"}`, func() any { return new(ReasoningSummaryTextDeltaNotification) }},
		{`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"summary","summaryIndex":"0"}`, func() any { return new(ReasoningSummaryTextDeltaNotification) }},
		{`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"reason"}`, func() any { return new(ReasoningTextDeltaNotification) }},
		{`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"reason","contentIndex":1.5}`, func() any { return new(ReasoningTextDeltaNotification) }},
		{`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"reason","contentIndex":0,"summaryIndex":0}`, func() any { return new(ReasoningTextDeltaNotification) }},
		{`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"reason","contentIndex":0} {}`, func() any { return new(ReasoningTextDeltaNotification) }},
	}
	for _, tc := range indexedInvalid {
		if err := json.Unmarshal([]byte(tc.input), tc.target()); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", tc.input)
		}
	}
}

func TestItemDeltaNotificationNilReceiversAndDistinctTypes(t *testing.T) {
	var agent *AgentMessageDeltaNotification
	var plan *PlanDeltaNotification
	var summaryPart *ReasoningSummaryPartAddedNotification
	var summaryText *ReasoningSummaryTextDeltaNotification
	var reasoningText *ReasoningTextDeltaNotification
	for name, decode := range map[string]func() error{
		"agent":          func() error { return agent.UnmarshalJSON([]byte(`{}`)) },
		"plan":           func() error { return plan.UnmarshalJSON([]byte(`{}`)) },
		"summary part":   func() error { return summaryPart.UnmarshalJSON([]byte(`{}`)) },
		"summary text":   func() error { return summaryText.UnmarshalJSON([]byte(`{}`)) },
		"reasoning text": func() error { return reasoningText.UnmarshalJSON([]byte(`{}`)) },
	} {
		if err := decode(); err == nil {
			t.Errorf("nil %s receiver succeeded", name)
		}
	}
	types := []reflect.Type{
		reflect.TypeFor[AgentMessageDeltaNotification](),
		reflect.TypeFor[PlanDeltaNotification](),
		reflect.TypeFor[ReasoningSummaryPartAddedNotification](),
		reflect.TypeFor[ReasoningSummaryTextDeltaNotification](),
		reflect.TypeFor[ReasoningTextDeltaNotification](),
	}
	for i := range types {
		for j := i + 1; j < len(types); j++ {
			if types[i] == types[j] {
				t.Fatalf("types %d and %d unexpectedly alias", i, j)
			}
		}
	}
}

func TestItemDeltaNotificationContractsRemainStandalone(t *testing.T) {
	names := []string{
		"AgentMessageDeltaNotification",
		"PlanDeltaNotification",
		"ReasoningSummaryPartAddedNotification",
		"ReasoningSummaryTextDeltaNotification",
		"ReasoningTextDeltaNotification",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	states := map[string]MethodState{
		"item/agentMessage/delta":         MethodImplemented,
		"item/plan/delta":                 MethodBlocked,
		"item/reasoning/summaryPartAdded": MethodBlocked,
		"item/reasoning/summaryTextDelta": MethodBlocked,
		"item/reasoning/textDelta":        MethodImplemented,
	}
	for method, want := range states {
		info, ok := LookupMethod(method)
		if !ok || info.State != want {
			t.Errorf("%s = %#v, %v; want %s", method, info, ok, want)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestItemDeltaNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type AgentMessageDeltaNotification = {`,
		`export type PlanDeltaNotification = {`,
		`export type ReasoningSummaryPartAddedNotification = {`,
		`"summaryIndex": number;`,
		`export type ReasoningSummaryTextDeltaNotification = {`,
		`export type ReasoningTextDeltaNotification = {`,
		`"contentIndex": number;`,
		`"delta": string;`,
		`"itemId": string;`,
		`"threadId": string;`,
		`"turnId": string;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

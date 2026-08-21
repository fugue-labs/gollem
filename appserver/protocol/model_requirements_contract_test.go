package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestModelRequirementsSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	defaults, ok := defs["NewThreadModelDefaults"].(Schema)
	if !ok {
		t.Fatal("$defs missing NewThreadModelDefaults")
	}
	assertClosedObjectSchema(t, defaults, "model", "modelReasoningEffort", "serviceTier")
	defaultProperties := defaults["properties"].(Schema)
	assertNullableStringSchema(t, defaultProperties["model"])
	assertNullableSchemaRef(t, defaultProperties["modelReasoningEffort"], "#/$defs/ReasoningEffort")
	assertNullableStringSchema(t, defaultProperties["serviceTier"])

	requirements, ok := defs["ModelsRequirements"].(Schema)
	if !ok {
		t.Fatal("$defs missing ModelsRequirements")
	}
	assertClosedObjectSchema(t, requirements, "newThread")
	assertNullableSchemaRef(t, requirements["properties"].(Schema)["newThread"], "#/$defs/NewThreadModelDefaults")
}

func TestModelRequirementsAcceptExactWireForms(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
		value func() any
	}{
		{
			"omitted defaults",
			`{}`,
			`{"model":null,"modelReasoningEffort":null,"serviceTier":null}`,
			func() any { return new(NewThreadModelDefaults) },
		},
		{
			"null defaults",
			`{"model":null,"modelReasoningEffort":null,"serviceTier":null}`,
			`{"model":null,"modelReasoningEffort":null,"serviceTier":null}`,
			func() any { return new(NewThreadModelDefaults) },
		},
		{
			"full defaults",
			`{"model":"","modelReasoningEffort":"provider-effort","serviceTier":""}`,
			`{"model":"","modelReasoningEffort":"provider-effort","serviceTier":""}`,
			func() any { return new(NewThreadModelDefaults) },
		},
		{
			"partial defaults",
			`{"model":"model"}`,
			`{"model":"model","modelReasoningEffort":null,"serviceTier":null}`,
			func() any { return new(NewThreadModelDefaults) },
		},
		{
			"omitted requirements",
			`{}`,
			`{"newThread":null}`,
			func() any { return new(ModelsRequirements) },
		},
		{
			"null requirements",
			`{"newThread":null}`,
			`{"newThread":null}`,
			func() any { return new(ModelsRequirements) },
		},
		{
			"nested omitted defaults",
			`{"newThread":{}}`,
			`{"newThread":{"model":null,"modelReasoningEffort":null,"serviceTier":null}}`,
			func() any { return new(ModelsRequirements) },
		},
		{
			"full nested defaults",
			`{"newThread":{"model":"model","modelReasoningEffort":"high","serviceTier":"fast"}}`,
			`{"newThread":{"model":"model","modelReasoningEffort":"high","serviceTier":"fast"}}`,
			func() any { return new(ModelsRequirements) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.value()
			if err := json.Unmarshal([]byte(tc.input), value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != tc.want {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, tc.want)
			}
		})
	}
}

func TestModelRequirementsRejectMalformedWireForms(t *testing.T) {
	defaultsInvalid := []string{
		`null`, `[]`, `"value"`, `1`, `true`,
		`{"model":1}`,
		`{"modelReasoningEffort":0}`,
		`{"modelReasoningEffort":""}`,
		`{"serviceTier":false}`,
		`{"model":null,"modelReasoningEffort":null,"serviceTier":null,"extra":true}`,
		`{"model":null,"modelReasoningEffort":null,"serviceTier":null} {}`,
	}
	for _, input := range defaultsInvalid {
		var value NewThreadModelDefaults
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("NewThreadModelDefaults accepted %s", input)
		}
	}

	requirementsInvalid := []string{
		`null`, `[]`, `"value"`, `1`, `true`,
		`{"newThread":false}`,
		`{"newThread":{"modelReasoningEffort":""}}`,
		`{"newThread":{"extra":true}}`,
		`{"newThread":null,"extra":true}`,
		`{"newThread":null} {}`,
	}
	for _, input := range requirementsInvalid {
		var value ModelsRequirements
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ModelsRequirements accepted %s", input)
		}
	}
}

func TestModelRequirementsNilReceiversAndInvalidMarshal(t *testing.T) {
	var defaults *NewThreadModelDefaults
	if err := defaults.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil NewThreadModelDefaults receiver succeeded")
	}
	var requirements *ModelsRequirements
	if err := requirements.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ModelsRequirements receiver succeeded")
	}

	emptyEffort := ReasoningEffort("")
	if _, err := json.Marshal(NewThreadModelDefaults{ModelReasoningEffort: &emptyEffort}); err == nil {
		t.Fatal("empty reasoning effort marshal succeeded")
	}
}

func TestModelRequirementsContractsRemainStandalone(t *testing.T) {
	names := []string{"NewThreadModelDefaults", "ModelsRequirements"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	info, ok := LookupMethod("configRequirements/read")
	if !ok || info.State != MethodImplemented {
		t.Fatalf("configRequirements/read = %#v, %v; want implemented", info, ok)
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

func TestModelRequirementsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ModelsRequirements = {`,
		`"newThread": NewThreadModelDefaults | null;`,
		`export type NewThreadModelDefaults = {`,
		`"model": string | null;`,
		`"modelReasoningEffort": ReasoningEffort | null;`,
		`"serviceTier": string | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

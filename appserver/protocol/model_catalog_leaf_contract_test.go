package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestModelCatalogLeafSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(t, defs["InputModality"], "text", "image")

	effort := defs["ReasoningEffortOption"].(Schema)
	assertClosedObjectSchema(t, effort, "reasoningEffort", "description")
	effortProperties := effort["properties"].(Schema)
	if !reflect.DeepEqual(effortProperties["reasoningEffort"], Schema{"$ref": "#/$defs/ReasoningEffort"}) {
		t.Fatalf("ReasoningEffortOption.reasoningEffort = %#v", effortProperties["reasoningEffort"])
	}
	if !reflect.DeepEqual(effortProperties["description"], Schema{"type": "string"}) {
		t.Fatalf("ReasoningEffortOption.description = %#v", effortProperties["description"])
	}

	nux := defs["ModelAvailabilityNux"].(Schema)
	assertClosedObjectSchema(t, nux, "message")
	if !reflect.DeepEqual(nux["properties"].(Schema)["message"], Schema{"type": "string"}) {
		t.Fatalf("ModelAvailabilityNux.message = %#v", nux["properties"].(Schema)["message"])
	}

	tier := defs["ModelServiceTier"].(Schema)
	assertClosedObjectSchema(t, tier, "id", "name", "description")
	for _, field := range []string{"id", "name", "description"} {
		if !reflect.DeepEqual(tier["properties"].(Schema)[field], Schema{"type": "string"}) {
			t.Errorf("ModelServiceTier.%s = %#v", field, tier["properties"].(Schema)[field])
		}
	}

	upgrade := defs["ModelUpgradeInfo"].(Schema)
	assertClosedObjectSchema(t, upgrade, "model", "upgradeCopy", "modelLink", "migrationMarkdown")
	upgradeProperties := upgrade["properties"].(Schema)
	if !reflect.DeepEqual(upgradeProperties["model"], Schema{"type": "string"}) {
		t.Fatalf("ModelUpgradeInfo.model = %#v", upgradeProperties["model"])
	}
	for _, field := range []string{"upgradeCopy", "modelLink", "migrationMarkdown"} {
		assertNullableStringSchema(t, upgradeProperties[field])
	}
}

func TestModelCatalogLeafValuesAcceptExactWireForms(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
		value func() any
	}{
		{"text modality", `"text"`, `"text"`, func() any { return new(InputModality) }},
		{"image modality", `"image"`, `"image"`, func() any { return new(InputModality) }},
		{"reasoning effort", `{"reasoningEffort":"low","description":""}`, `{"reasoningEffort":"low","description":""}`, func() any { return new(ReasoningEffortOption) }},
		{"availability nux", `{"message":""}`, `{"message":""}`, func() any { return new(ModelAvailabilityNux) }},
		{"service tier", `{"id":"","name":"","description":""}`, `{"id":"","name":"","description":""}`, func() any { return new(ModelServiceTier) }},
		{"omitted upgrade options", `{"model":""}`, `{"model":"","upgradeCopy":null,"modelLink":null,"migrationMarkdown":null}`, func() any { return new(ModelUpgradeInfo) }},
		{"full upgrade", `{"model":"next","upgradeCopy":"copy","modelLink":"https://example.test","migrationMarkdown":"# Move"}`, `{"model":"next","upgradeCopy":"copy","modelLink":"https://example.test","migrationMarkdown":"# Move"}`, func() any { return new(ModelUpgradeInfo) }},
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

func TestModelCatalogLeafValuesRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{`null`, `""`, `"audio"`, `1`, `"text" {}`} {
		var value InputModality
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("InputModality accepted %s", input)
		}
	}

	effortInvalid := []string{
		`null`, `{}`, `{"description":"desc"}`, `{"reasoningEffort":"low"}`,
		`{"reasoningEffort":null,"description":"desc"}`,
		`{"reasoningEffort":"","description":"desc"}`,
		`{"reasoningEffort":"low","description":null}`,
		`{"reasoningEffort":"low","description":"desc","extra":true}`,
		`{"reasoningEffort":"low","description":"desc"} {}`,
	}
	for _, input := range effortInvalid {
		var value ReasoningEffortOption
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ReasoningEffortOption accepted %s", input)
		}
	}

	nuxInvalid := []string{
		`null`, `{}`, `{"message":null}`, `{"message":1}`,
		`{"message":"available","extra":true}`, `{"message":"available"} {}`,
	}
	for _, input := range nuxInvalid {
		var value ModelAvailabilityNux
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ModelAvailabilityNux accepted %s", input)
		}
	}

	tierInvalid := []string{
		`null`, `{}`, `{"name":"tier","description":"desc"}`,
		`{"id":"id","description":"desc"}`, `{"id":"id","name":"tier"}`,
		`{"id":null,"name":"tier","description":"desc"}`,
		`{"id":"id","name":1,"description":"desc"}`,
		`{"id":"id","name":"tier","description":false}`,
		`{"id":"id","name":"tier","description":"desc","extra":true}`,
		`{"id":"id","name":"tier","description":"desc"} {}`,
	}
	for _, input := range tierInvalid {
		var value ModelServiceTier
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ModelServiceTier accepted %s", input)
		}
	}

	upgradeInvalid := []string{
		`null`, `{}`, `{"model":null}`, `{"model":1}`,
		`{"model":"next","upgradeCopy":1}`,
		`{"model":"next","modelLink":false}`,
		`{"model":"next","migrationMarkdown":[]}`,
		`{"model":"next","upgradeCopy":null,"modelLink":null,"migrationMarkdown":null,"extra":true}`,
		`{"model":"next","upgradeCopy":null,"modelLink":null,"migrationMarkdown":null} {}`,
	}
	for _, input := range upgradeInvalid {
		var value ModelUpgradeInfo
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ModelUpgradeInfo accepted %s", input)
		}
	}
}

func TestModelCatalogLeafNilReceiversAndInvalidMarshal(t *testing.T) {
	var modality *InputModality
	var effort *ReasoningEffortOption
	var nux *ModelAvailabilityNux
	var tier *ModelServiceTier
	var upgrade *ModelUpgradeInfo
	for name, decode := range map[string]func() error{
		"modality": func() error { return modality.UnmarshalJSON([]byte(`null`)) },
		"effort":   func() error { return effort.UnmarshalJSON([]byte(`{}`)) },
		"nux":      func() error { return nux.UnmarshalJSON([]byte(`{}`)) },
		"tier":     func() error { return tier.UnmarshalJSON([]byte(`{}`)) },
		"upgrade":  func() error { return upgrade.UnmarshalJSON([]byte(`{}`)) },
	} {
		if err := decode(); err == nil {
			t.Errorf("nil %s receiver succeeded", name)
		}
	}
	for name, value := range map[string]any{
		"modality": InputModality("audio"),
		"effort":   ReasoningEffortOption{ReasoningEffort: "", Description: "desc"},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid %s marshal succeeded", name)
		}
	}
}

func TestModelCatalogLeafContractsRemainStandalone(t *testing.T) {
	names := []string{
		"InputModality",
		"ReasoningEffortOption",
		"ModelAvailabilityNux",
		"ModelServiceTier",
		"ModelUpgradeInfo",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
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

func TestModelCatalogLeafTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type InputModality = "text" | "image";`,
		`export type ReasoningEffortOption = {`,
		`"reasoningEffort": ReasoningEffort;`,
		`"description": string;`,
		`export type ModelAvailabilityNux = {`,
		`"message": string;`,
		`export type ModelServiceTier = {`,
		`"id": string;`,
		`"name": string;`,
		`export type ModelUpgradeInfo = {`,
		`"model": string;`,
		`"upgradeCopy": string | null;`,
		`"modelLink": string | null;`,
		`"migrationMarkdown": string | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

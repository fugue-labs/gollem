package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestModelSchemaIsExact(t *testing.T) {
	model := JSONSchema()["$defs"].(Schema)["Model"].(Schema)
	assertClosedObjectSchema(t, model,
		"id", "model", "upgrade", "upgradeInfo", "availabilityNux",
		"displayName", "description", "hidden", "supportedReasoningEfforts",
		"defaultReasoningEffort", "inputModalities", "supportsPersonality",
		"additionalSpeedTiers", "serviceTiers", "defaultServiceTier", "isDefault",
	)
	properties := model["properties"].(Schema)
	for _, field := range []string{"id", "model", "displayName", "description"} {
		if !reflect.DeepEqual(properties[field], Schema{"type": "string"}) {
			t.Errorf("Model.%s = %#v", field, properties[field])
		}
	}
	for _, field := range []string{"hidden", "supportsPersonality", "isDefault"} {
		if !reflect.DeepEqual(properties[field], Schema{"type": "boolean"}) {
			t.Errorf("Model.%s = %#v", field, properties[field])
		}
	}
	assertNullableStringSchema(t, properties["upgrade"])
	assertNullableSchemaRef(t, properties["upgradeInfo"], "#/$defs/ModelUpgradeInfo")
	assertNullableSchemaRef(t, properties["availabilityNux"], "#/$defs/ModelAvailabilityNux")
	assertNullableStringSchema(t, properties["defaultServiceTier"])
	assertModelArraySchema(t, properties["supportedReasoningEfforts"], Schema{"$ref": "#/$defs/ReasoningEffortOption"})
	assertModelArraySchema(t, properties["inputModalities"], Schema{"$ref": "#/$defs/InputModality"})
	assertModelArraySchema(t, properties["additionalSpeedTiers"], Schema{"type": "string"})
	assertModelArraySchema(t, properties["serviceTiers"], Schema{"$ref": "#/$defs/ModelServiceTier"})
	if !reflect.DeepEqual(properties["defaultReasoningEffort"], Schema{"$ref": "#/$defs/ReasoningEffort"}) {
		t.Fatalf("Model.defaultReasoningEffort = %#v", properties["defaultReasoningEffort"])
	}
	if properties["additionalSpeedTiers"].(Schema)["description"] != "Deprecated: use `serviceTiers` instead." {
		t.Fatalf("Model.additionalSpeedTiers description = %#v", properties["additionalSpeedTiers"])
	}
	if properties["defaultServiceTier"].(Schema)["description"] != "Catalog default service tier id for this model, when one is configured." {
		t.Fatalf("Model.defaultServiceTier description = %#v", properties["defaultServiceTier"])
	}
}

func TestModelAcceptsExactWireFormsAndCanonicalizesDefaults(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "omitted rust options and serde defaults",
			input: `{"id":"","model":"","displayName":"","description":"","hidden":false,` +
				`"supportedReasoningEfforts":[],"defaultReasoningEffort":"low","isDefault":false}`,
			want: `{"id":"","model":"","upgrade":null,"upgradeInfo":null,"availabilityNux":null,` +
				`"displayName":"","description":"","hidden":false,"supportedReasoningEfforts":[],` +
				`"defaultReasoningEffort":"low","inputModalities":["text","image"],` +
				`"supportsPersonality":false,"additionalSpeedTiers":[],"serviceTiers":[],` +
				`"defaultServiceTier":null,"isDefault":false}`,
		},
		{
			name: "full value preserves arrays order and duplicates",
			input: `{"id":"id","model":"model","upgrade":"","upgradeInfo":{"model":"next"},` +
				`"availabilityNux":{"message":""},"displayName":"name","description":"desc","hidden":true,` +
				`"supportedReasoningEfforts":[{"reasoningEffort":"high","description":"H"},{"reasoningEffort":"high","description":"H"}],` +
				`"defaultReasoningEffort":"high","inputModalities":["image","text","image"],` +
				`"supportsPersonality":true,"additionalSpeedTiers":["fast","fast"],` +
				`"serviceTiers":[{"id":"priority","name":"Priority","description":""}],` +
				`"defaultServiceTier":"priority","isDefault":true}`,
			want: `{"id":"id","model":"model","upgrade":"","upgradeInfo":{"model":"next","upgradeCopy":null,"modelLink":null,"migrationMarkdown":null},` +
				`"availabilityNux":{"message":""},"displayName":"name","description":"desc","hidden":true,` +
				`"supportedReasoningEfforts":[{"reasoningEffort":"high","description":"H"},{"reasoningEffort":"high","description":"H"}],` +
				`"defaultReasoningEffort":"high","inputModalities":["image","text","image"],` +
				`"supportsPersonality":true,"additionalSpeedTiers":["fast","fast"],` +
				`"serviceTiers":[{"id":"priority","name":"Priority","description":""}],` +
				`"defaultServiceTier":"priority","isDefault":true}`,
		},
		{
			name: "explicit null options and empty defaulted arrays",
			input: `{"id":"id","model":"model","upgrade":null,"upgradeInfo":null,"availabilityNux":null,` +
				`"displayName":"name","description":"desc","hidden":false,"supportedReasoningEfforts":[],` +
				`"defaultReasoningEffort":"minimal","inputModalities":[],"supportsPersonality":false,` +
				`"additionalSpeedTiers":[],"serviceTiers":[],"defaultServiceTier":null,"isDefault":false}`,
			want: `{"id":"id","model":"model","upgrade":null,"upgradeInfo":null,"availabilityNux":null,` +
				`"displayName":"name","description":"desc","hidden":false,"supportedReasoningEfforts":[],` +
				`"defaultReasoningEffort":"minimal","inputModalities":[],"supportsPersonality":false,` +
				`"additionalSpeedTiers":[],"serviceTiers":[],"defaultServiceTier":null,"isDefault":false}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var value Model
			if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != tc.want {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, tc.want)
			}
		})
	}
}

func TestModelRejectsMalformedWireForms(t *testing.T) {
	mandatory := []string{
		"id", "model", "displayName", "description", "hidden",
		"supportedReasoningEfforts", "defaultReasoningEffort", "isDefault",
	}
	for _, field := range mandatory {
		t.Run("missing "+field, func(t *testing.T) {
			payload := validModelPayload()
			delete(payload, field)
			assertModelDecodeFails(t, payload)
		})
		t.Run("null "+field, func(t *testing.T) {
			payload := validModelPayload()
			payload[field] = nil
			assertModelDecodeFails(t, payload)
		})
	}
	for field, invalid := range map[string]any{
		"id":                        1,
		"model":                     false,
		"displayName":               []any{},
		"description":               map[string]any{},
		"hidden":                    "false",
		"supportedReasoningEfforts": false,
		"defaultReasoningEffort":    1,
		"isDefault":                 "true",
	} {
		t.Run("wrong primitive "+field, func(t *testing.T) {
			payload := validModelPayload()
			payload[field] = invalid
			assertModelDecodeFails(t, payload)
		})
	}

	invalidFields := map[string]any{
		"upgrade":                   1,
		"upgradeInfo":               false,
		"availabilityNux":           "message",
		"inputModalities":           nil,
		"supportsPersonality":       nil,
		"additionalSpeedTiers":      nil,
		"serviceTiers":              nil,
		"defaultServiceTier":        1,
		"supportedReasoningEfforts": []any{nil},
	}
	for field, invalid := range invalidFields {
		t.Run("invalid "+field, func(t *testing.T) {
			payload := validModelPayload()
			payload[field] = invalid
			assertModelDecodeFails(t, payload)
		})
	}

	for name, mutate := range map[string]func(map[string]any){
		"unknown modality": func(payload map[string]any) { payload["inputModalities"] = []any{"audio"} },
		"null modality":    func(payload map[string]any) { payload["inputModalities"] = []any{nil} },
		"bad effort": func(payload map[string]any) {
			payload["supportedReasoningEfforts"] = []any{map[string]any{"reasoningEffort": "low"}}
		},
		"bad tier": func(payload map[string]any) {
			payload["serviceTiers"] = []any{map[string]any{"id": "id", "name": "name"}}
		},
		"extension": func(payload map[string]any) { payload["providerId"] = "openai" },
	} {
		t.Run(name, func(t *testing.T) {
			payload := validModelPayload()
			mutate(payload)
			assertModelDecodeFails(t, payload)
		})
	}

	for _, input := range []string{`null`, `[]`, `"model"`, `{}`, `{"id":"id"} {}`} {
		var value Model
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("Model accepted %s", input)
		}
	}
}

func TestModelMarshalNormalizesArraysAndRejectsInvalidNestedValues(t *testing.T) {
	valid := Model{
		ID: "id", Model: "model", DisplayName: "name", Description: "desc",
		DefaultReasoningEffort: "low",
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("Marshal nil arrays: %v", err)
	}
	for _, fragment := range []string{
		`"supportedReasoningEfforts":[]`, `"inputModalities":[]`,
		`"additionalSpeedTiers":[]`, `"serviceTiers":[]`,
	} {
		if !strings.Contains(string(encoded), fragment) {
			t.Errorf("Marshal nil arrays = %s; missing %s", encoded, fragment)
		}
	}

	for name, value := range map[string]Model{
		"empty default effort": func() Model {
			candidate := valid
			candidate.DefaultReasoningEffort = ""
			return candidate
		}(),
		"invalid modality": func() Model {
			candidate := valid
			candidate.InputModalities = []InputModality{"audio"}
			return candidate
		}(),
		"invalid effort option": func() Model {
			candidate := valid
			candidate.SupportedReasoningEfforts = []ReasoningEffortOption{{Description: "desc"}}
			return candidate
		}(),
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid %s marshal succeeded", name)
		}
	}
}

func TestModelNilReceiverAndStandaloneContract(t *testing.T) {
	var model *Model
	if err := model.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil Model receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "Model") || slices.Contains(binding.Result, "Model") {
			t.Fatalf("Model unexpectedly bound to %s", binding.Method)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestModelTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type Model = {`,
		`"additionalSpeedTiers": Array<string>;`,
		`"availabilityNux": ModelAvailabilityNux | null;`,
		`"defaultReasoningEffort": ReasoningEffort;`,
		`"defaultServiceTier": string | null;`,
		`"inputModalities": Array<InputModality>;`,
		`"serviceTiers": Array<ModelServiceTier>;`,
		`"supportedReasoningEfforts": Array<ReasoningEffortOption>;`,
		`"upgradeInfo": ModelUpgradeInfo | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func validModelPayload() map[string]any {
	return map[string]any{
		"id": "id", "model": "model", "upgrade": nil, "upgradeInfo": nil,
		"availabilityNux": nil, "displayName": "name", "description": "desc",
		"hidden": false, "supportedReasoningEfforts": []any{},
		"defaultReasoningEffort": "low", "inputModalities": []any{"text"},
		"supportsPersonality": false, "additionalSpeedTiers": []any{},
		"serviceTiers": []any{}, "defaultServiceTier": nil, "isDefault": false,
	}
}

func assertModelDecodeFails(t *testing.T, payload map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal fixture: %v", err)
	}
	var value Model
	if err := json.Unmarshal(encoded, &value); err == nil {
		t.Fatalf("Model accepted %s", encoded)
	}
}

func assertModelArraySchema(t *testing.T, raw any, wantItems Schema) {
	t.Helper()
	schema, ok := raw.(Schema)
	if !ok || schema["type"] != "array" || !reflect.DeepEqual(schema["items"], wantItems) {
		t.Fatalf("array schema = %#v, want items %#v", raw, wantItems)
	}
}

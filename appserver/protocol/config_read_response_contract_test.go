package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestConfigReadResponseSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	want := Schema{
		"type": "object",
		"properties": Schema{
			"config": Schema{"$ref": "#/$defs/Config"},
			"origins": Schema{
				"type":                             "object",
				"additionalProperties":             Schema{"$ref": "#/$defs/ConfigLayerMetadata"},
				"x-gollem-typescript-optional-map": true,
			},
			"layers": nullableConfigPrerequisiteSchema(Schema{
				"type":  "array",
				"items": Schema{"$ref": "#/$defs/ConfigLayer"},
			}),
		},
		"required":             []string{"config", "origins", "layers"},
		"additionalProperties": true,
		"x-gollem-typescript-ignore-additional-properties": true,
	}
	if got := defs["ConfigReadResponse"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ConfigReadResponse schema = %#v, want %#v", got, want)
	}
}

func TestConfigReadResponseCanonicalizesOmittedLayers(t *testing.T) {
	for _, input := range []string{
		`{"config":{},"origins":{}}`,
		`{"config":{},"origins":{},"layers":null}`,
		`{"config":{},"origins":{},"future":{"ignored":true}}`,
		`{"config":{},"origins":{},"future":1,"future":2}`,
	} {
		var value ConfigReadResponse
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Fatalf("unmarshal ConfigReadResponse %s: %v", input, err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal ConfigReadResponse %s: %v", input, err)
		}
		want := `{"config":` + emptyPublicConfigJSON() + `,"origins":{},"layers":null}`
		if got := string(encoded); got != want {
			t.Fatalf("ConfigReadResponse %s = %s, want %s", input, got, want)
		}
	}
}

func TestConfigReadResponsePreservesNestedValuesAndOrder(t *testing.T) {
	input := `{
		"config":{"model":"gpt-5","future":{"integer":9007199254740993}},
		"origins":{
			"":{"name":{"type":"sessionFlags"},"version":""},
			"model":{"name":{"type":"user","file":"/home/user/../user/.codex/config.toml"},"version":"v1"}
		},
		"layers":[
			{"name":{"type":"project","dotCodexFolder":"/workspace/../workspace/.codex"},"version":"v2","config":{"integer":9007199254740993}},
			{"name":{"type":"sessionFlags"},"version":"v3","config":null,"disabledReason":"policy"},
			{"name":{"type":"sessionFlags"},"version":"v3","config":null,"disabledReason":"policy"}
		],
		"future":true
	}`
	var value ConfigReadResponse
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("unmarshal full ConfigReadResponse: %v", err)
	}
	if got, want := len(value.Origins), 2; got != want {
		t.Fatalf("origins = %d, want %d", got, want)
	}
	if got, want := len(value.Layers), 3; got != want {
		t.Fatalf("layers = %d, want %d", got, want)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal full ConfigReadResponse: %v", err)
	}
	if strings.Contains(string(encoded), `"future":true`) {
		t.Fatalf("unknown response field survived canonical output: %s", encoded)
	}
	for _, want := range []string{
		`"future":{"integer":9007199254740993}`,
		`"file":"/home/user/.codex/config.toml","profile":null`,
		`"dotCodexFolder":"/workspace/.codex"`,
		`"integer":9007199254740993`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("canonical response missing %s: %s", want, encoded)
		}
	}
	if got := strings.Count(string(encoded), `"version":"v3"`); got != 2 {
		t.Errorf("duplicate ordered layers = %d, want 2: %s", got, encoded)
	}

	var roundTripped ConfigReadResponse
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("unmarshal canonical ConfigReadResponse: %v", err)
	}
	reencoded, err := json.Marshal(roundTripped)
	if err != nil {
		t.Fatalf("remarshal canonical ConfigReadResponse: %v", err)
	}
	if string(reencoded) != string(encoded) {
		t.Fatalf("canonical response changed:\nfirst: %s\nsecond: %s", encoded, reencoded)
	}
}

func TestConfigReadResponseOriginsUseLastDuplicateValue(t *testing.T) {
	input := `{"config":{},"origins":{"model":{"name":{"type":"sessionFlags"},"version":"v1"},"model":{"name":{"type":"sessionFlags"},"version":"v2"}}}`
	var value ConfigReadResponse
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("unmarshal duplicate origin: %v", err)
	}
	if got := value.Origins["model"].Version; got != "v2" {
		t.Fatalf("duplicate origin version = %q, want v2", got)
	}
}

func TestConfigReadResponseRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`, `{} {}`, `{} x`,
		`{"origins":{}}`,
		`{"config":null,"origins":{}}`,
		`{"config":[],"origins":{}}`,
		`{"config":{"model":1},"origins":{}}`,
		`{"config":{}}`,
		`{"config":{},"origins":null}`,
		`{"config":{},"origins":[]}`,
		`{"config":{},"origins":{"model":null}}`,
		`{"config":{},"origins":{"model":{}}}`,
		`{"config":{},"origins":{"model":{"name":{"type":"sessionFlags"}}}}`,
		`{"config":{},"origins":{"model":{"name":{"type":"sessionFlags"},"version":"v1","extra":true}}}`,
		`{"config":{},"origins":{},"layers":{}}`,
		`{"config":{},"origins":{},"layers":[null]}`,
		`{"config":{},"origins":{},"layers":[{}]}`,
		`{"config":{},"origins":{},"layers":[{"name":{"type":"sessionFlags"},"version":"v1","config":null,"extra":true}]}`,
		`{"config":{},"origins":{},"layers":[{"name":{"type":"sessionFlags"},"version":"v1","config":{"bad":}}]}`,
		`{"config":{},"config":{},"origins":{}}`,
		`{"config":{},"origins":{},"origins":{}}`,
		`{"config":{},"origins":{},"layers":null,"layers":[]}`,
	} {
		assertJSONRejects[ConfigReadResponse](t, input)
	}
}

func TestConfigReadResponseDirectDecoderRejectsMalformedTokenStreams(t *testing.T) {
	for _, input := range []string{
		``, `{@`, `{"config"`, `{"config":`, `{"future":1`, `{} {}`, `{} x`,
	} {
		var value ConfigReadResponse
		if err := value.UnmarshalJSON([]byte(input)); err == nil {
			t.Errorf("direct ConfigReadResponse decoder accepted %q", input)
		}
	}
}

func TestConfigReadResponseMarshalRejectsInvalidConstructedValues(t *testing.T) {
	var valid ConfigReadResponse
	if err := json.Unmarshal([]byte(`{"config":{},"origins":{"model":{"name":{"type":"sessionFlags"},"version":"v1"}},"layers":[{"name":{"type":"sessionFlags"},"version":"v1","config":null}]}`), &valid); err != nil {
		t.Fatalf("decode valid response: %v", err)
	}
	invalidConfig := valid
	invalidConfig.Config = Config{Additional: map[string]JsonValue{"future": {}}}
	invalidOrigin := valid
	invalidOrigin.Origins = map[string]ConfigLayerMetadata{"model": {}}
	invalidLayer := valid
	invalidLayer.Layers[0].Config = JsonValue{}
	for name, value := range map[string]ConfigReadResponse{
		"nil origins":    {},
		"invalid config": invalidConfig,
		"invalid origin": invalidOrigin,
		"invalid layer":  invalidLayer,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := json.Marshal(value); err == nil {
				t.Fatal("invalid ConfigReadResponse marshaled")
			}
		})
	}
}

func TestConfigReadResponseNilReceiver(t *testing.T) {
	var value *ConfigReadResponse
	if err := value.UnmarshalJSON([]byte(`{"config":{},"origins":{}}`)); err == nil {
		t.Fatal("nil ConfigReadResponse receiver succeeded")
	}
}

func TestConfigReadResponseRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ConfigReadResponse") || slices.Contains(binding.Result, "ConfigReadResponse") {
			t.Fatalf("ConfigReadResponse unexpectedly bound to %s", binding.Method)
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

func TestConfigReadResponseTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type ConfigReadResponse = {`,
		`"config": Config;`,
		`"origins": { [key in string]?: ConfigLayerMetadata };`,
		`"layers": Array<ConfigLayer> | null;`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = ConfigReadResponse{}
	_ json.Unmarshaler = (*ConfigReadResponse)(nil)
)

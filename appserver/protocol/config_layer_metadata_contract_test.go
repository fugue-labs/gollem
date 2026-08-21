package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestConfigLayerMetadataSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)

	metadata := defs["ConfigLayerMetadata"].(Schema)
	assertClosedObjectSchema(t, metadata, "name", "version")
	metadataProperties := metadata["properties"].(Schema)
	assertConfigLayerMetadataSchemaProperty(t, metadataProperties, "name", "ConfigLayerSource")
	if !reflect.DeepEqual(metadataProperties["version"], Schema{"type": "string"}) {
		t.Errorf("ConfigLayerMetadata version schema = %#v", metadataProperties["version"])
	}

	layer := defs["ConfigLayer"].(Schema)
	assertClosedObjectSchema(t, layer, "name", "version", "config", "disabledReason")
	layerProperties := layer["properties"].(Schema)
	assertConfigLayerMetadataSchemaProperty(t, layerProperties, "name", "ConfigLayerSource")
	if !reflect.DeepEqual(layerProperties["version"], Schema{"type": "string"}) {
		t.Errorf("ConfigLayer version schema = %#v", layerProperties["version"])
	}
	assertConfigLayerMetadataSchemaProperty(t, layerProperties, "config", "JsonValue")
	assertConfigNullableSchema(t, layerProperties["disabledReason"], Schema{"type": "string"})

	overridden := defs["OverriddenMetadata"].(Schema)
	assertClosedObjectSchema(t, overridden, "message", "overridingLayer", "effectiveValue")
	overriddenProperties := overridden["properties"].(Schema)
	if !reflect.DeepEqual(overriddenProperties["message"], Schema{"type": "string"}) {
		t.Errorf("OverriddenMetadata message schema = %#v", overriddenProperties["message"])
	}
	assertConfigLayerMetadataSchemaProperty(t, overriddenProperties, "overridingLayer", "ConfigLayerMetadata")
	assertConfigLayerMetadataSchemaProperty(t, overriddenProperties, "effectiveValue", "JsonValue")
}

func TestConfigLayerMetadataAcceptsExactWireForms(t *testing.T) {
	for _, test := range []struct {
		name      string
		input     string
		canonical string
	}{
		{
			"session flags",
			`{"name":{"type":"sessionFlags"},"version":""}`,
			`{"name":{"type":"sessionFlags"},"version":""}`,
		},
		{
			"user source omitted profile",
			`{"name":{"type":"user","file":"/home/user/../user/.codex/config.toml"},"version":"v1"}`,
			`{"name":{"type":"user","file":"/home/user/.codex/config.toml","profile":null},"version":"v1"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value ConfigLayerMetadata
			assertConfigLayerMetadataRoundTrip(t, test.input, test.canonical, &value)
		})
	}
}

func TestConfigLayerAcceptsExactWireForms(t *testing.T) {
	for _, test := range []struct {
		name      string
		input     string
		canonical string
	}{
		{
			"null config and omitted reason",
			`{"name":{"type":"sessionFlags"},"version":"v1","config":null}`,
			`{"name":{"type":"sessionFlags"},"version":"v1","config":null,"disabledReason":null}`,
		},
		{
			"precision-preserving config and null reason",
			`{"name":{"type":"project","dotCodexFolder":"/workspace/../workspace/.codex"},"version":"v2","config":{"integer":9007199254740993,"decimal":1.234567890123456789,"items":[false,null,"value"]},"disabledReason":null}`,
			`{"name":{"type":"project","dotCodexFolder":"/workspace/.codex"},"version":"v2","config":{"decimal":1.234567890123456789,"integer":9007199254740993,"items":[false,null,"value"]},"disabledReason":null}`,
		},
		{
			"disabled reason",
			`{"name":{"type":"system","file":"/etc/codex/config.toml"},"version":"v3","config":[],"disabledReason":"policy"}`,
			`{"name":{"type":"system","file":"/etc/codex/config.toml"},"version":"v3","config":[],"disabledReason":"policy"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value ConfigLayer
			assertConfigLayerMetadataRoundTrip(t, test.input, test.canonical, &value)
		})
	}
}

func TestOverriddenMetadataAcceptsExactWireForms(t *testing.T) {
	for _, test := range []struct {
		name      string
		input     string
		canonical string
	}{
		{
			"null effective value",
			`{"message":"","overridingLayer":{"name":{"type":"legacyManagedConfigTomlFromMdm"},"version":"v1"},"effectiveValue":null}`,
			`{"message":"","overridingLayer":{"name":{"type":"legacyManagedConfigTomlFromMdm"},"version":"v1"},"effectiveValue":null}`,
		},
		{
			"object effective value",
			`{"message":"overridden","overridingLayer":{"name":{"type":"user","file":"/home/user/.codex/config.toml"},"version":"v2"},"effectiveValue":{"nested":[9007199254740993,true]}}`,
			`{"message":"overridden","overridingLayer":{"name":{"type":"user","file":"/home/user/.codex/config.toml","profile":null},"version":"v2"},"effectiveValue":{"nested":[9007199254740993,true]}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value OverriddenMetadata
			assertConfigLayerMetadataRoundTrip(t, test.input, test.canonical, &value)
		})
	}
}

func TestConfigLayerMetadataRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"version":"v1"}`,
		`{"name":null,"version":"v1"}`,
		`{"name":{"type":"unknown"},"version":"v1"}`,
		`{"name":{"type":"sessionFlags"}}`,
		`{"name":{"type":"sessionFlags"},"version":null}`,
		`{"name":{"type":"sessionFlags"},"version":1}`,
		`{"name":{"type":"sessionFlags"},"version":"v1","extra":true}`,
		`{"name":{"type":"sessionFlags"},"version":"v1"} {}`,
	} {
		assertJSONRejects[ConfigLayerMetadata](t, input)
	}
}

func TestConfigLayerRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"version":"v1","config":null}`,
		`{"name":null,"version":"v1","config":null}`,
		`{"name":{"type":"sessionFlags"},"config":null}`,
		`{"name":{"type":"sessionFlags"},"version":null,"config":null}`,
		`{"name":{"type":"sessionFlags"},"version":"v1"}`,
		`{"name":{"type":"sessionFlags"},"version":"v1","config":}`,
		`{"name":{"type":"sessionFlags"},"version":"v1","config":null,"disabledReason":1}`,
		`{"name":{"type":"sessionFlags"},"version":"v1","config":null,"extra":true}`,
		`{"name":{"type":"sessionFlags","extra":true},"version":"v1","config":null}`,
		`{"name":{"type":"sessionFlags"},"version":"v1","config":null} {}`,
	} {
		assertJSONRejects[ConfigLayer](t, input)
	}
}

func TestOverriddenMetadataRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"overridingLayer":{"name":{"type":"sessionFlags"},"version":"v1"},"effectiveValue":null}`,
		`{"message":null,"overridingLayer":{"name":{"type":"sessionFlags"},"version":"v1"},"effectiveValue":null}`,
		`{"message":"value","effectiveValue":null}`,
		`{"message":"value","overridingLayer":null,"effectiveValue":null}`,
		`{"message":"value","overridingLayer":{"name":{"type":"sessionFlags"}},"effectiveValue":null}`,
		`{"message":"value","overridingLayer":{"name":{"type":"sessionFlags"},"version":"v1"}}`,
		`{"message":"value","overridingLayer":{"name":{"type":"sessionFlags"},"version":"v1"},"effectiveValue":null,"extra":true}`,
		`{"message":"value","overridingLayer":{"name":{"type":"sessionFlags"},"version":"v1"},"effectiveValue":null} {}`,
	} {
		assertJSONRejects[OverriddenMetadata](t, input)
	}
}

func TestConfigLayerMetadataNilReceiversAndEmptyMarshal(t *testing.T) {
	var metadata *ConfigLayerMetadata
	if err := metadata.UnmarshalJSON([]byte(`{"name":{"type":"sessionFlags"},"version":"v1"}`)); err == nil {
		t.Fatal("nil ConfigLayerMetadata receiver succeeded")
	}
	var layer *ConfigLayer
	if err := layer.UnmarshalJSON([]byte(`{"name":{"type":"sessionFlags"},"version":"v1","config":null}`)); err == nil {
		t.Fatal("nil ConfigLayer receiver succeeded")
	}
	var overridden *OverriddenMetadata
	if err := overridden.UnmarshalJSON([]byte(`{"message":"","overridingLayer":{"name":{"type":"sessionFlags"},"version":"v1"},"effectiveValue":null}`)); err == nil {
		t.Fatal("nil OverriddenMetadata receiver succeeded")
	}
	if _, err := json.Marshal(ConfigLayerMetadata{}); err == nil {
		t.Fatal("empty ConfigLayerMetadata marshal succeeded")
	}
	if _, err := json.Marshal(ConfigLayer{}); err == nil {
		t.Fatal("empty ConfigLayer marshal succeeded")
	}
	if _, err := json.Marshal(OverriddenMetadata{}); err == nil {
		t.Fatal("empty OverriddenMetadata marshal succeeded")
	}

	var valid ConfigLayer
	if err := json.Unmarshal([]byte(`{"name":{"type":"sessionFlags"},"version":"v1","config":null}`), &valid); err != nil {
		t.Fatalf("decode valid ConfigLayer: %v", err)
	}
	valid.Config = JsonValue{}
	if _, err := json.Marshal(valid); err == nil {
		t.Fatal("ConfigLayer with empty config marshal succeeded")
	}

	var validOverridden OverriddenMetadata
	if err := json.Unmarshal([]byte(`{"message":"","overridingLayer":{"name":{"type":"sessionFlags"},"version":"v1"},"effectiveValue":null}`), &validOverridden); err != nil {
		t.Fatalf("decode valid OverriddenMetadata: %v", err)
	}
	validOverridden.EffectiveValue = JsonValue{}
	if _, err := json.Marshal(validOverridden); err == nil {
		t.Fatal("OverriddenMetadata with empty effective value marshal succeeded")
	}
}

func TestConfigLayerMetadataContractsRemainStandalone(t *testing.T) {
	names := []string{"ConfigLayerMetadata", "ConfigLayer", "OverriddenMetadata"}
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

func TestConfigLayerMetadataTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ConfigLayerMetadata = {`,
		`"name": ConfigLayerSource;`,
		`"version": string;`,
		`export type ConfigLayer = {`,
		`"config": JsonValue;`,
		`"disabledReason": string | null;`,
		`export type OverriddenMetadata = {`,
		`"message": string;`,
		`"overridingLayer": ConfigLayerMetadata;`,
		`"effectiveValue": JsonValue;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertConfigLayerMetadataSchemaProperty(t *testing.T, properties Schema, fieldName, definitionName string) {
	t.Helper()
	want := Schema{"$ref": "#/$defs/" + definitionName}
	if !reflect.DeepEqual(properties[fieldName], want) {
		t.Errorf("%s schema = %#v, want %#v", fieldName, properties[fieldName], want)
	}
}

func assertConfigLayerMetadataRoundTrip(t *testing.T, input, want string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(input), target); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(target)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

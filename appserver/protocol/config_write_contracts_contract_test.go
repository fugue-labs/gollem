package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestConfigWriteContractSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)

	if got := defs["MergeStrategy"]; !reflect.DeepEqual(got, Schema{
		"type": "string", "enum": []any{"replace", "upsert"},
	}) {
		t.Fatalf("MergeStrategy schema = %#v", got)
	}
	if got := defs["WriteStatus"]; !reflect.DeepEqual(got, Schema{
		"type": "string", "enum": []any{"ok", "okOverridden"},
	}) {
		t.Fatalf("WriteStatus schema = %#v", got)
	}

	edit := defs["ConfigEdit"].(Schema)
	assertClosedObjectSchema(t, edit, "keyPath", "value", "mergeStrategy")
	editProperties := edit["properties"].(Schema)
	assertConfigWriteSchemaProperty(t, editProperties, "keyPath", Schema{"type": "string"})
	assertConfigWriteSchemaRef(t, editProperties, "value", "JsonValue")
	assertConfigWriteSchemaRef(t, editProperties, "mergeStrategy", "MergeStrategy")

	valueParams := defs["ConfigValueWriteParams"].(Schema)
	assertConfigWriteClosedSchemaRequired(
		t,
		valueParams,
		[]string{"keyPath", "value", "mergeStrategy"},
		"keyPath", "value", "mergeStrategy", "filePath", "expectedVersion",
	)
	valueProperties := valueParams["properties"].(Schema)
	assertConfigWriteSchemaProperty(t, valueProperties, "keyPath", Schema{"type": "string"})
	assertConfigWriteSchemaRef(t, valueProperties, "value", "JsonValue")
	assertConfigWriteSchemaRef(t, valueProperties, "mergeStrategy", "MergeStrategy")
	assertConfigNullableSchema(t, valueProperties["filePath"], Schema{"type": "string"})
	assertConfigWriteSchemaDescription(t, valueProperties, "filePath", configWriteFilePathDescription)
	assertConfigNullableSchema(t, valueProperties["expectedVersion"], Schema{"type": "string"})

	batchParams := defs["ConfigBatchWriteParams"].(Schema)
	assertConfigWriteClosedSchemaRequired(
		t,
		batchParams,
		[]string{"edits"},
		"edits", "filePath", "expectedVersion", "reloadUserConfig",
	)
	batchProperties := batchParams["properties"].(Schema)
	if got := batchProperties["edits"]; !reflect.DeepEqual(got, Schema{
		"type": "array", "items": Schema{"$ref": "#/$defs/ConfigEdit"},
	}) {
		t.Fatalf("ConfigBatchWriteParams edits schema = %#v", got)
	}
	assertConfigNullableSchema(t, batchProperties["filePath"], Schema{"type": "string"})
	assertConfigWriteSchemaDescription(t, batchProperties, "filePath", configWriteFilePathDescription)
	assertConfigNullableSchema(t, batchProperties["expectedVersion"], Schema{"type": "string"})
	assertConfigWriteSchemaProperty(t, batchProperties, "reloadUserConfig", Schema{
		"type":        "boolean",
		"description": "When true, hot-reload the updated user config into all loaded threads after writing.",
	})

	response := defs["ConfigWriteResponse"].(Schema)
	assertClosedObjectSchema(t, response, "status", "version", "filePath", "overriddenMetadata")
	responseProperties := response["properties"].(Schema)
	assertConfigWriteSchemaRef(t, responseProperties, "status", "WriteStatus")
	assertConfigWriteSchemaProperty(t, responseProperties, "version", Schema{"type": "string"})
	assertConfigWriteSchemaProperty(t, responseProperties, "filePath", Schema{
		"$ref":        "#/$defs/AbsolutePathBuf",
		"description": "Canonical path to the config file that was written.",
	})
	assertConfigNullableSchema(
		t,
		responseProperties["overriddenMetadata"],
		Schema{"$ref": "#/$defs/OverriddenMetadata"},
	)
}

func TestConfigWriteContractEnumsAcceptExactValues(t *testing.T) {
	for _, test := range []struct {
		input  string
		target any
	}{
		{`"replace"`, new(MergeStrategy)},
		{`"upsert"`, new(MergeStrategy)},
		{`"ok"`, new(WriteStatus)},
		{`"okOverridden"`, new(WriteStatus)},
	} {
		if err := json.Unmarshal([]byte(test.input), test.target); err != nil {
			t.Errorf("Unmarshal(%s): %v", test.input, err)
			continue
		}
		encoded, err := json.Marshal(test.target)
		if err != nil || string(encoded) != test.input {
			t.Errorf("round trip %s = %s, %v", test.input, encoded, err)
		}
	}
}

func TestConfigEditAcceptsExactWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{
			`{"keyPath":"","value":null,"mergeStrategy":"replace"}`,
			`{"keyPath":"","value":null,"mergeStrategy":"replace"}`,
		},
		{
			`{"keyPath":"model.reasoning","value":{"integer":9007199254740993,"decimal":1.234567890123456789,"items":[false,null,"value"]},"mergeStrategy":"upsert"}`,
			`{"keyPath":"model.reasoning","value":{"decimal":1.234567890123456789,"integer":9007199254740993,"items":[false,null,"value"]},"mergeStrategy":"upsert"}`,
		},
	} {
		var value ConfigEdit
		assertConfigWriteRoundTrip(t, test.input, test.want, &value)
	}
}

func TestConfigValueWriteParamsAcceptExactWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{
			`{"keyPath":"","value":null,"mergeStrategy":"replace"}`,
			`{"keyPath":"","value":null,"mergeStrategy":"replace","filePath":null,"expectedVersion":null}`,
		},
		{
			`{"keyPath":"model","value":[],"mergeStrategy":"upsert","filePath":null,"expectedVersion":null}`,
			`{"keyPath":"model","value":[],"mergeStrategy":"upsert","filePath":null,"expectedVersion":null}`,
		},
		{
			`{"keyPath":"model","value":{"nested":[9007199254740993,true]},"mergeStrategy":"replace","filePath":"relative/config.toml","expectedVersion":""}`,
			`{"keyPath":"model","value":{"nested":[9007199254740993,true]},"mergeStrategy":"replace","filePath":"relative/config.toml","expectedVersion":""}`,
		},
	} {
		var value ConfigValueWriteParams
		assertConfigWriteRoundTrip(t, test.input, test.want, &value)
	}
}

func TestConfigBatchWriteParamsAcceptExactWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{
			`{"edits":[]}`,
			`{"edits":[],"filePath":null,"expectedVersion":null}`,
		},
		{
			`{"edits":[],"filePath":null,"expectedVersion":null,"reloadUserConfig":false}`,
			`{"edits":[],"filePath":null,"expectedVersion":null}`,
		},
		{
			`{"edits":[{"keyPath":"first","value":1,"mergeStrategy":"replace"},{"keyPath":"first","value":{"large":9007199254740993},"mergeStrategy":"upsert"}],"filePath":"config.toml","expectedVersion":"v1","reloadUserConfig":true}`,
			`{"edits":[{"keyPath":"first","value":1,"mergeStrategy":"replace"},{"keyPath":"first","value":{"large":9007199254740993},"mergeStrategy":"upsert"}],"filePath":"config.toml","expectedVersion":"v1","reloadUserConfig":true}`,
		},
	} {
		var value ConfigBatchWriteParams
		assertConfigWriteRoundTrip(t, test.input, test.want, &value)
	}
}

func TestConfigWriteResponseAcceptsExactWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{
			`{"status":"ok","version":"","filePath":"/workspace/../workspace/config.toml"}`,
			`{"status":"ok","version":"","filePath":"/workspace/config.toml","overriddenMetadata":null}`,
		},
		{
			`{"status":"okOverridden","version":"v2","filePath":"/workspace/config.toml","overriddenMetadata":null}`,
			`{"status":"okOverridden","version":"v2","filePath":"/workspace/config.toml","overriddenMetadata":null}`,
		},
		{
			`{"status":"okOverridden","version":"v3","filePath":"/workspace/config.toml","overriddenMetadata":{"message":"overridden","overridingLayer":{"name":{"type":"user","file":"/home/user/.codex/config.toml"},"version":"managed"},"effectiveValue":{"large":9007199254740993}}}`,
			`{"status":"okOverridden","version":"v3","filePath":"/workspace/config.toml","overriddenMetadata":{"message":"overridden","overridingLayer":{"name":{"type":"user","file":"/home/user/.codex/config.toml","profile":null},"version":"managed"},"effectiveValue":{"large":9007199254740993}}}`,
		},
	} {
		var value ConfigWriteResponse
		assertConfigWriteRoundTrip(t, test.input, test.want, &value)
	}
}

func TestConfigWriteContractEnumsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{`null`, `[]`, `{}`, `1`, `true`, `""`, `"merge"`, `"Replace"`, `"ok_overridden"`} {
		assertJSONRejects[MergeStrategy](t, input)
		assertJSONRejects[WriteStatus](t, input)
	}
}

func TestConfigEditRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"value":null,"mergeStrategy":"replace"}`,
		`{"keyPath":null,"value":null,"mergeStrategy":"replace"}`,
		`{"keyPath":"key","mergeStrategy":"replace"}`,
		`{"keyPath":"key","value":,"mergeStrategy":"replace"}`,
		`{"keyPath":"key","value":null}`,
		`{"keyPath":"key","value":null,"mergeStrategy":null}`,
		`{"keyPath":"key","value":null,"mergeStrategy":"merge"}`,
		`{"keyPath":"key","value":null,"mergeStrategy":"replace","extra":true}`,
		`{"keyPath":"key","value":null,"mergeStrategy":"replace"} {}`,
	} {
		assertJSONRejects[ConfigEdit](t, input)
	}
}

func TestConfigValueWriteParamsRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"value":null,"mergeStrategy":"replace"}`,
		`{"keyPath":null,"value":null,"mergeStrategy":"replace"}`,
		`{"keyPath":"key","mergeStrategy":"replace"}`,
		`{"keyPath":"key","value":null}`,
		`{"keyPath":"key","value":null,"mergeStrategy":"merge"}`,
		`{"keyPath":"key","value":null,"mergeStrategy":"replace","filePath":1}`,
		`{"keyPath":"key","value":null,"mergeStrategy":"replace","expectedVersion":false}`,
		`{"key":"key","value":null,"mergeStrategy":"replace"}`,
		`{"keyPath":"key","value":null,"merge_strategy":"replace"}`,
		`{"keyPath":"key","value":null,"mergeStrategy":"replace","extra":true}`,
		`{"keyPath":"key","value":null,"mergeStrategy":"replace"} {}`,
	} {
		assertJSONRejects[ConfigValueWriteParams](t, input)
	}
}

func TestConfigBatchWriteParamsRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"edits":null}`,
		`{"edits":{}}`,
		`{"edits":[null]}`,
		`{"edits":[{}]}`,
		`{"edits":[{"keyPath":"key","value":null,"mergeStrategy":"merge"}]}`,
		`{"edits":[],"filePath":1}`,
		`{"edits":[],"expectedVersion":false}`,
		`{"edits":[],"reloadUserConfig":null}`,
		`{"edits":[],"reloadUserConfig":1}`,
		`{"entries":[]}`,
		`{"values":{}}`,
		`{"edits":[],"reload_user_config":true}`,
		`{"edits":[],"extra":true}`,
		`{"edits":[]} {}`,
	} {
		assertJSONRejects[ConfigBatchWriteParams](t, input)
	}
}

func TestConfigWriteResponseRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"version":"v1","filePath":"/config.toml"}`,
		`{"status":null,"version":"v1","filePath":"/config.toml"}`,
		`{"status":"accepted","version":"v1","filePath":"/config.toml"}`,
		`{"status":"ok","filePath":"/config.toml"}`,
		`{"status":"ok","version":null,"filePath":"/config.toml"}`,
		`{"status":"ok","version":"v1"}`,
		`{"status":"ok","version":"v1","filePath":null}`,
		`{"status":"ok","version":"v1","filePath":"relative/config.toml"}`,
		`{"status":"ok","version":"v1","filePath":"/config.toml","overriddenMetadata":1}`,
		`{"status":"ok","version":"v1","filePath":"/config.toml","overriddenMetadata":{"message":"value","overridingLayer":{"name":{"type":"sessionFlags"},"version":"v1"}}}`,
		`{"status":"ok","version":"v1","filePath":"/config.toml","path":"/other"}`,
		`{"status":"ok","version":"v1","filePath":"/config.toml","extra":true}`,
		`{"status":"ok","version":"v1","filePath":"/config.toml"} {}`,
	} {
		assertJSONRejects[ConfigWriteResponse](t, input)
	}
}

func TestConfigWriteContractNilReceiversAndInvalidMarshal(t *testing.T) {
	var strategy *MergeStrategy
	if err := strategy.UnmarshalJSON([]byte(`"replace"`)); err == nil {
		t.Fatal("nil MergeStrategy receiver succeeded")
	}
	var status *WriteStatus
	if err := status.UnmarshalJSON([]byte(`"ok"`)); err == nil {
		t.Fatal("nil WriteStatus receiver succeeded")
	}
	var edit *ConfigEdit
	if err := edit.UnmarshalJSON([]byte(`{"keyPath":"key","value":null,"mergeStrategy":"replace"}`)); err == nil {
		t.Fatal("nil ConfigEdit receiver succeeded")
	}
	var valueParams *ConfigValueWriteParams
	if err := valueParams.UnmarshalJSON([]byte(`{"keyPath":"key","value":null,"mergeStrategy":"replace"}`)); err == nil {
		t.Fatal("nil ConfigValueWriteParams receiver succeeded")
	}
	var batchParams *ConfigBatchWriteParams
	if err := batchParams.UnmarshalJSON([]byte(`{"edits":[]}`)); err == nil {
		t.Fatal("nil ConfigBatchWriteParams receiver succeeded")
	}
	var response *ConfigWriteResponse
	if err := response.UnmarshalJSON([]byte(`{"status":"ok","version":"v1","filePath":"/config.toml"}`)); err == nil {
		t.Fatal("nil ConfigWriteResponse receiver succeeded")
	}

	for name, value := range map[string]any{
		"strategy":     MergeStrategy("merge"),
		"status":       WriteStatus("accepted"),
		"edit":         ConfigEdit{},
		"value params": ConfigValueWriteParams{},
		"batch params": ConfigBatchWriteParams{},
		"response":     ConfigWriteResponse{},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("empty/invalid %s marshal succeeded", name)
		}
	}
}

func TestConfigWriteContractsRemainStandalone(t *testing.T) {
	names := []string{
		"MergeStrategy",
		"WriteStatus",
		"ConfigEdit",
		"ConfigValueWriteParams",
		"ConfigBatchWriteParams",
		"ConfigWriteResponse",
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

func TestConfigWriteContractTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type MergeStrategy = "replace" | "upsert";`,
		`export type WriteStatus = "ok" | "okOverridden";`,
		`export type ConfigEdit = {`,
		`"mergeStrategy": MergeStrategy;`,
		`export type ConfigValueWriteParams = {`,
		`"filePath"?: string | null;`,
		`"expectedVersion"?: string | null;`,
		`export type ConfigBatchWriteParams = {`,
		`"edits": Array<ConfigEdit>;`,
		`"reloadUserConfig"?: boolean;`,
		`export type ConfigWriteResponse = {`,
		`"status": WriteStatus;`,
		`"filePath": AbsolutePathBuf;`,
		`"overriddenMetadata": OverriddenMetadata | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertConfigWriteClosedSchemaRequired(
	t *testing.T,
	schema Schema,
	required []string,
	properties ...string,
) {
	t.Helper()
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("schema is not a closed object: %#v", schema)
	}
	if got := schemaRequiredNames(schema); !slices.Equal(got, required) {
		t.Fatalf("schema required = %v, want %v", got, required)
	}
	gotProperties := schema["properties"].(Schema)
	if len(gotProperties) != len(properties) {
		t.Fatalf("schema properties = %v, want %v", gotProperties, properties)
	}
	for _, name := range properties {
		if _, ok := gotProperties[name]; !ok {
			t.Errorf("schema missing property %q", name)
		}
	}
}

func assertConfigWriteSchemaProperty(t *testing.T, properties Schema, fieldName string, want Schema) {
	t.Helper()
	if got := properties[fieldName]; !reflect.DeepEqual(got, want) {
		t.Errorf("%s schema = %#v, want %#v", fieldName, got, want)
	}
}

func assertConfigWriteSchemaRef(t *testing.T, properties Schema, fieldName, definitionName string) {
	t.Helper()
	assertConfigWriteSchemaProperty(t, properties, fieldName, Schema{"$ref": "#/$defs/" + definitionName})
}

const configWriteFilePathDescription = "Path to the config file to write; defaults to the user's `config.toml` when omitted."

func assertConfigWriteSchemaDescription(t *testing.T, properties Schema, fieldName, want string) {
	t.Helper()
	field, ok := properties[fieldName].(Schema)
	if !ok || field["description"] != want {
		t.Errorf("%s description = %#v, want %q", fieldName, field["description"], want)
	}
}

func assertConfigWriteRoundTrip(t *testing.T, input, want string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(input), target); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(target)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}

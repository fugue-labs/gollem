package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestExternalAgentConfigImportHistoryRecordSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range map[string]Schema{
		"ExternalAgentConfigImportHistoryRecordSuccessParams":    externalAgentConfigImportHistoryRecordSuccessParamsSchema(),
		"ExternalAgentConfigImportHistoryRecordTypeResultParams": externalAgentConfigImportHistoryRecordTypeResultParamsSchema(),
		"ExternalAgentConfigImportHistoryRecordParams":           externalAgentConfigImportHistoryRecordParamsSchema(),
	} {
		if got := defs[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestExternalAgentConfigImportHistoryRecordSuccessPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"itemType":"CONFIG","future":true}`, `{"itemType":"CONFIG","cwd":null,"source":null,"target":null,"title":null}`},
		{`{"itemType":"SESSIONS","cwd":"repo/../repo","source":" Claude Code ","target":"","title":" Original title "}`, `{"itemType":"SESSIONS","cwd":"repo/../repo","source":" Claude Code ","target":"","title":" Original title "}`},
	} {
		var value ExternalAgentConfigImportHistoryRecordSuccessParams
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.input, err)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Fatalf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestExternalAgentConfigImportHistoryRecordTypeResultPreservesSerdeWireForms(t *testing.T) {
	input := `{"itemType":"HOOKS","successes":[{"itemType":"CONFIG"},{"itemType":"SESSIONS","title":" old "}],"failures":[{"itemType":"HOOKS","failureStage":"","message":""}],"future":true}`
	want := `{"itemType":"HOOKS","successes":[{"itemType":"CONFIG","cwd":null,"source":null,"target":null,"title":null},{"itemType":"SESSIONS","cwd":null,"source":null,"target":null,"title":" old "}],"failures":[{"itemType":"HOOKS","errorType":null,"failureStage":"","message":"","cwd":null,"source":null}]}`
	var value ExternalAgentConfigImportHistoryRecordTypeResultParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip = %s, %v; want %s", encoded, err, want)
	}
	if encoded, err := json.Marshal(ExternalAgentConfigImportHistoryRecordTypeResultParams{ItemType: ExternalAgentConfigMigrationItemTypeConfig}); err != nil || string(encoded) != `{"itemType":"CONFIG","successes":[],"failures":[]}` {
		t.Fatalf("nil arrays = %s, %v", encoded, err)
	}
}

func TestExternalAgentConfigImportHistoryRecordParamsPreserveSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"providerId":" provider ","itemTypeResults":[],"future":true}`, `{"providerId":" provider ","itemTypeResults":[]}`},
		{`{"providerId":"provider","itemTypeResults":[{"itemType":"PLUGINS","successes":[],"failures":[]}]}`, `{"providerId":"provider","itemTypeResults":[{"itemType":"PLUGINS","successes":[],"failures":[]}]}`},
	} {
		var value ExternalAgentConfigImportHistoryRecordParams
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.input, err)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Fatalf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
	if encoded, err := json.Marshal(ExternalAgentConfigImportHistoryRecordParams{ProviderID: "provider"}); err != nil || string(encoded) != `{"providerId":"provider","itemTypeResults":[]}` {
		t.Fatalf("nil result array = %s, %v", encoded, err)
	}
}

func TestExternalAgentConfigImportHistoryRecordRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"itemType":null}`, `{"itemType":"OTHER"}`, `{"itemType":"CONFIG","title":1}`,
		`{"itemType":"CONFIG","cwd":null,"cwd":"cwd"}`, `{"itemType":"CONFIG","title":null,"title":"title"}`,
	} {
		assertJSONRejects[ExternalAgentConfigImportHistoryRecordSuccessParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"itemType":"CONFIG","successes":[],"failures":null}`,
		`{"itemType":"CONFIG","successes":null,"failures":[]}`,
		`{"itemType":"CONFIG","successes":[null],"failures":[]}`,
		`{"itemType":"CONFIG","successes":[{"itemType":"OTHER"}],"failures":[]}`,
		`{"itemType":"CONFIG","successes":[],"failures":[null]}`,
		`{"itemType":"CONFIG","successes":[],"failures":[],"failures":[]}`,
	} {
		assertJSONRejects[ExternalAgentConfigImportHistoryRecordTypeResultParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"providerId":null,"itemTypeResults":[]}`, `{"providerId":"provider","itemTypeResults":null}`,
		`{"providerId":"provider","itemTypeResults":[null]}`,
		`{"providerId":"provider","itemTypeResults":[{"itemType":"CONFIG","successes":[],"failures":[]}],"providerId":"other"}`,
		`{"providerId":"provider","itemTypeResults":[]} {}`,
	} {
		assertJSONRejects[ExternalAgentConfigImportHistoryRecordParams](t, input)
	}
}

func TestExternalAgentConfigImportHistoryRecordRemainsStandalone(t *testing.T) {
	var success *ExternalAgentConfigImportHistoryRecordSuccessParams
	if err := success.UnmarshalJSON([]byte(`{"itemType":"CONFIG"}`)); err == nil {
		t.Fatal("nil success receiver succeeded")
	}
	var result *ExternalAgentConfigImportHistoryRecordTypeResultParams
	if err := result.UnmarshalJSON([]byte(`{"itemType":"CONFIG","successes":[],"failures":[]}`)); err == nil {
		t.Fatal("nil type-result receiver succeeded")
	}
	var params *ExternalAgentConfigImportHistoryRecordParams
	if err := params.UnmarshalJSON([]byte(`{"providerId":"provider","itemTypeResults":[]}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			switch name {
			case "ExternalAgentConfigImportHistoryRecordSuccessParams", "ExternalAgentConfigImportHistoryRecordTypeResultParams", "ExternalAgentConfigImportHistoryRecordParams":
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestExternalAgentConfigImportHistoryRecordTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type ExternalAgentConfigImportHistoryRecordSuccessParams = { itemType: ExternalAgentConfigMigrationItemType, cwd: string | null, source: string | null, target: string | null,
/**
 * Original title for an imported session, when available.
 */
title?: string | null, };`,
		`export type ExternalAgentConfigImportHistoryRecordTypeResultParams = { itemType: ExternalAgentConfigMigrationItemType, successes: Array<ExternalAgentConfigImportHistoryRecordSuccessParams>, failures: Array<ExternalAgentConfigImportItemTypeFailure>, };`,
		`export type ExternalAgentConfigImportHistoryRecordParams = {
/**
 * Opaque provider identifier for the externally completed import.
 */
providerId: string,
/**
 * Completed results grouped by imported item type.
 */
itemTypeResults: Array<ExternalAgentConfigImportHistoryRecordTypeResultParams>, };`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

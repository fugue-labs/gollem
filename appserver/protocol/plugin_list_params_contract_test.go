package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPluginListParamsSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range map[string]Schema{
		"PluginListMarketplaceKind": pluginListMarketplaceKindSchema(),
		"PluginListParams":          pluginListParamSchema(),
	} {
		if got := defs[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestPluginListMarketplaceKindIsClosed(t *testing.T) {
	for _, kind := range []PluginListMarketplaceKind{
		PluginListMarketplaceKindLocal,
		PluginListMarketplaceKindVertical,
		PluginListMarketplaceKindWorkspaceDirectory,
		PluginListMarketplaceKindSharedWithMe,
		PluginListMarketplaceKindCreatedByMeRemote,
	} {
		encoded, err := json.Marshal(kind)
		if err != nil {
			t.Fatalf("Marshal(%q): %v", kind, err)
		}
		var roundTrip PluginListMarketplaceKind
		if err := json.Unmarshal(encoded, &roundTrip); err != nil || roundTrip != kind {
			t.Fatalf("round trip = %q, %v; want %q", roundTrip, err, kind)
		}
	}
	for _, input := range []string{`null`, `""`, `"remote"`, `1`, `{}`, `[]`, `"local" {}`} {
		var kind PluginListMarketplaceKind
		if err := json.Unmarshal([]byte(input), &kind); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	if _, err := json.Marshal(PluginListMarketplaceKind("remote")); err == nil {
		t.Fatal("invalid marketplace kind marshaled")
	}
	var kind *PluginListMarketplaceKind
	if err := kind.UnmarshalJSON([]byte(`"local"`)); err == nil {
		t.Fatal("nil marketplace-kind receiver succeeded")
	}
}

func TestPluginListParamsPreserveSerdeWireForms(t *testing.T) {
	assertPluginListRoundTrip(t, `{"future":true}`, `{"cwds":null,"marketplaceKinds":null}`)
	assertPluginListRoundTrip(t, `{"cwds":null,"marketplaceKinds":null,"forceRefetch":false}`, `{"cwds":null,"marketplaceKinds":null}`)
	assertPluginListRoundTrip(t, `{"cwds":["/repo"],"marketplaceKinds":["local","vertical","workspace-directory","shared-with-me","created-by-me-remote"],"forceRefetch":true,"future":true}`, `{"cwds":["/repo"],"marketplaceKinds":["local","vertical","workspace-directory","shared-with-me","created-by-me-remote"],"forceRefetch":true}`)
}

func TestPluginListParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"cwds":{}}`, `{"cwds":[null]}`, `{"cwds":["relative"]}`,
		`{"marketplaceKinds":{}}`, `{"marketplaceKinds":[null]}`, `{"marketplaceKinds":["remote"]}`, `{"marketplaceKinds":[1]}`,
		`{"forceRefetch":null}`, `{"forceRefetch":0}`,
		`{"cwds":null,"cwds":null}`, `{"forceRefetch":false,"forceRefetch":true}`,
		`{} {}`, `{} x`,
	} {
		assertJSONRejects[PluginListParams](t, input)
	}
}

func TestPluginListParamsRemainStandalone(t *testing.T) {
	var nilParams *PluginListParams
	if err := nilParams.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil PluginListParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "PluginListMarketplaceKind" || name == "PluginListParams" {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestPluginListParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type PluginListMarketplaceKind = "local" | "vertical" | "workspace-directory" | "shared-with-me" | "created-by-me-remote";`,
		`export type PluginListParams = {
/**
 * Optional working directories used to discover repo marketplaces. When omitted,
 * only home-scoped marketplaces and the official curated marketplace are considered.
 */
cwds?: Array<AbsolutePathBuf> | null,
/**
 * Optional marketplace kind filter. When omitted, only local marketplaces are queried, plus
 * the default remote catalog when enabled by feature flag.
 */
marketplaceKinds?: Array<PluginListMarketplaceKind> | null,
/**
 * Whether the client requests a fresh remote plugin catalog fetch.
 */
forceRefetch?: boolean, };`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertPluginListRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value PluginListParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}
